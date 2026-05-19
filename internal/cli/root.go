// Package cli wires the conduit command tree. It builds the root command
// from the feature registry plus core commands (version, features). It
// must never import a concrete feature package — features self-register.
package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/feature"
	"github.com/ShizukaJiku/conduit/internal/logx"
	"github.com/ShizukaJiku/conduit/internal/storage"
)

// Execute builds and runs the root command.
func Execute() error { return newRoot().Execute() }

// newRoot builds the full command tree (extracted for testability).
func newRoot() *cobra.Command {
	cfg := &config.Config{}
	deps := feature.Deps{
		Config:  cfg,
		Storage: storage.New,
		Log:     logx.New(os.Stderr), // until a feature opens the file sink
	}

	root := &cobra.Command{
		Use:           "conduit",
		Short:         "conduit — sincronizador de carpetas storage-agnóstico",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Resolve config (and migrate the legacy Python JSON on first run)
		// after flags are parsed, before any subcommand runs. The shared
		// *cfg is filled in place so features see the resolved values.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if migrated, from, to, err := config.MigrateLegacy(); err != nil {
				fmt.Fprintf(os.Stderr, "aviso: no se pudo migrar la config legacy: %v\n", err)
			} else if migrated {
				fmt.Fprintf(os.Stderr, "config migrada: %s → %s (revisá los permisos del archivo)\n", from, to)
			}
			resolved, err := config.Resolve(cmd.Flags())
			if err != nil {
				return err
			}
			*cfg = *resolved
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.String("config", "", "ruta del archivo de config (default ~/.conduit/config.toml)")
	pf.String("backend", "", "backend de storage (p.ej. sftp)")
	pf.String("log-file", "", "ruta del log (default ~/.conduit/conduit.log)")
	pf.Bool("verbose", false, "también enviar mensajes Info a stderr")
	pf.Bool("insecure-host-key", false, "DESHABILITA la verificación de host key SSH (INSEGURO; solo legacy)")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newFeaturesCmd())
	for _, f := range feature.All() {
		root.AddCommand(f.NewCommand(deps))
	}
	return root
}

// newFeaturesCmd lists the registered plugins (core command, not a plugin).
func newFeaturesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "features",
		Short: "Lista las funcionalidades (plugins) disponibles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, f := range feature.All() {
				fmt.Fprintf(w, "%s\t%s\n", f.Name(), f.Synopsis())
			}
			return w.Flush()
		},
	}
}
