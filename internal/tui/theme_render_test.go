package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/fakihariefnoto/omssh/internal/config"
)

func themedModel(t *testing.T, name string) *Model {
	t.Helper()
	cfg := config.Default()
	cfg.UI.Theme = name
	cfg.UI.Start = "home"
	m, err := New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: false})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 100, 30)
	return m
}

// A light theme has to paint its own background. Without it, dawn on a dark
// terminal drew dark text on the terminal's dark screen — the menu that looked
// like it had merged with the background.
func TestLightThemePaintsItsBackground(t *testing.T) {
	m := themedModel(t, "dawn")
	out := render(m)
	if !strings.Contains(out, "\x1b[48;2;251;251;253m") {
		t.Fatalf("dawn should paint its own background")
	}
	// And every line carries it, not just the styled ones.
	for i, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "\x1b[48;2;251;251;253m") {
			t.Fatalf("line %d has no theme background: %q", i, line)
		}
	}
}

// Mono has no colours at all, so selection has to be carried by attributes.
func TestMonoCarriesSelectionByAttribute(t *testing.T) {
	m := themedModel(t, "mono")
	if m.bgSeq != "" {
		t.Error("mono should not paint a background")
	}
	if !m.st.PaneRowCursor.GetReverse() {
		t.Error("the cursor row should be reverse video in mono")
	}
	if !m.st.SidebarItemActive.GetReverse() {
		t.Error("the active sidebar row should be reverse video in mono")
	}
	if !m.st.PaletteItemActive.GetReverse() {
		t.Error("the palette selection should be reverse video in mono")
	}
	if !m.st.StatusMode.GetReverse() {
		t.Error("the mode chip should be reverse video in mono")
	}
	// The launcher's chosen row is drawn with reverse video, so it is visible.
	out := render(m)
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("mono should still use attributes")
	}
}
