package theme

import (
	"charm.land/lipgloss/v2"
)

// Styles is every style the interface uses, derived from one theme.
//
// Views never construct colours themselves: they ask for a semantic style, so
// switching theme changes intent rather than chasing hex values through the
// rendering code.
type Styles struct {
	Theme Theme

	// Text
	Accent  lipgloss.Style
	Muted   lipgloss.Style
	Success lipgloss.Style
	Warning lipgloss.Style
	Danger  lipgloss.Style

	// Pane chrome
	PaneTitle        lipgloss.Style
	PaneTitleBlurred lipgloss.Style
	PaneBorder       lipgloss.Style
	PaneBorderBlur   lipgloss.Style
	PaneHeader       lipgloss.Style
	PaneRow          lipgloss.Style
	PaneRowCursor    lipgloss.Style
	PaneRowSelected  lipgloss.Style
	PaneDir          lipgloss.Style
	PaneSymlink      lipgloss.Style
	PaneEmpty        lipgloss.Style

	// Breadcrumb and path bar
	Breadcrumb       lipgloss.Style
	BreadcrumbActive lipgloss.Style
	PathInput        lipgloss.Style

	// Session tabs
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	TabReady    lipgloss.Style
	TabBusy     lipgloss.Style
	TabIdle     lipgloss.Style
	TabError    lipgloss.Style

	// Action bar
	ActionBar      lipgloss.Style
	ActionVerb     lipgloss.Style
	ActionKey      lipgloss.Style
	ActionHint     lipgloss.Style
	ActionSelected lipgloss.Style

	// The bar that says what is marked and where it is going. It sits between
	// the panes and the action bar, and only exists while something is marked.
	StagedBar lipgloss.Style

	// Sidebar
	Sidebar           lipgloss.Style
	SidebarTitle      lipgloss.Style
	SidebarItem       lipgloss.Style
	SidebarItemActive lipgloss.Style

	// Status bar
	StatusBar   lipgloss.Style
	StatusText  lipgloss.Style
	StatusKey   lipgloss.Style
	StatusMuted lipgloss.Style
	StatusMode  lipgloss.Style

	// Overlays
	Card              lipgloss.Style
	CardTitle         lipgloss.Style
	CardText          lipgloss.Style
	CardMuted         lipgloss.Style
	Button            lipgloss.Style
	ButtonActive      lipgloss.Style
	ButtonDanger      lipgloss.Style
	PaletteItem       lipgloss.Style
	PaletteItemActive lipgloss.Style
	PaletteKey        lipgloss.Style
	PaletteGroup      lipgloss.Style
	Menu              lipgloss.Style

	// Toasts
	ToastInfo    lipgloss.Style
	ToastSuccess lipgloss.Style
	ToastWarning lipgloss.Style
	ToastError   lipgloss.Style
}

