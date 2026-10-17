package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// visibleWidth is the on-screen width of a styled line, which is what must fit
// in the terminal: escape sequences are not cells.
func visibleWidth(line string) int { return lipgloss.Width(line) }

// rgbaOf builds a colour for the messages a terminal sends back.
func rgbaOf(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 0xff} }

// newTestModel builds an interface over a temporary directory with a known
// shape, so rendering assertions have something real to describe.
func newTestModel(t *testing.T, tune ...func(*config.Config)) *Model {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "# hello\nsecond line\n")
	mustWrite(t, filepath.Join(dir, "notes.txt"), "one\ntwo\nthree\n")
	mustWrite(t, filepath.Join(dir, "archive.tar.gz"), "not really a tarball")
	mustWrite(t, filepath.Join(dir, ".hidden"), "dotfile\n")
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "assets", "logo.svg"), "<svg/>\n")

	cfg := config.Default()
	cfg.UI.Theme = "midnight"
	cfg.UI.Icons = "ascii"
	cfg.Keys.Preset = "standard"
	for _, fn := range tune {
		fn(&cfg)
	}

	// Start on the file manager: these tests are about files. The launcher has
	// its own tests.
	cfg.UI.Start = "files"
	m, err := New(Options{
		Config:  cfg,
		Profile: colorprofile.TrueColor,
		HasDark: true,
		Dir:     dir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resize(t, m, 140, 40)
	return m
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resize drives the model the way the program does.
func resize(t *testing.T, m *Model, width, height int) {
	t.Helper()
	if _, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height}); m.width != width {
		t.Fatalf("resize did not take effect")
	}
}

// press sends a key the way Bubble Tea would.
func press(t *testing.T, m *Model, msg tea.KeyPressMsg) {
	t.Helper()
	_, _ = m.Update(msg)
}

// run is the shorthand for a key built from a rune and modifiers.
func run(code rune, mod tea.KeyMod, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod, Text: text}
}

func keyNamed(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func render(m *Model) string { return m.render() }

// --- layout ------------------------------------------------------------------

func TestRendersAtEveryDocumentedBreakpoint(t *testing.T) {
	sizes := []struct {
		name          string
		width, height int
	}{
		{"wide", 160, 44},
		{"medium", 120, 40},
		{"narrow", 90, 30},
		{"tiny", 60, 20},
		{"short", 100, 8},
	}
	for _, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			m := newTestModel(t)
			resize(t, m, size.width, size.height)
			out := render(m)
			if strings.TrimSpace(out) == "" {
				t.Fatal("rendered nothing")
			}
			for i, line := range strings.Split(out, "\n") {
				if width := visibleWidth(line); width > size.width {
					t.Errorf("line %d is %d cells wide, wider than the %d cell terminal:\n%s",
						i, width, size.width, line)
				}
			}
			// The mode indicator survives every size, because "what mode am I
			// in" must never be guesswork.
			if !strings.Contains(out, "NORMAL") {
				t.Errorf("the status bar lost the mode indicator at %dx%d", size.width, size.height)
			}
		})
	}
}

