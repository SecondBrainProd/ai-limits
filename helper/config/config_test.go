package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	c, err := loadFrom(filepath.Join(t.TempDir(), "none", "config.json"))
	if err != nil || c.Relay != DefaultRelay || c.HelperToken != "" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSaveLoadRoundTripAndModes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ailimits", "config.json")
	want := Config{Relay: "https://r.example", HelperToken: "tok"}
	if err := saveTo(p, want); err != nil {
		t.Fatal(err)
	}
	if err := saveTo(p, want); err != nil { // overwrite works
		t.Fatal(err)
	}
	got, err := loadFrom(p)
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		di, _ := os.Stat(filepath.Dir(p))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("temp files left: %v", ents)
	}
}

func TestLoadBadJSONDoesNotLeak(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"helper_token":"SECRET`), 0o600)
	_, err := loadFrom(p)
	if err == nil || len(err.Error()) > 60 {
		t.Fatal(err)
	}
}
