package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShizukaJiku/conduit/internal/clock"
	"github.com/ShizukaJiku/conduit/internal/storage"
	"github.com/ShizukaJiku/conduit/internal/storage/memfs"
	"github.com/ShizukaJiku/conduit/internal/watcher"
	"github.com/ShizukaJiku/conduit/testutil/fixtures"
)

var epoch = time.Unix(1_700_000_000, 0)

func connectedMemfs(t *testing.T, now func() time.Time) storage.Storage {
	t.Helper()
	s := memfs.NewClocked(now)
	if err := s.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func writeFile(t *testing.T, p, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func getRemote(t *testing.T, s storage.Storage, rel string) (string, bool) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "got")
	if _, err := s.Get(rel, dst); err != nil {
		return "", false
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// TestPutNeededMatchesFixtures cross-checks the upload rule against the
// executable parity oracle for every local-present case.
func TestPutNeededMatchesFixtures(t *testing.T) {
	for _, c := range fixtures.FileCases {
		if !c.LocalPresent {
			continue // putNeeded is only evaluated for local files
		}
		var rp *storage.FileInfo
		localSize, remoteSize := int64(100), int64(100)
		if !c.SizeEqual {
			remoteSize = 200
		}
		localM := int64(1000)
		if c.RemotePresent {
			rp = &storage.FileInfo{Path: c.Name, Size: remoteSize, ModTimeUnix: localM + int64(c.MtimeDeltaSec)}
		}
		got := putNeeded(localSize, localM, rp)
		want := fixtures.ExpectedWatch(c) == fixtures.WatchPut
		if got != want {
			t.Errorf("%s: putNeeded=%v want %v (delta=%d sizeEq=%v remote=%v)",
				c.Name, got, want, c.MtimeDeltaSec, c.SizeEqual, c.RemotePresent)
		}
	}
}

func TestDeepestFirstMatchesFixtures(t *testing.T) {
	in := []string{"a", "a/b", "a/b/c", "z", "z/y"}
	got := deepestFirst(in)
	want := fixtures.DeepestFirst(in)
	if len(got) != len(want) {
		t.Fatalf("len mismatch: %v vs %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("deepestFirst=%v, fixtures=%v", got, want)
		}
	}
}

func TestFullResyncUploadsNewAndChanged(t *testing.T) {
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "a.txt"), "alpha", epoch)
	writeFile(t, filepath.Join(local, "d", "sub", "b.txt"), "beta", epoch)

	s := connectedMemfs(t, func() time.Time { return epoch })
	e := New(s, local, nil, clock.NewFake())
	if err := e.FullResync(); err != nil {
		t.Fatal(err)
	}
	if c, ok := getRemote(t, s, "a.txt"); !ok || c != "alpha" {
		t.Errorf("a.txt remote = %q,%v", c, ok)
	}
	if c, ok := getRemote(t, s, "d/sub/b.txt"); !ok || c != "beta" {
		t.Errorf("nested remote = %q,%v", c, ok)
	}
}

func TestFullResyncRemovesRemoteOnlyIncludingTransient(t *testing.T) {
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "keep.txt"), "k", epoch)

	s := connectedMemfs(t, func() time.Time { return epoch })
	// Seed remote with files that do NOT exist locally, incl. a .part.
	for _, rel := range []string{"keep.txt", "extra.txt", "stale.part"} {
		src := filepath.Join(t.TempDir(), "s")
		writeFile(t, src, "x", epoch)
		if err := s.Put(src, rel); err != nil {
			t.Fatal(err)
		}
	}
	e := New(s, local, nil, clock.NewFake())
	if err := e.FullResync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := getRemote(t, s, "keep.txt"); !ok {
		t.Error("keep.txt must survive (exists locally)")
	}
	if _, ok := getRemote(t, s, "extra.txt"); ok {
		t.Error("extra.txt must be removed (remote-only)")
	}
	if _, ok := getRemote(t, s, "stale.part"); ok {
		t.Error("watch does NOT filter .part — stale.part must be pruned")
	}
}

