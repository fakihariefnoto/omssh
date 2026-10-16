// Package config loads and validates omssh's YAML configuration.
//
// Paths follow the XDG base directory specification on every platform,
// deliberately: the documentation promises $XDG_CONFIG_HOME/omssh, and someone
// who runs omssh on Linux and macOS should find their config in one place.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns the directory holding config.yaml and themes/.
func Dir() (string, error) { return xdgDir("XDG_CONFIG_HOME", ".config") }

// StateDir returns the directory holding history and deploy logs.
func StateDir() (string, error) { return xdgDir("XDG_STATE_HOME", ".local/state") }

// CacheDir returns the directory holding previews and cached listings.
func CacheDir() (string, error) { return xdgDir("XDG_CACHE_HOME", ".cache") }

func xdgDir(env, fallback string) (string, error) {
	if dir := os.Getenv(env); dir != "" {
		return filepath.Join(dir, "omssh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, filepath.FromSlash(fallback), "omssh"), nil
}

// Path returns the configuration file path, whether or not it exists.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// ThemeDir returns the directory searched for user themes.
func ThemeDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "themes"), nil
}

// Load reads the configuration from path. A missing file is not an error: the
// defaults are returned instead, so a first run works with no configuration.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return cfg, nil
	case err != nil:
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := unmarshalStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the configuration atomically, creating the directory if needed.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	out, err := marshal(cfg)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
