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
)

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
}

// homeItems is the menu. It lists what this build can actually do; options whose
// feature has not landed are absent rather than disabled, because a menu of dead
// ends teaches nothing. Connect joins this list in M1.
func (m *Model) homeItems() []homeItem {
	items := make([]homeItem, 0, 6)
	if m.offersConnect {
		items = append(items, homeItem{ID: "session.open", Icon: ">", Label: "Connect to a host", Hint: "Ctrl+O"})
	}
	return append(items,
		homeItem{ID: "app.files", Icon: "~", Label: "Browse local files", Hint: "Enter"},
		homeItem{ID: "nav.path", Icon: "/", Label: "Go to a folder", Hint: "Ctrl+L"},
		homeItem{ID: "app.help", Icon: "?", Label: "Keyboard and help", Hint: "F1"},
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

	// The whole plate and menu is laid out as one block and then centred, so the
	// icons form a column. Centring each row on its own would leave every line
	// with a different left edge, which reads as a broken layout.
	type row struct {
		text   string
		centre bool
	}
	block := make([]row, 0, 24)
	for _, line := range plate {
		block = append(block, row{text: m.st.Accent.Render(line)})
	}
	block = append(block,
		row{},
		row{text: m.st.Muted.Render("SSH / SFTP for the terminal"), centre: true},
		row{},
	)

	// The hints are the first thing to go when the terminal is narrow: the menu
	// itself must survive at any width.
	const hintColumn = 26
	showHints := width >= 42
	items := m.homeItems()
	cursor := clamp(m.home.cursor, 0, max(0, len(items)-1))
	for i, item := range items {
		icon := m.st.StatusKey.Render("[" + item.Icon + "]")
		gap, hint := "", ""
		if showHints {
			gap = spaces(max(1, hintColumn-lipgloss.Width(item.Label)))
			hint = m.st.StatusMuted.Render(item.Hint)
		}
		if i == cursor {
			block = append(block, row{text: m.st.Accent.Bold(true).Render(" ▸ ") + icon + " " +
				m.st.Accent.Bold(true).Render(item.Label) + gap + hint})
			continue
		}
		block = append(block, row{text: "   " + icon + " " + m.st.CardText.Render(item.Label) + gap + hint})
	}

	footer := "↑↓ choose · Enter open · Esc go to the files · Ctrl+K actions"
	if width < lipgloss.Width(footer)+2 {
		footer = "↑↓ · Enter · Esc files"
	}
	block = append(block, row{}, row{text: m.st.CardMuted.Render(footer), centre: true})

	blockWidth := 0
	for _, r := range block {
		blockWidth = max(blockWidth, lipgloss.Width(r.text))
	}
	blockWidth = min(blockWidth, width)

	rows := make([]string, 0, len(block))
	for _, r := range block {
		if r.centre {
			rows = append(rows, lipgloss.PlaceHorizontal(blockWidth, lipgloss.Center, ansi.Truncate(r.text, blockWidth, "…")))
			continue
		}
		rows = append(rows, padVisible(ansi.Truncate(r.text, blockWidth, "…"), blockWidth))
	}

	// Centre the block vertically, then every line horizontally.
	out := make([]string, 0, l.Height)
	for i := 0; i < top; i++ {
		out = append(out, spaces(width))
	}
	for _, row := range rows {
		// Every row is clamped before it is centred, so nothing can wrap and push
		// the frame down: a plate wider than the terminal must be cut, not scrolled.
		out = append(out, lipgloss.PlaceHorizontal(width, lipgloss.Center, ansi.Truncate(row, width, "…")))
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
func (m *Model) handleHomeKey(msg tea.KeyPressMsg) tea.Cmd {
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
		m.openFiles()
	case "enter", "space":
		if m.home.cursor < len(items) {
			return m.activateHome(items[m.home.cursor])
		}
	}
	return nil
}

// activateHome runs a launcher entry.
func (m *Model) activateHome(item homeItem) tea.Cmd {
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