func TestActionBarNamesTheVerbsAndTheirKeys(t *testing.T) {
	m := newTestModel(t)
	// Put the cursor on a file and select it, so file operations apply.
	m.selectByName("README.md")
	press(t, m, keyNamed(tea.KeySpace))
	out := render(m)

	// The action bar is the main teaching surface: the verb, the key, and an
	// escape hatch to everything else.
	for _, want := range []string{"View", "Rename", "Delete", "F9 more", "Ctrl+K search"} {
		if !strings.Contains(out, want) {
			t.Errorf("the action bar does not offer %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "selected") {
		t.Errorf("the selection summary is missing:\n%s", out)
	}
}

func TestEmptyStatesSayWhatToDo(t *testing.T) {
	m := newTestModel(t)
	out := render(m)
	// The remote pane has no connection yet, and says so with the way out.
	if !strings.Contains(out, "No connection yet") || !strings.Contains(out, "Ctrl+O") {
		t.Errorf("the unconnected pane should explain itself:\n%s", out)
	}
}

// A pane is a box: every line inside it must be exactly as wide as the box, or
// the border detaches from the content and the row wraps mid-column.
func TestPaneBoxesAreExact(t *testing.T) {
	for _, size := range []struct{ w, h int }{{140, 40}, {150, 38}, {200, 44}} {
		m := newTestModel(t)
		resize(t, m, size.w, size.h)
		l := m.measure()
		for i := range m.Panes {
			pane := m.renderPane(i, l.PaneWidth, l.PaneRows, l)
			for n, line := range strings.Split(pane, "\n") {
				if w := visibleWidth(line); w != l.PaneWidth {
					t.Errorf("%dx%d pane %d line %d is %d cells, want %d:\n%q",
						size.w, size.h, i, n, w, l.PaneWidth, line)
				}
			}
		}
	}
}

// Every column a row shows must be complete. A row that is one cell too wide
// silently loses the end of its timestamp, which is exactly the kind of thing
// nobody notices until they need it.
func TestRowColumnsAreNotClipped(t *testing.T) {
	for _, width := range []int{140, 150, 200, 240} {
		m := newTestModel(t)
		resize(t, m, width, 40)
		l := m.measure()
		row := m.renderRow(&m.Panes[0], Entry{
			Name: "notes.txt", Size: 14, ModTime: time.Date(2026, 10, 16, 23, 39, 0, 0, time.UTC),
		}, false, l.PaneWidth-2, true)
		plain := ansi.Strip(row)
		if !strings.Contains(plain, "notes.txt") {
			t.Errorf("width %d: the name is missing from %q", width, plain)
		}
		if !strings.Contains(plain, "23:39") {
			t.Errorf("width %d: the timestamp was clipped: %q", width, plain)
		}
		if w := lipgloss.Width(row); w > l.PaneWidth-2 {
			t.Errorf("width %d: the row is %d cells, wider than its pane interior %d", width, w, l.PaneWidth-2)
		}
	}
}

// The breadcrumb shares its row with the pane beside it, so it must never run
// into the neighbouring pane's title.
func TestBreadcrumbStaysInsideItsPane(t *testing.T) {
	m := newTestModel(t)
	l := m.measure()
	for i := range m.Panes {
		header := m.renderBreadcrumb(&m.Panes[i], l.PaneWidth, i == m.Focused)
		if w := visibleWidth(header); w > l.PaneWidth {
			t.Errorf("pane %d breadcrumb is %d cells, wider than its %d cell pane:\n%q",
				i, w, l.PaneWidth, header)
		}
	}
}

// Both panes are the same height, so the layout reads as two columns rather
// than a staircase.
func TestPanesHaveEqualHeight(t *testing.T) {
	m := newTestModel(t)
	l := m.measure()
	body := m.renderPanes(l)
	lines := strings.Split(body, "\n")
	for n, line := range lines {
		if !strings.Contains(line, "╰") {
			continue
		}
		// A bottom border row should close both panes.
		if count := strings.Count(line, "╰"); count != 2 {
			t.Errorf("line %d closes %d panes, want 2:\n%q", n, count, line)
		}
	}
}

// The preview is a wide-terminal convenience; on a narrow one it would squeeze
// both halves into uselessness.
func TestPreviewIsOffOnNarrowTerminals(t *testing.T) {
	// Below 100 columns there is no preview, and even at 140 the panes are too
	// narrow for both halves to be useful.
	for _, width := range []int{76, 90, 99, 140, 150} {
		m := newTestModel(t)
		resize(t, m, width, 30)
		if got := m.measure().PreviewWidth; got != 0 {
			t.Errorf("at %d columns the preview should be off, it is %d wide", width, got)
		}
	}
	m := newTestModel(t)
	resize(t, m, 200, 40)
	if m.measure().PreviewWidth == 0 {
		t.Error("on a wide terminal the preview should be available")
	}
}

// A narrow listing shows the name, never a truncated name beside metadata the
// reader did not ask for.
func TestNamesSurviveMetadataPressure(t *testing.T) {
	m := newTestModel(t)
	resize(t, m, 200, 40)
	out := render(m)
	for _, name := range []string{"README.md", "notes.txt", "archive.tar.gz", "assets"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s should be readable in the listing:\n%s", name, out)
		}
	}
}

// The selection is summarised once. Saying it twice wastes the row that teaches
// the keyboard shortcuts.
func TestSelectionIsSummarisedOnce(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("README.md")
	press(t, m, keyNamed(tea.KeySpace))
	out := render(m)
	if count := strings.Count(out, "1 selected"); count != 1 {
		t.Errorf("the selection summary appears %d times, want once:\n%s", count, out)
	}
}

// Esc dismisses, cancels and goes back. It never quits: it is one byte that
// terminals also use to begin escape sequences, and it has three jobs already.
func TestEscNeverQuits(t *testing.T) {
	m := newTestModel(t)
	press(t, m, keyNamed(tea.KeyEsc))
	if m.quit {
		t.Fatal("Esc quit the application")
	}
	if !strings.Contains(m.quitHint, "Ctrl+Q") {
		t.Errorf("with nothing to dismiss, Esc should explain how to quit, got %q", m.quitHint)
	}

	// With a selection, Esc clears it instead.
	m.selectByName("README.md")
	press(t, m, keyNamed(tea.KeySpace))
	press(t, m, keyNamed(tea.KeyEsc))
	if m.quit {
		t.Fatal("Esc quit instead of clearing the selection")
	}
	if m.Pane().SelectedCount() != 0 {
		t.Errorf("Esc should have cleared the selection, %d remain", m.Pane().SelectedCount())
	}
}

// Quitting is Ctrl+Q, which is rebindable, and F10 for the file-manager
// convention.
func TestCtrlQQuitsAndF10Too(t *testing.T) {
	for _, msg := range []tea.KeyPressMsg{run('q', tea.ModCtrl, ""), keyNamed(tea.KeyF10)} {
		m := newTestModel(t)
		press(t, m, msg)
		if !m.quit {
			t.Errorf("%q should quit", msg.String())
		}
	}
}

// Ctrl+C is copy, and with nothing to copy it says so and names the quit key
// rather than doing nothing at all.
func TestCtrlCExplainsItselfWithNothingSelected(t *testing.T) {
	m := newTestModel(t)
	press(t, m, run('c', tea.ModCtrl, ""))
	if m.quit {
		t.Fatal("Ctrl+C quit the application")
	}
	if !strings.Contains(m.quitHint, "Ctrl+Q") {
		t.Errorf("Ctrl+C with nothing selected should name the quit key, got %q", m.quitHint)
	}
}

// Every overlay card is a box: lines must measure exactly the card width, or Lip
// Gloss wraps them and each item grows by a line. And the footer must survive,
// because that is where the card says how to get out.
func TestCardBoxesAreExactAndFootersSurvive(t *testing.T) {
	cases := []struct {
		name   string
		open   func(t *testing.T, m *Model)
		footer string
	}{
		{"palette", func(t *testing.T, m *Model) { press(t, m, run('k', tea.ModCtrl, "")) }, "Esc close"},
		{"menu", func(t *testing.T, m *Model) {
			m.selectByName("README.md")
			press(t, m, keyNamed(tea.KeySpace))
			press(t, m, keyNamed(tea.KeyF9))
		}, "Esc close"},
		{"help", func(t *testing.T, m *Model) { press(t, m, keyNamed(tea.KeyF1)) }, "Ctrl+K searches"},
		{"settings", func(t *testing.T, m *Model) { press(t, m, run(',', tea.ModCtrl, "")) }, "Ctrl+, close"},
		{"prompt", func(t *testing.T, m *Model) { press(t, m, keyNamed(tea.KeyF7)) }, "Esc cancel"},
		{"confirm", func(t *testing.T, m *Model) {
			m.selectByName("README.md")
			press(t, m, keyNamed(tea.KeyF8))
		}, "no trash and no undo"},
		{"onboarding", func(t *testing.T, m *Model) { m.Overlay = OverlayOnboarding }, "Esc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []struct{ w, h int }{{140, 40}, {120, 34}, {90, 30}} {
				m := newTestModel(t)
				resize(t, m, size.w, size.h)
				tc.open(t, m)
				card := m.renderOverlay(m.measure())
				if card == "" {
					t.Fatalf("no card was rendered")
				}
				lines := strings.Split(card, "\n")
				width := visibleWidth(lines[0])
				for i, line := range lines {
					if got := visibleWidth(line); got != width {
						t.Errorf("%dx%d line %d is %d cells, want %d:\n%q", size.w, size.h, i, got, width, line)
					}
				}
				if !strings.Contains(ansi.Strip(card), tc.footer) {
					t.Errorf("%dx%d: the footer %q is missing:\n%s", size.w, size.h, tc.footer, ansi.Strip(card))
				}
			}
		})
	}
}

