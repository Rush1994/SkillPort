package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rush1994/SkillPort/internal/testregistry"
)

func TestHTTPDirectoryRoundTrip(t *testing.T) {
	server := testregistry.New()
	server.Username = "fixture-user"
	server.Password = "fixture-password"
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	root := t.TempDir()
	cfg := filepath.Join(root, "cli.json")
	docker := filepath.Join(root, "docker.json")
	host := strings.TrimPrefix(httpServer.URL, "http://")
	dockerBytes, _ := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-password"))}}})
	if e := os.WriteFile(docker, dockerBytes, 0600); e != nil {
		t.Fatal(e)
	}
	invoke := func(want int, args ...string) Envelope {
		t.Helper()
		var out, diag bytes.Buffer
		args = append([]string{"--config", cfg, "--registry-config", docker, "--format", "json"}, args...)
		code := 0
		if binary := os.Getenv("SKILLPORT_TEST_BINARY"); binary != "" {
			cmd := exec.Command(binary, args...)
			cmd.Stdout = &out
			cmd.Stderr = &diag
			if err := cmd.Run(); err != nil {
				if status, ok := err.(*exec.ExitError); ok {
					code = status.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
		} else {
			code = Run(context.Background(), args, &out, &diag, BuildInfo{})
		}
		if code != want {
			t.Fatalf("%v: code %d want %d: %s %s", args, code, want, out.String(), diag.String())
		}
		var result Envelope
		if json.Unmarshal(out.Bytes(), &result) != nil {
			t.Fatalf("invalid JSON: %s", out.String())
		}
		if strings.Contains(out.String()+diag.String(), "fixture-password") {
			t.Fatal("credential leaked")
		}
		return result
	}
	invoke(0, "registry", "add", "test", "--endpoint", httpServer.URL, "--project", "skills")
	invoke(0, "registry", "use", "test")
	invoke(0, "doctor")
	dir := filepath.Join(root, "中文 空格", "hello-skill")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	md := []byte("---\r\nname: hello-skill\r\ndescription: Round trip fixture.\r\n---\r\n\r\nBody 中文\r\n")
	os.WriteFile(filepath.Join(dir, "SKILL.md"), md, 0600)
	os.WriteFile(filepath.Join(dir, "note.txt"), []byte("original\n"), 0600)
	invoke(0, "validate", "--dir", dir)
	invoke(0, "push", "--dir", dir, "--tag", "v1", "--dry-run")
	if server.Writes != 0 {
		t.Fatal("dry-run wrote remote content")
	}
	if _, e := os.Stat(filepath.Join(dir, ".skillport")); !os.IsNotExist(e) {
		t.Fatal("dry-run saved state")
	}
	invoke(0, "push", "--dir", dir, "--tag", "v1")
	invoke(0, "tags", "hello-skill")
	invoke(0, "inspect", "hello-skill:v1")
	target := filepath.Join(root, "下载 skills")
	invoke(0, "pull", "hello-skill:v1", "--target", target)
	download := filepath.Join(target, "hello-skill")
	got, e := os.ReadFile(filepath.Join(download, "SKILL.md"))
	if e != nil || !bytes.Equal(got, md) {
		t.Fatal("file bytes changed")
	}
	invoke(0, "status", "--dir", download)
	os.WriteFile(filepath.Join(download, "note.txt"), []byte("edited\n"), 0600)
	invoke(0, "push", "--dir", download, "--tag", "v2")
	invoke(7, "push", "--dir", download, "--tag", "v1")
	invoke(0, "pull", "hello-skill:v2", "--target", filepath.Join(root, "verify"))
	got, e = os.ReadFile(filepath.Join(root, "verify", "hello-skill", "note.txt"))
	if e != nil || string(got) != "edited\n" {
		t.Fatal("edited version missing")
	}
	invoke(0, "push", "--dir", download, "--tag", "v2")
	invoke(0, "pull", "--dir", download)
	got, e = os.ReadFile(docker)
	if e != nil || !bytes.Equal(got, dockerBytes) {
		t.Fatal("Docker config was modified")
	}
}
