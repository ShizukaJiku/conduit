// Package logx is conduit's logging facade. The file + stderr sink,
// levels and formatting are implemented in the storage-core step; this
// minimal Logger keeps the feature SPI stable in the meantime.
package logx

import (
	"io"
	"log"
)

// Logger is a thin wrapper so callers don't depend on the std log type.
type Logger struct {
	l *log.Logger
}

// New builds a Logger writing to w. A nil writer discards output.
func New(w io.Writer) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{l: log.New(w, "", log.LstdFlags)}
}

// Printf logs a formatted line. Safe on a nil Logger.
func (lg *Logger) Printf(format string, args ...any) {
	if lg == nil || lg.l == nil {
		return
	}
	lg.l.Printf(format, args...)
}
