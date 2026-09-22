package skill

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Rush1994/SkillPort/internal/errs"
	"github.com/moby/patternmatcher"
	"gopkg.in/yaml.v3"
)

const MaxFile = 20 << 20
const MaxTotal = 50 << 20
const MaxEntries = 10000

type File struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	Mode int64  `json:"mode"`
	Data []byte `json:"-"`
}
type Finding struct {
	Rule   string `json:"rule"`
	Path   string `json:"path"`
	Status string `json:"status"`
}
type Snapshot struct {
	Metadata Config    `json:"metadata"`
	Files    []File    `json:"files"`
	Findings []Finding `json:"findings"`
	Size     int64     `json:"size"`
}

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

var nameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func ValidName(s string) bool { return len(s) >= 1 && len(s) <= 64 && nameRE.MatchString(s) }
func SafePath(p string) bool {
	if p == "" || !utf8.ValidString(p) || strings.ContainsAny(p, "\\:\x00\r\n\t") || path.IsAbs(p) || path.Clean(p) != p || len(p) > 4096 {
		return false
	}
	parts := strings.Split(p, "/")
	if len(parts) > 32 {
		return false
	}
	for _, c := range parts {
		if c == "" || c == "." || c == ".." || strings.TrimRight(c, " .") != c || strings.ContainsAny(c, `<>"|?*`) {
			return false
		}
		for _, r := range c {
			if r < 32 || r == 127 {
				return false
			}
		}
		base := strings.ToUpper(strings.SplitN(c, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CLOCK$" || regexp.MustCompile(`^(COM|LPT)[1-9¹²³]$`).MatchString(base) {
			return false
		}
	}
	return true
}
func Metadata(b []byte) (Config, error) {
	c := Config{SchemaVersion: 1}
	if !utf8.Valid(b) {
		return c, errs.Validation("META_UTF8", "SKILL.md")
	}
	b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(b, []byte("---\n")) {
		return c, errs.Validation("META_HEADER", "SKILL.md")
	}
	end := bytes.Index(b[4:], []byte("\n---\n"))
	if end < 0 || end > 65536 {
		return c, errs.Validation("META_HEADER_LIMIT", "SKILL.md")
	}
	if len(bytes.TrimSpace(b[4+end+5:])) == 0 {
		return c, errs.Validation("META_BODY", "SKILL.md")
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(b[4 : 4+end]))
	if dec.Decode(&doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return c, errs.Validation("META_YAML", "SKILL.md")
	}
	var extra yaml.Node
	if dec.Decode(&extra) != io.EOF {
		return c, errs.Validation("META_YAML", "SKILL.md")
	}
	nodes := 0
	var check func(*yaml.Node, int) bool
	check = func(n *yaml.Node, depth int) bool {
		nodes++
		if depth > 20 || nodes > 10000 || n.Kind == yaml.AliasNode || n.Anchor != "" || !strings.HasPrefix(n.Tag, "!!") {
			return false
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
					return false
				}
				seen[k.Value] = true
			}
		}
		for _, child := range n.Content {
			if !check(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !check(doc.Content[0], 0) {
		return c, errs.Validation("META_YAML_STRUCTURE", "SKILL.md")
	}
	n := doc.Content[0]
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i].Value, n.Content[i+1]
		if k == "name" || k == "description" || k == "version" {
			if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
				return c, errs.Validation("META_FIELD_TYPE", "SKILL.md")
			}
			switch k {
			case "name":
				c.Name = v.Value
			case "description":
				c.Description = strings.TrimSpace(v.Value)
			case "version":
				c.Version = v.Value
			}
		}
	}
	if !ValidName(c.Name) {
		return c, errs.Validation("META_NAME", "SKILL.md")
	}
	if utf8.RuneCountInString(c.Description) < 1 || utf8.RuneCountInString(c.Description) > 1024 {
		return c, errs.Validation("META_DESCRIPTION", "SKILL.md")
	}
	return c, nil
}
func Excluded(p string) string {
	parts := strings.Split(strings.ToLower(p), "/")
	for _, c := range parts {
		switch c {
		case ".git", StateDirectory, LegacyStateDirectory:
			return "CONTROL"
		case ".ssh", ".aws", ".azure", ".kube", ".docker", ".netrc", ".npmrc", ".pypirc", "id_rsa", "id_ed25519", "id_ecdsa":
			return "SENSITIVE_PATH"
		}
		if (c == ".env" || strings.HasPrefix(c, ".env.")) && c != ".env.example" && c != ".env.sample" && c != ".env.template" {
			return "SENSITIVE_PATH"
		}
		for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore"} {
			if strings.HasSuffix(c, ext) {
				return "SENSITIVE_PATH"
			}
		}
	}
	return ""
}

var secretRE = regexp.MustCompile(`(?im)(-----BEGIN [A-Z ]*PRIVATE KEY-----|\bgh[pousr]_[A-Za-z0-9]{20,}|\bAKIA[A-Z0-9]{16}\b|(?:password|passwd|api[_-]?key|secret|access[_-]?token)\s*[:=]\s*["']?[^\s"'<>${}]{8,})`)

