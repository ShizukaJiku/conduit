// Package config resolves conduit runtime configuration with precedence
// flags > env > file > default (via viper). It also migrates the legacy
// Python JSON (~/.sftpwatcher/config.json) on first run.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
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
	LogFile      string  `mapstructure:"log_file"`
	Verbose      bool    `mapstructure:"verbose"`
	Backend      Backend `mapstructure:"backend"`
}

// envKeys are bound explicitly so AutomaticEnv reaches nested struct keys.
var envKeys = []string{
	"local_folder", "remote_folder", "poll_seconds", "log_file", "verbose",
	"backend.type",
	"backend.sftp.host", "backend.sftp.port", "backend.sftp.user",
	"backend.sftp.password", "backend.sftp.known_hosts",
	"backend.sftp.insecure_host_key",
}

// flagToKey maps a persistent CLI flag name to its viper config key.
var flagToKey = map[string]string{
	"backend":           "backend.type",
	"log-file":          "log_file",
	"verbose":           "verbose",
	"insecure-host-key": "backend.sftp.insecure_host_key",
}

func newViper() *viper.Viper {
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
	// D2: a short, documented alias for the SFTP password so secrets can
	// be passed via the environment instead of a plaintext config file.
	_ = v.BindEnv("backend.sftp.password", "CONDUIT_BACKEND_SFTP_PASSWORD", "CONDUIT_PASSWORD")
	return v
}

func readFile(v *viper.Viper, path string) error {
	if path == "" {
		return nil
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		var nf viper.ConfigFileNotFoundError
		if !errors.As(err, &nf) && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("config: read %s: %w", path, err)
		}
	}
	return nil
}

// DefaultPath is ~/.conduit/config.toml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home dir: %w", err)
	}
	return filepath.Join(home, ".conduit", "config.toml"), nil
}

// LegacyPath is the Python tool's config: ~/.sftpwatcher/config.json.
func LegacyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home dir: %w", err)
	}
	return filepath.Join(home, ".sftpwatcher", "config.json"), nil
}

// LoadDefault loads from DefaultPath (missing file is not an error).
func LoadDefault() (*Config, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return Load(p)
}

// Load resolves configuration from env > file > defaults (no flags).
// path may be "" to skip the file. A missing file is not an error.
func Load(path string) (*Config, error) {
	v := newViper()
	if err := readFile(v, path); err != nil {
		return nil, err
	}
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &c, nil
}

// Resolve loads configuration with full precedence flags > env > file >
// default. The config file is flags["config"] or DefaultPath. Only
// *changed* flags override env/file (viper.BindPFlag semantics).
func Resolve(flags *pflag.FlagSet) (*Config, error) {
	v := newViper()
	if flags != nil {
		for name, key := range flagToKey {
			if f := flags.Lookup(name); f != nil {
				_ = v.BindPFlag(key, f)
			}
		}
	}
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	if flags != nil {
		if f := flags.Lookup("config"); f != nil && f.Changed {
			path = f.Value.String()
		}
	}
	if err := readFile(v, path); err != nil {
		return nil, err
	}
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &c, nil
}

// PasswordFromEnv reports whether the SFTP password is supplied via the
// environment (so a plaintext password in the config file can be warned
// about — D2).
func PasswordFromEnv() bool {
	return os.Getenv("CONDUIT_PASSWORD") != "" ||
		os.Getenv("CONDUIT_BACKEND_SFTP_PASSWORD") != ""
}

type legacyJSON struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	RemoteFolder string `json:"remote_folder"`
	LocalFolder  string `json:"local_folder"`
}

// tomlBasicString encodes s as a TOML basic string using ONLY the escape
// sequences TOML defines (strconv.Quote emits Go escapes like \a and \xHH
// that TOML parsers reject, which would lock the user out after a silent
// migration). Printable Unicode is kept literal; other control chars use
// \uXXXX. Invalid UTF-8 degrades to U+FFFD (valid TOML, lossy but rare).
func tomlBasicString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// MigrateLegacy converts the Python ~/.sftpwatcher/config.json into
// ~/.conduit/config.toml on first run. It is a no-op if the conduit
// config already exists or there is no legacy file. The written file is
// 0600 because it carries a password (the legacy JSON was world-readable).
func MigrateLegacy() (migrated bool, from string, to string, err error) {
	to, err = DefaultPath()
	if err != nil {
		return false, "", "", err
	}
	if _, statErr := os.Stat(to); statErr == nil {
		return false, "", to, nil // conduit config already present
	}
	from, err = LegacyPath()
	if err != nil {
		return false, "", to, err
	}
	data, rerr := os.ReadFile(from)
	if rerr != nil {
		return false, from, to, nil // no legacy config → nothing to do
	}
	var lj legacyJSON
	if jerr := json.Unmarshal(data, &lj); jerr != nil {
		return false, from, to, fmt.Errorf("config: parse legacy %s: %w", from, jerr)
	}
	port := lj.Port
	if port == 0 {
		port = 22
	}
	remote := lj.RemoteFolder
	if remote == "" {
		remote = "/"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Migrado automáticamente desde %s\n", from)
	fmt.Fprintf(&b, "local_folder = %s\n", tomlBasicString(lj.LocalFolder))
	fmt.Fprintf(&b, "remote_folder = %s\n", tomlBasicString(remote))
	fmt.Fprintf(&b, "poll_seconds = 15\n\n")
	fmt.Fprintf(&b, "[backend]\ntype = \"sftp\"\n\n")
	fmt.Fprintf(&b, "[backend.sftp]\n")
	fmt.Fprintf(&b, "host = %s\n", tomlBasicString(lj.Host))
	fmt.Fprintf(&b, "port = %d\n", port)
	fmt.Fprintf(&b, "user = %s\n", tomlBasicString(lj.Username))
	fmt.Fprintf(&b, "password = %s\n", tomlBasicString(lj.Password))

	if mkErr := os.MkdirAll(filepath.Dir(to), 0o700); mkErr != nil {
		return false, from, to, fmt.Errorf("config: mkdir for migration: %w", mkErr)
	}
	// O_EXCL: atomic create. Closes the stat→write TOCTOU and refuses to
	// follow a symlink planted at `to` (would write the password elsewhere).
	f, oerr := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if oerr != nil {
		if errors.Is(oerr, fs.ErrExist) {
			return false, from, to, nil // raced or already migrated
		}
		return false, from, to, fmt.Errorf("config: create migrated %s: %w", to, oerr)
	}
	if _, wErr := f.WriteString(b.String()); wErr != nil {
		_ = f.Close()
		return false, from, to, fmt.Errorf("config: write migrated %s: %w", to, wErr)
	}
	if cErr := f.Close(); cErr != nil {
		return false, from, to, fmt.Errorf("config: close migrated %s: %w", to, cErr)
	}
	return true, from, to, nil
}
