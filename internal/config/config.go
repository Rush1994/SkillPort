package config

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Rush1994/SkillPort/internal/errs"
)

type Registry struct {
	Endpoint string `json:"endpoint"`
	Project  string `json:"project"`
	CAFile   string `json:"caFile,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
}
type Config struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Default       string              `json:"default,omitempty"`
	Registries    map[string]Registry `json:"registries"`
}

var aliasRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var projectRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

func ValidAlias(s string) bool { return aliasRE.MatchString(s) }
func DefaultPath() string {
	if p := os.Getenv("SKILLPORT_CONFIG"); p != "" {
		return p
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	current := filepath.Join(d, "skillport", "config.json")
	if _, err := os.Stat(current); os.IsNotExist(err) {
		legacy := filepath.Join(d, "skillctl", "config.json")
		if _, err = os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return current
}
func (r Registry) Validate() (Registry, error) {
	if !strings.Contains(r.Endpoint, "://") {
		r.Endpoint = "https://" + r.Endpoint
	}
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return r, errs.New(2, "Endpoint must be an HTTP/HTTPS host with optional port, without credentials or path.")
	}
	if u.Scheme == "http" && (r.Insecure || r.CAFile != "") {
		return r, errs.New(2, "HTTP cannot be combined with TLS options.")
	}
	if !projectRE.MatchString(r.Project) {
		return r, errs.New(2, "A valid lowercase project/repository prefix is required.")
	}
	r.Endpoint = u.Scheme + "://" + strings.ToLower(u.Host)
	return r, nil
}
func (r Registry) Host() string { u, _ := url.Parse(r.Endpoint); return u.Host }
func Load(p string) (Config, error) {
	c := Config{SchemaVersion: 1, Registries: map[string]Registry{}}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, errs.New(2, "Cannot read skillport configuration.")
	}
	if len(b) > 1<<20 || json.Unmarshal(b, &c) != nil || c.SchemaVersion != 1 || c.Registries == nil {
		return c, errs.New(2, "Invalid skillport configuration.")
	}
	for a, r := range c.Registries {
		if !ValidAlias(a) {
			return c, errs.New(2, "Invalid registry alias.")
		}
		r, err = r.Validate()
		if err != nil {
			return c, err
		}
		if r.CAFile != "" && !filepath.IsAbs(r.CAFile) {
			r.CAFile = filepath.Join(filepath.Dir(p), r.CAFile)
		}
		c.Registries[a] = r
	}
	if c.Default != "" {
		if _, ok := c.Registries[c.Default]; !ok {
			return c, errs.New(2, "Default registry does not exist.")
		}
	}
	return c, nil
}
func Save(p string, c Config) error {
	if p == "" {
		return errs.New(2, "No configuration path available.")
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return errs.New(2, "Cannot create configuration directory.")
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".config-*")
	if err != nil {
		return errs.New(2, "Cannot stage configuration.")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errs.New(2, "Cannot write configuration.")
	}
	return errs.Wrap(2, "Cannot replace configuration.", os.Rename(tmp, p))
}
