package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A remote edit fetches the file, lets the editor change it, and sends it back.
// The test drives the same three messages the real editor round trip would.
func TestRemoteEditFetchesAndSendsBack(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path
	fsys := m.Pane().FS
	// Make the focused pane look remote while it reads a real filesystem.
	m.Panes[1] = Pane{
		Side: SideRemote, Host: "test", FS: fsys, Home: root, Path: root,
		Selected: map[string]bool{}, History: []string{root},
		Entries: m.Panes[0].Entries,
	}
	m.Focused = 1
	m.selectByName("notes.txt")

	cmd := m.editCursor()
	if cmd == nil {
		t.Fatal("editing a remote file should start a fetch")
	}
	msg, ok := cmd().(editReadyMsg)
	if !ok {
		t.Fatalf("the fetch should produce a ready file, got %T", msg)
	}
	defer func() { _ = os.RemoveAll(msg.dir) }()
	if got, _ := os.ReadFile(msg.tmp); !strings.HasPrefix(string(got), "one") {
		t.Errorf("the fetched copy should hold the file, got %q", got)
	}
	if filepath.Base(msg.tmp) != "notes.txt" {
		t.Errorf("the temp file should keep the name for syntax highlighting, got %q", msg.tmp)
	}

	// The editor writes, and the file goes back.
	if err := os.WriteFile(msg.tmp, []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	back := m.uploadEditedFile(editClosedMsg{ready: msg})
	if back == nil {
		t.Fatal("closing the editor should send the file back")
	}
	if _, ok := back().(editReloadMsg); !ok {
		t.Fatalf("the upload should ask for a refresh, got %T", back())
	}
	if got, _ := fsys.ReadFile(fsys.Join(root, "notes.txt")); string(got) != "edited\n" {
		t.Errorf("the edited bytes should be on the remote side, got %q", got)
	}
	if _, err := os.Stat(msg.dir); !os.IsNotExist(err) {
		t.Error("the temporary directory should be gone after the upload")
	}
}

// Permissions are octal because that is what the manuals say.
func TestChmodAcceptsOctal(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("notes.txt")
	m.chmodTargets("600")
	info, err := os.Stat(filepath.Join(m.Pane().Path, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the mode should be 600, got %o", info.Mode().Perm())
	}

	m.chmodTargets("not-a-mode")
	if len(m.toasts) == 0 {
		t.Error("a bad mode should say so")
	}
}

// A symlink points at the cursor entry and carries the name that was typed.
func TestSymlinkPointsAtTheCursor(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("notes.txt")
	m.makeSymlink("latest")
	target, err := os.Readlink(filepath.Join(m.Pane().Path, "latest"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "notes.txt" {
		t.Errorf("the link should point at notes.txt, got %q", target)
	}
}

// Sync browse walks both trees together: the pane that is not focused follows to
// the same path when it exists there.
func TestSyncBrowseFollowsTheOtherPane(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path
	fsys := m.Panes[0].FS
	m.Panes[1] = Pane{
		Side: SideRemote, Host: "test", FS: fsys, Home: root, Path: root,
		Selected: map[string]bool{}, History: []string{root},
	}
	m.syncBrowse = true
	m.Focused = 0

	cmd := m.navigate(fsys.Join(root, "assets"))
	if cmd == nil {
		t.Fatal("navigating should produce work")
	}
	// The batch holds the listing and the follow; running the follow directly is
	// what the message loop would do next.
	follow := m.followOtherPanes(fsys.Join(root, "assets"))
	if follow == nil {
		t.Fatal("the other pane should be asked to follow")
	}
	msg, ok := follow().(syncPaneMsg)
	if !ok {
		t.Fatalf("the follow should produce a sync message, got %T", msg)
	}
	m.followInPane(msg)
	if m.Panes[1].Path != fsys.Join(root, "assets") {
		t.Errorf("the other pane should be at assets, it is at %q", m.Panes[1].Path)
	}

	// With sync browse off, nothing follows.
	m.syncBrowse = false
	if follow := m.followOtherPanes(fsys.Join(root, "assets")); follow != nil {
		t.Error("sync browse off should follow nothing")
	}
}

// Editing a folder says so instead of shelling out.
func TestEditRefusesFolders(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("assets")
	if cmd := m.editCursor(); cmd != nil {
		t.Error("a folder should not be opened in an editor")
	}
	if len(m.toasts) == 0 {
		t.Error("refusing should say why")
	}
}
