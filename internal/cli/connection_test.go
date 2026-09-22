package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Rush1994/SkillPort/internal/testregistry"
)

func invokeConnection(t *testing.T, want int, args ...string) Envelope {
	t.Helper()
	var out, diag bytes.Buffer
	code := 0
	if binary := os.Getenv("SKILLPORT_TEST_BINARY"); binary != "" {
		cmd := exec.Command(binary, args...)
		cmd.Stdout = &out
		cmd.Stderr = &diag
		if e := cmd.Run(); e != nil {
			if ex, ok := e.(*exec.ExitError); ok {
				code = ex.ExitCode()
			} else {
				t.Fatal(e)
			}
		}
	} else {
		code = Run(context.Background(), args, &out, &diag, BuildInfo{})
	}
	if code != want {
		t.Fatalf("got code %d want %d: %s %s", code, want, out.String(), diag.String())
	}
	var result Envelope
	if json.Unmarshal(out.Bytes(), &result) != nil {
		t.Fatalf("invalid JSON %s", out.String())
	}
	if strings.Contains(out.String()+diag.String(), "fixture-password") {
		t.Fatal("credential leaked")
	}
	return result
}

func TestAutomaticConnectionRoundTrip(t *testing.T) {
	for _, mode := range []string{"auto-http", "docker-http", "auto-selfsigned", "docker-selfsigned", "url-http", "url-https"} {
		t.Run(mode, func(t *testing.T) {
			mock := testregistry.New()
			mock.Username = "fixture-user"
			mock.Password = "fixture-password"
			var probes, probeCredentials atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/" {
					probes.Add(1)
					if r.Header.Get("Authorization") != "" {
						probeCredentials.Add(1)
					}
				}
				mock.ServeHTTP(w, r)
			})
			var srv *httptest.Server
			if strings.Contains(mode, "selfsigned") || mode == "url-https" {
				srv = httptest.NewTLSServer(handler)
			} else {
				srv = httptest.NewServer(handler)
			}
			defer srv.Close()
			root := t.TempDir()
			host := strings.SplitN(srv.URL, "://", 2)[1]
			daemon := filepath.Join(root, "daemon.json")
			entries := []string{}
			if strings.HasPrefix(mode, "docker") {
				entries = append(entries, host)
			}
			daemonBytes, _ := json.Marshal(map[string]any{"insecure-registries": entries})
			os.WriteFile(daemon, daemonBytes, 0600)
			docker := filepath.Join(root, "auth.json")
			creds, _ := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-password"))}}})
			os.WriteFile(docker, creds, 0600)
			prefix := []string{"--config", filepath.Join(root, "cli.json"), "--docker-daemon-config", daemon, "--registry-config", docker, "--format", "json"}
			invoke := func(args ...string) Envelope {
				return invokeConnection(t, 0, append(append([]string{}, prefix...), args...)...)
			}
			dir := filepath.Join(root, "source")
			os.MkdirAll(dir, 0700)
			os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: test\ndescription: fixture\n---\nBody\n"), 0600)
			ref := host + "/skills/test:v1"
			if strings.HasPrefix(mode, "url-") {
				ref = srv.URL + "/skills/test:v1"
			}
			result := invoke("push", ref, "--dir", dir)
			data := result.Data.(map[string]any)
			info := data["connection"].(map[string]any)
			if info["endpoint"] != srv.URL {
				t.Fatalf("wrong protocol: %+v", info)
			}
			if strings.HasPrefix(mode, "docker") && info["dockerInsecureRegistry"] != true {
				t.Fatalf("Docker policy not recognized: %+v", info)
			}
			invoke("pull", ref, "--target", filepath.Join(root, "target"))
			invoke("push", "--dir", filepath.Join(root, "target", "test"), "--tag", "v2")
			if probes.Load() == 0 || probeCredentials.Load() != 0 {
				t.Fatalf("expected anonymous probes: %d credentialed %d", probes.Load(), probeCredentials.Load())
			}
			after, _ := os.ReadFile(daemon)
			if !bytes.Equal(after, daemonBytes) {
				t.Fatal("daemon configuration modified")
			}
		})
	}
}

func TestConnectionOverridesAndDryRun(t *testing.T) {
	root := t.TempDir()
	daemon := filepath.Join(root, "daemon.json")
	os.WriteFile(daemon, []byte(`{}`), 0600)
	prefix := []string{"--config", filepath.Join(root, "cli.json"), "--docker-daemon-config", daemon, "--anonymous", "--format", "json"}
	srv := httptest.NewTLSServer(testregistry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	invokeConnection(t, 5, append(prefix, "tags", host+"/skills/test", "--insecure=false")...)
	invokeConnection(t, 2, append(prefix, "tags", srv.URL+"/skills/test", "--plain-http")...)
	invokeConnection(t, 2, append(prefix, "tags", "http://user:fixture-password@host/skills/test")...)
	var calls atomic.Int32
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer httpSrv.Close()
	dir := filepath.Join(root, "source")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: test\ndescription: fixture\n---\nBody\n"), 0600)
	result := invokeConnection(t, 0, append(prefix, "push", strings.TrimPrefix(httpSrv.URL, "http://")+"/skills/test:v1", "--dir", dir, "--dry-run")...)
	if calls.Load() != 0 || result.Data.(map[string]any)["connection"].(map[string]any)["pending"] != true {
		t.Fatal("dry run probed network or claimed selected protocol")
	}
}
