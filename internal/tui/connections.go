package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/cloudcred"
	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/keys"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// connEntry is one host in the connections screen.
type connEntry struct {
	Alias, HostName, User, Identity, ProxyJump, Source string
	// Cloud marks a bucket. It is not a host and has no shell: the same list, a
	// different kind of place.
	Cloud    bool
	Port     int
	Extra    []string // additional patterns on the Host line
	Editable bool     // lives in the main file, not an include
}

// connectionsState is the SSH host manager. It edits ~/.ssh/config directly, so
// what it shows is exactly what `ssh` will use.
// connSection is which management screen is showing. They share one list, one
// filter and one form; only the kind of place and the tab differ.
type connSection int

const (
	secOverview connSection = iota
	secSSH
	secS3
	secWebDAV
	secAzure
	secGCP
)

func (s connSection) title() string {
	switch s {
	case secSSH:
		return "SSH connections"
	case secS3:
		return "S3-compatible storage"
	case secWebDAV:
		return "WebDAV"
	case secAzure:
		return "Azure Blob"
	case secGCP:
		return "Google Cloud Storage"
	default:
		return "Connections"
	}
}

// planned is true for a section whose driver has not shipped. The tab exists so
// the plan is visible, and it says so instead of pretending to be empty.
func (s connSection) planned() bool { return s >= secWebDAV }

func (s connSection) plannedNote() string {
	switch s {
	case secWebDAV:
		return "WebDAV (Nextcloud, Box, SharePoint) arrives with M10: PROPFIND, MOVE, ETags."
	case secAzure:
		return "Azure Blob arrives with M10: native REST, block uploads, SAS or Entra ID."
	default:
		return "Google Cloud Storage's native API arrives only if its S3-compatible XML API is not enough."
	}
}

type connectionsState struct {
	// section is the tab the screen is showing.
	section connSection
	// showAll includes hidden and code hosts, for the moment you need one.
	showAll bool
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
	// kind is "host" for ~/.ssh/config blocks and "cloud" for a bucket profile.
	kind   string
	fields []connField
	focus  int
	err    string
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
	// Cloud profiles are places too, and the same list is the right place to pick
	// one. They are appended rather than merged, so the sort above keeps hosts
	// first and buckets after them.
	for name, profile := range m.cfg.Cloud.Profiles {
		entries = append(entries, connEntry{
			Alias:    name,
			HostName: "s3://" + profile.Bucket + "/" + strings.Trim(profile.Prefix, "/"),
			User:     profile.Provider,
			Source:   "cloud profile",
			Cloud:    true,
		})
	}
	m.conn.entries = entries
	m.conn.cursor = clamp(m.conn.cursor, 0, max(0, len(entries)-1))
}

// codeHosts are the forges people keep in ~/.ssh/config for git, not for logins.
// They are hidden by default so the list is the machines you actually visit;
// H brings everything back.
var codeHosts = map[string]bool{
	"github.com": true, "gitlab.com": true, "bitbucket.org": true,
	"codeberg.org": true, "ssh.dev.azure.com": true, "git.sr.ht": true,
}

// isCodeHost reports whether a host exists for git rather than for a session.
func isCodeHost(e connEntry) bool {
	if e.User == "git" {
		return true
	}
	return codeHosts[strings.ToLower(e.Alias)] || codeHosts[strings.ToLower(e.HostName)]
}

// favourite reports whether the host is pinned.
func (m *Model) favourite(alias string) bool {
	for _, name := range m.cfg.Connections.Favorites {
		if name == alias {
			return true
		}
	}
	return false
}

// recentRank is the host's position in the recent list, or -1.
func (m *Model) recentRank(alias string) int {
	for i, name := range m.cfg.Connections.Recent {
		if name == alias {
			return i
		}
	}
	return -1
}

// hiddenByChoice reports whether the person hid the host.
func (m *Model) hiddenByChoice(alias string) bool {
	for _, name := range m.cfg.Connections.Hidden {
		if name == alias {
			return true
		}
	}
	return false
}

