package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// Four panes, two by two, is the dashboard's promise: four machines in one view.
// With more sessions than that, which four is a choice rather than a guess, so
// the choice is a screen: the local machine and every connected host, ticked.
//
// The rest of the tabs stay: a session that is not on screen is still open, and
// Tab brings it to a pane when you ask for it.

// panesState is the pane chooser's state.
type panesState struct {
	cursor int
	chosen map[string]bool
}

// paneChoices is the list the chooser offers: local first, then the connected
// hosts in tab order, so the list matches the strip above it.
func (m *Model) paneChoices() []string {
	choices := []string{""} // the empty alias is the local pane
	seen := map[string]bool{}
	for _, tab := range m.tabs {
		if tab.Host == "" || seen[tab.Host] {
			continue
		}
		seen[tab.Host] = true
		choices = append(choices, tab.Host)
	}
	return choices
}

// openPanes shows the chooser with what is on screen already ticked.
func (m *Model) openPanes() {
	chosen := map[string]bool{"": true} // the local pane is always one of them
	for i := 0; i < m.paneCount(); i++ {
		if host := m.Panes[i].Host; host != "" {
			chosen[host] = true
		}
	}
	m.panes = panesState{chosen: chosen}
	m.Overlay = OverlayPanes
}

// handlePanesKey drives the chooser: space ticks, Enter applies, Escape keeps
// what was there.
func (m *Model) handlePanesKey(msg tea.KeyPressMsg) {
	choices := m.paneChoices()
	switch msg.String() {
	case "esc":
		m.Overlay = OverlayNone
	case "up", "k":
		m.panes.cursor = (m.panes.cursor + len(choices) - 1) % len(choices)
	case "down", "j":
		m.panes.cursor = (m.panes.cursor + 1) % len(choices)
	case "space", "x":
		alias := choices[clamp(m.panes.cursor, 0, len(choices)-1)]
		if alias == "" {
			m.toastInfo("The local pane stays: it is where you started")
			break
		}
		if m.panes.chosen[alias] {
			delete(m.panes.chosen, alias)
			break
		}
		if len(m.panes.chosen) >= maxPanes {
			m.toastInfo("Four panes is the limit · untick one first")
			break
		}
		m.panes.chosen[alias] = true
	case "enter":
		m.applyPanes()
	}
}

// applyPanes rebuilds the pane list from the ticks, keeping the order the list
// showed.
func (m *Model) applyPanes() {
	m.panesAsked = true
	chosen := m.panes.chosen
	panes := make([]Pane, 0, maxPanes)
	local := NewPane(m.Panes[0].FS, m.Panes[0].Path, m.Panes[0].Home)
	if m.Panes[0].FS == nil {
		local = m.Panes[0]
	}
	panes = append(panes, local)

	for _, alias := range m.paneChoices() {
		if alias == "" || !chosen[alias] {
			continue
		}
		sess, live := m.live[alias]
		if !live {
			continue
		}
		pane := NewRemotePane()
		pane.Bind(sess.FS, sess.Target.Alias, sess.Root)
		pane.Base = sess.Base
		panes = append(panes, pane)
	}
	if len(panes) == 0 {
		panes = append(panes, m.Panes[0])
	}
	m.Panes = panes
	m.Focused = min(m.Focused, len(m.Panes)-1)
	m.syncActiveTab()
	m.Overlay = OverlayNone
	m.mode = "NORMAL"
	m.toastInfo(fmt.Sprintf("%d %s on screen · w moves between them",
		len(m.Panes), plural(len(m.Panes), "pane", "panes")))

	// Every pane needs its listing: they were rebuilt, not moved.
	for i := range m.Panes {
		m.loadPaneAsync(i)
	}
}

// renderPanesChooser draws the tick list.
func (m *Model) renderPanesChooser(l layout) string {
	width := cardWidth(l, 46, 68)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Panes"),
		m.st.CardMuted.Render("Space ticks · Enter shows · Esc keeps"),
		"",
	}

	choices := m.paneChoices()
	for i, alias := range choices {
		label := alias
		if label == "" {
			label = "local machine"
		}
		tick := "[ ]"
		if m.panes.chosen[alias] {
			tick = "[x]"
		}
		if alias == "" {
			tick = "[x]" // the local pane is not optional
		}
		note := ""
		if m.paneShowsOnScreen(alias) {
			note = "  on screen"
		}
		row := padRight(fmt.Sprintf("%s %s%s", tick, truncate(label, content-18), note), content)
		if i == m.panes.cursor {
			lines = append(lines, m.st.PaletteItemActive.Render(row))
			continue
		}
		lines = append(lines, m.st.PaletteItem.Render(row))
	}
	if len(choices) == 1 {
		lines = append(lines, "", m.st.PaneEmpty.Render("No sessions yet · Ctrl+O connects"))
	}
	lines = append(lines, "",
		m.st.CardMuted.Render(fmt.Sprintf("%d of %d panes chosen",
			len(m.panes.chosen), maxPanes)))
	return m.card(width, lines)
}

// paneShowsOnScreen reports whether a host is in one of the panes now.
func (m *Model) paneShowsOnScreen(alias string) bool {
	for i := 0; i < m.paneCount(); i++ {
		host := m.Panes[i].Host
		if alias == "" && host == "" && m.Panes[i].Side == SideLocal {
			return true
		}
		if alias != "" && host == alias {
			return true
		}
	}
	return false
}
