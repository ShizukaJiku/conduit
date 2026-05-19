// Package logx is conduit's logging facade: a fixed text format
// "ts [LEVEL] msg" fanned out to one or more sinks, each with a minimum
// level. The file sink always gets everything; the stderr sink only gets
// Info when --verbose is set (Warn/Error always reach stderr).
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type level int

const (
	levelInfo level = iota
	levelWarn
	levelError
)

func (l level) tag() string {
	switch l {
	case levelInfo:
		return "INFO"
	case levelWarn:
		return "WARN"
	default:
		return "ERROR"
	}
}

type sink struct {
	w   io.Writer
	min level
}

// Logger is safe for concurrent use.
type Logger struct {
	mu    sync.Mutex
	sinks []sink
	now   func() time.Time // injectable for tests
}

// New builds a Logger writing every level to w. A nil writer discards.
func New(w io.Writer) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{sinks: []sink{{w, levelInfo}}, now: time.Now}
}

// DefaultPath is ~/.conduit/conduit.log.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("logx: home dir: %w", err)
	}
	return filepath.Join(home, ".conduit", "conduit.log"), nil
}

// Open is OpenLeveled(path, true) — every level to file and stderr.
func Open(path string) (*Logger, io.Closer, error) { return OpenLeveled(path, true) }

// OpenLeveled appends to path (creating parent dirs) and tees to stderr.
// The file always receives every level; stderr receives Info only when
// verbose, but Warn/Error always.
func OpenLeveled(path string, verbose bool) (*Logger, io.Closer, error) {
	// 0700/0600: ~/.conduit holds the config (with a password); a
	// world-readable log dir/file would undermine that.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("logx: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("logx: open %s: %w", path, err)
	}
	stderrMin := levelWarn
	if verbose {
		stderrMin = levelInfo
	}
	lg := &Logger{
		sinks: []sink{{f, levelInfo}, {os.Stderr, stderrMin}},
		now:   time.Now,
	}
	return lg, f, nil
}

func (l *Logger) logf(lv level, format string, args ...any) {
	if l == nil || len(l.sinks) == 0 {
		return
	}
	line := fmt.Sprintf("%s [%s] %s\n",
		l.now().Format("2006-01-02 15:04:05"), lv.tag(), fmt.Sprintf(format, args...))
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.sinks {
		if lv >= s.min {
			_, _ = io.WriteString(s.w, line)
		}
	}
}

func (l *Logger) Infof(format string, args ...any)  { l.logf(levelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(levelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(levelError, format, args...) }
