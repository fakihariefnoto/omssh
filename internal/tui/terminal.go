package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// A full shell is not something to reimplement. Shells, full-screen programs,
// colours and modes all already agree with the terminal in front of you, so "open
// a terminal" hands this terminal to ssh and takes it back when the session ends.
// It is the real thing: vim, top, tmux and the rest work because nothing is
// standing in their way.

// terminalDoneMsg reports that an interactive session ended.
type terminalDoneMsg struct {
	host string
	err  error
}

// openTerminal runs an interactive ssh client for one host. The alias is passed
// as-is: ssh already knows about ports, keys, jump hosts and every other option
// in ~/.ssh/config, and duplicating that knowledge here would only let the two
// drift apart.
func (m *Model) openTerminal(host string) tea.Cmd {
	if strings.TrimSpace(host) == "" {
		m.quitHint = "Put the cursor on a host first"
		return nil
	}
	m.mode = "TERMINAL"
	args := []string{"-t", host}
	cmd := exec.CommandContext(context.Background(), "ssh", args...) //nolint:gosec // the host is the user's own config entry
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return terminalDoneMsg{host: host, err: err}
	})
}

// openTerminalForPane opens a shell on the host the remote pane is showing, or a
// local shell when the local pane has the focus.
func (m *Model) openTerminalForPane() tea.Cmd {
	pane := m.Pane()
	if pane.Side == SideLocal {
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/sh"
		}
		m.mode = "TERMINAL"
		cmd := exec.CommandContext(context.Background(), shell) //nolint:gosec // the user's own shell
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return terminalDoneMsg{host: "local shell", err: err}
		})
	}
	if pane.Host == "" {
		m.quitHint = "This pane is not connected"
		return nil
	}
	if isCloudHost(pane.Host) {
		m.toastInfo("Object storage has no shell · t on a host opens one")
		return nil
	}
	return m.openTerminal(pane.Host)
}
