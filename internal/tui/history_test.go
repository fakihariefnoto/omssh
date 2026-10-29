package tui

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/tunnel"
)

// historyFixture writes one of each record under a temporary state directory.
func historyFixture(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "omssh")
	if err := os.MkdirAll(filepath.Join(dir, "recordings"), 0o700); err != nil {
		t.Fatal(err)
	}
	cast := filepath.Join(dir, "recordings", "contabo-20261028-100000.cast")
	body := "{\"version\":2,\"width\":40,\"height\":5,\"timestamp\":1}\n" +
		"[0.1,\"o\",\"hello\\r\\nworld\"]\n" +
		"[0.2,\"i\",\"ls\\r\"]\n"
	if err := os.WriteFile(cast, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now().Add(-time.Hour)
	events := []tunnel.Event{
		{Time: start, Session: "s1", Host: "contabo", Kind: tunnel.Local,
			Listen: "127.0.0.1:8080", Target: "db:5432"},
		{Time: start.Add(time.Minute), Session: "s1", Host: "contabo", Kind: tunnel.Local,
			Listen: "127.0.0.1:8080", Target: "db:5432", Client: "127.0.0.1:5000",
			BytesIn: 10, BytesOut: 20},
	}
	writeJSONLines(t, filepath.Join(dir, "tunnels.jsonl"), events)

	calls := []map[string]any{{
		"time": time.Now().Add(-time.Minute).Format(time.RFC3339),
		"tool": "ls", "place": "prod", "path": "/srv/app", "ok": true,
	}}
	writeJSONLines(t, filepath.Join(dir, "mcp.jsonl"), calls)
	return dir
}

