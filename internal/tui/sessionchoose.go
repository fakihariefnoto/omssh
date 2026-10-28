package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
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

// openSessionChooser asks which host a terminal or a tunnel is for. Connected
// hosts are opened; the rest are connected on the way, so the chooser is also the
// answer to "I have not connected yet".
func (m *Model) openSessionChooser(mode string) {
	m.sessionChoose = sessionChooseState{mode: mode}
	// Read ~/.ssh/config once, when the chooser opens; rendering runs on every
	// frame and must not touch the disk.
	m.loadConnections()
	m.Overlay = OverlaySessionChoose
}

// sessionRow is one line of the chooser. A live host is opened; an offline one is
// connected first, which is what makes this the launcher's answer to "combine the
// chooser with adding a connection".
type sessionRow struct {
	key   string // host alias, or a sentinel
	label string
	hint  string
	live  bool
}

const (
	rowTunnels     = "\x00tunnels"
	rowConnections = "\x00connections"
)

// sessionChooserRows is live sessions first, then the hosts in ~/.ssh/config that
// could be connected, plus the ways out.
func (m *Model) sessionChooserRows() []sessionRow {
	rows := make([]sessionRow, 0, len(m.live)+8)
	if m.sessionChoose.mode == "tunnel" && len(m.tunnels) > 0 {
		rows = append(rows, sessionRow{key: rowTunnels, label: "Running tunnels",
			hint: fmt.Sprintf("%d · watch", len(m.tunnels))})
	}
	live := make(map[string]bool, len(m.live))
	for _, host := range m.liveSSHHosts() {
		live[host] = true
		rows = append(rows, sessionRow{key: host, label: host, hint: "connected", live: true})
	}
	// The connections manager already knows how to list hosts and which ones are
	// not for browsing; reuse that answer.
	for _, entry := range m.visibleConnections() {
		if entry.Cloud || live[entry.Alias] {
			continue
		}
		rows = append(rows, sessionRow{key: entry.Alias, label: entry.Alias,
			hint: "not connected · Enter connects"})
	}
	return append(rows, sessionRow{key: rowConnections, label: "Manage connections…", hint: "Ctrl+O"})
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
		switch row.key {
		case rowTunnels:
			m.Overlay = OverlayTunnels
			m.tunnelCursor = 0
			return nil
		case rowConnections:
			m.Overlay = OverlayNone
			return m.runAction("session.open")
		}
		m.Overlay = OverlayNone
		if msg.String() == "t" {
			// t keeps ssh's own terminal; it works whether or not the session is
			// open here, because ssh does its own dialling.
			return m.openTerminal(row.key)
		}
		if !row.live {
			return m.connectForChooser(row.key)
		}
		if m.sessionChoose.mode == "tunnel" {
			m.openTunnelPromptHost(row.key)
			return nil
		}
		return m.openEmbeddedHost(row.key)
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
		text := fmt.Sprintf("%-24s %s", truncate(row.label, 24), row.hint)
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

// connectForChooser connects a host the chooser picked and remembers what to do
// when it lands. The connection itself is the normal path, so prompts, host keys
// and errors are the ones the rest of the interface uses.
func (m *Model) connectForChooser(host string) tea.Cmd {
	var entry *connEntry
	for i := range m.conn.entries {
		if m.conn.entries[i].Alias == host {
			entry = &m.conn.entries[i]
			break
		}
	}
	if entry == nil || entry.Cloud {
		m.toastError("No SSH host named " + host)
		return nil
	}
	m.pendingAfterConnect = m.sessionChoose.mode
	m.pendingHost = host
	target, err := ssh.TargetFrom(sshconfig.Spec{
		Alias: entry.Alias, HostName: entry.HostName, User: entry.User, Port: entry.Port,
		IdentityFile: entry.Identity, ProxyJump: entry.ProxyJump,
	})
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	return m.connect(target)
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
