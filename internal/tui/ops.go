package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// --- cursor movement ---------------------------------------------------------

// Pane returns the focused pane.
func (m *Model) Pane() *Pane { return &m.Panes[m.Focused] }

// moveCursor moves the cursor by delta, clamped to the listing.
func (m *Model) moveCursor(delta int) {
	pane := m.Pane()
	visible := pane.Visible()
	if len(visible) == 0 {
		pane.Cursor = 0
		return
	}
	pane.Cursor = clamp(pane.Cursor+delta, 0, len(visible)-1)
	m.ensureVisible()
}

// page moves the cursor by a screenful.
func (m *Model) page(delta int) {
	step := m.paneRows() * delta
	m.moveCursor(step)
}

// toFirst and toLast jump to the ends of the listing.
func (m *Model) toFirst() { m.Pane().Cursor = 0; m.ensureVisible() }
func (m *Model) toLast() {
	if n := len(m.Pane().Visible()); n > 0 {
		m.Pane().Cursor = n - 1
	}
	m.ensureVisible()
}

// ensureVisible scrolls the pane so the cursor is on screen.
func (m *Model) ensureVisible() {
	pane := m.Pane()
	rows := m.paneRows()
	if rows <= 0 {
		return
	}
	if pane.Cursor < pane.Offset {
		pane.Offset = pane.Cursor
	}
	if pane.Cursor >= pane.Offset+rows {
		pane.Offset = pane.Cursor - rows + 1
	}
	pane.Offset = clamp(pane.Offset, 0, max(0, len(pane.Visible())-1))
}

// --- navigation --------------------------------------------------------------

// open enters the cursor entry: a directory becomes the pane's path, a file
// moves focus to the preview.
func (m *Model) open() tea.Cmd {
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok {
		return nil
	}
	if entry.Dir {
		return m.navigate(pane.FS.Join(pane.Path, entry.Name))
	}
	m.preview = true
	m.mode = "PREVIEW"
	return nil
}

// parent moves the pane to the directory above, stopping at the root.
func (m *Model) parent() tea.Cmd {
	pane := m.Pane()
	if pane.FS == nil {
		return nil
	}
	parent := pane.FS.Dir(pane.Path)
	if parent == pane.Path || parent == "" {
		return nil
	}
	return m.navigate(parent)
}

// navigate changes the pane's directory and records it in the history, keeping
// the cursor on the directory that was just entered so the way back is obvious.
func (m *Model) navigate(path string) tea.Cmd {
	pane := m.Pane()
	if path == "" || path == pane.Path {
		return nil
	}
	previous := pane.Path
	pane.Path = path
	pane.LastDir = previous
	pane.Cursor, pane.Offset, pane.Filter = 0, 0, ""

	// Trim any forward history and append, the way a browser does.
	if pane.HIndex < len(pane.History)-1 {
		pane.History = pane.History[:pane.HIndex+1]
	}
	pane.History = append(pane.History, path)
	pane.HIndex = len(pane.History) - 1
	return m.loadPaneAsync(m.Focused)
}

// historyBack and historyForward walk the visited-directory stack.
func (m *Model) historyBack() tea.Cmd {
	pane := m.Pane()
	if pane.HIndex <= 0 {
		return nil
	}
	pane.HIndex--
	pane.Path = pane.History[pane.HIndex]
	pane.Cursor, pane.Offset = 0, 0
	return m.loadPaneAsync(m.Focused)
}

func (m *Model) historyForward() tea.Cmd {
	pane := m.Pane()
	if pane.HIndex >= len(pane.History)-1 {
		return nil
	}
	pane.HIndex++
	pane.Path = pane.History[pane.HIndex]
	pane.Cursor, pane.Offset = 0, 0
	return m.loadPaneAsync(m.Focused)
}