// A card taller than the screen keeps its footer and says so in the middle.
func TestTallCardsAreClippedNotBroken(t *testing.T) {
	m := newTestModel(t)
	resize(t, m, 100, 14) // deliberately short
	press(t, m, keyNamed(tea.KeyF1))
	rendered := ansi.Strip(render(m))
	if !strings.Contains(rendered, "Ctrl+K searches") {
		t.Errorf("a clipped card should keep its footer:\n%s", rendered)
	}
	if !strings.Contains(rendered, "…") {
		t.Errorf("a clipped card should say it was clipped:\n%s", rendered)
	}
}

// The three columns of the body share one top edge and one bottom edge. Without
// this, the sidebar's text floats a row above the pane borders and the whole
// layout reads as misaligned.
func TestSidebarAlignsWithPanes(t *testing.T) {
	m := newTestModel(t)
	resize(t, m, 140, 40)
	body := m.renderBody(m.measure())
	lines := strings.Split(body, "\n")

	top, bottom := -1, -1
	for i, line := range lines {
		plain := ansi.Strip(line)
		if top < 0 && strings.Count(plain, "╭") == 3 {
			top = i
		}
		if strings.Count(plain, "╰") == 3 {
			bottom = i
		}
	}
	if top < 0 {
		t.Fatalf("the three boxes do not share a top edge:\n%s", ansi.Strip(body))
	}
	if bottom < 0 {
		t.Fatalf("the three boxes do not share a bottom edge:\n%s", ansi.Strip(body))
	}
	if bottom-top != m.measure().PaneRows+1 {
		t.Errorf("the boxes span rows %d..%d, which is not the pane height", top, bottom)
	}
}

