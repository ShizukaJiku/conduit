package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ShizukaJiku/conduit/internal/clock"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitFor consumes events until one satisfies pred or the deadline passes.
func waitFor(t *testing.T, w *Watcher, pred func(Event) bool) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-w.Events():
			if !ok {
				t.Fatal("events channel closed before a matching event")
			}
			if pred(e) {
				return e
			}
		case <-deadline:
			t.Fatal("timed out waiting for expected event")
		}
	}
}

func startWatcher(t *testing.T, root string) *Watcher {
	t.Helper()
	w, err := New(root, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestCreateModifyDelete(t *testing.T) {
	root := t.TempDir()
	w := startWatcher(t, root)

	f := filepath.Join(root, "a.txt")
	write(t, f, "hello")
	ev := waitFor(t, w, func(e Event) bool { return e.Path == f && e.Op == Created })
	if ev.Op != Created {
		t.Fatalf("want Created, got %v", ev.Op)
	}

	write(t, f, "hello world larger")
	waitFor(t, w, func(e Event) bool { return e.Path == f && e.Op == Modified })

	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	waitFor(t, w, func(e Event) bool { return e.Path == f && e.Op == Deleted })
}

func TestNewSubdirWithFileD3(t *testing.T) {
	root := t.TempDir()
	w := startWatcher(t, root)

	sub := filepath.Join(root, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(sub, "inside.txt")
	write(t, f, "data")

	// The file is detected even though its directory did not exist when
	// the watcher started (D3 mitigation: add watch + re-walk).
	waitFor(t, w, func(e Event) bool { return e.Path == f && e.Op == Created })
}

func TestNestedExistingTreeIsWatched(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "x", "y")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(deep, "pre.txt"), "pre") // exists before Start

	w := startWatcher(t, root) // addTree must watch x/y recursively

	f := filepath.Join(deep, "post.txt")
	write(t, f, "post")
	waitFor(t, w, func(e Event) bool { return e.Path == f && e.Op == Created })
}

func TestCloseSignalsDone(t *testing.T) {
	w, err := New(t.TempDir(), clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-w.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done() not closed after Close()")
	}
	// Close is idempotent (no double-close panic on w.done).
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestOpString(t *testing.T) {
	for op, want := range map[Op]string{Created: "created", Modified: "modified", Deleted: "deleted"} {
		if op.String() != want {
			t.Errorf("Op(%d).String() = %q, want %q", op, op.String(), want)
		}
	}
}

func TestOpStringUnknown(t *testing.T) {
	if got := Op(99).String(); got != "unknown" {
		t.Errorf("Op(99).String() = %q, want unknown", got)
	}
}

func TestEmitAfterCloseDoesNotBlock(t *testing.T) {
	w, err := New(t.TempDir(), clock.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// done is closed → emit must return immediately, not deadlock.
	doneCh := make(chan struct{})
	go func() {
		w.emit(Event{Op: Created, Path: "x"})
		w.emitErr(errSentinel)
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("emit/emitErr blocked after Close")
	}
}

var errSentinel = &sentinelErr{}

type sentinelErr struct{}

func (*sentinelErr) Error() string { return "sentinel" }

func TestRenameMapsToDeleteThenCreate(t *testing.T) {
	root := t.TempDir()
	w := startWatcher(t, root)

	old := filepath.Join(root, "old.txt")
	write(t, old, "x")
	waitFor(t, w, func(e Event) bool { return e.Path == old && e.Op == Created })

	newp := filepath.Join(root, "new.txt")
	if err := os.Rename(old, newp); err != nil {
		t.Fatal(err)
	}
	// Net effect of a move = Deleted(old) + Created(new) = Remove+Put,
	// matching the legacy "moved" parity expectation.
	waitFor(t, w, func(e Event) bool { return e.Path == old && e.Op == Deleted })
	waitFor(t, w, func(e Event) bool { return e.Path == newp && e.Op == Created })
}

func TestWaitStableFakeClock(t *testing.T) {
	root := t.TempDir()
	w, err := New(root, clock.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	f := filepath.Join(root, "stable.bin")
	write(t, f, "constant size")

	done := make(chan struct{})
	go func() { w.WaitStable(f); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitStable did not return for a stable file")
	}

	fk := w.clk.(*clock.Fake)
	if len(fk.Sleeps()) == 0 {
		t.Error("WaitStable should have slept at least once via the clock")
	}
	if len(fk.Sleeps()) >= stableTries {
		t.Errorf("stable file should settle fast, slept %d times", len(fk.Sleeps()))
	}
}

func TestWaitStableMissingFile(t *testing.T) {
	w, err := New(t.TempDir(), clock.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	done := make(chan struct{})
	go func() { w.WaitStable(filepath.Join(t.TempDir(), "ghost")); close(done) }()
	select {
	case <-done: // returns after exhausting tries, no panic
	case <-time.After(2 * time.Second):
		t.Fatal("WaitStable must return even if the file never appears")
	}
	if got := len(w.clk.(*clock.Fake).Sleeps()); got != stableTries {
		t.Errorf("missing file: slept %d times, want %d", got, stableTries)
	}
}
