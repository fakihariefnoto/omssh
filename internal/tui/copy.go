package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/archive"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/xfer"
)

// Marking files for copying is a two-step gesture: mark them, review them, then
// say where they go. The review step exists because a copy between machines is
// the kind of thing a person wants to look at once before it happens.

// stage adds the current targets to the marked set. Marking is cumulative: mark
// a file, walk to the next, mark that too, and the set grows. A cursor entry that
// is already marked is unmarked, so y both adds and removes and one key covers
// both gestures.
func (m *Model) stage(cut bool) {
	entries := m.targets()
	if len(entries) == 0 {
		m.quitHint = "Nothing selected · Space selects · Ctrl+Q quits"
		return
	}

	// A marked set belongs to the pane it came from: a copy has one source. Moving
	// to another pane and marking there starts a new set rather than mixing two.
	if m.stagedCount() > 0 && m.staged.From != m.Focused {
		m.clearStaged()
	}
	if m.stagedCount() == 0 {
		m.staged = stagedState{From: m.Focused, Dir: m.Pane().Path, Policy: int(xfer.PolicyUpdateIfNewer)}
	}

	// With a selection, y marks all of it. On a bare cursor it toggles, so the
	// same key takes a mark back — unless the other gesture was asked for, in
	// which case y then x changes a mark from copy to move rather than removing
	// it.
	marked := make(map[string]bool, len(m.staged.Paths))
	for _, name := range m.staged.Paths {
		marked[name] = true
	}
	selected := m.Pane().SelectedCount() > 0
	sameKind := m.staged.Cut == cut
	for _, entry := range entries {
		if marked[entry.Name] {
			if selected || !sameKind {
				continue
			}
			delete(marked, entry.Name)
			continue
		}
		marked[entry.Name] = true
	}
	if len(marked) == 0 {
		m.clearStaged()
		m.toastInfo("Nothing marked now")
		return
	}

	// Keep the order the pane shows, so the card and the bar read like the listing.
	paths := make([]string, 0, len(marked))
	for _, entry := range m.Panes[m.staged.From].Entries {
		if marked[entry.Name] {
			paths = append(paths, entry.Name)
		}
	}
	m.staged.Paths = paths
	m.staged.Cut = cut
	m.staged.Sizes = map[string]int64{}
	for _, entry := range m.Panes[m.staged.From].Entries {
		m.staged.Sizes[entry.Name] = entry.Size
	}

	verb := "copied"
	if cut {
		verb = "moved"
	}
	// The review screen is offered rather than opened: marking is a step, not a
	// decision, and taking over the screen mid-selection would interrupt it.
	m.toastInfo(fmt.Sprintf("%d %s marked to be %s · s reviews it",
		len(paths), plural(len(paths), "item", "items"), verb))
}

// clearStaged forgets the selection.
func (m *Model) clearStaged() { m.staged = stagedState{} }

// stagedCount is how many files are marked.
func (m *Model) stagedCount() int { return len(m.staged.Paths) }

// stagedRoute names the two ends of the pending operation, for the review
// screen.
func (m *Model) stagedRoute() (from, to string) {
	if m.staged.From < 0 || m.staged.From >= len(m.Panes) {
		return "(none)", "(none)"
	}
	dstIndex := m.nextPane(m.staged.From)
	src := m.Panes[m.staged.From]
	from = m.paneDescription(m.staged.From) + "  " + src.Path
	if dstIndex < 0 || dstIndex >= len(m.Panes) {
		return from, "(none)"
	}
	return from, m.paneDescription(dstIndex) + "  " + m.Panes[dstIndex].Path
}

