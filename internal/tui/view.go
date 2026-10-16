package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/actions"
	"github.com/fakihariefnoto/omssh/internal/version"
)

// View renders the interface. Bubble Tea v2 takes the terminal features from
// the view itself, so the alt screen, mouse reporting and the window title are
// declared here rather than passed to the program.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "omssh"
	return v
}

// render lays out the whole screen: chrome, body, overlay.
func (m *Model) render() string {
	l := m.measure()
	rows := make([]string, 0, 6)

	if l.Tabs {
		rows = append(rows, m.renderTabs(l))
	}

	body := m.renderBody(l)
	if card := m.renderOverlay(l); card != "" {
		body = m.overlayOn(body, card, l)
	}
	if len(m.toasts) > 0 {
		body = m.toastOn(body, l)
	}
	rows = append(rows, body)

	if l.ActionBar {
		rows = append(rows, m.renderActionBar(l))
	}
	rows = append(rows, m.renderStatusBar(l))

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// renderBody draws the sidebar and the panes side by side.
func (m *Model) renderBody(l layout) string {
	if l.Sidebar {
		sidebar := lipgloss.NewStyle().
			Width(l.SidebarWidth).
			Height(l.Height - m.reservedRows(l)).
			Render(m.renderSidebar(l))
		return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, m.renderPanes(l))
	}
	return m.renderPanes(l)
}

// reservedRows is the vertical chrome outside the body.
func (m *Model) reservedRows(l layout) int {
	rows := 1 // status bar
	if l.Tabs {
		rows++
	}
	if l.ActionBar {
		rows += l.ActionBarRows
	}
	return rows
}

// renderTabs draws the session tab bar, with a right-aligned hint pair so the
// two keys that are always available are always visible.
func (m *Model) renderTabs(l layout) string {
	var b strings.Builder
	for i, tab := range m.tabs {
		dot := m.stateDot(tab.State)
		label := fmt.Sprintf("%s %s", dot, tab.Title)
		if i == m.activeTab {
			b.WriteString(m.st.TabActive.Render(label))
		} else {
			b.WriteString(m.st.TabInactive.Render(label))
		}
	}
	left := b.String()

	right := m.st.StatusMuted.Render("Ctrl+K actions") + "  " + m.st.StatusMuted.Render("F1 help")
	gap := l.Width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, l.Width, "")
	}
	return left + strings.Repeat(" ", gap) + right
}

// stateDot renders the connection state as a glyph, always paired with a shape
// so the meaning does not depend on colour alone.
func (m *Model) stateDot(state ConnState) string {
	switch state {
	case StateReady:
		return m.st.TabReady.Render(m.icons.DotReady)
	case StateConnecting:
		return m.st.TabBusy.Render(m.icons.DotBusy)
	case StateBusy:
		return m.st.TabBusy.Render(m.icons.DotBusy)
	case StateError:
		return m.st.TabError.Render(m.icons.DotError)
	default:
		return m.st.TabIdle.Render(m.icons.DotIdle)
	}
}

