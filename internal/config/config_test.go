package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The defaults must be valid, because they are what a first run uses and what
// every file is merged onto.
func TestDefaultsAreValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the defaults are invalid: %v", err)
	}
	// The embedded terminal exists to be recorded; history that is never written
	// is history nobody can read.
	if !cfg.Sessions.Record {
		t.Error("recording should be on by default")
	}
}

// A missing file is not an error: first run works with no configuration at all.
func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UI.Theme != "amber" || cfg.Transfers.Workers != 4 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.UI.Start != "home" {
		t.Errorf("a first run should open on the launcher, got %q", cfg.UI.Start)
	}
}

// A typo in a key name must fail loudly with the file and line, because the
// alternative is a setting that silently does nothing.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	path := writeConfig(t, "version: 1\nui:\n  theme: midnight\n  colour: red\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("an unknown key should be an error")
	}
	if !strings.Contains(err.Error(), "colour") {
		t.Errorf("the error should name the offending key, got %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error should name the file, got %v", err)
	}
}

func TestLoadRejectsDuplicateKeys(t *testing.T) {
	path := writeConfig(t, "version: 1\nui:\n  theme: dawn\n  theme: midnight\n")
	if _, err := Load(path); err == nil {
		t.Fatal("a duplicate key should be an error")
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]func(*Config){
		"unknown theme":      func(c *Config) { c.UI.Theme = "sunset" },
		"unknown icons":      func(c *Config) { c.UI.Icons = "emoji" },
		"unknown motion":     func(c *Config) { c.UI.Motion = "cinematic" },
		"esc as quit":        func(c *Config) { c.Keys.Quit = "esc" },
		"no workers":         func(c *Config) { c.Transfers.Workers = 0 },
		"negative workers":   func(c *Config) { c.Transfers.Workers = -1 },
		"wrong version":      func(c *Config) { c.Version = 2 },
		"negative threshold": func(c *Config) { c.UI.ConfirmThreshold.Files = -1 },
	}
	for name, mutate := range cases {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

// The refusal to let Esc quit is explained, not just enforced.
func TestEscAsQuitSaysWhy(t *testing.T) {
	cfg := Default()
	cfg.Keys.Quit = "esc"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "dismiss") {
		t.Errorf("the error should explain what Esc is for, got %v", err)
	}
}

func TestDeployProfileValidation(t *testing.T) {
	cfg := Default()
	cfg.Deploy.Profiles = map[string]Profile{
		"prod": {LocalRoot: "./dist", RemoteRoot: "/srv/api"}, // no host
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a profile without a host should be rejected")
	}

	cfg.Deploy.Profiles["prod"] = Profile{Host: "prod", LocalRoot: "./dist", RemoteRoot: "/srv/api", Mode: "sync"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a complete profile should validate: %v", err)
	}

	cfg.Deploy.Profiles["prod"] = Profile{Host: "prod", LocalRoot: "./dist", RemoteRoot: "/", Mode: "mirror"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("an unknown deploy mode should be rejected")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg := Default()
	cfg.UI.Theme = "amber"
	cfg.UI.Icons = "ascii"
	cfg.Keys.Overrides = map[string]string{"file.delete": "ctrl+d"}
	cfg.Deploy.Profiles = map[string]Profile{
		"prod-api": {
			Host: "prod-api", LocalRoot: "./dist", RemoteRoot: "/srv/api/current",
			Mode: "sync", Atomic: true, PreservePerms: true, Backup: true,
			Exclude: []string{".git/", "*.log"},
			Hooks:   Hooks{LocalPre: []string{"npm run build"}, RemotePost: []string{"systemctl restart api"}},
		},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.UI.Theme != "amber" || loaded.UI.Icons != "ascii" {
		t.Errorf("ui not round-tripped: %+v", loaded.UI)
	}
	if loaded.Keys.Overrides["file.delete"] != "ctrl+d" {
		t.Errorf("overrides not round-tripped: %+v", loaded.Keys.Overrides)
	}
	profile, ok := loaded.Deploy.Profiles["prod-api"]
	if !ok {
		t.Fatal("the deploy profile did not survive the round trip")
	}
	if !profile.Atomic || !profile.Backup || len(profile.Exclude) != 2 {
		t.Errorf("profile not round-tripped: %+v", profile)
	}
	if len(profile.Hooks.RemotePost) != 1 {
		t.Errorf("hooks not round-tripped: %+v", profile.Hooks)
	}
}

// The example written by `config init` must itself be valid, or a first run
// would break the moment someone ran the command.
func TestExampleConfigIsValid(t *testing.T) {
	path := writeConfig(t, exampleConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the example config does not load: %v", err)
	}
	if cfg.UI.Theme != "amber" {
		t.Errorf("the example should ship the default theme, got %q", cfg.UI.Theme)
	}
	if cfg.Keys.Quit != "ctrl+q" {
		t.Errorf("the example should document Ctrl+Q as quit, got %q", cfg.Keys.Quit)
	}
}

func TestWriteExampleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteExampleFile(path); err != nil {
		t.Fatalf("WriteExampleFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the config should not be world readable, got %v", info.Mode().Perm())
	}
}

// Paths follow XDG on every platform, which the documentation promises.
func TestXDGPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join("/tmp/xdg-config", "omssh") {
		t.Errorf("Dir = %q", dir)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("Path = %q, want a .yaml file", path)
	}
	state, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(state, "xdg-state/omssh") && !strings.HasSuffix(state, filepath.Join("xdg-state", "omssh")) {
		t.Errorf("StateDir = %q", state)
	}
}

// With no XDG variables set, the fallbacks are the documented ~/.config paths.
func TestXDGDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "omssh")
	if dir != want {
		t.Errorf("Dir = %q, want %q", dir, want)
	}
}
