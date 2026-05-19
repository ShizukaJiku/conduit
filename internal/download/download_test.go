package download

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

// seedRemote uploads content to rel with remote mtime = the memfs clock.
func seedRemote(t *testing.T, s storage.Storage, rel, content string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "seed")
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(src, rel); err != nil {
		t.Fatal(err)
	}
}

func writeLocal(t *testing.T, p, content string, mtime time.Time) {
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

func TestDownloadNeededMatchesFixtures(t *testing.T) {
	for _, c := range fixtures.FileCases {
		if !c.RemotePresent {
			continue // download only acts on remote entries
		}
		if c.Transient {
			// Filtered at scan time; the rule never runs. Sanity-check
			// the oracle agrees it is a noop.
			if fixtures.ExpectedDownload(c) != fixtures.DownloadNoop {
				t.Errorf("%s: transient should be noop in oracle", c.Name)
			}
			continue
		}
		localSize, remoteSize := int64(100), int64(100)
		if !c.SizeEqual {
			remoteSize = 200
		}
		localM := int64(1000)
		var li *localInfo
		if c.LocalPresent {
			li = &localInfo{sizeBytes: localSize, mtimeSec: localM}
		}
		remote := storage.FileInfo{Path: c.Name, Size: remoteSize, ModTimeUnix: localM + int64(c.MtimeDeltaSec)}
		got := downloadNeeded(li, remote)
		want := fixtures.ExpectedDownload(c) == fixtures.DownloadGet
		if got != want {
			t.Errorf("%s: downloadNeeded=%v want %v (delta=%d sizeEq=%v local=%v)",
				c.Name, got, want, c.MtimeDeltaSec, c.SizeEqual, c.LocalPresent)
		}
	}
}

func TestIsTransientMatchesFixtures(t *testing.T) {
	for _, n := range []string{"a.part", "d/x.tmp", "ok.txt", "part.go", "a.partition", "deep/y.part"} {
		if isTransient(n) != fixtures.RemoteIgnored(n) {
			t.Errorf("isTransient(%q)=%v but fixtures.RemoteIgnored=%v", n, isTransient(n), fixtures.RemoteIgnored(n))
		}
	}
}

func TestInterval(t *testing.T) {
	for _, tc := range []struct {
		poll int
		want time.Duration
	}{
		{0, 15 * time.Second}, {-3, 15 * time.Second}, {3, 5 * time.Second},
		{5, 5 * time.Second}, {7, 7 * time.Second}, {30, 30 * time.Second},
	} {
		e := New(nil, "", tc.poll, nil, clock.NewFake())
		if got := e.interval(); got != tc.want {
			t.Errorf("poll=%d interval=%v want %v", tc.poll, got, tc.want)
		}
	}
}

func TestFullSyncDownloadsAndPreservesMtime(t *testing.T) {
	local := t.TempDir()
	s := connectedMemfs(t, func() time.Time { return epoch })
	seedRemote(t, s, "a.txt", "alpha")
	seedRemote(t, s, "d/sub/b.txt", "beta")

	e := New(s, local, 0, nil, clock.NewFake())
	if err := e.FullSync(); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"a.txt": "alpha", "d/sub/b.txt": "beta"} {
		p := filepath.Join(local, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil || string(b) != want {
			t.Fatalf("%s = %q,%v want %q", rel, b, err, want)
		}
		fi, _ := os.Stat(p)
		if fi.ModTime().Unix() != epoch.Unix() {
			t.Errorf("%s mtime=%d want %d (remote mtime must be preserved)", rel, fi.ModTime().Unix(), epoch.Unix())
		}
	}
}

func TestFullSyncIsAdditive(t *testing.T) {
	local := t.TempDir()
	extra := filepath.Join(local, "extra.txt")
	writeLocal(t, extra, "mine", epoch)

	s := connectedMemfs(t, func() time.Time { return epoch })
	seedRemote(t, s, "a.txt", "remote")

	e := New(s, local, 0, nil, clock.NewFake())
	if err := e.FullSync(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "a.txt")); string(b) != "remote" {
		t.Errorf("a.txt not downloaded: %q", b)
	}
	if b, err := os.ReadFile(extra); err != nil || string(b) != "mine" {
		t.Errorf("extra.txt must NOT be deleted (additive), got %q,%v", b, err)
	}
}

