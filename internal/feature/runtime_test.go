package feature

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/logx"
)

func TestWarnSecurity(t *testing.T) {
	t.Setenv("CONDUIT_PASSWORD", "")
	t.Setenv("CONDUIT_BACKEND_SFTP_PASSWORD", "")

	var buf bytes.Buffer
	log := logx.New(&buf)
	d := Deps{Config: &config.Config{Backend: config.Backend{SFTP: config.SFTPConfig{
		InsecureHostKey: true,
		Password:        "plain",
	}}}}
	d.WarnSecurity(log)
	out := buf.String()
	if !strings.Contains(out, "host key DESHABILITADA") {
		t.Errorf("missing D1 host-key warning: %q", out)
	}
	if !strings.Contains(out, "texto plano") {
		t.Errorf("missing D2 plaintext-password warning: %q", out)
	}
}

func TestWarnSecurityQuietWhenSafe(t *testing.T) {
	t.Setenv("CONDUIT_PASSWORD", "fromenv")
	var buf bytes.Buffer
	log := logx.New(&buf)
	// Password present but supplied via env, host-key verification on.
	d := Deps{Config: &config.Config{Backend: config.Backend{SFTP: config.SFTPConfig{Password: "x"}}}}
	d.WarnSecurity(log)
	if buf.Len() != 0 {
		t.Errorf("no warnings expected, got: %q", buf.String())
	}
	// Nil config must not panic.
	Deps{}.WarnSecurity(log)
}

func TestOpenLogExplicitFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "c.log")
	d := Deps{Config: &config.Config{LogFile: p, Verbose: true}}
	lg, closeFn := d.OpenLog()
	if lg == nil {
		t.Fatal("nil logger")
	}
	lg.Infof("hello-e2e")
	closeFn()
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "hello-e2e") {
		t.Fatalf("log not written: %q err=%v", b, err)
	}
}

func TestOpenLogDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	d := Deps{Config: &config.Config{}} // empty LogFile → default path
	lg, closeFn := d.OpenLog()
	defer closeFn()
	lg.Warnf("warn-default")
	if _, err := os.Stat(filepath.Join(home, ".conduit", "conduit.log")); err != nil {
		t.Errorf("default log not created: %v", err)
	}
}

func TestOpenLogFallbackOnBadPath(t *testing.T) {
	// Parent of the log path is a regular file → OpenLeveled fails →
	// OpenLog must fall back to the injected logger, not panic.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	d := Deps{
		Config: &config.Config{LogFile: filepath.Join(blocker, "c.log")},
		Log:    logx.New(&buf),
	}
	lg, closeFn := d.OpenLog()
	defer closeFn()
	lg.Errorf("fallback-line")
	if !strings.Contains(buf.String(), "fallback-line") {
		t.Errorf("must fall back to injected logger, got: %q", buf.String())
	}
}
