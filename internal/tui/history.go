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
		name := strings.TrimSuffix(filepath.Base(file.path), ".cast")
		label := name
		if at := strings.LastIndex(name, "-2026"); at > 0 {
			label = name[:at]
		}
		out = append(out, historyEntry{kind: historyTerminal, when: file.when,
			label: label, detail: humanSize(file.size), path: file.path})
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
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Session == "" {
			continue // a line from before sessions were identified
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
		label := "agent · " + call.Tool
		if where != "" {
			label += " " + where
		}
		out = append(out, historyEntry{kind: historyAgent, when: when, label: label,
			ok: call.OK, key: call.Time + "|" + call.Tool + "|" + call.Path,
			detail: map[bool]string{true: "ok", false: "failed"}[call.OK]})
	}
	return out
}

// renderHistory draws the screen: a title, tab pills, a table with a type badge,
// and a footer that names the keys. It is deliberately quiet — history is
// something to scan, not a dashboard to admire.
func (m *Model) renderHistory(l layout) string {
	entries := m.historyEntries()
	width := l.Width
	rows := make([]string, 0, l.Height)

	title := m.st.Accent.Bold(true).Render("History")
	right := m.st.StatusMuted.Render(fmt.Sprintf("%d %s", len(entries), plural(len(entries), "entry", "entries")))
	rows = append(rows, spread(title, right, width), "")

	// Tabs are pills: the active one is filled, the rest are quiet words.
	pills := make([]string, 0, len(historyTabs))
	for i, tab := range historyTabs {
		if i == m.history.tab {
			pills = append(pills, m.st.PaletteItemActive.Render(" "+tab.label+" "))
			continue
		}
		pills = append(pills, m.st.CardMuted.Render(" "+tab.label+" "))
	}
	rows = append(rows, "  "+strings.Join(pills, " "), "")

	const badge = 7
	rightW := clamp(width/3, 22, 38)
	leftW := max(12, width-badge-2-rightW-3)
	header := "  " + padRight("TYPE", badge) + " " + padRight("ENTRY", leftW) +
		" " + padLeft("WHEN · DETAIL", rightW)
	rows = append(rows,
		m.st.PaneHeader.Render(truncate(header, width)),
		m.st.CardMuted.Render(spaces(2)+strings.Repeat("─", max(1, width-4))),
	)

	listRows := max(1, l.Height-len(rows)-4)
	m.history.cursor = clamp(m.history.cursor, 0, max(0, len(entries)-1))
	if m.history.cursor < m.history.offset {
		m.history.offset = m.history.cursor
	}
	if m.history.cursor >= m.history.offset+listRows {
		m.history.offset = m.history.cursor - listRows + 1
	}
	badgeStyle := map[historyKind]lipgloss.Style{
		historyTerminal: m.st.Accent,
		historyTunnel:   m.st.CardText,
		historyAgent:    m.st.StatusKey,
	}
	badgeText := map[historyKind]string{
		historyTerminal: "term", historyTunnel: "tunnel", historyAgent: "agent",
	}
	for i := m.history.offset; i < len(entries) && i < m.history.offset+listRows; i++ {
		entry := entries[i]
		meta := entry.detail
		if !entry.when.IsZero() {
			meta = entry.when.Format("Jan 02 15:04") + " · " + meta
		}
		label := truncate(entry.label, leftW)
		line := "  " + badgeStyle[entry.kind].Render(padRight(badgeText[entry.kind], badge)) +
			" " + m.st.CardText.Render(padRight(label, leftW)) +
			" " + m.st.CardMuted.Render(padLeft(truncate(meta, rightW), rightW))
		if i == m.history.cursor {
			line = m.st.PaletteItemActive.Width(width).Render(padVisible(
				"▸ "+badgeStyle[entry.kind].Bold(true).Render(padRight(badgeText[entry.kind], badge))+
					" "+m.st.Accent.Bold(true).Render(padRight(label, leftW))+
					" "+m.st.CardMuted.Render(padLeft(truncate(meta, rightW), rightW)), width))
		}
		rows = append(rows, line)
	}
	if len(entries) == 0 {
		rows = append(rows, "", "  "+m.st.PaneEmpty.Render(truncate(m.historyEmpty(), width-4)))
	}
	for len(rows) < l.Height-2 {
		rows = append(rows, "")
	}
	footer := "Tab filter · ↑↓ choose · Enter open · v conversation log · d delete · Esc back"
	rows = append(rows, "", m.st.StatusMuted.Render(truncate(footer, width)))
	return strings.Join(rows, "\n")
}

// historyEmpty explains the tab, and the one setting that is usually the reason
// terminal history is empty.
func (m *Model) historyEmpty() string {
	switch historyTabs[m.history.tab].kind {
	case historyTerminal:
		if !m.cfg.Sessions.Record {
			return "No recordings — sessions.record is off. Turn it on to keep embedded terminals."
		}
		return "No recordings yet — open an embedded terminal with U."
	case historyTunnel:
		return "No tunnels yet — Terminal & Tunnel on the launcher forwards a port."
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
	m.openHistoryDetail("Tunnel · watch", lines)
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
	title  string
	lines  []string
	offset int
}

func (m *Model) openHistoryDetail(title string, lines []string) {
	m.historyDetail = historyDetailState{title: title, lines: lines}
	m.Overlay = OverlayHistoryDetail
}

func (m *Model) handleHistoryDetailKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.Overlay = OverlayNone
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
