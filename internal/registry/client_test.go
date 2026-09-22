package registry

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rush1994/SkillPort/internal/config"
	"github.com/Rush1994/SkillPort/internal/errs"
	"github.com/Rush1994/SkillPort/internal/testregistry"
	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestTLSModes(t *testing.T) {
	srv := httptest.NewTLSServer(testregistry.New())
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600)
	for _, test := range []struct {
		name, ca        string
		insecure, valid bool
	}{{"verified", "", false, false}, {"skip", "", true, true}, {"ca", ca, false, true}} {
		t.Run(test.name, func(t *testing.T) {
			r, e := New(config.Registry{Endpoint: srv.URL, Project: "skills", CAFile: test.ca, Insecure: test.insecure}, "skills/test", auth.EmptyCredential)
			if e != nil {
				t.Fatal(e)
			}
			e = r.Tags(context.Background(), "", func([]string) error { return nil })
			if (e == nil) != test.valid {
				t.Fatalf("unexpected TLS result: %v", e)
			}
		})
	}
}
func TestHTTPNoDowngradeAndErrors(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mock := testregistry.New()
			mock.Denied = status
			srv := httptest.NewServer(mock)
			defer srv.Close()
			r, e := New(config.Registry{Endpoint: srv.URL, Project: "skills"}, "skills/test", auth.EmptyCredential)
			if e != nil {
				t.Fatal(e)
			}
			_, e = r.Resolve(context.Background(), "v1")
			safe := Classify(e)
			var typed *errs.Error
			if !errors.As(safe, &typed) {
				t.Fatal(safe)
			}
			want := map[int]int{401: 3, 403: 4, 404: 8}[status]
			if typed.Code != want {
				t.Fatalf("got %d want %d", typed.Code, want)
			}
		})
	}
}
func TestCrossHostTokenDenied(t *testing.T) {
	called := false
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.Write([]byte(`{"token":"fixture"}`)) }))
	defer token.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+token.URL+`/token",service="fixture"`)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	r, e := New(config.Registry{Endpoint: srv.URL, Project: "skills"}, "skills/test", auth.Credential{Username: "fixture", Password: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Resolve(context.Background(), "v1")
	if e == nil || called {
		t.Fatal("cross-host token request escaped policy")
	}
}

func TestSameHostBearerChallenge(t *testing.T) {
	mock := testregistry.New()
	tokenRequested := false
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			u, p, ok := r.BasicAuth()
			if !ok || u != "fixture" || p != "fixture" {
				w.WriteHeader(401)
				return
			}
			tokenRequested = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"token":"fixture-access-token"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-access-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fixture"`)
			w.WriteHeader(401)
			return
		}
		mock.ServeHTTP(w, r)
	}))
	defer srv.Close()
	r, e := New(config.Registry{Endpoint: srv.URL, Project: "skills"}, "skills/test", auth.Credential{Username: "fixture", Password: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	e = r.Tags(context.Background(), "", func([]string) error { return nil })
	if e != nil || !tokenRequested {
		t.Fatalf("bearer challenge failed: %v", e)
	}
}
