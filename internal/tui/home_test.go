package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// newLauncher builds an interface that starts on the front door.
func newLauncher(t *testing.T) *Model {
	t.Helper()
	cfg := config.Default()
	cfg.UI.Theme = "midnight"
	cfg.UI.Icons = "ascii"
	cfg.UI.Start = "home"
	dir := t.TempDir()
	mustWrite(t, dir+"/a.txt", "a\n")
	m, err := New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: true, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 140, 40)
	return m
}

// The launcher is the front door, and it says what the program is.
func TestHomeIsTheDefaultScreen(t *testing.T) {
	m := newLauncher(t)
	if m.screen != screenHome {
		t.Fatal("a first run should start on the launcher")
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{strings.TrimSpace(logo[0]), "Oh My SSH", "Local files", "Settings, theme and keys", "Quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launcher is missing %q:\n%s", want, out)
		}
	}
	// Every entry carries a plain ASCII icon, good on any terminal.
	for _, icon := range []string{"[~]", "[M]", "[+]", "[x]"} {
		if !strings.Contains(out, icon) {
			t.Errorf("the launcher is missing the %s icon:\n%s", icon, out)
		}
	}
	// And one line that teaches the keys, so nothing is memorised.
	if !strings.Contains(out, "Esc go to the files") {
		t.Errorf("the launcher should say how to skip it:\n%s", out)
	}
}

// The plate is five equal rows; unequal rows read as a rendering bug.
func TestLogoLinesAreEqualWidth(t *testing.T) {
	width := visibleWidth(logo[0])
	for i, line := range logo {
		if got := visibleWidth(line); got != width {
			t.Errorf("logo row %d is %d cells, want %d: %q", i, got, width, line)
		}
	}
	for i, line := range narrowLogo {
		if got := visibleWidth(line); got != visibleWidth(narrowLogo[0]) {
			t.Errorf("narrow logo row %d is %d cells and its row 0 is %d", i, got, visibleWidth(narrowLogo[0]))
		}
	}
}

// Both units on the launcher are centred on the terminal's centre line. The
// plate used to be padded into the menu's wider box, which left the wordmark
// visibly left of centre; they are separate units now.
func TestPlateAndMenuAreCentred(t *testing.T) {
	for _, width := range []int{140, 100, 76} {
		m := newLauncher(t)
		resize(t, m, width, 34)
		lines := strings.Split(ansi.Strip(render(m)), "\n")

		// Widths are measured in terminal cells, not bytes: a block glyph is three
		// bytes and one cell.
		centreOf := func(line string) int {
			left := visibleWidth(line) - visibleWidth(strings.TrimLeft(line, " "))
			right := visibleWidth(strings.TrimRight(line, " "))
			return (left + right) / 2
		}

		plateRow := ""
		for _, line := range lines {
			if strings.Contains(line, "█") {
				plateRow = line
				break
			}
		}
		if plateRow == "" {
			t.Fatalf("width %d: no wordmark found", width)
		}
		if diff := centreOf(plateRow) - width/2; diff < -1 || diff > 1 {
			t.Errorf("width %d: the wordmark centre is at %d, off by %d", width, centreOf(plateRow), diff)
		}

		// Every entry's icon sits in the same column. The cursor row is marked
		// with a pointer, so the leading spaces differ on purpose; the icon column
		// must not.
		iconColumn := func(line string) int {
			for _, icon := range []string{"[~]", "[M]", "[+]", "[x]"} {
				if i := strings.Index(line, icon); i >= 0 {
					return visibleWidth(line[:i])
				}
			}
			return -1
		}
		columns := map[int]bool{}
		for _, line := range lines {
			if col := iconColumn(line); col >= 0 {
				columns[col] = true
			}
		}
		if len(columns) != 1 {
			t.Errorf("width %d: the menu icons do not form one column: %v", width, columns)
		}

		footerRow := ""
		for _, line := range lines {
			if strings.Contains(line, "↑↓") {
				footerRow = line
				break
			}
		}
		if footerRow == "" {
			t.Fatalf("width %d: no footer found", width)
		}
		if diff := centreOf(footerRow) - width/2; diff < -1 || diff > 1 {
			t.Errorf("width %d: the menu centre is at %d, off by %d", width, centreOf(footerRow), diff)
		}
	}
}

// The launcher fills exactly the terminal: a short frame leaves the previous
// one's remnants on screen.
func TestHomeFillsTheTerminal(t *testing.T) {
	for _, size := range []struct{ w, h int }{{140, 40}, {100, 30}, {60, 20}, {40, 14}} {
		m := newLauncher(t)
		resize(t, m, size.w, size.h)
		lines := strings.Split(render(m), "\n")
		if len(lines) != size.h {
			t.Errorf("%dx%d: rendered %d lines", size.w, size.h, len(lines))
		}
		for i, line := range lines {
			if got := visibleWidth(line); got != size.w {
				t.Errorf("%dx%d line %d is %d cells, want %d", size.w, size.h, i, got, size.w)
			}
		}
	}
}

