package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
func (m *Model) open() {
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok {
		return
	}
	if entry.Dir {
		m.navigate(filepath.Join(pane.Path, entry.Name))
		return
	}
	m.preview = true
	m.mode = "PREVIEW"
}

// parent moves the pane to the directory above.
func (m *Model) parent() {
	pane := m.Pane()
	parent := filepath.Dir(filepath.Clean(pane.Path))
	if parent == pane.Path {
		return
	}
	m.navigate(parent)
}

// navigate changes the pane's directory and records it in the history, keeping
// the cursor on the directory that was just entered so the way back is obvious.
func (m *Model) navigate(path string) {
	pane := m.Pane()
	if path == "" || path == pane.Path {
		return
	}
	previous := pane.Path
	pane.Path = path
	pane.LastDir = previous
	pane.Cursor, pane.Offset, pane.Filter = 0, 0, ""
	pane.Load(m.showHidden)

	// Trim any forward history and append, the way a browser does.
	if pane.HIndex < len(pane.History)-1 {
		pane.History = pane.History[:pane.HIndex+1]
	}
	pane.History = append(pane.History, path)
	pane.HIndex = len(pane.History) - 1
}

// historyBack and historyForward walk the visited-directory stack.
func (m *Model) historyBack() {
	pane := m.Pane()
	if pane.HIndex <= 0 {
		return
	}
	pane.HIndex--
	pane.Path = pane.History[pane.HIndex]
	pane.Cursor, pane.Offset = 0, 0
	pane.Load(m.showHidden)
}

func (m *Model) historyForward() {
	pane := m.Pane()
	if pane.HIndex >= len(pane.History)-1 {
		return
	}
	pane.HIndex++
	pane.Path = pane.History[pane.HIndex]
	pane.Cursor, pane.Offset = 0, 0
	pane.Load(m.showHidden)
}

// goHome jumps to the home directory of the focused pane.
func (m *Model) goHome() {
	home, err := os.UserHomeDir()
	if err != nil {
		m.toastError("Cannot find your home directory")
		return
	}
	m.navigate(home)
}

// goLastDir is the "-" behaviour: back to the previous directory.
func (m *Model) goLastDir() {
	pane := m.Pane()
	if pane.LastDir != "" {
		m.navigate(pane.LastDir)
	}
}

// refresh reloads the listing, keeping the cursor on the same entry where it
// still exists, so a refresh does not move you.
func (m *Model) refresh() {
	pane := m.Pane()
	previous, had := pane.CursorEntry()
	pane.Load(m.showHidden)
	if !had {
		return
	}
	for i, e := range pane.Visible() {
		if e.Name == previous.Name {
			pane.Cursor = i
			break
		}
	}
	m.ensureVisible()
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

// --- file operations (local) -------------------------------------------------

// targets returns what an operation should act on: the selection when there is
// one, otherwise the cursor. No operation is cursor-only.
func (m *Model) targets() []Entry {
	pane := m.Pane()
	selected := pane.Selected
	if len(selected) > 0 {
		out := make([]Entry, 0, len(selected))
		for _, e := range pane.Entries {
			if selected[e.Name] {
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

// localPath joins a name onto the focused pane, refusing to act on a remote pane
// until that side exists.
func (m *Model) localPath(name string) (string, error) {
	pane := m.Pane()
	if pane.Side != SideLocal {
		return "", errors.New("remote panes arrive with connections")
	}
	return filepath.Join(pane.Path, name), nil
}

func (m *Model) createDir(name string) {
	path, err := m.localPath(name)
	if err != nil {
		m.toastError(err.Error())
		return
	}
	if err := os.Mkdir(path, 0o750); err != nil {
		m.toastError(fmt.Sprintf("Cannot create %s: %v", name, err))
		return
	}
	m.refresh()
	m.selectByName(name)
	m.toastSuccess("Created " + name)
}

func (m *Model) createFile(name string) {
	path, err := m.localPath(name)
	if err != nil {
		m.toastError(err.Error())
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		m.toastError(fmt.Sprintf("Cannot create %s: %v", name, err))
		return
	}
	_ = file.Close()
	m.refresh()
	m.selectByName(name)
	m.toastSuccess("Created " + name)
}

func (m *Model) renameCursor(newName string) {
	entry, ok := m.Pane().CursorEntry()
	if !ok {
		return
	}
	from, err := m.localPath(entry.Name)
	if err != nil {
		m.toastError(err.Error())
		return
	}
	to, err := m.localPath(newName)
	if err != nil {
		m.toastError(err.Error())
		return
	}
	if err := os.Rename(from, to); err != nil {
		m.toastError(fmt.Sprintf("Cannot rename: %v", err))
		return
	}
	m.refresh()
	m.selectByName(newName)
	m.toastSuccess("Renamed to " + newName)
}

// deleteTargets removes what the confirmation dialog approved. There is no
// trash and no undo, which is exactly why the dialog states the facts first.
func (m *Model) deleteTargets(entries []Entry) {
	failed := 0
	for _, e := range entries {
		path, err := m.localPath(e.Name)
		if err != nil {
			m.toastError(err.Error())
			return
		}
		if err := os.RemoveAll(path); err != nil {
			failed++
		}
	}
	m.clearSelection()
	m.refresh()
	switch failed {
	case 0:
		m.toastSuccess(fmt.Sprintf("Deleted %d %s", len(entries), plural(len(entries), "item", "items")))
	default:
		m.toastError(fmt.Sprintf("Deleted %d of %d; %d failed", len(entries)-failed, len(entries), failed))
	}
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
func (m *Model) revealInOtherPane() {
	if len(m.Panes) < 2 {
		return
	}
	entry, ok := m.Pane().CursorEntry()
	if !ok {
		return
	}
	other := 1 - m.Focused
	target := filepath.Join(m.Panes[other].Path, entry.Name)
	if entry.Dir {
		m.Panes[other].Path = target
		m.Panes[other].Load(m.showHidden)
	}
	m.Focused = other
	m.ensureVisible()
}
