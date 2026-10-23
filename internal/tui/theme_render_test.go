package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
)

func themedModel(t *testing.T, name string, hasDark ...bool) *Model {
	t.Helper()
	dark := false
	if len(hasDark) > 0 {
		dark = hasDark[0]
	}
	cfg := config.Default()
	cfg.UI.Theme = name
	cfg.UI.Start = "home"
	// DarkKnown mirrors a terminal that said which it is (COLORFGBG or the query),
	// which is the only case "auto" acts on.
	m, err := New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: dark, DarkKnown: true})
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
	// A light theme on a dark terminal is the case that was unreadable.
	m := themedModel(t, "dawn", true)
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

// When the theme and the terminal already agree, the terminal's own background —
// including transparency — is left alone. Painting a matching colour would cover
// a terminal that was configured on purpose.
func TestMatchingBackgroundIsLeftAlone(t *testing.T) {
	light := themedModel(t, "dawn", false)
	if light.bgSeq != "" {
		t.Errorf("dawn on a light terminal should not paint: %q", light.bgSeq)
	}
	dark := themedModel(t, "midnight", true)
	if dark.bgSeq != "" {
		t.Errorf("midnight on a dark terminal should not paint: %q", dark.bgSeq)
	}
	// The mismatch in the other direction is painted too.
	if themedModel(t, "midnight", false).bgSeq == "" {
		t.Error("midnight on a light terminal should paint its dark background")
	}
}

// A terminal that did not say is left alone by "auto": guessing wrong would paint
// over a transparent background for no reason. "always" and "never" are explicit.
func TestBackgroundModes(t *testing.T) {
	base := config.Default()
	base.UI.Theme = "dawn"

	unknown := base
	m, err := New(Options{Config: unknown, Profile: colorprofile.TrueColor, HasDark: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.bgSeq != "" {
		t.Error("an unknown terminal should not be painted over")
	}

	always := base
	always.UI.PaintBackground = "always"
	m, err = New(Options{Config: always, Profile: colorprofile.TrueColor, HasDark: false})
	if err != nil {
		t.Fatal(err)
	}
	if m.bgSeq == "" {
		t.Error("paint_background: always should paint")
	}

	never := base
	never.UI.PaintBackground = "never"
	m, err = New(Options{Config: never, Profile: colorprofile.TrueColor, HasDark: true, DarkKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.bgSeq != "" {
		t.Error("paint_background: never should leave the terminal alone")
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

// A painted background needs the matching foreground: a light theme on a dark
// terminal inherits a light default foreground, and white text on a white
// background is what "dawn is broken" looked like.
func TestPaintedBackgroundAlsoSetsTheForeground(t *testing.T) {
	m := themedModel(t, "dawn", true)
	out := render(m)
	if !strings.Contains(out, "\x1b[38;2;36;41;47m") {
		t.Fatalf("dawn should paint its foreground as well")
	}
	for i, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "\x1b[38;2;36;41;47m") {
			t.Fatalf("line %d has no theme foreground: %q", i, line)
		}
	}
}

// Mono has no colour, but it still has shape: panes keep their borders, and the
// focused one is marked with a heavier border rather than a tint.
func TestMonoKeepsPaneBorders(t *testing.T) {
	m := themedModel(t, "mono")
	m.openFiles()
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "╔") && !strings.Contains(out, "╭") {
		t.Fatalf("mono panes should have borders:\n%s", out)
	}
	// The focused pane's border is distinct, so "where am I" survives mono.
	if m.st.PaneBorder.GetBorderStyle() == m.st.PaneBorderBlur.GetBorderStyle() {
		t.Error("the focused and unfocused borders should differ in mono")
	}
}