// renderPanes draws one or two panes, splitting the body width.
func (m *Model) renderPanes(l layout) string {
	if l.Panes == 1 {
		return m.renderPane(m.Focused, l.PaneWidth, l.PaneRows, l)
	}
	left := m.renderPane(0, l.PaneWidth, l.PaneRows, l)
	right := m.renderPane(1, l.PaneWidth, l.PaneRows, l)
	// A gap keeps the two borders from reading as one thick line.
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

// renderPane draws one pane: its breadcrumb, its border, and its rows.
func (m *Model) renderPane(index, width, rows int, l layout) string {
	pane := &m.Panes[index]
	focused := index == m.Focused

	header := m.renderBreadcrumb(pane, width, focused)
	inner := width - 2 // the border takes a cell on each side

	if l.PreviewWidth > 0 && focused && width >= 56 {
		list := m.renderRows(pane, inner-l.PreviewWidth-1, rows, focused)
		preview := m.renderPreview(l.PreviewWidth, rows)
		joined := lipgloss.JoinHorizontal(lipgloss.Top, list, m.st.Muted.Render("│"), preview)
		return lipgloss.JoinVertical(lipgloss.Left, header, m.border(joined, width, rows, focused))
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		header, m.border(m.renderRows(pane, inner, rows, focused), width, rows, focused))
}

// border wraps content in the pane border, accent when focused.
//
// Lip Gloss v2's Width is inclusive of the border, so the style is given the
// full pane width and the content must measure exactly width-2. Getting this
// wrong wraps every row and detaches the border, which is why
// TestPaneBoxesAreExact exists.
func (m *Model) border(content string, width, height int, focused bool) string {
	style := m.st.PaneBorderBlur
	if focused {
		style = m.st.PaneBorder
	}
	return style.Width(width).Height(height + 2).Render(content)
}

// renderBreadcrumb draws the pane title and its clickable path.
func (m *Model) renderBreadcrumb(pane *Pane, width int, focused bool) string {
	label := "LOCAL"
	if pane.Side == SideRemote {
		label = pane.Host
		if label == "" {
			label = "REMOTE"
		}
	}
	title := m.st.PaneTitleBlurred.Render(label)
	if focused {
		title = m.st.PaneTitle.Render(label)
	}
	if len(m.Panes) > 1 && focused {
		title = m.st.Accent.Render("▌") + title
	}

	path := pane.Path
	if pane.Side == SideRemote && pane.Host == "" {
		path = "not connected"
	}
	crumb := m.st.Breadcrumb.Render(crumbPath(path))
	if pane.Filter != "" {
		crumb = m.st.BreadcrumbActive.Render("/" + pane.Filter)
	}

	line := title + "  " + crumb
	right := m.st.StatusMuted.Render(fmt.Sprintf("%d items", len(pane.Visible())))
	gap := width - lipgloss.Width(line) - lipgloss.Width(right)
	if gap < 1 {
		// The breadcrumb shares its row with the pane beside it, so it is
		// clamped rather than allowed to run into the neighbour's title.
		return ansi.Truncate(line, width, "…")
	}
	return line + strings.Repeat(" ", gap) + right
}

// crumbPath shortens a path for display, keeping the tail.
func crumbPath(path string) string {
	if path == "" {
		return "."
	}
	return path
}

// renderRows draws the listing: entries, or an explanation of why there are
// none.
func (m *Model) renderRows(pane *Pane, width, rows int, focused bool) string {
	lines := make([]string, 0, rows)

	switch {
	case pane.Err != nil:
		lines = append(lines,
			m.st.Danger.Render("Cannot read this directory"),
			m.st.PaneEmpty.Render(truncate(pane.Err.Error(), width)),
			m.st.PaneEmpty.Render("Ctrl+R to retry"),
		)
	case !pane.Connected():
		lines = append(lines,
			m.st.PaneEmpty.Render("No connection yet"),
			m.st.PaneEmpty.Render("Ctrl+O to open one"),
		)
	case len(pane.Entries) == 0:
		lines = append(lines,
			m.st.PaneEmpty.Render("Empty directory"),
			m.st.PaneEmpty.Render("F7 to create a folder"),
		)
	case len(pane.Visible()) == 0:
		lines = append(lines,
			m.st.PaneEmpty.Render("Nothing matches "+pane.Filter),
			m.st.PaneEmpty.Render("Esc to clear the filter"),
		)
	default:
		visible := pane.Visible()
		offset := clamp(pane.Offset, 0, max(0, len(visible)-1))
		for i := offset; i < len(visible) && len(lines) < rows; i++ {
			lines = append(lines, m.renderRow(pane, visible[i], i == pane.Cursor, width, focused))
		}
	}

	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines[:rows], "\n")
}

