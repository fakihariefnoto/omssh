package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/actions"
)

// screen is which of the two top-level views is showing.
type screen int

const (
	// screenHome is the front door: a plate, and the things you can do.
	screenHome screen = iota
	// screenFiles is the file manager.
	screenFiles
	// screenConnections manages the hosts in ~/.ssh/config.
	screenConnections
)

// screenName is the value actions.Context sees, so an action can apply on one
// screen and not another.
func (m *Model) screenName() string {
	switch m.screen {
	case screenHome:
		return "home"
	case screenConnections:
		return "connections"
	default:
		return "files"
	}
}

// logo is the wordmark on the home screen. Five rows of equal width, so it
// cannot shear; a test asserts that.
var logo = []string{
	" ██████  ███    ███ ██████  ██████  ██    ██",
	"██    ██ ████  ████ ██      ██      ██    ██",
	"██    ██ ██ ████ ██ █████   █████   ████████",
	"██    ██ ██  ██  ██      ██      ██ ██    ██",
	" ██████  ██      ██ ██████  ██████  ██    ██",
}

// narrowLogo is the fallback when the plate does not fit: plain ASCII, no wider
// than a phone-sized terminal.
var narrowLogo = []string{
	"+-------+",
	"| omssh |",
	"+-------+",
}

// homeState is the launcher's cursor.
type homeState struct{ cursor int }

// homeItem is one row of the launcher. The icon is deliberately plain ASCII: it
// must read the same on every terminal, before any theme is applied.
type homeItem struct {
	ID    actions.ID
	Icon  string
	Label string
	Hint  string
	// Host is set on the rows that stand for a live session, so choosing one goes
	// back to that host rather than starting over.
	Host string
}

// homeItems is the menu. It lists what this build can actually do; options whose
// feature has not landed are absent rather than disabled, because a menu of dead
// ends teaches nothing. Connect joins this list in M1.
func (m *Model) homeItems() []homeItem {
	items := make([]homeItem, 0, 6+len(m.tabs))
	// The hosts that are still connected come first: coming back to the launcher
	// from a pane should offer the way back, not only the way in.
	for _, tab := range m.tabs {
		if tab.Host == "" {
			continue
		}
		hint := tab.State.Label()
		if tab.State == StateReady {
			hint = "connected"
		}
		items = append(items, homeItem{ID: "session.focus", Icon: "●", Label: tab.Host, Hint: hint, Host: tab.Host})
	}
	if len(items) > 0 {
		items = append(items, homeItem{ID: "app.files", Icon: "~", Label: "Back to the files", Hint: "Enter"})
	}
	return append(items,
		homeItem{ID: "session.open", Icon: ">", Label: "SSH connections", Hint: "Ctrl+O"},
		homeItem{ID: "app.files", Icon: "~", Label: "Browse local files", Hint: "Enter"},
		homeItem{ID: "nav.path", Icon: "/", Label: "Go to a folder", Hint: "Ctrl+L"},
		homeItem{ID: "app.help", Icon: "?", Label: "Keyboard and help", Hint: "?"},
		homeItem{ID: "app.settings", Icon: "+", Label: "Settings and theme", Hint: "Ctrl+,"},
		homeItem{ID: "app.quit", Icon: "x", Label: "Quit", Hint: "Ctrl+Q"},
	)
}

// homeGeometry reports where the launcher's rows land, so a click and a key press
// agree about what is under the pointer. It returns the plate, the first row of
// the screen, and the index of the first menu row.
func (m *Model) homeGeometry(l layout) (plate []string, top, itemStart int) {
	plate = logo
	if l.Width < lipgloss.Width(logo[0])+4 {
		plate = narrowLogo
	}
	itemStart = len(plate) + 3 // plate, blank, tagline, blank
	total := itemStart + len(m.homeItems()) + 2
	top = max(0, (l.Height-total)/2)
	return plate, top, itemStart
}

