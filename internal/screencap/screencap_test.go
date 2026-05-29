package screencap

import (
	"image"
	"testing"
	"time"
)

func TestParseHotkey(t *testing.T) {
	tests := []struct {
		in       string
		wantMods uint32
		wantVK   uint32
		wantText string
	}{
		{"ctrl+shift+s", ModControl | ModShift, 'S', "ctrl+shift+s"},
		{"CTRL+SHIFT+S", ModControl | ModShift, 'S', "ctrl+shift+s"},
		{" ctrl + alt + a ", ModControl | ModAlt, 'A', "ctrl+alt+a"},
		{"control+1", ModControl, '1', "ctrl+1"},
		{"win+shift+f4", ModWin | ModShift, 0x73, "shift+win+f4"},
		{"super+meta+q", ModWin, 'Q', "win+q"}, // duplicate Win modifier collapses
		{"cmd+f12", ModWin, 0x7B, "win+f12"},
	}
	for _, tt := range tests {
		got, err := ParseHotkey(tt.in)
		if err != nil {
			t.Errorf("ParseHotkey(%q) error inesperado: %v", tt.in, err)
			continue
		}
		if got.Mods != tt.wantMods || got.VK != tt.wantVK {
			t.Errorf("ParseHotkey(%q) = {mods:%#x vk:%#x}, want {mods:%#x vk:%#x}",
				tt.in, got.Mods, got.VK, tt.wantMods, tt.wantVK)
		}
		if got.Text != tt.wantText {
			t.Errorf("ParseHotkey(%q).Text = %q, want %q", tt.in, got.Text, tt.wantText)
		}
	}
}

func TestParseHotkeyErrors(t *testing.T) {
	for _, in := range []string{
		"",           // empty
		"ctrl",       // no key
		"ctrl+shift", // still no key (both are modifiers)
		"s",          // no modifier
		"ctrl+s+a",   // two keys
		"ctrl+enter", // unsupported key
		"ctrl+f0",    // F0 out of range
		"ctrl+f25",   // F25 out of range
		"ctrl+#",     // unsupported symbol
	} {
		if _, err := ParseHotkey(in); err == nil {
			t.Errorf("ParseHotkey(%q) esperaba error, got nil", in)
		}
	}
}

func TestScreenshotName(t *testing.T) {
	tm := time.Date(2026, 5, 28, 14, 3, 7, 123_000_000, time.UTC)
	if got, want := screenshotName(tm), "screenshot-20260528-140307.123.png"; got != want {
		t.Errorf("screenshotName = %q, want %q", got, want)
	}
}

func TestNormalizeRect(t *testing.T) {
	want := image.Rect(10, 20, 110, 220)
	// Any pair of opposite corners must yield the same canonical rect.
	corners := [][2]image.Point{
		{{10, 20}, {110, 220}}, // top-left → bottom-right
		{{110, 220}, {10, 20}}, // bottom-right → top-left
		{{110, 20}, {10, 220}}, // top-right → bottom-left
		{{10, 220}, {110, 20}}, // bottom-left → top-right
	}
	for _, c := range corners {
		if got := normalizeRect(c[0], c[1]); got != want {
			t.Errorf("normalizeRect(%v,%v) = %v, want %v", c[0], c[1], got, want)
		}
	}
}

func TestNormalizeRectEmpty(t *testing.T) {
	r := normalizeRect(image.Point{X: 5, Y: 5}, image.Point{X: 5, Y: 50})
	if r.Dx() != 0 {
		t.Errorf("puntos colineales en X deberían dar ancho 0, got Dx=%d", r.Dx())
	}
}
