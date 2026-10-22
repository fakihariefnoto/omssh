package cli

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/fakihariefnoto/omssh/internal/tui"
)

// runTUI starts the interface. It is the default action when omssh is run with
// no subcommand; a host argument opens that session once the interface is up.
func runTUI(opts *Options, connect string) error {
	cfg, configPath, err := loadConfig(opts)
	if err != nil {
		return err
	}
	reg, keys, err := buildKeymap(cfg)
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	profile := tui.ProfileFor(os.Stderr, os.Environ())
	if opts.NoColor {
		profile = colorprofile.ASCII
	}

	// COLORFGBG is free to read and most terminals set it. When it is missing the
	// dark themes are assumed: asking the terminal would gate the first paint on
	// a reply that some terminals never send. `Follow terminal background` in the
	// palette performs that query on demand.
	dark, darkKnown := tui.BackgroundFromEnv(os.Environ())
	model, err := tui.New(tui.Options{
		Config:     cfg,
		Profile:    profile,
		HasDark:    dark,
		DarkKnown:  darkKnown,
		Registry:   reg,
		Keys:       keys,
		Dir:        dir,
		Connect:    connect,
		ConfigPath: configPath,
	})
	if err != nil {
		return err
	}

	program := tea.NewProgram(model, tea.WithColorProfile(profile))
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("running the interface: %w", err)
	}
	return nil
}
