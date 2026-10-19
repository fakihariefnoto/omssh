package tui

import (
	"context"
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

// renderConnections draws the host manager: the list, the highlighted host's
// details, and one line of keys.
func (m *Model) renderConnections(l layout) string {
	width, height := l.Width, l.Height
	lines := make([]string, 0, height)

	// Header: what this screen is, and which file it edits.
	title := m.st.PaneTitle.Render("SSH connections")
	path := "~/.ssh/config"
	count := 0
	if m.conn != nil {
		count = len(m.conn.entries)
		if home, err := userHome(); err == nil && m.conn.path != "" {
			path = strings.Replace(m.conn.path, home, "~", 1)
		}
	}
	right := m.st.StatusMuted.Render(path + " · " + plural(count, "host", "hosts"))
	lines = append(lines,
		spread(title, right, width),
		m.st.Muted.Render(strings.Repeat("─", max(1, width))))

	// Filter line, always one row so the layout does not jump while typing.
	if m.conn != nil && (m.conn.filtering || m.conn.filter.Value != "") {
		lines = append(lines, m.st.PathInput.Width(width).Render("filter: "+m.conn.filter.Rendered()))
	} else {
		lines = append(lines, m.st.StatusMuted.Render("Ctrl+F to filter"))
	}

	visible := m.visibleConnections()
	rules := len(lines)

	// Detail panel height, reserved before the list so the list never pushes the
	// keys off the screen.
	detailRows := 8
	footerRows := 1
	listRows := max(1, height-rules-detailRows-footerRows-2)

	switch {
	case m.conn != nil && m.conn.err != "":
		lines = append(lines,
			m.st.Danger.Render("Cannot read the SSH config"),
			m.st.PaneEmpty.Render(truncate(m.conn.err, width)))
		for len(lines) < rules+listRows {
			lines = append(lines, "")
		}
	case len(visible) == 0:
		if m.conn != nil && len(m.conn.entries) == 0 {
			lines = append(lines,
				m.st.PaneEmpty.Render("No hosts yet"),
				m.st.PaneEmpty.Render("a adds one · o edits the file"))
		} else {
			lines = append(lines, m.st.PaneEmpty.Render("Nothing matches "+m.conn.filter.Value))
		}
		for len(lines) < rules+listRows {
			lines = append(lines, "")
		}
	default:
		start := clamp(m.conn.cursor-listRows/2, 0, max(0, len(visible)-listRows))
		for i := start; i < len(visible) && len(lines) < rules+listRows; i++ {
			lines = append(lines, m.renderConnRow(visible[i], i == m.conn.cursor, width))
		}
		for len(lines) < rules+listRows {
			lines = append(lines, "")
		}
	}

	lines = append(lines, m.st.Muted.Render(strings.Repeat("─", max(1, width))))
	lines = append(lines, m.renderConnDetail(width, detailRows)...)
	lines = append(lines, m.st.StatusMuted.Render(
		"Enter/c connect · e edit · a add · d delete · r reload · o edit file · Ctrl+F filter · Esc back"))

	// Exactly fill the screen.
	out := make([]string, 0, height)
	for _, line := range lines {
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

// renderConnRow draws one host: alias, hostname, user, port.
func (m *Model) renderConnRow(e connEntry, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = m.st.Accent.Render("▸ ")
	}
	aliasWidth := clamp(width/3, 10, 24)
	hostWidth := clamp(width/3, 10, 30)

	alias := padRight(e.Alias, aliasWidth)
	host := padRight(e.HostName, hostWidth)
	user := e.User
	port := ""
	if e.Port != 0 {
		port = ":" + itoa(e.Port)
	}
	note := ""
	if !e.Editable {
		note = m.st.Muted.Render("  (from include)")
	}

	line := marker + alias + host + padRight(user+port, 16) + note
	if selected {
		return m.st.Accent.Bold(true).Render(line)
	}
	if e.HostName == "" {
		return m.st.PaneSymlink.Render(line) // incomplete: worth noticing
	}
	return line
}

// renderConnDetail shows the highlighted host's resolved-looking settings.
func (m *Model) renderConnDetail(width, rows int) []string {
	entry, ok := m.currentConnection()
	if !ok {
		out := make([]string, rows)
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

	// Values are clamped to the panel: a long identity path must not run into the
	// next column or wrap the row.
	lines := []string{
		m.st.PaneTitle.Render(truncate(entry.Alias, width-9)),
		label("host") + pick(truncate(entry.HostName, width-9)),
		label("user") + pick(truncate(entry.User, width-9)),
		label("port") + pick(itoaZero(entry.Port)),
		label("key") + pick(truncate(entry.Identity, width-9)),
		label("jump") + pick(truncate(entry.ProxyJump, width-9)),
		label("source") + m.st.CardMuted.Render(truncate(entry.Source, width-9)),
	}
	if len(entry.Extra) > 0 {
		lines = append(lines, label("also")+m.st.CardMuted.Render(truncate(strings.Join(entry.Extra, ", "), width-9)))
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
	case "enter", "c":
		return m.runAction("session.connect"), true
	case "e":
		return m.runAction("session.edit"), true
	case "a":
		return m.runAction("session.add"), true
	case "d":
		return m.runAction("session.delete"), true
	case "r":
		return m.runAction("session.reload"), true
	case "o":
		return m.runAction("session.open-config"), true
	case "esc":
		// Leave the manager: filtering is handled above, so this is "back".
		m.screen = m.connReturn
		m.mode = "NORMAL"
		return nil, true
	}
	// Swallow other printable input: the file panes are behind this screen, and
	// their type-ahead must not fire from here.
	if msg.Key().Text != "" {
		return nil, true
	}
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
