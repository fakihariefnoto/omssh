package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// A chooser for places that only exist once a connection does. The launcher can
// offer "Terminal" and "Tunnel" without pretending they work before anything is
// connected; this is the question in between, and it is the same list the
// dashboard chooser uses — the sessions that are actually live.

// sessionChooseState is the chooser's cursor and what picking one means.
type sessionChooseState struct {
	mode   string // "terminal" or "tunnel"
	cursor int
}

// liveSSHHosts is the connected hosts, in tab order so the list is stable.
func (m *Model) liveSSHHosts() []string {
	seen := make(map[string]bool, len(m.live))
	hosts := make([]string, 0, len(m.live))
	for _, tab := range m.tabs {
		if tab.Host == "" || isCloudHost(tab.Host) || seen[tab.Host] {
			continue
		}
		if _, ok := m.live[tab.Host]; !ok {
			continue
		}
		seen[tab.Host] = true
		hosts = append(hosts, tab.Host)
	}
	// A live session that has no tab is still reachable; sort those in.
	rest := make([]string, 0, len(m.live))
	for host := range m.live {
		if seen[host] || isCloudHost(host) {
			continue
		}
		rest = append(rest, host)
	}
	sort.Strings(rest)
	return append(hosts, rest...)
}

// openSessionChooser asks which connected host a terminal or a tunnel is for.
func (m *Model) openSessionChooser(mode string) {
	if len(m.liveSSHHosts()) == 0 {
		m.toastInfo("Connect to a host first · Ctrl+O manages connections")
		return
	}
	m.sessionChoose = sessionChooseState{mode: mode}
	m.Overlay = OverlaySessionChoose
}

// sessionChooserRows is the hosts plus the two ways out: watch running tunnels
// when there are any, and go add a connection when there are not enough.
func (m *Model) sessionChooserRows() []string {
	rows := m.liveSSHHosts()
	if m.sessionChoose.mode == "tunnel" && len(m.tunnels) > 0 {
		rows = append(rows, "\x00tunnels")
	}
	return append(rows, "\x00connections")
}

// handleSessionChooserKey picks a row. It returns a command only for the external
// terminal, which hands the terminal over.
func (m *Model) handleSessionChooserKey(msg tea.KeyPressMsg) tea.Cmd {
	rows := m.sessionChooserRows()
	switch msg.String() {
	case "esc", "q":
		m.Overlay = OverlayNone
	case "up", "k":
		m.sessionChoose.cursor = max(0, m.sessionChoose.cursor-1)
	case "down", "j":
		m.sessionChoose.cursor = min(max(0, len(rows)-1), m.sessionChoose.cursor+1)
	case "enter", "t":
		if m.sessionChoose.cursor < 0 || m.sessionChoose.cursor >= len(rows) {
			return nil
		}
		row := rows[m.sessionChoose.cursor]
		switch row {
		case "\x00tunnels":
			m.Overlay = OverlayTunnels
			m.tunnelCursor = 0
			return nil
		case "\x00connections":
			m.Overlay = OverlayNone
			return m.runAction("session.open")
		}
		m.Overlay = OverlayNone
		if m.sessionChoose.mode == "tunnel" {
			m.openTunnelPromptHost(row)
			return nil
		}
		if msg.String() == "t" {
			// t keeps ssh's own terminal: the key that always worked.
			return m.openTerminal(row)
		}
		return m.openEmbeddedHost(row)
	}
	return nil
}

// renderSessionChooser draws the list.
func (m *Model) renderSessionChooser(l layout) string {
	width := cardWidth(l, 52, 76)
	content := cardContent(width)
	title := "Terminal"
	verb := "Open"
	if m.sessionChoose.mode == "tunnel" {
		title = "Tunnel"
		verb = "Forward on"
	}
	lines := []string{
		m.st.CardTitle.Render(title),
		m.st.CardMuted.Render(truncate("Choose a connected host · "+verb+" it", content)),
		"",
	}
	rows := m.sessionChooserRows()
	for i, row := range rows {
		label := row
		hint := "connected"
		switch row {
		case "\x00tunnels":
			label, hint = "Running tunnels", fmt.Sprintf("%d · watch", len(m.tunnels))
		case "\x00connections":
			label, hint = "Manage connections…", "Ctrl+O"
		}
		text := fmt.Sprintf("%-24s %s", truncate(label, 24), hint)
		if i == m.sessionChoose.cursor {
			lines = append(lines, m.st.Accent.Bold(true).Render(truncate("▸ "+text, content)))
			continue
		}
		lines = append(lines, m.st.CardText.Render(truncate("  "+text, content)))
	}
	footer := "↑↓ choose · Enter open · Esc close"
	if m.sessionChoose.mode == "terminal" {
		footer = "↑↓ choose · Enter embedded · t external ssh · Esc close"
	}
	lines = append(lines, "", m.st.CardMuted.Render(footer))
	return m.card(width, lines)
}

// mcpLogEntry is one call an agent made, as the card shows it.
type mcpLogEntry struct {
	Time  string `json:"time"`
	Tool  string `json:"tool"`
	Place string `json:"place"`
	Path  string `json:"path"`
	OK    bool   `json:"ok"`
}

func (e mcpLogEntry) label() string {
	where := strings.TrimSpace(e.Place + ":" + e.Path)
	if where == ":" || where == "" {
		where = e.Tool
	}
	mark := "ok"
	if !e.OK {
		mark = "failed"
	}
	return fmt.Sprintf("%s  %s  %s  %s", e.Time, mark, e.Tool, where)
}