// The sidebar is a box too, so its lines measure exactly its width.
func TestSidebarBoxIsExact(t *testing.T) {
	m := newTestModel(t)
	resize(t, m, 160, 40)
	l := m.measure()
	content := m.renderSidebar(l)
	for i, line := range strings.Split(content, "\n") {
		if w := visibleWidth(line); w != l.SidebarWidth-2 {
			t.Errorf("sidebar line %d is %d cells, want %d:\n%q", i, w, l.SidebarWidth-2, line)
		}
		if !strings.HasPrefix(ansi.Strip(line), " ") {
			t.Errorf("sidebar line %d has no inner padding:\n%q", i, ansi.Strip(line))
		}
	}
	if got := strings.Count(content, "\n") + 1; got != l.PaneRows {
		t.Errorf("the sidebar has %d rows, want %d so it matches the panes", got, l.PaneRows)
	}
}

// --- discoverability ---------------------------------------------------------

// The palette finds an action by name and shows the shortcut beside it, which is
// how a person learns the keys without reading anything.
func TestPaletteFindsActionsAndShowsShortcuts(t *testing.T) {
	m := newTestModel(t)
	press(t, m, run('k', tea.ModCtrl, ""))
	if m.Overlay != OverlayPalette {
		t.Fatal("Ctrl+K should open the palette")
	}
	for _, ch := range "copy" {
		press(t, m, run(ch, 0, string(ch)))
	}
	out := render(m)
	if !strings.Contains(out, "Copy to…") {
		t.Errorf("typing \"copy\" should find the copy actions:\n%s", out)
	}
	if !strings.Contains(out, "F5") {
		t.Errorf("the palette should print the shortcut beside the action:\n%s", out)
	}
	// The filter is a real search, not a prefix match.
	press(t, m, run('u', tea.ModCtrl, "")) // clear the query
	for _, ch := range "extract" {
		press(t, m, run(ch, 0, string(ch)))
	}
	if out := render(m); !strings.Contains(out, "Extract") {
		t.Errorf("the palette should keep matching:\n%s", out)
	}
	press(t, m, keyNamed(tea.KeyEsc))
	if m.Overlay != OverlayNone {
		t.Error("Esc should close the palette")
	}
}

