package registry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Rush1994/SkillPort/internal/config"
	"github.com/Rush1994/SkillPort/internal/errs"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

// Restricted transport applies to token requests too, including credential-bearing POSTs.
// Cross-host token realms are deliberately rejected until per-realm policy is implemented.
type transport struct {
	base  *http.Transport
	host  string
	plain bool
}

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.EqualFold(r.URL.Host, t.host) || r.URL.User != nil {
		return nil, errs.New(5, "Cross-host registry/token request denied.")
	}
	if r.URL.Scheme != "https" && !(t.plain && r.URL.Scheme == "http") {
		return nil, errs.New(5, "HTTP endpoint has not been explicitly enabled.")
	}
	return t.base.RoundTrip(r)
}
func New(r config.Registry, repository string, cred auth.Credential) (*remote.Repository, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: r.Insecure} // Explicit HTTPS-only option, validated below.
	var err error
	r, err = r.Validate()
	if err != nil {
		return nil, err
	}
	if r.CAFile != "" {
		pem, err := os.ReadFile(r.CAFile)
		if err != nil {
			return nil, errs.New(2, "Cannot read CA file.")
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errs.New(2, "CA file contains no valid certificates.")
		}
		tlsConfig.RootCAs = pool
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = tlsConfig
	base.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	base.ResponseHeaderTimeout = 30 * time.Second
	httpClient := &http.Client{Transport: transport{base, r.Host(), strings.HasPrefix(r.Endpoint, "http://")}, Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errs.New(5, "Too many redirects.")
		}
		if !strings.EqualFold(req.URL.Host, r.Host()) {
			req.Header.Del("Authorization")
			return errs.New(5, "Cross-host redirect denied.")
		}
		return nil
	}}
	repo, err := remote.NewRepository(r.Host() + "/" + repository)
	if err != nil {
		return nil, errs.New(2, "Invalid repository reference.")
	}
	repo.PlainHTTP = strings.HasPrefix(r.Endpoint, "http://")
	repo.Client = &auth.Client{Client: httpClient, Cache: auth.NewCache(), Credential: func(_ context.Context, host string) (auth.Credential, error) {
		if !strings.EqualFold(host, r.Host()) {
			return auth.EmptyCredential, nil
		}
		return cred, nil
	}}
	return repo, nil
}
func Classify(err error) error {
	if err == nil {
		return nil
	}
	var safe *errs.Error
	if errors.As(err, &safe) {
		return safe
	}
	if errors.Is(err, errdef.ErrNotFound) {
		return errs.New(8, "Reference not found.")
	}
	var response *errcode.ErrorResponse
	if errors.As(err, &response) {
		switch response.StatusCode {
		case 401:
			return errs.New(3, "Authentication rejected; run docker login again.")
		case 403:
			return errs.New(4, "Access denied; check Harbor project permissions.")
		case 404:
			return errs.New(8, "Reference or project not found.")
		}
	}
	return errs.New(5, "Registry request failed; check protocol, TLS/CA, endpoint and connectivity.")
}
