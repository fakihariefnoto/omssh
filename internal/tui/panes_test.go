package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/archive"
	"github.com/fakihariefnoto/omssh/internal/config"
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

	m.openDashboard()
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

// Disconnecting ends the connection but keeps the tab: the place in the
// interface stays, and choosing it reconnects.
func TestDisconnectKeepsTheTab(t *testing.T) {
	m := twoHosts(t)
	m.activeTab = 1 // contabo
	m.Focused = m.paneIndexFor("contabo")
	if m.Focused < 0 {
		t.Fatal("contabo should have a pane")
	}

	press(t, m, run('Q', 0, "Q"))
	if _, live := m.live["contabo"]; live {
		t.Error("the session should be gone")
	}
	if len(m.tabs) < 2 || m.tabs[1].Host != "contabo" {
		t.Error("the tab should stay")
	}
	if m.tabs[1].State != StateIdle {
		t.Errorf("the tab should say idle, got %v", m.tabs[1].State)
	}
	if m.paneIndexFor("contabo") >= 0 {
		t.Error("no pane should still point at the closed connection")
	}

	// Choosing the tab asks for the connection back instead of dead-ending.
	m.activeTab = 1
	cmd := m.switchTab(1)
	if cmd == nil {
		t.Fatalf("choosing a disconnected tab should reconnect: %v", m.toasts)
	}
	if m.tabs[1].State != StateConnecting {
		t.Errorf("the tab should say connecting, got %v", m.tabs[1].State)
	}
}

// Marked files are state, and state lives in the sidebar: names, sizes and the
// destination, not just a count.
func TestSidebarListsTheMarkedFiles(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("README.md")
	press(t, m, run('y', 0, "y"))
	m.selectByName("notes.txt")
	press(t, m, run('y', 0, "y"))

	out := ansi.Strip(render(m))
	for _, want := range []string{"MARKED 2", "notes.txt", "README.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("the sidebar should show %q:\n%s", want, out)
		}
	}
}

// In the grid the preview still splits the focused pane when there is room: the
// folder description on the right of the listing, as before.
func TestPreviewSplitsTheFocusedGridPane(t *testing.T) {
	m, _ := fourPanes(t)
	m.preview = true
	m.Focused = 0
	l := m.measure()
	if l.PreviewWidth == 0 {
		t.Fatalf("a %d-wide pane should keep the preview", l.PaneWidth)
	}
	if l.PreviewWidth >= l.PaneWidth {
		t.Errorf("the preview must leave the listing room: %d of %d", l.PreviewWidth, l.PaneWidth)
	}
}

// Switching back to a tab that is already showing its directory is a change of
// focus, not a reason to ask the server again: that is what made it feel laggy.
func TestSwitchingToABoundTabDoesNotReload(t *testing.T) {
	m := twoHosts(t)
	m.activeTab = 1
	pane := m.paneIndexFor("contabo")
	m.Panes[pane].Entries = []Entry{{Name: "kept.txt"}}

	cmd := m.switchTab(1)
	if cmd != nil {
		t.Error("a pane that already has its listing should not load again")
	}
	if len(m.Panes[pane].Entries) != 1 || m.Panes[pane].Entries[0].Name != "kept.txt" {
		t.Error("the listing should be left exactly as it was")
	}
	if m.Focused != pane {
		t.Errorf("the focus should move to the pane, got %d", m.Focused)
	}
}

