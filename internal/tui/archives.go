package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/archive"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Archives cross sides the same way files do, so both the archive and its
// destination name a side. The prompt accepts "local:/path" and "remote:/path",
// which is short enough to type and says exactly which machine is meant.

// extractDoneMsg reports a finished extraction.
type extractDoneMsg struct {
	count   int
	dst     string
	archive string
	err     error
}

// compressDoneMsg reports a finished archive.
type compressDoneMsg struct {
	count int
	dst   string
	err   error
}

// paneOnSide finds the pane for a side name.
func (m *Model) paneOnSide(side string) (*Pane, bool) {
	for i := range m.Panes {
		pane := &m.Panes[i]
		switch side {
		case "local":
			if pane.Side == SideLocal {
				return pane, true
			}
		case "remote":
			if pane.Side == SideRemote {
				return pane, true
			}
		}
	}
	return nil, false
}

// sidePrefix names the side of a pane, for prompting.
func sidePrefix(pane *Pane) string {
	if pane.Side == SideLocal {
		return "local:"
	}
	return "remote:"
}

// parseDestination reads a prompt value into a filesystem and a directory. A
// value without a side prefix stays on the focused pane, so "backups" means what
// it looks like.
func (m *Model) parseDestination(value string) (vfs.FS, string, error) {
	value = strings.TrimSpace(value)
	side, rest, hasSide := strings.Cut(value, ":")
	if !hasSide {
		pane := m.Pane()
		if pane.FS == nil {
			return nil, "", errNotConnected
		}
		return pane.FS, pane.FS.Join(pane.Path, value), nil
	}
	pane, ok := m.paneOnSide(strings.ToLower(side))
	if !ok {
		return nil, "", errUnknownSide(side)
	}
	if pane.FS == nil {
		return nil, "", errNotConnected
	}
	return pane.FS, pane.FS.Join(rest), nil
}

type destError string

func (e destError) Error() string { return string(e) }

const errNotConnected = destError("that side is not connected")

func errUnknownSide(side string) error {
	return destError("unknown side " + side + " · use local: or remote:")
}

// openExtractPrompt asks where an archive should go. The default is the other
// pane, because that is the interesting case: a tarball on the server, unpacked
// into the project on this machine.
func (m *Model) openExtractPrompt() {
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok || !IsArchive(entry.Name) {
		m.quitHint = "Put the cursor on an archive first"
		return
	}
	other := 1 - m.Focused
	dst := pane
	if other >= 0 && other < len(m.Panes) && m.Panes[other].FS != nil {
		dst = &m.Panes[other]
	}
	hint := "Folder to unpack into · local:/path or remote:/path"
	m.prompt = promptState{
		Title:  "Extract " + entry.Name,
		Hint:   hint,
		Action: "prompt.extract",
		open:   true,
	}
	m.prompt.input.Set(sidePrefix(dst) + dst.Path)
	m.Overlay = OverlayPrompt
}

// extractCursor unpacks the cursor archive into the chosen destination.
func (m *Model) extractCursor(value string) tea.Cmd {
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok || !IsArchive(entry.Name) {
		m.quitHint = "Put the cursor on an archive first"
		return nil
	}
	dstFS, dstDir, err := m.parseDestination(value)
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	srcFS := pane.FS
	srcPath := srcFS.Join(pane.Path, entry.Name)
	name := entry.Name
	ctx := m.connectContext()
	m.mode = "EXTRACTING"

	return func() tea.Msg {
		count, err := archive.Extract(ctx, srcFS, srcPath, dstFS, dstDir, nil)
		return extractDoneMsg{count: count, dst: dstDir, archive: name, err: err}
	}
}

// openCompressPrompt names the archive to write. The default is the focused pane
// and a sensible name, so Enter is enough for the common case.
func (m *Model) openCompressPrompt() {
	targets := m.targets()
	if len(targets) == 0 {
		m.quitHint = "Nothing selected to compress"
		return
	}
	names := make([]string, 0, len(targets))
	for _, entry := range targets {
		names = append(names, entry.Name)
	}
	defaultName := archive.DefaultName(names)
	m.prompt = promptState{
		Title:  "Compress " + plural(len(names), "item", "items"),
		Hint:   "Archive name · local:/path or remote:/path",
		Action: "prompt.compress",
		open:   true,
	}
	m.prompt.input.Set(sidePrefix(m.Pane()) + m.Pane().FS.Join(m.Pane().Path, defaultName))
	m.Overlay = OverlayPrompt
}

// compressTargets writes the selection into a new archive.
func (m *Model) compressTargets(value string) tea.Cmd {
	pane := m.Pane()
	targets := m.targets()
	if len(targets) == 0 || pane.FS == nil {
		m.quitHint = "Nothing selected to compress"
		return nil
	}
	dstFS, dstPath, err := m.parseDestination(value)
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	if archive.Detect(dstPath) == archive.Unknown {
		m.toastError("Name the format: .tar.gz, .tar, or .zip")
		return nil
	}
	items := make([]archive.Item, 0, len(targets))
	for _, entry := range targets {
		items = append(items, archive.Item{FS: pane.FS, Dir: pane.Path, Name: entry.Name})
	}
	ctx := m.connectContext()
	m.mode = "COMPRESSING"

	return func() tea.Msg {
		count, err := archive.Compress(ctx, dstFS, dstPath, items, nil)
		return compressDoneMsg{count: count, dst: dstPath, err: err}
	}
}
