// Package watch is the conduit "watch" plugin: it destructively mirrors a
// local folder onto the configured storage backend (port of
// sftp_sync.py). It self-registers via init(); internal/cli never imports
// it directly.
package watch

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/clock"
	"github.com/ShizukaJiku/conduit/internal/feature"
	"github.com/ShizukaJiku/conduit/internal/logx"
	"github.com/ShizukaJiku/conduit/internal/screencap"
	syncengine "github.com/ShizukaJiku/conduit/internal/sync"
)

type plugin struct{}

func (plugin) Name() string { return "watch" }

func (plugin) Synopsis() string {
	return "Espeja una carpeta local hacia el backend (sube/borra; destructivo)"
}

func (p plugin) NewCommand(d feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   p.Name(),
		Short: p.Synopsis(),
		Args:  cobra.NoArgs,
		Long: "Mantiene el backend como un espejo exacto de la carpeta local: " +
			"sube los archivos nuevos o modificados y borra del backend lo que " +
			"ya no existe localmente. Corre hasta Ctrl+C.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			screen, _ := cmd.Flags().GetBool("screen")
			return run(cmd.Context(), d, screen)
		},
	}
	cmd.Flags().Bool("screen", false,
		"captura de pantalla por hotkey (Ctrl+Shift+S) hacia <local>/<screen.dir> (Windows)")
	return cmd
}

func run(parent context.Context, d feature.Deps, screen bool) error {
	if d.Config == nil || d.Config.LocalFolder == "" {
		return errors.New("watch: configura 'local_folder' (archivo de config o CONDUIT_LOCAL_FOLDER)")
	}
	if d.Storage == nil {
		return errors.New("watch: backend de storage no disponible")
	}

	// Catch Ctrl+C before any blocking work (store construction included).
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log, closeLog := d.OpenLog()
	defer closeLog()
	d.WarnSecurity(log)

	store, err := d.Storage(d.Config)
	if err != nil {
		return err
	}

	if screen {
		startScreen(ctx, d, log)
	}

	log.Infof("watch: espejando %s → backend %s", d.Config.LocalFolder, d.Config.Backend.Type)
	eng := syncengine.New(store, d.Config.LocalFolder, log, clock.System{})
	return eng.Run(ctx)
}

// startScreen arms the screenshot daemon alongside the mirror. Failures
// (bad hotkey, unsupported OS, hotkey already taken) are non-fatal: they
// are logged and the watch keeps running. Screenshots land in a subfolder
// of the watched local folder so the mirror uploads them automatically.
func startScreen(ctx context.Context, d feature.Deps, log *logx.Logger) {
	hk, err := screencap.ParseHotkey(d.Config.Screen.Hotkey)
	if err != nil {
		log.Errorf("screen: hotkey inválido (capturas desactivadas): %v", err)
		return
	}
	dir := filepath.Join(d.Config.LocalFolder, d.Config.Screen.Dir)
	// Contain screen.dir inside local_folder: a value like "../x" would
	// write (and then sync) outside the watched tree. Defensive against
	// misconfiguration, not a remote threat.
	if rel, relErr := filepath.Rel(d.Config.LocalFolder, dir); relErr != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		log.Errorf("screen: screen.dir %q escaparía de local_folder (capturas desactivadas)", d.Config.Screen.Dir)
		return
	}
	// Created up front so the watcher covers it from startup (live upload).
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		log.Errorf("screen: no se pudo crear %s (capturas desactivadas): %v", dir, mkErr)
		return
	}
	go func() {
		if rErr := screencap.Run(ctx, screencap.Options{Dir: dir, Hotkey: hk}, log); rErr != nil {
			log.Errorf("screen: %v", rErr)
		}
	}()
}

func init() { feature.Register(plugin{}) }
