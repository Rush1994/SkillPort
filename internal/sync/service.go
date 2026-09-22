package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Rush1994/SkillPort/internal/errs"
	regclient "github.com/Rush1994/SkillPort/internal/registry"
	"github.com/Rush1994/SkillPort/internal/skill"
	"github.com/gofrs/flock"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
)

type State struct {
	SchemaVersion int               `json:"schemaVersion"`
	Registry      string            `json:"registry"`
	Repository    string            `json:"repository"`
	Tag           string            `json:"tag,omitempty"`
	Digest        string            `json:"digest"`
	SyncedAt      string            `json:"syncedAt"`
	Files         []skill.File      `json:"files"`
	Baseline      map[string][]byte `json:"baseline"`
}
type Result struct {
	Connection *regclient.ConnectionInfo `json:"connection,omitempty"`
	Reference  string                    `json:"reference"`
	Digest     string                    `json:"digest"`
	Directory  string                    `json:"directory,omitempty"`
	Status     string                    `json:"status"`
	Files      int                       `json:"files"`
	Size       int64                     `json:"size"`
	Findings   []skill.Finding           `json:"findings,omitempty"`
	Backup     string                    `json:"backup,omitempty"`
}

func Load(dir string) (*State, error) {
	stateDir := skill.StateDirectory
	if err := skill.CheckParents(filepath.Join(dir, stateDir)); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, stateDir, "state.json")
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		stateDir = skill.LegacyStateDirectory
		if err := skill.CheckParents(filepath.Join(dir, stateDir)); err != nil {
			return nil, err
		}
		p = filepath.Join(dir, stateDir, "state.json")
		info, err = os.Lstat(p)
	}
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.New(7, "Cannot inspect synchronization state.")
	}
	if err = skill.CheckFile(p, info); err != nil {
		return nil, err
	}
	if info.Size() > 80<<20 {
		return nil, errs.New(7, "Synchronization state is too large.")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, errs.New(7, "Cannot read synchronization state.")
	}
	var s State
	if json.Unmarshal(b, &s) != nil || s.SchemaVersion != 1 || s.Registry == "" || s.Repository == "" || digest.Digest(s.Digest).Validate() != nil {
		return nil, errs.New(7, "Invalid synchronization state.")
	}
	seen := map[string]bool{}
	var total int64
	if len(s.Files) > skill.MaxEntries || len(s.Files) != len(s.Baseline) {
		return nil, errs.New(7, "Invalid baseline file count.")
	}
	for _, f := range s.Files {
		b, ok := s.Baseline[f.Path]
		key := strings.ToLower(f.Path)
		total += int64(len(b))
		if !skill.SafePath(f.Path) || skill.Excluded(f.Path) != "" || seen[key] || !ok || skill.Hash(b) != f.Hash || int64(len(b)) != f.Size || len(b) > skill.MaxFile || total > skill.MaxTotal || (f.Mode != 0644 && f.Mode != 0755) {
			return nil, errs.New(7, "Invalid baseline path or hash.")
		}
		seen[key] = true
	}
	return &s, nil
}
func save(dir string, s State) error {
	parent := filepath.Join(dir, skill.StateDirectory)
	if err := skill.CheckParents(parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return errs.New(1, "Cannot create synchronization state directory.")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(parent, "state-*")
	if err != nil {
		return errs.New(1, "Cannot stage synchronization state.")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return errs.New(1, "Cannot save synchronization state.")
	}
	return errs.Wrap(1, "Cannot commit synchronization state.", os.Rename(tmp, filepath.Join(parent, "state.json")))
}
func newState(repo *remote.Repository, tag string, d oci.Descriptor, s skill.Snapshot) State {
	state := State{SchemaVersion: 1, Registry: repo.Reference.Registry, Repository: repo.Reference.Repository, Tag: tag, Digest: d.Digest.String(), SyncedAt: time.Now().UTC().Format(time.RFC3339), Files: s.Files, Baseline: map[string][]byte{}}
	for _, f := range s.Files {
		state.Baseline[f.Path] = f.Data
	}
	return state
}
func Lock(dir string) (func(), error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, errs.New(2, "Invalid directory.")
	}
	if err = skill.CheckParents(abs); err != nil {
		return nil, err
	}
	parent := filepath.Dir(abs)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return nil, errs.New(1, "Cannot create target parent.")
	}
	var locks []*flock.Flock
	release := func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].Unlock()
			_ = locks[i].Close()
		}
	}
	// Hold the legacy lock too so an older binary cannot race the migration.
	for _, prefix := range []string{skill.StateDirectory, skill.LegacyStateDirectory} {
		lockPath := filepath.Join(parent, prefix+"-lock-"+skill.Hash([]byte(strings.ToLower(abs)))[:24])
		if err = skill.CheckParents(lockPath); err != nil {
			release()
			return nil, err
		}
		l := flock.New(lockPath)
		ok, e := l.TryLock()
		if e != nil || !ok {
			_ = l.Close()
			release()
			return nil, errs.New(7, "Another operation holds this directory lock.")
		}
		locks = append(locks, l)
	}
	return release, nil
}
func descriptor(media string, b []byte) oci.Descriptor {
	return oci.Descriptor{MediaType: media, Digest: digest.FromBytes(b), Size: int64(len(b))}
}
func remoteDigest(ctx context.Context, r *remote.Repository, ref string) (oci.Descriptor, error) {
	d, e := r.Resolve(ctx, ref)
	if errors.Is(e, errdef.ErrNotFound) {
		return oci.Descriptor{}, nil
	}
	return d, e
}
func Push(ctx context.Context, r *remote.Repository, dir, tag string, strict, dry bool) (Result, error) {
	result := Result{Reference: r.Reference.Registry + "/" + r.Reference.Repository + ":" + tag}
	if tag == "" || !regexp.MustCompile(`^[\w][\w.-]{0,127}$`).MatchString(tag) {
		return result, errs.New(2, "Push requires an explicit valid tag.")
	}
	unlock, err := Lock(dir)
	if err != nil {
		return result, err
	}
	defer unlock()
	s, err := skill.Scan(dir, strict)
	if err != nil {
		return result, err
	}
	if path.Base(r.Reference.Repository) != s.Metadata.Name {
		return result, errs.New(6, "Repository name must match SKILL.md name.")
	}
	if regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:[-+].*)?$`).MatchString(tag) && s.Metadata.Version != "" && s.Metadata.Version != tag {
		return result, errs.New(6, "SemVer tag must match metadata version.")
	}
	state, err := Load(dir)
	if err != nil {
		return result, err
	}
	// Preserve executable bits from a Windows pull, where filesystem modes cannot encode them.
	if state != nil {
		modes := map[string]int64{}
		for _, f := range state.Files {
			modes[f.Path] = f.Mode
		}
		preserveModes(&s, modes)
	}
	layer, err := skill.Pack(s)
	if err != nil {
		return result, err
	}
	cfg, _ := json.Marshal(s.Metadata)
	ld, cd := descriptor(skill.LayerMediaType, layer), descriptor(skill.ConfigMediaType, cfg)
	m := oci.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: oci.MediaTypeImageManifest, ArtifactType: skill.ArtifactType, Config: cd, Layers: []oci.Descriptor{ld}}
	mb, _ := json.Marshal(m)
	md := descriptor(oci.MediaTypeImageManifest, mb)
	result.Digest = md.Digest.String()
	result.Files = len(s.Files)
	result.Size = s.Size
	result.Findings = s.Findings
	if dry {
		result.Status = "dry-run; remote conflict not checked"
		return result, nil
	}
	before, err := remoteDigest(ctx, r, tag)
	if err != nil {
		return result, regclient.Classify(err)
	}
	if before.Digest != md.Digest && before.Digest != "" {
		return result, errs.New(7, "Tag already exists with different content; publish a new tag. Use Harbor immutable tags for strict concurrency protection.")
	}
	if state != nil && state.Registry == r.Reference.Registry && state.Repository == r.Reference.Repository && state.Tag == tag && before.Digest == "" {
		return result, errs.New(7, "Tracked tag was deleted; publish a new tag.")
	}
	result.Status = "unchanged"
	if before.Digest != md.Digest {
		for _, blob := range []struct {
			d oci.Descriptor
			b []byte
		}{{cd, cfg}, {ld, layer}} {
			if err = r.Push(ctx, blob.d, bytes.NewReader(blob.b)); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
				return result, regclient.Classify(err)
			}
		}
		now, err := remoteDigest(ctx, r, tag)
		if err != nil {
			return result, regclient.Classify(err)
		}
		if now.Digest != before.Digest {
			return result, errs.New(7, "Tag changed while uploading; publish another unique tag.")
		}
		if err = r.PushReference(ctx, md, bytes.NewReader(mb), tag); err != nil {
			return result, regclient.Classify(err)
		}
		result.Status = "published"
	}
	if err = save(dir, newState(r, tag, md, s)); err != nil {
		result.Status = "published-state-failed"
		return result, errs.New(1, "Remote artifact is published at "+md.Digest.String()+" but local baseline could not be saved.")
	}
	return result, nil
}
func fetch(ctx context.Context, r *remote.Repository, d oci.Descriptor, limit int64) ([]byte, error) {
	if d.Size < 0 || d.Size > limit || d.Digest.Algorithm() != digest.SHA256 || d.Digest.Validate() != nil {
		return nil, errs.New(6, "Invalid descriptor or size limit exceeded.")
	}
	stream, err := r.Fetch(ctx, d)
	if err != nil {
		return nil, regclient.Classify(err)
	}
	defer stream.Close()
	b, err := io.ReadAll(io.LimitReader(stream, limit+1))
	if err != nil {
		return nil, errs.New(5, "Download interrupted.")
	}
	if int64(len(b)) != d.Size || int64(len(b)) > limit || digest.FromBytes(b) != d.Digest {
		return nil, errs.New(6, "Downloaded size/digest mismatch.")
	}
	return b, nil
}
func Inspect(ctx context.Context, r *remote.Repository, ref string) (oci.Descriptor, oci.Manifest, skill.Config, error) {
	d, err := r.Resolve(ctx, ref)
	if err != nil {
		return d, oci.Manifest{}, skill.Config{}, regclient.Classify(err)
	}
	if d.MediaType != oci.MediaTypeImageManifest {
		return d, oci.Manifest{}, skill.Config{}, errs.New(6, "Not a supported Skill manifest.")
	}
	b, err := fetch(ctx, r, d, 1<<20)
	if err != nil {
		return d, oci.Manifest{}, skill.Config{}, err
	}
	var m oci.Manifest
	if json.Unmarshal(b, &m) != nil || m.SchemaVersion != 2 || m.MediaType != oci.MediaTypeImageManifest || len(m.Layers) != 1 || !skill.SupportedFormat(m.ArtifactType, m.Config.MediaType, m.Layers[0].MediaType) {
		return d, m, skill.Config{}, errs.New(6, "Unsupported Skill artifact format.")
	}
	b, err = fetch(ctx, r, m.Config, 1<<20)
	var c skill.Config
	if err != nil {
		return d, m, c, err
	}
	if json.Unmarshal(b, &c) != nil || c.SchemaVersion != 1 || !skill.ValidName(c.Name) || path.Base(r.Reference.Repository) != c.Name {
		return d, m, c, errs.New(6, "Invalid Skill config.")
	}
	return d, m, c, nil
}
func Pull(ctx context.Context, r *remote.Repository, ref, dir string, strict, dry bool) (Result, error) {
	separator := ":"
	if strings.HasPrefix(ref, "sha256:") {
		separator = "@"
	}
	result := Result{Reference: r.Reference.Registry + "/" + r.Reference.Repository + separator + ref}
	d, m, c, err := Inspect(ctx, r, ref)
	if err != nil {
		return result, err
	}
	result.Digest = d.Digest.String()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return result, errs.New(2, "Invalid target.")
	}
	result.Directory = abs
	unlock, err := Lock(abs)
	if err != nil {
		return result, err
	}
	defer unlock()
	state, err := Load(abs)
	if err != nil {
		return result, err
	}
	entries, e := os.ReadDir(abs)
	if e != nil && !os.IsNotExist(e) {
		return result, errs.New(7, "Cannot inspect target directory.")
	}
	if len(entries) > 0 && state == nil {
		return result, errs.New(7, "Target is nonempty and untracked; use another target directory.")
	}
	if state != nil {
		if state.Registry != r.Reference.Registry || state.Repository != r.Reference.Repository {
			return result, errs.New(7, "Target belongs to another source.")
		}
		current, e := skill.WorkingFiles(abs)
		if e != nil {
			return result, e
		}
		if !Equal(current, state.Files) {
			if state.Digest == d.Digest.String() {
				result.Status = "local changes preserved"
				return result, nil
			}
			return result, errs.New(7, "Local and remote content differ; use a new target to compare.")
		}
		if state.Digest == d.Digest.String() && (state.Tag == ref || (state.Tag == "" && strings.HasPrefix(ref, "sha256:"))) {
			result.Status = "unchanged"
			result.Files = len(state.Files)
			return result, nil
		}
	}
	layer, err := fetch(ctx, r, m.Layers[0], skill.MaxTotal)
	if err != nil {
		return result, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(abs), ".skillport-stage-*")
	if err != nil {
		return result, errs.New(1, "Cannot create download staging directory.")
	}
	defer os.RemoveAll(stage) // Private directory returned by MkdirTemp, never a user-provided path.
	if err = skill.Extract(layer, stage); err != nil {
		return result, err
	}
	snapshot, err := skill.Scan(stage, strict)
	if err != nil {
		return result, err
	}
	if snapshot.Metadata != c {
		return result, errs.New(6, "Config and SKILL.md metadata differ.")
	}
	modes := skill.ArchiveModes(layer)
	for i := range snapshot.Files {
		if mode, ok := modes[snapshot.Files[i].Path]; ok {
			snapshot.Files[i].Mode = mode
		}
	}
	for _, finding := range snapshot.Findings {
		if finding.Status == "excluded" {
			return result, errs.New(6, "Remote content cannot be silently excluded.")
		}
	}
	result.Files = len(snapshot.Files)
	result.Size = snapshot.Size
	result.Findings = snapshot.Findings
	if dry {
		result.Status = "dry-run"
		return result, nil
	}
	tag := ref
	if strings.HasPrefix(tag, "sha256:") {
		tag = ""
	}
	if err = save(stage, newState(r, tag, d, snapshot)); err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, errs.New(5, "Operation cancelled before local commit.")
	}
	// Move the whole old directory aside before replacing it; retain the old tree as backup.
	backup := ""
	if e == nil {
		backup = abs + ".skillport-backup-" + time.Now().UTC().Format("20060102T150405.000000000")
		if _, e = os.Lstat(backup); !os.IsNotExist(e) {
			return result, errs.New(7, "Backup path already exists or is inaccessible.")
		}
		if err = os.Rename(abs, backup); err != nil {
			return result, errs.New(7, "Cannot back up target; nothing was overwritten.")
		}
	}
	if err = os.Rename(stage, abs); err != nil {
		if backup != "" {
			if restore := os.Rename(backup, abs); restore != nil {
				return result, errs.New(1, "Commit and rollback failed; recover the directory from "+backup)
			}
		}
		return result, errs.New(1, "Cannot commit download; previous target restored.")
	}
	result.Backup = backup
	result.Status = "downloaded"
	return result, nil
}
func Equal(a, b []skill.File) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]string{}
	for _, f := range a {
		m[f.Path] = f.Hash
	}
	for _, f := range b {
		if m[f.Path] != f.Hash {
			return false
		}
	}
	return true
}

type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func Status(dir string) ([]Change, error) {
	s, err := Load(dir)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errs.New(7, "Directory is untracked.")
	}
	current, err := skill.WorkingFiles(dir)
	if err != nil {
		return nil, err
	}
	old := map[string]string{}
	for _, f := range s.Files {
		old[f.Path] = f.Hash
	}
	changes := []Change{}
	for _, f := range current {
		status := "unchanged"
		if h, ok := old[f.Path]; !ok {
			status = "added"
		} else if h != f.Hash {
			status = "modified"
		}
		changes = append(changes, Change{f.Path, status})
		delete(old, f.Path)
	}
	for p := range old {
		changes = append(changes, Change{p, "deleted"})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}
