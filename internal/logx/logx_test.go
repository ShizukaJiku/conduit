package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormatAndLevels(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.now = func() time.Time { return time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC) }

	l.Infof("hola %s", "mundo")
	l.Warnf("ojo")
	l.Errorf("boom %d", 7)

	got := buf.String()
	want := "2026-05-19 01:02:03 [INFO] hola mundo\n" +
		"2026-05-19 01:02:03 [WARN] ojo\n" +
		"2026-05-19 01:02:03 [ERROR] boom 7\n"
	if got != want {
		t.Fatalf("log output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDefaultPath(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "conduit.log" || filepath.Base(filepath.Dir(p)) != ".conduit" {
		t.Errorf("DefaultPath = %q, want ~/.conduit/conduit.log", p)
	}
}

func TestOpenBadPath(t *testing.T) {
	// A path whose parent is an existing file cannot be created.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(filepath.Join(f, "conduit.log")); err == nil {
		t.Error("Open should fail when parent path is a file")
	}
}

func TestNilWriterSafe(t *testing.T) {
	New(nil).Infof("must not panic")
	var l *Logger
	l.Infof("nil receiver must not panic")
}

func TestOpenAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "conduit.log")

	l1, c1, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	l1.Infof("first")
	_ = c1.Close()

	l2, c2, err := Open(p) // must append, not truncate
	if err != nil {
		t.Fatal(err)
	}
	l2.Infof("second")
	_ = c2.Close()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("append failed, got: %q", out)
	}
	if strings.Count(out, "[INFO]") != 2 {
		t.Fatalf("expected 2 lines, got: %q", out)
	}
}