// The launcher is a menu: arrows wrap, and Enter takes the focused entry.
func TestHomeMenuNavigatesAndActingLeaves(t *testing.T) {
	m := newLauncher(t)
	items := m.homeItems()

	press(t, m, keyNamed(tea.KeyDown))
	if m.home.cursor != 1 {
		t.Fatalf("down should move to the second entry, cursor is %d", m.home.cursor)
	}
	press(t, m, keyNamed(tea.KeyUp))
	press(t, m, keyNamed(tea.KeyUp))
	if m.home.cursor != len(items)-1 {
		t.Errorf("up from the first entry should wrap to the last, cursor is %d", m.home.cursor)
	}

	// "Local files" asks how, then takes you to the files.
	m.home.cursor = indexOfHome(m, "app.local")
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayLocalChoose {
		t.Fatalf("Local files should ask browse or go to, overlay=%v", m.Overlay)
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if m.screen != screenFiles {
		t.Error("choosing Browse local files should open the file manager")
	}

	// "Quit" really quits.
	m2 := newLauncher(t)
	m2.home.cursor = indexOfHome(m2, "app.quit")
	press(t, m2, keyNamed(tea.KeyEnter))
	if !m2.quit {
		t.Error("choosing Quit should quit")
	}
}

// "Go to a folder" is the second choice inside Local files, and its prompt opens
// over the launcher's card.
func TestHomeGoToFolderOpensThePathPrompt(t *testing.T) {
	m := newLauncher(t)
	m.home.cursor = indexOfHome(m, "app.local")
	press(t, m, keyNamed(tea.KeyEnter))
	press(t, m, keyNamed(tea.KeyDown))
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayPrompt {
		t.Errorf("Go to a folder should open the path prompt, overlay is %v", m.Overlay)
	}
}

// A launcher entry with no action behind it is a dead end, and dead ends are
// exactly what this menu must not have.
func TestHomeItemsAreRealActions(t *testing.T) {
	m := newLauncher(t)
	for _, item := range m.homeItems() {
		if _, ok := m.reg.Get(item.ID); !ok {
			t.Errorf("launcher entry %q has no action", item.ID)
		}
		if item.Label == "" || item.Icon == "" || item.Hint == "" {
			t.Errorf("launcher entry %q is incomplete: %+v", item.ID, item)
		}
	}
}

// The launcher advertises shortcuts in its menu, so those shortcuts have to work
// from the launcher: a menu entry that promises Ctrl+O and swallows it is worse
// than no hint at all.
func TestLauncherHonoursTheShortcutsItAdvertises(t *testing.T) {
	m := newLauncher(t)
	press(t, m, run('o', tea.ModCtrl, ""))
	if m.screen != screenConnections {
		t.Errorf("Ctrl+O should open the connections screen from the launcher, screen is %v", m.screen)
	}
	if m.connReturn != screenHome {
		t.Errorf("Esc should return to the launcher, return screen is %v", m.connReturn)
	}
	press(t, m, keyNamed(tea.KeyEsc))
	if m.screen != screenHome {
		t.Errorf("Esc should come back to the launcher, screen is %v", m.screen)
	}

	// Ctrl+K still works from the launcher, and plain letters stay local.
	m2 := newLauncher(t)
	press(t, m2, run('k', tea.ModCtrl, ""))
	if m2.Overlay != OverlayPalette {
		t.Error("Ctrl+K should open the palette from the launcher")
	}
	m3 := newLauncher(t)
	cursor := m3.home.cursor
	press(t, m3, run('z', 0, "z"))
	if m3.home.cursor != cursor {
		t.Error("a plain letter should not move the launcher's cursor")
	}
}

// The launcher is clickable, and a click means the same as Enter there.
func TestHomeIsClickable(t *testing.T) {
	m := newLauncher(t)
	_, top, itemStart := m.homeGeometry(m.measure())
	index := indexOfHome(m, "app.local")
	click := tea.MouseClickMsg(tea.Mouse{
		X: 20, Y: top + itemStart + index, Button: ansi.MouseLeft,
	})
	_, _ = m.Update(click)
	if m.Overlay != OverlayLocalChoose {
		t.Fatalf("clicking Local files should open its card, overlay=%v", m.Overlay)
	}
	// The first row is Browse, which then opens the files.
	press(t, m, keyNamed(tea.KeyEnter))
	if m.screen != screenFiles {
		t.Errorf("choosing Browse local files should open the file manager, screen is %v", m.screen)
	}
}

// ui.start = files skips the launcher for people who do not want a front door.
func TestStartScreenIsConfigurable(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Start = "files"
	m, err := New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: true, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if m.screen != screenFiles {
		t.Error("ui.start=files should skip the launcher")
	}
}

// The tab strip reads as a tab bar: labelled, one row, with the active tab marked
// by an underline rather than a filled chip that looks like a floating badge.
func TestTabStripReadsAsATabBar(t *testing.T) {
	m := newTestModel(t)
	out := render(m)
	row := strings.Split(out, "\n")[0]
	plain := ansi.Strip(row)

	if !strings.Contains(plain, "sessions") {
		t.Errorf("the tab strip should say what it is: %q", plain)
	}
	if !strings.Contains(plain, "localhost") {
		t.Errorf("the tab strip should name the tab: %q", plain)
	}
	// The frame paints the theme's background behind every line; the tab itself
	// must not add a chip on top of it.
	tab := strings.ReplaceAll(row, "\x1b[48;2;14;17;22m", "")
	if strings.Contains(tab, "48;") {
		t.Errorf("the tab strip should not paint a background chip: %q", tab)
	}
	if !strings.Contains(row, "\x1b[4m") && !strings.Contains(row, ";4m") && !strings.Contains(row, ";4;") {
		t.Errorf("the active tab should be underlined: %q", row)
	}
}

func indexOfHome(m *Model, id string) int {
	for i, item := range m.homeItems() {
		if string(item.ID) == id {
			return i
		}
	}
	return -1
}

// Coming back to the launcher from a pane should offer the way back to every
// host that is still connected: the menu is the map of where you are, not only
// of where you can go.
func TestLauncherListsConnectedHosts(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{})
	dir := t.TempDir()
	fsys, err := vfs.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.addSessionTab(&session.Session{ID: "s1", Target: omssh.Target{Alias: "contabo"}, FS: fsys, Root: "/", Base: dir})
	m.screen = screenHome

	out := ansi.Strip(render(m))
	if !strings.Contains(out, "contabo") {
		t.Errorf("the launcher should list the connected host:\n%s", out)
	}
	if !strings.Contains(out, "connected") {
		t.Errorf("the host row should say it is connected:\n%s", out)
	}

	// Choosing it returns to that host, not to the first tab.
	m.home.cursor = 0
	cmd := m.activateHome(m.homeItems()[0])
	_ = cmd
	if m.screen != screenFiles {
		t.Errorf("choosing a host should open the files, screen is %v", m.screen)
	}
	if m.activeTab != 1 || m.Focused != 1 {
		t.Errorf("choosing contabo should focus its pane, tab %d focused %d", m.activeTab, m.Focused)
	}
}

