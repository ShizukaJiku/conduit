// Package config resolves conduit runtime configuration with precedence
// flags > env > file > default. Flags are bound in the config/UX step;
// this step implements env > file > default. The legacy Python JSON
// (~/.sftpwatcher/config.json) import is deferred to the config/UX step.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// SFTPConfig holds the SFTP backend connection settings.
type SFTPConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	User            string `mapstructure:"user"`
	Password        string `mapstructure:"password"`
	KnownHosts      string `mapstructure:"known_hosts"`       // path; empty → ~/.ssh/known_hosts
	InsecureHostKey bool   `mapstructure:"insecure_host_key"` // D1: legacy AutoAddPolicy
}

// Backend selects and configures the storage driver.
type Backend struct {
	Type string     `mapstructure:"type"` // "sftp" (default) | "memfs" (tests)
	SFTP SFTPConfig `mapstructure:"sftp"`
}

// Config is the resolved runtime configuration.
type Config struct {
	LocalFolder  string  `mapstructure:"local_folder"`
	RemoteFolder string  `mapstructure:"remote_folder"`
	PollSeconds  int     `mapstructure:"poll_seconds"`
	Backend      Backend `mapstructure:"backend"`
}

// envKeys are bound explicitly so AutomaticEnv reaches nested struct keys.
var envKeys = []string{
	"local_folder", "remote_folder", "poll_seconds",
	"backend.type",
	"backend.sftp.host", "backend.sftp.port", "backend.sftp.user",
	"backend.sftp.password", "backend.sftp.known_hosts",
	"backend.sftp.insecure_host_key",
}

// DefaultPath is ~/.conduit/config.toml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home dir: %w", err)
	}
	return filepath.Join(home, ".conduit", "config.toml"), nil
}

// LoadDefault loads from DefaultPath (missing file is not an error).
func LoadDefault() (*Config, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return Load(p)
}

// Load resolves configuration. path may be "" to skip the file entirely.
// Precedence: env (CONDUIT_*) > file (TOML) > built-in defaults. A missing
// file is not an error (first run uses defaults).
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("toml")

	v.SetDefault("remote_folder", "/")
	v.SetDefault("poll_seconds", 15)
	v.SetDefault("backend.type", "sftp")
	v.SetDefault("backend.sftp.port", 22)

	v.SetEnvPrefix("CONDUIT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	for _, k := range envKeys {
		_ = v.BindEnv(k)
	}

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			var nf viper.ConfigFileNotFoundError
			if !errors.As(err, &nf) && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("config: read %s: %w", path, err)
			}
		}
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &c, nil
}