// Marking is cumulative: one file at a time, or a whole selection at once. The
// old behaviour replaced the set, so marking the second file lost the first.
func TestMarkingAccumulates(t *testing.T) {
	m := newTestModel(t)

	m.selectByName("README.md")
	press(t, m, run('y', 0, "y"))
	m.selectByName("notes.txt")
	press(t, m, run('y', 0, "y"))
	if m.stagedCount() != 2 {
		t.Fatalf("marking two files should mark two, got %v", m.staged.Paths)
	}
	if m.staged.Paths[0] != "notes.txt" || m.staged.Paths[1] != "README.md" {
		t.Errorf("the set should keep the listing's order, got %v", m.staged.Paths)
	}

	// y again on a marked file takes it back.
	m.selectByName("notes.txt")
	press(t, m, run('y', 0, "y"))
	if m.stagedCount() != 1 || m.staged.Paths[0] != "README.md" {
		t.Errorf("y on a marked file should unmark it, got %v", m.staged.Paths)
	}

	// y then x on the same file changes the kind instead of unmarking it.
	m.selectByName("README.md")
	press(t, m, run('x', 0, "x"))
	if m.stagedCount() != 1 || !m.staged.Cut {
		t.Errorf("x should turn the mark into a move, got %+v", m.staged)
	}

	// A selection marks all of it in one go.
	m.clearStaged()
	m.clearSelection()
	m.selectByName("notes.txt")
	press(t, m, keyNamed(tea.KeySpace))
	press(t, m, keyNamed(tea.KeySpace))
	press(t, m, run('y', 0, "y"))
	if m.stagedCount() != 2 {
		t.Errorf("y with two selected should mark both, got %v", m.staged.Paths)
	}
}