func TestFullResyncNoopWhenRemoteNotOlder(t *testing.T) {
	local := t.TempDir()
	// Local content differs but same size; local mtime OLDER than remote.
	writeFile(t, filepath.Join(local, "x.txt"), "BBBBB", epoch.Add(-10*time.Second))

	s := connectedMemfs(t, func() time.Time { return epoch })
	src := filepath.Join(t.TempDir(), "s")
	writeFile(t, src, "AAAAA", epoch) // remote mtime = epoch (newer)
	if err := s.Put(src, "x.txt"); err != nil {
		t.Fatal(err)
	}
	e := New(s, local, nil, clock.NewFake())
	if err := e.FullResync(); err != nil {
		t.Fatal(err)
	}
	if c, _ := getRemote(t, s, "x.txt"); c != "AAAAA" {
		t.Errorf("remote must NOT be overwritten (delta>-2), got %q", c)
	}
}

func TestFullResyncUploadsWhenLocalNewer(t *testing.T) {
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "y.txt"), "CCCCC", epoch.Add(10*time.Second))

	s := connectedMemfs(t, func() time.Time { return epoch })
	src := filepath.Join(t.TempDir(), "s")
	writeFile(t, src, "AAAAA", epoch)
	if err := s.Put(src, "y.txt"); err != nil {
		t.Fatal(err)
	}
	e := New(s, local, nil, clock.NewFake())
	if err := e.FullResync(); err != nil {
		t.Fatal(err)
	}
	if c, _ := getRemote(t, s, "y.txt"); c != "CCCCC" {
		t.Errorf("local strictly newer (>2s) must upload, got %q", c)
	}
}

func TestFullResyncPrunesDirsDeepestFirst(t *testing.T) {
	local := t.TempDir()
	// Local keeps one dir tree; remote has an extra empty tree to prune.
	writeFile(t, filepath.Join(local, "keep", "f.txt"), "f", epoch)

	s := connectedMemfs(t, func() time.Time { return epoch })
	src := filepath.Join(t.TempDir(), "s")
	writeFile(t, src, "f", epoch)
	if err := s.Put(src, "keep/f.txt"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a", "a/b", "a/b/c"} {
		if err := s.EnsureDir(d); err != nil {
			t.Fatal(err)
		}
	}
	e := New(s, local, nil, clock.NewFake())
	if err := e.FullResync(); err != nil {
		t.Fatalf("FullResync (memfs RemoveDir errors on non-empty → wrong order would fail): %v", err)
	}
	_, dirs, _ := s.List()
	for _, d := range dirs {
		if d == "a" || d == "a/b" || d == "a/b/c" {
			t.Errorf("dir %q must have been pruned", d)
		}
	}
}

func TestApplyEventCreateModifyDelete(t *testing.T) {
	local := t.TempDir()
	s := connectedMemfs(t, func() time.Time { return epoch })
	e := New(s, local, nil, clock.NewFake())
	w, err := watcher.New(local, clock.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	f := filepath.Join(local, "n.txt")
	writeFile(t, f, "v1", epoch)
	e.applyEvent(w, watcher.Event{Op: watcher.Created, Path: f})
	if c, ok := getRemote(t, s, "n.txt"); !ok || c != "v1" {
		t.Fatalf("created not uploaded: %q,%v", c, ok)
	}
	writeFile(t, f, "v2-bigger", epoch)
	e.applyEvent(w, watcher.Event{Op: watcher.Modified, Path: f})
	if c, _ := getRemote(t, s, "n.txt"); c != "v2-bigger" {
		t.Fatalf("modified not uploaded: %q", c)
	}
	_ = os.Remove(f)
	e.applyEvent(w, watcher.Event{Op: watcher.Deleted, Path: f})
	if _, ok := getRemote(t, s, "n.txt"); ok {
		t.Fatal("deleted not removed remotely")
	}
}

// failPingStore wraps memfs, failing the first Ping and counting Connects.
// Connect runs on the engine goroutine while the test reads connects, so
// the counters are atomic (this is a test-only race guard).
type failPingStore struct {
	storage.Storage
	connectsN atomic.Int32
	pingedN   atomic.Int32
}

func (f *failPingStore) connects() int { return int(f.connectsN.Load()) }

func (f *failPingStore) Connect(ctx context.Context) error {
	f.connectsN.Add(1)
	return f.Storage.Connect(ctx)
}

func (f *failPingStore) Ping() error {
	if f.pingedN.Add(1) == 1 {
		return errors.New("simulated drop")
	}
	return f.Storage.Ping()
}

func TestReconnect(t *testing.T) {
	fs := &failPingStore{Storage: memfs.NewClocked(func() time.Time { return epoch })}
	e := New(fs, t.TempDir(), nil, clock.NewFake())
	if err := fs.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := fs.connects()
	if err := e.reconnect(context.Background()); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if fs.connects() != before+1 {
		t.Errorf("reconnect must re-Connect: connects %d → %d", before, fs.connects())
	}
}

var errBoom = errors.New("boom")

type hookStore struct {
	storage.Storage
	connectErr, listErr, putErr error
	connects                    int
}

func (h *hookStore) Connect(ctx context.Context) error {
	h.connects++
	if h.connectErr != nil {
		return h.connectErr
	}
	return h.Storage.Connect(ctx)
}
func (h *hookStore) List() ([]storage.FileInfo, []string, error) {
	if h.listErr != nil {
		return nil, nil, h.listErr
	}
	return h.Storage.List()
}
func (h *hookStore) Put(local, rel string) error {
	if h.putErr != nil {
		return h.putErr
	}
	return h.Storage.Put(local, rel)
}

func TestReconnectError(t *testing.T) {
	h := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), connectErr: errBoom}
	e := New(h, t.TempDir(), nil, clock.NewFake())
	if err := e.reconnect(context.Background()); err == nil {
		t.Fatal("reconnect must return the Connect error")
	}
}

