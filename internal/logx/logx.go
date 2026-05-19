// Package logx is conduit's logging facade: append to a file and tee to
// stderr, with a fixed text format "ts [LEVEL] msg".
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type level string

const (
	lvlInfo  level = "INFO"
	lvlWarn  level = "WARN"
	lvlError level = "ERROR"
)

// Logger is safe for concurrent use.
type Logger struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time // injectable for tests
}

// New builds a Logger writing to w. A nil writer discards output.
func New(w io.Writer) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{w: w, now: time.Now}
}

// DefaultPath is ~/.conduit/conduit.log.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("logx: home dir: %w", err)
	}
	return filepath.Join(home, ".conduit", "conduit.log"), nil
}

// Open opens path in append mode (creating parent dirs) and tees output to
// stderr. The returned closer closes the underlying file.
func Open(path string) (*Logger, io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("logx: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("logx: open %s: %w", path, err)
	}
	return &Logger{w: io.MultiWriter(f, os.Stderr), now: time.Now}, f, nil
}

func (l *Logger) logf(lv level, format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	line := fmt.Sprintf("%s [%s] %s\n",
		l.now().Format("2006-01-02 15:04:05"), lv, fmt.Sprintf(format, args...))
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.w, line)
}

func (l *Logger) Infof(format string, args ...any)  { l.logf(lvlInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(lvlWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(lvlError, format, args...) }