func writeJSONLines(t *testing.T, path string, values any) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	switch list := values.(type) {
	case []tunnel.Event:
		for _, value := range list {
			if err := encoder.Encode(value); err != nil {
				t.Fatal(err)
			}
		}
	case []map[string]any:
		for _, value := range list {
			if err := encoder.Encode(value); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// One screen, three kinds, tabs that filter, and an agent badge in the combined
// view.
func TestHistoryTabs(t *testing.T) {
	historyFixture(t)
	m := newTestModel(t)
	m.openHistory(3)
	if m.screen != screenHistory {
		t.Fatalf("history should be a screen, got %v", m.screen)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"History", "contabo", "127.0.0.1:8080", "agent · ls", "Terminal", "Tunnel", "Agent", "All"} {
		if !strings.Contains(out, want) {
			t.Errorf("history should show %q:\n%s", want, out)
		}
	}

	// The Tunnel tab hides the agent and terminal rows.
	m.openHistory(1)
	out = ansi.Strip(render(m))
	if !strings.Contains(out, "127.0.0.1:8080") || strings.Contains(out, "agent · ls") {
		t.Errorf("the Tunnel tab should filter to tunnels:\n%s", out)
	}

	// Tab cycles the filter.
	m.openHistory(1)
	press(t, m, keyNamed(tea.KeyTab))
	if m.history.tab != 2 {
		t.Errorf("Tab should move to Agent, tab=%d", m.history.tab)
	}
}

// Enter on a tunnel opens the tail: start line, connection, bytes.
func TestHistoryTunnelTail(t *testing.T) {
	historyFixture(t)
	m := newTestModel(t)
	m.openHistory(1)
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayHistoryDetail {
		t.Fatalf("Enter should open the tail, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"Tunnel · watch", "listening on 127.0.0.1:8080", "127.0.0.1:5000"} {
		if !strings.Contains(out, want) {
			t.Errorf("the tail should show %q:\n%s", want, out)
		}
	}
}

// v reads the conversation log instead of replaying it.
func TestHistoryTranscript(t *testing.T) {
	historyFixture(t)
	m := newTestModel(t)
	m.openHistory(0)
	press(t, m, run('v', 0, "v"))
	if m.Overlay != OverlayHistoryDetail {
		t.Fatalf("v should open the log, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("the log should contain the session output:\n%s", out)
	}
}

// d deletes only the selected record, from its own store.
func TestHistoryDelete(t *testing.T) {
	dir := historyFixture(t)
	m := newTestModel(t)
	m.openHistory(1)
	press(t, m, run('d', 0, "d"))
	if m.Overlay != OverlayConfirm {
		t.Fatalf("d should ask first, overlay=%v", m.Overlay)
	}
	m.deleteHistoryEntry()
	body, err := os.ReadFile(filepath.Join(dir, "tunnels.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "s1") {
		t.Errorf("the tunnel session should be gone:\n%s", body)
	}
	// The terminal recording is untouched by a tunnel delete.
	if _, err := os.Stat(filepath.Join(dir, "recordings", "contabo-20261028-100000.cast")); err != nil {
		t.Errorf("deleting a tunnel must not touch recordings: %v", err)
	}
}

// The tunnel form asks for a direction, then shows the fields and a simulation.
func TestTunnelFormIsIllustrated(t *testing.T) {
	m := newTestModel(t)
	m.live = map[string]*session.Session{"contabo": {Target: omssh.Target{
		Alias: "contabo", User: "deploy", Host: "203.0.113.10", Port: 22,
	}}}
	m.openTunnelPromptHost("contabo")
	out := ansi.Strip(render(m))
	for _, want := range []string{"Tunnel through contabo", "Local forwarding", "Remote forwarding",
		"Dynamic forwarding", "Redirects traffic", "SOCKS proxy"} {
		if !strings.Contains(out, want) {
			t.Errorf("step one should explain %q:\n%s", want, out)
		}
	}

	// Enter chooses Local and moves to fields with a simulation.
	press(t, m, keyNamed(tea.KeyEnter))
	if m.tunnelForm.step != 1 {
		t.Fatalf("Enter should move to the fields, step=%d", m.tunnelForm.step)
	}
	out = ansi.Strip(render(m))
	for _, want := range []string{"bind", "port", "target host", "target port",
		"What will happen", "localhost in the target means the server"} {
		if !strings.Contains(out, want) {
			t.Errorf("step two should explain %q:\n%s", want, out)
		}
	}
}

// An empty terminal tab says why it is empty, including the setting.
func TestHistoryEmptyExplainsRecording(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := newTestModel(t)
	m.cfg.Sessions.Record = false
	m.openHistory(0)
	if out := ansi.Strip(render(m)); !strings.Contains(out, "sessions.record") && !strings.Contains(out, "recording is off") {
		t.Errorf("with recording off, history should say so:\n%s", out)
	}
	m.cfg.Sessions.Record = true
	m.openHistory(0)
	if out := ansi.Strip(render(m)); !strings.Contains(out, "embedded terminal") {
		t.Errorf("with recording on, history should say how to make one:\n%s", out)
	}
}

// Recordings are not a second launcher row: they are History's Terminal tab, and
// opening History from the launcher keeps the launcher behind it.
func TestRecordingsBelongToHistory(t *testing.T) {
	historyFixture(t)
	m := newTestModel(t)
	m.screen = screenHome
	for _, item := range m.homeItems() {
		if string(item.ID) == "session.recordings" {
			t.Error("Recordings should not be its own launcher row")
		}
	}
	m.runAction("app.history")
	if m.screen != screenHistory {
		t.Fatalf("History should be a screen, got %v", m.screen)
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "contabo") {
		t.Errorf("the Terminal tab should list the recording:\n%s", out)
	}
	press(t, m, keyNamed(tea.KeyEsc))
	if m.screen != screenHome {
		t.Errorf("Esc should return to the launcher, screen=%v", m.screen)
	}
}

// A running tunnel is marked, and can be stopped from History without deleting
// its record.
func TestHistoryStopsRunningTunnel(t *testing.T) {
	historyFixture(t)
	m := newTestModel(t)
	m.tunnels = []*activeTunnel{{id: 1, sessionID: "s1", host: "contabo", kind: tunnel.Local,
		listen: "127.0.0.1:8080", target: "db:5432"}}
	m.openHistory(1)
	if out := ansi.Strip(render(m)); !strings.Contains(out, "running") {
		t.Errorf("a running tunnel should say so:\n%s", out)
	}
	press(t, m, run('s', 0, "s"))
	if len(m.tunnels) != 0 {
		t.Error("s should stop the running tunnel")
	}
	// Stopping is not deleting: the history row stays.
	if out := ansi.Strip(render(m)); !strings.Contains(out, "127.0.0.1:8080") {
		t.Errorf("the history record should survive stopping:\n%s", out)
	}
}

// The form names the real host address, and whose localhost is whose.
func TestTunnelFormShowsTheRealAddress(t *testing.T) {
	m := newTestModel(t)
	m.live = map[string]*session.Session{"contabo": {Target: omssh.Target{
		Alias: "contabo", User: "deploy", Host: "203.0.113.10", Port: 2222,
	}}}
	m.openTunnelPromptHost("contabo")
	press(t, m, keyNamed(tea.KeyEnter))
	out := ansi.Strip(render(m))
	for _, want := range []string{"deploy@203.0.113.10:2222", "localhost in the target means the server", "this machine"} {
		if !strings.Contains(out, want) {
			t.Errorf("the form should explain %q:\n%s", want, out)
		}
	}
}

// A busy port is caught before anything starts, with a free one suggested.
func TestTunnelFormChecksPort(t *testing.T) {
	var listenConfig net.ListenConfig
	busy, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	port := busy.Addr().(*net.TCPAddr).Port

	m := newTestModel(t)
	m.live = map[string]*session.Session{"contabo": {Target: omssh.Target{Alias: "contabo", Host: "h"}}}
	m.openTunnelPromptHost("contabo")
	press(t, m, keyNamed(tea.KeyEnter))
	for i := range m.tunnelForm.fields {
		if m.tunnelForm.fields[i].key == "port" {
			m.tunnelForm.fields[i].in.Set(strconv.Itoa(port))
		}
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if !strings.Contains(m.tunnelForm.err, "in use") {
		t.Fatalf("a busy port should be refused, err=%q", m.tunnelForm.err)
	}
	if !strings.Contains(m.tunnelForm.err, "try") {
		t.Errorf("the error should suggest a free port: %q", m.tunnelForm.err)
	}
	if len(m.tunnels) != 0 {
		t.Error("nothing should start when the port is busy")
	}
}

// The suggested default is actually free.
func TestSuggestPortIsFree(t *testing.T) {
	port := suggestPort("127.0.0.1", 8080)
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("suggested port %d is not free: %v", port, err)
	}
	_ = listener.Close()
}

// A tunnel can be saved to the config, listed, edited and deleted.
func TestSavedTunnelProfiles(t *testing.T) {
	m := newTestModel(t)
	m.configPath = t.TempDir() + "/config.yaml"
	m.live = map[string]*session.Session{"contabo": {Target: omssh.Target{Alias: "contabo", Host: "h"}}}
	m.openTunnelPromptHost("contabo")
	press(t, m, keyNamed(tea.KeyEnter)) // choose Local

	// s saves under a prompt; the form stays, so Enter can still start it.
	press(t, m, run('s', 0, "s"))
	if m.Overlay != OverlayPrompt || m.prompt.Action != "prompt.tunnel-save" {
		t.Fatalf("s should ask for a name, overlay=%v action=%q", m.Overlay, m.prompt.Action)
	}
	m.applyPrompt("prompt.tunnel-save", "dev-db")
	profile, ok := m.cfg.Tunnels.Profiles["dev-db"]
	if !ok {
		t.Fatalf("the profile should be saved: %+v", m.cfg.Tunnels.Profiles)
	}
	if profile.Host != "contabo" || profile.Kind != "local" {
		t.Errorf("profile = %+v", profile)
	}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Tunnels.Profiles["dev-db"]; !ok {
		t.Error("the profile should be in the config file")
	}

	// The saved list starts, edits and deletes.
	m.Overlay = OverlayNone
	m.openTunnelSaved()
	if m.Overlay != OverlayTunnelSaved {
		t.Fatalf("saved tunnels should list, overlay=%v", m.Overlay)
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "dev-db") {
		t.Errorf("the list should name the profile:\n%s", out)
	}
	press(t, m, run('e', 0, "e"))
	if m.Overlay != OverlayTunnelForm || m.tunnelForm.saved != "dev-db" {
		t.Fatalf("e should edit the profile, overlay=%v saved=%q", m.Overlay, m.tunnelForm.saved)
	}
	if got := m.tunnelForm.value("host"); got != "localhost" {
		t.Errorf("the form should be filled from the profile, target=%q", got)
	}

	m.openTunnelSaved()
	press(t, m, run('x', 0, "x"))
	if m.Overlay != OverlayConfirm {
		t.Fatalf("x should confirm deletion, overlay=%v", m.Overlay)
	}
	press(t, m, keyNamed(tea.KeyRight))
	press(t, m, keyNamed(tea.KeyEnter))
	if len(m.cfg.Tunnels.Profiles) != 0 {
		t.Error("the profile should be deleted")
	}
}

// The three directions say what they do, in full sentences.
func TestTunnelDirectionsAreExplained(t *testing.T) {
	m := newTestModel(t)
	m.live = map[string]*session.Session{"contabo": {Target: omssh.Target{Alias: "contabo", Host: "h"}}}
	m.openTunnelPromptHost("contabo")
	out := ansi.Strip(render(m))
	for _, want := range []string{
		"Local forwarding", "remote server.",
		"Remote forwarding", "back to your local machine",
		"Dynamic forwarding", "SOCKS proxy", "through the SSH server",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the chooser should explain %q:\n%s", want, out)
		}
	}
}

// Enter on a tunnel that is still running opens the live dashboard, not the
// historical tail: bytes and connections are happening now.
func TestHistoryRunningTunnelOpensDashboard(t *testing.T) {
	historyFixture(t)
	m := newTestModel(t)
	m.tunnels = []*activeTunnel{{id: 1, sessionID: "s1", host: "contabo", kind: tunnel.Local,
		listen: "127.0.0.1:8080", target: "db:5432"}}
	m.openHistory(1)
	press(t, m, keyNamed(tea.KeyEnter))
	if m.screen != screenTunnels || m.Overlay != OverlayNone {
		t.Errorf("Enter on a running tunnel should show the live dashboard, screen=%v overlay=%v", m.screen, m.Overlay)
	}
}

// Progress reports add only new bytes, count a connection once, and a final
// report does not double it.
func TestTunnelProgressAccounting(t *testing.T) {
	active := &activeTunnel{open: map[string][2]int64{}}
	active.progress(tunnel.Event{Client: "a", BytesIn: 100, BytesOut: 200, Active: true})
	active.progress(tunnel.Event{Client: "a", BytesIn: 150, BytesOut: 300, Active: true})
	conns, in, out, _ := active.snapshot()
	if conns != 1 || in != 150 || out != 300 {
		t.Fatalf("after progress: %d conns %d/%d", conns, in, out)
	}
	active.observe(tunnel.Event{Client: "a", BytesIn: 150, BytesOut: 300})
	conns, in, out, _ = active.snapshot()
	if conns != 1 || in != 150 || out != 300 {
		t.Errorf("a final report doubled the totals: %d conns %d/%d", conns, in, out)
	}

	active.progress(tunnel.Event{Client: "b", BytesIn: 10, BytesOut: 20, Active: true})
	conns, in, out, _ = active.snapshot()
	if conns != 2 || in != 160 || out != 320 {
		t.Errorf("second client: %d conns %d/%d", conns, in, out)
	}
}

// The dashboard shows a connection while it is open, not only after it closes.
func TestTunnelDashboardShowsActiveTraffic(t *testing.T) {
	m := newTestModel(t)
	active := &activeTunnel{id: 1, sessionID: "s1", host: "ovh", kind: tunnel.Local,
		listen: "127.0.0.1:8080", target: "localhost:3000", open: map[string][2]int64{}}
	active.progress(tunnel.Event{Client: "203.0.113.7:50123", BytesIn: 1024, BytesOut: 4096, Active: true})
	m.tunnels = []*activeTunnel{active}
	m.openTunnelDashboard(0)
	out := ansi.Strip(render(m))
	for _, want := range []string{"Active now", "203.0.113.7:50123", "1.0 KB in", "4.0 KB out", "1 connections"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dashboard should show %q:\n%s", want, out)
		}
	}
}
