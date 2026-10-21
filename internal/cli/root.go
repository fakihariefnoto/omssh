// Package cli wires the command line to the interface and to configuration.
//
// Every subcommand here is also a library call, so nothing about loading
// configuration or resolving keys is only reachable through the terminal UI.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/actions"
	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/keymap"
	"github.com/fakihariefnoto/omssh/internal/version"
)

// Options are the process-wide flags.
type Options struct {
	ConfigPath string
	Theme      string
	Icons      string
	NoColor    bool
	ASCII      bool
	// WidthMargin is -1 unless the flag was given, so the config's value wins by
	// default and a flag always beats a file.
	WidthMargin int
}

// Execute runs the command line and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	opts := &Options{}
	root := newRoot(opts, stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		// A remote command's status is the interesting one, so exec passes it
		// through instead of flattening everything to 1.
		var status interface{ ExitCode() int }
		if errors.As(err, &status) {
			return status.ExitCode()
		}
		fmt.Fprintln(stderr, "omssh: "+err.Error())
		return 1
	}
	return 0
}

func newRoot(opts *Options, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "omssh",
		Short: "Oh My SSH — a modern SSH/SFTP client for the terminal",
		// --version, because that is the first thing a package manager runs.
		Version: version.String(),
		Long: "omssh manages many SSH and SFTP sessions in one terminal, moves files\n" +
			"between any two machines without thinking about which side is which, and\n" +
			"deploys a local tree to a remote host repeatably.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := ""
			if len(args) > 0 {
				host = args[0]
			}
			return runTUI(opts, host)
		},
	}

	root.PersistentFlags().StringVar(&opts.ConfigPath, "config", "", "path to config.yaml (default: $XDG_CONFIG_HOME/omssh/config.yaml)")
	root.PersistentFlags().StringVar(&opts.Theme, "theme", "", "theme override: "+join(config.Themes))
	root.PersistentFlags().StringVar(&opts.Icons, "icons", "", "icon set override: "+join(config.IconSets))
	root.PersistentFlags().BoolVar(&opts.NoColor, "no-color", false, "disable colour entirely")
	root.PersistentFlags().BoolVar(&opts.ASCII, "no-icons", false, "use plain ASCII glyphs instead of Nerd Font icons")
	root.PersistentFlags().IntVar(&opts.WidthMargin, "width-margin", -1, "columns to keep free at the right edge (0-8); try 1 if the layout shifts while typing")

	root.SetOut(stdout)
	root.SetErr(stderr)

	root.AddCommand(newExecCmd(opts, stdout, stderr))
	root.AddCommand(newConfigCmd(opts, stdout))
	root.AddCommand(newKeysCmd(opts, stdout))
	root.AddCommand(newVersionCmd(stdout))
	return root
}

// loadConfig resolves the configuration with the command line flags applied on
// top, so a flag always beats a file.
func loadConfig(opts *Options) (config.Config, string, error) {
	path := opts.ConfigPath
	if path == "" {
		var err error
		path, err = config.Path()
		if err != nil {
			return config.Config{}, "", err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, path, err
	}
	if opts.Theme != "" {
		cfg.UI.Theme = opts.Theme
	}
	if opts.Icons != "" {
		cfg.UI.Icons = opts.Icons
	}
	if opts.ASCII {
		cfg.UI.Icons = "ascii"
	}
	if opts.WidthMargin >= 0 {
		cfg.UI.WidthMargin = opts.WidthMargin
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, path, err
	}
	return cfg, path, nil
}

// buildRegistry is the single place the action registry is created, so the CLI
// and the interface can never disagree about what exists.
func buildRegistry() *actions.Registry { return actions.Default() }

// buildKeymap resolves bindings for the configuration's preset and overrides.
func buildKeymap(cfg config.Config) (*actions.Registry, *keymap.Map, error) {
	reg := buildRegistry()
	keys, err := keymap.Build(reg, keymap.Options{Overrides: cfg.Keys.Overrides})
	if err != nil {
		return nil, nil, err
	}
	return reg, keys, nil
}

func join(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
