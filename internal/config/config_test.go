package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/pflag"
)

func testFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("conduit", pflag.ContinueOnError)
	fs.String("config", "", "")
	fs.String("backend", "", "")
	fs.String("log-file", "", "")
	fs.Bool("verbose", false, "")
	fs.Bool("insecure-host-key", false, "")
	return fs
}

func TestResolveFlagBeatsEnvAndFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte("[backend]\ntype = \"sftp\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUIT_BACKEND_TYPE", "memfs")

	fs := testFlags()
	if err := fs.Parse([]string{"--config", p, "--backend", "sftp"}); err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(fs)
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend.Type != "sftp" {
		t.Errorf("flag must beat env+file: backend.type = %q, want sftp", c.Backend.Type)
	}
}

func TestResolveUnchangedFlagDoesNotClobberEnv(t *testing.T) {
	t.Setenv("CONDUIT_POLL_SECONDS", "9")
	fs := testFlags() // no flags changed
	c, err := Resolve(fs)
	if err != nil {
		t.Fatal(err)
	}
	if c.PollSeconds != 9 {
		t.Errorf("unchanged flag must not clobber env: poll = %d, want 9", c.PollSeconds)
	}
}

func TestConduitPasswordAlias(t *testing.T) {
	t.Setenv("CONDUIT_PASSWORD", "s3cr3t")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend.SFTP.Password != "s3cr3t" {
		t.Errorf("CONDUIT_PASSWORD alias not applied: %q", c.Backend.SFTP.Password)
	}
	if !PasswordFromEnv() {
		t.Error("PasswordFromEnv must be true when CONDUIT_PASSWORD is set")
	}
}

func TestMigrateLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	legacy := filepath.Join(home, ".sftpwatcher", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	const js = `{"host":"h.example.com","port":2222,"username":"alice","password":"p@ss\"q","remote_folder":"/data","local_folder":"C:/x"}`
	if err := os.WriteFile(legacy, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, from, to, err := MigrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || from != legacy {
		t.Fatalf("migrated=%v from=%q", migrated, from)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(to); fi != nil && fi.Mode().Perm() != 0o600 {
			t.Errorf("migrated config perms = %o, want 600 (contains password)", fi.Mode().Perm())
		}
	}
	c, err := Load(to)
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend.SFTP.Host != "h.example.com" || c.Backend.SFTP.Port != 2222 ||
		c.Backend.SFTP.User != "alice" || c.Backend.SFTP.Password != `p@ss"q` ||
		c.RemoteFolder != "/data" || c.LocalFolder != "C:/x" {
		t.Fatalf("legacy fields not mapped: %+v / %+v", *c, c.Backend.SFTP)
	}

	// Idempotent: conduit config now exists → no re-migration.
	again, _, _, err := MigrateLegacy()
	if err != nil || again {
		t.Errorf("second MigrateLegacy must be a no-op, got migrated=%v err=%v", again, err)
	}
}

// TestTomlBasicStringRoundTrip is a property test: whatever
// tomlBasicString emits must parse back (via the real TOML loader) to the
// original value. This avoids brittle hardcoded \uXXXX expectations and
// directly proves the migrated config is loadable.
func TestTomlBasicStringRoundTrip(t *testing.T) {
	inputs := []string{
		"plain",
		`a"b`,
		`a\b`,
		"tab\there",
		"newline\nhere",
		"cr\rhere",
		"form\ffeed",
		"back\bspace",
		"bell" + string(rune(0x07)) + "x", // not a TOML escape → \uXXXX
		"del" + string(rune(0x7f)),
		"unicode-ñé€-日本",
		`mix "q" \s ` + string(rune(0x01)) + "\t end",
	}
	for _, in := range inputs {
		p := filepath.Join(t.TempDir(), "c.toml")
		body := "[backend.sftp]\npassword = " + tomlBasicString(in) + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		if err != nil {
			t.Fatalf("encoded %q produced invalid TOML (%s): %v", in, body, err)
		}
		if c.Backend.SFTP.Password != in {
			t.Errorf("round-trip mismatch: in=%q out=%q (toml=%s)", in, c.Backend.SFTP.Password, body)
		}
	}
}

func TestMigrateLegacyExoticPasswordRoundTrips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".sftpwatcher", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	// Password with quote, backslash, tab and a control char that
	// strconv.Quote would have rendered as an invalid TOML escape.
	pw := "p\"a\\s\tword" + string(rune(0x07))
	js, _ := json.Marshal(legacyJSON{Host: "h", Port: 22, Username: "u", Password: pw, LocalFolder: "L"})
	if err := os.WriteFile(legacy, js, 0o644); err != nil {
		t.Fatal(err)
	}
	migrated, _, to, err := MigrateLegacy()
	if err != nil || !migrated {
		t.Fatalf("migrate: migrated=%v err=%v", migrated, err)
	}
	c, err := Load(to) // must parse as valid TOML and round-trip the password
	if err != nil {
		t.Fatalf("migrated TOML must be valid: %v", err)
	}
	if c.Backend.SFTP.Password != pw {
		t.Errorf("password round-trip = %q, want %q", c.Backend.SFTP.Password, pw)
	}
}

func TestMigrateLegacyNoLegacyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	migrated, _, _, err := MigrateLegacy()
	if err != nil || migrated {
		t.Errorf("no legacy file → no migration; got migrated=%v err=%v", migrated, err)
	}
}

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
	if c.Screen.Hotkey != "ctrl+shift+s" {
		t.Errorf("screen.hotkey default = %q, want ctrl+shift+s", c.Screen.Hotkey)
	}
	if c.Screen.Dir != "screenshot" {
		t.Errorf("screen.dir default = %q, want screenshot", c.Screen.Dir)
	}
}

func TestScreenEnvOverride(t *testing.T) {
	t.Setenv("CONDUIT_SCREEN_HOTKEY", "ctrl+alt+p")
	t.Setenv("CONDUIT_SCREEN_DIR", "shots")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Screen.Hotkey != "ctrl+alt+p" {
		t.Errorf("CONDUIT_SCREEN_HOTKEY not applied: %q", c.Screen.Hotkey)
	}
	if c.Screen.Dir != "shots" {
		t.Errorf("CONDUIT_SCREEN_DIR not applied: %q", c.Screen.Dir)
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
