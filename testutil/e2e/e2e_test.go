//go:build e2e

// Package e2e exercises the full stack end to end: cobra feature →
// engine → sftp driver → in-process SSH/SFTP server → its filesystem.
// Run with: go test -tags e2e ./testutil/e2e/...
package e2e

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ShizukaJiku/conduit/internal/clock"
	"github.com/ShizukaJiku/conduit/internal/config"
	dlengine "github.com/ShizukaJiku/conduit/internal/download"
	"github.com/ShizukaJiku/conduit/internal/feature"
	_ "github.com/ShizukaJiku/conduit/internal/feature/download"
	_ "github.com/ShizukaJiku/conduit/internal/feature/watch"
	"github.com/ShizukaJiku/conduit/internal/storage"
	_ "github.com/ShizukaJiku/conduit/internal/storage/sftp"
	"github.com/ShizukaJiku/conduit/testutil/sftpserver"
)

const poll = 30 * time.Millisecond

func sftpConfig(t *testing.T, srv *sftpserver.Server, local string) *config.Config {
	t.Helper()
	host, p, err := net.SplitHostPort(srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(p)
	return &config.Config{
		LocalFolder:  local,
		RemoteFolder: "", // sandboxed to srv.Root by the test server
		PollSeconds:  0,
		Backend: config.Backend{Type: "sftp", SFTP: config.SFTPConfig{
			Host: host, Port: port, User: srv.User, Password: srv.Pass,
			InsecureHostKey: true,
		}},
	}
}

func feat(t *testing.T, name string) feature.Feature {
	t.Helper()
	for _, f := range feature.All() {
		if f.Name() == name {
			return f
		}
	}
	t.Fatalf("feature %q not registered", name)
	return nil
}

// waitUntil polls cond until true or the deadline (fails the test).
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for: %s", what)
		case <-time.After(poll):
		}
	}
}

func remoteContent(srv *sftpserver.Server, rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(srv.Root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func TestE2EWatchThroughCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	srv := sftpserver.Start(t)
	local := t.TempDir()
	deps := feature.Deps{Config: sftpConfig(t, srv, local), Storage: storage.New}

	cmd := feat(t, "watch").NewCommand(deps)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()

	a := filepath.Join(local, "a.txt")
	// First upload: re-write until observed (removes the FullResync vs
	// watcher-start race; the watcher is reliable thereafter).
	waitUntil(t, "a.txt uploaded", func() bool {
		_ = os.WriteFile(a, []byte("v1"), 0o644)
		c, ok := remoteContent(srv, "a.txt")
		return ok && c == "v1"
	})

	// Modify.
	if err := os.WriteFile(a, []byte("v2-bigger"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "a.txt modified remotely", func() bool {
		c, ok := remoteContent(srv, "a.txt")
		return ok && c == "v2-bigger"
	})

	// D3: brand-new nested directory with a file inside.
	sub := filepath.Join(local, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "D3 nested file uploaded", func() bool {
		c, ok := remoteContent(srv, "deep/nested/b.txt")
		return ok && c == "beta"
	})

	// Move (rename) = delete old + create new remotely.
	if err := os.Rename(a, filepath.Join(local, "c.txt")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "rename reflected remotely", func() bool {
		_, oldGone := remoteContent(srv, "a.txt")
		c, newThere := remoteContent(srv, "c.txt")
		return !oldGone && newThere && c == "v2-bigger"
	})

	// Delete.
	if err := os.Remove(filepath.Join(sub, "b.txt")); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "delete reflected remotely", func() bool {
		_, ok := remoteContent(srv, "deep/nested/b.txt")
		return !ok
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch RunE returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop after cancel")
	}
}

func placeRemote(t *testing.T, srv *sftpserver.Server, rel, content string) {
	t.Helper()
	p := filepath.Join(srv.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestE2EDownloadInitialThroughCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	srv := sftpserver.Start(t)
	placeRemote(t, srv, "r1.txt", "one")
	placeRemote(t, srv, "sub/r2.txt", "two")

	local := t.TempDir()
	deps := feature.Deps{Config: sftpConfig(t, srv, local), Storage: storage.New}

	cmd := feat(t, "download").NewCommand(deps)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()

	waitUntil(t, "initial download r1.txt", func() bool {
		b, err := os.ReadFile(filepath.Join(local, "r1.txt"))
		return err == nil && string(b) == "one"
	})
	waitUntil(t, "initial download sub/r2.txt", func() bool {
		b, err := os.ReadFile(filepath.Join(local, "sub", "r2.txt"))
		return err == nil && string(b) == "two"
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("download RunE returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download did not stop after cancel")
	}
}

func TestE2EDownloadPollingEngine(t *testing.T) {
	srv := sftpserver.Start(t)
	placeRemote(t, srv, "first.txt", "1")

	local := t.TempDir()
	store, err := storage.New(sftpConfig(t, srv, local))
	if err != nil {
		t.Fatal(err)
	}
	fk := clock.NewFake()
	e := dlengine.New(store, local, 0, nil, fk)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	waitUntil(t, "initial sync first.txt", func() bool {
		b, err := os.ReadFile(filepath.Join(local, "first.txt"))
		return err == nil && string(b) == "1"
	})

	// New remote file appears; drive a poll tick via the fake clock.
	placeRemote(t, srv, "second.txt", "2")
	waitUntil(t, "polled second.txt", func() bool {
		fk.Fire()
		b, err := os.ReadFile(filepath.Join(local, "second.txt"))
		return err == nil && string(b) == "2"
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("download engine Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download engine did not stop after cancel")
	}
}
