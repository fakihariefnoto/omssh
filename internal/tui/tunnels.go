package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	sshx "golang.org/x/crypto/ssh"

	"github.com/fakihariefnoto/omssh/internal/config"
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
	m.openTunnelPromptHost(host)
}

// tunnelFormState is the tunnel form: a host that cannot change, a spec, and the
// examples that make the spec understandable.
type tunnelFormState struct {
	host  string
	input input
	err   string
}

// openTunnelPromptHost asks for the spec for one chosen host.
func (m *Model) openTunnelPromptHost(host string) {
	if _, live := m.live[host]; !live {
		m.toastInfo("Connect to " + host + " first")
		return
	}
	m.tunnelHost = host
	m.tunnelForm = tunnelFormState{host: host}
	m.tunnelForm.input.Set("L:8080:localhost:80")
	m.Overlay = OverlayTunnelForm
}

// handleTunnelFormKey edits or submits the spec.
func (m *Model) handleTunnelFormKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc":
		m.tunnelForm = tunnelFormState{}
		m.Overlay = OverlayNone
	case "enter":
		if err := m.startTunnel(m.tunnelForm.input.Value); err != nil {
			m.tunnelForm.err = err.Error()
			return
		}
		m.tunnelForm = tunnelFormState{}
		// A tunnel is not a file: land in its own history, where Enter watches
		// the tail, instead of on a file dashboard nobody asked for.
		m.screen = screenHome
		m.mode = "NORMAL"
		m.openHistory(1)
		m.toastSuccess("Tunnel running · Enter to watch its tail")
	default:
		m.tunnelForm.err = ""
		m.tunnelForm.input.Handle(msg)
	}
}

// renderTunnelForm draws the form with the three shapes spelled out. A bare
// prompt asked beginners to know ssh's syntax before they could use the feature.
func (m *Model) renderTunnelForm(l layout) string {
	width := cardWidth(l, 66, 96)
	content := cardContent(width)
	form := m.tunnelForm
	lines := []string{
		m.st.CardTitle.Render("Tunnel through " + form.host),
		m.st.CardMuted.Render(truncate("Forward a port over the SSH connection. Nothing is captured.", content)),
		"",
	}
	examples := []struct{ spec, what string }{
		{"L 8080:db.internal:5432", "localhost:8080 reaches db.internal:5432 from the server"},
		{"R 9000:localhost:3000", "the server's port 9000 reaches your localhost:3000"},
		{"D 1080", "SOCKS5 proxy on 127.0.0.1:1080"},
	}
	for _, example := range examples {
		lines = append(lines,
			m.st.CardText.Render(truncate("  "+padRight(example.spec, 26)+example.what, content)))
	}
	lines = append(lines, "")
	label := m.st.StatusMuted.Render("spec  ")
	valueWidth := max(10, content-6)
	shown := form.input.Display()
	if m.Overlay == OverlayTunnelForm {
		shown = form.input.Rendered()
	}
	lines = append(lines,
		m.st.Accent.Bold(true).Render("▸ ")+label+
			m.st.PathInput.Width(valueWidth).Render(truncate(shown, valueWidth)),
		spaces(6)+m.st.CardMuted.Render(truncate("L:port:host:port · R:port:host:port · D:port", valueWidth)),
	)
	if form.err != "" {
		lines = append(lines, "", m.st.Danger.Render(truncate(form.err, content)))
	}
	lines = append(lines, "", m.st.CardMuted.Render("Enter start · Esc cancel"))
	return m.card(width, lines)
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
	host := m.tunnelHost
	if host == "" {
		host, _ = m.sshHostForTunnel()
	}
	if _, liveOK := m.live[host]; !liveOK {
		return fmt.Errorf("no live session on %s", host)
	}
	live := m.live[host]
	spec, err := tunnel.ParseSpec(specText)
	if err != nil {
		return err
	}
	options := tunnel.Options{Kind: spec.Kind, Listen: spec.Listen, Target: spec.Target, Host: host}
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
// loadMCPLog reads the calls agents made, newest first. It runs when the card
// opens or is refreshed, not per frame: a log is a file, and rendering is not the
// place to touch one.
func (m *Model) loadMCPLog() {
	m.mcpLog = nil
	dir, err := config.StateDir()
	if err != nil {
		return
	}
	file, err := os.Open(filepath.Join(dir, "mcp.jsonl"))
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	entries := make([]mcpLogEntry, 0, 32)
	for scanner.Scan() {
		var entry mcpLogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	// Newest first, capped: the card is a glance, not a pager.
	for i := len(entries) - 1; i >= 0 && len(m.mcpLog) < 8; i-- {
		m.mcpLog = append(m.mcpLog, entries[i])
	}
}

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
	lines := make([]string, 0, 24+strings.Count(snippet, "\n"))
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
		m.st.StatusMuted.Render(truncate("Recent agent calls", content)),
	)
	if len(m.mcpLog) == 0 {
		lines = append(lines, m.st.PaneEmpty.Render("Nothing yet · calls appear here as they happen"))
	}
	for _, entry := range m.mcpLog {
		lines = append(lines, m.st.CardText.Render(truncate(entry.label(), content)))
	}
	lines = append(lines,
		"",
		m.st.CardMuted.Render("c copy this config · r refresh · Esc close"),
	)
	return m.card(width, lines)
}
