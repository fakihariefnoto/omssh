package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// connEntry is one host in the connections screen.
type connEntry struct {
	Alias, HostName, User, Identity, ProxyJump, Source string
	Port                                               int
	Extra                                              []string // additional patterns on the Host line
	Editable                                           bool     // lives in the main file, not an include
}

// connectionsState is the SSH host manager. It edits ~/.ssh/config directly, so
// what it shows is exactly what `ssh` will use.
type connectionsState struct {
	path    string
	main    *sshconfig.File
	files   []*sshconfig.File
	entries []connEntry
	cursor  int
	filter  input
	// filtering is true while the filter is being typed.
	filtering bool
	err       string
}

// connForm is the add/edit form. One field per directive omssh manages.
type connForm struct {
	original string // alias being edited; empty when adding
	fields   []connField
	focus    int
	err      string
}

type connField struct {
	key, label, hint string
	in               input
}

// loadConnections parses ~/.ssh/config (and its includes, read-only).
func (m *Model) loadConnections() {
	if m.conn == nil {
		m.conn = &connectionsState{}
	}
	path := m.sshPath
	if path == "" {
		defaultPath, err := sshconfig.DefaultPath()
		if err != nil {
			m.conn.err = err.Error()
			return
		}
		path = defaultPath
	}
	m.conn.path = path
	files, err := sshconfig.LoadWithIncludes(path)
	if err != nil {
		m.conn.err = err.Error()
		return
	}
	m.conn.err = ""
	m.conn.files = files
	m.conn.main = files[0]

	entries := make([]connEntry, 0, len(files[0].Hosts()))
	for _, file := range files {
		for _, block := range file.Blocks {
			if block.Wildcard() || len(block.Patterns) == 0 {
				continue
			}
			spec := block.Spec()
			entries = append(entries, connEntry{
				Alias:     spec.Alias,
				HostName:  spec.HostName,
				User:      spec.User,
				Port:      spec.Port,
				Identity:  spec.IdentityFile,
				ProxyJump: spec.ProxyJump,
				Source:    file.Path,
				Extra:     block.Patterns[1:],
				Editable:  file == files[0],
			})
		}
	}
	m.conn.entries = entries
	m.conn.cursor = clamp(m.conn.cursor, 0, max(0, len(entries)-1))
}

