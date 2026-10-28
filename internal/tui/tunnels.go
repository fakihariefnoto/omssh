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
	events   []tunnel.Event
}

func (t *activeTunnel) observe(event tunnel.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// A short tail is enough to see what the tunnel is doing; the history file
	// keeps everything.
	t.events = append(t.events, event)
	if len(t.events) > 100 {
		t.events = t.events[len(t.events)-100:]
	}
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
	m.toastInfo(fmt.Sprintf("Stopped %s → %s", active.listen, active.target))
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
	if _, live := m.live[profile.Host]; !live {
		m.toastInfo("Connect to " + profile.Host + " first")
		return
	}
	form := profileForm(name, profile)
	if err := form.checkPort(); err != nil {
		m.toastError(err.Error())
		return
	}
	spec, err := form.spec()
	if err != nil {
		m.toastError(err.Error())
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
	if len(m.cfg.Tunnels.Profiles) > 0 {
		m.tunnelSaved = clamp(m.tunnelSaved, 0, len(m.cfg.Tunnels.Profiles)-1)
		m.Overlay = OverlayTunnelSaved
	} else {
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
		arrow := "→"
		if strings.EqualFold(profile.Kind, "remote") {
			arrow = "←"
		}
		line := fmt.Sprintf("%-18s %s %s %s %s", name, profile.Kind, profile.Listen, arrow, profile.Target)
		hostLine := m.st.CardMuted.Render(truncate("  via "+profile.Host, content))
		if i == m.tunnelSaved {
			lines = append(lines, m.st.PaletteItemActive.Width(content).Render(padVisible("▸ "+truncate(line, content-2), content)), hostLine)
			continue
		}
		lines = append(lines, "  "+m.st.CardText.Render(truncate(line, content-2)), hostLine)
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
func (m *Model) handleTunnelDashboardKey(msg tea.KeyPressMsg) {
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
			return
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
	}
}

func (m *Model) selectedTunnel() *activeTunnel {
	if m.tunnelDash < 0 || m.tunnelDash >= len(m.tunnels) {
		return nil
	}
	return m.tunnels[m.tunnelDash]
}

// renderTunnelDashboard is the live view: the list on the left, the selected
// tunnel's traffic and connections on the right.
func (m *Model) renderTunnelDashboard(l layout) string {
	width, height := l.Width, l.Height
	leftW := clamp(width*2/5, 30, 48)
	rightW := width - leftW - 3

	header := spread(m.st.Accent.Bold(true).Render("Tunnels"),
		m.st.StatusMuted.Render(fmt.Sprintf("%d running", len(m.tunnels))), width)
	lines := []string{header, m.st.CardMuted.Render(strings.Repeat("─", max(1, width)))}

	list := make([]string, 0, height-4)
	for i, active := range m.tunnels {
		conns, in, out, _ := active.snapshot()
		arrow := "→"
		if active.kind == tunnel.Remote {
			arrow = "←"
		}
		title := fmt.Sprintf("%s %s %s %s", active.kind, active.listen, arrow, active.target)
		meta := fmt.Sprintf("%s · %d conns · %s in · %s out", active.host, conns,
			humanSize(in), humanSize(out))
		if i == m.tunnelDash {
			list = append(list,
				m.st.PaletteItemActive.Width(leftW).Render(padVisible("▸ "+truncate(title, leftW-2), leftW)),
				m.st.CardMuted.Render(truncate("  "+meta, leftW)))
			continue
		}
		list = append(list,
			m.st.CardText.Render(truncate("  "+title, leftW)),
			m.st.CardMuted.Render(truncate("  "+meta, leftW)))
	}

	detail := make([]string, 0, height-4)
	if active := m.selectedTunnel(); active != nil {
		conns, in, out, errText := active.snapshot()
		detail = append(detail,
			m.st.CardTitle.Render(truncate(fmt.Sprintf("%s · %s", active.kind, active.host), rightW)),
			truncate(active.listen+" → "+active.target, rightW),
			"",
			fmt.Sprintf("%d connections · %s received · %s sent", conns, humanSize(in), humanSize(out)),
		)
		if errText != "" {
			detail = append(detail, m.st.Danger.Render(truncate(errText, rightW)))
		}
		detail = append(detail, "", m.st.StatusMuted.Render("Recent connections"))
		events := active.tail()
		shown := 0
		for i := len(events) - 1; i >= 0 && shown < max(3, height-12); i-- {
			event := events[i]
			switch {
			case event.Client == "" && event.Error == "":
				continue
			case event.Error != "":
				detail = append(detail, m.st.Danger.Render(truncate("  error  "+event.Error, rightW)))
			default:
				detail = append(detail, m.st.CardText.Render(truncate(fmt.Sprintf("  %s  %s  %s in · %s out",
					event.Time.Format("15:04:05"), event.Client,
					humanSize(event.BytesIn), humanSize(event.BytesOut)), rightW)))
			}
			shown++
		}
		if shown == 0 {
			detail = append(detail, m.st.PaneEmpty.Render("  waiting for a connection"))
		}
	}

	for i := 0; i < height-3; i++ {
		left := ""
		if i < len(list) {
			left = list[i]
		}
		right := ""
		if i < len(detail) {
			right = detail[i]
		}
		lines = append(lines, padVisible(left, leftW)+"   "+padVisible(truncate(right, rightW), rightW))
	}
	lines = append(lines, "", m.st.StatusMuted.Render(truncate(
		"↑↓ choose · x stop · p another port · n new tunnel · h history · Esc launcher", width)))
	return strings.Join(lines, "\n")
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
