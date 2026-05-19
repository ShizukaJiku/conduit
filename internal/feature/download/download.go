// Package download is the conduit "download" plugin: it mirrors the
// configured storage backend into a local folder (additive / never
// deletes local, like the legacy sftp_download.py). This step ships a
// stub; the engine is implemented in the port-download step.
package download

import (
	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/feature"
)

type plugin struct{}

func (plugin) Name() string { return "download" }

func (plugin) Synopsis() string {
	return "Espeja el backend hacia una carpeta local (no implementado aún)"
}

func (p plugin) NewCommand(_ feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   p.Name(),
		Short: p.Synopsis(),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write([]byte("conduit download: no implementado aún\n"))
			return err
		},
	}
}

func init() { feature.Register(plugin{}) }
