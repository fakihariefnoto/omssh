package tui

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	sshx "golang.org/x/crypto/ssh"

	"github.com/fakihariefnoto/omssh/internal/tunnel"
)

// Tunnels are long-lived and invisible by nature, so the interface gives them a
// list: what is listening, where it goes, and how much has moved. Starting one is
// a prompt; watching and stopping one is a card.

// activeTunnel is one running forward and its counters. The callbacks run on the
// tunnel's goroutines, so the numbers are behind a mutex and rendering only reads.
type activeTunnel struct {
	id     int
	host   string
	kind   tunnel.Kind
	listen string
	target string

	mu       sync.Mutex
	tunnel   *tunnel.Tunnel
	conns    int
	bytesIn  int64
	bytesOut int64
	err      string
}

func (t *activeTunnel) observe(event tunnel.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if event.Client != "" {
		if event.Error != "" {
			t.err = event.Error
			return
		}
		t.conns++
		t.bytesIn += event.BytesIn
		t.bytesOut += event.BytesOut
		return
	}
	if event.Error != "" {
		t.err = event.Error
	}
}

func (t *activeTunnel) snapshot() (conns int, in, out int64, errText string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conns, t.bytesIn, t.bytesOut, t.err
}

// openTunnelPrompt asks for a spec, showing the three shapes in the hint.
func (m *Model) openTunnelPrompt() {
	host, live := m.sshHostForTunnel()
	if !live {
		m.toastInfo("Open a host first · tunnels run over a live session")
		return
	}
	m.prompt = promptState{
		Title:  "Tunnel on " + host,
		Hint:   "L:8080:host:port · R:9000:host:port · D:1080 (L is assumed)",
		Action: "prompt.tunnel",
		open:   true,
	}
	m.prompt.input.Set("L:8080:localhost:80")
	m.Overlay = OverlayPrompt
}

// sshHostForTunnel finds the live session the focused pane or tab belongs to.
func (m *Model) sshHostForTunnel() (string, bool) {
	host := m.Pane().Host
	if host == "" && m.activeTab >= 0 && m.activeTab < len(m.tabs) {
		host = m.tabs[m.activeTab].Host
	}
	if host == "" {
		return "", false
	}
	_, live := m.live[host]
	return host, live
}

// startTunnel parses a spec and opens the forward. It returns an error message
// rather than reporting success for a port it could not bind.
func (m *Model) startTunnel(specText string) error {
	host, liveOK := m.sshHostForTunnel()
	if !liveOK {
		return fmt.Errorf("no live session on %s", host)
	}
	live := m.live[host]
	spec, err := tunnel.ParseSpec(specText)
	if err != nil {
		return err
	}
	options := tunnel.Options{Kind: spec.Kind, Listen: spec.Listen, Target: spec.Target}
	active := &activeTunnel{host: host, kind: spec.Kind, listen: spec.Listen, target: spec.Target}
	options.OnEvent = active.observe
	if spec.Kind == tunnel.Remote {
		options.Remote = live.Client.SSH()
	} else {
		options.Dialer = sshDialer{client: live.Client.SSH()}
	}
	started, err := tunnel.Start(context.Background(), options)
	if err != nil {
		return err
	}
	m.tunnelSeq++
	active.id, active.tunnel = m.tunnelSeq, started
	m.tunnels = append(m.tunnels, active)
	return nil
}

// sshDialer adapts an SSH client to tunnel.Dialer. x/crypto's Dial has no context,
// which is fine: the tunnel's context ends the listener, and a dial in flight is
// bounded by SSH's own timeouts.
type sshDialer struct{ client *sshx.Client }

func (d sshDialer) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	return d.client.Dial(network, addr)
}

// handleTunnelPrompt starts what was typed.
func (m *Model) handleTunnelPrompt(value string) {
	if err := m.startTunnel(value); err != nil {
		m.toastError(err.Error())
		return
	}
	m.Overlay = OverlayTunnels
}

// openTunnels lists the running forwards.
func (m *Model) openTunnels() {
	if len(m.tunnels) == 0 {
		m.openTunnelPrompt()
		return
	}
	m.tunnelCursor = 0
	m.Overlay = OverlayTunnels
}

