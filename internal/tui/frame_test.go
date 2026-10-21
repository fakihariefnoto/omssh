package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// No view may hand fitFrame a line that is too wide: the frame is a safety net,
// not a habit. A wrapped line is what makes a terminal shift every row.
func TestNoViewOverflowsTheFrame(t *testing.T) {
	openers := map[string]func(t *testing.T, m *Model){
		"plain":       func(t *testing.T, m *Model) {},
		"marked":      func(t *testing.T, m *Model) { press(t, m, run('y', 0, "y")) },
		"palette":     func(t *testing.T, m *Model) { press(t, m, run('k', tea.ModCtrl, "")) },
		"help":        func(t *testing.T, m *Model) { press(t, m, run('?', 0, "?")) },
		"settings":    func(t *testing.T, m *Model) { press(t, m, run(',', tea.ModCtrl, "")) },
		"prompt":      func(t *testing.T, m *Model) { press(t, m, run('n', 0, "n")) },
		"tabs":        func(t *testing.T, m *Model) { m.focusTabs = true },
		"menu":        func(t *testing.T, m *Model) { press(t, m, run('m', 0, "m")) },
		"launcher":    func(t *testing.T, m *Model) { m.screen = screenHome },
		"connections": func(t *testing.T, m *Model) { m.screen = screenConnections },
		"preview":     func(t *testing.T, m *Model) { m.preview = true },
	}
	for name, open := range openers {
		for _, width := range []int{200, 160, 140, 120, 100, 80, 60, 44, 32} {
			for _, height := range []int{50, 40, 30, 24, 20, 12} {
				m := newTestModel(t)
				resize(t, m, width, height)
				m.selectByName("README.md")
				open(t, m)
				m.frameTrimmed = false
				render(m)
				if m.frameTrimmed {
					t.Errorf("%s at %dx%d: a view measured itself wider than the frame", name, width, height)
				}
			}
		}
	}
}

// The same, on a real tree: a long-line file, a folder preview, and the palette
// over it — the shape of "Ctrl+K broke the UI".
func TestPaletteOverRealContentStaysInside(t *testing.T) {
	fsys, err := vfs.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mustWrite(t, root+"/go.mod", "module example.com/x\n\nrequire (\n\tgithub.com/charmbracelet/x/ansi v0.11.9\n)\n")
	if err := fsys.MkdirAll("/internal/tui", 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root+"/internal/tui/view.go", strings.Repeat("a fairly long line of code that will be truncated\n", 5))

	m := newTestModel(t)
	m.Panes[0] = NewPane(fsys, "/internal/tui", root)
	m.Panes[0].Load(false)
	m.Focused = 0
	m.preview = true
	for _, width := range []int{220, 180, 140, 120, 100} {
		resize(t, m, width, 40)
		for _, name := range []string{"view.go", "."} {
			m.selectByName(name)
			if fetch := m.previewCmd(); fetch != nil {
				m.applyPreview(fetch().(previewReadyMsg))
			}
			press(t, m, run('k', tea.ModCtrl, ""))
			m.frameTrimmed = false
			render(m)
			if m.frameTrimmed {
				t.Errorf("at %d wide, %q plus the palette overflowed the frame", width, name)
			}
			m.Overlay = OverlayNone
		}
	}
}
