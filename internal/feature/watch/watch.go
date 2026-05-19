// Package watch is the conduit "watch" plugin: it mirrors a local folder
// to the configured storage backend (destructive, like the legacy
// sftp_sync.py). This step ships a stub; the engine is implemented in the
// port-watch step.
package watch

import (
	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/feature"
)

type plugin struct{}

func (plugin) Name() string { return "watch" }

func (plugin) Synopsis() string {
	return "Espeja una carpeta local hacia el backend (no implementado aún)"
}

func (p plugin) NewCommand(_ feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   p.Name(),
		Short: p.Synopsis(),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write([]byte("conduit watch: no implementado aún\n"))
			return err
		},
	}
}

func init() { feature.Register(plugin{}) }
