// Package watcher is a recursive filesystem watcher for the watch
// feature. fsnotify is NOT recursive (issue #18), so we walk the tree and
// Add every directory, and on a new directory we Add it AND re-walk it to
// emit Created for files that appeared in the race window between mkdir
// and Add (delta D3 in PARITY.md §1.4).
//
// A rename is delivered by fsnotify as Rename(old)+Create(new); we map
// those to Deleted(old)+Created(new), whose net Storage ops (Remove+Put)
// equal the legacy "moved" behavior — so no synthetic move event.
package watcher

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ShizukaJiku/conduit/internal/clock"
)

// Op is the kind of change observed for a file.
type Op int

const (
	Created Op = iota
	Modified
	Deleted
)

func (o Op) String() string {
	switch o {
	case Created:
		return "created"
	case Modified:
		return "modified"
	case Deleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// Event is a file change. Path is an absolute local path.
type Event struct {
	Op   Op
	Path string
}

const (
	stableTries = 15
	stableDelay = 200 * time.Millisecond
)

// Watcher watches root recursively.
type Watcher struct {
	root string
	clk  clock.Clock
	fsw  *fsnotify.Watcher

	events    chan Event
	errs      chan error
	done      chan struct{}
	closeOnce sync.Once
}

// New creates a watcher for root (which must already exist).
func New(root string, clk clock.Clock) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		root:   root,
		clk:    clk,
		fsw:    fsw,
		events: make(chan Event, 256),
		errs:   make(chan error, 16),
		done:   make(chan struct{}),
	}, nil
}

// Events streams file changes. Modified/Created files should be uploaded;
// Deleted files removed remotely.
func (w *Watcher) Events() <-chan Event { return w.events }

// Errors streams non-fatal watcher errors.
func (w *Watcher) Errors() <-chan error { return w.errs }

// Start adds watches for the whole tree and begins streaming events.
func (w *Watcher) Start() error {
	if err := w.addTree(w.root); err != nil {
		return err
	}
	go w.loop()
	return nil
}

// Close stops watching and closes the channels.
func (w *Watcher) Close() error {
	err := w.fsw.Close() // unblocks loop (fsnotify channels close)
	w.closeOnce.Do(func() { close(w.done) })
	return err
}

// addTree adds a watch for dir and every subdirectory.
func (w *Watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries; not fatal
		}
		if d.IsDir() {
			if aerr := w.fsw.Add(p); aerr != nil {
				w.emitErr(aerr) // partial coverage is surfaced, not silent
			}
		}
		return nil
	})
}

// rewalkEmit adds watches for a freshly-seen directory subtree and emits
// Created for every file in it (covers the D3 race window). A file may
// also get a separate fsnotify Create, yielding a duplicate Created — the
// engine's Put is idempotent, so this is extra work, not a correctness
// bug.
func (w *Watcher) rewalkEmit(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = w.fsw.Add(p)
			return nil
		}
		w.emit(Event{Op: Created, Path: p})
		return nil
	})
}

func (w *Watcher) emit(e Event) {
	select {
	case w.events <- e:
	case <-w.done:
	}
}

func (w *Watcher) emitErr(err error) {
	select {
	case w.errs <- err:
	default: // never block the loop on a slow error consumer
	}
}

func (w *Watcher) loop() {
	defer close(w.events)
	for {
		select {
		case <-w.done:
			return
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.emitErr(err)
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(ev)
		}
	}
}

func (w *Watcher) handle(ev fsnotify.Event) {
	switch {
	case ev.Has(fsnotify.Create):
		fi, err := os.Stat(ev.Name)
		if err != nil {
			return // vanished as fast as it appeared
		}
		if fi.IsDir() {
			w.rewalkEmit(ev.Name) // D3: add watch + emit files inside
			return
		}
		w.emit(Event{Op: Created, Path: ev.Name})
	case ev.Has(fsnotify.Write):
		if fi, err := os.Stat(ev.Name); err == nil && !fi.IsDir() {
			w.emit(Event{Op: Modified, Path: ev.Name})
		}
	case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
		// Can't stat (gone). Engine treats Remove of a dir/missing path
		// leniently; full-resync reconciles directories.
		w.emit(Event{Op: Deleted, Path: ev.Name})
	}
}

// WaitStable blocks until path stops growing (two equal size samples) or
// stableTries elapse, so partially-written files are not uploaded
// (PARITY.md §1.5). The clock is injectable so tests don't really sleep.
func (w *Watcher) WaitStable(path string) {
	var last int64 = -1
	for i := 0; i < stableTries; i++ {
		if fi, err := os.Stat(path); err == nil {
			if sz := fi.Size(); sz == last {
				return
			} else {
				last = sz
			}
		}
		w.clk.Sleep(stableDelay)
	}
}