// goHome jumps to the home directory of the focused pane: the user's own locally,
// the session's root remotely.
func (m *Model) goHome() tea.Cmd {
	pane := m.Pane()
	if pane.Home != "" {
		return m.navigate(pane.Home)
	}
	m.toastInfo("This pane has no home directory")
	return nil
}

// goLastDir is the "-" behaviour: back to the previous directory.
func (m *Model) goLastDir() tea.Cmd {
	pane := m.Pane()
	if pane.LastDir != "" {
		return m.navigate(pane.LastDir)
	}
	return nil
}

// refresh reloads the listing, keeping the cursor on the same entry where it
// still exists, so a refresh does not move you.
func (m *Model) refresh() tea.Cmd {
	pane := m.Pane()
	previous, had := pane.CursorEntry()
	cmd := m.loadPaneAsync(m.Focused)
	if !had {
		return cmd
	}
	// The cursor follows the entry it was on. For a local pane that has already
	// happened; for a remote one the listing arrives later, so remember the name.
	pane.pendingCursor = previous.Name
	return cmd
}

// typeAhead jumps the cursor to the next entry starting with the typed prefix.
// Repeating the same character cycles through the matches, which is what file
// managers have done for forty years and therefore needs no explanation.
func (m *Model) typeAhead(char string) {
	pane := m.Pane()
	visible := pane.Visible()
	if len(visible) == 0 {
		return
	}
	if char == m.typeAheadLast && m.typeAheadBuf == char {
		// Same single character pressed again: cycle from the cursor.
		for i := 1; i <= len(visible); i++ {
			idx := (pane.Cursor + i) % len(visible)
			if strings.HasPrefix(strings.ToLower(visible[idx].Name), char) {
				pane.Cursor = idx
				m.ensureVisible()
				return
			}
		}
		return
	}
	if m.typeAheadLast != char {
		m.typeAheadBuf += char
	} else {
		m.typeAheadBuf = char
	}
	m.typeAheadLast = char
	needle := strings.ToLower(m.typeAheadBuf)

	// Prefer a match at or after the cursor, then wrap around.
	for i := 0; i < len(visible); i++ {
		idx := (pane.Cursor + i) % len(visible)
		if strings.HasPrefix(strings.ToLower(visible[idx].Name), needle) {
			pane.Cursor = idx
			m.ensureVisible()
			m.statusHint = "/" + m.typeAheadBuf
			return
		}
	}
}

// resetTypeAhead forgets the typed prefix.
func (m *Model) resetTypeAhead() {
	m.typeAheadBuf, m.typeAheadLast, m.statusHint = "", "", ""
}

// --- selection ---------------------------------------------------------------

// toggleSelect selects the entry under the cursor and advances, so building a
// selection is one key pressed repeatedly.
func (m *Model) toggleSelect() {
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok {
		return
	}
	if pane.Selected[entry.Name] {
		delete(pane.Selected, entry.Name)
	} else {
		pane.Selected[entry.Name] = true
	}
	m.moveCursor(1)
}

// extendSelection extends the selection in a direction, selecting as it goes.
func (m *Model) extendSelection(delta int) {
	pane := m.Pane()
	before, hadBefore := pane.CursorEntry()
	m.moveCursor(delta)
	after, hadAfter := pane.CursorEntry()
	if hadBefore {
		pane.Selected[before.Name] = true
	}
	if hadAfter {
		pane.Selected[after.Name] = true
	}
	if !hadBefore && !hadAfter {
		return
	}
	m.mode = "SELECT"
}

func (m *Model) selectAll() {
	pane := m.Pane()
	for _, e := range pane.Visible() {
		pane.Selected[e.Name] = true
	}
}

func (m *Model) invertSelection() {
	pane := m.Pane()
	for _, e := range pane.Visible() {
		if pane.Selected[e.Name] {
			delete(pane.Selected, e.Name)
		} else {
			pane.Selected[e.Name] = true
		}
	}
}

func (m *Model) clearSelection() {
	m.Pane().Selected = map[string]bool{}
}

