package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/actions"
	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/xfer"
)

// Copying to a chosen place is the same engine as pasting into the next pane; only
// the destination is different. These helpers keep that one engine and one report,
// so "Copy to…" behaves exactly like p: progress in the sidebar, marks kept on a
// copy, sources removed only after a move arrives.

// copyCursorTo copies the focused pane's targets into a pane that is not it.
func (m *Model) copyCursorTo(to int, cut bool) tea.Cmd {
	if len(m.Panes) < 2 {
		m.quitHint = "There is only one pane to copy into"
		return nil
	}
	if to < 0 || to >= len(m.Panes) || to == m.Focused {
		m.quitHint = "There is no other pane to copy into"
		return nil
	}
	targets := m.targets()
	if len(targets) == 0 {
		m.quitHint = "Nothing under the cursor to copy"
		return nil
	}
	return m.copyEntries(m.Focused, to, targets, cut)
}

// copyEntries runs one copy between two panes.
func (m *Model) copyEntries(from, to int, targets []Entry, cut bool) tea.Cmd {
	src, dst := m.Panes[from], m.Panes[to]
	if src.FS == nil || dst.FS == nil {
		m.toastError("Both panes need a directory to copy between")
		return nil
	}
	names := make([]string, 0, len(targets))
	for _, entry := range targets {
		names = append(names, entry.Name)
	}
	return m.copyNames(from, to, src.FS, src.Path, dst.FS, dst.Path, names, cut)
}

// copyNames is the shared middle: register the transfer, run it, report it.
func (m *Model) copyNames(from, to int, srcFS vfs.FS, srcDir string, dstFS vfs.FS, dstDir string,
	names []string, cut bool) tea.Cmd {
	verb := "copy"
	if cut {
		verb = "move"
	}
	id, onProgress := m.startTransfer(fmt.Sprintf("%s → %s", verb, m.paneLabel(to)))
	var total int64
	if from >= 0 && from < len(m.Panes) {
		for _, entry := range m.Panes[from].Entries {
			for _, name := range names {
				if entry.Name == name {
					total += entry.Size
				}
			}
		}
	}
	m.openTransfersIfBig(len(names), total)
	request := xfer.Request{
		Src:      srcFS,
		SrcDir:   srcDir,
		Dst:      dstFS,
		DstDir:   dstDir,
		Names:    names,
		Policy:   xfer.PolicyUpdateIfNewer,
		Progress: onProgress,
		Workers:  m.cfg.Transfers.Workers,
	}
	ctx := m.connectContext()

	return func() tea.Msg {
		report := copyReport{id: id, cut: cut, from: from, to: to}
		plan, err := xfer.PlanCopy(ctx, request)
		if err != nil {
			report.err = err
			return report
		}
		result, err := xfer.Copy(ctx, request, plan)
		report.created, report.updated, report.skipped = result.Created, result.Updated, result.Skipped
		report.bytes, report.err = result.Bytes, err
		if cut && err == nil {
			for _, name := range names {
				_ = srcFS.RemoveAll(srcFS.Join(srcDir, name))
			}
		}
		return report
	}
}

// copyToPath copies the focused pane's targets to a typed destination.
func (m *Model) copyToPath(value string, cut bool) tea.Cmd {
	targets := m.targets()
	if len(targets) == 0 {
		m.quitHint = "Nothing under the cursor to copy"
		return nil
	}
	dstFS, dstDir, err := m.parseDestination(value)
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	names := make([]string, 0, len(targets))
	for _, entry := range targets {
		names = append(names, entry.Name)
	}
	return m.copyNames(m.Focused, m.Focused, m.Pane().FS, m.Pane().Path, dstFS, dstDir, names, cut)
}

// openCopyTo asks where the selection should go, with the other pane as the
// default so Enter is the common case.
func (m *Model) openCopyTo(cut bool) {
	if len(m.targets()) == 0 {
		m.quitHint = "Nothing under the cursor to copy"
		return
	}
	title, action := "Copy to", actions.ID("prompt.copy-to")
	if cut {
		title, action = "Move to", actions.ID("prompt.move-to")
	}
	dst := m.nextPane(m.Focused)
	m.prompt = promptState{
		Title:  title,
		Hint:   "Folder to copy into · local:/path or remote:/path",
		Action: action,
		open:   true,
	}
	if dst < len(m.Panes) && m.Panes[dst].FS != nil {
		m.prompt.input.Set(sidePrefix(&m.Panes[dst]) + m.Panes[dst].Path)
	}
	m.rebuildPathSuggestions()
	m.Overlay = OverlayPrompt
}

// fetchFromOther copies the cursor entry of the pane that is not focused into this
// one: the mirror of send, for when the thing you want is over there.
func (m *Model) fetchFromOther() tea.Cmd {
	other := m.nextPane(m.Focused)
	if other == m.Focused {
		m.quitHint = "There is only one pane"
		return nil
	}
	pane := &m.Panes[other]
	entry, ok := pane.CursorEntry()
	if !ok {
		m.quitHint = "Nothing under the cursor in the other pane"
		return nil
	}
	return m.copyEntries(other, m.Focused, []Entry{entry}, false)
}
