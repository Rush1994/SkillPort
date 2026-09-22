package cli

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localauth "github.com/Rush1994/SkillPort/internal/auth"
	"github.com/Rush1994/SkillPort/internal/config"
	"github.com/Rush1994/SkillPort/internal/errs"
	regclient "github.com/Rush1994/SkillPort/internal/registry"
	"github.com/Rush1994/SkillPort/internal/skill"
	syncer "github.com/Rush1994/SkillPort/internal/sync"
	"github.com/spf13/cobra"
	oreg "oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

type application struct {
	root                  *cobra.Command
	format                *string
	configPath, alias, ca string
	daemonFile            string
	connection            regclient.ConnectionInfo
	plain, insecure       bool
	auth                  localauth.Options
	timeout               time.Duration
	failureData           any
}

func (a *application) output(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	if *a.format == "json" {
		return enc.Encode(Envelope{SchemaVersion: 1, Success: true, Data: v})
	}
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func (a *application) register() {
	f := a.root.PersistentFlags()
	f.StringVar(&a.configPath, "config", config.DefaultPath(), "skillport configuration JSON file")
	f.StringVar(&a.alias, "registry", os.Getenv("SKILLPORT_REGISTRY"), "Configured registry alias")
	f.BoolVar(&a.plain, "plain-http", false, "Pin HTTP (false pins HTTPS); otherwise discover the protocol")
	f.BoolVar(&a.insecure, "insecure", false, "Skip HTTPS certificate verification")
	f.StringVar(&a.ca, "ca-file", "", "Enterprise CA PEM file")
	f.StringVar(&a.daemonFile, "docker-daemon-config", os.Getenv("SKILLPORT_DOCKER_DAEMON_CONFIG"), "Read insecure-registries from this daemon.json (otherwise auto-discover)")
	f.StringVar(&a.auth.ConfigFile, "registry-config", "", "Read credentials from this Docker config.json")
	f.BoolVar(&a.auth.Anonymous, "anonymous", false, "Disable all credential reads")
	f.StringVar(&a.auth.Username, "username", "", "Username paired with --password-stdin")
	f.BoolVar(&a.auth.PasswordStdin, "password-stdin", false, "Read password from stdin")
	f.BoolVar(&a.auth.TokenStdin, "identity-token-stdin", false, "Read identity token from stdin")
	f.DurationVar(&a.timeout, "timeout", 5*time.Minute, "Overall remote operation timeout")
	a.registryCommands()
	a.transferCommands()
	a.root.AddCommand(&cobra.Command{Use: "doctor [alias|endpoint]", Short: "Check registry reachability and credential source without writing", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		input := "diagnostic"
		if len(args) > 0 {
			c, e := config.Load(a.configPath)
			if e != nil {
				return e
			}
			if _, ok := c.Registries[args[0]]; ok {
				a.alias = args[0]
			} else if strings.ContainsAny(args[0], ".:") || args[0] == "localhost" {
				input = strings.TrimRight(args[0], "/") + "/diagnostic/connection"
			} else {
				a.alias = args[0]
			}
		}
		ctx, cancel, e := a.context(cmd)
		if e != nil {
			return e
		}
		defer cancel()
		repo, _, cred, e := a.repository(ctx, cmd, input, "", "", false, false)
		if e != nil {
			return e
		}
		reg, e := remote.NewRegistry(repo.Reference.Registry)
		if e != nil {
			return errs.New(2, "Invalid registry.")
		}
		reg.PlainHTTP = repo.PlainHTTP
		reg.Client = repo.Client
		e = reg.Ping(ctx)
		if e != nil {
			return regclient.Classify(e)
		}
		return a.output(cmd, map[string]any{"host": repo.Reference.Registry, "plainHTTP": repo.PlainHTTP, "connection": a.connection, "authentication": cred, "reachable": true, "uploadPermission": "not tested"})
	}})
	var dir string
	var strict bool
	v := &cobra.Command{Use: "validate", Short: "Validate a local Skill without network access", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, e := skill.Scan(dir, strict)
		if e != nil {
			a.failureData = s
			return e
		}
		return a.output(cmd, s)
	}}
	v.Flags().StringVar(&dir, "dir", ".", "Skill directory")
	v.Flags().BoolVar(&strict, "strict", false, "Reject unscanned content warnings")
	a.root.AddCommand(v)
	var statusDir string
	st := &cobra.Command{Use: "status", Short: "Compare local files with the saved baseline (offline)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, e := syncer.Status(statusDir)
		if e != nil {
			return e
		}
		return a.output(cmd, c)
	}}
	st.Flags().StringVar(&statusDir, "dir", ".", "Tracked Skill directory")
	a.root.AddCommand(st)
	for _, verb := range []string{"tags", "inspect"} {
		verb := verb
		cmd := &cobra.Command{Use: verb + " <reference>", Short: "Read remote " + verb, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel, e := a.context(cmd)
			if e != nil {
				return e
			}
			defer cancel()
			repo, ref, _, e := a.repository(ctx, cmd, args[0], "", "", false, false)
			if e != nil {
				return e
			}
			if verb == "tags" {
				tags := []string{}
				e = repo.Tags(ctx, "", func(page []string) error {
					tags = append(tags, page...)
					if len(tags) > 10000 {
						return errs.New(5, "Tag listing limit exceeded.")
					}
					return nil
				})
				if e != nil {
					return regclient.Classify(e)
				}
				return a.output(cmd, tags)
			}
			if ref == "" {
				return errs.New(2, "Inspect requires a tag or digest.")
			}
			d, m, c, e := syncer.Inspect(ctx, repo, ref)
			if e != nil {
				return e
			}
			return a.output(cmd, map[string]any{"descriptor": d, "manifest": m, "metadata": c, "connection": a.connection})
		}}
		a.root.AddCommand(cmd)
	}
}
func (a *application) context(cmd *cobra.Command) (context.Context, context.CancelFunc, error) {
	if a.timeout <= 0 {
		return nil, nil, errs.New(2, "Timeout must be positive.")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), a.timeout)
	return ctx, cancel, nil
}
func (a *application) registryCommands() {
	parent := &cobra.Command{Use: "registry", Short: "Manage registry endpoints; never store credentials"}
	var endpoint, project, ca string
	var insecure bool
	add := &cobra.Command{Use: "add <alias>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !config.ValidAlias(args[0]) {
			return errs.New(2, "Invalid alias.")
		}
		c, e := config.Load(a.configPath)
		if e != nil {
			return e
		}
		if _, exists := c.Registries[args[0]]; exists {
			return errs.New(2, "Alias exists; remove it explicitly before replacing it.")
		}
		r, e := (config.Registry{Endpoint: endpoint, Project: project, CAFile: ca, Insecure: insecure}).Validate()
		if e != nil {
			return e
		}
		if ca != "" {
			r.CAFile, e = filepath.Abs(ca)
			if e != nil {
				return errs.New(2, "Invalid CA path.")
			}
		}
		c.Registries[args[0]] = r
		if e = config.Save(a.configPath, c); e != nil {
			return e
		}
		return a.output(cmd, r)
	}}
	add.Flags().StringVar(&endpoint, "endpoint", "", "Harbor HTTP/HTTPS endpoint (required)")
	add.Flags().StringVar(&project, "project", "", "Existing Harbor project/prefix (required)")
	add.Flags().StringVar(&ca, "ca-file", "", "Enterprise CA PEM file")
	add.Flags().BoolVar(&insecure, "insecure", false, "Skip HTTPS certificate verification")
	_ = add.MarkFlagRequired("endpoint")
	_ = add.MarkFlagRequired("project")
	parent.AddCommand(add)
	parent.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		c, e := config.Load(a.configPath)
		if e != nil {
			return e
		}
		return a.output(cmd, c)
	}})
	for _, verb := range []string{"use", "remove"} {
		verb := verb
		parent.AddCommand(&cobra.Command{Use: verb + " <alias>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			c, e := config.Load(a.configPath)
			if e != nil {
				return e
			}
			if _, ok := c.Registries[args[0]]; !ok {
				return errs.New(2, "Registry alias not found.")
			}
			if verb == "use" {
				c.Default = args[0]
			} else {
				delete(c.Registries, args[0])
				if c.Default == args[0] {
					c.Default = ""
				}
			}
			if e = config.Save(a.configPath, c); e != nil {
				return e
			}
			return a.output(cmd, c)
		}})
	}
	a.root.AddCommand(parent)
}
func (a *application) transferCommands() {
	var pushDir, tag string
	var pushStrict, pushDry bool
	push := &cobra.Command{Use: "push [reference]", Short: "Publish a Skill using a unique tag", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel, e := a.context(cmd)
		if e != nil {
			return e
		}
		defer cancel()
		input := ""
		if len(args) > 0 {
			input = args[0]
		}
		s, e := skill.Scan(pushDir, pushStrict)
		if e != nil {
			return e
		}
		repo, ref, _, e := a.repository(ctx, cmd, input, pushDir, s.Metadata.Name, true, pushDry)
		if e != nil {
			return e
		}
		if tag != "" {
			if ref != "" && input != "" && ref != tag {
				return errs.New(2, "Reference tag conflicts with --tag.")
			}
			ref = tag
		}
		if strings.HasPrefix(ref, "sha256:") {
			return errs.New(2, "Push requires --tag for a digest source.")
		}
		result, e := syncer.Push(ctx, repo, pushDir, ref, pushStrict, pushDry)
		result.Connection = &a.connection
		if e != nil {
			a.failureData = result
			return e
		}
		return a.output(cmd, result)
	}}
	push.Flags().StringVar(&pushDir, "dir", ".", "Skill directory")
	push.Flags().StringVar(&tag, "tag", "", "Publish tag; no implicit latest")
	push.Flags().BoolVar(&pushStrict, "strict", false, "Reject unscanned content")
	push.Flags().BoolVar(&pushDry, "dry-run", false, "Validate and preview without publishing")
	a.root.AddCommand(push)
	var target, pullDir string
	var pullStrict, pullDry bool
	pull := &cobra.Command{Use: "pull [reference]", Short: "Download into an editable directory, preserving existing data", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if pullDir != "" && cmd.Flags().Changed("target") {
			return errs.New(2, "Use --dir or --target, not both.")
		}
		ctx, cancel, e := a.context(cmd)
		if e != nil {
			return e
		}
		defer cancel()
		input := ""
		if len(args) > 0 {
			input = args[0]
		}
		repo, ref, _, e := a.repository(ctx, cmd, input, pullDir, "", false, false)
		if e != nil {
			return e
		}
		if ref == "" {
			return errs.New(2, "Pull requires a tag/digest or tracked --dir.")
		}
		dir := pullDir
		if dir == "" {
			dir = filepath.Join(target, path.Base(repo.Reference.Repository))
		}
		result, e := syncer.Pull(ctx, repo, ref, dir, pullStrict, pullDry)
		result.Connection = &a.connection
		if e != nil {
			a.failureData = result
			return e
		}
		return a.output(cmd, result)
	}}
	pull.Flags().StringVar(&target, "target", "skills", "Destination parent directory")
	pull.Flags().StringVar(&pullDir, "dir", "", "Update a tracked Skill directory")
	pull.Flags().BoolVar(&pullStrict, "strict", false, "Reject unscanned content")
	pull.Flags().BoolVar(&pullDry, "dry-run", false, "Fetch and validate without committing files")
	a.root.AddCommand(pull)
}
func (a *application) repository(ctx context.Context, cmd *cobra.Command, input, dir, name string, push, dry bool) (*remote.Repository, string, localauth.Result, error) {
	var authResult localauth.Result
	c, e := config.Load(a.configPath)
	if e != nil {
		return nil, "", authResult, e
	}
	if input == "" && dir != "" {
		s, e := syncer.Load(dir)
		if e != nil {
			return nil, "", authResult, e
		}
		if s != nil {
			input = s.Registry + "/" + s.Repository
			if s.Tag != "" {
				input += ":" + s.Tag
			} else {
				input += "@" + s.Digest
			}
		}
	}
	alias := a.alias
	if alias == "" {
		alias = c.Default
	}
	selected, selectedOK := c.Registries[alias]
	if a.alias != "" && !selectedOK {
		return nil, "", authResult, errs.New(2, "Selected registry alias does not exist.")
	}
	full := false
	referenceScheme := ""
	if strings.Contains(input, "://") {
		u, err := url.Parse(input)
		if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || strings.Contains(input, "%") {
			return nil, "", authResult, errs.New(2, "Reference URL must use HTTP/HTTPS without credentials, query or fragment.")
		}
		referenceScheme = u.Scheme
		input = u.Host + u.Path
		full = true
	}
	if first, _, ok := strings.Cut(input, "/"); ok {
		full = full || strings.ContainsAny(first, ".:") || first == "localhost" || strings.Count(input, "/") >= 2
		for _, candidate := range c.Registries {
			if strings.EqualFold(candidate.Host(), first) {
				full = true
			}
		}
	}
	if !full {
		if !selectedOK {
			return nil, "", authResult, errs.New(2, "Select a registry alias or provide a complete reference.")
		}
		if input == "" {
			input = name
		}
		if input == "" {
			return nil, "", authResult, errs.New(2, "A Skill reference is required.")
		}
		input = selected.Host() + "/" + selected.Project + "/" + input
	}
	parsed, e := oreg.ParseReference(input)
	if e != nil {
		return nil, "", authResult, errs.New(2, "Invalid registry reference.")
	}
	parsed.Registry = strings.ToLower(parsed.Registry)
	r := config.Registry{Endpoint: "https://" + parsed.Registry, Project: path.Dir(parsed.Repository), Insecure: true}
	configured := false
	if r.Project == "." {
		return nil, "", authResult, errs.New(2, "Repository must include a Harbor project.")
	}
	if selectedOK && strings.EqualFold(selected.Host(), parsed.Registry) {
		r = selected
		configured = true
	} else {
		found := false
		for _, candidate := range c.Registries {
			if strings.EqualFold(candidate.Host(), parsed.Registry) {
				if found && (candidate.Endpoint != r.Endpoint || candidate.CAFile != r.CAFile || candidate.Insecure != r.Insecure) {
					return nil, "", authResult, errs.New(2, "Conflicting host policies; select a registry alias.")
				}
				r = candidate
				configured = true
				found = true
			}
		}
	}
	auto := !configured
	source := "saved-config"
	if auto {
		source = "auto"
	}
	if referenceScheme != "" {
		if !strings.HasPrefix(r.Endpoint, referenceScheme+"://") {
			r.CAFile = ""
			r.Insecure = referenceScheme == "https"
		}
		r.Endpoint = referenceScheme + "://" + parsed.Registry
		auto = false
		source = "reference-url"
	}
	plain := strings.HasPrefix(r.Endpoint, "http://")
	var explicitTLS bool
	for _, v := range []struct {
		flag, env string
		dest      *bool
	}{{"plain-http", "SKILLPORT_PLAIN_HTTP", &plain}, {"insecure", "SKILLPORT_INSECURE", &r.Insecure}} {
		if cmd.Flags().Changed(v.flag) || a.root.PersistentFlags().Changed(v.flag) {
			if v.flag == "plain-http" {
				if referenceScheme != "" && a.plain != (referenceScheme == "http") {
					return nil, "", authResult, errs.New(2, "Reference URL conflicts with --plain-http.")
				}
				*v.dest = a.plain
			} else {
				*v.dest = a.insecure
				explicitTLS = a.insecure
			}
			auto = false
			source = "command-line"
		} else if value, ok := os.LookupEnv(v.env); ok {
			if referenceScheme != "" && v.flag == "plain-http" {
				continue
			}
			b, e := strconv.ParseBool(value)
			if e != nil {
				return nil, "", authResult, errs.New(2, "Invalid boolean connection environment setting.")
			}
			*v.dest = b
			if v.flag == "insecure" {
				explicitTLS = b
			}
			auto = false
			source = "environment"
		}
	}
	if a.root.PersistentFlags().Changed("ca-file") {
		r.CAFile = a.ca
		auto = false
		source = "command-line"
		if !explicitTLS {
			r.Insecure = false
		}
	} else if ca, ok := os.LookupEnv("SKILLPORT_CA_FILE"); ok {
		r.CAFile = ca
		auto = false
		source = "environment"
		if !explicitTLS {
			r.Insecure = false
		}
	}
	scheme := "https://"
	if plain {
		scheme = "http://"
		if !explicitTLS {
			r.Insecure = false
		}
	}
	r.Endpoint = scheme + parsed.Registry
	r, e = r.Validate()
	if e != nil {
		return nil, "", authResult, e
	}
	r, a.connection, e = regclient.SelectConnection(ctx, r, auto, dry, a.daemonFile, source)
	if e != nil {
		return nil, "", authResult, e
	}
	if !dry {
		o := a.auth
		o.Input = cmd.InOrStdin()
		authResult, e = localauth.Resolve(ctx, parsed.Registry, o)
		if e != nil {
			return nil, "", authResult, e
		}
	}
	repo, e := regclient.New(r, parsed.Repository, authResult.Credential)
	return repo, parsed.Reference, authResult, e
}