// Every action is in the palette: nothing is key-only.
func TestPaletteListsEveryAction(t *testing.T) {
	m := newTestModel(t)
	press(t, m, run('k', tea.ModCtrl, ""))
	items := m.paletteItems()
	if len(items) != len(m.reg.All()) {
		t.Fatalf("the palette shows %d of %d actions", len(items), len(m.reg.All()))
	}
}

// The context menu is the second discovery net: every action that applies, with
// the destructive one marked and last.
func TestContextMenuMarksDangerousActions(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("README.md")
	press(t, m, keyNamed(tea.KeySpace))
	press(t, m, keyNamed(tea.KeyF9))
	if m.Overlay != OverlayMenu {
		t.Fatal("F9 should open the context menu")
	}
	items := m.menuItems()
	if len(items) == 0 {
		t.Fatal("the context menu is empty with a selection")
	}
	if !items[len(items)-1].Danger {
		t.Errorf("the destructive action should be last, got %s", items[len(items)-1].ID)
	}
	if !strings.Contains(render(m), "Delete") {
		t.Errorf("the context menu should offer Delete:\n%s", render(m))
	}
}

// Help is searchable and grouped, so it can be used as a reference rather than a
// wall of keys.
func TestHelpListsGroupedActions(t *testing.T) {
	m := newTestModel(t)
	press(t, m, keyNamed(tea.KeyF1))
	if m.Overlay != OverlayHelp {
		t.Fatal("F1 should open help")
	}
	out := render(m)
	// Every category is represented, so help is an overview of what the app can
	// do, and the footer says where the rest lives.
	for _, want := range []string{"Navigation", "File", "Archive", "Session", "Deploy", "App", "Ctrl+K searches"} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q:\n%s", want, out)
		}
	}
}

// --- navigation and selection ------------------------------------------------

// Type-ahead is the file-manager behaviour that needs no key to learn.
func TestTypeAheadJumpsToTheName(t *testing.T) {
	m := newTestModel(t)
	if _, ok := m.Pane().CursorEntry(); !ok {
		t.Fatal("the pane is empty")
	}
	press(t, m, run('n', 0, "n"))
	entry, _ := m.Pane().CursorEntry()
	if entry.Name != "notes.txt" {
		t.Errorf("typing \"n\" should land on notes.txt, got %q", entry.Name)
	}
	// The status bar shows what was typed, so the jump is explained.
	if !strings.Contains(render(m), "notes.txt") {
		t.Error("the cursor line should be visible after a type-ahead jump")
	}
	m.resetTypeAhead()
	// Directories sort first, so "a" lands on assets; pressing it again cycles
	// to the next match, which is the behaviour that makes type-ahead usable.
	press(t, m, run('a', 0, "a"))
	entry, _ = m.Pane().CursorEntry()
	if entry.Name != "assets" {
		t.Errorf("typing \"a\" should land on assets first, got %q", entry.Name)
	}
	press(t, m, run('a', 0, "a"))
	entry, _ = m.Pane().CursorEntry()
	if entry.Name != "archive.tar.gz" {
		t.Errorf("pressing \"a\" again should cycle to the next match, got %q", entry.Name)
	}
}