// visibleConnections applies the filter.
func (m *Model) visibleConnections() []connEntry {
	if m.conn == nil {
		return nil
	}
	query := strings.TrimSpace(m.conn.filter.Value)
	if query == "" {
		return m.conn.entries
	}
	out := make([]connEntry, 0, len(m.conn.entries))
	for _, e := range m.conn.entries {
		for _, field := range []string{e.Alias, e.HostName, e.User} {
			if _, ok := fuzzyMatch(field, query); ok {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// currentConnection returns the highlighted host.
func (m *Model) currentConnection() (connEntry, bool) {
	visible := m.visibleConnections()
	if m.conn == nil || m.conn.cursor < 0 || m.conn.cursor >= len(visible) {
		return connEntry{}, false
	}
	return visible[m.conn.cursor], true
}

// renderConnections draws the host manager as two boxes: the hosts, and the
// highlighted one's details. It is a table, so it looks like one — header row,
// aligned columns, and a selected row you can see at a glance.
func (m *Model) renderConnections(l layout) string {
	width, height := l.Width, l.Height
	rows := make([]string, 0, height)

	// Title row, matching the pane headers so the screen belongs to the same
	// family.
	path := "~/.ssh/config"
	count := 0
	if m.conn != nil {
		count = len(m.conn.entries)
		if home, err := userHome(); err == nil && m.conn.path != "" {
			path = strings.Replace(m.conn.path, home, "~", 1)
		}
	}
	title := m.st.PaneTitle.Render("SSH connections")
	right := m.st.StatusMuted.Render(fmt.Sprintf("%s · %d %s", path, count, plural(count, "host", "hosts")))
	rows = append(rows, spread(title, right, width), "")

	// The details box is exactly as tall as its content, so it never looks like an
	// empty panel; the list takes the rest of the screen, which is where a person
	// is looking anyway.
	const detailHeight = 5
	listRows := max(4, height-detailHeight-6) // title, blank, footer, two borders
	inner := width - 2

	headers := m.connHeaderRow(inner)
	list := make([]string, 0, listRows)
	list = append(list, headers, m.st.Muted.Render(strings.Repeat("─", max(1, inner))))

	visible := m.visibleConnections()
	switch {
	case m.conn != nil && m.conn.err != "":
		list = append(list, m.st.Danger.Render(truncate("Cannot read the SSH config", inner)),
			m.st.PaneEmpty.Render(truncate(m.conn.err, inner)))
	case len(visible) == 0 && m.conn != nil && len(m.conn.entries) == 0:
		list = append(list,
			"", m.st.PaneEmpty.Render("No hosts yet"),
			m.st.PaneEmpty.Render("a adds one · o opens the file in $EDITOR"))
	case len(visible) == 0:
		list = append(list, "", m.st.PaneEmpty.Render(truncate("Nothing matches "+m.conn.filter.Value, inner)))
	default:
		start := clamp(m.conn.cursor-max(1, listRows-3)/2, 0, max(0, len(visible)-(listRows-2)))
		for i := start; i < len(visible) && len(list) < listRows; i++ {
			list = append(list, m.renderConnRow(visible[i], i == m.conn.cursor, inner))
		}
	}
	for len(list) < listRows {
		list = append(list, "")
	}
	// The list, then the selected host's details.
	rows = append(rows,
		m.st.PaneBorderBlur.Width(width).Height(listRows+2).Render(strings.Join(list, "\n")),
		m.st.PaneBorder.Width(width).Height(detailHeight+2).
			Render(strings.Join(m.connDetailRows(inner, detailHeight), "\n")))

	// Filter line and keys.
	if m.conn != nil && (m.conn.filtering || m.conn.filter.Value != "") {
		rows = append(rows, m.st.PathInput.Width(width).Render("filter: "+m.conn.filter.Rendered()))
	} else {
		rows = append(rows, m.st.StatusMuted.Render(
			"Enter browse · t terminal · e edit · a add · d delete · r reload · o edit file · / filter · Esc back"))
	}

	// The row slice holds multi-line boxes, so it is joined before it is trimmed:
	// truncating an element would cut everything after its first line.
	out := make([]string, 0, height)
	for _, line := range strings.Split(strings.Join(rows, "\n"), "\n") {
		if len(out) >= height {
			break
		}
		out = append(out, padVisible(ansi.Truncate(line, width, "…"), width))
	}
	for len(out) < height {
		out = append(out, spaces(width))
	}
	return strings.Join(out, "\n")
}

// connHeaderRow labels the columns once, so the table explains itself.
func (m *Model) connHeaderRow(inner int) string {
	name, host, user, port := m.connColumns(inner)
	header := padRight("NAME", name) + padRight("HOST", host) + padRight("USER", user) +
		padLeft("PORT", port) + " " + padRight("STATE", connStateWidth)
	return m.st.PaneHeader.Render(padVisible(header, inner))
}

// connColumns decides the column widths for the current interface width: the
// name is short, the address is what varies, and a narrow screen drops the
// right-hand columns rather than squeezing every one of them.
func (m *Model) connColumns(inner int) (name, host, user, port int) {
	const gaps = 4 // one space between the five columns
	if inner < 56 {
		// Name and address only: they are what a person chooses between.
		avail := inner - connStateWidth - 2
		return max(8, avail/3), max(8, avail-avail/3), 0, 0
	}
	port = 6
	user = clamp(inner/8, 6, 14)
	name = clamp(inner/6, 12, 24)
	host = inner - connStateWidth - port - user - name - gaps
	if host < 16 {
		host = 16
	}
	return name, host, user, port
}

// connStateWidth is the width of the state column: enough for "not connected".
const connStateWidth = 13

// renderConnRow draws one host in the table, highlighted when chosen.
func (m *Model) renderConnRow(e connEntry, selected bool, inner int) string {
	name, hostWidth, userWidth, portWidth := m.connColumns(inner)
	marker := "  "
	if selected {
		marker = "▸ "
	}
	host := e.HostName
	if host == "" {
		host = e.Alias
	}
	state := m.connState(e)
	port := "—"
	if e.Port != 0 {
		port = itoa(e.Port)
	}
	cols := marker + padRight(e.Alias, name) + padRight(host, hostWidth)
	if userWidth > 0 {
		cols += padRight(e.User, userWidth)
	}
	if portWidth > 0 {
		cols += padLeft(port, portWidth)
	}
	cols += " " + padRight(state.label, connStateWidth-1)
	if !e.Editable {
		// It lives in an Include, so the main file cannot change it: say so here
		// rather than failing when the edit is attempted.
		cols += " from include"
	}

	if selected {
		// A filled row, so the choice is unmistakable rather than a tint.
		return m.st.PaletteItemActive.Width(inner).Render(padVisible(cols, inner))
	}
	if !e.Editable {
		return m.st.CardMuted.Render(padVisible(cols, inner))
	}
	return padVisible(cols, inner)
}

// connState reports whether a host is connected, connecting, or idle, using the
// same dots as the tab strip.
func (m *Model) connState(e connEntry) struct{ label string } {
	for _, s := range m.liveSessions() {
		if s.alias != e.Alias {
			continue
		}
		return struct{ label string }{label: s.state.Label()}
	}
	return struct{ label string }{label: "not connected"}
}

// connDetailRows is the detail box: the highlighted host, in full.
func (m *Model) connDetailRows(inner, rows int) []string {
	entry, ok := m.currentConnection()
	if !ok {
		out := make([]string, rows)
		out[0] = m.st.PaneEmpty.Render("No host chosen")
		out[1] = m.st.PaneEmpty.Render("Enter connects, e edits, a adds")
		return out
	}
	dash := m.st.Muted.Render("—")
	pick := func(v string) string {
		if v == "" {
			return dash
		}
		return m.st.CardText.Render(v)
	}
	label := func(s string) string { return m.st.StatusMuted.Render(padRight(s, 9)) }
	source := entry.Source
	if home, err := userHome(); err == nil {
		source = strings.Replace(source, home, "~", 1)
	}

	// Two label/value pairs per line, each column padded so the second one starts
	// in the same place on every row.
	column := max(22, (inner-2)/2)
	pair := func(k1, v1, k2, v2 string) string {
		first := label(k1) + pick(truncate(v1, column-11))
		if k2 == "" {
			return padVisible(first, inner)
		}
		return padVisible(first, column) + label(k2) + pick(truncate(v2, inner-column-11))
	}

	lines := []string{
		spread(m.st.PaneTitle.Render(entry.Alias), m.st.StatusMuted.Render(m.connState(entry).label), inner),
		pair("host", entry.HostName, "user", entry.User),
		pair("port", itoaZero(entry.Port), "key", entry.Identity),
		pair("jump", entry.ProxyJump, "", ""),
		pair("source", source, "", ""),
	}
	if len(entry.Extra) > 0 {
		lines = append(lines, pair("also", strings.Join(entry.Extra, ", "), "", ""))
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines[:rows]
}

// handleConnectionsKey drives the host manager. Editing a host is the primary
// action here, which is why Enter opens the form rather than connecting.
func (m *Model) handleConnectionsKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if m.conn == nil {
		m.loadConnections()
	}

	if m.conn.filtering {
		switch msg.String() {
		case "esc":
			m.conn.filtering = false
			m.conn.filter.Clear()
			m.conn.cursor = 0
			m.mode = "CONNECTIONS"
		case "enter":
			m.conn.filtering = false
			m.mode = "CONNECTIONS"
		default:
			if m.conn.filter.Handle(msg) {
				m.conn.cursor = 0
			}
		}
		return nil, true
	}

	// Modifier keys belong to the keymap: Ctrl+F, Ctrl+R and the global keys must
	// keep working from here.
	if msg.Key().Mod != 0 {
		return nil, false
	}

	switch msg.String() {
	case "up", "k":
		m.conn.cursor = max(0, m.conn.cursor-1)
	case "down", "j":
		m.conn.cursor = min(max(0, len(m.visibleConnections())-1), m.conn.cursor+1)
	case "home":
		m.conn.cursor = 0
	case "end":
		m.conn.cursor = max(0, len(m.visibleConnections())-1)
	case "enter":
		return m.runAction("session.connect"), true
	case "esc":
		// Leave the manager: filtering is handled above, so this is "back".
		m.screen = m.connReturn
		m.mode = "NORMAL"
		return nil, true
	}
	// Everything else goes to the keymap, which is screen-aware: "a" here adds a
	// host, and a letter with no binding on this screen does nothing at all.
	return nil, false
}

// openConnectionForm opens the form, empty for a new host or filled for edit.
func (m *Model) openConnectionForm(original string, spec sshconfig.Spec) {
	form := connForm{original: original}
	add := func(key, label, hint, value string) {
		field := connField{key: key, label: label, hint: hint}
		field.in.Set(value)
		form.fields = append(form.fields, field)
	}
	add("Alias", "name", "what you type after ssh", spec.Alias)
	add("HostName", "host", "address or DNS name", spec.HostName)
	add("User", "user", "login user", spec.User)
	add("Port", "port", "blank for 22", itoaZero(spec.Port))
	add("IdentityFile", "key", "path to a private key", spec.IdentityFile)
	add("ProxyJump", "jump", "host to reach this one through", spec.ProxyJump)
	m.form = form
	m.Overlay = OverlayForm
}

// saveConnectionForm writes the form to ~/.ssh/config.
func (m *Model) saveConnectionForm() tea.Cmd {
	spec := sshconfig.Spec{}
	for _, field := range m.form.fields {
		value := strings.TrimSpace(field.in.Value)
		switch field.key {
		case "Alias":
			spec.Alias = value
		case "HostName":
			spec.HostName = value
		case "User":
			spec.User = value
		case "Port":
			if value != "" {
				port, err := atoi(value)
				if err != nil || port < 1 || port > 65535 {
					m.form.err = "port must be a number between 1 and 65535"
					return nil
				}
				spec.Port = port
			}
		case "IdentityFile":
			spec.IdentityFile = value
		case "ProxyJump":
			spec.ProxyJump = value
		}
	}

	if err := spec.Validate(); err != nil {
		m.form.err = err.Error()
		return nil
	}

	var err error
	if m.form.original == "" {
		err = m.conn.main.Add(spec)
	} else {
		err = m.conn.main.Update(m.form.original, spec)
	}
	if err != nil {
		m.form.err = err.Error()
		return nil
	}
	if err := m.conn.main.Save(); err != nil {
		m.form.err = err.Error()
		return nil
	}

	alias := spec.Alias
	was := m.form.original
	m.form = connForm{}
	m.Overlay = OverlayNone
	m.loadConnections()
	for i, e := range m.visibleConnections() {
		if e.Alias == alias {
			m.conn.cursor = i
		}
	}
	switch {
	case was == "":
		m.toastSuccess("Added " + alias)
	case was != alias:
		m.toastSuccess("Renamed " + was + " to " + alias)
	default:
		m.toastSuccess("Saved " + alias)
	}
	return nil
}

// deleteConnection asks first, then removes the block from the main file.
func (m *Model) deleteConnection() {
	entry, ok := m.currentConnection()
	if !ok {
		return
	}
	if !entry.Editable {
		m.toastWarning(entry.Alias + " lives in " + entry.Source + "; edit that file")
		return
	}
	m.confirm = confirmState{
		Title:  "Delete connection?",
		Body:   []string{entry.Alias + " will be removed from ~/.ssh/config.", "Nothing else in the file is touched."},
		Danger: true,
		Action: "session.delete",
		Alias:  entry.Alias,
		open:   true,
	}
	m.Overlay = OverlayConfirm
}

// applyDeleteConnection removes the confirmed host.
func (m *Model) applyDeleteConnection(alias string) {
	if m.conn == nil || m.conn.main == nil {
		return
	}
	if err := m.conn.main.Delete(alias); err != nil {
		m.toastError(err.Error())
		return
	}
	if err := m.conn.main.Save(); err != nil {
		m.toastError(err.Error())
		return
	}
	m.loadConnections()
	m.conn.cursor = clamp(m.conn.cursor, 0, max(0, len(m.conn.entries)-1))
	m.toastSuccess("Deleted " + alias)
}

// editConfigFile suspends the interface and opens ~/.ssh/config in $EDITOR.
func (m *Model) editConfigFile() tea.Cmd {
	if m.conn == nil || m.conn.path == "" {
		return nil
	}
	cmd := exec.CommandContext(context.Background(), editorCommand(), m.conn.path) //nolint:gosec // $EDITOR is the user's own choice
	return tea.ExecProcess(cmd, func(error) tea.Msg {
		m.loadConnections()
		return nil
	})
}

// editorCommand resolves $EDITOR, then $VISUAL, then vi.
func editorCommand() string {
	for _, env := range []string{"EDITOR", "VISUAL"} {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			return value
		}
	}
	return "vi"
}

// renderConnectionForm draws the add/edit card.
func (m *Model) renderConnectionForm(l layout) string {
	width := cardWidth(l, 52, 76)
	content := cardContent(width)

	title := "Add connection"
	if m.form.original != "" {
		title = "Edit " + m.form.original
	}
	lines := []string{m.st.CardTitle.Render(title), ""}

	for i, field := range m.form.fields {
		label := m.st.StatusMuted.Render(padRight(field.label, 6))
		value := m.st.CardText.Render(truncate(field.in.Value, content-12))
		if i == m.form.focus {
			value = m.st.PathInput.Width(content - 12).Render(truncate(field.in.Rendered(), content-12))
			lines = append(lines, m.st.Accent.Bold(true).Render("▸ ")+label+value)
		} else {
			lines = append(lines, "  "+label+value)
		}
		lines = append(lines, "        "+m.st.CardMuted.Render(field.hint))
	}

	if m.form.err != "" {
		lines = append(lines, "", m.st.Danger.Render(truncate(m.form.err, content)))
	}
	footer := "Tab next · Enter save · Esc cancel"
	if m.form.original != "" {
		footer = "Tab next · Enter save · Esc cancel (renaming is allowed)"
	}
	lines = append(lines, "", m.st.CardMuted.Render(footer))
	return m.card(width, lines)
}

// handleFormKey edits the form fields.
func (m *Model) handleFormKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.form = connForm{}
		m.Overlay = OverlayNone
		return nil
	case "tab", "down":
		m.form.focus = (m.form.focus + 1) % len(m.form.fields)
		return nil
	case "shift+tab", "up":
		m.form.focus = (m.form.focus - 1 + len(m.form.fields)) % len(m.form.fields)
		return nil
	case "enter":
		return m.saveConnectionForm()
	default:
		m.form.err = ""
		m.form.fields[m.form.focus].in.Handle(msg)
	}
	return nil
}
