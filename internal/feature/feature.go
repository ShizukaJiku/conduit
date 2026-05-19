// Package feature is conduit's plugin SPI. A feature is a self-contained
// capability exposed as a top-level subcommand (watch, download, ...).
//
// Features register themselves from their package init() via Register and
// are activated by a blank import in cmd/conduit. internal/cli builds the
// command tree by iterating All(); it never imports a concrete feature.
// This is the database/sql driver pattern applied to capabilities.
package feature

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/ShizukaJiku/conduit/internal/config"
	"github.com/ShizukaJiku/conduit/internal/logx"
	"github.com/ShizukaJiku/conduit/internal/storage"
)

// Deps are the shared dependencies injected into a feature when its cobra
// command is built. Storage is a Factory (not a Storage) so each feature
// constructs the backend lazily with its own context.
type Deps struct {
	Config  *config.Config
	Storage storage.Factory
	Log     *logx.Logger
}

// Feature is a conduit plugin.
type Feature interface {
	Name() string                     // subcommand name, e.g. "watch"
	Synopsis() string                 // one-line help
	NewCommand(d Deps) *cobra.Command // builds the cobra subcommand
}

var registry = map[string]Feature{}

// Register adds f to the plugin registry. It panics on an empty or
// duplicate name so collisions fail loudly at process start rather than
// silently shadowing a capability at runtime.
func Register(f Feature) {
	name := f.Name()
	if name == "" {
		panic("feature: Register called with empty Name()")
	}
	if _, dup := registry[name]; dup {
		panic("feature: duplicate registration for " + name)
	}
	registry[name] = f
}

// All returns every registered feature ordered by Name for stable output.
func All() []Feature {
	out := make([]Feature, 0, len(registry))
	for _, f := range registry {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
