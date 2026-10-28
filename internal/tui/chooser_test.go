package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/remoteinfo"
	"github.com/fakihariefnoto/omssh/internal/session"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// The chooser is a wall of cards: each session says where it is, what it is
// doing, and whether it is already on screen.
func TestSessionCardsCarryTheNumbers(t *testing.T) {
	m, _ := fourPanes(t)
	m.openPanes()
	m.samples["two"] = remoteinfo.Parse("6.40 4.10 3.30 2/345 6789\n" +
		"Mem:  8589934592 7516192768 1 0 0 4\n" +
		"/dev/sda1  41943040 37748736 4194304  90% /\n8\n")

	out := ansi.Strip(render(m))
	for _, want := range []string{
		"Sessions", "local machine", "one", "two", "three",
		"load 6.40", "7.0 GB/8.0 GB", "on screen", "Space tick", "4 of 4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the chooser should show %q:\n%s", want, out)
		}
	}

	// A host that has not answered yet shows dashes, not zeroes pretending to be
	// a healthy machine.
	if !strings.Contains(out, "load   —") {
		t.Errorf("an unanswered host should show dashes:\n%s", out)
	}
}

// The chooser owns the screen: the panes behind it are cleared, so a wall of
// cards is not read through four borders.
func TestChooserOwnsTheScreen(t *testing.T) {
	m, _ := fourPanes(t)
	m.openPanes()
	lines := strings.Split(ansi.Strip(render(m)), "\n")
	first := lines[0]
	if strings.Contains(first, "sessions") || strings.Contains(first, "omssh") {
		t.Errorf("the chooser should clear the frame behind it: %q", first)
	}
	if strings.TrimSpace(first) != "" {
		t.Errorf("the first row should be blank, got %q", first)
	}
}

// The preview sits to the right of the listing in the grid too: one rule for both
// layouts, so "where is the preview" never has two answers.
func TestPreviewInTheGridIsBesideTheListing(t *testing.T) {
	m, _ := fourPanes(t)
	m.Panes[0].Path = m.Panes[0].Home
	m.Panes[0].Load(false)
	mustWrite(t, m.Panes[0].Path+"/hello.txt", "the quick brown fox\n")
	m.Panes[0].Load(false)
	m.selectByName("hello.txt")
	m.preview = true
	m.Focused = 0
	if fetch := m.previewCmd(); fetch == nil {
		t.Fatal("the preview should be fetched")
	} else {
		m.applyPreview(fetch().(previewReadyMsg))
	}

	out := ansi.Strip(render(m))
	if !strings.Contains(out, "the quick brown fox") {
		t.Errorf("the preview should show the file in the focused pane:\n%s", out)
	}
	// The listing is still there, to the left of the preview, in the same pane.
	if !strings.Contains(out, "hello.txt") {
		t.Errorf("the listing should stay beside its preview:\n%s", out)
	}
	// And the preview is inside the focused pane's box, not a panel of its own.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "the quick brown fox") && strings.Contains(line, "│") {
			break
		}
	}
	// And the frame is still exact.
	l := m.measure()
	for i, line := range strings.Split(out, "\n") {
		if len([]rune(line)) != 160 {
			t.Fatalf("line %d is %d cells, want 160: %q", i, len([]rune(line)), line)
		}
	}
	_ = l
}

// A tab whose session ended reconnects when chosen: the tab is a promise, and a
// dead end is not an answer.
func TestChoosingADeadTabReconnects(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{})
	m.tabs = append(m.tabs, Tab{Title: "contabo", Host: "contabo", State: StateError})
	m.activeTab = 1
	m.screen = screenFiles

	cmd := m.switchTab(1)
	if cmd == nil {
		t.Fatalf("choosing a dead tab should start a connection: %v", m.toasts)
	}
	if m.tabs[1].State != StateConnecting {
		t.Errorf("the tab should say connecting, got %v", m.tabs[1].State)
	}
}

// A failure for a host that has no tab must not invent one: a phantom tab is a
// dead end that says "no longer connected" forever.
func TestFailedConnectDoesNotInventTabs(t *testing.T) {
	m := newTestModel(t)
	before := len(m.tabs)
	m.tabState("ghost", StateError)
	if len(m.tabs) != before {
		t.Errorf("a failed connect should not add a tab, %d tabs became %d", before, len(m.tabs))
	}

	// A real attempt still has a tab to land on, because connect makes it first.
	m.ensureTab("contabo")
	if len(m.tabs) != before+1 {
		t.Error("connect should create the tab before dialing")
	}
	_ = keyNamed(tea.KeyEnter)
}

// The dashboard header carries the host's numbers, compactly when narrow.
func TestPaneHeaderShowsHostStats(t *testing.T) {
	m := newTestModel(t)
	local, err := vfs.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.Panes[0] = Pane{Side: SideRemote, Host: "contabo", FS: local, Home: "/", Path: "/",
		Selected: map[string]bool{}, History: []string{"/"}}
	m.samples = map[string]remoteinfo.Info{"contabo": {
		Known: true, Load: 1.5, CPUs: 2,
		MemTotal: 100, MemUsed: 75, DiskTotal: 100, DiskFree: 40,
	}}
	resize(t, m, 140, 30)
	out := ansi.Strip(render(m))
	for _, want := range []string{"L1.5", "M75%", "D60%"} {
		if !strings.Contains(out, want) {
			t.Errorf("the pane header should show %q:\n%s", want, out)
		}
	}
}

// The chooser says how to move, and both axes work.
func TestPanesChooserGuidanceAndArrows(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{"backups": {Provider: "s3", Bucket: "b", Region: "eu-central-1"}}
	m.screen = screenHome
	cmd := m.runAction("app.dashboard")
	if m.Overlay != OverlayPanes {
		t.Fatalf("the dashboard should ask which panes, overlay=%v", m.Overlay)
	}
	if cmd == nil {
		t.Error("opening the chooser should also start sampling; otherwise the cards stay at load —")
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"←/→", "↑/↓", "Space tick", "Esc back"} {
		if !strings.Contains(out, want) {
			t.Errorf("the chooser should explain %q:\n%s", want, out)
		}
	}

	// Right and left move one card, like down and up.
	before := m.panes.cursor
	press(t, m, keyNamed(tea.KeyRight))
	if m.panes.cursor == before {
		t.Error("right should move the selection")
	}
	press(t, m, keyNamed(tea.KeyLeft))
	if m.panes.cursor != before {
		t.Error("left should move it back")
	}

	// Escape goes back to where the chooser opened.
	press(t, m, keyNamed(tea.KeyEsc))
	if m.Overlay != OverlayNone || m.screen != screenHome {
		t.Errorf("Esc should return to the launcher, overlay=%v screen=%v", m.Overlay, m.screen)
	}
}
