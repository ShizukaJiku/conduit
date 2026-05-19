package sftp_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/storage"
	"github.com/ShizukaJiku/conduit/testutil/sftpserver"
)

func connected(t *testing.T) storage.Storage {
	t.Helper()
	srv := sftpserver.Start(t)
	s, err := storage.New(newCfg(t, srv, true, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newDisconnected builds the driver without Connect, to exercise the
// "not connected" guards on every method cheaply (no server needed).
func newDisconnected(t *testing.T) storage.Storage {
	t.Helper()
	s, err := storage.New(&config.Config{
		RemoteFolder: "/srv/data",
		Backend:      config.Backend{Type: "sftp", SFTP: config.SFTPConfig{Host: "h", User: "u"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMethodsErrorWhenNotConnected(t *testing.T) {
	s := newDisconnected(t)

	if _, _, err := s.List(); err == nil {
		t.Error("List must error when not connected")
	}
	if err := s.Put("x", "a.txt"); err == nil {
		t.Error("Put must error when not connected")
	}
	if _, err := s.Get("a.txt", "x"); err == nil {
		t.Error("Get must error when not connected")
	}
	if err := s.Remove("a.txt"); err == nil {
		t.Error("Remove must error when not connected")
	}
	if err := s.EnsureDir("d"); err == nil {
		t.Error("EnsureDir must error when not connected")
	}
	if err := s.RemoveDir("d"); err == nil {
		t.Error("RemoveDir must error when not connected")
	}
	if err := s.Ping(); err == nil {
		t.Error("Ping must error when not connected")
	}
	// Close on a never-connected driver is a no-op.
	if err := s.Close(); err != nil {
		t.Errorf("Close (never connected) = %v, want nil", err)
	}
}

func TestEnsureDirEmptyAndRemoveRoot(t *testing.T) {
	srv := sftpserver.Start(t)
	s, err := storage.New(newCfg(t, srv, true, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// EnsureDir("") is a no-op (nothing to create at the root).
	if err := s.EnsureDir(""); err != nil {
		t.Errorf(`EnsureDir("") = %v, want nil`, err)
	}
	// RemoveDir("") must refuse to delete the configured root.
	if err := s.RemoveDir(""); err == nil {
		t.Error(`RemoveDir("") must refuse (root protection)`)
	}
}

func TestConnectWrongPassword(t *testing.T) {
	srv := sftpserver.Start(t)
	cfg := newCfg(t, srv, true, "")
	cfg.Backend.SFTP.Password = "wrong"
	s, err := storage.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err == nil {
		_ = s.Close()
		t.Fatal("Connect must fail with a wrong password")
	}
}

func TestPutMissingLocalFile(t *testing.T) {
	s := connected(t)
	if err := s.Put(filepath.Join(t.TempDir(), "does-not-exist"), "a.txt"); err == nil {
		t.Error("Put with a missing local file must error")
	}
}

func TestGetLocalParentIsFile(t *testing.T) {
	s := connected(t)
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(src, "f.txt"); err != nil {
		t.Fatal(err)
	}
	// Target whose parent is an existing regular file → MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("f.txt", filepath.Join(blocker, "out")); err == nil {
		t.Error("Get must fail when the local parent path is a file")
	}
}

func TestRemoveMissingRemote(t *testing.T) {
	s := connected(t)
	if err := s.Remove("nope.txt"); err == nil {
		t.Error("Remove of a missing remote file must error")
	}
}

func TestDefaultKnownHostsPathRejectsEphemeralHost(t *testing.T) {
	// KnownHosts == "" → driver resolves ~/.ssh/known_hosts. Our ephemeral
	// test host is never in a real known_hosts (or the file is absent), so
	// Connect must fail — exercising the default-path branch.
	srv := sftpserver.Start(t)
	s, err := storage.New(newCfg(t, srv, false, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err == nil {
		_ = s.Close()
		t.Fatal("default known_hosts must not accept an unknown ephemeral host")
	}
}

func TestConnectInvalidKnownHostsPath(t *testing.T) {
	s, err := storage.New(&config.Config{
		Backend: config.Backend{Type: "sftp", SFTP: config.SFTPConfig{
			Host: "127.0.0.1", Port: 22, User: "u", Password: "p",
			KnownHosts: "/definitely/not/here/known_hosts",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Connect(context.Background()); err == nil {
		_ = s.Close()
		t.Fatal("Connect must fail when known_hosts path is unreadable")
	}
}