// Advanced paste: choose the conflict rule, the mode, and whether an archive is
// unpacked on arrival — one card, not three dialogs.
func TestPasteOptionsExtractOnArrival(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path

	// Build an archive to mark.
	m.selectByName("assets")
	if cmd := m.compressTargets("local:" + filepath.Join(root, "site.tar.gz")); cmd != nil {
		cmd()
	}
	m.refresh()
	m.clearSelection()
	m.selectByName("site.tar.gz")
	press(t, m, run('y', 0, "y"))
	if m.stagedCount() != 1 {
		t.Fatalf("expected the archive to be marked, got %v", m.staged.Paths)
	}

	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1

	press(t, m, run('A', 0, "A"))
	if m.Overlay != OverlayPaste {
		t.Fatalf("A should open the paste options, overlay is %v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"Paste options", "update if newer", "after"} {
		if !strings.Contains(out, want) {
			t.Errorf("the options card should say %q:\n%s", want, out)
		}
	}

	// Unpack on arrival, and paste.
	press(t, m, run('e', 0, "e"))
	if m.staged.After != pasteExtract {
		t.Fatal("e should choose unpacking")
	}
	cmd := m.handlePasteOptionsKey(keyNamed(tea.KeyEnter))
	if cmd == nil {
		t.Fatalf("Enter should paste: %v", m.toasts)
	}
	report, ok := cmd().(copyReport)
	if !ok || report.err != nil {
		t.Fatalf("paste failed: %+v", report)
	}
	if report.extracted == "" {
		t.Fatalf("the archive should have been unpacked: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dest, "site", "assets", "logo.svg")); err != nil {
		t.Errorf("the extracted tree should be in the destination: %v", err)
	}

	// A copy keeps the marks; a move clears them.
	if m.stagedCount() != 1 {
		t.Error("a copy should leave the marks in place")
	}
}

// Marks outlive navigation: marking in one folder and pasting into several places
// is one gesture, so the source directory travels with the marks.
func TestMarksSurviveNavigation(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path
	m.selectByName("README.md")
	press(t, m, run('y', 0, "y"))

	// Walk somewhere else in the source pane.
	m.navigate(filepath.Join(root, "assets"))
	if m.stagedCount() != 1 {
		t.Fatalf("navigating should not drop the marks, got %v", m.staged.Paths)
	}

	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1

	cmd := m.pasteStaged()
	if cmd == nil {
		t.Fatalf("paste should start: %v", m.toasts)
	}
	if report, ok := cmd().(copyReport); !ok || report.err != nil {
		t.Fatalf("paste failed: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("the marked file should have travelled from its own directory: %v", err)
	}
}

// Pack-on-paste: the destination gets one archive, not the files. "Copy this
// folder over there, but as a tarball" is one gesture.
func TestPasteOptionsPackInsteadOfCopy(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("assets")
	press(t, m, run('y', 0, "y"))

	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1

	press(t, m, run('A', 0, "A"))
	if out := ansi.Strip(render(m)); !strings.Contains(out, "packed into assets.tar.gz") {
		t.Fatalf("the card should offer the pack name:\n%s", out)
	}
	press(t, m, run('c', 0, "c"))
	cmd := m.handlePasteOptionsKey(keyNamed(tea.KeyEnter))
	if cmd == nil {
		t.Fatalf("Enter should paste: %v", m.toasts)
	}
	report, ok := cmd().(copyReport)
	if !ok || report.err != nil {
		t.Fatalf("pack paste failed: %+v", report)
	}
	if report.packed == "" {
		t.Fatalf("the report should name the archive: %+v", report)
	}

	// The archive is there, and the loose files are not.
	if _, err := os.Stat(filepath.Join(dest, "assets.tar.gz")); err != nil {
		t.Fatalf("the archive should be in the destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "assets")); err == nil {
		t.Error("a pack paste must not copy the files loose as well")
	}

	// And the archive really holds the tree.
	entries, err := archive.List(destFS, "/assets.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name, "logo.svg") {
			found = true
		}
	}
	if !found {
		t.Errorf("the archive should hold the folder's files, got %+v", entries)
	}
}

// A copy in flight is visible in the sidebar, with a bar and the current file,
// and it disappears when the report lands.
func TestTransfersShowProgressInTheSidebar(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("README.md")
	press(t, m, run('y', 0, "y"))

	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1

	cmd := m.pasteStaged()
	if cmd == nil {
		t.Fatalf("paste should start: %v", m.toasts)
	}
	if m.Jobs() != 1 {
		t.Fatalf("the copy should be registered as running, jobs=%d", m.Jobs())
	}

	// Progress arrives on the channel; feed one report the way the runtime would.
	var id int
	for key := range m.transfers {
		id = key
	}
	m.Update(transferUpdate{id: id, name: "README.md", done: 5, total: 10})
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "TRANSFERS") || !strings.Contains(out, "50%") {
		t.Errorf("the sidebar should show the copy's progress:\n%s", out)
	}
	if !strings.Contains(out, "README.md") {
		t.Errorf("the sidebar should name the file being copied:\n%s", out)
	}

	// The report finishes it.
	report, ok := cmd().(copyReport)
	if !ok || report.err != nil {
		t.Fatalf("the copy should finish: %+v", report)
	}
	m.Update(report)
	// It stays visible for a moment, saying it finished: a copy of small files is
	// over before the next frame, and a job that never appears reads as a job that
	// never ran.
	if m.Jobs() != 1 {
		t.Errorf("a finished copy should stay visible briefly, jobs=%d", m.Jobs())
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "done") {
		t.Errorf("the sidebar should say the copy finished:\n%s", out)
	}

	// And then it goes.
	m.pruneTransfers(time.Now().Add(time.Minute))
	if m.Jobs() != 0 {
		t.Errorf("a finished copy should be forgotten, jobs=%d", m.Jobs())
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "Nothing running") {
		t.Errorf("the sidebar should go back to saying nothing is running:\n%s", out)
	}
}

// The copy's progress must reach the update loop. It did not: the channel was
// created when the paste began, after Init had already decided there was nothing
// to listen to, so the sidebar showed "working…" until the report arrived.
func TestProgressReportsReachTheChannel(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.armProgress(); cmd == nil {
		t.Fatal("arming should start a reader")
	}

	m.selectByName("README.md")
	press(t, m, run('y', 0, "y"))
	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1

	cmd := m.pasteStaged()
	if cmd == nil {
		t.Fatalf("paste should start: %v", m.toasts)
	}
	if report, ok := cmd().(copyReport); !ok || report.err != nil {
		t.Fatalf("copy failed: %+v", report)
	}
	if len(m.progress) == 0 {
		t.Fatal("the copy should have reported progress")
	}
	update := <-m.progress
	if update.done <= 0 || update.total <= 0 {
		t.Errorf("progress should carry bytes: %+v", update)
	}
	if _, ok := m.transfers[update.id]; !ok {
		t.Errorf("the report should belong to the running job, id %d", update.id)
	}
}

// The whole loop, the way the runtime runs it: a real copy command in a
// goroutine, a real reader on the channel, and the report folded through Update.
// If any link is missing, this is where it shows.
func TestTransferReportsTravelThroughTheChannel(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.armProgress(); cmd == nil {
		t.Fatal("startup should arm a reader")
	}
	m.selectByName("README.md")
	press(t, m, run('y', 0, "y"))

	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1

	copyCmd := m.pasteStaged()
	if copyCmd == nil {
		t.Fatalf("paste should start: %v", m.toasts)
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- copyCmd() }()

	// The reader blocks until a report arrives; that is what the runtime does.
	update, ok := waitForTransfer(m.progress)().(transferUpdate)
	if !ok {
		t.Fatal("the reader should deliver a transfer update")
	}
	m.Update(update)
	job := m.transfers[update.id]
	if job.done <= 0 {
		t.Errorf("the update should carry bytes, got %+v", job)
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "copy → contabo") {
		t.Errorf("the sidebar should name the running copy:\n%s", out)
	}

	// And the copy finishes.
	<-done
}

// The sidebar spans the whole pane area in the grid. Sized for one block it showed
// HOSTS and BOOKMARKS but cut TRANSFERS and THEME in half.
func TestSidebarSpansTheGridHeight(t *testing.T) {
	m, _ := fourPanes(t)
	m.Panes = m.Panes[:3]
	resize(t, m, 150, 40)

	out := ansi.Strip(render(m))
	for _, want := range []string{"HOSTS", "BOOKMARKS", "TRANSFERS", "THEME"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the sidebar should show %q:\n%s", want, out)
		}
	}

	lines := strings.Split(out, "\n")
	bottom := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "╰") {
			bottom = i
		}
	}
	if bottom < 0 {
		t.Fatal("the sidebar has no bottom border")
	}
	// The panes' bottom borders are on the same row: one body, one bottom edge.
	if count := strings.Count(lines[bottom], "╰"); count < 3 {
		t.Errorf("the sidebar and the panes should end together, row %d has %d corners:\n%s",
			bottom, count, lines[bottom])
	}
}

