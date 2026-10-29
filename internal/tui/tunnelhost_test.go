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

// The server check reads sshd and the listening socket, and says which fact
// decided the outcome.
func TestServerCheckLines(t *testing.T) {
	loopback := tunnelCheckMsg{host: "ovh", port: "9000", output: "gatewayports no\nallowtcpforwarding yes\n---\n" +
		"LISTEN 0 128 127.0.0.1:9000 0.0.0.0:*"}
	out := strings.Join(serverCheckLines(loopback), "\n")
	for _, want := range []string{"GatewayPorts is no", "loopback only", "GatewayPorts clientspecified"} {
		if !strings.Contains(out, want) {
			t.Errorf("loopback check should say %q:\n%s", want, out)
		}
	}

	public := tunnelCheckMsg{host: "ovh", port: "9000", output: "gatewayports clientspecified\nallowtcpforwarding yes\n---\n" +
		"LISTEN 0 128 0.0.0.0:9000 0.0.0.0:*"}
	out = strings.Join(serverCheckLines(public), "\n")
	if !strings.Contains(out, "listening publicly") {
		t.Errorf("public check should say it is listening publicly:\n%s", out)
	}
	if !strings.Contains(out, "does not change sshd") {
		t.Errorf("the check should say it is read-only:\n%s", out)
	}
}

// Starting a tunnel writes it to the config, once: the same forward reuses its
// profile, a different port gets its own.
func TestTunnelAutosavesConfig(t *testing.T) {
	m := newTestModel(t)
	m.configPath = t.TempDir() + "/config.yaml"
	spec := tunnel.Spec{Kind: tunnel.Local, Listen: "127.0.0.1:8080", Target: "db:5432"}
	name := m.autosaveTunnel("ovh", spec)
	if name == "" {
		t.Fatal("autosave should name the profile")
	}
	profile, ok := m.cfg.Tunnels.Profiles[name]
	if !ok || profile.Host != "ovh" || profile.Kind != "local" {
		t.Fatalf("profile = %+v, %v", profile, ok)
	}
	if again := m.autosaveTunnel("ovh", spec); again != name {
		t.Errorf("an identical forward should reuse %q, got %q", name, again)
	}
	if len(m.cfg.Tunnels.Profiles) != 1 {
		t.Errorf("profiles = %d, want 1", len(m.cfg.Tunnels.Profiles))
	}
	other := spec
	other.Listen = "127.0.0.1:8081"
	if second := m.autosaveTunnel("ovh", other); second == name {
		t.Errorf("a different port needs its own name, got %q", second)
	}
	if len(m.cfg.Tunnels.Profiles) != 2 {
		t.Errorf("profiles = %d, want 2", len(m.cfg.Tunnels.Profiles))
	}

	// And it survives a restart, which is the point.
	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tunnels.Profiles) != 2 {
		t.Errorf("saved file has %d profiles, want 2", len(cfg.Tunnels.Profiles))
	}
}
