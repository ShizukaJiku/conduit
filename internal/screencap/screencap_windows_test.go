//go:build windows

package screencap

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSavePNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	dir := filepath.Join(t.TempDir(), "screenshot") // must be created by savePNG
	tm := time.Date(2026, 5, 28, 14, 3, 7, 5_000_000, time.UTC)

	path, err := savePNG(img, dir, tm)
	if err != nil {
		t.Fatalf("savePNG: %v", err)
	}
	if want := "screenshot-20260528-140307.005.png"; filepath.Base(path) != want {
		t.Errorf("filename = %q, want %q", filepath.Base(path), want)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("path %q not under %q", path, dir)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := png.Decode(f)
	if err != nil {
		t.Fatalf("written file is not valid PNG: %v", err)
	}
	if got.Bounds() != img.Bounds() {
		t.Errorf("decoded bounds = %v, want %v", got.Bounds(), img.Bounds())
	}
}

// TestCursorPosSmoke exercises the GetCursorPos syscall path: it must
// return ok on a normal interactive/CI session without panicking.
func TestCursorPosSmoke(t *testing.T) {
	if _, ok := cursorPos(); !ok {
		t.Skip("GetCursorPos returned !ok (headless session); nothing to assert")
	}
	// keyDown must not panic and returns a bool for a never-pressed key.
	_ = keyDown(vkEscape)
}
