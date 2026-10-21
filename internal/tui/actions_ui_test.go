package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// destinationPane points the second pane at a fresh directory.
func destinationPane(t *testing.T, m *Model) string {
	t.Helper()
	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	return dest
}

// C copies the cursor entry to a chosen place, not only to the next pane.
func TestCopyToAChosenFolder(t *testing.T) {
	m := newTestModel(t)
	dest := destinationPane(t, m)
	m.selectByName("README.md")
	m.Focused = 0

	press(t, m, run('C', 0, "C"))
	if m.Overlay != OverlayPrompt || m.prompt.Action != "prompt.copy-to" {
		t.Fatalf("C should ask where, overlay=%v action=%q", m.Overlay, m.prompt.Action)
	}
	if !strings.Contains(m.prompt.input.Value, "remote:") {
		t.Errorf("the prompt should default to the other pane, got %q", m.prompt.input.Value)
	}

	m.overlayCmd = m.applyPrompt("prompt.copy-to", "local:"+filepath.Join(dest, "sub"))
	// The folder does not exist yet: copy into the parent, which does.
	m.overlayCmd = m.applyPrompt("prompt.copy-to", "local:"+dest)
	if m.overlayCmd == nil {
		t.Fatalf("copy should start: %v", m.toasts)
	}
	if report, ok := m.overlayCmd().(copyReport); !ok || report.err != nil {
		t.Fatalf("copy failed: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("the file should be in the chosen folder: %v", err)
	}
	// A copy keeps the marks, as p does.
	if m.stagedCount() != 0 {
		t.Errorf("no marks were made, got %v", m.staged.Paths)
	}
}

// M moves instead of copying.
func TestMoveToAChosenFolder(t *testing.T) {
	m := newTestModel(t)
	dest := destinationPane(t, m)
	root := m.Pane().Path
	m.selectByName("notes.txt")
	m.Focused = 0

	press(t, m, run('M', 0, "M"))
	if m.prompt.Action != "prompt.move-to" {
		t.Fatalf("M should ask where to move, action=%q", m.prompt.Action)
	}
	cmd := m.applyPrompt("prompt.move-to", "local:"+dest)
	if cmd == nil {
		t.Fatalf("move should start: %v", m.toasts)
	}
	m.handleCopyReport(cmd().(copyReport))
	if _, err := os.Stat(filepath.Join(dest, "notes.txt")); err != nil {
		t.Errorf("the file should be in the destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Error("a move should remove the source")
	}
}

// Send and fetch are the two directions of the same copy, without marking first.
func TestSendAndFetchBetweenPanes(t *testing.T) {
	m := newTestModel(t)
	dest := destinationPane(t, m)
	m.selectByName("README.md")
	m.Focused = 0

	cmd := m.runAction("nav.send")
	if cmd == nil {
		t.Fatalf("send should copy: %v", m.toasts)
	}
	m.handleCopyReport(cmd().(copyReport))
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("send should copy the file over: %v", err)
	}

	// Fetch takes the other pane's cursor entry back.
	mustWrite(t, filepath.Join(dest, "from-remote.txt"), "remote\n")
	m.Panes[1].Load(false)
	m.Focused = 1
	m.selectByName("from-remote.txt")
	m.Focused = 0
	cmd = m.runAction("nav.fetch")
	if cmd == nil {
		t.Fatalf("fetch should copy: %v", m.toasts)
	}
	m.handleCopyReport(cmd().(copyReport))
	if _, err := os.Stat(filepath.Join(m.Pane().Path, "from-remote.txt")); err != nil {
		t.Errorf("fetch should bring the file back: %v", err)
	}
}

// Find in subtree searches below the cursor and shows the match.
func TestFindBelowJumpsToTheMatch(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path
	mustWrite(t, filepath.Join(root, "assets", "deep", "needle.txt"), "found\n")
	m.Pane().Load(false)
	m.selectByName("assets")

	cmd := m.runAction("nav.find")
	_ = cmd
	if m.Overlay != OverlayPrompt || m.prompt.Action != "prompt.find" {
		t.Fatalf("find should ask for a name, overlay=%v action=%q", m.Overlay, m.prompt.Action)
	}
	search := m.applyPrompt("prompt.find", "needle")
	if search == nil {
		t.Fatal("search should start")
	}
	result, ok := search().(findResultMsg)
	if !ok || result.err != nil {
		t.Fatalf("search failed: %+v", result)
	}
	m.showFindResult(result)
	if m.Pane().Path != filepath.Join(root, "assets", "deep") {
		t.Errorf("the pane should show the match's folder, got %q", m.Pane().Path)
	}
	if entry, ok := m.Pane().CursorEntry(); !ok || entry.Name != "needle.txt" {
		t.Errorf("the match should be under the cursor, got %+v", entry)
	}

	// A miss says so instead of moving.
	m.mode = "NORMAL"
	m.showFindResult(findResultMsg{err: errNoMatch})
	if out := ansi.Strip(render(m)); !strings.Contains(out, "No match") {
		t.Errorf("a miss should be reported:\n%s", out)
	}
	_ = tea.KeyEnter
}
