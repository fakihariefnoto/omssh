package tui

import (
	"io/fs"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// countingFS counts the reads a preview causes.
type countingFS struct {
	vfs.FS
	opens int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.opens++
	return c.FS.Open(name)
}

// Rendering must not read a remote file on every frame: that is what made typing
// in the palette blank and blink on a real connection.
func TestPreviewIsReadOncePerChange(t *testing.T) {
	m := newTestModel(t)
	resize(t, m, 160, 44)
	root := m.Pane().Path
	counted := &countingFS{FS: m.Pane().FS}
	m.Panes[1] = Pane{
		Side: SideRemote, Host: "contabo", FS: counted, Home: root, Path: root,
		Selected: map[string]bool{}, History: []string{root},
		Entries: m.Panes[0].Entries,
	}
	m.Focused = 1
	m.preview = true
	m.selectByName("notes.txt")

	render(m)
	after := counted.opens
	if after == 0 {
		t.Fatal("the first frame should read the file once")
	}

	// Ten more frames, including opening the palette and typing in it.
	render(m)
	render(m)
	press(t, m, run('k', tea.ModCtrl, ""))
	for _, ch := range "copy" {
		press(t, m, run(ch, 0, string(ch)))
		render(m)
	}
	if counted.opens != after {
		t.Errorf("the preview should not read again for frames that changed nothing: %d reads became %d",
			after, counted.opens)
	}

	// A different file is a different question, and is read.
	m.Overlay = OverlayNone
	m.selectByName("README.md")
	render(m)
	if counted.opens != after+1 {
		t.Errorf("changing the cursor entry should read once, reads went from %d to %d", after, counted.opens)
	}
}

// The palette over a remote pane still draws a whole, exact screen.
func TestPaletteOverARemotePaneDrawsAFullScreen(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path
	m.Panes[1] = Pane{
		Side: SideRemote, Host: "contabo", FS: m.Panes[0].FS, Home: root, Path: root,
		Selected: map[string]bool{}, History: []string{root},
		Entries: m.Panes[0].Entries,
	}
	m.Focused = 1
	resize(t, m, 140, 40)

	press(t, m, run('k', tea.ModCtrl, ""))
	out := ansi.Strip(render(m))
	lines := strings.Split(out, "\n")
	if len(lines) != 40 {
		t.Errorf("the palette frame should be 40 rows, got %d", len(lines))
	}
	for i, line := range lines {
		if len([]rune(line)) != 140 {
			t.Fatalf("line %d is %d cells, want 140", i, len([]rune(line)))
		}
	}
	if !strings.Contains(out, "Actions") {
		t.Errorf("the palette should be drawn:\n%s", out)
	}
}
