package registry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Rush1994/SkillPort/internal/config"
	"github.com/Rush1994/SkillPort/internal/errs"
)

type ConnectionInfo struct {
	Endpoint       string `json:"endpoint,omitempty"`
	SkipTLSVerify  bool   `json:"skipTLSVerify"`
	Source         string `json:"source"`
	DockerConfig   string `json:"dockerDaemonConfig,omitempty"`
	DockerInsecure bool   `json:"dockerInsecureRegistry"`
	Probed         bool   `json:"probed"`
	Pending        bool   `json:"pending"`
}

// DaemonPaths are local file discovery paths, not a claim about the active
// Docker context. Never contact a Docker Engine or execute the docker CLI.
func DaemonPaths() []string {
	home, _ := os.UserHomeDir()
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".docker", "daemon.json"))
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		paths = append(paths, filepath.Join(base, "docker", "config", "daemon.json"))
	} else if runtime.GOOS == "linux" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" && home != "" {
			base = filepath.Join(home, ".config")
		}
		if base != "" {
			paths = append(paths, filepath.Join(base, "docker", "daemon.json"))
		}
		paths = append(paths, "/etc/docker/daemon.json")
	}
	return paths
}

// ReadDockerPolicy reads the first existing file only, with an explicit path
// taking precedence over discovery. It does not merge unrelated Engine configs.
func ReadDockerPolicy(explicit string, paths []string) ([]string, string, error) {
	if explicit != "" {
		paths = []string{explicit}
	}
	for _, p := range paths {
		f, e := os.Open(p)
		if os.IsNotExist(e) && explicit == "" {
			continue
		}
		if e != nil {
			return nil, p, errs.New(2, "Cannot read Docker daemon configuration; use --docker-daemon-config to select the file.")
		}
		b, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
		f.Close()
		if e != nil || len(b) > 4<<20 {
			return nil, p, errs.New(2, "Cannot read Docker daemon configuration within size limit.")
		}
		var v struct {
			Registries []string `json:"insecure-registries"`
		}
		if json.Unmarshal(b, &v) != nil || len(v.Registries) > 10000 {
			return nil, p, errs.New(2, "Invalid Docker daemon insecure-registries configuration.")
		}
		return v.Registries, p, nil
	}
	return nil, "", nil
}

func MatchesDockerPolicy(ctx context.Context, host string, entries []string) bool {
	host = strings.ToLower(host)
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSpace(entry), host) {
			return true
		}
	}
	u, e := url.Parse("https://" + host)
	if e != nil {
		return false
	}
	hostname := u.Hostname()
	var networks []*net.IPNet
	for _, entry := range entries {
		if _, network, e := net.ParseCIDR(strings.TrimSpace(entry)); e == nil {
			networks = append(networks, network)
		}
	}
	if len(networks) == 0 {
		return false
	}
	ips := []net.IP{net.ParseIP(hostname)}
	if ips[0] == nil {
		child, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		ips, _ = net.DefaultResolver.LookupIP(child, "ip", hostname)
	}
	for _, ip := range ips {
		for _, network := range networks {
			if network.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// SelectConnection receives the effective high-priority policy from the CLI.
// With auto enabled, anonymous /v2/ probes select one protocol before any
// credential lookup. Once selected, authenticated operations never fall back.
func SelectConnection(ctx context.Context, r config.Registry, auto, dry bool, daemonFile string, source string) (config.Registry, ConnectionInfo, error) {
	info := ConnectionInfo{Endpoint: r.Endpoint, SkipTLSVerify: r.Insecure, Source: source}
	if !auto {
		return r, info, nil
	}
	entries, file, e := ReadDockerPolicy(daemonFile, DaemonPaths())
	if e != nil {
		return r, info, e
	}
	info.DockerConfig = file
	if dry {
		info.Source = "auto"
		info.Pending = true
		info.Endpoint = ""
		return r, info, nil
	}
	info.DockerInsecure = MatchesDockerPolicy(ctx, r.Host(), entries)
	info.Source = "auto-probe"
	if info.DockerInsecure {
		info.Source = "docker-insecure-registry+probe"
	}
	host := r.Host()
	for _, scheme := range []string{"https", "http"} {
		if ctx.Err() != nil {
			return r, info, errs.New(5, "Connection discovery cancelled.")
		}
		candidate := r
		candidate.Endpoint = scheme + "://" + host
		if scheme == "http" {
			candidate.Insecure = false
			candidate.CAFile = ""
		}
		ok, responded := probe(ctx, candidate)
		if ok {
			info.Endpoint = candidate.Endpoint
			info.SkipTLSVerify = candidate.Insecure
			info.Probed = true
			return candidate, info, nil
		}
		if responded {
			return r, info, errs.New(5, "Endpoint responded but /v2/ did not identify an accessible Registry; specify the intended protocol or endpoint.")
		}
	}
	return r, info, errs.New(5, "Cannot detect registry protocol; check endpoint and connectivity, or specify http:// or https:// explicitly.")
}

func probe(ctx context.Context, r config.Registry) (bool, bool) {
	child, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	t := http.DefaultTransport.(*http.Transport).Clone()
	defer t.CloseIdleConnections()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: r.Insecure} // User-selected permissive discovery; contains no credentials.
	client := &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(child, http.MethodGet, r.Endpoint+"/v2/", nil)
	if e != nil {
		return false, false
	}
	response, e := client.Do(req)
	if e != nil {
		return false, false
	}
	defer response.Body.Close()
	return response.StatusCode == 200 || response.StatusCode == 401 || response.StatusCode == 403, true
}
