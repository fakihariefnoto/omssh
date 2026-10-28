package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

// The tunnel form teaches the shapes instead of trusting the reader to know ssh.
func TestTunnelFormIsIllustrated(t *testing.T) {
	m := newTestModel(t)
	m.tunnelForm = tunnelFormState{host: "contabo"}
	m.tunnelForm.input.Set("L:8080:localhost:80")
	m.Overlay = OverlayTunnelForm
	out := ansi.Strip(render(m))
	for _, want := range []string{"Tunnel through contabo", "L local", "R remote", "D dynamic",
		"8080:db.internal:5432", "9000:localhost:3000", "1080", "spec"} {
		if !strings.Contains(out, want) {
			t.Errorf("the form should explain %q:\n%s", want, out)
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
