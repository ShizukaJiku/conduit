// Package cli wires the conduit command tree. It builds the root command
// from the feature registry and a core "version" command. It must never
// import a concrete feature package — features self-register.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/feature"
	"github.com/ShizukaJiku/conduit/internal/logx"
)

// Execute builds and runs the root command.
func Execute() error {
	root := &cobra.Command{
		Use:           "conduit",
		Short:         "conduit — sincronizador de carpetas storage-agnóstico",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newVersionCmd())

	deps := buildDeps()
	for _, f := range feature.All() {
		root.AddCommand(f.NewCommand(deps))
	}

	return root.Execute()
}

// buildDeps assembles the shared dependencies handed to every feature.
// Config/Storage/logging are fleshed out in later steps; for now this
// produces safe, minimal values so the SPI is stable.
func buildDeps() feature.Deps {
	cfg, _ := config.Load()
	return feature.Deps{
		Config:  cfg,
		Storage: nil, // wired in the storage-core step
		Log:     logx.New(nil),
	}
}
