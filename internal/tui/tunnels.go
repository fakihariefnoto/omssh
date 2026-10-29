package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	events   []tunnel.Event
	// savedAs is the config profile this forward is stored under.
	savedAs string
	// open tracks per-client cumulative bytes from progress reports, so totals
	// only add what is new and a connection is counted once.
	open map[string][2]int64
}

func (t *activeTunnel) observe(event tunnel.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if event.Client != "" {
		_, seen := t.open[event.Client]
		if event.Error != "" {
			t.err = event.Error
			delete(t.open, event.Client)
			return
		}
		if !seen {
			t.conns++
		}
		t.addDeltaLocked(event.Client, event.BytesIn, event.BytesOut)
		delete(t.open, event.Client)
		t.appendEventLocked(event)
		return
	}
	t.appendEventLocked(event)
	if event.Error != "" {
		t.err = event.Error
	}
}

// progress folds a running connection's totals into the tunnel's, adding only
// the difference from the last report.
func (t *activeTunnel) progress(event tunnel.Event) {
	if event.Client == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, seen := t.open[event.Client]; !seen {
		t.conns++
	}
	t.addDeltaLocked(event.Client, event.BytesIn, event.BytesOut)
}

func (t *activeTunnel) addDeltaLocked(client string, in, out int64) {
	last := t.open[client]
	deltaIn, deltaOut := in-last[0], out-last[1]
	if deltaIn < 0 {
		deltaIn = in
	}
	if deltaOut < 0 {
		deltaOut = out
	}
	t.bytesIn += deltaIn
	t.bytesOut += deltaOut
	t.open[client] = [2]int64{in, out}
}

func (t *activeTunnel) appendEventLocked(event tunnel.Event) {
	// A short tail is enough to see what the tunnel is doing; the history file
	// keeps everything.
	t.events = append(t.events, event)
	if len(t.events) > 100 {
		t.events = t.events[len(t.events)-100:]
	}
}

// activeConnections is a snapshot of connections currently open.
func (t *activeTunnel) activeConnections() map[string][2]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string][2]int64, len(t.open))
	for client, counts := range t.open {
		out[client] = counts
	}
	return out
}

func (t *activeTunnel) snapshot() (conns int, in, out int64, errText string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conns, t.bytesIn, t.bytesOut, t.err
}

