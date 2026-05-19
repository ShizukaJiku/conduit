// Package fixtures is the executable parity oracle for conduit.
//
// It encodes — backend-neutrally, over abstract Storage operations — the
// exact behavior the watch/download engines must reproduce from the legacy
// Python tools. docs/PARITY.md is the prose spec; this package is the
// machine-checkable version. A consistency test asserts the hand-authored
// tables match the codified rules, so a divergence fails CI.
//
// Steps 4/5 import this package and assert their engine's decisions equal
// ExpectedWatch / ExpectedDownload over FileCases (and the dir/event
// tables), and use SecondsBetween for the canonical mtime comparison.
package fixtures

import (
	"sort"
	"strings"
	"time"
)

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

// IgnoredRemoteSuffixes are skipped by the download engine when scanning
// the remote (transient uploader files). Known limitation: a
// legitimately-named remote "x.part" is never downloaded (inherited from
// Python, not fixed). The watch engine does NOT filter these — a stale
// remote "*.part" with no local counterpart is pruned like any other.
var IgnoredRemoteSuffixes = []string{".part", ".tmp"}

// RemoteIgnored reports whether a remote entry name must be skipped by the
// download engine. Engines MUST use this so behavior stays in lockstep
// with the oracle.
func RemoteIgnored(name string) bool {
	for _, s := range IgnoredRemoteSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// SecondsBetween is the canonical mtime comparison: both times are
// truncated to whole seconds (SFTP/object stores report seconds), then
// subtracted. Returns remote_sec - local_sec. Engines MUST derive the
// mtime delta through this so sub-second jitter can never cause a
// re-upload/re-download loop.
func SecondsBetween(remote, local time.Time) int {
	return int(remote.Unix() - local.Unix())
}

// FileCase is one file's local/remote state and the expected decision of
// each engine. MtimeDeltaSec is remote_mtime_sec - local_mtime_sec, in
// whole seconds (mtimes are truncated to seconds before comparison;
// see SecondsBetween). Transient marks a remote entry whose name ends in
// an IgnoredRemoteSuffixes element.
type FileCase struct {
	Name          string
	LocalPresent  bool
	RemotePresent bool
	SizeEqual     bool // only meaningful when both present
	MtimeDeltaSec int  // remote - local, integer seconds
	Transient     bool // remote entry name ends in .part/.tmp
	Watch         WatchAction
	Download      DownloadAction
	Note          string
}

// ExpectedWatch is the codified watch rule (PARITY.md §1.2). Strict ±2s:
// upload only when local is strictly newer by more than 2 seconds. Watch
// does not filter transient names — it mirrors and prunes everything.
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

// ExpectedDownload is the codified download rule (PARITY.md §2.2/§2.3).
// Additive: never deletes local; ignores transient remote names; download
// only when remote is strictly newer by >2s.
func ExpectedDownload(c FileCase) DownloadAction {
	switch {
	case c.RemotePresent && c.Transient:
		return DownloadNoop // §2.3 — .part/.tmp skipped (known limitation)
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
		MtimeDeltaSec: 0,
		Watch:         WatchPut, Download: DownloadGet,
		Note: "size distinto manda en ambos; mtime es IRRELEVANTE aquí (delta 0 a propósito)"},

	// Transient remote names (.part/.tmp): download SIEMPRE los ignora
	// (§2.3); watch los trata como cualquier remoto huérfano y los poda.
	{Name: "remote-only-part", LocalPresent: false, RemotePresent: true, Transient: true,
		Watch: WatchRemove, Download: DownloadNoop,
		Note: "download IGNORA sufijo .part/.tmp (limitación heredada); watch sí limpia el huérfano"},
	{Name: "both-part-remote-newer", LocalPresent: true, RemotePresent: true, SizeEqual: true,
		MtimeDeltaSec: 99, Transient: true,
		Watch: WatchNoop, Download: DownloadNoop,
		Note: "aunque el remoto .part sea más nuevo, download lo ignora"},

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
	{Name: "delta-plus3", LocalPresent: true, RemotePresent: true, SizeEqual: true, MtimeDeltaSec: 3,
		Watch: WatchNoop, Download: DownloadGet, Note: "remoto > 2s más nuevo → descargar"},
}

// DirCase: a remote directory and whether it exists locally. Used for the
// destructive prune in watch full-resync (PARITY.md §1.3).
type DirCase struct {
	RemoteRel    string
	LocalPresent bool
}

// DirPruneCases: a nested non-empty remote tree absent locally plus a
// directory that exists locally and must survive.
var DirPruneCases = []DirCase{
	{RemoteRel: "a", LocalPresent: false},
	{RemoteRel: "a/b", LocalPresent: false},
	{RemoteRel: "a/b/c", LocalPresent: false},
	{RemoteRel: "keep", LocalPresent: true}, // present locally → NOT pruned
}

// ExpectedDirPruned reports whether watch must RemoveDir this directory: a
// remote directory is pruned iff it does not exist locally.
func ExpectedDirPruned(c DirCase) bool { return !c.LocalPresent }

// PruneOrder returns the directories that must be removed, in the exact
// order watch must call Storage.RemoveDir (deepest-first), excluding any
// that exist locally.
func PruneOrder(cases []DirCase) []string {
	var rel []string
	for _, c := range cases {
		if ExpectedDirPruned(c) {
			rel = append(rel, c.RemoteRel)
		}
	}
	return DeepestFirst(rel)
}

// DeepestFirst orders directory paths so children precede parents. Ties
// (same depth) break on reverse-lexicographic order for determinism.
func DeepestFirst(dirs []string) []string {
	out := append([]string(nil), dirs...)
	sort.Slice(out, func(i, j int) bool {
		di, dj := depth(out[i]), depth(out[j])
		if di != dj {
			return di > dj // deeper first
		}
		return out[i] > out[j] // deterministic tiebreak (reverse-lex)
	})
	return out
}

func depth(p string) int { return strings.Count(p, "/") }

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
