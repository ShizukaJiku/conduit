package fixtures

import (
	"reflect"
	"testing"
)

// TestFileCasesMatchRules is the double-entry check: every hand-authored
// row must equal the codified rule. Catches mistakes on either side.
func TestFileCasesMatchRules(t *testing.T) {
	if len(FileCases) == 0 {
		t.Fatal("FileCases is empty — fixtures failed to load")
	}
	for _, c := range FileCases {
		if got := ExpectedWatch(c); got != c.Watch {
			t.Errorf("%s: Watch table=%q rule=%q (delta=%d sizeEq=%v L=%v R=%v)",
				c.Name, c.Watch, got, c.MtimeDeltaSec, c.SizeEqual, c.LocalPresent, c.RemotePresent)
		}
		if got := ExpectedDownload(c); got != c.Download {
			t.Errorf("%s: Download table=%q rule=%q (delta=%d sizeEq=%v L=%v R=%v)",
				c.Name, c.Download, got, c.MtimeDeltaSec, c.SizeEqual, c.LocalPresent, c.RemotePresent)
		}
	}
}

// TestStrictBoundaries pins the anti-loop semantics explicitly.
func TestStrictBoundaries(t *testing.T) {
	eq := FileCase{LocalPresent: true, RemotePresent: true, SizeEqual: true}

	minus2 := eq
	minus2.MtimeDeltaSec = -2
	if ExpectedWatch(minus2) != WatchNoop {
		t.Error("delta -2 must NOT upload (strict <-2)")
	}
	minus3 := eq
	minus3.MtimeDeltaSec = -3
	if ExpectedWatch(minus3) != WatchPut {
		t.Error("delta -3 must upload")
	}
	plus2 := eq
	plus2.MtimeDeltaSec = 2
	if ExpectedDownload(plus2) != DownloadNoop {
		t.Error("delta +2 must NOT download (strict >2)")
	}
	plus3 := eq
	plus3.MtimeDeltaSec = 3
	if ExpectedDownload(plus3) != DownloadGet {
		t.Error("delta +3 must download")
	}
}

// TestDownloadNeverDeletesLocal: no FileCase yields a destructive download.
func TestDownloadNeverDeletesLocal(t *testing.T) {
	c := FileCase{LocalPresent: true, RemotePresent: false}
	if ExpectedDownload(c) != DownloadNoop {
		t.Error("download must be additive: local-only file → noop, never delete")
	}
}

func TestDeepestFirst(t *testing.T) {
	got := DeepestFirst([]string{"a", "a/b/c", "a/b"})
	want := []string{"a/b/c", "a/b", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeepestFirst = %v, want %v", got, want)
	}
}

func TestEventAndDirTablesNonEmpty(t *testing.T) {
	if len(EventCases) == 0 || len(DirPruneCases) == 0 || len(IgnoredRemoteSuffixes) == 0 {
		t.Fatal("event/dir/suffix fixtures failed to load")
	}
	// moved must produce remove-then-put in that order.
	for _, e := range EventCases {
		if e.Kind == "moved" {
			if len(e.Expected) != 2 || e.Expected[0].Action != WatchRemove || e.Expected[1].Action != WatchPut {
				t.Fatalf("moved must be [remove,put], got %v", e.Expected)
			}
		}
	}
}
