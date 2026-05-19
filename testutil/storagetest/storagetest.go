// Package storagetest is the shared, backend-neutral contract suite every
// storage.Storage driver must pass. memfs and sftp run the exact same
// suite, so a driver that diverges from the contract fails CI.
//
// The contract deliberately does NOT assert Put preserves the source
// mtime: real SFTP servers stamp the upload time. mtime preservation is a
// download-side concern (Get), tested as List/Get consistency here and as
// engine parity in later steps.
package storagetest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ShizukaJiku/conduit/internal/storage"
)

// RunContract runs the full suite. mk returns a fresh, not-yet-connected
// Storage for the given test (it may register its own t.Cleanup).
func RunContract(t *testing.T, mk func(t *testing.T) storage.Storage) {
	t.Helper()

	connect := func(t *testing.T) storage.Storage {
		s := mk(t)
		if err := s.Connect(context.Background()); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}

	stage := func(t *testing.T, content string) string {
		p := filepath.Join(t.TempDir(), "src")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	find := func(files []storage.FileInfo, rel string) (storage.FileInfo, bool) {
		for _, f := range files {
			if f.Path == rel {
				return f, true
			}
		}
		return storage.FileInfo{}, false
	}

	t.Run("put_list_get", func(t *testing.T) {
		s := connect(t)
		if err := s.Put(stage(t, "hello"), "a.txt"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		files, _, err := s.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		fi, ok := find(files, "a.txt")
		if !ok {
			t.Fatalf("List missing a.txt: %+v", files)
		}
		if fi.Size != 5 {
			t.Errorf("Size = %d, want 5", fi.Size)
		}
		if fi.ModTimeUnix <= 0 {
			t.Errorf("ModTimeUnix = %d, want > 0 (seconds)", fi.ModTimeUnix)
		}

		dst := filepath.Join(t.TempDir(), "out")
		mt, err := s.Get("a.txt", dst)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if mt != fi.ModTimeUnix {
			t.Errorf("Get mtime %d != List mtime %d", mt, fi.ModTimeUnix)
		}
		b, _ := os.ReadFile(dst)
		if string(b) != "hello" {
			t.Errorf("roundtrip content = %q, want hello", b)
		}
	})

	t.Run("put_creates_parent_dirs", func(t *testing.T) {
		s := connect(t)
		if err := s.Put(stage(t, "x"), "d/sub/b.txt"); err != nil {
			t.Fatalf("Put nested: %v", err)
		}
		files, dirs, err := s.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if _, ok := find(files, "d/sub/b.txt"); !ok {
			t.Fatalf("List missing d/sub/b.txt: %+v", files)
		}
		for _, want := range []string{"d", "d/sub"} {
			if !slices.Contains(dirs, want) {
				t.Errorf("List dirs %v missing %q", dirs, want)
			}
		}
	})

	t.Run("overwrite_atomic", func(t *testing.T) {
		s := connect(t)
		if err := s.Put(stage(t, "v1"), "a.txt"); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(stage(t, "version-2-longer"), "a.txt"); err != nil {
			t.Fatalf("overwrite Put: %v", err)
		}
		files, _, _ := s.List()
		fi, ok := find(files, "a.txt")
		if !ok || fi.Size != int64(len("version-2-longer")) {
			t.Fatalf("overwrite size wrong: %+v", fi)
		}
		dst := filepath.Join(t.TempDir(), "o")
		if _, err := s.Get("a.txt", dst); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(dst); string(b) != "version-2-longer" {
			t.Errorf("content after overwrite = %q", b)
		}
		// No transient ".part" artifact must survive an atomic Put.
		all, _, _ := s.List()
		for _, f := range all {
			if filepath.Ext(f.Path) == ".part" {
				t.Errorf("leftover transient file: %s", f.Path)
			}
		}
	})

	t.Run("remove", func(t *testing.T) {
		s := connect(t)
		if err := s.Put(stage(t, "z"), "x.txt"); err != nil {
			t.Fatal(err)
		}
		if err := s.Remove("x.txt"); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		files, _, _ := s.List()
		if _, ok := find(files, "x.txt"); ok {
			t.Error("x.txt still listed after Remove")
		}
	})

	t.Run("ensure_and_remove_dir", func(t *testing.T) {
		s := connect(t)
		if err := s.EnsureDir("empty"); err != nil {
			t.Fatalf("EnsureDir: %v", err)
		}
		_, dirs, _ := s.List()
		if !slices.Contains(dirs, "empty") {
			t.Errorf("dirs %v missing 'empty' after EnsureDir", dirs)
		}
		if err := s.RemoveDir("empty"); err != nil {
			t.Fatalf("RemoveDir empty: %v", err)
		}
		_, dirs, _ = s.List()
		if slices.Contains(dirs, "empty") {
			t.Error("'empty' still listed after RemoveDir")
		}
		// RemoveDir on a non-empty directory must error (faithful to SFTP).
		if err := s.Put(stage(t, "q"), "ne/f.txt"); err != nil {
			t.Fatal(err)
		}
		if err := s.RemoveDir("ne"); err == nil {
			t.Error("RemoveDir on non-empty dir must error")
		}
	})

	t.Run("ping_and_missing_get", func(t *testing.T) {
		s := connect(t)
		if err := s.Ping(); err != nil {
			t.Errorf("Ping while connected: %v", err)
		}
		if _, err := s.Get("does-not-exist.txt", filepath.Join(t.TempDir(), "n")); err == nil {
			t.Error("Get of missing remote file must error")
		}
	})
}
