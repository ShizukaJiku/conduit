//go:build !windows

package screencap

import (
	"context"
	"errors"

	"github.com/ShizukaJiku/conduit/internal/logx"
)

// Run is unsupported off Windows: the global hotkey, mouse polling and GDI
// capture are Win32-only. Returns an error so the caller can log and keep
// the rest of the watch running.
func Run(_ context.Context, _ Options, _ *logx.Logger) error {
	return errors.New("screen: la captura de pantalla solo está soportada en Windows")
}
