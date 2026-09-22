package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Rush1994/SkillPort/internal/errs"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SKILLPORT_USERNAME", "SKILLPORT_PASSWORD", "SKILLPORT_IDENTITY_TOKEN"} {
		value, present := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if present {
				os.Setenv(k, value)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}
func TestCredentialPrecedence(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	b, _ := json.Marshal(map[string]any{"auths": map[string]any{"host.test:8080": map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("fixture:password"))}}})
	os.WriteFile(file, b, 0600)
	r, e := Resolve(context.Background(), "host.test:8080", Options{ConfigFile: file})
	if e != nil || r.Credential.Username != "fixture" || r.Source != "docker-auths" {
		t.Fatalf("bad credentials: %v", e)
	}
	r, e = Resolve(context.Background(), "host.test", Options{ConfigFile: file})
	if e != nil || r.Credential.Username != "" {
		t.Fatal("cross-port credential reuse")
	}
	t.Setenv("SKILLPORT_USERNAME", "partial")
	if _, e = Resolve(context.Background(), "host.test:8080", Options{ConfigFile: file}); e == nil {
		t.Fatal("partial environment fell back")
	}
	r, e = Resolve(context.Background(), "host.test:8080", Options{ConfigFile: file, Anonymous: true})
	if e != nil || r.Credential.Username != "" {
		t.Fatal("anonymous read credentials")
	}
	r, e = Resolve(context.Background(), "host.test:8080", Options{Username: "explicit", PasswordStdin: true, Input: strings.NewReader("fixture-pass\r\n")})
	if e != nil || r.Credential.Username != "explicit" {
		t.Fatal("explicit precedence failed")
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(b, after) {
		t.Fatal("Docker config modified")
	}
}
func TestNativeHelperProtocol(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker-credential-skillportfixture")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/helper")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("helper build: %v %s", e, b)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	file := filepath.Join(dir, "config.json")
	b := []byte(`{"credsStore":"does-not-exist-fixture","credHelpers":{"ok.test":"skillportfixture","missing.test":"skillportfixture","failure.test":"skillportfixture","timeout.test":"skillportfixture","token.test":"skillportfixture"}}`)
	os.WriteFile(file, b, 0600)
	for _, test := range []struct {
		host string
		code int
	}{{"ok.test", 0}, {"missing.test", 0}, {"failure.test", 9}, {"timeout.test", 9}, {"unknown.test", 9}, {"token.test", 0}} {
		t.Run(test.host, func(t *testing.T) {
			timeout := 2 * time.Second
			if test.host == "timeout.test" {
				timeout = 100 * time.Millisecond
			}
			r, e := Resolve(context.Background(), test.host, Options{ConfigFile: file, HelperTimeout: timeout})
			if test.code != 0 {
				var safe *errs.Error
				if !errors.As(e, &safe) || safe.Code != test.code {
					t.Fatalf("wrong failure: %v", e)
				}
				if strings.Contains(e.Error(), "fixture-private-value") {
					t.Fatal("helper output leaked")
				}
			} else if e != nil {
				t.Fatal(e)
			} else if test.host == "ok.test" && r.Credential.Username != "fixture-user" {
				t.Fatal("helper not selected")
			} else if test.host == "token.test" && r.Credential.RefreshToken != "fixture-token" {
				t.Fatal("token helper failed")
			}
		})
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(b, after) {
		t.Fatal("Docker config modified")
	}
}