// renderRow draws one entry: a selection marker, an icon, the name, and as many
// metadata columns as fit. Columns are dropped from the right in the order
// UX.md §12 documents, so a narrow pane shows the name and size rather than
// overflowing its border.
func (m *Model) renderRow(pane *Pane, e Entry, isCursor bool, width int, focused bool) string {
	marker := " "
	if pane.Selected[e.Name] {
		marker = "*"
	}

	icon := m.icons.File
	name := e.Name
	switch {
	case e.Symlink:
		icon = m.icons.Symlink
		name = e.Name + " → " + e.Link
	case e.Dir:
		icon = m.icons.Folder
	}
	if IsArchive(e.Name) && !e.Dir {
		icon = m.icons.Archive
	}

	sizeColumn := padLeft(humanSize(e.Size), 9)
	permColumn := permString(e)
	timeColumn := e.ModTime.Format("Jan 02 15:04")

	// Richest first: full metadata, then without the timestamp, then size only.
	candidates := []string{
		" " + sizeColumn + " " + permColumn + " " + timeColumn,
		" " + sizeColumn + " " + permColumn,
		" " + sizeColumn,
		"",
	}
	// marker, space, icon, space: four cells before the name. Getting this wrong
	// by one silently clips the last column of every row.
	const prefix = 4
	// The name is what a person reads; metadata is a bonus. A column is only
	// added if a readable name survives, which is what stops a wide layout from
	// rendering "n…" beside a timestamp nobody asked for.
	const minName = 12
	meta := ""
	for _, candidate := range candidates {
		if width-prefix-lipgloss.Width(candidate) >= minName {
			meta = candidate
			break
		}
	}

	nameWidth := max(1, width-prefix-lipgloss.Width(meta))
	line := marker + " " + icon + " " + padRight(name, nameWidth) + meta
	// Belt and braces: a styled row must never be wider than its pane, or it
	// wraps and the border breaks.
	line = ansi.Truncate(line, width, "")

	switch {
	case isCursor && focused:
		// The active pane's cursor is a filled bar: unmissable.
		return m.st.PaneRowCursor.Render(line)
	case isCursor:
		// The other pane's cursor stays visible but stops competing for
		// attention, so "which pane am I in" is never a question.
		return m.st.PaneRowSelected.Render("▏" + line[1:])
	case pane.Selected[e.Name]:
		return m.st.PaneRowSelected.Render(line)
	case e.Dir:
		return m.st.PaneDir.Render(line)
	case e.Symlink:
		return m.st.PaneSymlink.Render(line)
	default:
		return line
	}
}

// renderSidebar draws hosts, bookmarks and the job queue. Every section states
// what to do when it is empty rather than showing nothing.
func (m *Model) renderSidebar(l layout) string {
	width := l.SidebarWidth - 2
	rows := []string{
		m.st.SidebarTitle.Render("HOSTS"),
		m.st.PaneEmpty.Render(truncate("No connections yet", width)),
		m.st.PaneEmpty.Render(truncate("Ctrl+O to open one", width)),
		"",
		m.st.SidebarTitle.Render("BOOKMARKS"),
		m.st.PaneEmpty.Render(truncate("Alt+B adds this folder", width)),
		"",
	}

	jobs := m.st.SidebarTitle.Render("TRANSFERS")
	if m.Jobs() == 0 {
		rows = append(rows, jobs, m.st.PaneEmpty.Render("Nothing running"))
	} else {
		rows = append(rows, fmt.Sprintf("%s %d", jobs, m.Jobs()))
	}
	rows = append(rows, "", m.st.SidebarTitle.Render("THEME"),
		m.st.SidebarItem.Render(m.themeName))

	if len(m.dropped) > 0 {
		rows = append(rows, "", m.st.Warning.Render("Keys unavailable"),
			m.st.PaneEmpty.Render(truncate("Ctrl+K for alternatives", width)))
	}
	return strings.Join(rows, "\n")
}

