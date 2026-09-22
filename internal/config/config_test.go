package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigRoundTripAndPolicy(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	c, e := Load(file)
	if e != nil {
		t.Fatal(e)
	}
	r, e := (Registry{Endpoint: "http://EXAMPLE.test:8080", Project: "skills"}).Validate()
	if e != nil || r.Endpoint != "http://example.test:8080" {
		t.Fatal(e)
	}
	c.Registries["team"] = r
	c.Default = "team"
	if e = Save(file, c); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(file)
	if e != nil || loaded.Default != "team" {
		t.Fatal(e)
	}
	loaded.Default = ""
	if e = Save(file, loaded); e != nil {
		t.Fatal(e)
	}
	for _, r := range []Registry{{Endpoint: "http://host", Project: "skills", Insecure: true}, {Endpoint: "http://host", Project: "skills", CAFile: "ca.pem"}, {Endpoint: "https://user:pass@host", Project: "skills"}, {Endpoint: "https://host/path", Project: "skills"}} {
		if _, e = r.Validate(); e == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	os.WriteFile(file, []byte(`{"schemaVersion":1,"registries":{"team":{"endpoint":"https://example.test","project":"skills","caFile":"ca.pem"}}}`), 0600)
	loaded, e = Load(file)
	if e != nil || loaded.Registries["team"].CAFile != filepath.Join(filepath.Dir(file), "ca.pem") {
		t.Fatal("relative CA not resolved")
	}
}

func TestRenamedDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SKILLPORT_CONFIG", "")
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	base, e := os.UserConfigDir()
	if e != nil {
		t.Fatal(e)
	}
	modern := filepath.Join(base, "skillport", "config.json")
	legacy := filepath.Join(base, "skillctl", "config.json")
	if DefaultPath() != modern {
		t.Fatal("wrong new config location")
	}
	os.MkdirAll(filepath.Dir(legacy), 0700)
	os.WriteFile(legacy, []byte(`{}`), 0600)
	if DefaultPath() != legacy {
		t.Fatal("legacy config not found")
	}
	os.MkdirAll(filepath.Dir(modern), 0700)
	os.WriteFile(modern, []byte(`{}`), 0600)
	if DefaultPath() != modern {
		t.Fatal("new config not preferred")
	}
	t.Setenv("SKILLPORT_CONFIG", filepath.Join(dir, "custom.json"))
	if DefaultPath() != filepath.Join(dir, "custom.json") {
		t.Fatal("explicit config lost priority")
	}
}
