package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build metadata, injected at release time via:
//
//	-ldflags "-X github.com/ShizukaJiku/conduit/internal/cli.version=...
//	          -X .../internal/cli.commit=... -X .../internal/cli.date=..."
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Imprime versión, commit y fecha de build",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"conduit %s (commit %s, built %s)\n", version, commit, date)
			return err
		},
	}
}
