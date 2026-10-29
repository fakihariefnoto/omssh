package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"github.com/fakihariefnoto/omssh/internal/session"
)

// Every full screen says the product name beside its own, so a screenshot or a
// half-remembered window still answers "what is this".
func TestFullScreensCarryTheProductName(t *testing.T) {
	contains := func(t *testing.T, out, want string) {
		t.Helper()
		if !strings.Contains(ansi.Strip(out), want) {
			t.Errorf("screen should show %q:\n%s", want, ansi.Strip(out))
		}
	}

	t.Run("manage connections", func(t *testing.T) {
		m, _ := newConnectionsModel(t, connFixture)
		contains(t, render(m), "OMSSH | Manage Connections")
	})

	t.Run("choose sessions", func(t *testing.T) {
		m, _ := fourPanes(t)
		m.openPanes()
		contains(t, render(m), "OMSSH | Choose Sessions")
	})

	t.Run("dashboard", func(t *testing.T) {
		m := newTestModel(t)
		m.screen = screenFiles
		contains(t, render(m), "OMSSH | Dashboard")
	})

	t.Run("tunnels", func(t *testing.T) {
		m := newTestModel(t)
		m.tunnels = []*activeTunnel{{id: 1, host: "contabo", kind: "local",
			listen: "127.0.0.1:8080", target: "db:5432"}}
		m.openTunnelDashboard(0)
		contains(t, render(m), "OMSSH | Tunnels")
	})

	t.Run("embedded terminal", func(t *testing.T) {
		m := newTestModel(t)
		m.screen = screenFiles
		m.embedded = &embeddedState{host: "contabo", emu: vt.NewEmulator(40, 5)}
		m.Overlay = OverlayTerminal
		contains(t, render(m), "OMSSH | Embedded Terminal")
	})

	t.Run("history", func(t *testing.T) {
		historyFixture(t)
		m := newTestModel(t)
		m.openHistory(3)
		contains(t, render(m), "OMSSH | History")
	})

	t.Run("mcp agents", func(t *testing.T) {
		m := newTestModel(t)
		m.Overlay = OverlayMCP
		contains(t, render(m), "OMSSH | MCP Agents")
	})

	t.Run("settings and themes", func(t *testing.T) {
		m := newTestModel(t)
		m.Overlay = OverlaySettings
		contains(t, render(m), "OMSSH | Settings and themes")
	})

	t.Run("replay", func(t *testing.T) {
		m := newTestModel(t)
		rec := &session.Recording{Header: session.RecordingHeader{Version: 2, Width: 40, Height: 5}}
		m.replay = &replayState{rec: rec, emu: vt.NewEmulator(40, 5), speed: 1}
		m.Overlay = OverlayReplay
		contains(t, render(m), "OMSSH | Replay")
	})
}