// paneDescription names a pane: "local" or its host.
func (m *Model) paneDescription(index int) string {
	if index < 0 || index >= len(m.Panes) {
		return "?"
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

// stagedEntry is one marked file with the row it came from.
type stagedEntry struct {
	Name string
	Size int64
}

// stagedEntries is the marked set, in the order it was marked, with sizes from
// the source pane's current listing where it still has them.
func (m *Model) stagedEntries() []stagedEntry {
	out := make([]stagedEntry, 0, len(m.staged.Paths))
	for _, name := range m.staged.Paths {
		out = append(out, stagedEntry{Name: name, Size: m.staged.Sizes[name]})
	}
	return out
}

// stagedBytes totals the marked set.
func (m *Model) stagedBytes() int64 {
	var total int64
	for _, entry := range m.stagedEntries() {
		total += entry.Size
	}
	return total
}

// copyReport is the result of a finished copy, for the toast and for refreshing
// the panes it touched.
type copyReport struct {
	// id ties the report to the progress it replaces in the sidebar.
	id                        int
	created, updated, skipped int
	bytes                     int64
	cut                       bool
	from, to                  int
	// extracted is the directory an archive was unpacked into, when the paste
	// asked for it.
	extracted string
	// packed is the archive a pack-paste wrote instead of copying the files.
	packed string
	err    error
}

// pasteStaged copies the marked files to the other pane.
func (m *Model) pasteStaged() tea.Cmd {
	if len(m.staged.Paths) == 0 {
		m.quitHint = "Nothing marked · Ctrl+C marks a selection"
		return nil
	}
	from := m.staged.From
	// Paste where the keys are: mark in one pane, move to another, press p. If the
	// focus never left the source, use the next pane so p still means something.
	to := m.Focused
	if to == from {
		to = m.nextPane(from)
	}
	if from < 0 || from >= len(m.Panes) || to < 0 || to >= len(m.Panes) {
		m.toastError("There is no other pane to paste into")
		return nil
	}
	src, dst := m.Panes[from], m.Panes[to]
	if src.FS == nil || dst.FS == nil {
		// Saying which side is empty is more useful than "cannot copy".
		empty := "the source pane"
		if src.FS != nil {
			empty = "the destination pane"
		}
		m.toastError("Cannot copy: " + empty + " is not connected or has no directory")
		return nil
	}

	srcDir := m.staged.Dir
	if srcDir == "" {
		srcDir = src.Path
	}
	policy := xfer.ConflictPolicy(m.staged.Policy)
	if m.staged.Policy == 0 {
		// Zero is PolicyAsk, and a paste with no one to ask must not silently
		// skip: update-if-newer is the sensible default and needs no prompt.
		policy = xfer.PolicyUpdateIfNewer
	}
	request := xfer.Request{
		Src:     src.FS,
		SrcDir:  srcDir,
		Dst:     dst.FS,
		DstDir:  dst.Path,
		Names:   append([]string{}, m.staged.Paths...),
		Policy:  policy,
		Workers: m.cfg.Transfers.Workers,
	}
	cut := m.staged.Cut
	after := m.staged.After
	packName := m.packName()

	verb := "copy"
	if cut {
		verb = "move"
	}
	id, onProgress := m.startTransfer(fmt.Sprintf("%s → %s", verb, m.paneLabel(to)))
	request.Progress = onProgress
	m.openTransfersIfBig(len(request.Names), m.stagedBytes())
	m.mode = "COPYING"
	if after == pastePack {
		m.mode = "ARCHIVING"
	}
	ctx := m.connectContext()

	return func() tea.Msg {
		report := copyReport{id: id, cut: cut, from: from, to: to}

		// Pack instead of copying: the destination gets one archive, not the files.
		// "Copy this folder over there, but as a tarball" is one gesture.
		if after == pastePack {
			items := make([]archive.Item, 0, len(request.Names))
			for _, name := range request.Names {
				items = append(items, archive.Item{FS: src.FS, Dir: srcDir, Name: name})
			}
			target := dst.FS.Join(dst.Path, packName)
			seen := 0
			count, err := archive.Compress(ctx, dst.FS, target, items, func(name string) {
				seen++
				onProgress(int64(seen), 0, name)
			})
			report.created, report.packed, report.err = count, target, err
			if err == nil {
				if info, statErr := dst.FS.Stat(target); statErr == nil {
					report.bytes = info.Size()
				}
			}
			if cut && err == nil {
				for _, name := range request.Names {
					_ = src.FS.RemoveAll(src.FS.Join(srcDir, name))
				}
			}
			return report
		}

		plan, err := xfer.PlanCopy(ctx, request)
		if err != nil {
			report.err = err
			return report
		}
		result, err := xfer.Copy(ctx, request, plan)
		report.created, report.updated, report.skipped = result.Created, result.Updated, result.Skipped
		report.bytes, report.err = result.Bytes, err
		if cut && err == nil {
			// A move deletes only what arrived: the per-item result is the
			// authority, and a partial failure leaves the sources alone.
			for _, name := range request.Names {
				_ = src.FS.RemoveAll(src.FS.Join(srcDir, name))
			}
		}
		if after == pasteExtract && err == nil && len(request.Names) == 1 {
			// "Copy this tarball and unpack it over there" is one gesture, not two.
			name := request.Names[0]
			target := dst.FS.Join(dst.Path, archive.Stem(name))
			if _, extractErr := archive.Extract(ctx, dst.FS, dst.FS.Join(dst.Path, name), dst.FS, target, nil); extractErr != nil {
				report.err = extractErr
			} else {
				report.extracted = target
			}
		}
		return report
	}
}

// handleCopyReport turns a finished copy into a message and refreshes what
// changed.
func (m *Model) handleCopyReport(report copyReport) tea.Cmd {
	m.mode = "NORMAL"
	took := m.finishTransfer(report.id)

	// Both ends may have changed: a move removes from the source. The reloads go
	// through the same path as every other listing, so a slow link cannot stall
	// the update loop right after a transfer finishes.
	cmds := make([]tea.Cmd, 0, 2)
	for _, index := range []int{report.from, report.to} {
		if index >= 0 && index < len(m.Panes) {
			if cmd := m.loadPaneAsync(index); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	refresh := tea.Batch(cmds...)

	if report.err != nil {
		m.toastError("Copy: " + report.err.Error())
		return refresh
	}

	verb := "Copied"
	if report.cut {
		verb = "Moved"
		// A move takes the marks with it: the sources are gone.
		m.clearStaged()
	}
	parts := make([]string, 0, 3)
	if report.created > 0 {
		parts = append(parts, fmt.Sprintf("%d created", report.created))
	}
	if report.updated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", report.updated))
	}
	if report.skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", report.skipped))
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing to do")
	}
	message := fmt.Sprintf("%s %s (%s)", verb, strings.Join(parts, ", "), humanSize(report.bytes))
	if report.extracted != "" {
		message += " · unpacked into " + report.extracted
	}
	if report.packed != "" {
		message = fmt.Sprintf("%s %d %s into %s (%s)", verb,
			report.created, plural(report.created, "entry", "entries"), report.packed, humanSize(report.bytes))
	}
	if took > 50*time.Millisecond {
		message += " · " + took.Round(100*time.Millisecond).String()
	}
	if !report.cut {
		// The marks stay: copying the same set to a second place is normal, and
		// re-marking it by hand every time is not.
		message += " · marks kept"
	}
	m.toastSuccess(message)
	return refresh
}
