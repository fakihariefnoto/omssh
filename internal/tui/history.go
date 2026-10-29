package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	"github.com/fakihariefnoto/omssh/internal/tunnel"
)

// History is the record of what happened: terminal recordings, tunnel sessions
// and the calls an agent made. They are different shapes, so the screen has tabs,
// and the launcher opens it on the tab a person asked for.

type historyKind string

const (
	historyTerminal historyKind = "terminal"
	historyTunnel   historyKind = "tunnel"
	historyAgent    historyKind = "agent"
)

var historyTabs = []struct {
	kind  historyKind
	label string
}{
	{historyTerminal, "Terminal"},
	{historyTunnel, "Tunnel"},
	{historyAgent, "Agent"},
	{"all", "All"},
}

// historyEntry is one row: a recording, a tunnel session or an agent call.
type historyEntry struct {
	kind    historyKind
	when    time.Time
	label   string
	detail  string
	path    string // terminal recording
	session string // tunnel session id
	key     string // agent line identity
	ok      bool
	// tunnel holds the grouped events for a tunnel entry.
	tunnel *tunnelSession
}

// tunnelSession is a tunnel's start and everything it did.
type tunnelSession struct {
	id, host, listen, target string
	kind                     tunnel.Kind
	start, last              time.Time
	conns                    int
	bytesIn, bytesOut        int64
	errs                     []string
	events                   []tunnel.Event
}

// historyState is the screen's tab, cursor and scroll.
type historyState struct {
	tab    int
	cursor int
	offset int
	all    []historyEntry
}

// openHistory shows the screen on a tab: 0 terminal, 1 tunnel, 2 agent, 3 all.
func (m *Model) openHistory(tab int) {
	m.history = historyState{tab: clamp(tab, 0, len(historyTabs)-1)}
	m.history.all = m.loadHistory()
	m.screen = screenHistory
	m.mode = "HISTORY"
}

// loadHistory reads all three sources, newest first.
func (m *Model) loadHistory() []historyEntry {
	entries := append(terminalHistory(), tunnelHistory()...)
	entries = append(entries, agentHistory()...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].when.After(entries[j].when) })
	return entries
}

// entries is what the current tab shows.
func (m *Model) historyEntries() []historyEntry {
	if m.history.tab == len(historyTabs)-1 {
		return m.history.all
	}
	kind := historyTabs[m.history.tab].kind
	out := make([]historyEntry, 0, len(m.history.all))
	for _, entry := range m.history.all {
		if entry.kind == kind {
			out = append(out, entry)
		}
	}
	return out
}

// terminalHistory lists .cast files.
func terminalHistory() []historyEntry {
	files := recordingFiles()
	out := make([]historyEntry, 0, len(files))
	for _, file := range files {
		out = append(out, historyEntry{kind: historyTerminal, when: file.when,
			label: filepath.Base(file.path), detail: humanSize(file.size), path: file.path})
	}
	return out
}

// tunnelHistory groups tunnels.jsonl by session.
func tunnelHistory() []historyEntry {
	dir, err := config.StateDir()
	if err != nil {
		return nil
	}
	file, err := os.Open(filepath.Join(dir, "tunnels.jsonl"))
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	sessions := map[string]*tunnelSession{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var event tunnel.Event
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Session == "" || event.Active {
			continue // old lines, or live progress rather than a finished connection
		}
		state := sessions[event.Session]
		if state == nil {
			state = &tunnelSession{id: event.Session, kind: event.Kind, start: event.Time}
			sessions[event.Session] = state
		}
		state.events = append(state.events, event)
		if event.Host != "" {
			state.host = event.Host
		}
		if event.Listen != "" {
			state.listen = event.Listen
		}
		if event.Target != "" {
			state.target = event.Target
		}
		if event.Time.After(state.last) {
			state.last = event.Time
		}
		if event.Client != "" {
			state.conns++
			state.bytesIn += event.BytesIn
			state.bytesOut += event.BytesOut
		}
		if event.Error != "" {
			state.errs = append(state.errs, event.Error)
		}
	}

	out := make([]historyEntry, 0, len(sessions))
	for _, state := range sessions {
		label := fmt.Sprintf("%s %s", state.kind, state.listen)
		if state.target != "" {
			label += " → " + state.target
		}
		if state.host != "" {
			label = state.host + " · " + label
		}
		out = append(out, historyEntry{kind: historyTunnel, when: state.last,
			label: label, detail: fmt.Sprintf("%d conns · %s", state.conns, humanSize(state.bytesIn)),
			session: state.id, tunnel: state})
	}
	return out
}

