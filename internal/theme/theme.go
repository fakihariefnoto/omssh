// Package theme turns a named palette into concrete colours and styles for the
// current terminal.
//
// Every token is defined three times — a 16-colour value, a 256-colour value and
// a true-colour value — and lipgloss.Complete picks the one the terminal can
// actually show. That is what makes a 16-colour terminal a supported
// configuration rather than a degraded one, and `mono` a first-class theme
// rather than "colours that happen to be grey".
package theme

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Names of the built-in themes, in the order the settings screen lists them.
// config.Themes must match; a test enforces it.
var names = []string{"auto", "amber", "midnight", "dawn", "mist", "mono", "contrast"}

// Names returns the built-in theme names.
func Names() []string {
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// triple is one token at three colour depths.
type triple struct {
	ansi      string // 0-15
	ansi256   string // 16-255
	truecolor string // #rrggbb
}

// palette is a theme's raw definition, before the terminal is consulted.
type palette struct {
	dark bool
	// noColor marks a theme that uses no colour at all, so `mono` is genuinely
	// monochrome rather than a palette that happens to be grey.
	noColor                         bool
	bg, fg, subtle, selection       triple
	accent, accentEmphasis          triple
	success, warning, danger, muted triple
}

// Colors is a palette resolved to the colours this terminal can display.
type Colors struct {
	BG, FG                 color.Color
	Subtle, Selection      color.Color
	Accent, AccentEmphasis color.Color
	Success, Warning       color.Color
	Danger, Muted          color.Color
}

// Theme is a resolved theme: its name, whether it is dark, and its colours.
type Theme struct {
	Name   string
	Dark   bool
	Colors Colors
}

// IsDark reports whether the theme is intended for a dark terminal.
func (t Theme) IsDark() bool { return t.Dark }

// Resolve picks a theme and downsamples it to the given colour profile.
//
// "auto" resolves to midnight on a dark terminal and dawn on a light one, using
// hasDark, which the caller obtains from the terminal background query.
func Resolve(name string, profile colorprofile.Profile, hasDark bool) (Theme, error) {
	if name == "" || name == "auto" {
		if hasDark {
			name = "midnight"
		} else {
			name = "dawn"
		}
	}
	p, ok := builtin(name)
	if !ok {
		return Theme{}, fmt.Errorf("theme: unknown theme %q, want one of %v", name, names)
	}
	complete := lipgloss.Complete(profile)
	if p.noColor {
		return Theme{Name: name, Dark: p.dark, Colors: Colors{
			BG: noColor, FG: noColor, Subtle: noColor, Selection: noColor,
			Accent: noColor, AccentEmphasis: noColor,
			Success: noColor, Warning: noColor, Danger: noColor, Muted: noColor,
		}}, nil
	}
	return Theme{
		Name: name,
		Dark: p.dark,
		Colors: Colors{
			BG:             p.bg.complete(complete),
			FG:             p.fg.complete(complete),
			Subtle:         p.subtle.complete(complete),
			Selection:      p.selection.complete(complete),
			Accent:         p.accent.complete(complete),
			AccentEmphasis: p.accentEmphasis.complete(complete),
			Success:        p.success.complete(complete),
			Warning:        p.warning.complete(complete),
			Danger:         p.danger.complete(complete),
			Muted:          p.muted.complete(complete),
		},
	}, nil
}

// noColor is the colour a theme uses when it deliberately has none.
var noColor = lipgloss.NoColor{}

// complete picks the tier this profile can render.
func (t triple) complete(c lipgloss.CompleteFunc) color.Color {
	return c(lipgloss.Color(t.ansi), lipgloss.Color(t.ansi256), lipgloss.Color(t.truecolor))
}

// noColorTheme uses no colour at all: the terminal's own defaults carry the
// whole interface, which is what `mono` means.
// mono uses no colour at all: the terminal's own defaults carry the whole
// interface, which is what `mono` means.
var mono = palette{dark: true, noColor: true}

func builtin(name string) (palette, bool) {
	switch name {
	case "midnight":
		return palette{
			dark:           true,
			bg:             triple{"0", "234", "#0E1116"},
			fg:             triple{"7", "252", "#C9D1D9"},
			subtle:         triple{"0", "236", "#1B2129"},
			selection:      triple{"4", "237", "#1F2A33"},
			accent:         triple{"14", "80", "#57D9C9"},
			accentEmphasis: triple{"14", "123", "#8FF5E6"},
			success:        triple{"10", "114", "#7BD88F"},
			warning:        triple{"11", "180", "#E5C07B"},
			danger:         triple{"9", "168", "#E06C75"},
			muted:          triple{"8", "245", "#6B7684"},
		}, true
	case "dawn":
		return palette{
			dark:           false,
			bg:             triple{"15", "255", "#FBFBFD"},
			fg:             triple{"0", "236", "#24292F"},
			subtle:         triple{"7", "254", "#EAEEF2"},
			selection:      triple{"7", "195", "#DDEBEA"},
			accent:         triple{"6", "36", "#1F9D8F"},
			accentEmphasis: triple{"6", "30", "#14776C"},
			success:        triple{"2", "28", "#1A7F37"},
			warning:        triple{"3", "94", "#9A6700"},
			danger:         triple{"1", "160", "#CF222E"},
			muted:          triple{"8", "243", "#6E7781"},
		}, true
	case "mist":
		return palette{
			dark:           true,
			bg:             triple{"0", "235", "#14171A"},
			fg:             triple{"7", "250", "#A9B2BC"},
			subtle:         triple{"0", "237", "#1C2126"},
			selection:      triple{"8", "238", "#242A31"},
			accent:         triple{"6", "109", "#7FB3AD"},
			accentEmphasis: triple{"6", "116", "#9CCCC6"},
			success:        triple{"2", "108", "#84B08C"},
			warning:        triple{"3", "180", "#C0A374"},
			danger:         triple{"1", "174", "#C08080"},
			muted:          triple{"8", "242", "#5C656E"},
		}, true
	case "amber":
		return palette{
			dark:           true,
			bg:             triple{"0", "233", "#191411"},
			fg:             triple{"7", "253", "#EADFD1"},
			subtle:         triple{"0", "235", "#221B16"},
			selection:      triple{"3", "236", "#32261D"},
			accent:         triple{"11", "179", "#E8A33D"},
			accentEmphasis: triple{"11", "222", "#F7CE8B"},
			success:        triple{"2", "107", "#8FB56A"},
			warning:        triple{"3", "173", "#E07A4F"},
			danger:         triple{"1", "167", "#D45D5D"},
			muted:          triple{"8", "243", "#8A7A6A"},
		}, true
	case "contrast":
		return palette{
			dark:           true,
			bg:             triple{"0", "16", "#000000"},
			fg:             triple{"15", "231", "#FFFFFF"},
			subtle:         triple{"0", "235", "#222222"},
			selection:      triple{"4", "17", "#003366"},
			accent:         triple{"14", "51", "#00FFFF"},
			accentEmphasis: triple{"14", "87", "#7FFFFF"},
			success:        triple{"10", "46", "#00FF00"},
			warning:        triple{"11", "226", "#FFFF00"},
			danger:         triple{"9", "196", "#FF0000"},
			muted:          triple{"7", "248", "#AAAAAA"},
		}, true
	case "mono":
		return mono, true
	}
	return palette{}, false
}
