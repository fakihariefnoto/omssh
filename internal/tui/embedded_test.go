package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"github.com/fakihariefnoto/omssh/internal/session"
	"github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/tunnel"
)

// Keys reach the remote shell as the bytes a terminal would send.
func TestKeyBytes(t *testing.T) {
	cases := []struct {
		msg  tea.KeyPressMsg
		want string
	}{
		{run(tea.KeyEnter, 0, ""), "\r"},
		{run(tea.KeyTab, 0, ""), "\t"},
		{run(tea.KeyBackspace, 0, ""), "\x7f"},
		{run(tea.KeyEsc, 0, ""), "\x1b"},
		{run(tea.KeyUp, 0, ""), "\x1b[A"},
		{run(tea.KeyLeft, 0, ""), "\x1b[D"},
		{run('a', 0, "a"), "a"},
		{run('c', tea.ModCtrl, ""), "\x03"},
		{run('x', tea.ModAlt, "x"), "\x1bx"},
		{run(tea.KeySpace, 0, " "), " "},
	}
	for _, tc := range cases {
		if got := string(keyBytes(tc.msg)); got != tc.want {
			t.Errorf("keyBytes(%q) = %q, want %q", tc.msg.String(), got, tc.want)
		}
	}
}

// A replay rebuilds the screen at a time and can find text later in the file.
func TestReplaySeekAndFind(t *testing.T) {
	rec := &session.Recording{
		Header: session.RecordingHeader{Version: 2, Width: 40, Height: 5},
		Events: []session.RecordingEvent{
			{Time: 0, Type: "o", Data: "hello"},
			{Time: 1, Type: "o", Data: "\r\nworld"},
			{Time: 2, Type: "i", Data: "q"},
		},
	}
	state := &replayState{rec: rec, speed: 1}
	state.seek(0)
	if screen := state.emu.Render(); !strings.Contains(screen, "hello") || strings.Contains(screen, "world") {
		t.Fatalf("at 0 the screen = %q", screen)
	}
	state.seek(1500 * time.Millisecond)
	if screen := state.emu.Render(); !strings.Contains(screen, "world") {
		t.Fatalf("at 1.5s the screen = %q", screen)
	}
	index := rec.Find("q", 0)
	if index != 2 {
		t.Errorf("Find(q) = %d, want the input event", index)
	}
}

// The player's keys: space starts, arrows seek, and closing clears the overlay.
func TestReplayKeys(t *testing.T) {
	m := newTestModel(t)
	m.replay = &replayState{
		rec: &session.Recording{
			Header: session.RecordingHeader{Version: 2, Width: 40, Height: 5},
			Events: []session.RecordingEvent{
				{Time: 0, Type: "o", Data: "hello"},
				{Time: 3, Type: "o", Data: "\r\nworld"},
			},
		},
		speed: 1,
	}
	m.replay.seek(0)
	m.Overlay = OverlayReplay

	press(t, m, keyNamed(tea.KeySpace))
	if !m.replay.playing {
		t.Fatal("space should start playback")
	}
	press(t, m, keyNamed(tea.KeyRight))
	if m.replay.playing || m.replay.at <= 0 {
		t.Errorf("right should pause and seek: playing=%v at=%v", m.replay.playing, m.replay.at)
	}
	press(t, m, run('q', 0, "q"))
	if m.Overlay != OverlayNone || m.replay != nil {
		t.Error("q should close the player")
	}
}

// Embedded needs a live connection and says so instead of failing obscurely.
func TestEmbeddedNeedsALiveSession(t *testing.T) {
	m := newTestModel(t)
	m.Panes[0] = Pane{Side: SideRemote, Host: "contabo", Path: "/", Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 0
	if cmd := m.openEmbedded(); cmd != nil {
		t.Fatal("without a live session the terminal should not open")
	}
	if m.Overlay == OverlayTerminal {
		t.Error("the terminal overlay should not be open")
	}
}

// Recordings are listed from the launcher and play from the list.
func TestRecordingsListAndReplay(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "omssh", "recordings")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "contabo-20260101-120000.cast")
	body := "{\"version\":2,\"width\":40,\"height\":5,\"timestamp\":1}\n[0.1,\"o\",\"hello\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	m.openHistory(0)
	if m.screen != screenHistory {
		t.Fatalf("recordings belong to History, screen=%v", m.screen)
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "contabo") {
		t.Errorf("the Terminal tab should name the recording:\n%s", out)
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayReplay || m.replay == nil {
		t.Fatalf("Enter should open the player, overlay=%v", m.Overlay)
	}
}