func TestFullSyncIgnoresTransient(t *testing.T) {
	local := t.TempDir()
	s := connectedMemfs(t, func() time.Time { return epoch })
	seedRemote(t, s, "ok.txt", "good")
	seedRemote(t, s, "x.part", "partial")
	seedRemote(t, s, "y.tmp", "temp")

	e := New(s, local, 0, nil, clock.NewFake())
	if err := e.FullSync(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(local, "ok.txt")); err != nil {
		t.Error("ok.txt should be downloaded")
	}
	for _, skip := range []string{"x.part", "y.tmp"} {
		if _, err := os.Stat(filepath.Join(local, skip)); err == nil {
			t.Errorf("%s must be ignored (not downloaded)", skip)
		}
	}
}

func TestFullSyncNoopWhenLocalNotOlder(t *testing.T) {
	local := t.TempDir()
	// Local present, same size, same mtime as remote (delta 0 ≤ 2): keep.
	writeLocal(t, filepath.Join(local, "x.txt"), "LOCAL", epoch)

	s := connectedMemfs(t, func() time.Time { return epoch })
	seedRemote(t, s, "x.txt", "REMOT") // 5 bytes, same size, remote mtime = epoch

	e := New(s, local, 0, nil, clock.NewFake())
	if err := e.FullSync(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "x.txt")); string(b) != "LOCAL" {
		t.Errorf("must NOT overwrite (delta ≤ 2), got %q", b)
	}
}

// hookStore wraps memfs to inject errors for branch coverage.
type hookStore struct {
	storage.Storage
	connectErr error
	listErr    error
	getErr     error
	getErrFor  string // if set, getErr applies only to this rel
	connects   atomic.Int32
	listCalls  atomic.Int32
	failFirst  bool // listErr only on the first List call
}

func (h *hookStore) Connect(ctx context.Context) error {
	h.connects.Add(1)
	if h.connectErr != nil {
		return h.connectErr
	}
	return h.Storage.Connect(ctx)
}
func (h *hookStore) List() ([]storage.FileInfo, []string, error) {
	n := h.listCalls.Add(1)
	if h.listErr != nil && (!h.failFirst || n == 1) {
		return nil, nil, h.listErr
	}
	return h.Storage.List()
}
func (h *hookStore) Get(rel, local string) (int64, error) {
	if h.getErr != nil && (h.getErrFor == "" || h.getErrFor == rel) {
		return 0, h.getErr
	}
	return h.Storage.Get(rel, local)
}

var errBoom = errors.New("boom")

func TestReconnect(t *testing.T) {
	h := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch })}
	if err := h.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := New(h, t.TempDir(), 0, nil, clock.NewFake())
	before := h.connects.Load()
	if err := e.reconnect(context.Background()); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if h.connects.Load() != before+1 {
		t.Errorf("reconnect must re-Connect: %d → %d", before, h.connects.Load())
	}

	h2 := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), connectErr: errBoom}
	e2 := New(h2, t.TempDir(), 0, nil, clock.NewFake())
	if err := e2.reconnect(context.Background()); err == nil {
		t.Error("reconnect must return Connect error")
	}
}

func TestRunConnectFails(t *testing.T) {
	h := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), connectErr: errBoom}
	e := New(h, t.TempDir(), 0, nil, clock.NewFake())
	if err := e.Run(context.Background()); err == nil {
		t.Fatal("Run must fail if Connect fails")
	}
}

