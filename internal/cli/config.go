package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/config"
)

func newConfigCmd(opts *Options, stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate omssh's configuration",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the configuration file path",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				path, err := config.Path()
				if err != nil {
					return err
				}
				fmt.Fprintln(stdout, path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "init",
			Short: "Write a commented example configuration, if none exists",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				path, err := config.Path()
				if err != nil {
					return err
				}
				if _, err := os.Stat(path); err == nil {
					fmt.Fprintf(stdout, "%s already exists\n", path)
					return nil
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				if err := config.WriteExampleFile(path); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "wrote %s\n", path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open the configuration in $EDITOR",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				path, err := config.Path()
				if err != nil {
					return err
				}
				if _, err := os.Stat(path); os.IsNotExist(err) {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						return err
					}
					if err := config.WriteExampleFile(path); err != nil {
						return err
					}
				}
				editor := os.Getenv("EDITOR")
				if editor == "" {
					editor = os.Getenv("VISUAL")
				}
				if editor == "" {
					editor = "vi"
				}
				// $EDITOR is the user's own choice, and cobra's context lets a
				// signal during the edit propagate cleanly.
				//nolint:gosec // editor is $EDITOR, the user's own choice, and path is our own config file
				process := exec.CommandContext(cmd.Context(), editor, path)
				process.Stdin, process.Stdout, process.Stderr = os.Stdin, os.Stdout, os.Stderr
				return process.Run()
			},
		},
		&cobra.Command{
			Use:   "check",
			Short: "Validate the configuration and report every problem",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				cfg, path, err := loadConfig(opts)
				if err != nil {
					return err
				}
				// Keys are validated too: a duplicate or undeliverable binding
				// is a configuration error, not a runtime surprise.
				if _, _, err := buildKeymap(cfg); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "%s is valid\n", path)
				fmt.Fprintf(stdout, "  theme %s · icons %s · motion %s\n", cfg.UI.Theme, cfg.UI.Icons, cfg.UI.Motion)
				fmt.Fprintf(stdout, "  keymap %s · copy %s · quit %s\n", cfg.Keys.Preset, cfg.Keys.Copy, cfg.Keys.Quit)
				fmt.Fprintf(stdout, "  transfers %d %s · %d deploy %s\n",
					cfg.Transfers.Workers, plural(cfg.Transfers.Workers, "worker", "workers"),
					len(cfg.Deploy.Profiles), plural(len(cfg.Deploy.Profiles), "profile", "profiles"))
				return nil
			},
		},
	)
	return cmd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
