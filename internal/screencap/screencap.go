// Package screencap is the "screen" capability behind `conduit watch
// --screen`: a global hotkey arms a two-click rectangular screenshot that
// is written into a subfolder of the watched local folder, so the watch
// engine uploads it like any other file (no change to the sync core).
//
// The capture itself is OS-specific (global hotkey + mouse hooks + GDI),
// so Run lives in screencap_windows.go; non-Windows builds get a stub in
// screencap_other.go. The pieces in THIS file are platform-agnostic and
// unit-tested everywhere: hotkey parsing, output filename, rect math.
package screencap

import (
	"fmt"
	"image"
	"strconv"
	"strings"
	"time"
)

// Win32 RegisterHotKey modifier flags (also used by the hotkey parser, so
// they live in the agnostic file as the single source of truth).
const (
	ModAlt     uint32 = 0x0001
	ModControl uint32 = 0x0002
	ModShift   uint32 = 0x0004
	ModWin     uint32 = 0x0008
)

// Hotkey is a parsed global hotkey: a modifier mask plus a virtual-key code.
type Hotkey struct {
	Mods uint32 // OR of Mod* flags
	VK   uint32 // Win32 virtual-key code
	Text string // canonical form for logging, e.g. "ctrl+shift+s"
}

// Options configures the capture daemon.
type Options struct {
	Dir    string // destination directory for PNGs (e.g. <local>/screenshot)
	Hotkey Hotkey // the armed combination
}

var modAliases = map[string]uint32{
	"ctrl":    ModControl,
	"control": ModControl,
	"shift":   ModShift,
	"alt":     ModAlt,
	"win":     ModWin,
	"super":   ModWin,
	"cmd":     ModWin,
	"meta":    ModWin,
}

// canonicalMods renders a modifier mask back to text in a stable order.
func canonicalMods(mods uint32) []string {
	var out []string
	if mods&ModControl != 0 {
		out = append(out, "ctrl")
	}
	if mods&ModShift != 0 {
		out = append(out, "shift")
	}
	if mods&ModAlt != 0 {
		out = append(out, "alt")
	}
	if mods&ModWin != 0 {
		out = append(out, "win")
	}
	return out
}

// keyToVK maps a single key token to its Win32 virtual-key code. Letters
// and digits map to their ASCII uppercase value (the VK codes coincide);
// F1–F24 map to VK_F1+offset (0x70..). tok is assumed already lowercased
// and trimmed (ParseHotkey guarantees this).
func keyToVK(tok string) (vk uint32, canon string, ok bool) {
	if len(tok) == 1 {
		c := tok[0]
		switch {
		case c >= 'a' && c <= 'z':
			return uint32(c - 'a' + 'A'), string(c), true
		case c >= '0' && c <= '9':
			return uint32(c), string(c), true
		}
		return 0, "", false
	}
	if tok[0] == 'f' {
		if n, err := strconv.Atoi(tok[1:]); err == nil && n >= 1 && n <= 24 {
			return uint32(0x70 + (n - 1)), fmt.Sprintf("f%d", n), true
		}
	}
	return 0, "", false
}

// ParseHotkey parses a combination like "ctrl+shift+s" into a Hotkey. It
// requires at least one modifier and exactly one non-modifier key (the
// last token). Tokens are case-insensitive and trimmed.
func ParseHotkey(s string) (Hotkey, error) {
	raw := strings.Split(s, "+")
	var mods uint32
	var keyTok string
	for _, t := range raw {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if m, ok := modAliases[t]; ok {
			mods |= m
			continue
		}
		if keyTok != "" {
			return Hotkey{}, fmt.Errorf("hotkey %q: más de una tecla (%q y %q)", s, keyTok, t)
		}
		keyTok = t
	}
	if keyTok == "" {
		return Hotkey{}, fmt.Errorf("hotkey %q: falta la tecla (p.ej. ctrl+shift+s)", s)
	}
	if mods == 0 {
		return Hotkey{}, fmt.Errorf("hotkey %q: requiere al menos un modificador (ctrl/shift/alt/win)", s)
	}
	vk, canonKey, ok := keyToVK(keyTok)
	if !ok {
		return Hotkey{}, fmt.Errorf("hotkey %q: tecla no soportada %q (usá A-Z, 0-9 o F1-F24)", s, keyTok)
	}
	text := strings.Join(append(canonicalMods(mods), canonKey), "+")
	return Hotkey{Mods: mods, VK: vk, Text: text}, nil
}

// screenshotName is the output filename for a capture taken at t. The
// millisecond field keeps names unique within the same second.
func screenshotName(t time.Time) string {
	return "screenshot-" + t.Format("20060102-150405.000") + ".png"
}

// normalizeRect builds the canonical rectangle whose opposite corners are
// a and b (image.Rect already orders Min before Max). A zero-area result
// means the two points coincided on an axis.
func normalizeRect(a, b image.Point) image.Rectangle {
	return image.Rect(a.X, a.Y, b.X, b.Y)
}
