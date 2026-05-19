package fixtures

import (
	"reflect"
	"testing"
	"time"
)

// TestFileCasesMatchRules is the double-entry check: every hand-authored
// row must equal the codified rule. Catches mistakes on either side.
func TestFileCasesMatchRules(t *testing.T) {
	if len(FileCases) == 0 {
		t.Fatal("FileCases is empty — fixtures failed to load")
	}
	for _, c := range FileCases {
		if got := ExpectedWatch(c); got != c.Watch {
			t.Errorf("%s: Watch table=%q rule=%q (delta=%d sizeEq=%v L=%v R=%v tr=%v)",
				c.Name, c.Watch, got, c.MtimeDeltaSec, c.SizeEqual, c.LocalPresent, c.RemotePresent, c.Transient)
		}
		if got := ExpectedDownload(c); got != c.Download {
			t.Errorf("%s: Download table=%q rule=%q (delta=%d sizeEq=%v L=%v R=%v tr=%v)",
				c.Name, c.Download, got, c.MtimeDeltaSec, c.SizeEqual, c.LocalPresent, c.RemotePresent, c.Transient)
		}
	}
}

// TestStrictBoundaries pins the anti-loop semantics explicitly.
func TestStrictBoundaries(t *testing.T) {
	eq := FileCase{LocalPresent: true, RemotePresent: true, SizeEqual: true}
	cases := []struct {
		delta int
		watch WatchAction
		dl    DownloadAction
	}{
		{-3, WatchPut, DownloadNoop},
		{-2, WatchNoop, DownloadNoop}, // strict: -2 does NOT upload
		{2, WatchNoop, DownloadNoop},  // strict: +2 does NOT download
		{3, WatchNoop, DownloadGet},
	}
	for _, tc := range cases {
		c := eq
		c.MtimeDeltaSec = tc.delta
		if got := ExpectedWatch(c); got != tc.watch {
			t.Errorf("delta %d: watch=%q want %q", tc.delta, got, tc.watch)
		}
		if got := ExpectedDownload(c); got != tc.dl {
			t.Errorf("delta %d: download=%q want %q", tc.delta, got, tc.dl)
		}
	}
}

// TestSubSecondTruncation pins the actual contract the misleading
// "subsec" fixture row could not represent.
func TestSubSecondTruncation(t *testing.T) {
	// Core anti-loop guarantee: sub-second jitter WITHIN the same wall
	// second collapses to delta 0 → noop for both engines. This is the
	// case that actually caused re-upload/re-download loops in practice.
	local := time.Unix(1_000_000, 100*int64(time.Millisecond))
	remote := time.Unix(1_000_000, 900*int64(time.Millisecond))
	if d := SecondsBetween(remote, local); d != 0 {
		t.Fatalf("same-second jitter: SecondsBetween = %d, want 0", d)
	}
	jitter := FileCase{LocalPresent: true, RemotePresent: true, SizeEqual: true,
		MtimeDeltaSec: SecondsBetween(remote, local)}
	if ExpectedWatch(jitter) != WatchNoop || ExpectedDownload(jitter) != DownloadNoop {
		t.Error("sub-second jitter within a second must be noop on both engines")
	}

	// A remote 2s+999ms newer truncates to an integer delta of 2 (not 3,
	// since the real diff < 3s) and must stay noop on the strict +2 edge.
	base := time.Unix(1_000_000, 0)
	newer := base.Add(2*time.Second + 999*time.Millisecond)
	if d := SecondsBetween(newer, base); d != 2 {
		t.Fatalf("2s+999ms truncation = %d, want 2", d)
	}
	edge := FileCase{LocalPresent: true, RemotePresent: true, SizeEqual: true,
		MtimeDeltaSec: SecondsBetween(newer, base)}
	if ExpectedDownload(edge) != DownloadNoop {
		t.Error("2s+999ms must truncate to 2 and stay noop (anti-loop)")
	}
}

func TestRemoteIgnored(t *testing.T) {
	for _, name := range []string{"x.part", "a/b/y.tmp", "deep/z.part"} {
		if !RemoteIgnored(name) {
			t.Errorf("RemoteIgnored(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"x.txt", "part.go", "report.tmpl", "a/b.partition"} {
		if RemoteIgnored(name) {
			t.Errorf("RemoteIgnored(%q) = true, want false", name)
		}
	}
}

// TestDownloadIgnoresTransient: a transient remote, even if newer, is noop;
// download stays additive.
func TestDownloadIgnoresTransient(t *testing.T) {
	c := FileCase{RemotePresent: true, Transient: true, MtimeDeltaSec: 99}
	if ExpectedDownload(c) != DownloadNoop {
		t.Error("transient remote (.part/.tmp) must never download")
	}
	if ExpectedDownload(FileCase{LocalPresent: true, RemotePresent: false}) != DownloadNoop {
		t.Error("download must be additive: local-only file → noop, never delete")
	}
}

func TestExpectedDirPruneAndOrder(t *testing.T) {
	for _, c := range DirPruneCases {
		want := !c.LocalPresent
		if ExpectedDirPruned(c) != want {
			t.Errorf("%s: ExpectedDirPruned=%v want %v", c.RemoteRel, ExpectedDirPruned(c), want)
		}
	}
	got := PruneOrder(DirPruneCases)
	want := []string{"a/b/c", "a/b", "a"} // deepest-first, "keep" excluded
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PruneOrder = %v, want %v (keep must be excluded, deepest-first)", got, want)
	}
}

func TestDeepestFirst(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"a", "a/b/c", "a/b"}, []string{"a/b/c", "a/b", "a"}},
		{[]string{"a/y", "a/x"}, []string{"a/y", "a/x"}}, // same depth → reverse-lex tiebreak
		{[]string{"solo"}, []string{"solo"}},
		{nil, nil},
	}
	for _, tc := range cases {
		if got := DeepestFirst(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("DeepestFirst(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestEventTable(t *testing.T) {
	if len(EventCases) == 0 {
		t.Fatal("EventCases failed to load")
	}
	for _, e := range EventCases {
		if e.Kind == "moved" {
			if len(e.Expected) != 2 || e.Expected[0].Action != WatchRemove || e.Expected[1].Action != WatchPut {
				t.Fatalf("moved must be [remove,put] in order, got %v", e.Expected)
			}
		}
	}
}
