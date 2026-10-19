package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Editing a remote file is download-edit-upload: a person keeps their own editor,
// and the round trip is invisible unless it fails.
//
// The size limit is the point where "just download it" stops being honest: an
// editor holds the whole file in memory and writes it back whole, so a 2 GB log
// would be copied twice and edited in RAM. Refusing is friendlier than freezing.
const maxEditSize = 16 << 20

// editReadyMsg carries a remote file that has been fetched and is ready to open.
type editReadyMsg struct {
	dir    string // temp directory to remove afterwards
	tmp    string // the copy the editor will open
	remote bool
	fsys   vfs.FS // nil for a local file
	path   string // the file's path on fsys
	dirIdx int    // the pane that owns it, for the refresh
}

// editClosedMsg is delivered when the editor exits.
type editClosedMsg struct {
	ready editReadyMsg
	err   error
}

// editFailedMsg says the round trip failed before or after the editor ran.
type editFailedMsg struct{ err error }

// editReloadMsg asks for a refresh once a local edit is finished.
type editReloadMsg struct{ dirIdx int }

// editCursor opens the cursor entry in $EDITOR. A local file is opened in place;
// a remote one is fetched to a temporary file and sent back if the editor
// succeeds.
func (m *Model) editCursor() tea.Cmd {
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok {
		return nil
	}
	if entry.Dir {
		m.toastWarning(entry.Name + " is a folder")
		return nil
	}
	if pane.FS == nil {
		return nil
	}
	path := pane.FS.Join(pane.Path, entry.Name)
	index := m.Focused

	if pane.Side == SideLocal {
		cmd := exec.CommandContext(context.Background(), editorCommand(), path) //nolint:gosec // $EDITOR is the user's own choice
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return editFailedMsg{err: err}
			}
			return editReloadMsg{dirIdx: index}
		})
	}

	if entry.Size > maxEditSize {
		m.toastWarning(fmt.Sprintf("%s is %s · too large to edit safely", entry.Name, humanSize(entry.Size)))
		return nil
	}
	fsys := pane.FS
	m.mode = "EDITING"

	return func() tea.Msg {
		data, err := fsys.ReadFile(path)
		if err != nil {
			return editFailedMsg{err: err}
		}
		dir, err := os.MkdirTemp("", "omssh-edit-")
		if err != nil {
			return editFailedMsg{err: err}
		}
		// Keep the name, and with it the extension: the editor's syntax
		// highlighting is decided by the file name, not by the contents.
		tmp := filepath.Join(dir, sanitizeName(entry.Name))
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return editFailedMsg{err: err}
		}
		return editReadyMsg{dir: dir, tmp: tmp, remote: true, fsys: fsys, path: path, dirIdx: index}
	}
}

// sanitizeName keeps a file name usable on the local filesystem: the remote name
// is only a label, and a path separator in it would write outside the temp dir.
func sanitizeName(name string) string {
	clean := filepath.Base(filepath.Clean("/" + name))
	if clean == "" || clean == "/" || clean == "." {
		return "remote-file"
	}
	return clean
}

// openRemoteEditor starts the editor on a fetched copy.
func (m *Model) openRemoteEditor(msg editReadyMsg) tea.Cmd {
	cmd := exec.CommandContext(context.Background(), editorCommand(), msg.tmp) //nolint:gosec // $EDITOR is the user's own choice
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editClosedMsg{ready: msg, err: err}
	})
}

// uploadEditedFile sends the edited copy back and forgets the temporary file.
func (m *Model) uploadEditedFile(msg editClosedMsg) tea.Cmd {
	ready := msg.ready
	if msg.err != nil {
		_ = os.RemoveAll(ready.dir)
		m.mode = "NORMAL"
		m.toastError("Editor failed: " + msg.err.Error())
		return nil
	}
	data, err := os.ReadFile(ready.tmp)
	if err != nil {
		_ = os.RemoveAll(ready.dir)
		m.mode = "NORMAL"
		m.toastError("Cannot read the edited copy: " + err.Error())
		return nil
	}
	m.mode = "SAVING"

	return func() tea.Msg {
		defer func() { _ = os.RemoveAll(ready.dir) }()
		file, err := ready.fsys.OpenFile(ready.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return editFailedMsg{err: err}
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return editFailedMsg{err: err}
		}
		if err := file.Close(); err != nil {
			return editFailedMsg{err: err}
		}
		return editReloadMsg{dirIdx: ready.dirIdx}
	}
}