// agentHistory lists MCP tool calls. An agent is not a session, but its calls are
// part of what happened, and they are marked as such wherever they appear.
func agentHistory() []historyEntry {
	dir, err := config.StateDir()
	if err != nil {
		return nil
	}
	file, err := os.Open(filepath.Join(dir, "mcp.jsonl"))
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	out := make([]historyEntry, 0, 32)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var call struct {
			Time  string `json:"time"`
			Tool  string `json:"tool"`
			Place string `json:"place"`
			Path  string `json:"path"`
			OK    bool   `json:"ok"`
		}
		if json.Unmarshal(scanner.Bytes(), &call) != nil {
			continue
		}
		when, _ := time.Parse(time.RFC3339, call.Time)
		where := strings.Trim(call.Place+":"+call.Path, ":")
		label := call.Tool
		if where != "" {
			label += " · " + where
		}
		out = append(out, historyEntry{kind: historyAgent, when: when, label: label,
			ok: call.OK, key: call.Time + "|" + call.Tool + "|" + call.Path,
			detail: map[bool]string{true: "ok", false: "failed"}[call.OK]})
	}
	return out
}

// renderHistory draws History as two panels like the tunnel dashboard: a list
// to scan and a detail panel that answers "what is this?" before opening it.
func (m *Model) renderHistory(l layout) string {
	entries := m.historyEntries()
	width, height := l.Width, l.Height
	leftW := clamp(width*52/100, 46, 84)
	rightW := max(30, width-leftW-3)
	panelRows := max(5, height-9)

	counts := map[historyKind]int{}
	for _, entry := range m.history.all {
		counts[entry.kind]++
	}

	// Heading and tabs.
	header := spread(m.brandScreen("History"),
		m.st.StatusMuted.Render(fmt.Sprintf("%d %s", len(entries), plural(len(entries), "entry", "entries"))), width)
	pills := make([]string, 0, len(historyTabs))
	for i, tab := range historyTabs {
		label := tab.label
		if tab.kind == "all" {
			label += fmt.Sprintf(" %d", len(m.history.all))
		} else {
			label += fmt.Sprintf(" %d", counts[tab.kind])
		}
		if i == m.history.tab {
			pills = append(pills, m.st.PaletteItemActive.Render(" "+label+" "))
			continue
		}
		pills = append(pills, m.st.CardMuted.Render(" "+label+" "))
	}

	// The list: TYPE, entry, when. One line per record, selection filled.
	list := []string{
		spread(m.st.PaneHeader.Render("TYPE    ENTRY"), m.st.PaneHeader.Render("WHEN"), leftW-6),
		m.st.CardMuted.Render(strings.Repeat("─", max(1, leftW-6))),
	}
	listRows := max(1, panelRows-2)
	m.history.cursor = clamp(m.history.cursor, 0, max(0, len(entries)-1))
	if m.history.cursor < m.history.offset {
		m.history.offset = m.history.cursor
	}
	if m.history.cursor >= m.history.offset+listRows {
		m.history.offset = m.history.cursor - listRows + 1
	}
	badges := map[historyKind]string{
		historyTerminal: "term", historyTunnel: "tunnel", historyAgent: "agent",
	}
	for i := m.history.offset; i < len(entries) && len(list) < panelRows; i++ {
		entry := entries[i]
		label := truncate(entry.label, leftW-6-8-13-1)
		when := ""
		if !entry.when.IsZero() {
			when = entry.when.Format("Jan 02 15:04")
		}
		content := leftW - 6
		line := spread(m.st.CardMuted.Render(padRight(badges[entry.kind], 8))+m.st.CardText.Render(label),
			m.st.CardMuted.Render(when), content)
		if i == m.history.cursor {
			line = m.st.PaletteItemActive.Width(content).Render(padVisible(ansi.Strip(line), content))
		}
		list = append(list, line)
	}
	if len(entries) == 0 {
		list = append(list, "", m.st.PaneEmpty.Render(truncate(m.historyEmpty(), leftW-8)))
	}
	for len(list) < panelRows {
		list = append(list, "")
	}
	list = list[:panelRows]

	// The detail panel: metadata and the next action, never a file read per frame.
	var detail []string
	if len(entries) > 0 && m.history.cursor < len(entries) {
		detail = m.historyDetailFor(entries[m.history.cursor], rightW-6)
	} else {
		detail = []string{"", m.st.PaneEmpty.Render("Nothing selected")}
	}
	for len(detail) < panelRows {
		detail = append(detail, "")
	}
	detail = detail[:panelRows]

	panels := lipgloss.JoinHorizontal(lipgloss.Top, m.card(leftW, list), " ", m.card(rightW, detail))
	footer := "Tab filter · ↑↓ choose · Enter open · v conversation log · d delete · Esc back"
	return header + "\n" + strings.Join(pills, " ") + "\n" + panels + "\n" + m.st.StatusMuted.Render(truncate(footer, width))
}

