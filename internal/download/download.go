// Package download is the download engine: an additive mirror of a
// storage backend into a local folder (port of sftp_download.py). It
// never deletes local files. It operates only against storage.Storage.
// Decision rules follow PARITY.md §2; the executable oracle is
// testutil/fixtures.
package download

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ShizukaJiku/conduit/internal/clock"
	"github.com/ShizukaJiku/conduit/internal/logx"
	"github.com/ShizukaJiku/conduit/internal/storage"
)

// transientSuffixes are remote names skipped by download (half-uploaded
// files). Kept in sync with testutil/fixtures.IgnoredRemoteSuffixes — a
// test cross-checks the two so they cannot drift.
var transientSuffixes = []string{".part", ".tmp"}

func isTransient(name string) bool {
	for _, s := range transientSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

type localInfo struct {
	sizeBytes int64
	mtimeSec  int64
}

// Engine mirrors store → localFolder, additively.
type Engine struct {
	store    storage.Storage
	local    string
	pollSecs int
	log      *logx.Logger
	clk      clock.Clock
}

// New builds the download engine. pollSecs is clamped per PARITY.md §2.1:
// the effective interval is max(5, pollSecs || 15) seconds.
func New(store storage.Storage, localFolder string, pollSecs int, log *logx.Logger, clk clock.Clock) *Engine {
	if log == nil {
		log = logx.New(nil)
	}
	if clk == nil {
		clk = clock.System{}
	}
	return &Engine{store: store, local: localFolder, pollSecs: pollSecs, log: log, clk: clk}
}

func (e *Engine) interval() time.Duration {
	p := e.pollSecs
	if p <= 0 {
		p = 15
	}
	if p < 5 {
		p = 5
	}
	return time.Duration(p) * time.Second
}

func statLocal(abs string) *localInfo {
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return nil
	}
	return &localInfo{sizeBytes: fi.Size(), mtimeSec: fi.ModTime().Unix()}
}

// downloadNeeded is the PARITY.md §2.2 rule: download if the local file is
// absent, the size differs, or the remote is strictly newer by more than
// 2 whole seconds (delta = remote-local > 2). It never yields a delete.
func downloadNeeded(local *localInfo, remote storage.FileInfo) bool {
	if local == nil {
		return true
	}
	if local.sizeBytes != remote.Size {
		return true
	}
	return remote.ModTimeUnix-local.mtimeSec > 2
}

// FullSync downloads new/changed remote files. It is additive: a local
// file with no remote counterpart is left untouched.
func (e *Engine) FullSync() error {
	remoteFiles, _, err := e.store.List()
	if err != nil {
		return err
	}
	for _, rf := range remoteFiles {
		if isTransient(rf.Path) {
			continue // §2.3 — half-uploaded files (known limitation)
		}
		abs := filepath.Join(e.local, filepath.FromSlash(rf.Path))
		if !downloadNeeded(statLocal(abs), rf) {
			continue
		}
		mt, err := e.store.Get(rf.Path, abs)
		if err != nil {
			e.log.Errorf("get %s: %v", rf.Path, err)
			return err // connection-level failure → caller reconnects
		}
		// Preserve the remote mtime locally so the next pass sees delta 0
		// (no re-download loop). mtime is whole seconds.
		t := time.Unix(mt, 0)
		if cerr := os.Chtimes(abs, t, t); cerr != nil {
			e.log.Errorf("chtimes %s: %v", rf.Path, cerr)
		}
		e.log.Infof("downloaded %s", rf.Path)
	}
	return nil
}

func (e *Engine) reconnect(ctx context.Context) error {
	if err := e.store.Close(); err != nil {
		e.log.Errorf("reconnect: close previous session: %v", err)
	}
	if err := e.store.Connect(ctx); err != nil {
		e.log.Errorf("reconnect: %v", err)
		return err
	}
	e.log.Infof("reconnected")
	return nil
}

// Run executes the PARITY.md §2.1 lifecycle and blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) error {
	if err := os.MkdirAll(e.local, 0o755); err != nil {
		return err
	}
	if err := e.store.Connect(ctx); err != nil {
		return err
	}
	defer e.store.Close()

	if err := e.FullSync(); err != nil {
		if rerr := e.reconnect(ctx); rerr == nil {
			_ = e.FullSync()
		}
	}

	tick := e.clk.After(e.interval())
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			if err := e.FullSync(); err != nil {
				if rerr := e.reconnect(ctx); rerr == nil {
					_ = e.FullSync()
				}
			}
			tick = e.clk.After(e.interval())
		}
	}
}