// renderActionBar is the interface's main teaching surface: the verbs that apply
// to the current selection, with the keys that trigger them.
func (m *Model) renderActionBar(l layout) string {
	ctx := m.actionContext()
	applicable := m.reg.Applicable(ctx)
	palette := m.actionBarOrder()

	chosen := make([]actions.Action, 0, 6)
	byID := map[actions.ID]actions.Action{}
	for _, a := range applicable {
		byID[a.ID] = a
	}
	// Six verbs, matching the action bar in UX.md §2: the ones a person reaches
	// for appear, and everything else is one F9 away.
	for _, id := range palette {
		if a, ok := byID[id]; ok {
			chosen = append(chosen, a)
		}
		if len(chosen) == 6 {
			break
		}
	}

	var b strings.Builder
	if m.typeAheadBuf != "" {
		b.WriteString(m.st.ActionSelected.Render("/" + m.typeAheadBuf))
		b.WriteString("  ")
	}
	// The selection summary belongs to the status bar; repeating it here would
	// spend the row that teaches the shortcuts.
	if ctx.Connected && ctx.Selection > 0 {
		b.WriteString(m.st.ActionVerb.Render(fmt.Sprintf("Send → %s", m.paneLabel(1-m.Focused))))
		b.WriteString("   ")
	}

	if l.Compact {
		b.WriteString(m.st.ActionHint.Render("F9 all actions · Ctrl+K search"))
		return ansi.Truncate(b.String(), l.Width, "…")
	}

	for i, a := range chosen {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(m.st.ActionVerb.Render(a.Label))
		if key := m.keys.Primary(a.ID); key != "" {
			b.WriteString(" ")
			b.WriteString(m.st.ActionKey.Render(key))
		}
	}
	b.WriteString("  ")
	b.WriteString(m.st.ActionHint.Render("F9 more · Ctrl+K search"))

	return ansi.Truncate(b.String(), l.Width, "…")
}

// actionBarOrder is the priority list for the action bar. It is deliberately
// independent of registry order: what a person needs first goes first.
func (m *Model) actionBarOrder() []actions.ID {
	return []actions.ID{
		"file.view", "file.edit", "file.rename",
		"file.copy-to", "file.move-to", "file.delete",
		"archive.extract", "archive.compress",
		"nav.send", "nav.fetch", "nav.open", "nav.parent",
		"file.mkdir", "sel.toggle",
	}
}

// paneLabel names a pane the way the interface refers to it: "local", or the
// host name.
func (m *Model) paneLabel(index int) string {
	if index < 0 || index >= len(m.Panes) {
		return "local"
	}
	pane := m.Panes[index]
	if pane.Side == SideLocal {
		return "local"
	}
	if pane.Host == "" {
		return "remote"
	}
	return pane.Host
}

// renderStatusBar names the mode, summarises the selection, and reports what is
// running. It is never dropped, at any size.
func (m *Model) renderStatusBar(l layout) string {
	mode := m.mode
	if mode == "" {
		mode = "NORMAL"
	}
	left := m.st.StatusMode.Render(mode)

	parts := []string{}
	if ctx := m.actionContext(); ctx.Selection > 0 {
		parts = append(parts, fmt.Sprintf("%d selected · %s", ctx.Selection, humanSize(m.Pane().SelectionBytes())))
	}
	if m.Jobs() > 0 {
		parts = append(parts, fmt.Sprintf("%d %s running", m.Jobs(), plural(m.Jobs(), "job", "jobs")))
	}
	if pending := m.resolver.Pending(); pending != "" {
		parts = append(parts, "…"+pending)
	}
	if m.statusHint != "" && m.typeAheadBuf != "" {
		parts = append(parts, "type-ahead "+m.statusHint)
	}
	summary := strings.Join(parts, " · ")

	right := m.st.StatusMuted.Render("? help · Ctrl+Q quit")
	if m.quitHint != "" {
		right = m.st.Warning.Render(m.quitHint)
	}
	if m.Overlay != OverlayNone && m.Overlay != OverlayConfirm && m.Overlay != OverlayPrompt {
		right = m.st.StatusMuted.Render("Esc back")
	}

	mid := " " + summary
	line := left + mid
	gap := l.Width - lipgloss.Width(line) - lipgloss.Width(right) - 1
	if gap < 1 {
		return ansi.Truncate(line, l.Width, "")
	}
	return line + strings.Repeat(" ", gap) + right + " "
}