// Every row of the "after" group carries a mark, including the one that cannot be
// chosen: a row without one looked like an option whose icon had gone missing.
func TestPasteOptionsEveryRowHasAMark(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("notes.txt") // not an archive
	press(t, m, run('y', 0, "y"))
	resize(t, m, 120, 40)
	press(t, m, run('A', 0, "A"))

	card := ansi.Strip(m.renderPasteOptions(m.measure()))
	rows := 0
	for _, line := range strings.Split(card, "\n") {
		if !strings.Contains(line, "after") && !strings.HasPrefix(strings.TrimPrefix(line, "│"), "  e") &&
			!strings.HasPrefix(strings.TrimPrefix(line, "│"), "  c") {
			continue
		}
		rows++
		if !strings.Contains(line, "[x]") && !strings.Contains(line, "[ ]") && !strings.Contains(line, "[-]") {
			t.Errorf("an option row has no mark: %q", line)
		}
	}
	if rows != 3 {
		t.Errorf("expected three rows in the after group, found %d:\n%s", rows, card)
	}
	if !strings.Contains(card, "not an archive") {
		t.Errorf("the unavailable unpack should say why:\n%s", card)
	}
}

// The local folder is a choice, not a fact: four servers is a legitimate
// dashboard, and a bucket needs no session to be offered.
func TestDashboardChooserOffersBucketsAndOptionalLocal(t *testing.T) {
	m, _ := fourPanes(t)
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{
		"backups": {Provider: "s3", Bucket: "my-backups", Region: "eu-central-1"},
	}
	m.loadConnections()

	m.openPanes()
	// The bucket is offered even though nothing opened it.
	found := false
	for i, choice := range m.paneChoices() {
		if choice == "cloud:backups" {
			found = true
			m.panes.cursor = i
		}
	}
	if !found {
		t.Fatalf("the chooser should offer the bucket: %v", m.paneChoices())
	}
	if !strings.Contains(ansi.Strip(render(m)), "on demand") {
		t.Error("the bucket card should say it opens on demand")
	}

	// Untick the local machine and tick the bucket: apply, then run the client
	// command the way the runtime would.
	delete(m.panes.chosen, "")
	m.panes.chosen["cloud:backups"] = true
	cmd := m.applyPanes()
	if cmd == nil {
		t.Fatal("applying a bucket choice should start opening it")
	}
	for _, pane := range m.Panes {
		if pane.Side == SideLocal {
			t.Error("the local pane was unticked and should not be on screen")
		}
	}
}

