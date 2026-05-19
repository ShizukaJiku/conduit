// Package fixtures is the executable parity oracle for conduit.
//
// It encodes — backend-neutrally, over abstract Storage operations — the
// exact behavior the watch/download engines must reproduce from the legacy
// Python tools. docs/PARITY.md is the prose spec; this package is the
// machine-checkable version. A consistency test asserts the hand-authored
// tables match the codified rules, so a divergence fails CI.
//
// Steps 4/5 import this package and assert their engine's decisions equal
// ExpectedWatch / ExpectedDownload over FileCases (and the dir/event tables).
package fixtures

import "sort"

// WatchAction is the decision of the local→remote (watch) engine for one file.
type WatchAction string

const (
	WatchNoop   WatchAction = "noop"
	WatchPut    WatchAction = "put"    // upload local → remote
	WatchRemove WatchAction = "remove" // remote file no longer exists locally
)

// DownloadAction is the decision of the remote→local (download) engine.
// There is deliberately no "delete local" action: download is additive.
type DownloadAction string

const (
	DownloadNoop DownloadAction = "noop"
	DownloadGet  DownloadAction = "get" // download remote → local
)

// FileCase is one file's local/remote state and the expected decision of
// each engine. MtimeDeltaSec is remote_mtime_sec - local_mtime_sec, in
// whole seconds (mtimes are truncated to seconds before comparison).
type FileCase struct {
	Name          string
	LocalPresent  bool
	RemotePresent bool
	SizeEqual     bool // only meaningful when both present
	MtimeDeltaSec int  // remote - local, integer seconds
	Watch         WatchAction
	Download      DownloadAction
	Note          string
}

// ExpectedWatch is the codified watch rule (PARITY.md §1.2). Strict ±2s:
// upload only when local is strictly newer by more than 2 seconds.
func ExpectedWatch(c FileCase) WatchAction {
	switch {
	case c.LocalPresent && !c.RemotePresent:
		return WatchPut
	case c.LocalPresent && c.RemotePresent && !c.SizeEqual:
		return WatchPut
	case c.LocalPresent && c.RemotePresent && c.SizeEqual && c.MtimeDeltaSec < -2:
		return WatchPut
	case !c.LocalPresent && c.RemotePresent:
		return WatchRemove
	default:
		return WatchNoop
	}
}

// ExpectedDownload is the codified download rule (PARITY.md §2.2). Additive:
// never deletes local; download only when remote is strictly newer by >2s.
func ExpectedDownload(c FileCase) DownloadAction {
	switch {
	case !c.LocalPresent && c.RemotePresent:
		return DownloadGet
	case c.LocalPresent && c.RemotePresent && !c.SizeEqual:
		return DownloadGet
	case c.LocalPresent && c.RemotePresent && c.SizeEqual && c.MtimeDeltaSec > 2:
		return DownloadGet
	default:
		return DownloadNoop
	}
}

// FileCases is the hand-authored decision matrix. The consistency test
// asserts every row matches ExpectedWatch/ExpectedDownload, catching both
// authoring mistakes here and rule mistakes there (double-entry).
var FileCases = []FileCase{
	{Name: "local-only", LocalPresent: true, RemotePresent: false,
		Watch: WatchPut, Download: DownloadNoop, Note: "subir; nada que descargar"},
	{Name: "remote-only", LocalPresent: false, RemotePresent: true,
		Watch: WatchRemove, Download: DownloadGet, Note: "watch borra remoto; download lo trae"},
	{Name: "neither", LocalPresent: false, RemotePresent: false,
		Watch: WatchNoop, Download: DownloadNoop},
	{Name: "both-size-differ", LocalPresent: true, RemotePresent: true, SizeEqual: false,
		Watch: WatchPut, Download: DownloadGet, Note: "size distinto manda en ambos"},

	// Both present, size equal — mtime boundary sweep (delta = remote - local).
	{Name: "delta-minus3", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: -3,
		Watch: WatchPut, Download: DownloadNoop, Note: "local > 2s más nuevo → subir"},
	{Name: "delta-minus2", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: -2,
		Watch: WatchNoop, Download: DownloadNoop, Note: "frontera estricta: -2 NO sube (anti-bucle)"},
	{Name: "delta-minus1", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: -1,
		Watch: WatchNoop, Download: DownloadNoop},
	{Name: "delta-zero", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: 0,
		Watch: WatchNoop, Download: DownloadNoop},
	{Name: "delta-plus1", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: 1,
		Watch: WatchNoop, Download: DownloadNoop},
	{Name: "delta-plus2", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: 2,
		Watch: WatchNoop, Download: DownloadNoop, Note: "frontera estricta: +2 NO descarga (anti-bucle)"},
	{Name: "delta-plus2-subsec", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: 2,
		Watch: WatchNoop, Download: DownloadNoop,
		Note: "remoto +2s y fracción: trunca a 2 → noop. El driver DEBE truncar a segundos."},
	{Name: "delta-plus3", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: 3,
		Watch: WatchNoop, Download: DownloadGet, Note: "remoto > 2s más nuevo → descargar"},
}

// DirCase: a remote directory and whether it exists locally. Used for the
// destructive prune in watch full-resync (PARITY.md §1.3).
type DirCase struct {
	RemoteRel    string
	LocalPresent bool
}

// DirPruneCases: a nested non-empty remote tree absent locally. Expected:
// every dir removed, deepest-first, so RemoveDir never hits a non-empty dir.
var DirPruneCases = []DirCase{
	{RemoteRel: "a", LocalPresent: false},
	{RemoteRel: "a/b", LocalPresent: false},
	{RemoteRel: "a/b/c", LocalPresent: false},
	{RemoteRel: "keep", LocalPresent: true}, // present locally → NOT pruned
}

// DeepestFirst orders directory paths so children precede parents. This is
// the order in which watch must call Storage.RemoveDir.
func DeepestFirst(dirs []string) []string {
	out := append([]string(nil), dirs...)
	sort.Slice(out, func(i, j int) bool {
		di, dj := depth(out[i]), depth(out[j])
		if di != dj {
			return di > dj // deeper first
		}
		return out[i] > out[j] // deterministic tiebreak
	})
	return out
}

func depth(p string) int {
	n := 0
	for _, r := range p {
		if r == '/' {
			n++
		}
	}
	return n
}

// WatchOp is a single Storage operation produced by a live watcher event.
type WatchOp struct {
	Action WatchAction
	Path   string
}

// EventCase maps a watcher event to the ordered Storage ops watch must emit
// (PARITY.md §1.4). For "moved", Src is the old path and Dst the new one.
type EventCase struct {
	Kind     string // "created" | "modified" | "deleted" | "moved"
	Src      string
	Dst      string
	Expected []WatchOp
}

var EventCases = []EventCase{
	{Kind: "created", Src: "f.txt", Expected: []WatchOp{{WatchPut, "f.txt"}}},
	{Kind: "modified", Src: "f.txt", Expected: []WatchOp{{WatchPut, "f.txt"}}},
	{Kind: "deleted", Src: "f.txt", Expected: []WatchOp{{WatchRemove, "f.txt"}}},
	{Kind: "moved", Src: "old.txt", Dst: "new.txt",
		Expected: []WatchOp{{WatchRemove, "old.txt"}, {WatchPut, "new.txt"}}},
}

// IgnoredRemoteSuffixes are skipped by the download engine when scanning the
// remote (transient uploader files). Known limitation: a legitimately-named
// remote "x.part" is never downloaded (inherited from Python, not fixed).
var IgnoredRemoteSuffixes = []string{".part", ".tmp"}
