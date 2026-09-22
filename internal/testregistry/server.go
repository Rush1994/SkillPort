// Package testregistry is an in-memory test double, not a Harbor implementation.
package testregistry

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
)

type Server struct {
	mu                 sync.Mutex
	blobs              map[string][]byte
	manifests          map[string][]byte
	tags               map[string]string
	Username, Password string
	Denied             int
	Writes             int
}

func New() *Server {
	return &Server{blobs: map[string][]byte{}, manifests: map[string][]byte{}, tags: map[string]string{}}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Denied != 0 {
		http.Error(w, "denied", s.Denied)
		return
	}
	if s.Username != "" {
		u, p, ok := r.BasicAuth()
		if !ok || u != s.Username || p != s.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			http.Error(w, "authentication required", 401)
			return
		}
	}
	if r.URL.Path == "/v2/" {
		w.WriteHeader(200)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/v2/")
	repo, ref, manifest := strings.Cut(p, "/manifests/")
	if manifest {
		key := repo + "@" + ref
		if r.Method == "PUT" {
			b, _ := io.ReadAll(r.Body)
			d := digest.FromBytes(b).String()
			s.manifests[repo+"@"+d] = b
			s.tags[key] = d
			s.Writes++
			w.Header().Set("Docker-Content-Digest", d)
			w.WriteHeader(201)
			return
		}
		if d, ok := s.tags[key]; ok {
			key = repo + "@" + d
		}
		b, ok := s.manifests[key]
		if !ok {
			http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, 404)
			return
		}
		w.Header().Set("Content-Type", oci.MediaTypeImageManifest)
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(b).String())
		w.Header().Set("Content-Length", itoa(len(b)))
		if r.Method != "HEAD" {
			_, _ = w.Write(b)
		}
		return
	}
	if strings.Contains(p, "/blobs/uploads/") {
		if r.Method == "POST" {
			w.Header().Set("Location", "/v2/"+strings.Split(p, "/blobs/uploads/")[0]+"/blobs/uploads/fixture")
			w.WriteHeader(202)
			return
		}
		if r.Method == "PUT" {
			b, _ := io.ReadAll(r.Body)
			d := digest.FromBytes(b).String()
			if r.URL.Query().Get("digest") != d {
				http.Error(w, "bad digest", 400)
				return
			}
			s.blobs[d] = b
			s.Writes++
			w.Header().Set("Docker-Content-Digest", d)
			w.WriteHeader(201)
			return
		}
	}
	if _, d, ok := strings.Cut(p, "/blobs/"); ok {
		b, ok := s.blobs[d]
		if !ok {
			http.Error(w, "missing", 404)
			return
		}
		w.Header().Set("Content-Length", itoa(len(b)))
		w.Header().Set("Docker-Content-Digest", d)
		if r.Method != "HEAD" {
			_, _ = w.Write(b)
		}
		return
	}
	if repo, ok := strings.CutSuffix(p, "/tags/list"); ok {
		tags := []string{}
		for key := range s.tags {
			if strings.HasPrefix(key, repo+"@") {
				tags = append(tags, strings.TrimPrefix(key, repo+"@"))
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": repo, "tags": tags})
		return
	}
	http.Error(w, "unsupported test request", 404)
}
func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
