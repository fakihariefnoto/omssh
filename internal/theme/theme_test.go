package theme

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// The settings screen and the config validator must agree on the theme list, or
// one of them is lying.
func TestThemeNamesMatchConfig(t *testing.T) {
	names := Names()
	if len(names) != len(config.Themes) {
		t.Fatalf("theme.Names() = %v, config.Themes = %v", names, config.Themes)
	}
	for i, name := range names {
		if name != config.Themes[i] {
			t.Errorf("position %d: theme %q vs config %q", i, name, config.Themes[i])
		}
	}
}

// auto follows the terminal's background, which is what someone who has not
// chosen a theme wants.
func TestAutoFollowsTheTerminal(t *testing.T) {
	dark, err := Resolve("auto", colorprofile.TrueColor, true)
	if err != nil {
		t.Fatal(err)
	}
	light, err := Resolve("auto", colorprofile.TrueColor, false)
	if err != nil {
		t.Fatal(err)
	}
	if dark.Name != "midnight" {
		t.Errorf("on a dark terminal auto should pick midnight, got %q", dark.Name)
	}
	if light.Name != "dawn" {
		t.Errorf("on a light terminal auto should pick dawn, got %q", light.Name)
	}
	if !dark.IsDark() || light.IsDark() {
		t.Error("the resolved themes report the wrong background")
	}
}

func TestUnknownThemeIsRejected(t *testing.T) {
	if _, err := Resolve("sunset", colorprofile.TrueColor, true); err == nil {
		t.Fatal("an unknown theme should be an error")
	}
	if _, err := Resolve("sunset", colorprofile.TrueColor, true); !strings.Contains(err.Error(), "midnight") {
		t.Errorf("the error should list the real themes, got %v", err)
	}
}

// The three-tier promise: a 16-colour terminal gets a 16-colour accent, a
// true-colour terminal gets the hex value, and a terminal that cannot do colour
// gets none. This is what makes `mono` and narrow terminals supported
// configurations rather than broken ones.
func TestAccentDownsamplesToTheTerminal(t *testing.T) {
	ansiTheme, err := Resolve("midnight", colorprofile.ANSI, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ansiTheme.Colors.Accent.(ansi.BasicColor); !ok {
		t.Errorf("a 16-colour terminal should get a basic colour, got %T", ansiTheme.Colors.Accent)
	}

	mid, err := Resolve("midnight", colorprofile.ANSI256, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mid.Colors.Accent.(lipgloss.ANSIColor); !ok {
		t.Errorf("a 256-colour terminal should get an ansi256 colour, got %T", mid.Colors.Accent)
	}

	trueTheme, err := Resolve("midnight", colorprofile.TrueColor, true)
	if err != nil {
		t.Fatal(err)
	}
	rgba, ok := trueTheme.Colors.Accent.(color.RGBA)
	if !ok {
		t.Fatalf("a true-colour terminal should get an rgba colour, got %T", trueTheme.Colors.Accent)
	}
	if got := rgbaToHex(rgba); got != "#57d9c9" {
		t.Errorf("the signature accent should be #57D9C9, got %s", got)
	}

	none, err := Resolve("midnight", colorprofile.NoTTY, true)
	if err != nil {
		t.Fatal(err)
	}
	if none.Colors.Accent != (lipgloss.NoColor{}) {
		t.Errorf("a terminal without colour should get no colour, got %T", none.Colors.Accent)
	}
}

// mono is a first-class theme, not a grey palette: it uses no colour at all.
func TestMonoUsesNoColour(t *testing.T) {
	mono, err := Resolve("mono", colorprofile.TrueColor, true)
	if err != nil {
		t.Fatal(err)
	}
	if mono.Colors.Accent != (lipgloss.NoColor{}) {
		t.Errorf("mono should have no accent colour, got %T", mono.Colors.Accent)
	}
	if mono.Colors.Danger != (lipgloss.NoColor{}) {
		t.Errorf("mono should have no danger colour, got %T", mono.Colors.Danger)
	}
}

// Every theme must define every token: a missing one would render as the
// terminal default and look like a bug.
func TestEveryThemeResolvesEveryToken(t *testing.T) {
	for _, name := range Names() {
		for _, profile := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor} {
			theme, err := Resolve(name, profile, true)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if theme.Colors.FG == nil || theme.Colors.Danger == nil || theme.Colors.Selection == nil {
				t.Errorf("%s at %v is missing a token", name, profile)
			}
		}
	}
}

// Selecting and cursoring must be distinguishable, and the focused pane must be
// distinguishable from both: that is what makes "where am I" obvious.
func TestFocusSelectionAndCursorDiffer(t *testing.T) {
	th, err := Resolve("midnight", colorprofile.TrueColor, true)
	if err != nil {
		t.Fatal(err)
	}
	if sameColor(th.Colors.Selection, th.Colors.Subtle) {
		t.Error("the selection and the subtle background must differ")
	}
	if sameColor(th.Colors.Accent, th.Colors.Muted) {
		t.Error("the accent and the muted colour must differ")
	}
	styles := NewStyles(th)
	if styles.PaneBorder.Render("x") == styles.PaneBorderBlur.Render("x") {
		t.Error("the focused and unfocused pane borders must differ")
	}
	if sameColor(th.Colors.Danger, th.Colors.Success) {
		t.Error("danger and success must differ")
	}
}

// Icons are two sets, and the ASCII set must contain no private-use glyphs, or
// it would render as a row of boxes on a terminal without a Nerd Font.
func TestASCIIConsHaveNoNerdGlyphs(t *testing.T) {
	icons := ASCII()
	all := []string{
		icons.Folder, icons.File, icons.Symlink, icons.Archive,
		icons.Local, icons.Remote, icons.DotReady, icons.DotBusy,
		icons.DotIdle, icons.DotError, icons.Check, icons.Cross,
		icons.Warning, icons.Info, icons.ArrowUp, icons.ArrowDown,
		icons.Selected, icons.Unselected,
	}
	for _, glyph := range all {
		for _, r := range glyph {
			if r >= 0xE000 && r <= 0xF8FF {
				t.Errorf("the ASCII set contains the private-use glyph %q", glyph)
			}
		}
	}
	if IconsFor("nonsense").Folder != ASCII().Folder {
		t.Error("an unknown icon set should fall back to ASCII")
	}
	if IconsFor("nerd").Folder != Nerd().Folder {
		t.Error("the nerd set should be selectable")
	}
}

func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func rgbaToHex(c color.RGBA) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 7)
	out = append(out, '#')
	for _, v := range []byte{c.R, c.G, c.B} {
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out)
}
