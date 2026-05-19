package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend.Type != "sftp" {
		t.Errorf("backend.type default = %q, want sftp", c.Backend.Type)
	}
	if c.Backend.SFTP.Port != 22 {
		t.Errorf("sftp.port default = %d, want 22", c.Backend.SFTP.Port)
	}
	if c.PollSeconds != 15 {
		t.Errorf("poll_seconds default = %d, want 15", c.PollSeconds)
	}
	if c.RemoteFolder != "/" {
		t.Errorf("remote_folder default = %q, want /", c.RemoteFolder)
	}
}

func TestMissingExplicitFileIsNotError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err != nil {
		t.Fatalf("missing file should fall back to defaults, got %v", err)
	}
}

func TestFileLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	const body = `
local_folder = "C:/data"
poll_seconds = 30

[backend]
type = "sftp"

[backend.sftp]
host = "files.example.com"
user = "alice"
port = 2222
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.LocalFolder != "C:/data" || c.PollSeconds != 30 {
		t.Errorf("top-level not loaded: %+v", c)
	}
	if c.Backend.SFTP.Host != "files.example.com" || c.Backend.SFTP.User != "alice" || c.Backend.SFTP.Port != 2222 {
		t.Errorf("sftp section not loaded: %+v", c.Backend.SFTP)
	}
}

func TestDefaultPathAndLoadDefault(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "config.toml" || filepath.Base(filepath.Dir(p)) != ".conduit" {
		t.Errorf("DefaultPath = %q, want ~/.conduit/config.toml", p)
	}
	// LoadDefault must succeed even if the file does not exist (defaults).
	c, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if c.Backend.Type != "sftp" || c.PollSeconds != 15 {
		t.Errorf("LoadDefault defaults wrong: %+v", c)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[backend.sftp]\nhost = \"from-file\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUIT_BACKEND_SFTP_HOST", "from-env")
	t.Setenv("CONDUIT_POLL_SECONDS", "7")

	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend.SFTP.Host != "from-env" {
		t.Errorf("env must override file: host = %q, want from-env", c.Backend.SFTP.Host)
	}
	if c.PollSeconds != 7 {
		t.Errorf("env poll_seconds = %d, want 7", c.PollSeconds)
	}
}
