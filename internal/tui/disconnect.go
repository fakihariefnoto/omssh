package tui

import tea "charm.land/bubbletea/v2"

// Disconnecting is not the same as closing the tab. Closing says "I am done with
// this host"; disconnecting says "stop using the connection, but leave the place
// in the interface where I will come back to it". The tab stays, greyed, and
// choosing it reconnects.

// disconnectPane ends the session behind the focused pane and leaves the pane
// empty rather than pointing at a connection that is gone.
func (m *Model) disconnectPane() tea.Cmd {
	pane := m.Pane()
	if pane.Side != SideRemote || pane.Host == "" {
		m.quitHint = "Nothing to disconnect: this pane is local or empty"
		return nil
	}
	return m.disconnectHost(pane.Host)
}

// disconnectHost ends one session by alias.
func (m *Model) disconnectHost(alias string) tea.Cmd {
	if alias == "" {
		m.quitHint = "Put the cursor on a connected host first"
		return nil
	}
	if sess, live := m.live[alias]; live {
		if m.sessions != nil {
			_ = m.sessions.Close(sess.ID)
		}
		delete(m.live, alias)
	}

	// The panes must not keep a filesystem that belongs to a closed connection.
	for i := range m.Panes {
		if m.Panes[i].Host != alias {
			continue
		}
		if m.stagedCount() > 0 && m.staged.From == i {
			m.clearStaged()
		}
		m.Panes[i] = NewRemotePane()
	}
	m.tabState(alias, StateIdle)
	if m.activeTab >= 0 && m.activeTab < len(m.tabs) && m.tabs[m.activeTab].Host == alias {
		m.Focused = 0
		m.syncActiveTab()
	}
	m.toastInfo("Disconnected from " + alias + " · its tab reconnects when chosen")
	return nil
}
