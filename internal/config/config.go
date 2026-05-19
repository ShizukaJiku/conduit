// Package config holds conduit runtime configuration. The full schema
// (backend selection, credentials, folders, per-feature sections) is
// implemented in the storage-core and config/UX steps. This minimal type
// keeps the feature SPI compilable and stable across steps.
package config

// Config is the resolved runtime configuration. Fields are added in later
// steps; intentionally minimal here.
type Config struct{}

// Load resolves configuration from defaults/env/file. The real precedence
// (flag > env > file) is implemented in the config/UX step.
func Load() (*Config, error) {
	return &Config{}, nil
}
