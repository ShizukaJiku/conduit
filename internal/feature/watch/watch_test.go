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

func TestScreenFlag(t *testing.T) {
	cmd := watchFeature(t).NewCommand(feature.Deps{})
	f := cmd.Flags().Lookup("screen")
	if f == nil {
		t.Fatal("watch command is missing the --screen flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--screen default = %q, want false", f.DefValue)
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

// TestScreenInvalidHotkeyIsNonFatal verifies that enabling --screen with a
// bad hotkey logs and disables capture WITHOUT aborting the watch (and
// without registering a real global hotkey). The context is pre-cancelled
// so the mirror returns immediately.
func TestScreenInvalidHotkeyIsNonFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	shared := memfs.NewClocked(func() time.Time { return time.Unix(1_700_000_000, 0) })
	deps := feature.Deps{
		Config: &config.Config{
			LocalFolder: t.TempDir(),
			Backend:     config.Backend{Type: "memfs"},
			Screen:      config.ScreenConfig{Hotkey: "not-a-valid-hotkey", Dir: "screenshot"},
		},
		Storage: func(*config.Config) (storage.Storage, error) { return shared, nil },
	}

	cmd := watchFeature(t).NewCommand(deps)
	if err := cmd.Flags().Set("screen", "true"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // mirror returns at once; the bad hotkey path runs first
	cmd.SetContext(ctx)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("an invalid screen hotkey must not abort watch, got %v", err)
	}
}

// TestScreenDirEscapeIsRejected verifies a screen.dir that would resolve
// outside local_folder disables capture without aborting watch and without
// creating the escaping directory.
func TestScreenDirEscapeIsRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	local := t.TempDir()
	shared := memfs.NewClocked(func() time.Time { return time.Unix(1_700_000_000, 0) })
	deps := feature.Deps{
		Config: &config.Config{
			LocalFolder: local,
			Backend:     config.Backend{Type: "memfs"},
			Screen:      config.ScreenConfig{Hotkey: "ctrl+shift+s", Dir: "../escape"},
		},
		Storage: func(*config.Config) (storage.Storage, error) { return shared, nil },
	}

	cmd := watchFeature(t).NewCommand(deps)
	if err := cmd.Flags().Set("screen", "true"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("an escaping screen.dir must not abort watch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(local), "escape")); err == nil {
		t.Error("escaping directory was created; containment guard failed")
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
