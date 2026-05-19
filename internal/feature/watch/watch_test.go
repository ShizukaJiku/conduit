package watch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/feature"
	_ "github.com/ShizukaJiku/conduit/internal/feature/watch"
	"github.com/ShizukaJiku/conduit/internal/storage"
	"github.com/ShizukaJiku/conduit/internal/storage/memfs"
)

func watchFeature(t *testing.T) feature.Feature {
	t.Helper()
	for _, f := range feature.All() {
		if f.Name() == "watch" {
			return f
		}
	}
	t.Fatal("watch feature not registered")
	return nil
}

func TestRegisteredAndCommand(t *testing.T) {
	f := watchFeature(t)
	if f.Synopsis() == "" {
		t.Error("empty synopsis")
	}
	cmd := f.NewCommand(feature.Deps{})
	if cmd.Use != "watch" {
		t.Errorf("cmd.Use = %q, want watch", cmd.Use)
	}
}

func TestRunValidationErrors(t *testing.T) {
	f := watchFeature(t)

	// Nil config.
	cmd := f.NewCommand(feature.Deps{})
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected error with nil config")
	}
	// Empty local folder.
	cmd = f.NewCommand(feature.Deps{Config: &config.Config{}})
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected error with empty local_folder")
	}
	// Missing storage factory.
	cmd = f.NewCommand(feature.Deps{Config: &config.Config{LocalFolder: t.TempDir()}})
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected error with nil Storage factory")
	}
}

func TestRunMirrorsAndStopsOnCancel(t *testing.T) {
	// Keep the log sink inside a temp HOME (hermetic).
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "f.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	shared := memfs.NewClocked(func() time.Time { return time.Unix(1_700_000_000, 0) })
	deps := feature.Deps{
		Config: &config.Config{LocalFolder: local, Backend: config.Backend{Type: "memfs"}},
		Storage: func(*config.Config) (storage.Storage, error) {
			return shared, nil
		},
	}

	f := watchFeature(t)
	cmd := f.NewCommand(deps)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()

	deadline := time.After(8 * time.Second)
	dst := filepath.Join(t.TempDir(), "out")
	for {
		if _, err := shared.Get("f.txt", dst); err == nil {
			break // FullResync uploaded it
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("watch did not mirror f.txt before timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch RunE returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not stop after cancel")
	}
}