// Entering the dashboard asks when there is something to choose between: a
// bucket counts even with no sessions.
func TestDashboardAsksWhenOnlyBucketsExist(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{
		"backups": {Provider: "s3", Bucket: "my-backups", Region: "eu-central-1"},
	}
	m.screen = screenHome
	m.panesAsked = false
	m.openDashboard()
	if m.Overlay != OverlayPanes {
		t.Fatalf("a configured bucket should make the dashboard ask, overlay=%v", m.Overlay)
	}
}

// The launcher offers both questions: "show me the files" and "what should the
// dashboard show?".
func TestLauncherDashboardAndLocalFiles(t *testing.T) {
	m, _ := fourPanes(t)
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{
		"backups": {Provider: "s3", Bucket: "b", Region: "eu-central-1"},
	}
	m.loadConnections()
	m.screen = screenHome

	// Dashboard asks.
	m.runAction("app.dashboard")
	if m.Overlay != OverlayPanes {
		t.Fatalf("Dashboard should open the chooser, overlay=%v", m.Overlay)
	}

	// Browse local files does not: it puts the local pane in front.
	m.Overlay = OverlayNone
	m.Panes = m.Panes[1:] // pretend a remote-only dashboard
	m.Focused = 0
	m.runAction("app.files")
	if m.Overlay == OverlayPanes {
		t.Error("Browse local files should not ask a question")
	}
	if index := m.localPaneIndex(); index < 0 {
		t.Fatal("Browse local files should bring the local pane back")
	} else if m.Focused != index {
		t.Errorf("the local pane should have the focus, focused %d", m.Focused)
	}
}

// A transfer large enough to watch gets a screen; a routine copy stays in the
// sidebar.
func TestBigTransfersGetAScreen(t *testing.T) {
	if bigTransfer(4, 2<<20) {
		t.Error("a four-file copy should stay in the sidebar")
	}
	if !bigTransfer(5, 1) {
		t.Error("five files is worth watching")
	}
	if !bigTransfer(1, 200<<20) {
		t.Error("two hundred megabytes is worth watching")
	}

	m := newTestModel(t)
	m.transfers = map[int]transfer{
		1: {id: 1, label: "copy → contabo", name: "assets/logo.svg", done: 3, total: 4, started: time.Now()},
	}
	// Alt+J opens it rather than toasting a summary.
	m.runAction("view.jobs")
	if m.Overlay != OverlayTransfers {
		t.Fatalf("Alt+J should open the transfer screen, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"Transfers", "copy → contabo", "assets/logo.svg", "Esc closes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the screen should show %q:\n%s", want, out)
		}
	}
	press(t, m, keyNamed(tea.KeyEsc))
	if m.Overlay != OverlayNone {
		t.Error("Esc should close the screen")
	}
	// The copy itself is untouched by closing it.
	if m.Jobs() != 1 {
		t.Errorf("closing the screen must not cancel the copy, jobs=%d", m.Jobs())
	}
}

// A big paste opens the screen on its own, and a dialog that is already open wins.
func TestBigPasteOpensTheTransferScreen(t *testing.T) {
	m := newTestModel(t)
	dest := t.TempDir()
	destFS, err := vfs.NewLocal(dest)
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[1] = Pane{Side: SideRemote, Host: "contabo", FS: destFS, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.Focused = 1

	// Eight tiny files: past the five-file threshold, without writing many.
	names := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		names = append(names, fmt.Sprintf("f%02d", i))
	}
	m.staged = stagedState{Paths: names, From: 0, Sizes: map[string]int64{}, Dir: m.Panes[0].Path}
	if cmd := m.pasteStaged(); cmd == nil {
		t.Fatalf("paste should start: %v", m.toasts)
	}
	if m.Overlay != OverlayTransfers {
		t.Errorf("an eight-file paste should open the transfer screen, overlay=%v", m.Overlay)
	}

	// With a dialog open, the screen does not steal it.
	m.Overlay = OverlayPrompt
	m.openTransfersIfBig(8, 0)
	if m.Overlay != OverlayPrompt {
		t.Error("an open dialog should not be replaced by the transfer screen")
	}
}