// historyDetailFor describes one record without opening it.
func (m *Model) historyDetailFor(entry historyEntry, width int) []string {
	lines := []string{m.st.PaneTitle.Render(truncate(entry.label, width)), m.st.CardMuted.Render(strings.Repeat("─", max(1, width)))}
	line := func(text string) { lines = append(lines, truncate(text, width)) }
	switch entry.kind {
	case historyTerminal:
		if !entry.when.IsZero() {
			line(entry.when.Format("2006-01-02 15:04:05") + " · " + entry.detail)
		}
		line("file  " + entry.label)
		line("")
		line("A recorded embedded terminal.")
		line("Enter replays it; v reads the conversation as text.")
		line("d deletes the recording file.")
		line("")
		line("path")
		line(entry.path)
	case historyTunnel:
		state := entry.tunnel
		if state == nil {
			line("Tunnel session")
			break
		}
		arrow := "→"
		if state.kind == tunnel.Remote {
			arrow = "←"
		}
		line(fmt.Sprintf("%s %s %s %s", titleWord(string(state.kind)), state.listen, arrow, state.target))
		line(fmt.Sprintf("host %s · started %s", state.host, state.start.Format("2006-01-02 15:04")))
		line(fmt.Sprintf("%d %s · %s received · %s sent", state.conns,
			plural(state.conns, "connection", "connections"), humanSize(state.bytesIn), humanSize(state.bytesOut)))
		if m.runningTunnel(entry.session) != nil {
			line(m.st.Success.Render("running now · Enter opens the live dashboard"))
		} else {
			line("finished · Enter opens the event tail")
		}
		line("")
		line("s stops it if it is still running · d deletes this record")
		if len(state.events) > 0 {
			line("")
			line("Recent connections")
			for i := len(state.events) - 1; i >= 0 && len(lines) < 15; i-- {
				event := state.events[i]
				if event.Client == "" && event.Error == "" {
					continue
				}
				if event.Error != "" {
					lines = append(lines, m.st.Danger.Render(truncate("  error  "+event.Error, width)))
					continue
				}
				lines = append(lines, m.st.CardText.Render(truncate(fmt.Sprintf("  %s  %s  %s in · %s out",
					event.Time.Format("15:04:05"), event.Client, humanSize(event.BytesIn), humanSize(event.BytesOut)), width)))
			}
		}
	case historyAgent:
		line(entry.detail + " · " + entry.when.Format("2006-01-02 15:04:05"))
		tool, where := entry.label, ""
		if at := strings.Index(tool, " · "); at >= 0 {
			tool, where = tool[:at], tool[at+len(" · "):]
		}
		line("tool  " + tool)
		if where != "" {
			line("where " + where)
		}
		line("")
		line("An MCP tool call made by an agent.")
		line("The same allowlists, keyring and audit log as the interface.")
	}
	return lines
}

// historyEmpty explains the tab, and the one setting that is usually the reason
// terminal history is empty.
func (m *Model) historyEmpty() string {
	switch historyTabs[m.history.tab].kind {
	case historyTerminal:
		if !m.cfg.Sessions.Record {
			return "No recordings — recording is off (sessions.record, or Ctrl+, Record sessions)."
		}
		dir, err := recordingsDir()
		if err != nil {
			return "No recordings yet — open an embedded terminal with U."
		}
		return "No recordings yet — U opens an embedded terminal. Looking in " + dir
	case historyTunnel:
		return "No tunnels yet — Terminal & Tunnel forwards a port; sessions are recorded automatically."
	case historyAgent:
		return "No agent calls yet — run an MCP client against omssh mcp."
	default:
		return "Nothing recorded yet. Terminals, tunnels and agent calls appear here."
	}
}

