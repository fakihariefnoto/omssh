package tui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/remoteinfo"
)

// Four panes, two by two, is the dashboard's promise: four machines in one view.
// With more sessions than that, which four is a choice rather than a guess, so
// the choice is a screen: the local machine and every connected host, ticked.
//
// The rest of the tabs stay: a session that is not on screen is still open, and
// Tab brings it to a pane when you ask for it.

// panesState is the pane chooser's state.
type panesState struct {
	cursor int
	chosen map[string]bool
}

// sessionSampleMsg carries one host's numbers back to the card.
type sessionSampleMsg struct {
	alias string
	info  remoteinfo.Info
}

// sampleTickMsg asks for the numbers to be read again while the chooser is open.
type sampleTickMsg struct{}

// paneChoices is the list the chooser offers: local first, then the connected
// hosts in tab order, so the list matches the strip above it.
func (m *Model) paneChoices() []string {
	choices := []string{""} // the empty alias is the local pane
	seen := map[string]bool{}
	for _, tab := range m.tabs {
		if tab.Host == "" || seen[tab.Host] {
			continue
		}
		seen[tab.Host] = true
		choices = append(choices, tab.Host)
	}
	return choices
}

// openPanes shows the chooser with what is on screen already ticked.
func (m *Model) openPanes() {
	chosen := map[string]bool{"": true} // the local pane is always one of them
	for i := 0; i < m.paneCount(); i++ {
		if host := m.Panes[i].Host; host != "" {
			chosen[host] = true
		}
	}
	m.panes = panesState{chosen: chosen}
	m.Overlay = OverlayPanes
	m.samples = map[string]remoteinfo.Info{}
	m.sampleSeq = m.sampleSessions()
}

// sampleSessions asks every live host for its load, memory and disk, and asks
// again in a few seconds while the chooser stays open. It is the only place the
// interface runs a command on a host, which is why it is a screen you opened on
// purpose.
func (m *Model) sampleSessions() tea.Cmd {
	commands := make([]tea.Cmd, 0, len(m.live)+1)
	for alias, sess := range m.live {
		live := sess
		name := alias
		// The path is read here, not in the goroutine: the model is not safe to
		// touch from there.
		path := live.Base
		for i := 0; i < m.paneCount(); i++ {
			if m.Panes[i].Host == alias {
				path = m.Panes[i].Path
				break
			}
		}
		if path == "" {
			path = "/"
		}
		commands = append(commands, func() tea.Msg {
			client := live.Client.SSH()
			run := func(command string) (string, error) {
				session, err := client.NewSession()
				if err != nil {
					return "", err
				}
				defer func() { _ = session.Close() }()
				output, err := session.CombinedOutput(command)
				return string(output), err
			}
			return sessionSampleMsg{alias: name, info: remoteinfo.Sample(run, path)}
		})
	}
	commands = append(commands, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return sampleTickMsg{}
	}))
	return tea.Batch(commands...)
}

// handlePanesKey drives the chooser: space ticks, Enter applies, Escape keeps
// what was there.
func (m *Model) handlePanesKey(msg tea.KeyPressMsg) {
	choices := m.paneChoices()
	switch msg.String() {
	case "esc":
		m.Overlay = OverlayNone
	case "up", "k":
		m.panes.cursor = (m.panes.cursor + len(choices) - 1) % len(choices)
	case "down", "j":
		m.panes.cursor = (m.panes.cursor + 1) % len(choices)
	case "space", "x":
		alias := choices[clamp(m.panes.cursor, 0, len(choices)-1)]
		if alias == "" {
			m.toastInfo("The local pane stays: it is where you started")
			break
		}
		if m.panes.chosen[alias] {
			delete(m.panes.chosen, alias)
			break
		}
		if len(m.panes.chosen) >= maxPanes {
			m.toastInfo("Four panes is the limit · untick one first")
			break
		}
		m.panes.chosen[alias] = true
	case "enter":
		m.applyPanes()
	}
}