// NewStyles derives the styles for a theme.
func NewStyles(t Theme) Styles {
	if t.Mono {
		return monoStyles(t)
	}
	c := t.Colors
	fg := lipgloss.NewStyle().Foreground(c.FG)
	accent := lipgloss.NewStyle().Foreground(c.Accent)
	muted := lipgloss.NewStyle().Foreground(c.Muted)

	// The focused pane is the only place the accent shows up as a border, so
	// "where am I" is answered by colour before anything is read.
	focused := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c.Accent)
	blurred := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c.Muted)

	return Styles{
		Theme: t,

		Accent:  accent,
		Muted:   muted,
		Success: lipgloss.NewStyle().Foreground(c.Success),
		Warning: lipgloss.NewStyle().Foreground(c.Warning),
		Danger:  lipgloss.NewStyle().Foreground(c.Danger),

		PaneTitle:        accent.Bold(true),
		PaneTitleBlurred: muted.Bold(true),
		PaneBorder:       focused,
		PaneBorderBlur:   blurred,
		PaneHeader:       lipgloss.NewStyle().Foreground(c.FG).Background(c.Subtle),
		PaneRow:          fg,
		PaneRowCursor:    lipgloss.NewStyle().Foreground(c.FG).Background(c.Selection).Bold(true),
		PaneRowSelected:  lipgloss.NewStyle().Foreground(c.AccentEmphasis),
		PaneDir:          accent,
		PaneSymlink:      lipgloss.NewStyle().Foreground(c.Warning),
		PaneEmpty:        muted.Italic(true),

		Breadcrumb:       muted,
		BreadcrumbActive: accent.Bold(true),
		PathInput:        lipgloss.NewStyle().Foreground(c.FG).Background(c.Subtle),

		// A tab is text with an underline, not a coloured block: a filled chip
		// reads as a floating badge or a tooltip rather than as chrome.
		TabActive:   lipgloss.NewStyle().Foreground(c.Accent).Bold(true).Underline(true),
		TabInactive: lipgloss.NewStyle().Foreground(c.Muted),
		TabReady:    lipgloss.NewStyle().Foreground(c.Success),
		TabBusy:     lipgloss.NewStyle().Foreground(c.Warning),
		TabIdle:     muted,
		TabError:    lipgloss.NewStyle().Foreground(c.Danger),

		ActionBar:      lipgloss.NewStyle().Foreground(c.Muted).Background(c.Subtle).Padding(0, 1),
		ActionVerb:     accent.Bold(true),
		ActionKey:      lipgloss.NewStyle().Foreground(c.FG).Background(c.Selection).Padding(0, 1),
		ActionHint:     muted,
		ActionSelected: lipgloss.NewStyle().Foreground(c.AccentEmphasis).Bold(true),
		StagedBar:      lipgloss.NewStyle().Foreground(c.FG).Background(c.Selection).Padding(0, 1),

		Sidebar:           lipgloss.NewStyle().Padding(0, 1),
		SidebarTitle:      muted.Bold(true),
		SidebarItem:       fg,
		SidebarItemActive: lipgloss.NewStyle().Foreground(c.AccentEmphasis).Bold(true),

		StatusBar:   lipgloss.NewStyle().Foreground(c.FG).Background(c.Subtle).Padding(0, 1),
		StatusText:  fg,
		StatusKey:   lipgloss.NewStyle().Foreground(c.Accent).Bold(true),
		StatusMuted: muted,
		StatusMode:  lipgloss.NewStyle().Foreground(c.BG).Background(c.Accent).Bold(true).Padding(0, 1),

		Card:              lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c.Accent).Padding(1, 2),
		CardTitle:         accent.Bold(true),
		CardText:          fg,
		CardMuted:         muted,
		Button:            lipgloss.NewStyle().Foreground(c.FG).Background(c.Subtle).Padding(0, 2),
		ButtonActive:      lipgloss.NewStyle().Foreground(c.BG).Background(c.Accent).Bold(true).Padding(0, 2),
		ButtonDanger:      lipgloss.NewStyle().Foreground(c.BG).Background(c.Danger).Bold(true).Padding(0, 2),
		PaletteItem:       fg,
		PaletteItemActive: lipgloss.NewStyle().Foreground(c.BG).Background(c.Accent).Bold(true),
		PaletteKey:        lipgloss.NewStyle().Foreground(c.Muted).Padding(0, 1),
		PaletteGroup:      muted.Bold(true),
		Menu:              lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c.Accent).Padding(0, 1),

		ToastInfo:    lipgloss.NewStyle().Foreground(c.BG).Background(c.Accent).Padding(0, 1),
		ToastSuccess: lipgloss.NewStyle().Foreground(c.BG).Background(c.Success).Padding(0, 1),
		ToastWarning: lipgloss.NewStyle().Foreground(c.BG).Background(c.Warning).Padding(0, 1),
		ToastError:   lipgloss.NewStyle().Foreground(c.BG).Background(c.Danger).Bold(true).Padding(0, 1),
	}
}

