// Package watch is the conduit "watch" plugin: it destructively mirrors a
// local folder onto the configured storage backend (port of
// sftp_sync.py). It self-registers via init(); internal/cli never imports
// it directly.
package watch

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/clock"
	"github.com/ShizukaJiku/conduit/internal/feature"
	"github.com/ShizukaJiku/conduit/internal/logx"
	syncengine "github.com/ShizukaJiku/conduit/internal/sync"
)

type plugin struct{}

func (plugin) Name() string { return "watch" }

func (plugin) Synopsis() string {
	return "Espeja una carpeta local hacia el backend (sube/borra; destructivo)"
}

func (p plugin) NewCommand(d feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   p.Name(),
		Short: p.Synopsis(),
		Args:  cobra.NoArgs,
		Long: "Mantiene el backend como un espejo exacto de la carpeta local: " +
			"sube los archivos nuevos o modificados y borra del backend lo que " +
			"ya no existe localmente. Corre hasta Ctrl+C.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), d)
		},
	}
}

func run(parent context.Context, d feature.Deps) error {
	if d.Config == nil || d.Config.LocalFolder == "" {
		return errors.New("watch: configura 'local_folder' (archivo de config o CONDUIT_LOCAL_FOLDER)")
	}
	if d.Storage == nil {
		return errors.New("watch: backend de storage no disponible")
	}

	// Open the real log sink only now that a long-running command runs, so
	// read-only commands never create ~/.conduit.
	log := d.Log
	if lp, err := logx.DefaultPath(); err == nil {
		if lg, closer, oerr := logx.Open(lp); oerr == nil {
			log = lg
			defer closer.Close()
		}
	}

	store, err := d.Storage(d.Config)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Infof("watch: espejando %s → backend %s", d.Config.LocalFolder, d.Config.Backend.Type)
	eng := syncengine.New(store, d.Config.LocalFolder, log, clock.System{})
	return eng.Run(ctx)
}

func init() { feature.Register(plugin{}) }