func TestRunConnectFails(t *testing.T) {
	h := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), connectErr: errBoom}
	e := New(h, t.TempDir(), nil, clock.NewFake())
	if err := e.Run(context.Background()); err == nil {
		t.Fatal("Run must fail if Connect fails")
	}
}

func TestFullResyncListError(t *testing.T) {
	h := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), listErr: errBoom}
	if err := h.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := New(h, t.TempDir(), nil, clock.NewFake())
	if err := e.FullResync(); err == nil {
		t.Fatal("FullResync must propagate List error")
	}
}

func TestFullResyncPutError(t *testing.T) {
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "a.txt"), "x", epoch)
	h := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), putErr: errBoom}
	if err := h.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := New(h, local, nil, clock.NewFake())
	if err := e.FullResync(); err == nil {
		t.Fatal("FullResync must propagate Put error")
	}
}

func TestApplyEventEdgeCases(t *testing.T) {
	local := t.TempDir()
	s := connectedMemfs(t, func() time.Time { return epoch })
	e := New(s, local, nil, clock.NewFake())
	w, err := watcher.New(local, clock.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Path outside the root → ignored.
	e.applyEvent(w, watcher.Event{Op: watcher.Created, Path: filepath.Join(t.TempDir(), "outside.txt")})
	// Created on a directory → ignored (stat says dir).
	d := filepath.Join(local, "adir")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	e.applyEvent(w, watcher.Event{Op: watcher.Created, Path: d})
	// Deleted for something never uploaded → store.Remove errors, logged.
	e.applyEvent(w, watcher.Event{Op: watcher.Deleted, Path: filepath.Join(local, "ghost.txt")})

	files, _, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("no uploads expected, got %v", files)
	}
}

func TestRunReconnectsOnKeepaliveFailure(t *testing.T) {
	fs := &failPingStore{Storage: memfs.NewClocked(func() time.Time { return epoch })}
	fk := clock.NewFake()
	e := New(fs, t.TempDir(), nil, fk)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- e.Run(ctx) }()

	// Drive keepalive ticks deterministically via the fake clock until the
	// first (failing) Ping forces a reconnect.
	deadline := time.After(5 * time.Second)
	for fs.connects() < 2 {
		fk.Fire() // no-op if Run hasn't reached the select yet
		select {
		case <-deadline:
			cancel()
			t.Fatalf("expected reconnect after keepalive failure, connects=%d", fs.connects())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-errc:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunSmoke(t *testing.T) {
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "seed.txt"), "seed", epoch)
	s := connectedMemfs(t, func() time.Time { return epoch })
	e := New(s, local, nil, clock.NewFake()) // instant WaitStable, no timer churn

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- e.Run(ctx) }()

	// Re-write live.txt each poll: this removes the race over exactly when
	// Run finishes FullResync and the watcher becomes active — once it is,
	// a write event is caught and the file is uploaded.
	live := filepath.Join(local, "live.txt")
	deadline := time.After(8 * time.Second)
	for {
		writeFile(t, live, "live", epoch)
		if c, ok := getRemote(t, s, "live.txt"); ok && c == "live" {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("live.txt not uploaded by Run within timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
