package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Rush1994/SkillPort/internal/errs"
	orasauth "oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

type Options struct {
	Anonymous                 bool
	Username                  string
	PasswordStdin, TokenStdin bool
	ConfigFile                string
	Input                     io.Reader
	HelperTimeout             time.Duration
}
type Result struct {
	Credential orasauth.Credential `json:"-"`
	Source     string              `json:"source"`
	ConfigFile string              `json:"configFile,omitempty"`
	Helper     string              `json:"helper,omitempty"`
}

func Resolve(ctx context.Context, host string, o Options) (Result, error) {
	r := Result{Source: "anonymous"}
	explicit := o.Username != "" || o.PasswordStdin || o.TokenStdin
	if o.Anonymous {
		if explicit {
			return r, errs.New(2, "Anonymous and explicit authentication cannot be combined.")
		}
		return r, nil
	}
	if explicit {
		if (o.TokenStdin && (o.PasswordStdin || o.Username != "")) || (!o.TokenStdin && (o.Username == "" || !o.PasswordStdin)) {
			return r, errs.New(2, "Use username with password-stdin, or identity-token-stdin alone.")
		}
		if o.Input == nil {
			o.Input = os.Stdin
		}
		b, err := io.ReadAll(io.LimitReader(o.Input, 65537))
		if err != nil || len(b) > 65536 {
			return r, errs.New(2, "Cannot read credential from stdin.")
		}
		secret := strings.TrimRight(string(b), "\r\n")
		if secret == "" {
			return r, errs.New(2, "Empty credential from stdin.")
		}
		r.Source = "stdin"
		if o.TokenStdin {
			r.Credential.RefreshToken = secret
		} else {
			r.Credential.Username = o.Username
			r.Credential.Password = secret
		}
		return r, nil
	}
	u, uok := os.LookupEnv("SKILLPORT_USERNAME")
	p, pok := os.LookupEnv("SKILLPORT_PASSWORD")
	t, tok := os.LookupEnv("SKILLPORT_IDENTITY_TOKEN")
	if uok || pok || tok {
		if tok {
			if uok || pok || t == "" {
				return r, errs.New(2, "Identity token must be configured alone.")
			}
			r.Credential.RefreshToken = t
		} else {
			if !uok || !pok || u == "" || p == "" {
				return r, errs.New(2, "Environment username/password must both be set.")
			}
			r.Credential.Username = u
			r.Credential.Password = p
		}
		r.Source = "environment"
		return r, nil
	}
	pfile := o.ConfigFile
	if pfile == "" {
		d := os.Getenv("DOCKER_CONFIG")
		if d == "" {
			h, err := os.UserHomeDir()
			if err != nil {
				return r, errs.New(2, "Cannot locate Docker configuration.")
			}
			d = filepath.Join(h, ".docker")
		}
		pfile = filepath.Join(d, "config.json")
	}
	r.ConfigFile = pfile
	b, err := os.ReadFile(pfile)
	if os.IsNotExist(err) && o.ConfigFile == "" {
		return r, nil
	}
	if err != nil {
		return r, errs.New(2, "Cannot read Docker configuration.")
	}
	var cfg struct {
		CredsStore string                                                              `json:"credsStore"`
		Helpers    map[string]string                                                   `json:"credHelpers"`
		Auths      map[string]struct{ Auth, IdentityToken, Username, Password string } `json:"auths"`
	}
	if len(b) > 4<<20 || json.Unmarshal(b, &cfg) != nil {
		return r, errs.New(2, "Invalid Docker configuration.")
	}
	helper := cfg.Helpers[host]
	if helper == "" {
		helper = cfg.CredsStore
	}
	if helper != "" {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(helper) {
			return r, errs.New(9, "Invalid Docker credential helper name.")
		}
		r.Source = "docker-helper"
		r.Helper = helper
		if o.HelperTimeout <= 0 {
			o.HelperTimeout = 15 * time.Second
		}
		child, cancel := context.WithTimeout(ctx, o.HelperTimeout)
		defer cancel()
		r.Credential, err = credentials.NewNativeStore(helper).Get(child, host)
		if err != nil {
			return r, errs.New(9, "Docker credential helper failed or timed out; check helper installation and keychain access.")
		}
		return r, nil
	}
	entry, ok := cfg.Auths[host]
	if !ok {
		return r, nil
	}
	r.Source = "docker-auths"
	if entry.IdentityToken != "" {
		r.Credential.RefreshToken = entry.IdentityToken
		return r, nil
	}
	if entry.Auth != "" {
		b, err = base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			return r, errs.New(2, "Invalid Docker auth encoding.")
		}
		var found bool
		u, p, found = strings.Cut(string(b), ":")
		if !found {
			return r, errs.New(2, "Invalid Docker auth value.")
		}
	} else {
		u, p = entry.Username, entry.Password
	}
	if u == "" || p == "" {
		return r, errs.New(2, "Incomplete Docker credentials.")
	}
	r.Credential.Username = u
	r.Credential.Password = p
	return r, nil
}
