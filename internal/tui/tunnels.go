package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
	id        int
	sessionID string
	host      string
	kind      tunnel.Kind
	listen    string
	target    string

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

// tunnelFormState is the tunnel form. It is two steps — which direction, then
// the addresses — because "L:8080:host:port" is ssh's syntax, not a person's.
type tunnelFormState struct {
	host   string
	step   int // 0: choose L/R/D, 1: fill the fields
	kind   tunnel.Kind
	cursor int // kind chooser
	fields []tunnelField
	focus  int
	err    string
}

type tunnelField struct {
	key, label, hint string
	in               input
}

// tunnelKindOptions is the first step, in the order it is shown.
var tunnelKindOptions = []struct {
	kind    tunnel.Kind
	name    string
	meaning string
}{
	{tunnel.Local, "Local", "listen here; the server dials the target"},
	{tunnel.Remote, "Remote", "listen on the server; this machine dials"},
	{tunnel.Dynamic, "Dynamic", "a SOCKS5 proxy on this machine"},
}

// openTunnelPromptHost starts at the direction question.
func (m *Model) openTunnelPromptHost(host string) {
	if _, live := m.live[host]; !live {
		m.toastInfo("Connect to " + host + " first")
		return
	}
	m.tunnelHost = host
	m.tunnelForm = tunnelFormState{host: host, kind: tunnel.Local}
	m.Overlay = OverlayTunnelForm
}

// enterTunnelFields moves to step 2 with defaults a beginner can press Enter on.
func (f *tunnelFormState) enterFields(kind tunnel.Kind) {
	f.step, f.kind, f.focus, f.err = 1, kind, 0, ""
	add := func(key, label, hint, value string) {
		field := tunnelField{key: key, label: label, hint: hint}
		field.in.Set(value)
		f.fields = append(f.fields, field)
	}
	switch kind {
	case tunnel.Local, tunnel.Bastion:
		// Bastion is the CLI's name for a local forward with metadata intent; the
		// interface only offers L, R and D, but the switch stays exhaustive.
		port := suggestPort("127.0.0.1", 8080)
		add("bind", "bind", "127.0.0.1 keeps it private; 0.0.0.0 exposes it", "127.0.0.1")
		add("port", "port", fmt.Sprintf("%d is free on this machine", port), strconv.Itoa(port))
		add("host", "target host", "resolved by the server, not by you", "localhost")
		add("port2", "target port", "the service on the target", "80")
	case tunnel.Remote:
		add("bind", "server bind", "where the server listens; loopback is private", "127.0.0.1")
		add("port", "server port", "the port to open on the server", "9000")
		add("host", "local host", "resolved on this machine", "localhost")
		add("port2", "local port", "the service here to reach", "3000")
	case tunnel.Dynamic:
		port := suggestPort("127.0.0.1", 1080)
		add("bind", "bind", "the SOCKS5 listener on this machine", "127.0.0.1")
		add("port", "port", fmt.Sprintf("%d is free; a SOCKS5 client connects here", port), strconv.Itoa(port))
	}
}

func (f tunnelFormState) value(key string) string {
	for _, field := range f.fields {
		if field.key == key {
			return strings.TrimSpace(field.in.Value)
		}
	}
	return ""
}

// spec turns the fields into what the tunnel package starts. Validation happens
// here, so an empty port is a sentence rather than a failed listener.
func (f tunnelFormState) spec() (tunnel.Spec, error) {
	bind, port := f.value("bind"), f.value("port")
	if bind == "" {
		return tunnel.Spec{}, fmt.Errorf("a bind address is required")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return tunnel.Spec{}, fmt.Errorf("port must be a number from 1 to 65535")
	}
	listen := net.JoinHostPort(bind, port)
	if f.kind == tunnel.Dynamic {
		return tunnel.Spec{Kind: tunnel.Dynamic, Listen: listen}, nil
	}
	host := f.value("host")
	if host == "" {
		return tunnel.Spec{}, fmt.Errorf("a target host is required")
	}
	targetPort, err := strconv.Atoi(f.value("port2"))
	if err != nil || targetPort < 1 || targetPort > 65535 {
		return tunnel.Spec{}, fmt.Errorf("the target port must be a number from 1 to 65535")
	}
	return tunnel.Spec{Kind: f.kind, Listen: listen,
		Target: net.JoinHostPort(host, strconv.Itoa(targetPort))}, nil
}

