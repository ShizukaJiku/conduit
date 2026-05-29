//go:build windows

package screencap

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
	"golang.org/x/sys/windows"

	"github.com/ShizukaJiku/conduit/internal/logx"
)

const (
	modNoRepeat = 0x4000 // MOD_NOREPEAT: one WM_HOTKEY per physical press
	wmQuit      = 0x0012
	wmHotkey    = 0x0312
	vkLButton   = 0x01
	vkEscape    = 0x1B
	keyDownBit  = 0x8000 // GetAsyncKeyState high bit = key currently down

	pollEvery     = 10 * time.Millisecond
	captureWindow = 30 * time.Second // abort a selection with no clicks
)

// Supported reports whether screen capture works on this OS (true here).
const Supported = true

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

type point struct{ X, Y int32 }

// msg mirrors the Win32 MSG. Field order matches C; Go applies the same
// platform alignment, so offsets agree without manual padding.
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

func keyDown(vk uint32) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&keyDownBit != 0
}

func cursorPos() (image.Point, bool) {
	var p point
	r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if r == 0 {
		return image.Point{}, false
	}
	return image.Point{X: int(p.X), Y: int(p.Y)}, true
}

// waitClick blocks until the user presses the left mouse button and
// returns the cursor position captured at press time. ok is false if ESC
// is pressed, ctx is cancelled, or the capture window elapses.
func waitClick(ctx context.Context) (image.Point, bool) {
	deadline := time.Now().Add(captureWindow)
	// Drain a button that is still held from arming the hotkey so we react
	// to a fresh press, not a leftover one.
	for keyDown(vkLButton) {
		if ctx.Err() != nil {
			return image.Point{}, false
		}
		time.Sleep(pollEvery)
	}
	for {
		if ctx.Err() != nil || time.Now().After(deadline) || keyDown(vkEscape) {
			return image.Point{}, false
		}
		if keyDown(vkLButton) {
			pt, ok := cursorPos()
			for keyDown(vkLButton) { // wait for release: clean state for next click
				time.Sleep(pollEvery / 2)
			}
			return pt, ok
		}
		time.Sleep(pollEvery)
	}
}

func captureFlow(ctx context.Context, opts Options, log *logx.Logger) error {
	log.Infof("screen: seleccioná 2 esquinas opuestas con clic izquierdo (ESC cancela)")
	p1, ok := waitClick(ctx)
	if !ok {
		log.Infof("screen: captura cancelada")
		return nil
	}
	p2, ok := waitClick(ctx)
	if !ok {
		log.Infof("screen: captura cancelada")
		return nil
	}
	rect := normalizeRect(p1, p2)
	if rect.Dx() == 0 || rect.Dy() == 0 {
		log.Warnf("screen: área vacía (los 2 puntos coinciden); ignoro")
		return nil
	}
	img, err := screenshot.CaptureRect(rect)
	if err != nil {
		return fmt.Errorf("capturar %v: %w", rect, err)
	}
	path, err := savePNG(img, opts.Dir, time.Now())
	if err != nil {
		return err
	}
	log.Infof("screen: capturado %dx%d → %s", rect.Dx(), rect.Dy(), path)
	return nil
}

func savePNG(img image.Image, dir string, t time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("crear %s: %w", dir, err)
	}
	path := filepath.Join(dir, screenshotName(t))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("crear %s: %w", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("codificar PNG %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("cerrar %s: %w", path, err)
	}
	return path, nil
}

// Run registers the global hotkey and pumps the thread message queue until
// ctx is cancelled. Each hotkey press arms a two-click capture (ignored if
// one is already in progress). RegisterHotKey + GetMessage must share one
// OS thread, hence LockOSThread.
func Run(ctx context.Context, opts Options, log *logx.Logger) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Physical-pixel coordinates so GetCursorPos and CaptureRect agree on
	// HiDPI displays. Best-effort.
	_, _, _ = procSetProcessDPIAware.Call()

	// hotkeyID is unique within this thread's registrations (RegisterHotKey
	// with hwnd=0 uses a per-thread id namespace), so a constant is safe.
	const hotkeyID = 1
	r, _, callErr := procRegisterHotKey.Call(0, hotkeyID,
		uintptr(opts.Hotkey.Mods|modNoRepeat), uintptr(opts.Hotkey.VK))
	if r == 0 {
		return fmt.Errorf("registrar hotkey %s (¿ya está en uso?): %v", opts.Hotkey.Text, callErr)
	}
	defer procUnregisterHotKey.Call(0, hotkeyID)

	tid, _, _ := procGetCurrentThreadId.Call()
	go func() {
		<-ctx.Done()
		// Posting WM_QUIT is what unblocks GetMessage so Run returns. If it
		// fails the loop would block forever, so surface it.
		if pr, _, perr := procPostThreadMessageW.Call(tid, wmQuit, 0, 0); pr == 0 {
			log.Errorf("screen: PostThreadMessage falló (%v); el daemon podría no cerrar limpiamente", perr)
		}
	}()

	log.Infof("screen: hotkey %s activo; capturas → %s", opts.Hotkey.Text, opts.Dir)

	var wg sync.WaitGroup // wait for in-flight captures before returning
	var capturing atomic.Bool
	var runErr error
	var m msg
	for {
		// GetMessage returns 0 on WM_QUIT and -1 (^uintptr(0)) on error.
		ret, _, getErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 {
			break // WM_QUIT (posted on ctx cancel)
		}
		if ret == ^uintptr(0) {
			runErr = fmt.Errorf("GetMessage: %v", getErr)
			break
		}
		if m.message == wmHotkey && capturing.CompareAndSwap(false, true) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer capturing.Store(false)
				if err := captureFlow(ctx, opts, log); err != nil {
					log.Errorf("screen: %v", err)
				}
			}()
		}
	}
	wg.Wait() // captureFlow polls ctx every ~10ms, so this returns promptly
	return runErr
}
