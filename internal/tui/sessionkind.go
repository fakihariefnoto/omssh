package tui

import tea "charm.land/bubbletea/v2"

// Terminal and Tunnel ask the same first question — which host — but they are
// different jobs. The launcher has one row; this card is the fork, so a person
// chooses the job before the machine.

type sessionKindState struct{ cursor int }

func (m *Model) openSessionKind() {
	m.sessionKind = sessionKindState{}
	// The cards are the launcher's own question; keeping the file explorer behind
	// them made choosing a terminal look like it had already switched screens.
	m.screen = screenHome
	m.mode = "NORMAL"
	m.Overlay = OverlaySessionKind
}

func (m *Model) handleSessionKindKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc", "q":
		m.Overlay = OverlayNone
	case "up", "k":
		m.sessionKind.cursor = max(0, m.sessionKind.cursor-1)
	case "down", "j":
		m.sessionKind.cursor = min(1, m.sessionKind.cursor+1)
	case "enter":
		mode := "terminal"
		if m.sessionKind.cursor == 1 {
			mode = "tunnel"
		}
		m.Overlay = OverlayNone
		m.openSessionChooser(mode)
	}
}

func (m *Model) renderSessionKind(l layout) string {
	width := cardWidth(l, 52, 76)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Terminal & Tunnel"),
		m.st.CardMuted.Render(truncate("What do you want to do on a connected host?", content)),
		"",
	}
	rows := []struct{ label, hint string }{
		{"Terminal", "open a shell; embedded and recordable"},
		{"Tunnel", "forward a port through the host"},
	}
	for i, row := range rows {
		text := spread(m.st.CardText.Render(row.label), m.st.CardMuted.Render(row.hint), content)
		if i == m.sessionKind.cursor {
			lines = append(lines, m.st.PaletteItemActive.Width(content).Render(padVisible(
				"▸ "+spread(row.label, row.hint, content-2), content)))
			continue
		}
		lines = append(lines, text)
	}
	lines = append(lines, "", m.st.CardMuted.Render("↑↓ choose · Enter next · Esc close"))
	return m.card(width, lines)
}
