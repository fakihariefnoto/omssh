package config

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// Config is the whole of omssh's own configuration. Connections deliberately
// live in ~/.ssh/config instead (ADR 0004); only deploy profiles reference them.
type Config struct {
	Version   int       `yaml:"version"`
	UI        UI        `yaml:"ui"`
	Keys      Keys      `yaml:"keys"`
	Transfers Transfers `yaml:"transfers"`
	Deploy    Deploy    `yaml:"deploy"`
}

// UI covers everything the interface does before you touch a file.
type UI struct {
	// Theme is one of midnight, dawn, mist, ember, mono, contrast or auto.
	Theme string `yaml:"theme"`
	// Icons is "nerd" or "ascii".
	Icons string `yaml:"icons"`
	// Motion is "full" or "reduced".
	Motion string `yaml:"motion"`
	// Sidebar and Preview are the initial visibility of those two views.
	Sidebar bool `yaml:"sidebar"`
	Preview bool `yaml:"preview"`
	// ShowHidden starts with dotfiles visible.
	ShowHidden bool `yaml:"show_hidden"`
	// ConfirmThreshold decides when a bulk operation asks before starting.
	ConfirmThreshold ConfirmThreshold `yaml:"confirm_threshold"`
	// Start is "home" (the launcher) or "files" (straight to the file manager).
	Start string `yaml:"start"`
	// Onboarding records that the first-run card has been dismissed.
	Onboarding bool `yaml:"onboarding_done"`
}

// ConfirmThreshold is the point at which a copy or move stops being silent.
type ConfirmThreshold struct {
	Files int   `yaml:"files"`
	Bytes int64 `yaml:"bytes"`
}

// Keys configures the keymap.
type Keys struct {
	// Preset is "standard" (default) or "vim".
	Preset string `yaml:"preset"`
	// Copy overrides what Ctrl+C does, e.g. "ctrl+insert".
	Copy string `yaml:"copy"`
	// Quit overrides what quits the app. "esc" is rejected.
	Quit string `yaml:"quit"`
	// Overrides rebinds individual actions by id, e.g. {file.delete: "ctrl+d"}.
	Overrides map[string]string `yaml:"overrides"`
}

// Transfers configures the copy engine.
type Transfers struct {
	// Workers is the number of files copied in parallel.
	Workers int `yaml:"workers"`
}

// Deploy holds the deploy profiles, keyed by name.
type Deploy struct {
	Profiles map[string]Profile `yaml:"profiles"`
}

// Profile is one deploy target: a local tree, a remote tree, and what to run.
type Profile struct {
	Host           string   `yaml:"host"`
	LocalRoot      string   `yaml:"local_root"`
	RemoteRoot     string   `yaml:"remote_root"`
	Mode           string   `yaml:"mode"` // sync | update
	Exclude        []string `yaml:"exclude"`
	IncludeHidden  bool     `yaml:"include_hidden"`
	FollowSymlinks bool     `yaml:"follow_symlinks"`
	Atomic         bool     `yaml:"atomic"`
	PreservePerms  bool     `yaml:"preserve_perms"`
	Backup         bool     `yaml:"backup"`
	Workers        int      `yaml:"workers"`
	Hooks          Hooks    `yaml:"hooks"`
}

// Hooks are commands run around a deploy, on either side.
type Hooks struct {
	LocalPre   []string `yaml:"local_pre"`
	LocalPost  []string `yaml:"local_post"`
	RemotePre  []string `yaml:"remote_pre"`
	RemotePost []string `yaml:"remote_post"`
	OnFailure  []string `yaml:"on_failure"`
}

// Known values, kept in one place so validation and help text cannot drift.
var (
	Themes       = []string{"auto", "amber", "midnight", "dawn", "mist", "mono", "contrast"}
	IconSets     = []string{"nerd", "ascii"}
	Motions      = []string{"full", "reduced"}
	KeyPresets   = []string{"standard", "vim"}
	StartScreens = []string{"home", "files"}
	DeployMode   = []string{"sync", "update"}
)

// Default returns the configuration omssh starts from on a machine with no
// config file, and the base every file is merged onto.
func Default() Config {
	return Config{
		Version: 1,
		UI: UI{
			Theme:      "amber",
			Icons:      "nerd",
			Motion:     "full",
			Sidebar:    true,
			Preview:    true,
			ShowHidden: false,
			Start:      "home",
			ConfirmThreshold: ConfirmThreshold{
				Files: 20,
				Bytes: 10 << 20, // 10 MiB
			},
		},
		Keys: Keys{
			Preset:    "standard",
			Copy:      "ctrl+c",
			Quit:      "ctrl+q",
			Overrides: map[string]string{},
		},
		Transfers: Transfers{Workers: 4},
		Deploy:    Deploy{Profiles: map[string]Profile{}},
	}
}

// Validate reports the first thing about the configuration that cannot work.
// Messages name the key and the acceptable values, because they surface
// verbatim in `omssh config check`.
func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("version: got %d, want 1", c.Version)
	}
	if err := oneOf("ui.theme", c.UI.Theme, Themes); err != nil {
		return err
	}
	if err := oneOf("ui.icons", c.UI.Icons, IconSets); err != nil {
		return err
	}
	if err := oneOf("ui.motion", c.UI.Motion, Motions); err != nil {
		return err
	}
	if c.UI.ConfirmThreshold.Files < 0 {
		return fmt.Errorf("ui.confirm_threshold.files: must not be negative")
	}
	if c.UI.ConfirmThreshold.Bytes < 0 {
		return fmt.Errorf("ui.confirm_threshold.bytes: must not be negative")
	}
	if err := oneOf("ui.start", c.UI.Start, StartScreens); err != nil {
		return err
	}
	if err := oneOf("keys.preset", c.Keys.Preset, KeyPresets); err != nil {
		return err
	}
	if c.Keys.Quit == "esc" || c.Keys.Quit == "escape" {
		return fmt.Errorf("keys.quit: Esc cannot quit; it dismisses, cancels and goes back")
	}
	if c.Keys.Copy == "" {
		return fmt.Errorf("keys.copy: must not be empty")
	}
	if c.Transfers.Workers < 1 {
		return fmt.Errorf("transfers.workers: got %d, want at least 1", c.Transfers.Workers)
	}
	for name, p := range c.Deploy.Profiles {
		if err := p.validate(name); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) validate(name string) error {
	where := "deploy.profiles." + name
	if p.Host == "" {
		return fmt.Errorf("%s.host: must name a connection", where)
	}
	if p.LocalRoot == "" {
		return fmt.Errorf("%s.local_root: must name a directory", where)
	}
	if p.RemoteRoot == "" {
		return fmt.Errorf("%s.remote_root: must name a directory", where)
	}
	if p.Mode == "" {
		p.Mode = "sync"
	}
	if err := oneOf(where+".mode", p.Mode, DeployMode); err != nil {
		return err
	}
	if p.Workers < 0 {
		return fmt.Errorf("%s.workers: must not be negative", where)
	}
	return nil
}

func oneOf(key, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s: got %q, want one of %s", key, value, strings.Join(allowed, ", "))
}

func unmarshalStrict(data []byte, v any) error {
	return yaml.UnmarshalWithOptions(data, v, yaml.Strict())
}

func marshal(v any) ([]byte, error) {
	out, err := yaml.MarshalWithOptions(v, yaml.Indent(2))
	if err != nil {
		return nil, fmt.Errorf("encoding configuration: %w", err)
	}
	return out, nil
}