// renderHome draws the front door: the plate, the menu, and one line of keys.
// It fills exactly the terminal, because a short screen leaves the previous
// frame's crumbs behind it.
func (m *Model) renderHome(l layout) string {
	width := l.Width
	plate, top, _ := m.homeGeometry(l)

	// The plate and the menu are two centred units, not one. They are different
	// widths (the plate is 44, the menu is as wide as its longest hint), and
	// padding the narrow one into the wide one's box would leave the wordmark
	// sitting visibly left of centre.
	tagline := "Oh My SSH — SSH/SFTP with terminal UI"
	if width < lipgloss.Width(tagline)+2 {
		tagline = "Oh My SSH"
	}

	rows := make([]string, 0, l.Height)
	for _, line := range plate {
		rows = append(rows, lipgloss.PlaceHorizontal(width, lipgloss.Center, m.st.Accent.Render(line)))
	}
	rows = append(rows, "",
		lipgloss.PlaceHorizontal(width, lipgloss.Center, m.st.Muted.Render(tagline)),
		"")

	// The menu is one block: its rows are padded to a common width so the icons
	// form a column, and only then is the block centred.
	type menuRow struct {
		text   string
		centre bool
	}
	const hintColumn = 26
	showHints := width >= 46
	items := m.homeItems()
	cursor := clamp(m.home.cursor, 0, max(0, len(items)-1))
	menu := make([]menuRow, 0, len(items)+2)
	for i, item := range items {
		icon := m.st.StatusKey.Render("[" + item.Icon + "]")
		gap, hint := "", ""
		if showHints {
			gap = spaces(max(1, hintColumn-lipgloss.Width(item.Label)))
			hint = m.st.StatusMuted.Render(item.Hint)
		}
		if i == cursor {
			menu = append(menu, menuRow{text: m.st.Accent.Bold(true).Render(" ▸ ") + icon + " " +
				m.st.Accent.Bold(true).Render(item.Label) + gap + hint})
			continue
		}
		menu = append(menu, menuRow{text: "   " + icon + " " + m.st.CardText.Render(item.Label) + gap + hint})
	}

	footer := "↑↓ choose · Enter open · Esc go to the files · Ctrl+K actions"
	if width < lipgloss.Width(footer)+2 {
		footer = "↑↓ · Enter · Esc files"
	}
	menu = append(menu, menuRow{}, menuRow{text: m.st.CardMuted.Render(footer), centre: true})

	menuWidth := 0
	for _, r := range menu {
		menuWidth = max(menuWidth, lipgloss.Width(r.text))
	}
	menuWidth = min(menuWidth, width)

	rows = append(rows, "")
	for _, r := range menu {
		text := ansi.Truncate(r.text, menuWidth, "…")
		if r.centre {
			text = lipgloss.PlaceHorizontal(menuWidth, lipgloss.Center, text)
		} else {
			text = padVisible(text, menuWidth)
		}
		rows = append(rows, lipgloss.PlaceHorizontal(width, lipgloss.Center, text))
	}
	rows = rows[:len(rows):len(rows)]

	// Centre the whole block vertically; each line is already centred.
	out := make([]string, 0, l.Height)
	for i := 0; i < top; i++ {
		out = append(out, spaces(width))
	}
	for _, row := range rows {
		// Every line is exactly the terminal width: a short line is fine to draw
		// but it makes the frame's shape depend on the renderer's clearing
		// behaviour, and the tests could no longer tell a full screen from a
		// half-drawn one.
		out = append(out, padVisible(ansi.Truncate(row, width, "…"), width))
	}
	for len(out) < l.Height {
		out = append(out, spaces(width))
	}
	if len(out) > l.Height {
		out = out[:l.Height]
	}
	return strings.Join(out, "\n")
}

// handleHomeKey drives the launcher. Escape is the documented way straight into
// the files, so the launcher can never trap someone who wants to start working.
//
// It reports whether it consumed the key. Modified keys fall through to the
// keymap, because the menu advertises shortcuts like Ctrl+O and a launcher that
// swallowed them would be advertising keys that do nothing.
func (m *Model) handleHomeKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if msg.Key().Mod != 0 {
		return nil, false
	}
	items := m.homeItems()
	switch msg.String() {
	case "up", "k", "shift+tab":
		m.home.cursor = (m.home.cursor + len(items) - 1) % len(items)
	case "down", "j", "tab":
		m.home.cursor = (m.home.cursor + 1) % len(items)
	case "home":
		m.home.cursor = 0
	case "end":
		m.home.cursor = len(items) - 1
	case "esc":
		// The launcher is the bottom of the stack, so Escape stops here and says
		// how to leave for good.
		m.quitHint = "Ctrl+Q to quit"
	case "enter", "space":
		if m.home.cursor < len(items) {
			return m.activateHome(items[m.home.cursor]), true
		}
	default:
		// Swallow other printable input: the file panes sit behind the launcher,
		// and their type-ahead must not fire from here.
		if msg.Key().Text != "" {
			return nil, true
		}
	}
	return nil, true
}

// activateHome runs a launcher entry.
func (m *Model) activateHome(item homeItem) tea.Cmd {
	if item.Host != "" {
		// A live session: go back to it, on its tab, with its pane focused.
		for i, tab := range m.tabs {
			if tab.Host == item.Host {
				m.openFiles()
				return m.selectTab(i, true)
			}
		}
		m.toastInfo(item.Host + " is no longer connected")
		return nil
	}
	switch item.ID {
	case "app.help", "app.settings":
		// Overlays open on top of the launcher, so the way back stays obvious.
		return m.runAction(item.ID)
	case "app.quit":
		m.quit = true
		return tea.Quit
	}
	// Everything else needs the file manager, so switch first and then act.
	m.openFiles()
	return m.runAction(item.ID)
}

// openFiles leaves the launcher for the file manager.
func (m *Model) openFiles() {
	m.screen = screenFiles
	m.mode = "NORMAL"
}
