package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestMetadata(t *testing.T) {
	for _, test := range []struct {
		name, header string
		valid        bool
	}{
		{"valid", "name: hello\ndescription: good\nmetadata:\n  extra: true", true},
		{"duplicate", "name: hello\nname: other\ndescription: good", false},
		{"missing", "description: good", false},
		{"number", "name: hello\ndescription: 123", false},
		{"alias", "name: &n hello\ndescription: *n", false},
		{"tag", "name: hello\ndescription: !execute data", false},
		{"double-hyphen", "name: a--b\ndescription: good", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Metadata([]byte("\xef\xbb\xbf---\n" + test.header + "\n---\nBody\n"))
			if (err == nil) != test.valid {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}
func TestPaths(t *testing.T) {
	for _, p := range []string{"../evil", "/evil", `C:\evil`, "a/../../b", "a:stream", "NUL.txt", "foo./bar", "a\\b", "COM1", "a\nsecret", "a//b"} {
		if SafePath(p) {
			t.Errorf("accepted %q", p)
		}
	}
	if !SafePath("中文 空格/note.txt") {
		t.Fatal("rejected valid path")
	}
}
func TestDeterministicArchive(t *testing.T) {
	s, e := Scan(filepath.Join("..", "..", "testdata", "skills", "valid", "hello-skill"), false)
	if e != nil {
		t.Fatal(e)
	}
	a, e := Pack(s)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Pack(s)
	if e != nil || !bytes.Equal(a, b) {
		t.Fatal("nondeterministic archive")
	}
	dir := t.TempDir()
	if e = Extract(a, dir); e != nil {
		t.Fatal(e)
	}
	for _, f := range s.Files {
		b, e := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if e != nil || !bytes.Equal(b, f.Data) {
			t.Fatalf("bytes changed: %s", f.Path)
		}
	}
}
func TestMaliciousArchives(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", ".skillport/state.json", "secret.pem", "NUL.txt"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			z := gzip.NewWriter(&b)
			tr := tar.NewWriter(z)
			tr.WriteHeader(&tar.Header{Name: name, Size: 1, Mode: 0644})
			tr.Write([]byte("x"))
			tr.Close()
			z.Close()
			if e := Extract(b.Bytes(), t.TempDir()); e == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
}
func TestSensitiveAndExclusions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: test\ndescription: good\n---\nBody\n"), 0600)
	os.WriteFile(filepath.Join(root, ".env"), []byte("inert data"), 0600)
	s, e := Scan(root, false)
	if e != nil || len(s.Findings) != 1 || s.Findings[0].Status != "excluded" {
		t.Fatalf("bad exclusion: %+v %v", s, e)
	}
	os.WriteFile(filepath.Join(root, "leak.txt"), []byte("password = fixture-not-a-real-secret"), 0600)
	_, e = Scan(root, false)
	if e == nil || bytes.Contains([]byte(e.Error()), []byte("fixture-not-a-real-secret")) {
		t.Fatalf("bad secret handling: %v", e)
	}
}

func TestArchiveCaseParentsAndChecksum(t *testing.T) {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	tr := tar.NewWriter(z)
	for _, name := range []string{"Foo/a.txt", "foo/b.txt"} {
		tr.WriteHeader(&tar.Header{Name: name, Size: 1, Mode: 0644})
		tr.Write([]byte("x"))
	}
	tr.Close()
	z.Close()
	if e := Extract(b.Bytes(), t.TempDir()); e == nil {
		t.Fatal("case-colliding parents accepted")
	}
	s, e := Scan(filepath.Join("..", "..", "testdata", "skills", "valid", "hello-skill"), false)
	if e != nil {
		t.Fatal(e)
	}
	data, e := Pack(s)
	if e != nil {
		t.Fatal(e)
	}
	data[len(data)-8] ^= 0xff
	if e = Extract(data, t.TempDir()); e == nil {
		t.Fatal("corrupted gzip accepted")
	}
}
func TestFileLimitAndHardlink(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: test\ndescription: test\n---\nBody\n"), 0600)
	p := filepath.Join(dir, "large.bin")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	e = f.Truncate(MaxFile)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Scan(dir, false); e != nil {
		t.Fatalf("exact limit rejected: %v", e)
	}
	f, _ = os.OpenFile(p, os.O_WRONLY, 0600)
	f.Truncate(MaxFile + 1)
	f.Close()
	if _, e = Scan(dir, false); e == nil {
		t.Fatal("oversize file accepted")
	}
	os.Remove(p)
	if e = os.Link(filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "hardlink")); e != nil {
		t.Skip("filesystem does not support hardlink fixture")
	}
	if _, e = Scan(dir, false); e == nil {
		t.Fatal("hardlink accepted")
	}
}
