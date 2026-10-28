package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// Choosing a host for a tunnel should show what already exists there: the
// forwards running on it and the ones saved for it. Starting a new one is a row,
// not the only possibility.

type tunnelHostState struct {
	host   string
	cursor int
}

type tunnelHostRow struct {
	kind  string // new, running, saved, back
	name  string // profile name for saved
	label string
	meta  string
}

// describeTunnel is the label both the per-host menu and the saved list use: the
// two ends, named by which machine they are on.
func describeTunnel(profile config.TunnelProfile) string {
	kind := strings.ToLower(profile.Kind)
	switch kind {
	case "remote":
		return "server " + profile.Listen + " → local " + profile.Target
	case "dynamic":
		return "local SOCKS5 " + profile.Listen + " → server chooses"
	default:
		return "local " + profile.Listen + " → server " + profile.Target
	}
}

// hostSavedTunnels is the profiles for one host, by name.
func (m *Model) hostSavedTunnels(host string) []string {
	names := make([]string, 0, len(m.cfg.Tunnels.Profiles))
	for name, profile := range m.cfg.Tunnels.Profiles {
		if profile.Host == host {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// hostRunningTunnels is how many forwards are live on one host.
func (m *Model) hostRunningTunnels(host string) int {
	count := 0
	for _, active := range m.tunnels {
		if active.host == host {
			count++
		}
	}
	return count
}

// hasTunnelChoices reports whether a host menu would have anything beyond "new".
func (m *Model) hasTunnelChoices(host string) bool {
	return len(m.hostSavedTunnels(host)) > 0 || m.hostRunningTunnels(host) > 0
}

func (m *Model) tunnelHostRows(host string) []tunnelHostRow {
	rows := []tunnelHostRow{{kind: "new", label: "New tunnel…", meta: "choose direction and ports"}}
	if running := m.hostRunningTunnels(host); running > 0 {
		rows = append(rows, tunnelHostRow{kind: "running", label: "Running tunnels",
			meta: fmt.Sprintf("%d on %s · live counters", running, host)})
	}
	for _, name := range m.hostSavedTunnels(host) {
		profile := m.cfg.Tunnels.Profiles[name]
		rows = append(rows, tunnelHostRow{kind: "saved", name: name,
			label: name, meta: describeTunnel(profile)})
	}
	return append(rows, tunnelHostRow{kind: "back", label: "Back to hosts", meta: "Esc"})
}

// openTunnelHostMenu shows the per-host list.
func (m *Model) openTunnelHostMenu(host string) {
	m.tunnelHostMenu = tunnelHostState{host: host}
	m.Overlay = OverlayTunnelHost
}

// openTunnelForHost is the chooser's decision: a host with saved or running
// tunnels gets the menu, an empty one goes straight to the form.
func (m *Model) openTunnelForHost(host string) {
	if m.hasTunnelChoices(host) {
		m.openTunnelHostMenu(host)
		return
	}
	m.openTunnelPromptHost(host)
}

func (m *Model) handleTunnelHostKey(msg tea.KeyPressMsg) {
	host := m.tunnelHostMenu.host
	rows := m.tunnelHostRows(host)
	cursor := clamp(m.tunnelHostMenu.cursor, 0, max(0, len(rows)-1))
	m.tunnelHostMenu.cursor = cursor
	row := rows[cursor]
	switch msg.String() {
	case "esc", "q":
		m.openSessionChooser("tunnel")
	case "up", "k":
		m.tunnelHostMenu.cursor = max(0, cursor-1)
	case "down", "j":
		m.tunnelHostMenu.cursor = min(max(0, len(rows)-1), cursor+1)
	case "enter":
		switch row.kind {
		case "new":
			m.openTunnelPromptHost(host)
		case "running":
			m.openHostTunnelDashboard(host)
		case "saved":
			m.startSavedTunnel(row.name)
		case "back":
			m.openSessionChooser("tunnel")
		}
	case "e":
		if row.kind == "saved" {
			m.tunnelForm = profileForm(row.name, m.cfg.Tunnels.Profiles[row.name])
			m.tunnelHost = host
			m.Overlay = OverlayTunnelForm
		}
	case "x", "d":
		if row.kind == "saved" {
			m.confirm = confirmState{Title: "Delete saved tunnel?",
				Body:   []string{row.name + " will be removed from config.yaml.", "Nothing is stopped; running tunnels are separate."},
				Danger: true, Action: "tunnel.delete-saved", Alias: row.name}
			m.confirm.open = true
			m.Overlay = OverlayConfirm
		}
	}
}

// openHostTunnelDashboard opens the live screen on the first running tunnel for
// a host.
func (m *Model) openHostTunnelDashboard(host string) {
	for i, active := range m.tunnels {
		if active.host == host {
			m.openTunnelDashboard(i)
			return
		}
	}
	m.toastInfo("No tunnels running on " + host)
}

func (m *Model) renderTunnelHost(l layout) string {
	host := m.tunnelHostMenu.host
	width := cardWidth(l, 64, 100)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Tunnel · " + host),
		m.st.CardMuted.Render(truncate("What do you want on this host?", content)),
		"",
	}
	rows := m.tunnelHostRows(host)
	for i, row := range rows {
		label := row.label
		meta := row.meta
		switch row.kind {
		case "saved":
			// Name on one line, the two ends under it: the label is what makes
			// "ovh: 0.0.0.0:9000 server → 127.0.0.1:8080 local" readable.
			if i == m.tunnelHostMenu.cursor {
				lines = append(lines,
					m.st.PaletteItemActive.Width(content).Render(padVisible("▸ "+label, content)),
					m.st.CardMuted.Render(truncate("  "+meta, content)))
				continue
			}
			lines = append(lines, "  "+m.st.CardText.Render(truncate(label, content-2)),
				m.st.CardMuted.Render(truncate("  "+meta, content)))
			continue
		}
		text := fmt.Sprintf("%-22s %s", truncate(label, 22), meta)
		if i == m.tunnelHostMenu.cursor {
			lines = append(lines, m.st.PaletteItemActive.Width(content).Render(padVisible("▸ "+text, content)))
			continue
		}
		lines = append(lines, "  "+m.st.CardText.Render(truncate(text, content-2)))
	}
	footer := "↑↓ choose · Enter open · e edit saved · x delete saved · Esc hosts"
	lines = append(lines, "", m.st.CardMuted.Render(truncate(footer, content)))
	return m.card(width, lines)
}

// hostTunnelStatus is the hint the host chooser shows when a host has saved or
// running forwards.
func (m *Model) hostTunnelStatus(host string) string {
	parts := make([]string, 0, 2)
	if running := m.hostRunningTunnels(host); running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	if saved := len(m.hostSavedTunnels(host)); saved > 0 {
		parts = append(parts, fmt.Sprintf("%d saved", saved))
	}
	if len(parts) == 0 {
		return "connected"
	}
	return "connected · " + strings.Join(parts, " · ")
}
