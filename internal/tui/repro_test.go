package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// After a copy and a reconnect, opening the palette must still produce a whole,
// exact frame. A frame that is one row short shifts every line below it, which is
// what "Ctrl+K broke the UI" looked like.
func TestPaletteAfterCopyAndReconnect(t *testing.T) {
	m := twoHosts(t)
	m.activeTab = 1
	if m.paneIndexFor("contabo") < 0 {
		t.Fatal("contabo should have a pane")
	}

	// Put a real file in the local pane to mark.
	localRoot := t.TempDir()
	mustWrite(t, localRoot+"/report.txt", "hello\n")
	m.Panes[0] = NewPane(m.Panes[0].FS, localRoot, localRoot)
	m.Panes[0].Load(false)
	m.Focused = 0
	m.selectByName("report.txt")
	press(t, m, run('y', 0, "y"))
	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "staging", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1
	cmd := m.pasteStaged()
	if cmd == nil {
		t.Fatalf("paste should start: %v", m.toasts)
	}
	if report, ok := cmd().(copyReport); ok {
		m.Update(report)
	}

	// Reconnect.
	m.disconnectHost("contabo")
	m.activeTab = 1
	m.switchTab(1)

	press(t, m, run('k', tea.ModCtrl, ""))
	if m.Overlay != OverlayPalette {
		t.Fatalf("Ctrl+K should open the palette, overlay is %v", m.Overlay)
	}
	for _, ch := range "copy" {
		press(t, m, run(ch, 0, string(ch)))
	}
	out := ansi.Strip(render(m))
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Errorf("frame is %d rows, want %d", len(lines), m.height)
	}
	for i, line := range lines {
		if len([]rune(line)) != m.width {
			t.Fatalf("line %d is %d cells, want %d: %q", i, len([]rune(line)), m.width, line)
		}
	}
}

// The grid frame is exact at every height, with and without the marked-files bar:
// the pane area is sometimes even, and the leftover row has to go somewhere.
func TestGridFrameIsExactWithAStagedBar(t *testing.T) {
	for height := 28; height <= 50; height++ {
		m := twoHosts(t)
		// Three panes: local and two hosts.
		m.Panes = m.Panes[:3]
		resize(t, m, 160, height)

		// A marked file turns the staged bar on, which takes a row from the panes.
		localRoot := t.TempDir()
		mustWrite(t, localRoot+"/report.txt", "hello\n")
		m.Panes[0] = NewPane(m.Panes[0].FS, localRoot, localRoot)
		m.Panes[0].Load(false)
		m.Focused = 0
		m.selectByName("report.txt")
		press(t, m, run('y', 0, "y"))

		lines := strings.Split(ansi.Strip(render(m)), "\n")
		if len(lines) != height {
			t.Fatalf("at %d rows the frame is %d lines", height, len(lines))
		}
		for i, line := range lines {
			if len([]rune(line)) != 160 {
				t.Fatalf("at %d rows line %d is %d cells: %q", height, i, len([]rune(line)), line)
			}
		}
	}
}
