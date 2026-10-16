package cli

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/fakihariefnoto/omssh/internal/tui"
)

// runTUI starts the interface. It is the default action when omssh is run with
// no subcommand.
func runTUI(opts *Options) error {
	cfg, _, err := loadConfig(opts)
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

	model, err := tui.New(tui.Options{
		Config:   cfg,
		Profile:  profile,
		HasDark:  tui.HasDarkBackground(),
		Registry: reg,
		Keys:     keys,
		Dir:      dir,
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
