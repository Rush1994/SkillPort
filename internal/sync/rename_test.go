package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rush1994/SkillPort/internal/config"
	regclient "github.com/Rush1994/SkillPort/internal/registry"
	"github.com/Rush1994/SkillPort/internal/skill"
	"github.com/Rush1994/SkillPort/internal/testregistry"
	"github.com/gofrs/flock"
	"github.com/opencontainers/image-spec/specs-go"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestRenameReadsLegacyArtifactAndState(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(testregistry.New())
	defer server.Close()
	repo, e := regclient.New(config.Registry{Endpoint: server.URL, Project: "skills"}, "skills/test", auth.EmptyCredential)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	src := filepath.Join(root, "source")
	writeSkill(t, src)
	s, e := skill.Scan(src, false)
	if e != nil {
		t.Fatal(e)
	}
	layer, e := skill.Pack(s)
	if e != nil {
		t.Fatal(e)
	}
	cfg, _ := json.Marshal(s.Metadata)
	cd, ld := descriptor(skill.LegacyConfigMediaType, cfg), descriptor(skill.LegacyLayerMediaType, layer)
	for _, item := range []struct {
		d oci.Descriptor
		b []byte
	}{{cd, cfg}, {ld, layer}} {
		if e = repo.Push(ctx, item.d, bytes.NewReader(item.b)); e != nil {
			t.Fatal(e)
		}
	}
	m := oci.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: oci.MediaTypeImageManifest, ArtifactType: skill.LegacyArtifactType, Config: cd, Layers: []oci.Descriptor{ld}}
	mb, _ := json.Marshal(m)
	md := descriptor(oci.MediaTypeImageManifest, mb)
	if e = repo.PushReference(ctx, md, bytes.NewReader(mb), "v1"); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(root, "download")
	if _, e = Pull(ctx, repo, "v1", dest, false, false); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(filepath.Join(dest, skill.StateDirectory), filepath.Join(dest, skill.LegacyStateDirectory)); e != nil {
		t.Fatal(e)
	}
	if state, e := Load(dest); e != nil || state == nil || state.Digest != md.Digest.String() {
		t.Fatalf("legacy state not recognized: %v", e)
	}
	if _, e = Push(ctx, repo, dest, "v2", false, false); e != nil {
		t.Fatal(e)
	}
	_, manifest, _, e := Inspect(ctx, repo, "v2")
	if e != nil || manifest.ArtifactType != skill.ArtifactType {
		t.Fatalf("new namespace not published: %v", e)
	}
	if _, e = os.Stat(filepath.Join(dest, skill.StateDirectory, "state.json")); e != nil {
		t.Fatal("new state missing")
	}
	s, e = skill.Scan(dest, false)
	if e != nil || len(s.Files) != 1 {
		t.Fatalf("control metadata entered package: %v", e)
	}
	working, e := skill.WorkingFiles(dest)
	if e != nil || len(working) != 1 {
		t.Fatal("legacy control data treated as local change")
	}
	if skill.SupportedFormat(skill.ArtifactType, skill.LegacyConfigMediaType, skill.LayerMediaType) {
		t.Fatal("mixed media types accepted")
	}
}

func TestRenameHonorsLegacyDirectoryLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "source")
	writeSkill(t, dir)
	abs, _ := filepath.Abs(dir)
	legacy := flock.New(filepath.Join(filepath.Dir(abs), skill.LegacyStateDirectory+"-lock-"+skill.Hash([]byte(strings.ToLower(abs)))[:24]))
	ok, e := legacy.TryLock()
	if e != nil || !ok {
		t.Fatal("cannot acquire test lock")
	}
	defer legacy.Close()
	defer legacy.Unlock()
	_, e = Lock(dir)
	code(t, e, 7)
}