func TestFullSyncListAndGetErrors(t *testing.T) {
	hl := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), listErr: errBoom}
	_ = hl.Connect(context.Background())
	if err := New(hl, t.TempDir(), 0, nil, clock.NewFake()).FullSync(); err == nil {
		t.Error("FullSync must propagate List error")
	}

	// A per-file Get error is logged and skipped (parity: log-and-continue),
	// NOT propagated — FullSync returns nil and the file stays absent.
	hg := &hookStore{Storage: memfs.NewClocked(func() time.Time { return epoch }), getErr: errBoom}
	_ = hg.Connect(context.Background())
	seedRemote(t, hg.Storage, "a.txt", "x")
	local := t.TempDir()
	if err := New(hg, local, 0, nil, clock.NewFake()).FullSync(); err != nil {
		t.Errorf("per-file Get error must NOT abort FullSync, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(local, "a.txt")); serr == nil {
		t.Error("file with Get error must not be created locally")
	}
}

func TestFullSyncPerFileErrorContinues(t *testing.T) {
	local := t.TempDir()
	h := &hookStore{
		Storage:   memfs.NewClocked(func() time.Time { return epoch }),
		getErr:    errBoom,
		getErrFor: "b.txt", // only b.txt fails
	}
	_ = h.Connect(context.Background())
	seedRemote(t, h.Storage, "a.txt", "good-a")
	seedRemote(t, h.Storage, "b.txt", "bad-b")
	seedRemote(t, h.Storage, "c.txt", "good-c")

	if err := New(h, local, 0, nil, clock.NewFake()).FullSync(); err != nil {
		t.Fatalf("FullSync must not abort on a per-file error: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "a.txt")); string(b) != "good-a" {
		t.Errorf("a.txt (before bad file) must download: %q", b)
	}
	if _, err := os.Stat(filepath.Join(local, "b.txt")); err == nil {
		t.Error("b.txt (errored) must be absent")
	}
	if b, _ := os.ReadFile(filepath.Join(local, "c.txt")); string(b) != "good-c" {
		t.Errorf("c.txt (after bad file) must STILL download (log-and-continue): %q", b)
	}
}

func TestFullSyncSkipsLocalDirCollision(t *testing.T) {
	local := t.TempDir()
	// A local directory occupies the path of a remote file.
	if err := os.MkdirAll(filepath.Join(local, "clash"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := connectedMemfs(t, func() time.Time { return epoch })
	seedRemote(t, s, "clash", "remote-data")
	seedRemote(t, s, "fine.txt", "ok")

	if err := New(s, local, 0, nil, clock.NewFake()).FullSync(); err != nil {
		t.Fatalf("dir collision must not abort FullSync: %v", err)
	}
	fi, _ := os.Stat(filepath.Join(local, "clash"))
	if fi == nil || !fi.IsDir() {
		t.Error("local directory 'clash' must be left intact")
	}
	if b, _ := os.ReadFile(filepath.Join(local, "fine.txt")); string(b) != "ok" {
		t.Errorf("other files still download despite a collision: %q", b)
	}
}

func TestRunRecoversFromFirstSyncFailure(t *testing.T) {
	h := &hookStore{
		Storage:   memfs.NewClocked(func() time.Time { return epoch }),
		listErr:   errBoom,
		failFirst: true, // List fails once, then succeeds after reconnect
	}
	local := t.TempDir()
	seedRemote(t, h.Storage, "a.txt", "data") // visible after the failed first List
	fk := clock.NewFake()
	e := New(h, local, 0, nil, fk)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- e.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(local, "a.txt")); err == nil {
			break // reconnect + FullSync recovered and downloaded
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("did not recover from first sync failure")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-errc:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunPollsForNewRemoteFiles(t *testing.T) {
	local := t.TempDir()
	s := connectedMemfs(t, func() time.Time { return epoch })
	seedRemote(t, s, "first.txt", "1")
	fk := clock.NewFake()
	e := New(s, local, 0, nil, fk)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- e.Run(ctx) }()

	// Initial FullSync (before the poll loop) must fetch first.txt.
	deadline := time.After(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(local, "first.txt")); err == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("initial FullSync did not download first.txt")
		case <-time.After(20 * time.Millisecond):
		}
	}

	// Add a new remote file and drive a poll tick via the fake clock.
	seedRemote(t, s, "second.txt", "2")
	for {
		fk.Fire()
		if _, err := os.Stat(filepath.Join(local, "second.txt")); err == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("poll did not download second.txt")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-errc:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