func Sensitive(b []byte) bool { return secretRE.Match(b) }
func Scan(root string, strict bool) (Snapshot, error) {
	s := Snapshot{Files: []File{}, Findings: []Finding{}}
	root, err := filepath.Abs(root)
	if err != nil {
		return s, errs.New(6, "Invalid skill directory.")
	}
	if err = CheckParents(root); err != nil {
		return s, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return s, errs.New(6, "Skill directory is missing.")
	}
	var matcher *patternmatcher.PatternMatcher
	ignorePath := filepath.Join(root, ".skillignore")
	if st, e := os.Lstat(ignorePath); e == nil {
		if e = CheckFile(ignorePath, st); e != nil {
			return s, e
		}
		if !st.Mode().IsRegular() || st.Size() > 65536 {
			return s, errs.Validation("IGNORE_INVALID", ".skillignore")
		}
		b, e := os.ReadFile(ignorePath)
		if e != nil {
			return s, errs.Validation("IGNORE_READ", ".skillignore")
		}
		patterns := []string{}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				patterns = append(patterns, l)
			}
		}
		matcher, e = patternmatcher.New(patterns)
		if e != nil {
			return s, errs.Validation("IGNORE_INVALID", ".skillignore")
		}
	}
	seen := map[string]bool{}
	entries := 0
	err = filepath.WalkDir(root, func(full string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errs.New(6, "Cannot scan skill directory.")
		}
		if full == root {
			return nil
		}
		rel, _ := filepath.Rel(root, full)
		p := filepath.ToSlash(rel)
		entries++
		if entries > MaxEntries {
			return errs.New(6, "LIMIT_ENTRIES exceeded.")
		}
		if !SafePath(p) {
			return errs.Validation("PATH_INVALID", "[invalid path]")
		}
		key := strings.ToLower(p)
		if seen[key] {
			return errs.Validation("PATH_CASE_COLLISION", p)
		}
		seen[key] = true
		if rule := Excluded(p); rule != "" {
			s.Findings = append(s.Findings, Finding{rule, p, "excluded"})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if matcher != nil {
			matched, e := matcher.MatchesOrParentMatches(p)
			if e != nil {
				return errs.New(6, "Invalid ignore pattern.")
			}
			if matched && !d.IsDir() {
				s.Findings = append(s.Findings, Finding{"IGNORE", p, "excluded"})
				return nil
			}
		}
		info, e := os.Lstat(full)
		if e != nil {
			return errs.Validation("FILE_READ", p)
		}
		if e = CheckFile(full, info); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errs.Validation("FILE_TYPE", p)
		}
		if info.Size() > MaxFile {
			return errs.Validation("LIMIT_FILE", p)
		}
		f, e := os.Open(full)
		if e != nil {
			return errs.Validation("FILE_READ", p)
		}
		opened, e := f.Stat()
		if e != nil || !os.SameFile(info, opened) {
			f.Close()
			return errs.Validation("FILE_CHANGED", p)
		}
		b, e := io.ReadAll(io.LimitReader(f, MaxFile+1))
		f.Close()
		if e != nil {
			return errs.Validation("FILE_READ", p)
		}
		if len(b) > MaxFile {
			return errs.Validation("LIMIT_FILE", p)
		}
		s.Size += int64(len(b))
		if s.Size > MaxTotal {
			return errs.New(6, "LIMIT_TOTAL exceeded.")
		}
		if len(b) > 5<<20 || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 || bytes.HasPrefix(b, []byte{'P', 'K', 3, 4}) || bytes.HasPrefix(b, []byte{0x1f, 0x8b}) {
			s.Findings = append(s.Findings, Finding{"CONTENT_UNSCANNED", p, "unscanned"})
		} else if Sensitive(b) {
			return errs.Validation("SECRET_CONTENT", p)
		}
		mode := int64(0644)
		if info.Mode().Perm()&0111 != 0 {
			mode = 0755
		}
		s.Files = append(s.Files, File{Path: p, Hash: Hash(b), Size: int64(len(b)), Mode: mode, Data: b})
		return nil
	})
	if err != nil {
		return s, err
	}
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	found := false
	for _, f := range s.Files {
		if f.Path == "SKILL.md" {
			s.Metadata, err = Metadata(f.Data)
			found = true
			break
		}
	}
	if !found {
		return s, errs.Validation("META_MISSING", "SKILL.md")
	}
	if err != nil {
		return s, err
	}
	if strict {
		for _, f := range s.Findings {
			if f.Status == "unscanned" {
				return s, errs.New(6, "Strict validation rejects unscanned content.")
			}
		}
	}
	return s, nil
}

// CheckParents rejects any existing link/reparse-point ancestor, including the root.
func CheckParents(p string) error {
	p, err := filepath.Abs(p)
	if err != nil {
		return errs.New(6, "Invalid local path.")
	}
	for {
		info, e := os.Lstat(p)
		if e == nil {
			if e = CheckFile(p, info); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return errs.New(6, "Cannot inspect local path.")
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return nil
}
