// Package download is the conduit "download" plugin: it additively
// mirrors the configured storage backend into a local folder (port of
// sftp_download.py — never deletes local files). It self-registers via
// init(); internal/cli never imports it directly.
package download

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/clock"
	dlengine "github.com/ShizukaJiku/conduit/internal/download"
	"github.com/ShizukaJiku/conduit/internal/feature"
	"github.com/ShizukaJiku/conduit/internal/logx"
)

type plugin struct{}

func (plugin) Name() string { return "download" }

func (plugin) Synopsis() string {
	return "Espeja el backend hacia una carpeta local (aditivo; nunca borra local)"
}

func (p plugin) NewCommand(d feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   p.Name(),
		Short: p.Synopsis(),
		Args:  cobra.NoArgs,
		Long: "Mantiene una copia local del backend: descarga lo que falta o " +
			"cambió. Es aditivo — nunca borra archivos locales aunque " +
			"desaparezcan del backend. Sondea cada poll_seconds. Corre hasta Ctrl+C.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), d)
		},
	}
}

func run(parent context.Context, d feature.Deps) error {
	if d.Config == nil || d.Config.LocalFolder == "" {
		return errors.New("download: configura 'local_folder' (archivo de config o CONDUIT_LOCAL_FOLDER)")
	}
	if d.Storage == nil {
		return errors.New("download: backend de storage no disponible")
	}

	// Catch Ctrl+C before any blocking work (store construction included).
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := d.Log
	if lp, err := logx.DefaultPath(); err == nil {
		if lg, closer, oerr := logx.Open(lp); oerr == nil {
			log = lg
			defer closer.Close()
		} else {
			log.Warnf("download: no se pudo abrir el log %s (sigo sin log en disco): %v", lp, oerr)
		}
	} else {
		log.Warnf("download: no se pudo resolver la ruta de log (sigo sin log en disco): %v", err)
	}

	store, err := d.Storage(d.Config)
	if err != nil {
		return err
	}

	log.Infof("download: espejando backend %s → %s (poll %ds)",
		d.Config.Backend.Type, d.Config.LocalFolder, d.Config.PollSeconds)
	eng := dlengine.New(store, d.Config.LocalFolder, d.Config.PollSeconds, log, clock.System{})
	return eng.Run(ctx)
}

func init() { feature.Register(plugin{}) }