// tail returns the recent events for the live dashboard.
func (t *activeTunnel) tail() []tunnel.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]tunnel.Event, len(t.events))
	copy(out, t.events)
	return out
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
	saved  string // name of the saved profile being edited
	step   int    // 0: choose L/R/D, 1: fill the fields
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
	{tunnel.Local, "Local forwarding",
		"Redirects traffic from your local machine to a remote server."},
	{tunnel.Remote, "Remote forwarding",
		"Redirects traffic from a remote server back to your local machine."},
	{tunnel.Dynamic, "Dynamic forwarding",
		"Creates a local SOCKS proxy to route all traffic through the SSH server."},
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
		add("bind", "local bind", "127.0.0.1 keeps it private; 0.0.0.0 exposes it", "127.0.0.1")
		add("port", "local port", fmt.Sprintf("%d is free on this machine", port), strconv.Itoa(port))
		add("host", "target host", "resolved by the server, not by you", "localhost")
		add("port2", "target port", "the service on the target", "80")
	case tunnel.Remote:
		add("bind", "server bind", "127.0.0.1 is server-only; 0.0.0.0 needs GatewayPorts", "127.0.0.1")
		add("port", "server port", "the public side; the port to reach on the server", "9000")
		add("host", "local host", "resolved on this machine", "localhost")
		add("port2", "local port", "the app on this machine to reach", "3000")
	case tunnel.Dynamic:
		port := suggestPort("127.0.0.1", 1080)
		add("bind", "local bind", "the SOCKS5 listener on this machine", "127.0.0.1")
		add("port", "local port", fmt.Sprintf("%d is free; a SOCKS5 client connects here", port), strconv.Itoa(port))
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
	case "s":
		defaultName := form.saved
		if defaultName == "" {
			defaultName = fmt.Sprintf("%s-%s-%s", form.host, form.kind, valueOr(form.value("port"), "port"))
		}
		m.prompt = promptState{Title: "Save tunnel", Hint: "A name for this saved configuration", Action: "prompt.tunnel-save", open: true}
		m.prompt.input.Set(defaultName)
		m.Overlay = OverlayPrompt
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
		if form.saved != "" {
			// Editing a saved tunnel and starting it also keeps the edit.
			if err := m.saveTunnelProfile(form.saved, *form); err != nil {
				form.err = err.Error()
				return
			}
		}
		m.tunnelHost = form.host
		if err := m.startTunnelSpec(spec); err != nil {
			form.err = err.Error()
			return
		}
		m.tunnelForm = tunnelFormState{}
		// A tunnel is not a file: show the running list, where x stops it and
		// Enter opens its history tail. The form is gone; the tunnel is not.
		m.openTunnelDashboard(len(m.tunnels) - 1)
		m.toastSuccess("Tunnel running · x stops it · p uses another port")
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
	title := "Tunnel through " + alias
	if form.saved != "" {
		title = "Edit tunnel · " + form.saved
	}
	lines := []string{
		m.st.CardTitle.Render(title),
		m.st.CardMuted.Render(truncate("via "+via+" · nothing is captured", content)),
		"",
	}
	if form.step == 0 {
		lines = append(lines, m.st.StatusMuted.Render(truncate("What should the forward do?", content)), "")
		for i, option := range tunnelKindOptions {
			if i == form.cursor {
				lines = append(lines,
					m.st.PaletteItemActive.Width(content).Render(padVisible("▸ "+option.name, content)),
					m.st.CardMuted.Render(truncate("  "+option.meaning, content)))
				continue
			}
			lines = append(lines,
				"  "+m.st.CardText.Render(option.name),
				m.st.CardMuted.Render(truncate("  "+option.meaning, content)))
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
	footer := "Tab next · Enter start · s save · Esc back"
	if form.kind == tunnel.Remote {
		footer = "Tab next · Enter start (server-side port) · s save · Esc back"
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
		lines := []string{
			fmt.Sprintf("%s  ◀──SSH──  %s", target, local),
			fmt.Sprintf("on %s: %s", alias, local),
		}
		explanation := []string{
			fmt.Sprintf("A program on %s connecting to %s reaches %s here.", alias, local, target),
			"localhost on the server side would mean the server; here it means this machine.",
		}
		if bind == "127.0.0.1" || bind == "localhost" || bind == "::1" {
			explanation = append(explanation,
				"Only the server itself can reach that address. For a public IP, bind 0.0.0.0.")
		} else {
			explanation = append(explanation,
				"The server must allow it: sshd GatewayPorts clientspecified (or yes), and the firewall open.")
		}
		return lines, explanation
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

// titleWord capitalises the first letter without the deprecated strings.Title.
func titleWord(word string) string {
	if word == "" {
		return word
	}
	return strings.ToUpper(word[:1]) + word[1:]
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// profileSpec rebuilds the spec a saved profile describes.
func profileSpec(profile config.TunnelProfile) (tunnel.Spec, error) {
	return tunnelFormState{kind: tunnel.Kind(strings.ToLower(profile.Kind))}.specFrom(profile)
}

// specFrom validates a profile's two addresses without a form.
func (f tunnelFormState) specFrom(profile config.TunnelProfile) (tunnel.Spec, error) {
	kind := tunnel.Kind(strings.ToLower(profile.Kind))
	if kind != tunnel.Local && kind != tunnel.Remote && kind != tunnel.Dynamic {
		return tunnel.Spec{}, fmt.Errorf("unknown tunnel kind %q", profile.Kind)
	}
	host, port, err := net.SplitHostPort(profile.Listen)
	if err != nil {
		return tunnel.Spec{}, fmt.Errorf("listen: %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return tunnel.Spec{}, fmt.Errorf("listen port must be 1 to 65535")
	}
	spec := tunnel.Spec{Kind: kind, Listen: net.JoinHostPort(host, port)}
	if kind == tunnel.Dynamic {
		return spec, nil
	}
	targetHost, targetPort, err := net.SplitHostPort(profile.Target)
	if err != nil {
		return tunnel.Spec{}, fmt.Errorf("target: %w", err)
	}
	targetNumber, err := strconv.Atoi(targetPort)
	if err != nil || targetNumber < 1 || targetNumber > 65535 {
		return tunnel.Spec{}, fmt.Errorf("target port must be 1 to 65535")
	}
	spec.Target = net.JoinHostPort(targetHost, targetPort)
	return spec, nil
}

// runningForward finds a live forward that already occupies this address on the
// same machine. For a remote forward "the same machine" is the server, so the
// listen namespace matches only remote-to-remote; local and dynamic share this
// machine's listener namespace.
func (m *Model) runningForward(host string, spec tunnel.Spec) *activeTunnel {
	for _, active := range m.tunnels {
		if active.host != host {
			continue
		}
		sameNamespace := (active.kind == tunnel.Remote) == (spec.Kind == tunnel.Remote)
		if sameNamespace && active.listen == spec.Listen {
			return active
		}
		if active.kind == spec.Kind && active.listen == spec.Listen && active.target == spec.Target {
			return active
		}
	}
	return nil
}

// startTunnelSpec opens a forward from an already validated spec.
func (m *Model) startTunnelSpec(spec tunnel.Spec) error {
	host := m.tunnelHost
	if host == "" {
		host, _ = m.sshHostForTunnel()
	}
	if active := m.runningForward(host, spec); active != nil {
		label := active.savedAs
		if label == "" {
			label = fmt.Sprintf("%s %s → %s", active.kind, active.listen, active.target)
		}
		return fmt.Errorf("already running as %s · open Tunnels to watch it", label)
	}
	live, liveOK := m.live[host]
	if !liveOK || live == nil || live.Client == nil || live.Client.SSH() == nil {
		return fmt.Errorf("%s is no longer connected · reconnect and try again", host)
	}
	options := tunnel.Options{Kind: spec.Kind, Listen: spec.Listen, Target: spec.Target, Host: host}
	active := &activeTunnel{host: host, kind: spec.Kind, listen: spec.Listen, target: spec.Target,
		open: map[string][2]int64{}}
	// The dashboard wants live updates and the history wants a record: one
	// callback feeds both. Only finished events go to the file; progress ticks
	// stay in memory.
	options.OnEvent = func(event tunnel.Event) {
		active.observe(event)
		recordTunnelMetadata(event)
	}
	options.OnProgress = active.progress
	if spec.Kind == tunnel.Remote {
		options.Remote = live.Client.SSH()
	} else {
		options.Dialer = sshDialer{client: live.Client.SSH()}
	}
	started, err := tunnel.Start(context.Background(), options)
	if err != nil {
		return err
	}
	if spec.Kind == tunnel.Remote {
		if host, _, err := net.SplitHostPort(spec.Listen); err == nil && (host == "0.0.0.0" || host == "::") {
			m.toastInfo("Remote bind on the server · GatewayPorts and the firewall decide if it is public")
		}
	}
	m.tunnelSeq++
	active.id, active.tunnel = m.tunnelSeq, started
	active.sessionID = started.ID()
	// A tunnel that ran is a tunnel worth keeping: starting one writes its
	// configuration, so a reconnect does not mean typing it again. Identical
	// profiles are reused rather than duplicated.
	active.savedAs = m.autosaveTunnel(host, spec)
	m.tunnels = append(m.tunnels, active)
	return nil
}

// recordTunnelMetadata appends one event to the state log that History reads.
// Tunnels started from the interface used to update only the live dashboard,
// which is why their history looked empty.
var tunnelLogMu sync.Mutex

func recordTunnelMetadata(event tunnel.Event) {
	if event.Active {
		return
	}
	dir, err := config.StateDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	line, err := json.Marshal(event)
	if err != nil {
		return
	}
	tunnelLogMu.Lock()
	defer tunnelLogMu.Unlock()
	file, err := os.OpenFile(filepath.Join(dir, "tunnels.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(append(line, '\n'))
}

// autosaveTunnel stores a forward in the config, reusing an identical profile.
// The name is readable: host-kind-port, with a suffix only on a real collision.
func (m *Model) autosaveTunnel(host string, spec tunnel.Spec) string {
	if m.cfg.Tunnels.Profiles == nil {
		m.cfg.Tunnels.Profiles = map[string]config.TunnelProfile{}
	}
	kind := strings.ToLower(string(spec.Kind))
	for name, profile := range m.cfg.Tunnels.Profiles {
		if profile.Host == host && strings.EqualFold(profile.Kind, kind) &&
			profile.Listen == spec.Listen && profile.Target == spec.Target {
			return name
		}
	}
	_, port, _ := net.SplitHostPort(spec.Listen)
	base := fmt.Sprintf("%s-%s-%s", safeName(host), kind, valueOr(port, "port"))
	if spec.Kind != tunnel.Dynamic {
		_, targetPort, _ := net.SplitHostPort(spec.Target)
		side := "server"
		if spec.Kind == tunnel.Remote {
			side = "local"
		}
		base += fmt.Sprintf("-%s-%s", side, valueOr(targetPort, "port"))
	}
	name := base
	for suffix := 2; ; suffix++ {
		if _, exists := m.cfg.Tunnels.Profiles[name]; !exists {
			break
		}
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
	m.cfg.Tunnels.Profiles[name] = config.TunnelProfile{Host: host, Kind: kind,
		Listen: spec.Listen, Target: spec.Target}
	m.saveConfig()
	return name
}

// sshDialer adapts an SSH client to tunnel.Dialer.
type sshDialer struct{ client *sshx.Client }

func (d sshDialer) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	if d.client == nil {
		return nil, fmt.Errorf("ssh: the connection is closed")
	}
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
	// Remove it from the list first, then close in the background. Close waits for
	// open connections to finish; doing that on the update loop froze the
	// dashboard for exactly as long as the tunnel's slowest client.
	m.tunnels = append(m.tunnels[:index], m.tunnels[index+1:]...)
	if active.tunnel != nil {
		go func() { _ = active.tunnel.Close() }()
	}
	if m.tunnelDash > len(m.tunnels)-1 {
		m.tunnelDash = max(0, len(m.tunnels)-1)
	}
	m.toastInfo(fmt.Sprintf("Stopped %s → %s", active.listen, active.target))
	if len(m.tunnels) == 0 && m.screen == screenTunnels {
		m.screen = screenHome
		m.mode = "NORMAL"
	}
}

// closeTunnels stops everything in parallel and waits only briefly. Quitting must
// not hang on a tunnel whose client never hangs up.
func (m *Model) closeTunnels() {
	tunnels := m.tunnels
	m.tunnels = nil
	if len(tunnels) == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, active := range tunnels {
			if active.tunnel == nil {
				continue
			}
			wg.Add(1)
			go func(closer interface{ Close() error }) {
				defer wg.Done()
				_ = closer.Close()
			}(active.tunnel)
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(900 * time.Millisecond):
	}
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

// profileForm turns a saved profile back into the two-step form.
func profileForm(name string, profile config.TunnelProfile) tunnelFormState {
	kind := tunnel.Kind(strings.ToLower(profile.Kind))
	form := tunnelFormState{host: profile.Host, kind: kind, saved: name}
	form.enterFields(kind)
	set := func(key, value string) {
		for i := range form.fields {
			if form.fields[i].key == key {
				form.fields[i].in.SetForEditing(value)
			}
		}
	}
	if host, port, err := net.SplitHostPort(profile.Listen); err == nil {
		set("bind", host)
		set("port", port)
	}
	if kind != tunnel.Dynamic {
		if host, port, err := net.SplitHostPort(profile.Target); err == nil {
			set("host", host)
			set("port2", port)
		}
	}
	return form
}

// saveTunnelProfile writes a form's values under a name, in the config.
func (m *Model) saveTunnelProfile(name string, form tunnelFormState) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a saved tunnel needs a name")
	}
	spec, err := form.spec()
	if err != nil {
		return err
	}
	profile := config.TunnelProfile{Host: form.host, Kind: string(form.kind),
		Listen: spec.Listen, Target: spec.Target}
	if m.cfg.Tunnels.Profiles == nil {
		m.cfg.Tunnels.Profiles = map[string]config.TunnelProfile{}
	}
	m.cfg.Tunnels.Profiles[name] = profile
	m.saveConfig()
	return nil
}

// openTunnelSaved lists the saved forwards.
func (m *Model) openTunnelSaved() {
	if len(m.cfg.Tunnels.Profiles) == 0 {
		m.toastInfo("No saved tunnels yet · s in the tunnel form saves one")
		return
	}
	m.tunnelSaved = 0
	m.Overlay = OverlayTunnelSaved
}

// savedNames is the profiles in a stable order.
func (m *Model) savedNames() []string {
	names := make([]string, 0, len(m.cfg.Tunnels.Profiles))
	for name := range m.cfg.Tunnels.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// handleTunnelSavedKey starts, edits or deletes a saved forward.
func (m *Model) handleTunnelSavedKey(msg tea.KeyPressMsg) {
	names := m.savedNames()
	switch msg.String() {
	case "esc", "q":
		m.Overlay = OverlayNone
	case "up", "k":
		m.tunnelSaved = max(0, m.tunnelSaved-1)
	case "down", "j":
		m.tunnelSaved = min(max(0, len(names)-1), m.tunnelSaved+1)
	case "enter":
		if m.tunnelSaved >= 0 && m.tunnelSaved < len(names) {
			m.startSavedTunnel(names[m.tunnelSaved])
		}
	case "e":
		if m.tunnelSaved >= 0 && m.tunnelSaved < len(names) {
			name := names[m.tunnelSaved]
			m.tunnelForm = profileForm(name, m.cfg.Tunnels.Profiles[name])
			m.tunnelHost = m.tunnelForm.host
			m.Overlay = OverlayTunnelForm
		}
	case "x", "d":
		if m.tunnelSaved >= 0 && m.tunnelSaved < len(names) {
			name := names[m.tunnelSaved]
			m.confirm = confirmState{Title: "Delete saved tunnel?",
				Body:   []string{name + " will be removed from config.yaml.", "Nothing is stopped; running tunnels are separate."},
				Danger: true, Action: "tunnel.delete-saved", Alias: name, open: true}
			m.Overlay = OverlayConfirm
		}
	}
}

// startSavedTunnel starts a profile on its host.
func (m *Model) startSavedTunnel(name string) {
	profile, ok := m.cfg.Tunnels.Profiles[name]
	if !ok {
		return
	}
	spec, err := profileSpec(profile)
	if err != nil {
		m.toastError(err.Error())
		return
	}
	// A tunnel that is already up is not started twice: the dashboard is what
	// the person wants to see, not an SSH error about a busy port.
	if active := m.runningForward(profile.Host, spec); active != nil {
		for i, candidate := range m.tunnels {
			if candidate == active {
				m.openTunnelDashboard(i)
				m.toastInfo(name + " is already running")
				return
			}
		}
	}
	if live := m.live[profile.Host]; live == nil || live.Client == nil || live.Client.SSH() == nil {
		target, found := m.targetForAlias(profile.Host)
		if !found {
			m.toastInfo("Connect to " + profile.Host + " first")
			return
		}
		cmd := m.connect(target)
		if cmd == nil {
			m.toastInfo("Connection to " + profile.Host + " is already in progress")
			return
		}
		m.pendingTunnelProfile = name
		m.Overlay = OverlayNone
		m.screen = screenHome
		m.mode = "NORMAL"
		m.toastInfo("Connecting to " + profile.Host + "…")
		m.overlayCmd = cmd
		return
	}
	if form := profileForm(name, profile); form.checkPort() != nil {
		if err := form.checkPort(); err != nil {
			m.toastError(err.Error())
		}
		return
	}
	m.tunnelHost = profile.Host
	if err := m.startTunnelSpec(spec); err != nil {
		m.toastError(err.Error())
		return
	}
	m.openTunnelDashboard(len(m.tunnels) - 1)
	m.toastSuccess("Tunnel running · x stops it")
}

// deleteTunnelProfile is the confirmed removal.
func (m *Model) deleteTunnelProfile(name string) {
	delete(m.cfg.Tunnels.Profiles, name)
	m.saveConfig()
	switch {
	case m.tunnelHostMenu.host != "":
		m.openTunnelHostMenu(m.tunnelHostMenu.host)
	case len(m.cfg.Tunnels.Profiles) > 0:
		m.tunnelSaved = clamp(m.tunnelSaved, 0, len(m.cfg.Tunnels.Profiles)-1)
		m.Overlay = OverlayTunnelSaved
	default:
		m.Overlay = OverlayNone
	}
	m.toastSuccess("Deleted saved tunnel " + name)
}

// renderTunnelSaved draws the list.
func (m *Model) renderTunnelSaved(l layout) string {
	names := m.savedNames()
	width := cardWidth(l, 60, 96)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Saved tunnels"),
		m.st.CardMuted.Render(truncate(fmt.Sprintf("%d · Enter starts · e edits · x deletes", len(names)), content)),
		"",
	}
	for i, name := range names {
		profile := m.cfg.Tunnels.Profiles[name]
		line := fmt.Sprintf("%-18s %s", name, describeTunnel(profile))
		hostLine := m.st.CardMuted.Render(truncate("  via "+profile.Host, content))
		meta := line
		if spec, err := profileSpec(profile); err == nil && m.runningForward(profile.Host, spec) != nil {
			meta = "running · " + line
		}
		if i == m.tunnelSaved {
			lines = append(lines, m.st.PaletteItemActive.Width(content).Render(padVisible("▸ "+truncate(meta, content-2), content)), hostLine)
			continue
		}
		lines = append(lines, "  "+m.st.CardText.Render(truncate(meta, content-2)), hostLine)
	}
	lines = append(lines, "", m.st.CardMuted.Render("↑↓ choose · Enter start · e edit · x delete · Esc back"))
	return m.card(width, lines)
}

// openTunnelDashboard shows the live forwards as their own screen. The tunnel is
// not a file and not a history row while it runs: it is counters and connections.
func (m *Model) openTunnelDashboard(cursor int) {
	if len(m.tunnels) == 0 {
		m.toastInfo("No tunnels running · Terminal & Tunnel starts one")
		return
	}
	m.tunnelDash = clamp(cursor, 0, len(m.tunnels)-1)
	m.screen = screenTunnels
	m.Overlay = OverlayNone
	m.mode = "TUNNELS"
}

// handleTunnelDashboardKey drives stop, another port, a new tunnel and the way out.
func (m *Model) handleTunnelDashboardKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		m.screen = screenHome
		m.mode = "NORMAL"
		m.home.cursor = 0
	case "up", "k":
		m.tunnelDash = max(0, m.tunnelDash-1)
	case "down", "j":
		m.tunnelDash = min(max(0, len(m.tunnels)-1), m.tunnelDash+1)
	case "x", "s":
		m.stopTunnel(m.tunnelDash)
		if len(m.tunnels) == 0 {
			m.screen = screenHome
			m.mode = "NORMAL"
		}
	case "n":
		if active := m.selectedTunnel(); active != nil {
			m.openTunnelPromptHost(active.host)
		}
	case "p":
		// "Another port": the same forward, with the next free listener, so two
		// services can share one target without editing the first."
		active := m.selectedTunnel()
		if active == nil {
			return nil
		}
		form := tunnelFormState{host: active.host, kind: active.kind}
		form.enterFields(active.kind)
		bind, port, _ := net.SplitHostPort(active.listen)
		host, port2, _ := net.SplitHostPort(active.target)
		for i := range form.fields {
			switch form.fields[i].key {
			case "bind":
				form.fields[i].in.SetForEditing(bind)
			case "port":
				next, _ := strconv.Atoi(port)
				form.fields[i].in.SetForEditing(strconv.Itoa(suggestPort(bind, next+1)))
			case "host":
				form.fields[i].in.SetForEditing(host)
			case "port2":
				form.fields[i].in.SetForEditing(port2)
			}
		}
		m.tunnelForm = form
		m.tunnelHost = active.host
		m.Overlay = OverlayTunnelForm
	case "h":
		m.screen = screenHistory
		m.history = historyState{tab: 1}
		m.history.all = m.loadHistory()
		m.history.cursor = 0
		m.mode = "HISTORY"
	case "c":
		return m.checkTunnelServer()
	}
	return nil
}

// tunnelCheckMsg carries the server's answer to a read-only forwarding check.
type tunnelCheckMsg struct {
	host   string
	port   string
	output string
	err    error
}

// checkTunnelServer asks the server what sshd allows and what it actually bound.
// It changes nothing: sshd's configuration is the server owner's decision, and
// changing it needs root.
func (m *Model) checkTunnelServer() tea.Cmd {
	active := m.selectedTunnel()
	if active == nil {
		return nil
	}
	if active.kind != tunnel.Remote {
		m.toastInfo("Server check is for remote forwards: the listen is on the server")
		return nil
	}
	live := m.live[active.host]
	if live == nil || live.Client == nil || live.Client.SSH() == nil {
		m.toastInfo("Connect to " + active.host + " to check it")
		return nil
	}
	_, port, _ := net.SplitHostPort(active.listen)
	client := live.Client.SSH()
	host := active.host
	name := port
	return func() tea.Msg {
		sessionLive, err := client.NewSession()
		if err != nil {
			return tunnelCheckMsg{host: host, port: name, err: err}
		}
		defer func() { _ = sessionLive.Close() }()
		command := fmt.Sprintf("sshd -T 2>/dev/null | grep -Ei '^(gatewayports|allowtcpforwarding)'; "+
			"echo '---'; ss -ltn 2>/dev/null | grep ':%s '", name)
		output, err := sessionLive.CombinedOutput(command)
		return tunnelCheckMsg{host: host, port: name, output: string(output), err: err}
	}
}

// serverCheckLines turns the check into a card, with the one sentence that
// matters for each answer.
func serverCheckLines(msg tunnelCheckMsg) []string {
	lines := []string{
		fmt.Sprintf("port %s on %s", msg.port, msg.host),
		"",
	}
	if msg.err != nil && strings.TrimSpace(msg.output) == "" {
		lines = append(lines, "The server did not answer: "+msg.err.Error(),
			"", "Run these by hand on the server:")
	} else {
		lines = append(lines, strings.Split(strings.TrimSpace(msg.output), "\n")...)
		lines = append(lines, "")
	}
	output := strings.ToLower(msg.output)
	switch {
	case strings.Contains(output, "gatewayports no"):
		lines = append(lines, "GatewayPorts is no: remote forwards bind loopback, so a public IP cannot reach them.")
	case !strings.Contains(output, "gatewayports"):
		lines = append(lines, "sshd -T did not report GatewayPorts (it may need root). If it is no, a public IP cannot reach the forward.")
	default:
		lines = append(lines, "GatewayPorts allows a non-loopback bind; the address in the listening line decides what is public.")
	}
	if strings.Contains(output, "allowtcpforwarding no") || strings.Contains(output, "allowtcpforwarding local") {
		lines = append(lines, "AllowTcpForwarding blocks remote forwards on this server.")
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, ":"+msg.port) {
			if strings.Contains(line, "127.0.0.1") || strings.Contains(line, "[::1]") {
				lines = append(lines, "It is listening on loopback only. For a public IP, bind 0.0.0.0 and set GatewayPorts clientspecified.")
			} else if strings.Contains(line, "0.0.0.0") || strings.Contains(line, "[::]") {
				lines = append(lines, "It is listening publicly; if the IP still does not answer, open the port in the provider and host firewall.")
			}
		}
	}
	lines = append(lines, "",
		"omssh does not change sshd: that needs root on the server.",
		"To allow it, add to /etc/ssh/sshd_config.d/omssh.conf:",
		"  AllowTcpForwarding yes",
		"  GatewayPorts clientspecified",
		"then: sudo sshd -t && sudo systemctl reload ssh",
		"",
		"Open the port in the server firewall:",
		fmt.Sprintf("  sudo ufw allow %s/tcp", msg.port),
		"  # or",
		fmt.Sprintf("  sudo firewall-cmd --permanent --add-port=%s/tcp && sudo firewall-cmd --reload", msg.port),
		"",
		fmt.Sprintf("Also allow TCP %s inbound in the provider's firewall or security group.", msg.port),
	)
	return lines
}

func (m *Model) selectedTunnel() *activeTunnel {
	if m.tunnelDash < 0 || m.tunnelDash >= len(m.tunnels) {
		return nil
	}
	return m.tunnels[m.tunnelDash]
}

// renderTunnelDashboard is the live view: two panels, like the file manager, so
// the list and the traffic do not run into each other.
func (m *Model) renderTunnelDashboard(l layout) string {
	width, height := l.Width, l.Height
	leftW := clamp(width*48/100, 38, 56)
	rightW := max(36, width-leftW-3)
	panelRows := max(4, height-9)

	// The left panel: one forward per two lines, selection filled.
	list := []string{
		spread(m.st.PaneTitle.Render("RUNNING"),
			m.st.StatusMuted.Render(fmt.Sprintf("%d", len(m.tunnels))), leftW-6),
		m.st.CardMuted.Render(strings.Repeat("─", max(1, leftW-6))),
	}
	for i, active := range m.tunnels {
		conns, in, out, _ := active.snapshot()
		arrow := "→"
		if active.kind == tunnel.Remote {
			arrow = "←"
		}
		title := fmt.Sprintf("%s %s %s %s", active.kind, active.listen, arrow, active.target)
		if active.savedAs != "" {
			title = active.savedAs
		}
		meta := fmt.Sprintf("%s · %d conns · %s in · %s out", active.host, conns,
			humanSize(in), humanSize(out))
		if i == m.tunnelDash {
			list = append(list,
				m.st.PaletteItemActive.Width(leftW-6).Render(padVisible("▸ "+truncate(title, leftW-8), leftW-6)),
				m.st.PaletteItemActive.Width(leftW-6).Render(padVisible("  "+truncate(meta, leftW-8), leftW-6)))
			continue
		}
		list = append(list,
			m.st.CardText.Render(truncate("  "+title, leftW-6)),
			m.st.CardMuted.Render(truncate("  "+meta, leftW-6)))
	}
	for len(list) < panelRows {
		list = append(list, "")
	}
	list = list[:panelRows]

	// The right panel: what this tunnel is doing, and the last few connections.
	detail := []string{}
	if active := m.selectedTunnel(); active != nil {
		conns, in, out, errText := active.snapshot()
		arrow := "→"
		if active.kind == tunnel.Remote {
			arrow = "←"
		}
		detail = append(detail,
			m.st.PaneTitle.Render(truncate(func() string {
				if active.savedAs != "" {
					return active.savedAs
				}
				return fmt.Sprintf("%s · %s", titleWord(string(active.kind)), active.host)
			}(), rightW-6)),
			m.st.CardMuted.Render(strings.Repeat("─", max(1, rightW-6))),
			m.st.CardText.Render(truncate(active.listen+" "+arrow+" "+active.target, rightW-6)),
			"",
		)
		if active.savedAs != "" {
			detail = append(detail, m.st.CardMuted.Render(truncate("saved as "+active.savedAs, rightW-6)))
		}
		detail = append(detail,
			"",
			fmt.Sprintf("%d connections · %s received · %s sent", conns, humanSize(in), humanSize(out)),
		)
		if errText != "" {
			detail = append(detail, m.st.Danger.Render(truncate(errText, rightW-6)))
		}
		if activeNow := active.activeConnections(); len(activeNow) > 0 {
			detail = append(detail, "", m.st.StatusMuted.Render("Active now"))
			clients := make([]string, 0, len(activeNow))
			for client := range activeNow {
				clients = append(clients, client)
			}
			sort.Strings(clients)
			for _, client := range clients {
				counts := activeNow[client]
				detail = append(detail, m.st.CardText.Render(truncate(fmt.Sprintf("  %s  %s in · %s out",
					client, humanSize(counts[0]), humanSize(counts[1])), rightW-6)))
			}
		}
		detail = append(detail, "", m.st.StatusMuted.Render("Recent connections"))
		events := active.tail()
		shown := 0
		for i := len(events) - 1; i >= 0 && len(detail) < panelRows-1; i-- {
			event := events[i]
			switch {
			case event.Client == "" && event.Error == "":
				continue
			case event.Error != "":
				detail = append(detail, m.st.Danger.Render(truncate("  error  "+event.Error, rightW-6)))
			default:
				detail = append(detail, m.st.CardText.Render(truncate(fmt.Sprintf("  %s  %s  %s in · %s out",
					event.Time.Format("15:04:05"), event.Client,
					humanSize(event.BytesIn), humanSize(event.BytesOut)), rightW-6)))
			}
			shown++
		}
		if shown == 0 {
			detail = append(detail, m.st.PaneEmpty.Render("  waiting for a connection"))
		}
	}
	for len(detail) < panelRows {
		detail = append(detail, "")
	}
	detail = detail[:panelRows]

	panels := lipgloss.JoinHorizontal(lipgloss.Top,
		m.card(leftW, list), " ", m.card(rightW, detail))
	header := spread(m.brandScreen("Tunnels"),
		m.st.StatusMuted.Render(fmt.Sprintf("%d running", len(m.tunnels))), width)
	footer := "↑↓ choose · x stop · p another port · n new · c server check · h history · Esc launcher"
	return header + "\n" + panels + "\n" + m.st.StatusMuted.Render(truncate(footer, width))
}

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
		m.brandCard("MCP Agents"),
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
