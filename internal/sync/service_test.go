package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rush1994/SkillPort/internal/config"
	"github.com/Rush1994/SkillPort/internal/errs"
	regclient "github.com/Rush1994/SkillPort/internal/registry"
	"github.com/Rush1994/SkillPort/internal/testregistry"
	"oras.land/oras-go/v2/registry/remote/auth"
)

func writeSkill(t *testing.T, dir string) {
	t.Helper()
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: test\ndescription: fixture\n---\nBody\n"), 0600); e != nil {
		t.Fatal(e)
	}
}
func code(t *testing.T, e error, want int) {
	t.Helper()
	var v *errs.Error
	if !errors.As(e, &v) || v.Code != want {
		t.Fatalf("expected %d: %v", want, e)
	}
}
func TestConflictsBackupsAndState(t *testing.T) {
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
	os.WriteFile(filepath.Join(src, "note.txt"), []byte("old"), 0600)
	if _, e = Push(ctx, repo, src, "v1", false, false); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(root, "target")
	if _, e = Pull(ctx, repo, "v1", dest, false, false); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(src, "note.txt"), []byte("new"), 0600)
	if _, e = Push(ctx, repo, src, "v2", false, false); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dest, ".env"), []byte("inert local file"), 0600)
	_, e = Pull(ctx, repo, "v2", dest, false, false)
	code(t, e, 7)
	b, _ := os.ReadFile(filepath.Join(dest, "note.txt"))
	if string(b) != "old" {
		t.Fatal("local data overwritten")
	}
	os.Remove(filepath.Join(dest, ".env"))
	result, e := Pull(ctx, repo, "v2", dest, false, false)
	if e != nil {
		t.Fatal(e)
	}
	if result.Backup == "" {
		t.Fatal("old target not backed up")
	}
	b, _ = os.ReadFile(filepath.Join(result.Backup, "note.txt"))
	if string(b) != "old" {
		t.Fatal("backup lost old bytes")
	}
	state, e := Load(dest)
	if e != nil || state.Tag != "v2" {
		t.Fatalf("bad state %v", e)
	}
	state.Files[0].Path = "../outside"
	b, _ = json.Marshal(state)
	os.WriteFile(filepath.Join(dest, ".skillport", "state.json"), b, 0600)
	_, e = Load(dest)
	code(t, e, 7)
	untracked := filepath.Join(root, "untracked")
	writeSkill(t, untracked)
	_, e = Pull(ctx, repo, "v1", untracked, false, false)
	code(t, e, 7)
}
func TestLockAndMetadataDeletionStatus(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test")
	writeSkill(t, dir)
	unlock, e := Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	_, e = Lock(dir)
	code(t, e, 7)
	unlock()
	unlock, e = Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	unlock()
}
