// Package cli wires the conduit command tree. It builds the root command
// from the feature registry and a core "version" command. It must never
// import a concrete feature package — features self-register.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/feature"
	"github.com/ShizukaJiku/conduit/internal/logx"
	"github.com/ShizukaJiku/conduit/internal/storage"
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
// The real log sink is opened by a feature when it actually runs (Step
// 4/5) so read-only commands (version/help) don't create ~/.conduit.
func buildDeps() feature.Deps {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg, _ = config.Load("") // fall back to built-in defaults
	}
	return feature.Deps{
		Config:  cfg,
		Storage: storage.New, // registry factory; selects backend by cfg
		Log:     logx.New(nil),
	}
}