// checkPort is the "is it free" question before a start, for the local listener.
// A remote forward listens on the server; its collision is reported when the
// server answers.
func (f tunnelFormState) checkPort() error {
	if f.kind == tunnel.Remote {
		return nil
	}
	spec, err := f.spec()
	if err != nil {
		return err
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", spec.Listen)
	if err != nil {
		host, port, splitErr := net.SplitHostPort(spec.Listen)
		if splitErr == nil {
			if number, convErr := strconv.Atoi(port); convErr == nil {
				return fmt.Errorf("%s is in use · try %d: %w", spec.Listen, suggestPort(host, number+1), err)
			}
		}
		return fmt.Errorf("%s is not free: %w", spec.Listen, err)
	}
	return listener.Close()
}

// handleTunnelFormKey drives the two steps.
func (m *Model) handleTunnelFormKey(msg tea.KeyPressMsg) {
	form := &m.tunnelForm
	if form.step == 0 {
		switch msg.String() {
		case "esc":
			m.tunnelForm = tunnelFormState{}
			m.Overlay = OverlayNone
		case "up", "k":
			form.cursor = (form.cursor + len(tunnelKindOptions) - 1) % len(tunnelKindOptions)
		case "down", "j":
			form.cursor = (form.cursor + 1) % len(tunnelKindOptions)
		case "enter":
			form.enterFields(tunnelKindOptions[form.cursor].kind)
		}
		return
	}
	switch msg.String() {
	case "esc":
		// Back to the direction, not out of the whole card: the fields are the
		// second half of one question.
		form.step, form.err = 0, ""
		if form.cursor < 0 || form.cursor >= len(tunnelKindOptions) {
			form.cursor = 0
		}
	case "tab", "down":
		form.focus = (form.focus + 1) % len(form.fields)
	case "shift+tab", "up":
		form.focus = (form.focus - 1 + len(form.fields)) % len(form.fields)
	case "enter":
		if err := form.checkPort(); err != nil {
			form.err = err.Error()
			return
		}
		spec, err := form.spec()
		if err != nil {
			form.err = err.Error()
			return
		}
		m.tunnelHost = form.host
		if err := m.startTunnelSpec(spec); err != nil {
			form.err = err.Error()
			return
		}
		m.tunnelForm = tunnelFormState{}
		// A tunnel is not a file: show the running list, where x stops it and
		// Enter opens its history tail. The form is gone; the tunnel is not.
		m.screen = screenHome
		m.mode = "NORMAL"
		m.Overlay = OverlayTunnels
		m.tunnelCursor = len(m.tunnels) - 1
		m.toastSuccess("Tunnel running · x stops it · Enter shows history")
	default:
		form.err = ""
		form.fields[form.focus].in.Handle(msg)
	}
}

// renderTunnelForm draws the step, and the preview that says what will happen.
func (m *Model) renderTunnelForm(l layout) string {
	form := m.tunnelForm
	width := cardWidth(l, 70, 100)
	content := cardContent(width)
	alias := form.host
	via := alias
	if live, ok := m.live[form.host]; ok {
		target := live.Target
		address := target.Host
		if target.Port != 0 {
			address = fmt.Sprintf("%s:%d", address, target.Port)
		}
		if target.User != "" {
			address = target.User + "@" + address
		}
		if address != "" {
			via = fmt.Sprintf("%s · %s", alias, address)
		}
	}
	lines := []string{
		m.st.CardTitle.Render("Tunnel through " + alias),
		m.st.CardMuted.Render(truncate("via "+via+" · nothing is captured", content)),
		"",
	}
	if form.step == 0 {
		lines = append(lines, m.st.StatusMuted.Render(truncate("What should the forward do?", content)), "")
		for i, option := range tunnelKindOptions {
			text := spread(m.st.CardText.Render(option.name), m.st.CardMuted.Render(option.meaning), content-1)
			if i == form.cursor {
				lines = append(lines, m.st.PaletteItemActive.Width(content).Render(padVisible(
					"▸ "+option.name+"  "+option.meaning, content)))
				continue
			}
			lines = append(lines, "  "+text)
		}
		lines = append(lines, "", m.st.CardMuted.Render("↑↓ choose · Enter next · Esc close"))
		return m.card(width, lines)
	}

	labelWidth := 11
	for _, field := range form.fields {
		labelWidth = max(labelWidth, len(field.label))
	}
	valueWidth := max(10, content-labelWidth-2)
	for i, field := range form.fields {
		label := m.st.StatusMuted.Render(padRight(field.label, labelWidth))
		shown := field.in.Display()
		if i == form.focus {
			shown = field.in.Rendered()
		}
		value := m.st.PathInput.Width(valueWidth).Render(truncate(shown, valueWidth))
		prefix := "  "
		if i == form.focus {
			prefix = m.st.Accent.Bold(true).Render("▸ ")
		}
		lines = append(lines,
			prefix+label+" "+value,
			spaces(2+labelWidth+1)+m.st.CardMuted.Render(truncate(field.hint, valueWidth)),
		)
	}

	diagram, explanation := form.preview(alias)
	lines = append(lines, "", m.st.StatusMuted.Render("What will happen"), "")
	for _, line := range diagram {
		lines = append(lines, m.st.CardText.Render(truncate("  "+line, content)))
	}
	for _, line := range explanation {
		lines = append(lines, m.st.CardMuted.Render(truncate("  "+line, content)))
	}
	if form.err != "" {
		lines = append(lines, "", m.st.Danger.Render(truncate(form.err, content)))
	}
	footer := "Tab next · Enter start · Esc back"
	if form.kind == tunnel.Remote {
		footer = "Tab next · Enter start (port checked by the server) · Esc back"
	}
	lines = append(lines, "", m.st.CardMuted.Render(footer))
	return m.card(width, lines)
}

// preview is the simulation: where the bytes enter, where they leave, and what
// localhost means at each end.
func (f tunnelFormState) preview(alias string) (diagram, explanation []string) {
	bind, port, host, port2 := f.value("bind"), f.value("port"), f.value("host"), f.value("port2")
	local := net.JoinHostPort(valueOr(bind, "127.0.0.1"), valueOr(port, "port"))
	target := net.JoinHostPort(valueOr(host, "host"), valueOr(port2, "port"))
	switch f.kind {
	case tunnel.Remote:
		return []string{
				fmt.Sprintf("%s  ◀──SSH──  %s", target, local),
				fmt.Sprintf("on %s: %s", alias, local),
			}, []string{
				fmt.Sprintf("A program on %s connecting to %s reaches %s here.", alias, local, target),
				"localhost on the server side would mean the server; here it means this machine.",
			}
	case tunnel.Dynamic:
		return []string{
				fmt.Sprintf("SOCKS5 %s  ───SSH──▶  any target", local),
				fmt.Sprintf("through %s", alias),
			}, []string{
				fmt.Sprintf("Point a SOCKS5 client at %s on this machine.", local),
				fmt.Sprintf("%s resolves and reaches whichever target the client asks for.", alias),
				"No fixed target: that is what dynamic means.",
			}
	default:
		return []string{
				fmt.Sprintf("%s  ───SSH──▶  %s", local, target),
				fmt.Sprintf("on this machine   through %s", alias),
			}, []string{
				fmt.Sprintf("Connect to %s on this machine; the connection leaves %s for %s.",
					local, alias, target),
				"localhost in the target means the server, not your laptop.",
			}
	}
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// startTunnelSpec opens a forward from an already validated spec.
func (m *Model) startTunnelSpec(spec tunnel.Spec) error {
	host := m.tunnelHost
	if host == "" {
		host, _ = m.sshHostForTunnel()
	}
	if _, liveOK := m.live[host]; !liveOK {
		return fmt.Errorf("no live session on %s", host)
	}
	live := m.live[host]
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
	active.sessionID = started.ID()
	m.tunnels = append(m.tunnels, active)
	return nil
}

// sshDialer adapts an SSH client to tunnel.Dialer.
type sshDialer struct{ client *sshx.Client }

func (d sshDialer) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	return d.client.Dial(network, addr)
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

// openTunnels lists the running forwards, or asks for one when there are none.
func (m *Model) openTunnels() {
	if len(m.tunnels) == 0 {
		m.openTunnelPrompt()
		return
	}
	m.tunnelCursor = 0
	m.Overlay = OverlayTunnels
}

// handleTunnelsKey stops one, opens its history, or leaves the list.
func (m *Model) handleTunnelsKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc", "q":
		m.Overlay = OverlayNone
	case "up", "k":
		m.tunnelCursor = max(0, m.tunnelCursor-1)
	case "down", "j":
		m.tunnelCursor = min(max(0, len(m.tunnels)-1), m.tunnelCursor+1)
	case "enter":
		m.Overlay = OverlayNone
		m.openHistory(1)
	case "x", "d":
		if m.tunnelCursor >= 0 && m.tunnelCursor < len(m.tunnels) {
			m.stopTunnel(m.tunnelCursor)
		}
	}
}

// runningTunnel finds a live forward by the session id history records.
func (m *Model) runningTunnel(session string) *activeTunnel {
	for _, active := range m.tunnels {
		if active.sessionID == session {
			return active
		}
	}
	return nil
}

// stopTunnelBySession stops a forward selected in History or its tail.
func (m *Model) stopTunnelBySession(session string) bool {
	for i, active := range m.tunnels {
		if active.sessionID == session {
			m.stopTunnel(i)
			return true
		}
	}
	return false
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
	if len(m.tunnels) == 0 && m.Overlay == OverlayTunnels {
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

// suggestPort returns the first free port at or after preferred, so the form can
// offer something that will actually start. It probes the local bind only; a
// remote listen is the server's answer to give.
func suggestPort(bind string, preferred int) int {
	var listenConfig net.ListenConfig
	for port := preferred; port < preferred+50; port++ {
		listener, err := listenConfig.Listen(context.Background(), "tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		port = listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		return port
	}
	listener, err := listenConfig.Listen(context.Background(), "tcp", net.JoinHostPort(bind, "0"))
	if err != nil {
		return preferred
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
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
	lines = append(lines, m.st.CardMuted.Render("↑↓ choose · x stop · Enter history · Esc close (tunnels keep running)"))
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