// Retention: old files go, and the size cap deletes the oldest first.
func TestPruneRecordings(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "omssh", "recordings")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCast := func(name string, size int, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
		return path
	}
	old := writeCast("old.cast", 10, 48*time.Hour)
	fresh := writeCast("fresh.cast", 10, time.Hour)

	pruneRecordings(1, 512)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a recording older than keep_days should be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a fresh recording should stay: %v", err)
	}

	// 1 MB cap: two 700 kB files cannot both stay; the newer one survives.
	big1 := writeCast("big1.cast", 700<<10, 2*time.Hour)
	writeCast("big2.cast", 700<<10, time.Hour)
	pruneRecordings(0, 1)
	if _, err := os.Stat(big1); !os.IsNotExist(err) {
		t.Error("the oldest recording over the cap should be removed")
	}
}

// Tunnels have their own dashboard: the list, live counters, and what each
// connection moved. Esc leaves; x stops.
func TestTunnelDashboard(t *testing.T) {
	m := newTestModel(t)
	active := &activeTunnel{id: 1, sessionID: "s1", host: "contabo", kind: "local",
		listen: "127.0.0.1:8080", target: "db:5432"}
	active.conns, active.bytesIn, active.bytesOut = 2, 1024, 2048
	active.events = []tunnel.Event{{Time: time.Now(), Session: "s1", Client: "127.0.0.1:5000",
		BytesIn: 1024, BytesOut: 2048}}
	m.tunnels = []*activeTunnel{active}
	m.openTunnelDashboard(0)
	if m.screen != screenTunnels {
		t.Fatalf("the tunnel dashboard should be a screen, got %v", m.screen)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"Tunnels", "local", "127.0.0.1:8080", "db:5432", "2 connections",
		"contabo", "127.0.0.1:5000", "Recent connections"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dashboard should show %q:\n%s", want, out)
		}
	}
	press(t, m, keyNamed(tea.KeyEsc))
	if m.screen != screenHome || len(m.tunnels) != 1 {
		t.Errorf("Esc should return to the launcher and leave the tunnel, screen=%v tunnels=%d", m.screen, len(m.tunnels))
	}

	// p starts the same tunnel on another free port.
	m.openTunnelDashboard(0)
	press(t, m, run('p', 0, "p"))
	if m.Overlay != OverlayTunnelForm {
		t.Fatalf("p should open the form for another port, overlay=%v", m.Overlay)
	}
	if got := m.tunnelForm.value("port"); got == "8080" || got == "" {
		t.Errorf("the new port should differ from the running one, got %q", got)
	}

	// x stops it and, with nothing left, returns to the launcher.
	m.Overlay = OverlayNone
	m.openTunnelDashboard(0)
	press(t, m, run('x', 0, "x"))
	if len(m.tunnels) != 0 || m.screen != screenHome {
		t.Errorf("x should stop the last tunnel and go home, tunnels=%d screen=%v", len(m.tunnels), m.screen)
	}
}

// Starting a tunnel without a live session explains what is missing instead of
// opening a prompt that cannot work.
func TestTunnelNeedsALiveSession(t *testing.T) {
	m := newTestModel(t)
	m.Panes[0] = Pane{Side: SideRemote, Host: "contabo", Path: "/", Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 0
	m.runAction("session.tunnel")
	if m.Overlay == OverlayPrompt {
		t.Error("there is no session, so there is nothing to tunnel through")
	}
}

// The MCP card carries the configuration, because the server itself is a stdio
// process started by the agent, not a screen inside omssh.
func TestMCPCard(t *testing.T) {
	m := newTestModel(t)
	m.runAction("app.mcp")
	if m.Overlay != OverlayMCP {
		t.Fatalf("app.mcp should open the card, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"MCP", "mcpServers", "--allow-write", "--allow-host"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card should show %q:\n%s", want, out)
		}
	}
}

