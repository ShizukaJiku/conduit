// Package sync is the watch engine: a destructive mirror of a local
// folder onto a storage backend (port of sftp_sync.py). It operates only
// against the storage.Storage contract. Decision rules follow PARITY.md
// §1; the executable oracle is testutil/fixtures.
package sync

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ShizukaJiku/conduit/internal/clock"
	"github.com/ShizukaJiku/conduit/internal/logx"
	"github.com/ShizukaJiku/conduit/internal/storage"
	"github.com/ShizukaJiku/conduit/internal/watcher"
)

// keepaliveEvery is the PARITY.md §1.1 30s ping interval (overridable in tests).
const keepaliveEvery = 30 * time.Second

type localInfo struct {
	sizeBytes int64
	mtimeSec  int64
}

// Engine mirrors localFolder → store, destructively.
type Engine struct {
	store     storage.Storage
	local     string
	log       *logx.Logger
	clk       clock.Clock
	keepEvery time.Duration
}

// New builds the watch engine.
func New(store storage.Storage, localFolder string, log *logx.Logger, clk clock.Clock) *Engine {
	if log == nil {
		log = logx.New(nil)
	}
	if clk == nil {
		clk = clock.System{}
	}
	return &Engine{
		store:     store,
		local:     localFolder,
		log:       log,
		clk:       clk,
		keepEvery: keepaliveEvery,
	}
}

// putNeeded is the PARITY.md §1.2 upload rule: upload if the remote is
// absent, the size differs, or the local file is strictly newer by more
// than 2 whole seconds (delta = remote-local < -2).
func putNeeded(localSize, localMtimeSec int64, remote *storage.FileInfo) bool {
	if remote == nil {
		return true
	}
	if remote.Size != localSize {
		return true
	}
	return remote.ModTimeUnix-localMtimeSec < -2
}

func toRel(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

func (e *Engine) scanLocal() (files map[string]localInfo, dirs map[string]bool, err error) {
	files = map[string]localInfo{}
	dirs = map[string]bool{}
	walkErr := filepath.WalkDir(e.local, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		rel, ok := toRel(e.local, p)
		if !ok {
			return nil // the root itself
		}
		if d.IsDir() {
			dirs[rel] = true
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files[rel] = localInfo{sizeBytes: info.Size(), mtimeSec: info.ModTime().Unix()}
		return nil
	})
	return files, dirs, walkErr
}

// deepestFirst orders directories so children precede parents (so
// RemoveDir never hits a non-empty directory). PARITY.md §1.3.
func deepestFirst(dirs []string) []string {
	out := append([]string(nil), dirs...)
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "/"), strings.Count(out[j], "/")
		if di != dj {
			return di > dj
		}
		return out[i] > out[j]
	})
	return out
}

// FullResync makes the remote an exact mirror of the local tree
// (destructive): upload new/changed, delete remote files and directories
// that no longer exist locally.
func (e *Engine) FullResync() error {
	localFiles, localDirs, err := e.scanLocal()
	if err != nil {
		return err
	}
	remoteFiles, remoteDirs, err := e.store.List()
	if err != nil {
		return err
	}
	remoteByRel := make(map[string]storage.FileInfo, len(remoteFiles))
	for _, f := range remoteFiles {
		remoteByRel[f.Path] = f
	}

	for rel, li := range localFiles {
		var rp *storage.FileInfo
		if r, ok := remoteByRel[rel]; ok {
			rp = &r
		}
		if !putNeeded(li.sizeBytes, li.mtimeSec, rp) {
			continue
		}
		abs := filepath.Join(e.local, filepath.FromSlash(rel))
		if err := e.store.Put(abs, rel); err != nil {
			e.log.Errorf("put %s: %v", rel, err)
			return err // connection-level failure → caller reconnects
		}
		e.log.Infof("uploaded %s", rel)
	}

	for _, rf := range remoteFiles {
		if _, ok := localFiles[rf.Path]; ok {
			continue
		}
		if err := e.store.Remove(rf.Path); err != nil {
			e.log.Errorf("remove %s: %v", rf.Path, err) // log & continue
		} else {
			e.log.Infof("removed remote %s", rf.Path)
		}
	}

	var toPrune []string
	for _, d := range remoteDirs {
		if !localDirs[d] {
			toPrune = append(toPrune, d)
		}
	}
	for _, d := range deepestFirst(toPrune) {
		if err := e.store.RemoveDir(d); err != nil {
			e.log.Errorf("rmdir %s: %v", d, err) // log & continue
		}
	}
	return nil
}

// applyEvent maps a watcher event to a storage operation (PARITY.md §1.4).
func (e *Engine) applyEvent(w *watcher.Watcher, ev watcher.Event) {
	rel, ok := toRel(e.local, ev.Path)
	if !ok {
		return
	}
	switch ev.Op {
	case watcher.Created, watcher.Modified:
		fi, err := os.Stat(ev.Path)
		if err != nil || fi.IsDir() {
			return
		}
		w.WaitStable(ev.Path)
		if err := e.store.Put(ev.Path, rel); err != nil {
			e.log.Errorf("put %s: %v", rel, err)
			return
		}
		e.log.Infof("uploaded %s", rel)
	case watcher.Deleted:
		// Could be a file or a (now-gone) directory; be lenient.
		if err := e.store.Remove(rel); err != nil {
			e.log.Errorf("remove %s: %v", rel, err)
			return
		}
		e.log.Infof("removed remote %s", rel)
	}
}

func (e *Engine) reconnect(ctx context.Context) error {
	_ = e.store.Close()
	if err := e.store.Connect(ctx); err != nil {
		e.log.Errorf("reconnect: %v", err)
		return err
	}
	e.log.Infof("reconnected")
	return nil
}

// Run executes the PARITY.md §1.1 lifecycle and blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) error {
	if err := os.MkdirAll(e.local, 0o755); err != nil {
		return err
	}
	if err := e.store.Connect(ctx); err != nil {
		return err
	}
	defer e.store.Close()

	if err := e.FullResync(); err != nil {
		if rerr := e.reconnect(ctx); rerr == nil {
			_ = e.FullResync()
		}
	}

	w, err := watcher.New(e.local, e.clk)
	if err != nil {
		return err
	}
	if err := w.Start(); err != nil {
		return err
	}
	defer w.Close()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-e.clk.After(e.keepEvery):
			if err := e.store.Ping(); err != nil {
				e.log.Errorf("keepalive: %v", err)
				if rerr := e.reconnect(ctx); rerr == nil {
					_ = e.FullResync()
				}
			}
		case ev, ok := <-w.Events():
			if !ok {
				return nil
			}
			e.applyEvent(w, ev)
		case werr, ok := <-w.Errors():
			if !ok {
				continue
			}
			e.log.Errorf("watcher: %v", werr)
		}
	}
}