// applyPanes rebuilds the pane list from the ticks, keeping the order the list
// showed.
func (m *Model) applyPanes() {
	m.panesAsked = true
	chosen := m.panes.chosen
	panes := make([]Pane, 0, maxPanes)
	local := NewPane(m.Panes[0].FS, m.Panes[0].Path, m.Panes[0].Home)
	if m.Panes[0].FS == nil {
		local = m.Panes[0]
	}
	panes = append(panes, local)

	for _, alias := range m.paneChoices() {
		if alias == "" || !chosen[alias] {
			continue
		}
		sess, live := m.live[alias]
		if !live {
			continue
		}
		pane := NewRemotePane()
		pane.Bind(sess.FS, sess.Target.Alias, sess.Root)
		pane.Base = sess.Base
		panes = append(panes, pane)
	}
	if len(panes) == 0 {
		panes = append(panes, m.Panes[0])
	}
	m.Panes = panes
	m.Focused = min(m.Focused, len(m.Panes)-1)
	m.syncActiveTab()
	m.Overlay = OverlayNone
	m.mode = "NORMAL"
	m.toastInfo(fmt.Sprintf("%d %s on screen · w moves between them",
		len(m.Panes), plural(len(m.Panes), "pane", "panes")))

	// Every pane needs its listing: they were rebuilt, not moved.
	for i := range m.Panes {
		m.loadPaneAsync(i)
	}
}

// sessionCard is one card on the chooser: everything the interface knows about a
// session, without opening it.
type sessionCard struct {
	alias  string
	title  string
	target string
	path   string
	state  string
	info   remoteinfo.Info
	local  bool
}

// sessionCards is the list of cards, in tab order with the local machine first.
func (m *Model) sessionCards() []sessionCard {
	cards := make([]sessionCard, 0, len(m.tabs))
	localPath := ""
	if len(m.Panes) > 0 {
		localPath = m.Panes[0].Path
	}
	cards = append(cards, sessionCard{
		alias:  "",
		title:  "local machine",
		target: fmt.Sprintf("%d CPUs · this terminal", runtime.NumCPU()),
		path:   localPath,
		state:  "ready",
		local:  true,
	})
	for _, alias := range m.paneChoices()[1:] {
		card := sessionCard{alias: alias, title: alias}
		if live, ok := m.live[alias]; ok {
			target := live.Target
			card.target = fmt.Sprintf("%s@%s:%d", target.User, target.Host, target.Port)
			if target.Host == "" {
				card.target = "from ~/.ssh/config"
			}
			card.path = live.Base
			for i := 0; i < m.paneCount(); i++ {
				if m.Panes[i].Host == alias {
					card.path = m.Panes[i].Path
					break
				}
			}
			if info, ok := m.samples[alias]; ok {
				card.info = info
			}
		}
		for _, tab := range m.tabs {
			if tab.Host == alias {
				card.state = tab.State.Label()
			}
		}
		cards = append(cards, card)
	}
	return cards
}

// renderPanesChooser draws the chooser as a wall of cards: a name, where it is,
// what it is doing, and whether it is on screen. The numbers are the point — a
// server that is out of memory is a bad place to start browsing.
func (m *Model) renderPanesChooser(l layout) string {
	width := clamp(l.Width-6, 60, 124)
	// The outer card's own border and padding take six cells; the wall of cards
	// has to fit inside what is left.
	inner := width - 6

	cards := m.sessionCards()
	columns := 1
	if width >= 92 {
		columns = 2
	}
	cardWidth := inner
	if columns == 2 {
		cardWidth = (inner - 1) / 2
	}

	lines := []string{
		m.st.CardTitle.Render("Sessions") + "  " +
			m.st.CardMuted.Render("choose up to four for the dashboard"),
		"",
	}

	// As many rows of cards as fit, scrolling with the cursor.
	const cardRows = 8
	perPage := max(columns, (l.Height-6)/cardRows*columns)
	start := clamp(m.panes.cursor, 0, max(0, len(cards)-1))
	start = max(0, min(start-(perPage-1), len(cards)-perPage))
	start = max(0, start)

	for i := start; i < len(cards); i += columns {
		if len(lines)+cardRows > l.Height-3 {
			break
		}
		rendered := make([]string, 0, columns)
		for column := 0; column < columns && i+column < len(cards); column++ {
			rendered = append(rendered, m.renderSessionCard(cards[i+column], i+column == m.panes.cursor, cardWidth))
		}
		if len(rendered) == 1 {
			rendered = append(rendered, "")
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, rendered[0], " ", rendered[1]))
	}

	if len(cards) == 1 {
		lines = append(lines, "", m.st.PaneEmpty.Render("No sessions yet · Ctrl+O connects"))
	}
	lines = append(lines, "",
		m.st.CardMuted.Render(truncate(fmt.Sprintf(
			"%d of %d chosen · arrows choose · Space picks · Enter shows · Esc keeps",
			len(m.panes.chosen), maxPanes), inner)))
	return m.card(width, lines)
}