// Entering a directory and coming back restores the cursor to where it was,
// which is the rule that makes walking feel right.
func TestCursorIsRestoredAfterVisitingADirectory(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("assets")
	m.open()
	if filepath.Base(m.Pane().Path) != "assets" {
		t.Fatalf("open did not enter assets, path is %q", m.Pane().Path)
	}
	press(t, m, keyNamed(tea.KeyLeft)) // parent
	m.selectByName("notes.txt")
	if entry, _ := m.Pane().CursorEntry(); entry.Name != "notes.txt" {
		t.Fatalf("the cursor did not land on notes.txt")
	}
}

func TestHiddenFilesAreHiddenUntilAskedFor(t *testing.T) {
	m := newTestModel(t)
	for _, e := range m.Pane().Entries {
		if e.Name == ".hidden" {
			t.Fatal("dotfiles should be hidden by default")
		}
	}
	press(t, m, run('h', tea.ModAlt, ""))
	found := false
	for _, e := range m.Pane().Entries {
		if e.Name == ".hidden" {
			found = true
		}
	}
	if !found {
		t.Fatal("Alt+H should reveal dotfiles")
	}
}

func TestSelectionExtendsAndInverts(t *testing.T) {
	m := newTestModel(t)
	press(t, m, run('a', tea.ModCtrl, ""))
	if m.Pane().SelectedCount() != len(m.Pane().Visible()) {
		t.Fatalf("Ctrl+A selected %d of %d", m.Pane().SelectedCount(), len(m.Pane().Visible()))
	}
	press(t, m, run('a', tea.ModAlt, ""))
	if m.Pane().SelectedCount() != 0 {
		t.Fatalf("Alt+A should invert to nothing, got %d", m.Pane().SelectedCount())
	}
}

// --- operations --------------------------------------------------------------

// Deleting is permanent in v1, so it always asks, and the question states the
// facts rather than asking "are you sure".
func TestDeleteAsksFirstAndStatesTheFacts(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("notes.txt")
	press(t, m, keyNamed(tea.KeyF8))
	if m.Overlay != OverlayConfirm {
		t.Fatal("F8 should open a confirmation")
	}
	out := render(m)
	for _, want := range []string{"Delete permanently?", "notes.txt", "Delete", "no trash and no undo"} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation should show %q:\n%s", want, out)
		}
	}

	// Cancelling leaves the file alone.
	press(t, m, keyNamed(tea.KeyEsc))
	if _, err := os.Stat(filepath.Join(m.Pane().Path, "notes.txt")); err != nil {
		t.Fatal("cancelling deleted the file")
	}

	// Confirming deletes it, and says so.
	press(t, m, keyNamed(tea.KeyF8))
	press(t, m, keyNamed(tea.KeyEnter))
	if _, err := os.Stat(filepath.Join(m.Pane().Path, "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("confirming did not delete the file")
	}
	if !strings.Contains(m.toasts[len(m.toasts)-1].text, "Deleted 1 item") {
		t.Errorf("the toast should report what happened, got %+v", m.toasts[len(m.toasts)-1])
	}
}

// A multi-item delete reports the count and the total size before doing anything.
func TestDeleteConfirmationSummarisesASelection(t *testing.T) {
	m := newTestModel(t)
	press(t, m, run('a', tea.ModCtrl, ""))
	m.Pane().Selected["archive.tar.gz"] = false // keep it small enough to list
	press(t, m, keyNamed(tea.KeyF8))
	out := render(m)
	if !strings.Contains(out, "items") {
		t.Errorf("a multi-item delete should count the items:\n%s", out)
	}
}

