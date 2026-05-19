package cli

import (
	"bytes"
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

func TestPersistentPreRunResolvesConfig(t *testing.T) {
	hermeticHome(t)
	r := newRoot()
	var out bytes.Buffer
	r.SetOut(&out)
	r.SetErr(&out)
	// --config points to a missing file (tolerated → defaults); the
	// PersistentPreRunE must still resolve without error.
	r.SetArgs([]string{"--config", "/no/such/conduit.toml", "features"})
	if err := r.Execute(); err != nil {
		t.Fatalf("resolve with missing --config must not fail: %v", err)
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
