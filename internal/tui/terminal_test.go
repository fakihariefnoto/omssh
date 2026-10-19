package tui

import "testing"

// "Open a terminal" hands this terminal to ssh: the interface has to know it is
// out of the way until the shell ends.
func TestTerminalHandsOverTheTerminal(t *testing.T) {
	m := newTestModel(t)
	cmd := m.openTerminal("contabo")
	if cmd == nil {
		t.Fatal("opening a terminal should start ssh")
	}
	if m.mode != "TERMINAL" {
		t.Errorf("the mode should say TERMINAL, got %q", m.mode)
	}

	// No host: say so rather than running ssh with an empty argument.
	if cmd := m.openTerminal("  "); cmd != nil {
		t.Error("an empty host should not start a process")
	}
}

// From a connected remote pane, T opens a shell on that host.
func TestTerminalUsesThePaneHost(t *testing.T) {
	m := newTestModel(t)
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: m.Panes[0].FS, Path: "/"}
	m.Focused = 1
	if cmd := m.openTerminalForPane(); cmd == nil {
		t.Fatal("T on a remote pane should start a shell")
	}
	if m.mode != "TERMINAL" {
		t.Errorf("the mode should say TERMINAL, got %q", m.mode)
	}
}
