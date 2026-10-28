package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// twoHosts builds a model with two live hosts plus the local tab.
func twoHosts(t *testing.T) *Model {
	t.Helper()
	m, _ := newConnectModel(t, session.PrompterFunc{})
	dir := t.TempDir()
	for _, alias := range []string{"contabo", "staging"} {
		fsys, err := vfs.NewLocal(dir)
		if err != nil {
			t.Fatal(err)
		}
		m.addSessionTab(&session.Session{
			ID: alias, Target: omssh.Target{Alias: alias}, FS: fsys, Root: "/", Base: dir,
		})
		m.paneForSession(m.live[alias])
	}
	m.screen = screenFiles
	return m
}

// Tab is for hosts, not for panes: with several sessions, the key that says
// "next" should take you to the next session.
func TestTabCyclesHostsAndWSwitchesPanes(t *testing.T) {
	m := twoHosts(t)
	m.activeTab = 1
	press(t, m, keyNamed(tea.KeyTab))
	if m.activeTab != 2 {
		t.Errorf("Tab should move to the next session, active tab is %d", m.activeTab)
	}
	press(t, m, keyNamed(tea.KeyTab))
	if m.activeTab != 0 {
		t.Errorf("Tab should wrap to the local tab, active tab is %d", m.activeTab)
	}

	// The pane switch still exists, on w.
	m.activeTab = 1
	before := m.Focused
	press(t, m, run('w', 0, "w"))
	if m.Focused == before {
		t.Error("w should switch panes")
	}
}

// The sidebar is built from the tabs: one row per host, no duplicates, and the
// row that is marked is the open session rather than the focused pane.
func TestSidebarFollowsTheOpenSession(t *testing.T) {
	m := twoHosts(t)
	m.activeTab = 1
	// Move the keys to the local pane: the open session is still contabo.
	m.Focused = 0

	out := ansi.Strip(render(m))
	if n := strings.Count(out, "contabo"); n > 3 {
		// Tab strip, sidebar row, pane breadcrumb: three mentions of one session.
		// A fourth would mean a duplicate row.
		t.Errorf("contabo should appear at most three times, found %d:\n%s", n, out)
	}
	if hosts := m.liveSessions(); len(hosts) != 2 {
		t.Errorf("the sidebar should list each host once, got %+v", hosts)
	}
	for _, host := range m.liveSessions() {
		if host.alias == "contabo" && !host.active {
			t.Error("the open session should be the marked row")
		}
		if host.alias == "staging" && host.active {
			t.Error("a session that is not open should not be marked")
		}
	}

	// Closing the tab removes its row immediately.
	m.activeTab = 2
	m.closeTab(2)
	for _, host := range m.liveSessions() {
		if host.alias == "staging" {
			t.Error("a closed session should be gone from the sidebar")
		}
	}
	if out := ansi.Strip(render(m)); strings.Count(out, "staging") > 0 {
		// The connections screen may still name it, but the sidebar must not.
		for _, host := range m.liveSessions() {
			if host.alias == "staging" {
				t.Errorf("staging should no longer be listed:\n%s", out)
			}
		}
	}
}

// The settings card can hide dotfiles, the preference the config calls
// show_hidden, and it is saved.
func TestSettingsTogglesHiddenFiles(t *testing.T) {
	m := newTestModel(t)
	m.configPath = t.TempDir() + "/config.yaml"
	if !m.showHidden {
		t.Fatal("dotfiles should be visible by default")
	}
	m.overlayCmd = m.toggleShowHidden()
	if m.showHidden {
		t.Error("Enter should hide dotfiles")
	}
	for _, e := range m.Pane().Visible() {
		if e.Name == ".hidden" {
			t.Error("a hidden file is still listed")
		}
	}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.ShowHidden {
		t.Error("the preference should be saved as hidden")
	}

	// And back.
	m.toggleShowHidden()
	if !m.showHidden {
		t.Error("toggling again should show them")
	}
}

// A theme choice is a preference: it must survive the process.
func TestThemeChoiceIsPersisted(t *testing.T) {
	m := newTestModel(t)
	m.configPath = t.TempDir() + "/config.yaml"
	m.applyTheme("dawn")

	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatalf("the config should be valid: %v", err)
	}
	if cfg.UI.Theme != "dawn" {
		t.Errorf("the theme should be saved, got %q", cfg.UI.Theme)
	}
}

// The settings card can turn background painting off, for a terminal that was
// coloured by hand.
func TestBackgroundModeCycles(t *testing.T) {
	m := newTestModel(t)
	m.configPath = t.TempDir() + "/config.yaml"
	m.cfg.UI.PaintBackground = "auto"

	m.cycleBackground()
	if m.cfg.UI.PaintBackground != "always" {
		t.Errorf("auto should become always, got %q", m.cfg.UI.PaintBackground)
	}
	m.cycleBackground()
	if m.cfg.UI.PaintBackground != "never" {
		t.Errorf("always should become never, got %q", m.cfg.UI.PaintBackground)
	}
	m.cycleBackground()
	if m.cfg.UI.PaintBackground != "auto" {
		t.Errorf("never should wrap to auto, got %q", m.cfg.UI.PaintBackground)
	}

	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.PaintBackground != "auto" {
		t.Errorf("the mode should be saved, got %q", cfg.UI.PaintBackground)
	}
}

// Keys and help are part of settings, not a second menu row that answers half of
// the same question.
func TestSettingsCarriesTheKeymap(t *testing.T) {
	m := newTestModel(t)
	press(t, m, run(',', tea.ModCtrl, ""))
	if m.Overlay != OverlaySettings {
		t.Fatal("Ctrl+, should open settings")
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "Keyboard and help") {
		t.Fatalf("settings should offer the keymap:\n%s", out)
	}

	// Put the cursor on the keymap row (after the themes and the background row)
	// and open it.
	m.settings.cursor = len(m.settingsOptions()) + 4
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayHelp {
		t.Errorf("Enter on the keymap row should open help, overlay is %v", m.Overlay)
	}
}

// The recording toggle is a real setting: it flips the config and saves it, so a
// terminal recorded tomorrow still lands in History.
func TestSettingsToggleRecording(t *testing.T) {
	m := newTestModel(t)
	m.configPath = t.TempDir() + "/config.yaml"
	m.cfg.Sessions.Record = true
	m.toggleRecording()
	if m.cfg.Sessions.Record {
		t.Error("the toggle should turn recording off")
	}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sessions.Record {
		t.Error("the saved config should say recording is off")
	}
	m.toggleRecording()
	if !m.cfg.Sessions.Record {
		t.Error("a second toggle should turn it back on")
	}
}

// The interface must be told where to save. A New() that drops Options.ConfigPath
// writes nothing, and every preference silently resets — which is exactly what
// happened before this test existed.
func TestNewKeepsTheConfigPath(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	m, err := New(Options{
		Config:     config.Default(),
		Profile:    colorprofile.TrueColor,
		HasDark:    true,
		Dir:        t.TempDir(),
		ConfigPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.configPath != path {
		t.Fatalf("configPath = %q, want %q", m.configPath, path)
	}

	m.applyTheme("dawn")
	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("the theme should have been written to %s: %v", path, err)
	}
	if saved.UI.Theme != "dawn" {
		t.Errorf("saved theme = %q, want dawn", saved.UI.Theme)
	}
}