// renderSessionCard draws one session. The chosen cards get the accent border, so
// the wall reads at a glance.
func (m *Model) renderSessionCard(card sessionCard, selected bool, width int) string {
	// A card style's Width includes its border and its two cells of padding on
	// each side, so the text has this much room.
	inner := width - 6
	line := func(left, right string) string { return spread(left, right, inner) }

	tick := "[ ]"
	if m.panes.chosen[card.alias] {
		tick = "[x]"
	}
	if card.alias == "" {
		tick = "[x]"
	}
	title := m.st.CardText.Render(card.title)
	if selected {
		title = m.st.Accent.Bold(true).Render(card.title)
	}
	body := []string{
		line(title, m.st.CardMuted.Render(tick)),
		m.st.CardMuted.Render(truncate(card.target, inner)),
		m.st.CardText.Render(truncate(contractHomePath(card.path), inner)),
		"",
	}
	switch {
	case card.local:
		body = append(body, m.st.CardMuted.Render(truncate("the machine you are on", inner)))
	case card.info.Known:
		body = append(body,
			m.bar("load", fmt.Sprintf("%.2f", card.info.Load), card.info.LoadPercent(), inner),
			m.bar("mem", sizePair(card.info.MemUsed, card.info.MemTotal), card.info.MemPercent(), inner),
			m.bar("disk", sizePair(card.info.DiskTotal-card.info.DiskFree, card.info.DiskTotal), card.info.DiskPercent(), inner),
		)
	default:
		body = append(body,
			m.st.CardMuted.Render("load   —"),
			m.st.CardMuted.Render("mem    —"),
			m.st.CardMuted.Render("disk   —"),
		)
	}
	// Every card is the same height, so a wall of them stays a wall: a short
	// local card must not leave a gap under its neighbour.
	for len(body) < 7 {
		body = append(body, "")
	}
	body = append(body, line(m.st.CardMuted.Render(card.state), m.st.CardMuted.Render(func() string {
		if m.paneShowsOnScreen(card.alias) {
			return "on screen"
		}
		return ""
	}())))

	style := m.st.Card
	if selected {
		style = m.st.Card.BorderForeground(m.st.Accent.GetForeground())
	}
	return style.Width(width).Render(strings.Join(body, "\n"))
}

// sizePair renders "used/total" for a byte count, clamped so a nonsense value
// from a host cannot become a negative size.
func sizePair(used, total uint64) string {
	const maxInt64 = uint64(1<<63 - 1)
	if used > maxInt64 {
		used = maxInt64
	}
	if total > maxInt64 {
		total = maxInt64
	}
	return humanSize(int64(used)) + "/" + humanSize(int64(total))
}

// bar is one labelled figure with a proportional bar: the shape says "busy" or
// "fine" before the number is read.
func (m *Model) bar(label, value string, percent, width int) string {
	const barWidth = 10
	filled := clamp(percent*barWidth/100, 0, barWidth)
	style := m.st.Success
	switch {
	case percent >= 80:
		style = m.st.Danger
	case percent >= 60:
		style = m.st.Warning
	}
	bar := style.Render(strings.Repeat("█", filled)) + m.st.CardMuted.Render(strings.Repeat("░", barWidth-filled))
	row := fmt.Sprintf("%-4s %-16s %s %3d%%", label, value, bar, percent)
	return ansi.Truncate(row, width, "…")
}

// paneShowsOnScreen reports whether a host is in one of the panes now.
func (m *Model) paneShowsOnScreen(alias string) bool {
	for i := 0; i < m.paneCount(); i++ {
		host := m.Panes[i].Host
		if alias == "" && host == "" && m.Panes[i].Side == SideLocal {
			return true
		}
		if alias != "" && host == alias {
			return true
		}
	}
	return false
}
