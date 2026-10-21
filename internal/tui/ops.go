package tui

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// nextPane is the pane after index, wrapping: with four panes "the other one" is
// "the next one", which is the rule the copy engine and the view toggles share.
func (m *Model) nextPane(index int) int {
	if len(m.Panes) < 2 {
		return index
	}
	return (index + 1) % len(m.Panes)
}

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

// goToPath resolves what a person typed into "go to path". "~" is the pane's own
// home — the user's locally, the session's remotely — and a bare name is relative
// to where the pane already is. A path that is not a directory leaves the pane
// where it was, because a file manager that navigates into its own error message
// is worse than one that says what went wrong.
func (m *Model) goToPath(value string) {
	pane := m.Pane()
	if pane.FS == nil {
		return
	}
	path := resolveTypedPath(pane, value)
	if path == "" || path == pane.Path {
		return
	}
	info, err := pane.FS.Stat(path)
	if err != nil {
		m.toastError("Cannot open " + path + ": no such folder")
		return
	}
	if !info.IsDir() {
		m.toastError(path + " is a file, not a folder")
		return
	}
	m.overlayCmd = m.navigate(path)
}

// resolveTypedPath turns typed text into a path on the pane's filesystem.
func resolveTypedPath(pane *Pane, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value == "~" {
		if pane.Home != "" {
			return pane.Home
		}
		return pane.Path
	}
	if rest, ok := strings.CutPrefix(value, "~/"); ok {
		home := pane.Home
		if home == "" {
			home = "/"
		}
		return pane.FS.Join(home, rest)
	}
	// "/~/x" is what a prefilled "/" plus a typed "~/x" produces. Treat the tilde
	// as home even behind that slash rather than opening a folder named "~".
	if rest, ok := strings.CutPrefix(value, "/~/"); ok {
		home := pane.Home
		if home == "" {
			home = "/"
		}
		return pane.FS.Join(home, rest)
	}
	if !strings.HasPrefix(value, "/") {
		return pane.FS.Join(pane.Path, value)
	}
	// An absolute path still goes through the filesystem's own Join so the
	// trailing slashes and ".." segments are cleaned by the code that knows which
	// filesystem this is.
	return pane.FS.Join(value)
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

	// Sync browse: the other pane follows to the same path when that path is a
	// directory over there. Absolute paths line up because both filesystems are
	// rooted at "/", so this walks two trees together without a translation
	// table to get wrong.
	if cmd := m.followOtherPanes(path); cmd != nil {
		return tea.Batch(m.loadPaneAsync(m.Focused), cmd)
	}
	return m.loadPaneAsync(m.Focused)
}

// followOtherPanes asks the pane that is not focused to follow to path, if the
// path exists there.
func (m *Model) followOtherPanes(path string) tea.Cmd {
	if !m.syncBrowse || len(m.Panes) < 2 {
		return nil
	}
	index := m.nextPane(m.Focused)
	pane := m.Panes[index]
	if pane.FS == nil || pane.Path == path {
		return nil
	}
	fsys := pane.FS
	return func() tea.Msg {
		info, err := fsys.Stat(path)
		if err != nil || !info.IsDir() {
			return nil
		}
		return syncPaneMsg{index: index, path: path}
	}
}

// syncPaneMsg says the other pane can follow to this path.
type syncPaneMsg struct {
	index int
	path  string
}

// followInPane moves a pane that is not the focused one, without disturbing the
// focus: sync browse is a view, not a mode change.
func (m *Model) followInPane(msg syncPaneMsg) tea.Cmd {
	if msg.index < 0 || msg.index >= len(m.Panes) {
		return nil
	}
	pane := &m.Panes[msg.index]
	if pane.Path == msg.path {
		return nil
	}
	pane.LastDir = pane.Path
	pane.Path = msg.path
	pane.Cursor, pane.Offset, pane.Filter = 0, 0, ""
	pane.History = append(pane.History, msg.path)
	pane.HIndex = len(pane.History) - 1
	return m.loadPaneAsync(msg.index)
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

// refreshAll reloads both panes: an extraction or an archive write touches one
// side and is usually aimed at the other.
func (m *Model) refreshAll() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.Panes))
	for i := range m.Panes {
		if cmd := m.loadPaneAsync(i); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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

// chmodTargets applies a mode from the prompt to every target. The mode is octal
// because that is what people type and what every manual lists.
func (m *Model) chmodTargets(value string) tea.Cmd {
	mode, err := strconv.ParseUint(strings.TrimSpace(value), 8, 32)
	if err != nil || mode > 0o7777 {
		m.toastError("Permissions are octal, like 644 or 755")
		return nil
	}
	fsys, err := m.paneFS()
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	targets := m.targets()
	failed := 0
	for _, e := range targets {
		if err := fsys.Chmod(fsys.Join(m.Pane().Path, e.Name), os.FileMode(mode)); err != nil {
			failed++
		}
	}
	if failed > 0 {
		m.toastError(fmt.Sprintf("%d of %d could not be changed", failed, len(targets)))
	} else {
		m.toastSuccess(fmt.Sprintf("Permissions set to %o", mode))
	}
	return m.refresh()
}

// makeSymlink creates a link next to the cursor entry, pointing at it.
func (m *Model) makeSymlink(name string) tea.Cmd {
	entry, ok := m.Pane().CursorEntry()
	if !ok {
		return nil
	}
	fsys, err := m.paneFS()
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	target := entry.Name
	link := fsys.Join(m.Pane().Path, name)
	if err := fsys.Symlink(target, link); err != nil {
		m.toastError(fmt.Sprintf("Cannot create the link: %v", err))
		return nil
	}
	m.selectByName(name)
	m.toastSuccess("Linked " + name + " → " + target)
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
	m.syncActiveTab()
	m.ensureVisible()
}

// syncActiveTab keeps the strip's highlight on the session that has the keys.
// With one pane that is a nicety; with four panes on screen it is the only thing
// that says which of them you are typing into.
func (m *Model) syncActiveTab() {
	host := m.Pane().Host
	for i, tab := range m.tabs {
		if tab.Host == host {
			m.activeTab = i
			return
		}
	}
}

// revealInOtherPane opens the cursor entry in the other pane.
func (m *Model) revealInOtherPane() tea.Cmd {
	if len(m.Panes) < 2 {
		return nil
	}
	pane := m.Pane()
	other := m.nextPane(m.Focused)
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
	m.previewGen++
	if msg.err != nil {
		pane.Entries = nil
		if pane.Host != "" {
			// A directory that cannot be read is the first honest sign that a
			// connection is no longer what it was.
			m.tabState(pane.Host, StateError)
		}
		return
	}
	if pane.Host != "" {
		m.tabState(pane.Host, StateReady)
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
