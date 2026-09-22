package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rush1994/SkillPort/internal/config"
)

func TestReadDockerPolicy(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.json")
	desktop := filepath.Join(dir, "desktop.json")
	engine := filepath.Join(dir, "engine.json")
	original := []byte(`{"insecure-registries":["harbor.test:8080","10.0.0.0/8"],"other":"untouched"}`)
	os.WriteFile(desktop, original, 0600)
	os.WriteFile(engine, []byte(`{"insecure-registries":["other.test"]}`), 0600)
	entries, source, e := ReadDockerPolicy("", []string{missing, desktop, engine})
	if e != nil || source != desktop || len(entries) != 2 {
		t.Fatalf("discovery: %v %s %v", entries, source, e)
	}
	entries, source, e = ReadDockerPolicy(engine, []string{desktop})
	if e != nil || source != engine || entries[0] != "other.test" {
		t.Fatal("explicit file not preferred")
	}
	if _, _, e = ReadDockerPolicy(missing, nil); e == nil {
		t.Fatal("missing explicit file ignored")
	}
	os.WriteFile(engine, []byte(`{"insecure-registries":"secret-invalid"}`), 0600)
	if _, _, e = ReadDockerPolicy(engine, nil); e == nil || strings.Contains(e.Error(), "secret-invalid") {
		t.Fatal("invalid configuration not safely rejected")
	}
	after, _ := os.ReadFile(desktop)
	if string(after) != string(original) {
		t.Fatal("Docker configuration modified")
	}
}

func TestDockerHostAndCIDRMatch(t *testing.T) {
	entries := []string{"Harbor.test:8080", "10.0.0.0/8", "2001:db8::/32"}
	for _, test := range []struct {
		host string
		want bool
	}{{"harbor.test:8080", true}, {"harbor.test:8081", false}, {"other.harbor.test:8080", false}, {"10.2.3.4:5000", true}, {"192.168.1.1:5000", false}, {"[2001:db8::1]:5000", true}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		got := MatchesDockerPolicy(ctx, test.host, entries)
		cancel()
		if got != test.want {
			t.Errorf("%s got %v", test.host, got)
		}
	}
}

func TestProbeNeverFollowsRedirectOrSendsCredentials(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	var credentialSeen atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			credentialSeen.Store(true)
		}
		http.Redirect(w, r, target.URL, 302)
	}))
	defer srv.Close()
	r := config.Registry{Endpoint: srv.URL, Project: "skills", Insecure: true}
	ok, responded := probe(context.Background(), r)
	if ok || !responded || reached.Load() || credentialSeen.Load() {
		t.Fatal("probe followed redirect or sent credentials")
	}
}

func TestAutoProbeStopsOnRegistryResponse(t *testing.T) {
	file := filepath.Join(t.TempDir(), "daemon.json")
	os.WriteFile(file, []byte(`{}`), 0600)
	for _, status := range []int{200, 401, 403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer srv.Close()
			r, info, e := SelectConnection(context.Background(), config.Registry{Endpoint: srv.URL, Project: "skills", Insecure: true}, true, false, file, "auto")
			if status == 500 {
				if e == nil {
					t.Fatal("500 treated as successful discovery")
				}
				return
			}
			if e != nil || r.Endpoint != srv.URL || !info.Probed || !info.SkipTLSVerify {
				t.Fatalf("probe result: %+v %v", info, e)
			}
		})
	}
}