// handleTunnelsKey stops one or leaves the list. Tunnels keep running when the
// card closes: it is a watcher, not the tunnel's owner.
func (m *Model) handleTunnelsKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc", "q":
		m.Overlay = OverlayNone
	case "up", "k":
		m.tunnelCursor = max(0, m.tunnelCursor-1)
	case "down", "j":
		m.tunnelCursor = min(max(0, len(m.tunnels)-1), m.tunnelCursor+1)
	case "x", "d", "enter":
		if m.tunnelCursor >= 0 && m.tunnelCursor < len(m.tunnels) {
			m.stopTunnel(m.tunnelCursor)
		}
	}
}

// stopTunnel closes one and drops it from the list.
func (m *Model) stopTunnel(index int) {
	if index < 0 || index >= len(m.tunnels) {
		return
	}
	active := m.tunnels[index]
	if active.tunnel != nil {
		_ = active.tunnel.Close()
	}
	m.tunnels = append(m.tunnels[:index], m.tunnels[index+1:]...)
	m.tunnelCursor = clamp(m.tunnelCursor, 0, max(0, len(m.tunnels)-1))
	m.toastInfo(fmt.Sprintf("Stopped %s → %s", active.listen, active.target))
	if len(m.tunnels) == 0 {
		m.Overlay = OverlayNone
	}
}

// closeTunnels stops everything, for a clean quit.
func (m *Model) closeTunnels() {
	for _, active := range m.tunnels {
		if active.tunnel != nil {
			_ = active.tunnel.Close()
		}
	}
	m.tunnels = nil
}

// renderTunnels is the watcher: every forward, its counters, and its errors.
func (m *Model) renderTunnels(l layout) string {
	width := cardWidth(l, 60, 100)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Tunnels"),
		m.st.CardMuted.Render(truncate(fmt.Sprintf("%d running · they keep running when this closes", len(m.tunnels)), content)),
		"",
	}
	for i, active := range m.tunnels {
		conns, in, out, errText := active.snapshot()
		arrow := "→"
		if active.kind == tunnel.Remote {
			arrow = "←"
		}
		label := fmt.Sprintf("%s %s %s %s", active.kind, active.listen, arrow, active.target)
		counters := fmt.Sprintf("%d %s · %s in · %s out", conns, plural(conns, "conn", "conns"),
			humanSize(in), humanSize(out))
		row := spread(m.st.CardText.Render(truncate(label, content-2)), m.st.CardMuted.Render(counters), content)
		if i == m.tunnelCursor {
			row = spread(m.st.Accent.Bold(true).Render("▸ "+truncate(label, content-2)),
				m.st.CardMuted.Render(counters), content)
		}
		lines = append(lines, row)
		if errText != "" {
			lines = append(lines, m.st.Danger.Render(truncate("  "+errText, content)))
		}
		lines = append(lines, m.st.CardMuted.Render(truncate("  "+active.host, content)), "")
	}
	if len(m.tunnels) == 0 {
		lines = append(lines, m.st.PaneEmpty.Render("Nothing running"))
	}
	lines = append(lines, m.st.CardMuted.Render("↑↓ choose · x stop · Esc close"))
	return m.card(width, lines)
}

// mcpCard explains how to point an agent at this program. The server itself is a
// stdio process: it cannot live inside the interface, so the card is the setup.
// mcpConfigSnippet is the configuration an MCP client pastes, with the flags
// that make the write surface explicit rather than implied.
func mcpConfigSnippet() string {
	return `{
  "mcpServers": {
    "omssh": {
      "command": "omssh",
      "args": ["mcp", "--allow-write",
               "--allow-host", "prod",
               "--allow-path", "/srv/app"]
    }
  }
}`
}

func (m *Model) renderMCP(l layout) string {
	width := cardWidth(l, 62, 96)
	content := cardContent(width)
	snippet := mcpConfigSnippet()
	lines := make([]string, 0, 16+strings.Count(snippet, "\n"))
	lines = append(lines,
		m.st.CardTitle.Render("MCP · for AI agents"),
		m.st.CardMuted.Render(truncate("A stdio server, started by the agent, not a listener.", content)),
		"",
	)
	for _, line := range strings.Split(snippet, "\n") {
		lines = append(lines, m.st.CardText.Render(truncate(line, content)))
	}
	lines = append(lines,
		"",
		m.st.CardMuted.Render(truncate("Reads are free. Writes need --allow-write; remove it to be read-only.", content)),
		m.st.CardMuted.Render(truncate("--allow-host and --allow-path narrow what is reachable. rm is a plan", content)),
		m.st.CardMuted.Render(truncate("unless the run also has --yes.", content)),
		"",
		m.st.CardMuted.Render("c copy this config · Esc close"),
	)
	return m.card(width, lines)
}
