package download_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/feature"
	_ "github.com/ShizukaJiku/conduit/internal/feature/download"
	"github.com/ShizukaJiku/conduit/internal/storage"
	"github.com/ShizukaJiku/conduit/internal/storage/memfs"
)

func downloadFeature(t *testing.T) feature.Feature {
	t.Helper()
	for _, f := range feature.All() {
		if f.Name() == "download" {
			return f
		}
	}
	t.Fatal("download feature not registered")
	return nil
}

func TestRegisteredAndCommand(t *testing.T) {
	f := downloadFeature(t)
	if f.Synopsis() == "" {
		t.Error("empty synopsis")
	}
	if cmd := f.NewCommand(feature.Deps{}); cmd.Use != "download" {
		t.Errorf("cmd.Use = %q, want download", cmd.Use)
	}
}

func TestRunValidationErrors(t *testing.T) {
	f := downloadFeature(t)
	cmd := f.NewCommand(feature.Deps{})
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected error with nil config")
	}
	cmd = f.NewCommand(feature.Deps{Config: &config.Config{}})
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected error with empty local_folder")
	}
	cmd = f.NewCommand(feature.Deps{Config: &config.Config{LocalFolder: t.TempDir()}})
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected error with nil Storage factory")
	}
}

func TestRunMirrorsAndStopsOnCancel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	local := t.TempDir()
	shared := memfs.NewClocked(func() time.Time { return time.Unix(1_700_000_000, 0) })
	// Seed a remote file (memfs Put does not require Connect).
	src := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := shared.Put(src, "r.txt"); err != nil {
		t.Fatal(err)
	}

	deps := feature.Deps{
		Config: &config.Config{LocalFolder: local, PollSeconds: 0, Backend: config.Backend{Type: "memfs"}},
		Storage: func(*config.Config) (storage.Storage, error) {
			return shared, nil
		},
	}
	f := downloadFeature(t)
	cmd := f.NewCommand(deps)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()

	deadline := time.After(8 * time.Second)
	for {
		if b, err := os.ReadFile(filepath.Join(local, "r.txt")); err == nil && string(b) == "payload" {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("download did not mirror r.txt before timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("download RunE returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download did not stop after cancel")
	}
}