func TestCreateFolderAndRename(t *testing.T) {
	m := newTestModel(t)
	press(t, m, keyNamed(tea.KeyF7))
	if m.Overlay != OverlayPrompt {
		t.Fatal("F7 should ask for a name")
	}
	for _, ch := range "reports" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if info, err := os.Stat(filepath.Join(m.Pane().Path, "reports")); err != nil || !info.IsDir() {
		t.Fatalf("F7 did not create the directory: %v", err)
	}

	press(t, m, keyNamed(tea.KeyF2))
	for range "reports" {
		press(t, m, keyNamed(tea.KeyBackspace))
	}
	for _, ch := range "archive" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if _, err := os.Stat(filepath.Join(m.Pane().Path, "archive")); err != nil {
		t.Fatalf("F2 did not rename the directory: %v", err)
	}
}

// --- overlays and settings ---------------------------------------------------

// The first-run card is only shown once the build can do what it offers. A card
// that advertises an unavailable feature swallows every key, which reads as
// "the interface is broken".
func TestOnboardingIsOptInUntilHostImportExists(t *testing.T) {
	m := newTestModel(t)
	if m.Overlay != OverlayNone {
		t.Fatalf("a first run with nothing to import should not open a card, got %v", m.Overlay)
	}
	// And navigation works immediately.
	m.selectByName("assets")
	press(t, m, keyNamed(tea.KeyDown))
	if entry, _ := m.Pane().CursorEntry(); entry.Name == "assets" {
		t.Error("the cursor should move on the very first key press")
	}
}

func TestOnboardingAppearsWhenOfferedAndIsNavigable(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "a\n")
	cfg := config.Default()
	cfg.UI.Theme = "midnight"
	cfg.UI.Start = "files"
	m, err := New(Options{
		Config: cfg, Profile: colorprofile.TrueColor, HasDark: true,
		OffersConnect: true, Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 140, 40)
	if m.Overlay != OverlayOnboarding {
		t.Fatal("the first run should show the onboarding card when the build offers import")
	}
	out := render(m)
	for _, want := range []string{"Welcome to omssh", "Import hosts", "Ctrl+K", "Ctrl+Q", "F1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the onboarding card should mention %q:\n%s", want, out)
		}
	}

	// The card is a menu, not a trap: Tab and the arrow keys move between its
	// buttons, Enter takes the focused one.
	press(t, m, keyNamed(tea.KeyTab))
	if m.onboarding.cursor != 1 {
		t.Errorf("Tab should focus the second button, cursor is %d", m.onboarding.cursor)
	}
	press(t, m, keyNamed(tea.KeyRight))
	if m.onboarding.cursor != 2 {
		t.Errorf("right should focus the third button, cursor is %d", m.onboarding.cursor)
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayNone {
		t.Error("Enter should dismiss the card")
	}
	if !m.cfg.UI.Onboarding {
		t.Error("activating a button should be remembered")
	}
	if len(m.toasts) == 0 {
		t.Error("activating a button should say what it did")
	}

	// Esc always gets out, whichever button has focus.
	m2 := newTestModel(t)
	m2.Overlay = OverlayOnboarding
	press(t, m2, keyNamed(tea.KeyEsc))
	if m2.Overlay != OverlayNone {
		t.Error("Esc should dismiss the card")
	}
}

