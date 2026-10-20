package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// fourPanes builds the dashboard with the local pane and three hosts.
func fourPanes(t *testing.T) (*Model, map[string]string) {
	t.Helper()
	m, _ := newConnectModel(t, session.PrompterFunc{})
	dirs := map[string]string{}
	for i, alias := range []string{"one", "two", "three"} {
		dir := t.TempDir()
		dirs[alias] = dir
		mustWrite(t, dir+"/"+alias+".txt", alias)
		fsys, err := vfs.NewLocal(dir)
		if err != nil {
			t.Fatal(err)
		}
		m.addSessionTab(&session.Session{
			ID: alias, Target: omssh.Target{Alias: alias}, FS: fsys, Root: "/", Base: dir,
		})
		_ = i
	}
	// Bind every session to a pane, as connecting would.
	for _, alias := range []string{"one", "two", "three"} {
		m.paneForSession(m.live[alias])
	}
	m.screen = screenFiles
	resize(t, m, 160, 44)
	return m, dirs
}

// Four sessions are four panes, two by two, and the screen is still exactly the
// terminal: a grid that overflows by a row would break the frame.
func TestFourPanesFillTheScreen(t *testing.T) {
	m, _ := fourPanes(t)
	if m.paneCount() != 4 {
		t.Fatalf("expected four panes, got %d", m.paneCount())
	}
	l := m.measure()
	if !l.Grid {
		t.Fatal("four panes should use the grid layout")
	}

	lines := strings.Split(ansi.Strip(render(m)), "\n")
	if len(lines) != 44 {
		t.Errorf("the screen should be 44 rows, got %d", len(lines))
	}
	for i, line := range lines {
		if got := len([]rune(line)); got != 160 {
			t.Fatalf("line %d is %d cells, want 160: %q", i, got, line)
		}
	}
	// Each host's name is on screen at once: that is the point of four panes.
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"one", "two", "three", "LOCAL"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the grid should show %q:\n%s", want, joined)
		}
	}
}

// Focus walks the grid with w and wraps.
func TestFocusWalksTheGrid(t *testing.T) {
	m, _ := fourPanes(t)
	m.Focused = 0
	seen := map[int]bool{}
	for i := 0; i < 4; i++ {
		seen[m.Focused] = true
		press(t, m, run('w', 0, "w"))
	}
	if m.Focused != 0 {
		t.Errorf("w four times should come back to the first pane, got %d", m.Focused)
	}
	if len(seen) != 4 {
		t.Errorf("w should visit every pane, visited %v", seen)
	}
}

// Paste goes where the keys are: mark in one pane, move to another, press p.
func TestPasteIntoTheFocusedPane(t *testing.T) {
	m, dirs := fourPanes(t)
	localRoot := t.TempDir()
	// Point the local pane at a real directory with one file to mark.
	m.Panes[0] = NewPane(m.Panes[0].FS, localRoot, localRoot)
	m.Panes[0].Load(false)
	m.selectByName("") // no-op, keeps the helper honest
	m.Panes[0].Entries = []Entry{{Name: "report.txt", Size: 4}}
	m.Panes[0].Path = localRoot
	mustWrite(t, localRoot+"/report.txt", "data")

	m.Focused = 0
	m.selectByName("report.txt")
	press(t, m, run('y', 0, "y"))

	// Focus the last pane, which is "three".
	for i := 0; i < 3; i++ {
		press(t, m, run('w', 0, "w"))
	}
	if host := m.Panes[m.Focused].Host; host != "three" {
		t.Fatalf("expected to be in the three pane, in %q", host)
	}
	cmd := m.pasteStaged()
	if cmd == nil {
		t.Fatalf("paste should start: %v", m.toasts)
	}
	cmd()
	if _, err := m.Panes[m.Focused].FS.Stat("/report.txt"); err != nil {
		t.Errorf("the file should be in the focused pane's directory: %v", err)
	}
	if _, err := m.Panes[1].FS.Stat("/report.txt"); err == nil {
		t.Error("paste must not go to the pane that happens to be first")
	}
	_ = dirs
}

// More sessions than panes leaves the extras as tabs, and V chooses which four
// are on screen.
func TestPaneChooserTakesFourOfMany(t *testing.T) {
	m, _ := fourPanes(t)
	// A fifth session, with no pane to live in.
	dir := t.TempDir()
	fsys, err := vfs.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.addSessionTab(&session.Session{ID: "four", Target: omssh.Target{Alias: "four"}, FS: fsys, Root: "/", Base: dir})

	m.openPanes()
	if m.Overlay != OverlayPanes {
		t.Fatal("V should open the pane chooser")
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"local machine", "one", "four", "4 of 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("the chooser should list %q:\n%s", want, out)
		}
	}

	// Untick one and tick the newcomer: still four panes, now with "four".
	m.panes.chosen["one"] = false
	m.panes.chosen["four"] = true
	m.handlePanesKey(keyNamed(tea.KeyEnter))
	if m.paneCount() != 4 {
		t.Errorf("the dashboard should still have four panes, has %d", m.paneCount())
	}
	hosts := map[string]bool{}
	for i := 0; i < m.paneCount(); i++ {
		hosts[m.Panes[i].Host] = true
	}
	if hosts["one"] {
		t.Error("one was unticked and should be off screen")
	}
	if !hosts["four"] {
		t.Error("four was ticked and should be on screen")
	}
}

// Ticking a fifth pane is refused with a reason, not silently dropped.
func TestPaneChooserStopsAtFour(t *testing.T) {
	m, _ := fourPanes(t)
	dir := t.TempDir()
	fsys, err := vfs.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.addSessionTab(&session.Session{ID: "four", Target: omssh.Target{Alias: "four"}, FS: fsys, Root: "/", Base: dir})
	m.openPanes()
	m.panes.chosen = map[string]bool{"": true, "one": true, "two": true, "three": true}

	// Put the cursor on "four" and try to tick it.
	for i, choice := range m.paneChoices() {
		if choice == "four" {
			m.panes.cursor = i
		}
	}
	m.handlePanesKey(run(' ', 0, " "))
	if m.panes.chosen["four"] {
		t.Error("a fifth pane must not be ticked")
	}
	if len(m.toasts) == 0 {
		t.Error("refusing should say why")
	}
}

// More sessions than panes asks which four to show, once, before the dashboard
// gets busy: silently choosing for someone is how a screen becomes a mystery.
func TestDashboardAsksWhenThereAreTooManySessions(t *testing.T) {
	m, _ := fourPanes(t)
	dir := t.TempDir()
	fsys, err := vfs.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.addSessionTab(&session.Session{ID: "four", Target: omssh.Target{Alias: "four"}, FS: fsys, Root: "/", Base: dir})
	m.screen = screenHome
	m.panesAsked = false

	m.openFiles()
	if m.Overlay != OverlayPanes {
		t.Fatalf("five sessions should open the chooser, overlay is %v", m.Overlay)
	}

	// Answering it settles the question; coming back does not ask again.
	m.applyPanes()
	if m.Overlay != OverlayNone {
		t.Error("applying should close the chooser")
	}
	m.openFiles()
	if m.Overlay == OverlayPanes {
		t.Error("the question should be asked once, not every time")
	}
}
