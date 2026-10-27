package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/session"
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
	m.openRecordings()
	if m.Overlay != OverlayRecordings {
		t.Fatalf("recordings should open, overlay=%v", m.Overlay)
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "contabo-20260101-120000") {
		t.Errorf("the list should name the recording:\n%s", out)
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