// monoStyles is the monochrome theme: selection and emphasis are carried by
// attributes, not colour. A theme that sets every colour to "inherit" would
// otherwise render a highlighted row exactly like an unhighlighted one — which is
// what "the cursor does not work" looked like.
func monoStyles(t Theme) Styles {
	plain := lipgloss.NewStyle()
	selected := plain.Reverse(true).Bold(true)
	emphasis := plain.Bold(true).Underline(true)
	muted := plain.Faint(true)
	danger := plain.Bold(true)

	s := Styles{
		Theme: t,

		Accent:  emphasis,
		Muted:   muted,
		Success: plain,
		Warning: emphasis,
		Danger:  danger,

		PaneTitle:        emphasis,
		PaneTitleBlurred: muted,
		PaneBorder:       plain,
		PaneBorderBlur:   plain.Faint(true),
		PaneHeader:       plain.Underline(true),
		PaneRow:          plain,
		PaneRowCursor:    selected,
		PaneRowSelected:  emphasis,
		PaneDir:          plain.Bold(true),
		PaneSymlink:      plain.Underline(true),
		PaneEmpty:        muted.Italic(true),

		Breadcrumb:       muted,
		BreadcrumbActive: emphasis,
		PathInput:        plain.Reverse(true),

		TabActive:   emphasis,
		TabInactive: muted,
		TabReady:    plain,
		TabBusy:     emphasis,
		TabIdle:     muted,
		TabError:    plain.Bold(true),

		ActionBar:      muted,
		ActionVerb:     plain.Bold(true),
		ActionKey:      plain.Reverse(true),
		ActionHint:     muted,
		ActionSelected: selected,
		StagedBar:      plain.Reverse(true),

		Sidebar:           plain,
		SidebarTitle:      emphasis,
		SidebarItem:       plain,
		SidebarItemActive: selected,

		StatusBar:   muted,
		StatusText:  plain,
		StatusKey:   plain.Bold(true),
		StatusMuted: muted,
		StatusMode:  plain.Reverse(true).Bold(true),

		Card:              plain.Border(lipgloss.RoundedBorder()),
		CardTitle:         emphasis,
		CardText:          plain,
		CardMuted:         muted,
		Button:            plain,
		ButtonActive:      selected,
		ButtonDanger:      selected,
		PaletteItem:       plain,
		PaletteItemActive: selected,
		PaletteKey:        muted,
		PaletteGroup:      emphasis,
		Menu:              plain.Border(lipgloss.RoundedBorder()).Padding(0, 1),

		ToastInfo:    plain.Reverse(true),
		ToastSuccess: plain.Reverse(true),
		ToastWarning: plain.Reverse(true).Bold(true),
		ToastError:   plain.Reverse(true).Bold(true),
	}
	return s
}

// Icons holds the glyphs used for the interface, in two flavours: Nerd Font
// glyphs, and a plain Unicode/ASCII set for terminals without the font. The
// interface must never render a tofu box.
type Icons struct {
	Folder, File, Symlink, Archive       string
	Local, Remote                        string
	DotReady, DotBusy, DotIdle, DotError string
	Check, Cross, Warning, Info          string
	ArrowUp, ArrowDown                   string
	Selected, Unselected                 string
}

// Nerd returns the Nerd Font icon set.
func Nerd() Icons {
	return Icons{
		Folder: "\uf07b", File: "\uf15b", Symlink: "\uf0c1", Archive: "\uf410",
		Local: "\uf109", Remote: "\uf233",
		DotReady: "\uf111", DotBusy: "\uf111", DotIdle: "\uf10c", DotError: "\uf00d",
		Check: "\uf00c", Cross: "\uf00d", Warning: "\uf071", Info: "\uf05a",
		ArrowUp: "\uf062", ArrowDown: "\uf063",
		Selected: "\uf046", Unselected: "\uf096",
	}
}

// Unicode returns a set every ordinary terminal font can draw: BMP symbols, one
// cell wide, no private-use area and no emoji (which are double-width and would
// shift every column). It is the default, because a folder icon that renders as
// a question mark is worse than a triangle that renders everywhere.
func Unicode() Icons {
	return Icons{
		Folder: "▸", File: "·", Symlink: "@", Archive: "≡",
		Local: "L", Remote: "R",
		DotReady: "●", DotBusy: "◐", DotIdle: "○", DotError: "✕",
		Check: "✓", Cross: "✕", Warning: "⚠", Info: "i",
		ArrowUp: "↑", ArrowDown: "↓",
		Selected: "[x]", Unselected: "[ ]",
	}
}

// ASCII returns the fallback set: no private-use glyphs, nothing wider than one
// cell, and no dependence on a patched font.
func ASCII() Icons {
	return Icons{
		Folder: "d", File: "-", Symlink: "@", Archive: "z",
		Local: "L", Remote: "R",
		DotReady: "*", DotBusy: "~", DotIdle: "o", DotError: "x",
		Check: "+", Cross: "x", Warning: "!", Info: "i",
		ArrowUp: "^", ArrowDown: "v",
		Selected: "[x]", Unselected: "[ ]",
	}
}

// IconsFor returns the icon set named in the config. An unknown name falls back
// to the set that is safe everywhere rather than to the one that needs a font.
func IconsFor(name string) Icons {
	switch name {
	case "nerd":
		return Nerd()
	case "ascii":
		return ASCII()
	default:
		return Unicode()
	}
}