// --- file operations ---------------------------------------------------------

// targets returns what an operation should act on: the selection when there is
// one, otherwise the cursor. No operation is cursor-only.
func (m *Model) targets() []Entry {
	pane := m.Pane()
	if len(pane.Selected) > 0 {
		out := make([]Entry, 0, len(pane.Selected))
		for _, e := range pane.Entries {
			if pane.Selected[e.Name] {
				out = append(out, e)
			}
		}
		return out
	}
	if e, ok := pane.CursorEntry(); ok {
		return []Entry{e}
	}
	return nil
}

// paneFS is the filesystem the focused pane is showing, or an error the caller
// can show: a remote pane with no connection has nothing to write to.
func (m *Model) paneFS() (vfs.FS, error) {
	pane := m.Pane()
	if pane.FS == nil {
		return nil, errors.New("this pane is not connected")
	}
	return pane.FS, nil
}

// pathFor builds the full path of a name in the focused pane.
func (m *Model) pathFor(name string) (vfs.FS, string, error) {
	fsys, err := m.paneFS()
	if err != nil {
		return nil, "", err
	}
	return fsys, fsys.Join(m.Pane().Path, name), nil
}

func (m *Model) createDir(name string) tea.Cmd {
	fsys, path, err := m.pathFor(name)
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	if err := fsys.Mkdir(path, 0o750); err != nil {
		m.toastError(fmt.Sprintf("Cannot create %s: %v", name, err))
		return nil
	}
	m.selectByName(name)
	m.toastSuccess("Created " + name)
	return m.refresh()
}

func (m *Model) createFile(name string) tea.Cmd {
	fsys, path, err := m.pathFor(name)
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	// O_EXCL so "new file" never quietly truncates an existing one.
	file, err := fsys.OpenFile(path, vfs.O_WRONLY|vfs.O_CREATE|vfs.O_EXCL, 0o600)
	if err != nil {
		m.toastError(fmt.Sprintf("Cannot create %s: %v", name, err))
		return nil
	}
	if err := file.Close(); err != nil {
		m.toastError(fmt.Sprintf("Cannot create %s: %v", name, err))
		return nil
	}
	m.selectByName(name)
	m.toastSuccess("Created " + name)
	return m.refresh()
}

func (m *Model) renameCursor(newName string) tea.Cmd {
	entry, ok := m.Pane().CursorEntry()
	if !ok {
		return nil
	}
	fsys, err := m.paneFS()
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	from := fsys.Join(m.Pane().Path, entry.Name)
	to := fsys.Join(m.Pane().Path, newName)
	if err := fsys.Rename(from, to); err != nil {
		m.toastError(fmt.Sprintf("Cannot rename: %v", err))
		return nil
	}
	m.selectByName(newName)
	m.toastSuccess("Renamed to " + newName)
	return m.refresh()
}

// deleteTargets removes what the confirmation dialog approved. There is no trash
// and no undo, which is exactly why the dialog states the facts first.
func (m *Model) deleteTargets(entries []Entry) tea.Cmd {
	fsys, err := m.paneFS()
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	failed := 0
	for _, e := range entries {
		path := fsys.Join(m.Pane().Path, e.Name)
		if err := fsys.RemoveAll(path); err != nil {
			failed++
		}
	}
	m.clearSelection()
	cmd := m.refresh()
	switch failed {
	case 0:
		m.toastSuccess(fmt.Sprintf("Deleted %d %s", len(entries), plural(len(entries), "item", "items")))
	default:
		m.toastError(fmt.Sprintf("Deleted %d of %d; %d failed", len(entries)-failed, len(entries), failed))
	}
	return cmd
}

// selectByName puts the cursor on a named entry after a listing change.
func (m *Model) selectByName(name string) {
	for i, e := range m.Pane().Visible() {
		if e.Name == name {
			m.Pane().Cursor = i
			break
		}
	}
	m.ensureVisible()
}

