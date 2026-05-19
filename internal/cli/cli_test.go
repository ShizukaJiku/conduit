package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// Register the real features + sftp driver so the command tree and
	// config resolution exercise the production wiring.
	_ "github.com/ShizukaJiku/conduit/internal/feature/download"
	_ "github.com/ShizukaJiku/conduit/internal/feature/watch"
	_ "github.com/ShizukaJiku/conduit/internal/storage/sftp"
)

func hermeticHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
}

func TestFeaturesCommandLists(t *testing.T) {
	hermeticHome(t)
	r := newRoot()
	var out bytes.Buffer
	r.SetOut(&out)
	r.SetErr(&out)
	r.SetArgs([]string{"features"})
	if err := r.Execute(); err != nil {
		t.Fatalf("features: %v", err)
	}
	s := out.String()
	for _, want := range []string{"watch", "download"} {
		if !strings.Contains(s, want) {
			t.Errorf("features output missing %q:\n%s", want, s)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	hermeticHome(t)
	r := newRoot()
	var out bytes.Buffer
	r.SetOut(&out)
	r.SetArgs([]string{"version"})
	if err := r.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out.String(), "conduit") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestReadOnlyCommandsSkipConfigAndMigration(t *testing.T) {
	hermeticHome(t)
	// version and features skip the root PersistentPreRunE: they must not
	// migrate, must not resolve config, and must not fail for config
	// reasons even with a bogus --config.
	for _, args := range [][]string{
		{"--config", "/no/such/conduit.toml", "features"},
		{"--config", "/no/such/conduit.toml", "version"},
	} {
		r := newRoot()
		var out bytes.Buffer
		r.SetOut(&out)
		r.SetErr(&out)
		r.SetArgs(args)
		if err := r.Execute(); err != nil {
			t.Errorf("%v must not fail for config reasons: %v", args, err)
		}
	}
}

func TestPersistentPreRunMigratesLegacyOnFirstRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	legacy := filepath.Join(home, ".sftpwatcher", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty local_folder → after migration+resolve the root
	// PersistentPreRunE runs (watch has none of its own), then watch RunE
	// returns the validation error fast (no engine, no hang).
	const js = `{"host":"h","port":22,"username":"u","password":"p","remote_folder":"/","local_folder":""}`
	if err := os.WriteFile(legacy, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newRoot()
	var out bytes.Buffer
	r.SetOut(&out)
	r.SetErr(&out)
	r.SetArgs([]string{"watch"})
	err := r.Execute()
	if err == nil {
		t.Fatal("watch with empty local_folder must error after migration")
	}
	if _, serr := os.Stat(filepath.Join(home, ".conduit", "config.toml")); serr != nil {
		t.Errorf("legacy config must have been migrated: %v", serr)
	}
	if !strings.Contains(out.String(), "config migrada") {
		t.Errorf("expected migration notice on stderr, got: %q", out.String())
	}
}

func TestHelpListsPluginsAndCoreCommands(t *testing.T) {
	hermeticHome(t)
	r := newRoot()
	var out bytes.Buffer
	r.SetOut(&out)
	r.SetArgs([]string{"--help"})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"watch", "download", "version", "features"} {
		if !strings.Contains(s, want) {
			t.Errorf("--help missing %q", want)
		}
	}
}