// The launcher's order: connections first, then the things that use them, then
// the local machine as one row rather than two half-questions.
func TestLauncherOrderAndLocalRow(t *testing.T) {
	m := newTestModel(t)
	ids := make([]string, 0, len(m.homeItems()))
	for _, item := range m.homeItems() {
		ids = append(ids, string(item.ID))
	}
	index := func(id string) int {
		for i, got := range ids {
			if got == id {
				return i
			}
		}
		return -1
	}
	manage, dashboard := index("session.open"), index("app.dashboard")
	tools, history, mcp := index("app.session-tools"), index("app.history"), index("app.mcp")
	if manage < 0 || dashboard < 0 || tools < 0 || history < 0 || mcp < 0 {
		t.Fatalf("expected Manage, Dashboard, Terminal & Tunnel, History and MCP, got %v", ids)
	}
	if manage >= dashboard || dashboard >= tools || tools >= history || history >= mcp {
		t.Errorf("launcher order should be Manage, Dashboard, tools, History, MCP: %v", ids)
	}
	if index("app.local") < 0 {
		t.Errorf("one Local files row should be offered: %v", ids)
	}
	if index("app.files") != -1 || index("nav.path") != -1 {
		t.Errorf("Browse local files and Go to a folder should be folded into Local files: %v", ids)
	}
}

// Local files is a two-option card. Browse opens the local pane; go to a folder
// asks for the path first. Either way the dashboard follows.
func TestLocalChooser(t *testing.T) {
	m := newTestModel(t)
	m.runAction("app.local")
	if m.Overlay != OverlayLocalChoose {
		t.Fatalf("app.local should open the card, overlay=%v", m.Overlay)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"Local files", "Browse local files", "Go to a folder"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card should show %q:\n%s", want, out)
		}
	}

	// Browse: the local pane is where we land.
	press(t, m, keyNamed(tea.KeyEnter))
	if m.screen != screenFiles {
		t.Errorf("browse should go to the files, screen=%v", m.screen)
	}
	if m.localPaneIndex() < 0 {
		t.Error("browse should show the local pane")
	}

	// Go to a folder: a prompt, then the dashboard.
	dir := t.TempDir()
	m.screen = screenHome
	m.runAction("app.local")
	press(t, m, keyNamed(tea.KeyDown))
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayPrompt || m.prompt.Action != "prompt.path" {
		t.Fatalf("go to a folder should ask for a path, overlay=%v action=%q", m.Overlay, m.prompt.Action)
	}
	m.applyPrompt("prompt.path", dir)
	if m.screen != screenFiles {
		t.Errorf("a chosen folder should show the files, screen=%v", m.screen)
	}
	if m.Pane().Path != dir {
		t.Errorf("the local pane should be in %q, got %q", dir, m.Pane().Path)
	}
}
