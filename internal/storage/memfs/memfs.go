// Package memfs is an in-memory storage.Storage driver. It exists for
// fast, deterministic engine/parity tests (Steps 4/5) and as the
// reference implementation of the Storage contract. It is registered as
// the "memfs" backend; it is never wired into the shipped binary.
package memfs

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/storage"
)

func init() { storage.Register("memfs", New) }

// New builds an in-memory store. cfg is ignored (the root is virtual).
func New(_ *config.Config) (storage.Storage, error) {
	return &store{
		files: map[string]entry{},
		dirs:  map[string]bool{},
		now:   time.Now,
	}, nil
}

type entry struct {
	data  []byte
	mtime int64 // unix seconds
}

type store struct {
	mu        sync.Mutex
	connected bool
	files     map[string]entry
	dirs      map[string]bool
	now       func() time.Time
}

func clean(rel string) string {
	return strings.Trim(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
}

func ancestors(rel string) []string {
	rel = clean(rel)
	var out []string
	for i, r := range rel {
		if r == '/' {
			out = append(out, rel[:i])
		}
	}
	return out
}

func (s *store) Connect(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = true
	return nil
}

func (s *store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
	return nil
}

func (s *store) Ping() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return errors.New("memfs: not connected")
	}
	return nil
}

func (s *store) List() ([]storage.FileInfo, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files := make([]storage.FileInfo, 0, len(s.files))
	for p, e := range s.files {
		files = append(files, storage.FileInfo{Path: p, Size: int64(len(e.data)), ModTimeUnix: e.mtime})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	dirs := make([]string, 0, len(s.dirs))
	for d := range s.dirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return files, dirs, nil
}

func (s *store) Put(localPath, remoteRel string) error {
	b, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	rel := clean(remoteRel)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[rel] = entry{data: b, mtime: s.now().Unix()}
	for _, d := range ancestors(rel) {
		s.dirs[d] = true
	}
	return nil
}

func (s *store) Get(remoteRel, localPath string) (int64, error) {
	rel := clean(remoteRel)
	s.mu.Lock()
	e, ok := s.files[rel]
	s.mu.Unlock()
	if !ok {
		return 0, &os.PathError{Op: "get", Path: rel, Err: os.ErrNotExist}
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(localPath, e.data, 0o644); err != nil {
		return 0, err
	}
	return e.mtime, nil
}

func (s *store) Remove(remoteRel string) error {
	rel := clean(remoteRel)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[rel]; !ok {
		return &os.PathError{Op: "remove", Path: rel, Err: os.ErrNotExist}
	}
	delete(s.files, rel)
	return nil
}

func (s *store) EnsureDir(remoteRel string) error {
	rel := clean(remoteRel)
	if rel == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirs[rel] = true
	for _, d := range ancestors(rel) {
		s.dirs[d] = true
	}
	return nil
}

func (s *store) RemoveDir(remoteRel string) error {
	rel := clean(remoteRel)
	if rel == "" {
		return errors.New("memfs: refusing to remove root")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := rel + "/"
	for p := range s.files {
		if strings.HasPrefix(p, prefix) {
			return &os.PathError{Op: "rmdir", Path: rel, Err: errors.New("directory not empty")}
		}
	}
	for d := range s.dirs {
		if strings.HasPrefix(d, prefix) {
			return &os.PathError{Op: "rmdir", Path: rel, Err: errors.New("directory not empty")}
		}
	}
	if !s.dirs[rel] {
		return &os.PathError{Op: "rmdir", Path: rel, Err: os.ErrNotExist}
	}
	delete(s.dirs, rel)
	return nil
}
