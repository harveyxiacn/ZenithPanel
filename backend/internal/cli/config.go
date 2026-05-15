// Package cli implements the headless `zenithctl` command tree. It is a thin
// HTTP client over the panel's existing API plus the new unix socket; it
// never imports backend service packages.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config mirrors ~/.config/zenithctl/config.toml. The format is:
//
//	default = "local"
//
//	[profile.local]
//	host  = "unix:///run/zenithpanel.sock"
//
//	[profile.prod]
//	host       = "https://panel.example.com"
//	token      = "ztk_..."
//	verify_tls = true
type Config struct {
	Default string             `toml:"default"`
	Profile map[string]Profile `toml:"profile"`
}

// Profile is a single named target. `Host` may be:
//   - `unix:///path/to/sock` — connect via unix domain socket
//   - `http://host[:port]`   — plain HTTP (LAN/loopback only)
//   - `https://host[:port]`  — HTTPS (recommended for remote)
type Profile struct {
	Host      string `toml:"host"`
	Token     string `toml:"token"`
	VerifyTLS bool   `toml:"verify_tls"`
}

// ConfigPath returns the canonical config file path. Honors $XDG_CONFIG_HOME
// when set, otherwise falls back to ~/.config/zenithctl/config.toml.
func ConfigPath() string {
	if c := os.Getenv("XDG_CONFIG_HOME"); c != "" {
		return filepath.Join(c, "zenithctl", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "zenithctl", "config.toml")
}

// LoadConfig reads the config file. Missing file is not an error — the CLI
// will fall back to the implicit local profile (unix socket) when possible.
func LoadConfig() (*Config, error) {
	path := ConfigPath()
	if path == "" {
		return &Config{Profile: map[string]Profile{}}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Profile: map[string]Profile{}}, nil
		}
		return nil, err
	}
	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Profile == nil {
		cfg.Profile = map[string]Profile{}
	}
	return cfg, nil
}

// SaveConfig writes the config back to disk with 0600 permissions. Creates
// parent dirs as needed.
func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	if path == "" {
		return errors.New("could not determine config path (no home dir)")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// resolveProfile applies CLI overrides on top of the config and returns the
// profile that should be used for this invocation.
func resolveProfile(cfg *Config, hostOverride, tokenOverride, profileOverride string) (Profile, error) {
	// Build the base profile from --profile or `default` in config.
	name := profileOverride
	if name == "" {
		name = cfg.Default
	}
	base := cfg.Profile[name]

	// Env overrides
	if v := os.Getenv("ZENITHCTL_HOST"); v != "" {
		base.Host = v
	}
	if v := os.Getenv("ZENITHCTL_TOKEN"); v != "" {
		base.Token = v
	}
	// Flag overrides
	if hostOverride != "" {
		base.Host = hostOverride
	}
	if tokenOverride != "" {
		base.Token = tokenOverride
	}

	if base.Host == "" {
		// Last resort: implicit local socket on Linux.
		if runtime.GOOS == "linux" {
			sock := "/run/zenithpanel.sock"
			if _, err := os.Stat(sock); err == nil {
				return Profile{Host: "unix://" + sock}, nil
			}
			if alt := os.Getenv("XDG_RUNTIME_DIR"); alt != "" {
				p := filepath.Join(alt, "zenithpanel.sock")
				if _, err := os.Stat(p); err == nil {
					return Profile{Host: "unix://" + p}, nil
				}
			}
		}
		return Profile{}, errors.New("no host configured; pass --host or run `zenithctl token bootstrap` on the panel host")
	}
	return base, nil
}

// isUnixHost reports whether the profile targets a unix-domain socket.
func isUnixHost(host string) bool { return strings.HasPrefix(host, "unix://") }