// visibleConnections applies the filter.
func (m *Model) visibleConnections() []connEntry {
	if m.conn == nil {
		return nil
	}
	query := strings.TrimSpace(m.conn.filter.Value)
	out := make([]connEntry, 0, len(m.conn.entries))
	for _, e := range m.conn.entries {
		// The tabs are the same list seen three ways: hosts, buckets, everything.
		switch m.conn.section {
		case secSSH:
			if e.Cloud {
				continue
			}
		case secS3:
			if !e.Cloud {
				continue
			}
		case secOverview:
			// everything
		case secWebDAV, secAzure, secGCP:
			continue
		}
		if !m.conn.showAll {
			if m.hiddenByChoice(e.Alias) {
				continue
			}
			if isCodeHost(e) {
				continue
			}
		}
		if query != "" {
			matched := false
			for _, field := range []string{e.Alias, e.HostName, e.User} {
				if _, ok := fuzzyMatch(field, query); ok {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, e)
	}

	// Favourites, then recently used, then the rest. A stable sort by rank keeps
	// the ssh config's own order inside each group.
	rank := func(e connEntry) int {
		if m.favourite(e.Alias) {
			for i, name := range m.cfg.Connections.Favorites {
				if name == e.Alias {
					return i
				}
			}
		}
		if r := m.recentRank(e.Alias); r >= 0 {
			return 100 + r
		}
		return 1000
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
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
	if m.connSectionNow().planned() {
		return m.renderPlanned(l)
	}
	width, height := l.Width, l.Height
	rows := make([]string, 0, height)

	// Title row, matching the pane headers so the screen belongs to the same
	// family.
	path := "~/.ssh/config"
	count := 0
	if m.conn != nil {
		for _, entry := range m.conn.entries {
			switch m.connSectionNow() {
			case secSSH:
				if entry.Cloud {
					continue
				}
			case secS3:
				if !entry.Cloud {
					continue
				}
			case secOverview, secWebDAV, secAzure, secGCP:
			}
			count++
		}
		if home, err := userHome(); err == nil && m.conn.path != "" {
			path = strings.Replace(m.conn.path, home, "~", 1)
		}
	}
	// The count and the noun belong to the tab: the S3 screen has buckets, the
	// SSH screen has hosts, the overview has both.
	place, noun := path, plural(count, "host", "hosts")
	switch m.connSectionNow() {
	case secS3:
		place, noun = "config.yaml", plural(count, "bucket", "buckets")
	case secOverview:
		noun = plural(count, "connection", "connections")
	case secSSH, secWebDAV, secAzure, secGCP:
	}
	title := m.st.PaneTitle.Render(m.connSectionNow().title())
	right := m.st.StatusMuted.Render(fmt.Sprintf("%s · %d %s", place, count, noun))
	rows = append(rows, spread(title, right, width), m.renderConnectionTabs(width), "")

	// The details box is exactly as tall as its content, so it never looks like an
	// empty panel; the list takes the rest of the screen, which is where a person
	// is looking anyway.
	const detailHeight = 6 // title, host/user, port/key, jump, source, keys
	// title, blank, the list's two borders, the detail box, and the key line —
	// fourteen rows of chrome, so the legend is never the row that gets cut.
	listRows := max(4, height-detailHeight-8)
	inner := width - 2

	headers := m.connHeaderRow(inner)
	list := make([]string, 0, listRows)
	list = append(list, headers, m.st.Muted.Render(strings.Repeat("─", max(1, inner))))

	visible := m.visibleConnections()
	switch {
	case m.conn == nil:
		// The screen was reached before the file was read, or reading it failed.
		// Drawing is not the place to find out: say so and stop.
		list = append(list, "", m.st.PaneEmpty.Render(truncate("Reading ~/.ssh/config…", inner)),
			m.st.PaneEmpty.Render(truncate("r reloads it", inner)))
	case m.conn.err != "":
		list = append(list, m.st.Danger.Render(truncate("Cannot read the SSH config", inner)),
			m.st.PaneEmpty.Render(truncate(m.conn.err, inner)))
	case len(visible) == 0 && len(m.conn.entries) == 0:
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
		rows = append(rows, m.st.StatusMuted.Render(connKeyLegend(width)))
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

// toggleFavourite pins or unpins the host under the cursor.
func (m *Model) toggleFavourite() {
	entry, ok := m.currentConnection()
	if !ok {
		return
	}
	favourites := m.cfg.Connections.Favorites
	kept := make([]string, 0, len(favourites)+1)
	exists := false
	for _, name := range favourites {
		if name == entry.Alias {
			exists = true
			continue
		}
		kept = append(kept, name)
	}
	if exists {
		m.cfg.Connections.Favorites = kept
		m.toastInfo(entry.Alias + " is no longer pinned")
	} else {
		// New favourites go on top: the last one pinned is the one wanted next.
		m.cfg.Connections.Favorites = append([]string{entry.Alias}, kept...)
		m.toastInfo("Pinned " + entry.Alias)
	}
	m.saveConfig()
}

// toggleHidden hides or shows the host under the cursor.
func (m *Model) toggleHidden() {
	entry, ok := m.currentConnection()
	if !ok {
		return
	}
	hidden := m.cfg.Connections.Hidden
	kept := make([]string, 0, len(hidden)+1)
	exists := false
	for _, name := range hidden {
		if name == entry.Alias {
			exists = true
			continue
		}
		kept = append(kept, name)
	}
	if exists {
		m.cfg.Connections.Hidden = kept
		m.toastInfo(entry.Alias + " is back in the list")
	} else {
		kept = append(kept, entry.Alias)
		m.cfg.Connections.Hidden = kept
		m.toastInfo("Hid " + entry.Alias + " · H shows everything")
	}
	m.conn.cursor = clamp(m.conn.cursor, 0, max(0, len(m.visibleConnections())-1))
	m.saveConfig()
}

// noteRecent records a successful connection, newest first.
func (m *Model) noteRecent(alias string) {
	recent := make([]string, 0, len(m.cfg.Connections.Recent)+1)
	recent = append(recent, alias)
	for _, name := range m.cfg.Connections.Recent {
		if name != alias && len(recent) < config.RecentLimit {
			recent = append(recent, name)
		}
	}
	m.cfg.Connections.Recent = recent
	m.saveConfig()
}

// saveConfig writes preferences back. Failing to save is not worth interrupting a
// connection for: the session still works, the ordering is just not remembered.
func (m *Model) saveConfig() {
	if m.configPath == "" {
		return
	}
	if err := config.Save(m.configPath, m.cfg); err != nil {
		m.toastWarning("Could not save preferences: " + err.Error())
	}
}

// connKeyLegend is the key line, in as much detail as the width allows. The
// terminal key is never the one dropped: it is the whole point of the screen for
// someone who came here to get a shell.
func connKeyLegend(width int) string {
	switch {
	case width >= 136:
		return "Enter browse · t terminal · Q disconnect · Tab screen · a add · f pin · x hide · H all · e edit · d delete · / filter · Esc back"
	case width >= 130:
		return "Enter · t terminal · Q disconnect · Tab screen · a add · f pin · x hide · H all · / filter"
	case width >= 112:
		return "Enter · t terminal · Q disconnect · f pin · x hide · H all · a add · / filter · Esc"
	case width >= 92:
		return "Enter · t terminal · Q disconnect · f pin · x hide · H all · / · Esc"
	case width >= 70:
		return "Enter · t · Q disconnect · f pin · x hide · H all · Esc"
	default:
		return "Enter · t · Q · f · x · H · Esc"
	}
}

// renderConnectionTabs is the row of management screens. The planned ones are
// muted and marked, so the plan is visible without pretending they work.
func (m *Model) renderConnectionTabs(width int) string {
	type tab struct {
		section connSection
		label   string
	}
	tabs := []tab{
		{secOverview, "1 Overview"},
		{secSSH, "2 SSH"},
		{secS3, "3 S3"},
		{secWebDAV, "4 WebDAV"},
		{secAzure, "5 Azure"},
		{secGCP, "6 GCP"},
	}
	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		label := t.label
		if t.section.planned() {
			label += "·soon"
		}
		switch {
		case t.section == m.connSectionNow():
			parts = append(parts, m.st.Accent.Bold(true).Render("["+label+"]"))
		case t.section.planned():
			parts = append(parts, m.st.StatusMuted.Render(label))
		default:
			parts = append(parts, m.st.CardText.Render(label))
		}
	}
	return padVisible(truncate(strings.Join(parts, "  "), width), width)
}

// cycleSection moves between management screens; planned tabs are included so the
// plan is reachable and explained.
func (m *Model) cycleSection(delta int) {
	if m.conn == nil {
		return
	}
	section := int(m.conn.section) + delta
	if section < 0 {
		section = int(secGCP)
	}
	if section > int(secGCP) {
		section = 0
	}
	m.conn.section = connSection(section)
	m.conn.cursor = 0
	m.conn.filtering = false
	m.conn.filter.Set("")
}

// connSectionNow is the current section, or the overview before anything loaded.
func (m *Model) connSectionNow() connSection {
	if m.conn == nil {
		return secOverview
	}
	return m.conn.section
}

// renderPlanned is the whole screen for a section whose driver is not here yet.
func (m *Model) renderPlanned(l layout) string {
	width, height := l.Width, l.Height
	rows := []string{
		m.st.PaneTitle.Render(m.connSectionNow().title()),
		m.renderConnectionTabs(width),
		"",
		m.st.PaneEmpty.Render(truncate(m.connSectionNow().plannedNote(), width-2)),
		"",
		m.st.StatusMuted.Render("Nothing to configure here yet · Tab to another screen · Esc back"),
	}
	out := make([]string, 0, height)
	for _, row := range rows {
		out = append(out, padVisible(truncate(row, width), width))
	}
	for len(out) < height {
		out = append(out, spaces(width))
	}
	return strings.Join(out[:height], "\n")
}

// connHeaderRow labels the columns once, so the table explains itself.
// connMarkerPrefix is the width of the selection and pin/recent markers at the
// start of every row: "▸ ★ " for the chosen pinned host, four cells.
const connMarkerPrefix = "    "

// connStateWidth is the width of the state column: enough for "not connected".
const connStateWidth = 14

// connRowState is a place's state as the table shows it.
type connRowState struct{ label string }

// connState reports whether a host or bucket is open. A bucket has no handshake,
// so "idle" is the honest word for one that is not on screen.
func (m *Model) connState(e connEntry) connRowState {
	host := e.Alias
	if e.Cloud {
		host = cloudPrefix + e.Alias
	}
	for _, s := range m.liveSessions() {
		if s.alias == host {
			return connRowState{label: s.state.Label()}
		}
	}
	if e.Cloud {
		return connRowState{label: "idle"}
	}
	return connRowState{label: "not connected"}
}

// connCol is one column of the manager's table: a title, a width, and how to read
// the value out of an entry. The columns belong to the tab — a bucket has a
// provider, a host has a port — so the table is built per section rather than
// pretending every kind of place has the same fields.
type connCol struct {
	title string
	width int
	right bool
	value func(connEntry) string
}

// connCols is the column set for the current tab.
func (m *Model) connCols(inner int) []connCol {
	avail := inner - len(connMarkerPrefix)
	state := func(e connEntry) string { return m.connState(e).label }

	if m.connSectionNow() == secS3 {
		// NAME · BUCKET · PREFIX · TYPE · STATE
		gaps := 4
		provW, stateW := 8, connStateWidth
		rest := avail - provW - stateW - gaps
		nameW := clamp(rest/4, 10, 20)
		rest -= nameW
		bucketW := rest * 3 / 5
		prefixW := rest - bucketW
		if prefixW < 10 {
			prefixW = 10
			bucketW = max(10, rest-prefixW)
		}
		return []connCol{
			{title: "NAME", width: nameW, value: func(e connEntry) string { return e.Alias }},
			{title: "BUCKET", width: bucketW, value: m.bucketOf},
			{title: "PREFIX", width: prefixW, value: m.prefixOf},
			{title: "TYPE", width: provW, value: func(e connEntry) string { return e.User }},
			{title: "STATE", width: stateW, value: state},
		}
	}

	// NAME · HOST · USER · PORT · STATE — the SSH table, and the overview, where a
	// bucket fills HOST with its address and USER with its provider.
	if avail < 56 {
		gaps := 2
		rest := avail - connStateWidth - gaps
		nameW := max(8, rest/3)
		return []connCol{
			{title: "NAME", width: nameW, value: func(e connEntry) string { return e.Alias }},
			{title: "HOST", width: max(8, rest-nameW), value: m.hostOf},
			{title: "STATE", width: connStateWidth, value: state},
		}
	}
	gaps := 4
	portW := 6
	userW := clamp(inner/8, 6, 14)
	nameW := clamp(inner/6, 12, 24)
	hostW := avail - connStateWidth - portW - userW - nameW - gaps
	if m.hasInclude() {
		// The row has to say "· from include" as well as everything else; the host
		// column is the one with room to give.
		hostW -= len(" · from include")
	}
	if hostW < 14 {
		hostW = 14
	}
	return []connCol{
		{title: "NAME", width: nameW, value: func(e connEntry) string { return e.Alias }},
		{title: "HOST", width: hostW, value: m.hostOf},
		{title: "USER", width: userW, value: func(e connEntry) string { return e.User }},
		{title: "PORT", width: portW, right: true, value: func(e connEntry) string {
			if e.Port == 0 {
				return "—"
			}
			return itoa(e.Port)
		}},
		{title: "STATE", width: connStateWidth, value: state},
	}
}

// hasInclude reports whether any visible entry comes from an Include, which is
// what reserves room for the marker on every row of the table.
func (m *Model) hasInclude() bool {
	for _, e := range m.visibleConnections() {
		if !e.Editable && !e.Cloud {
			return true
		}
	}
	return false
}

// credStore is the keyring bucket credentials live in, or nil when the machine
// has none. It is never probed up front: an unavailable keyring means "use the
// AWS chain", which needs no keyring at all.
func (m *Model) credStore() *keys.Store {
	if m.secrets != nil {
		return m.secrets
	}
	if m.sessions != nil {
		if store := m.sessions.Store(); store != nil {
			return store
		}
	}
	return keys.Default("omssh")
}

// hostOf is the address column: a DNS name for a host, s3://bucket/prefix for a
// bucket, and the alias when neither is set.
func (m *Model) hostOf(e connEntry) string {
	if e.HostName != "" {
		return e.HostName
	}
	return e.Alias
}

// bucketOf and prefixOf read the profile, which is where a bucket's parts live.
func (m *Model) bucketOf(e connEntry) string {
	if profile, ok := m.cfg.Cloud.Profiles[e.Alias]; ok {
		return profile.Bucket
	}
	return strings.TrimPrefix(strings.TrimSuffix(e.HostName, "/"), "s3://")
}

func (m *Model) prefixOf(e connEntry) string {
	profile, ok := m.cfg.Cloud.Profiles[e.Alias]
	if !ok || strings.Trim(profile.Prefix, "/") == "" {
		return "—"
	}
	return strings.Trim(profile.Prefix, "/")
}

// connHeaderRow labels the columns once, so the table explains itself.
func (m *Model) connHeaderRow(inner int) string {
	cols := m.connCols(inner)
	header := connMarkerPrefix
	for i, c := range cols {
		if i > 0 {
			header += " "
		}
		if c.right {
			header += padLeft(c.title, c.width)
			continue
		}
		header += padRight(c.title, c.width)
	}
	return m.st.PaneHeader.Render(padVisible(ansi.Truncate(header, inner, "…"), inner))
}

func (m *Model) renderConnRow(e connEntry, selected bool, inner int) string {
	cols := m.connCols(inner)
	marker := "  "
	if selected {
		marker = "▸ "
	}
	// A marker column says why a place is where it is: pinned, or used lately.
	switch {
	case m.favourite(e.Alias):
		marker += "★ "
	case m.recentRank(e.Alias) >= 0:
		marker += "◷ "
	default:
		marker += "  "
	}

	row := marker
	for i, c := range cols {
		if i > 0 {
			row += " "
		}
		value := truncate(c.value(e), c.width)
		if c.right {
			row += padLeft(value, c.width)
			continue
		}
		row += padRight(value, c.width)
	}
	if !e.Editable && !e.Cloud {
		// It lives in an Include, so the main file cannot change it. The host
		// column gave up the room for this above; say it here.
		row = strings.TrimSpace(row) + " · from include"
	}
	row = ansi.Truncate(row, inner, "…")

	if selected {
		// A filled row, so the choice is unmistakable rather than a tint.
		return m.st.PaletteItemActive.Width(inner).Render(padVisible(row, inner))
	}
	if !e.Editable && !e.Cloud {
		return m.st.CardMuted.Render(padVisible(row, inner))
	}
	return padVisible(row, inner)
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
	// The keys for this host, beside the host: choose it, or open a shell on it.
	lines = append(lines, pair("keys", "t terminal · Enter browse", "", ""))
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
	case "f":
		m.toggleFavourite()
	case "x":
		m.toggleHidden()
	case "tab", "right", "l":
		m.cycleSection(1)
	case "shift+tab", "left", "h":
		m.cycleSection(-1)
	case "H":
		if m.conn != nil {
			m.conn.showAll = !m.conn.showAll
			m.conn.cursor = 0
			if m.conn.showAll {
				m.toastInfo("Showing every host, including code hosts and hidden ones")
			} else {
				m.toastInfo("Showing the hosts you visit")
			}
		}
	case "esc":
		// Back to the launcher. The dashboard is still there behind it, and Esc
		// from the launcher goes to the files — one key, one direction.
		m.screen = screenHome
		m.home.cursor = 0
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

// openCloudForm asks for the six things a bucket profile needs. It is the same
// form machinery as a host block: one place to type into, one place to save.
func (m *Model) openCloudForm(provider string) {
	if provider == "" {
		provider = "s3"
	}
	form := connForm{kind: "cloud"}
	add := func(key, label, hint, value string) {
		field := connField{key: key, label: label, hint: hint}
		field.in.Set(value)
		form.fields = append(form.fields, field)
	}
	add("Name", "name", "profile name, shown in this list", "")
	add("Provider", "type", "s3 or r2", provider)
	add("Bucket", "bucket", "the bucket name", "")
	add("Region", "region", "us-east-1 for AWS · auto for R2", "")
	add("Endpoint", "endpoint", "R2: https://<account>.r2.cloudflarestorage.com", "")
	add("Prefix", "prefix", "optional: root the pane inside the bucket", "")
	add("AccessKeyID", "key id", "optional: stored in the OS keyring, not here", "")
	add("SecretKey", "secret", "optional · leave blank to use the AWS chain", "")
	form.fields[len(form.fields)-1].in.Masked = true
	m.form = form
	m.Overlay = OverlayForm
}

// saveCloudForm writes a bucket profile to the config, after saying what is wrong
// with it if anything is.
func (m *Model) saveCloudForm() tea.Cmd {
	profile := config.CloudProfile{}
	name := ""
	accessKey, secretKey := "", ""
	for _, field := range m.form.fields {
		value := strings.TrimSpace(field.in.Value)
		switch field.key {
		case "Name":
			name = value
		case "Provider":
			profile.Provider = strings.ToLower(value)
		case "Bucket":
			profile.Bucket = value
		case "Region":
			profile.Region = value
		case "Endpoint":
			profile.Endpoint = value
		case "Prefix":
			profile.Prefix = value
		case "AccessKeyID":
			accessKey = value
		case "SecretKey":
			secretKey = value
		}
	}
	switch {
	case name == "":
		m.form.err = "a profile needs a name"
		return nil
	case profile.Provider == "":
		profile.Provider = "s3"
	}
	if profile.Provider != "s3" && profile.Provider != "r2" {
		m.form.err = "type must be s3 or r2"
		return nil
	}
	if profile.Bucket == "" {
		m.form.err = "a bucket name is required"
		return nil
	}
	if profile.Provider == "r2" && profile.Endpoint == "" {
		m.form.err = "R2 needs an endpoint, https://<account>.r2.cloudflarestorage.com"
		return nil
	}
	if profile.Provider == "s3" && profile.Region == "" {
		m.form.err = "S3 needs a region, like us-east-1"
		return nil
	}
	if profile.Provider == "r2" && profile.Region == "" {
		profile.Region = "auto"
	}
	if (accessKey == "") != (secretKey == "") {
		m.form.err = "give both the key id and the secret, or neither"
		return nil
	}
	if m.cfg.Cloud.Profiles == nil {
		m.cfg.Cloud.Profiles = map[string]config.CloudProfile{}
	}
	m.cfg.Cloud.Profiles[name] = profile
	if accessKey != "" {
		if err := cloudcred.Store(m.credStore(), name, accessKey, secretKey); err != nil {
			m.form.err = "cannot store the credentials: " + err.Error()
			return nil
		}
	}
	m.form = connForm{}
	m.Overlay = OverlayNone
	m.saveConfig()
	m.loadConnections()
	for i, e := range m.visibleConnections() {
		if e.Alias == name {
			m.conn.cursor = i
		}
	}
	m.toastSuccess("Added " + name + " · Enter opens it")
	return nil
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
	if m.form.kind == "cloud" {
		title = "Add bucket"
	}
	if m.form.original != "" {
		title = "Edit " + m.form.original
	}
	lines := []string{m.st.CardTitle.Render(title), ""}

	for i, field := range m.form.fields {
		label := m.st.StatusMuted.Render(padRight(field.label, 6))
		value := m.st.CardText.Render(truncate(field.in.Display(), content-12))
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
		if m.form.kind == "cloud" {
			return m.saveCloudForm()
		}
		return m.saveConnectionForm()
	default:
		m.form.err = ""
		m.form.fields[m.form.focus].in.Handle(msg)
	}
	return nil
}
