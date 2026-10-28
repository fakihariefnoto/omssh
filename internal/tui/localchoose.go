package tui

import (
	tea "charm.land/bubbletea/v2"
)

// The launcher has one way into the local machine, not two rows that each answer
// half the question. Choosing it asks the smaller question — browse what is here,
// or go somewhere — and then hands over to the dashboard chooser, where the
// remote panes (up to four) are ticked as usual.

// localChooseState is the two-option card's cursor.
type localChooseState struct{ cursor int }

// openLocalChooser shows the two ways into the local filesystem.
func (m *Model) openLocalChooser() {
	m.localChoose = localChooseState{}
	m.Overlay = OverlayLocalChoose
}

// handleLocalChooseKey picks one.
func (m *Model) handleLocalChooseKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc", "q":
		m.Overlay = OverlayNone
	case "up", "k":
		m.localChoose.cursor = max(0, m.localChoose.cursor-1)
	case "down", "j":
		m.localChoose.cursor = min(1, m.localChoose.cursor+1)
	case "enter":
		if m.localChoose.cursor == 1 {
			// A folder first, then the same dashboard question.
			m.Overlay = OverlayNone
			m.afterLocalSetup = true
			m.openPrompt("prompt.path")
			return
		}
		m.Overlay = OverlayNone
		m.openFiles()
		// Choosing local is half the dashboard question; ask the other half now,
		// so the next screen is the panes, not an empty remote column.
		m.overlayCmd = m.openDashboard()
	}
}

// renderLocalChooser draws the card.
func (m *Model) renderLocalChooser(l layout) string {
	width := cardWidth(l, 44, 66)
	content := cardContent(width)
	rows := []struct{ label, hint string }{
		{"Browse local files", "the folder you were in"},
		{"Go to a folder…", "type a path, ~ for home"},
	}
	lines := make([]string, 0, 3+len(rows)+2)
	lines = append(lines,
		m.st.CardTitle.Render("Local files"),
		m.st.CardMuted.Render(truncate("Then choose the remote panes to sit beside it", content)),
		"",
	)
	for i, row := range rows {
		text := spread(m.st.CardText.Render(row.label), m.st.CardMuted.Render(row.hint), content)
		if i == m.localChoose.cursor {
			lines = append(lines, m.st.PaletteItemActive.Width(content).Render(padVisible(
				"▸ "+spread(row.label, row.hint, content-2), content)))
			continue
		}
		lines = append(lines, text)
	}
	lines = append(lines, "", m.st.CardMuted.Render("↑↓ choose · Enter open · Esc close"))
	return m.card(width, lines)
}