// handleHistoryKey drives the history screen.
func (m *Model) handleHistoryKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	entries := m.historyEntries()
	switch msg.String() {
	case "esc", "q":
		m.screen = screenHome
		m.home.cursor = 0
		m.mode = "NORMAL"
	case "tab", "right", "l":
		m.history.tab = (m.history.tab + 1) % len(historyTabs)
		m.history.cursor, m.history.offset = 0, 0
	case "shift+tab", "left", "h":
		m.history.tab = (m.history.tab + len(historyTabs) - 1) % len(historyTabs)
		m.history.cursor, m.history.offset = 0, 0
	case "up", "k":
		m.history.cursor = max(0, m.history.cursor-1)
	case "down", "j":
		m.history.cursor = min(max(0, len(entries)-1), m.history.cursor+1)
	case "enter":
		if m.history.cursor >= 0 && m.history.cursor < len(entries) {
			m.openHistoryEntry(entries[m.history.cursor])
			return m, nil
		}
	case "s":
		if m.history.cursor >= 0 && m.history.cursor < len(entries) && entries[m.history.cursor].kind == historyTunnel {
			if m.stopTunnelBySession(entries[m.history.cursor].session) {
				m.history.all = m.loadHistory()
				m.toastSuccess("Tunnel stopped")
			} else {
				m.toastInfo("That tunnel is not running")
			}
		}
	case "v":
		if m.history.cursor >= 0 && m.history.cursor < len(entries) && entries[m.history.cursor].kind == historyTerminal {
			m.openTranscript(entries[m.history.cursor])
		}
	case "d":
		if m.history.cursor >= 0 && m.history.cursor < len(entries) {
			m.confirmHistoryDelete(entries[m.history.cursor])
		}
	}
	return m, nil
}

// openHistoryEntry opens the thing the row names: replay, tail, or the call.
func (m *Model) openHistoryEntry(entry historyEntry) {
	switch entry.kind {
	case historyTerminal:
		m.openReplay(entry.path)
	case historyTunnel:
		if active := m.runningTunnel(entry.session); active != nil {
			for i, candidate := range m.tunnels {
				if candidate == active {
					m.openTunnelDashboard(i)
					return
				}
			}
		}
		m.openTunnelTail(entry)
	case historyAgent:
		m.openHistoryDetail("Agent call", []string{
			"tool    " + strings.TrimPrefix(entry.label, "agent · "),
			"when    " + entry.when.Format(time.RFC3339),
			"result  " + entry.detail,
			"",
			"An agent's calls are the same files and places this program uses.",
		})
	}
}

// openTunnelTail shows a tunnel's events, and live counters when it is still
// running. It is the "watch the tail" the history row promises.
func (m *Model) openTunnelTail(entry historyEntry) {
	state := entry.tunnel
	if state == nil {
		return
	}
	lines := []string{
		fmt.Sprintf("%s %s → %s", state.kind, state.listen, state.target),
		fmt.Sprintf("host %s · started %s · %d conns · %s in · %s out",
			state.host, state.start.Format("2006-01-02 15:04:05"), state.conns,
			humanSize(state.bytesIn), humanSize(state.bytesOut)),
	}
	if len(state.errs) > 0 {
		lines = append(lines, "errors: "+strings.Join(state.errs, "; "))
	}
	lines = append(lines, "")
	for _, event := range state.events {
		switch {
		case event.Client == "" && event.Error == "":
			lines = append(lines, event.Time.Format("15:04:05")+"  listening on "+event.Listen)
		case event.Error != "":
			lines = append(lines, event.Time.Format("15:04:05")+"  error  "+event.Error)
		default:
			lines = append(lines, fmt.Sprintf("%s  %s  %s in · %s out", event.Time.Format("15:04:05"),
				event.Client, humanSize(event.BytesIn), humanSize(event.BytesOut)))
		}
	}
	for _, running := range m.tunnels {
		if running.tunnel != nil && running.tunnel.ID() == state.id {
			conns, in, out, errText := running.snapshot()
			lines = append(lines, "", fmt.Sprintf("live · %d conns · %s in · %s out", conns,
				humanSize(in), humanSize(out)))
			if errText != "" {
				lines = append(lines, "live error: "+errText)
			}
		}
	}
	m.historyDetail = historyDetailState{title: "Tunnel · watch", lines: lines, session: state.id}
	m.Overlay = OverlayHistoryDetail
}

// openTranscript is the conversation log: the output of a recording with the
// escape sequences removed, for reading rather than replaying.
func (m *Model) openTranscript(entry historyEntry) {
	rec, err := session.LoadRecording(entry.path)
	if err != nil {
		m.toastError(err.Error())
		return
	}
	var out strings.Builder
	for _, event := range rec.Events {
		if event.Type == "o" {
			out.WriteString(event.Data)
			if out.Len() > 4<<20 {
				break
			}
		}
	}
	text := ansi.Strip(out.String())
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	m.openHistoryDetail("Terminal · log · "+entry.label, lines)
}