// switchPane moves focus to the other pane, wrapping.
func (m *Model) switchPane(delta int) {
	if len(m.Panes) < 2 {
		return
	}
	m.Focused = ((m.Focused+delta)%len(m.Panes) + len(m.Panes)) % len(m.Panes)
	m.ensureVisible()
}

// revealInOtherPane opens the cursor entry in the other pane.
func (m *Model) revealInOtherPane() tea.Cmd {
	if len(m.Panes) < 2 {
		return nil
	}
	pane := m.Pane()
	other := 1 - m.Focused
	if pane.FS == nil || m.Panes[other].FS == nil {
		m.toastInfo("Both panes need a directory to reveal between")
		return nil
	}
	entry, ok := pane.CursorEntry()
	if !ok {
		return nil
	}
	if !entry.Dir {
		m.Focused = other
		m.ensureVisible()
		return nil
	}
	// The other pane may be a different filesystem, so the path is built with its
	// own arithmetic.
	m.Panes[other].Path = m.Panes[other].FS.Join(m.Panes[other].Path, entry.Name)
	m.Focused = other
	return m.loadPaneAsync(other)
}

// --- asynchronous listings ---------------------------------------------------

// dirLoadedMsg carries a finished listing back to the update loop.
type dirLoadedMsg struct {
	pane    int
	path    string
	entries []Entry
	err     error
}

// loadPaneAsync lists a pane without blocking the interface, which matters over
// a slow link: a directory of a thousand entries is a thousand round trips, and
// the update loop cannot wait for them.
//
// A local pane is listed inline. It takes microseconds, and keeping it
// synchronous means navigation stays instant and the tests stay deterministic.
func (m *Model) loadPaneAsync(index int) tea.Cmd {
	if index < 0 || index >= len(m.Panes) {
		return nil
	}
	pane := &m.Panes[index]
	if pane.FS == nil {
		pane.Entries, pane.Err, pane.Loading = nil, nil, false
		return nil
	}
	if _, local := pane.FS.(*vfs.Local); local {
		pane.Loading = false
		pane.Load(m.showHidden)
		return nil
	}

	// The previous entries stay on screen while the new ones are fetched, so a
	// slow link shows the old listing with a marker rather than an empty pane.
	pane.Loading = true
	fsys, path, hidden := pane.FS, pane.Path, m.showHidden
	return func() tea.Msg {
		entries, err := listEntries(fsys, path, hidden)
		return dirLoadedMsg{pane: index, path: path, entries: entries, err: err}
	}
}

// listEntries reads a directory and filters it, off the update loop.
func listEntries(fsys vfs.FS, path string, hidden bool) ([]Entry, error) {
	entries, err := readDir(fsys, path)
	if err != nil {
		return nil, err
	}
	visible := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if !hidden && strings.HasPrefix(entry.Name, ".") {
			continue
		}
		visible = append(visible, entry)
	}
	sortEntries(visible)
	return visible, nil
}

// applyListing folds a finished listing into its pane, unless the pane has moved
// on since the request was made.
func (m *Model) applyListing(msg dirLoadedMsg) {
	if msg.pane < 0 || msg.pane >= len(m.Panes) {
		return
	}
	pane := &m.Panes[msg.pane]
	if pane.Path != msg.path {
		// The user navigated again while this was in flight; the newer request
		// owns the pane.
		return
	}
	pane.Loading = false
	pane.Err = msg.err
	if msg.err != nil {
		pane.Entries = nil
		return
	}
	pane.Entries = msg.entries
	if pane.pendingCursor != "" {
		for i, entry := range pane.Entries {
			if entry.Name == pane.pendingCursor {
				pane.Cursor = i
				break
			}
		}
		pane.pendingCursor = ""
	}
	if pane.Cursor >= len(pane.Entries) {
		pane.Cursor = max(0, len(pane.Entries)-1)
	}
	if msg.pane == m.Focused {
		m.ensureVisible()
	}
}