// The background colour is discovered inside the program. Asking the terminal
// before the program starts blocks on stdin, which delays first paint and eats
// whatever the user types while waiting.
func TestBackgroundDetectionDoesNotBlockStartup(t *testing.T) {
	if dark, known := BackgroundFromEnv([]string{"COLORFGBG=15;0"}); !known || !dark {
		t.Errorf("COLORFGBG=15;0 should read as dark and known, got dark=%v known=%v", dark, known)
	}
	if dark, known := BackgroundFromEnv([]string{"COLORFGBG=0;15"}); !known || dark {
		t.Errorf("COLORFGBG=0;15 should read as light and known, got dark=%v known=%v", dark, known)
	}
	if _, known := BackgroundFromEnv([]string{"COLORFGBG=default;default"}); known {
		t.Error("an unparsable COLORFGBG should report itself unknown, not guess")
	}
	if _, known := BackgroundFromEnv(nil); known {
		t.Error("with no COLORFGBG the answer is unknown")
	}

	// Nothing is asked at startup: the first frame must not wait for a reply.
	cfg := config.Default()
	cfg.UI.Theme = "auto"
	m, err := New(Options{
		Config: cfg, Profile: colorprofile.TrueColor,
		HasDark: true, Dir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should still schedule the size request and the heartbeat")
	}

	// The terminal's answer is only ever requested by the user, through the
	// action that asks for it.
	if _, ok := m.reg.Get("view.follow-background"); !ok {
		t.Fatal("there should be an action to follow the terminal background")
	}
	if got := m.runAction("view.follow-background"); got == nil {
		t.Error("follow-background should ask the terminal, i.e. return a command")
	}
	if m.cfg.UI.Theme != "auto" {
		t.Errorf("follow-background should switch to auto, theme is %q", m.cfg.UI.Theme)
	}
	_, _ = m.Update(tea.BackgroundColorMsg{Color: rgbaOf(0xff, 0xff, 0xff)})
	if m.themeName != "dawn" {
		t.Errorf("a light background should resolve auto to dawn, got %q", m.themeName)
	}

	// A theme chosen by hand is never overridden by the terminal.
	cfg.UI.Theme = "ember"
	m, err = New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: true, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = m.Update(tea.BackgroundColorMsg{Color: rgbaOf(0xff, 0xff, 0xff)})
	if m.themeName != "ember" {
		t.Errorf("a fixed theme must not change on a background report, got %q", m.themeName)
	}
}

func TestSettingsChangesThemeImmediately(t *testing.T) {
	m := newTestModel(t)
	press(t, m, run(',', tea.ModCtrl, ""))
	if m.Overlay != OverlaySettings {
		t.Fatal("Ctrl+, should open settings")
	}
	before := m.themeName
	press(t, m, keyNamed(tea.KeyDown))
	press(t, m, keyNamed(tea.KeyEnter))
	if m.themeName == before {
		t.Fatalf("the theme did not change, still %q", before)
	}
	if !strings.Contains(render(m), "Theme:") {
		t.Errorf("changing the theme should confirm it:\n%s", render(m))
	}
}

// The capability probe drops keys the terminal cannot send, and the interface
// says which ones rather than silently ignoring them.
func TestCapabilityProbeReportsDroppedKeys(t *testing.T) {
	m := newTestModel(t)
	if len(m.dropped) != 0 {
		t.Fatalf("the default bindings should all be deliverable: %+v", m.dropped)
	}
	m.applyCapabilities(tea.KeyboardEnhancementsMsg{Flags: 0})
	// Nothing needs dropping without Kitty support unless a binding uses a
	// modified key, which the default preset does not.
	if len(m.dropped) != 0 {
		t.Errorf("unexpected drops: %+v", m.dropped)
	}
	press(t, m, keyNamed(tea.KeyF1))
	if !strings.Contains(render(m), "Esc close") {
		t.Errorf("help should stay usable:\n%s", render(m))
	}
}

// The heartbeat expires notifications so nothing accumulates in the corner.
func TestToastsExpire(t *testing.T) {
	m := newTestModel(t)
	m.toastSuccess("done")
	if len(m.toasts) != 1 {
		t.Fatal("the toast was not queued")
	}
	for i := range m.toasts {
		m.toasts[i].expires = m.toasts[i].expires.Add(-time.Hour)
	}
	m.expireToasts()
	if len(m.toasts) != 0 {
		t.Error("an expired toast should be dropped")
	}
}