// historyDetailState is a paged, read-only text card.
type historyDetailState struct {
	title   string
	lines   []string
	offset  int
	session string // a tunnel tail can stop the forward it is watching
}

func (m *Model) openHistoryDetail(title string, lines []string) {
	m.historyDetail = historyDetailState{title: title, lines: lines}
	m.Overlay = OverlayHistoryDetail
}

func (m *Model) handleHistoryDetailKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.Overlay = OverlayNone
	case "x", "s":
		sessionID := m.historyDetail.session
		if sessionID == "" {
			return
		}
		if m.stopTunnelBySession(sessionID) {
			m.Overlay = OverlayNone
			m.history.all = m.loadHistory()
			m.toastSuccess("Tunnel stopped")
		} else {
			m.toastInfo("That tunnel is not running")
		}
	case "up", "k":
		m.historyDetail.offset = max(0, m.historyDetail.offset-1)
	case "down", "j":
		m.historyDetail.offset++
	case "pgup":
		m.historyDetail.offset = max(0, m.historyDetail.offset-10)
	case "pgdown":
		m.historyDetail.offset += 10
	case "g":
		m.historyDetail.offset = 0
	case "G":
		m.historyDetail.offset = max(0, len(m.historyDetail.lines)-1)
	}
}

func (m *Model) renderHistoryDetail(l layout) string {
	width := cardWidth(l, 64, max(64, min(l.Width-4, 120)))
	content := cardContent(width)
	state := m.historyDetail
	rows := max(3, l.Height-10)
	state.offset = clamp(state.offset, 0, max(0, len(state.lines)-1))
	end := min(len(state.lines), state.offset+rows)
	lines := []string{
		m.st.CardTitle.Render(truncate(state.title, content)),
		m.st.CardMuted.Render(truncate(fmt.Sprintf("%d lines · ↑↓ scroll · Esc close", len(state.lines)), content)),
		"",
	}
	for _, line := range state.lines[state.offset:end] {
		lines = append(lines, m.st.CardText.Render(truncate(line, content)))
	}
	if end < len(state.lines) {
		lines = append(lines, m.st.PaneEmpty.Render(fmt.Sprintf("… %d more", len(state.lines)-end)))
	}
	return m.card(width, lines)
}

// confirmHistoryDelete asks before removing a record, like every other delete.
func (m *Model) confirmHistoryDelete(entry historyEntry) {
	m.historyPending = entry
	body := []string{entry.label}
	switch entry.kind {
	case historyTerminal:
		body = append(body, "", "The recording file is deleted.", "This cannot be undone.")
	case historyTunnel:
		body = append(body, "", "The session's metadata is removed.", "Running tunnels are unaffected.")
	default:
		body = append(body, "", "The agent call is removed from the log.")
	}
	m.confirm = confirmState{Title: "Delete this record?", Body: body, Danger: true,
		Action: "history.delete", open: true}
	m.Overlay = OverlayConfirm
}

// deleteHistoryEntry performs the confirmed removal.
func (m *Model) deleteHistoryEntry() {
	entry := m.historyPending
	switch entry.kind {
	case historyTerminal:
		if err := os.Remove(entry.path); err != nil {
			m.toastError(err.Error())
			return
		}
	case historyTunnel:
		dropJSONLines("tunnels.jsonl", func(raw map[string]any) bool {
			return raw["session"] == entry.session
		})
	case historyAgent:
		dropJSONLines("mcp.jsonl", func(raw map[string]any) bool {
			when, _ := raw["time"].(string)
			tool, _ := raw["tool"].(string)
			path, _ := raw["path"].(string)
			return when+"|"+tool+"|"+path == entry.key
		})
	}
	m.history.all = m.loadHistory()
	m.history.cursor = clamp(m.history.cursor, 0, max(0, len(m.historyEntries())-1))
	m.toastSuccess("Deleted " + entry.label)
}

// dropJSONLines rewrites one state log without the matching lines. The file is
// small and mode 0600; a rewrite is simpler and safer than a tombstone.
func dropJSONLines(name string, drop func(map[string]any) bool) {
	dir, err := config.StateDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, name)
	file, err := os.Open(path)
	if err != nil {
		return
	}
	kept := make([]string, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) == nil && drop(raw) {
			continue
		}
		kept = append(kept, scanner.Text())
	}
	_ = file.Close()
	content := strings.Join(kept, "\n")
	if content != "" {
		content += "\n"
	}
	_ = os.WriteFile(path, []byte(content), 0o600)
}
