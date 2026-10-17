package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
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
	for _, want := range []string{strings.TrimSpace(logo[0]), "SSH / SFTP for the terminal", "Browse local files", "Settings and theme", "Quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launcher is missing %q:\n%s", want, out)
		}
	}
	// Every entry carries a plain ASCII icon, good on any terminal.
	for _, icon := range []string{"[~]", "[/]", "[?]", "[+]", "[x]"} {
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

// Esc is the documented way straight into the files. The launcher must never
// trap someone who just wants to work.
func TestEscLeavesTheLauncher(t *testing.T) {
	m := newLauncher(t)
	press(t, m, keyNamed(tea.KeyEsc))
	if m.screen != screenFiles {
		t.Fatal("Esc should go to the file manager")
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

	// "Browse local files" takes you to the files.
	m.home.cursor = indexOfHome(m, "app.files")
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

// "Go to a folder" opens the path prompt over the file manager.
func TestHomeGoToFolderOpensThePathPrompt(t *testing.T) {
	m := newLauncher(t)
	m.home.cursor = indexOfHome(m, "nav.path")
	press(t, m, keyNamed(tea.KeyEnter))
	if m.screen != screenFiles {
		t.Fatal("Go to a folder should live in the file manager")
	}
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

// The launcher is clickable, and a click means the same as Enter there.
func TestHomeIsClickable(t *testing.T) {
	m := newLauncher(t)
	_, top, itemStart := m.homeGeometry(m.measure())
	index := indexOfHome(m, "app.files")
	click := tea.MouseClickMsg(tea.Mouse{
		X: 20, Y: top + itemStart + index, Button: ansi.MouseLeft,
	})
	_, _ = m.Update(click)
	if m.screen != screenFiles {
		t.Errorf("clicking Browse local files should open the file manager, screen is %v", m.screen)
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
	if strings.Contains(row, "48;") {
		t.Errorf("the tab strip should not paint a background chip: %q", row)
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
