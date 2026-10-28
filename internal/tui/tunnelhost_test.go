package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/tunnel"
)

// A connected host with saved or running forwards opens a per-host menu, and the
// saved rows say which side is server and which is local.
func TestTunnelHostMenuShowsSavedAndRunning(t *testing.T) {
	m := newTestModel(t)
	m.live = map[string]*session.Session{"ovh": {Target: omssh.Target{Alias: "ovh", Host: "15.235.198.5"}}}
	m.cfg.Tunnels.Profiles = map[string]config.TunnelProfile{
		"web": {Host: "ovh", Kind: "remote", Listen: "0.0.0.0:9000", Target: "127.0.0.1:8080"},
		"db":  {Host: "ovh", Kind: "local", Listen: "127.0.0.1:8080", Target: "db:5432"},
	}
	m.tunnels = []*activeTunnel{{id: 1, sessionID: "s1", host: "ovh", kind: tunnel.Local,
		listen: "127.0.0.1:8080", target: "db:5432"}}

	m.openTunnelForHost("ovh")
	if m.Overlay != OverlayTunnelHost {
		t.Fatalf("a host with saved tunnels should open its menu, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{
		"Tunnel · ovh", "New tunnel", "Running tunnels", "1 on ovh",
		"db", "local 127.0.0.1:8080 → server db:5432",
		"web", "server 0.0.0.0:9000 → local 127.0.0.1:8080",
		"Back to hosts",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the menu should show %q:\n%s", want, out)
		}
	}

	// Enter on Running opens the live dashboard.
	// rows: new(0), running(1), saved db(2), saved web(3), back(4)
	m.tunnelHostMenu.cursor = 1
	press(t, m, keyNamed(tea.KeyEnter))
	if m.screen != screenTunnels {
		t.Errorf("Running should open the dashboard, screen=%v", m.screen)
	}
}

// The host chooser says what a connected host already has, and Enter goes to the
// menu rather than straight to a blank form.
func TestTunnelChooserSummaryPerHost(t *testing.T) {
	m := newTestModel(t)
	m.live = map[string]*session.Session{"ovh": {Target: omssh.Target{Alias: "ovh", Host: "15.235.198.5"}}}
	m.cfg.Tunnels.Profiles = map[string]config.TunnelProfile{
		"web": {Host: "ovh", Kind: "remote", Listen: "0.0.0.0:9000", Target: "127.0.0.1:8080"},
	}
	m.tabs = []Tab{{Title: "ovh", Host: "ovh", State: StateReady}}
	m.openSessionChooser("tunnel")
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "1 saved") {
		t.Errorf("the host row should say it has a saved tunnel:\n%s", out)
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayTunnelHost {
		t.Errorf("Enter on the host should open its menu, overlay=%v", m.Overlay)
	}
}

// A host with nothing saved still goes straight to the form.
func TestTunnelHostWithoutChoicesOpensForm(t *testing.T) {
	m := newTestModel(t)
	m.live = map[string]*session.Session{"ovh": {Target: omssh.Target{Alias: "ovh", Host: "h"}}}
	m.openTunnelForHost("ovh")
	if m.Overlay != OverlayTunnelForm {
		t.Errorf("an empty host should go to the form, overlay=%v", m.Overlay)
	}
}