// toastOn floats the notifications into the bottom-right of the body, the way
// UX.md §9 describes: they report completions without stealing a row or moving
// anything else on screen.
func (m *Model) toastOn(body string, l layout) string {
	lines := strings.Split(body, "\n")
	for i := len(m.toasts) - 1; i >= 0; i-- {
		row := len(lines) - 1 - (len(m.toasts) - 1 - i)
		if row < 0 {
			break
		}
		toast := m.toasts[i]
		text := " " + toast.text + " "
		styled := m.toastStyle(toast.kind).Render(text)
		width := lipgloss.Width(styled)
		if width > l.Width-2 {
			styled = m.toastStyle(toast.kind).Render(truncate(text, max(1, l.Width-2)))
			width = lipgloss.Width(styled)
		}
		left := max(0, l.Width-width-1)
		lines[row] = spliceLine(lines[row], styled, left, width, l.Width)
	}
	return strings.Join(lines, "\n")
}

// toastStyle picks the notification's colour by what it means.
func (m *Model) toastStyle(kind toastKind) lipgloss.Style {
	switch kind {
	case toastSuccess:
		return m.st.ToastSuccess
	case toastWarning:
		return m.st.ToastWarning
	case toastError:
		return m.st.ToastError
	default:
		return m.st.ToastInfo
	}
}

// overlayOn splices an overlay card onto the body, so the panes stay visible
// around it instead of being replaced.
func (m *Model) overlayOn(body, card string, l layout) string {
	cardLines := strings.Split(card, "\n")
	bodyLines := strings.Split(body, "\n")

	// A card taller than the screen loses its middle, never its footer: the
	// footer is where it says how to get out. The last lines of a card are its
	// text, its bottom padding and its bottom border, so three are kept.
	const keepTail = 3
	if len(cardLines) > len(bodyLines) && len(bodyLines) >= keepTail+2 {
		head := cardLines[:len(bodyLines)-1-keepTail]
		tail := cardLines[len(cardLines)-keepTail:]
		clipped := make([]string, 0, len(head)+1+len(tail))
		clipped = append(clipped, head...)
		clipped = append(clipped, m.st.CardMuted.Render("  …"))
		clipped = append(clipped, tail...)
		cardLines = clipped
	}

	cardWidth := 0
	for _, line := range cardLines {
		cardWidth = max(cardWidth, lipgloss.Width(line))
	}
	top := max(0, (len(bodyLines)-len(cardLines))/2)

	out := make([]string, len(bodyLines))
	copy(out, bodyLines)
	for i, cardLine := range cardLines {
		row := top + i
		if row >= len(out) {
			break
		}
		left := max(0, (l.Width-cardWidth)/2)
		out[row] = spliceLine(out[row], padVisible(cardLine, cardWidth), left, cardWidth, l.Width)
	}
	return strings.Join(out, "\n")
}

// spliceLine replaces the middle of a styled line with replacement, keeping the
// ANSI-styled text on either side intact.
func spliceLine(base, replacement string, left, replacementWidth, width int) string {
	head := padVisible(ansi.Truncate(base, left, ""), left)
	tail := ansi.Cut(base, min(left+replacementWidth, width), width+1)
	return head + replacement + tail
}

// padVisible pads a styled string with spaces to a visible width.
func padVisible(s string, width int) string {
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// versionLine is shown by the version action and the onboarding card.
func (m *Model) versionLine() string { return version.String() }