// The launcher offers Terminal and Tunnel only once something is connected, and
// the chooser asks which live session they are for.
func TestLauncherChoosesConnectedSessions(t *testing.T) {
	m := newTestModel(t)
	has := func(id string) bool {
		for _, item := range m.homeItems() {
			if string(item.ID) == id {
				return true
			}
		}
		return false
	}
	// The rows are always there: the chooser can connect a host that is not open
	// yet, which is what "choose a connection" means.
	for _, id := range []string{"app.session-tools", "app.history", "app.mcp"} {
		if !has(id) {
			t.Errorf("the launcher should offer %q", id)
		}
	}
	m.tabs = []Tab{{Title: "contabo", Host: "contabo", State: StateReady}}
	m.live = map[string]*session.Session{"contabo": {}}

	m.runAction("app.session-tools")
	if m.Overlay != OverlaySessionKind {
		t.Fatalf("the combined menu should ask terminal or tunnel, overlay=%v", m.Overlay)
	}
	press(t, m, keyNamed(tea.KeyDown))
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlaySessionChoose {
		t.Fatalf("the chooser should open, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"Tunnel", "contabo", "Manage connections"} {
		if !strings.Contains(out, want) {
			t.Errorf("the chooser should show %q:\n%s", want, out)
		}
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayTunnelForm || m.tunnelHost != "contabo" {
		t.Fatalf("Enter should ask how to tunnel on contabo, overlay=%v host=%q", m.Overlay, m.tunnelHost)
	}
	if m.tunnelForm.step != 0 {
		t.Errorf("the form should start with the direction question, step=%d", m.tunnelForm.step)
	}
}

// The MCP card shows what the agent did, from the log the server writes.
func TestMCPCardShowsRecentCalls(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "omssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"time":"2026-10-28T10:00:00Z","tool":"ls","place":"prod","path":"/srv/app","ok":true}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mcp.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t)
	m.runAction("app.mcp")
	out := ansi.Strip(render(m))
	for _, want := range []string{"Recent agent calls", "ls", "prod:/srv/app", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card should show %q:\n%s", want, out)
		}
	}
}

// An offline host is in the chooser too: picking it connects, then opens what was
// asked for. That is the "choose a connection" half the launcher was missing.
func TestSessionChooserIncludesOfflineHosts(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.screen = screenHome
	m.runAction("app.session-tools")
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlaySessionChoose {
		t.Fatalf("the chooser should open with no live session, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "prod") || !strings.Contains(out, "not connected") {
		t.Errorf("an offline host should be offered with its state:\n%s", out)
	}
	if !strings.Contains(out, "Manage connections") {
		t.Errorf("the way to add a host should be there too:\n%s", out)
	}
}

// Connecting creates the tab before the session exists. If that tab is what
// addSessionTab finds, the session still has to be remembered — otherwise the
// chooser keeps calling a connected host "not connected".
func TestConnectedSessionIsRememberedWithAnExistingTab(t *testing.T) {
	m := newTestModel(t)
	m.tabs = []Tab{{Title: "contabo", Host: "contabo", State: StateConnecting}}
	sessionLive := &session.Session{Target: ssh.Target{Alias: "contabo"}}
	m.addSessionTab(sessionLive)
	if m.live["contabo"] != sessionLive {
		t.Fatal("the live session should be recorded even when its tab already existed")
	}
	if m.tabs[0].State != StateReady {
		t.Errorf("the tab should be ready, state=%v", m.tabs[0].State)
	}

	// And the chooser must say so.
	m.tabs = []Tab{{Title: "contabo", Host: "contabo", State: StateReady}}
	m.runAction("app.session-tools")
	press(t, m, keyNamed(tea.KeyEnter))
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "contabo") || !strings.Contains(out, "connected") {
		t.Errorf("the chooser should mark the host connected:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "contabo") && strings.Contains(line, "not connected") {
			t.Errorf("a live host was offered as offline: %s", line)
		}
	}
}

// The remote cursor is drawn: Render() alone is text, so the cell under the cursor
// is reversed, and it is hidden when the remote hides it.
func TestEmbeddedCursorIsDrawn(t *testing.T) {
	emu := vt.NewEmulator(20, 4)
	_, _ = emu.WriteString("hello")
	lines := renderTerminalScreen(emu, true)
	if !strings.Contains(lines[0], "\x1b[7m") {
		t.Errorf("the cursor cell should be reversed: %q", lines[0])
	}
	_, _ = emu.WriteString("\x1b[?25l")
	if lines := renderTerminalScreen(emu, true); strings.Contains(lines[0], "\x1b[7m") {
		t.Errorf("a hidden cursor should not be drawn: %q", lines[0])
	}
}

// Ctrl+] detaches without ending the shell; asking for the host again reattaches.
func TestEmbeddedDetachAndReattach(t *testing.T) {
	m := newTestModel(t)
	state := &embeddedState{host: "contabo", emu: vt.NewEmulator(40, 10), width: 40, height: 10}
	m.embedded = state
	m.Overlay = OverlayTerminal
	m.detachEmbedded()
	if m.embedded != nil || m.Overlay != OverlayNone {
		t.Fatalf("detach should hide the terminal, embedded=%v overlay=%v", m.embedded, m.Overlay)
	}
	if m.detachedState("contabo") != state {
		t.Fatal("the detached terminal should still be running")
	}
	m.openEmbeddedHost("contabo")
	if m.embedded != state || m.Overlay != OverlayTerminal {
		t.Fatalf("asking again should reattach, embedded=%v overlay=%v", m.embedded, m.Overlay)
	}
	if m.detachedState("contabo") != nil {
		t.Error("a reattached terminal should leave the detached list")
	}

	// A shell that ends in the background is removed and its recorder closed.
	m.embedded = nil
	m.detached = []*embeddedState{state}
	m.handleEmbeddedExit(embeddedExitMsg{host: "contabo"})
	if m.detachedState("contabo") != nil {
		t.Error("an exited detached session should be removed")
	}
}
