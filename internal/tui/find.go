package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Find in subtree walks the directory under the cursor and puts the first match
// in view. It is deliberately a jump rather than a results list: with four panes
// and a preview, a fifth list would be a place to get lost.
const findLimit = 5000

// findBelow asks for a name and searches for it.
func (m *Model) findBelow() tea.Cmd {
	entry, ok := m.Pane().CursorEntry()
	if !ok || !entry.Dir {
		m.quitHint = "Put the cursor on a folder to search inside it"
		return nil
	}
	m.prompt = promptState{
		Title:  "Find below " + entry.Name,
		Hint:   "Part of a name · Esc cancels",
		Action: "prompt.find",
		open:   true,
	}
	m.Overlay = OverlayPrompt
	return nil
}

// findResultMsg is the first match, or the reason there is none.
type findResultMsg struct {
	dir   string
	name  string
	path  string
	err   error
	count int
}

// searchBelow walks the pane's tree for a name containing the needle.
func (m *Model) searchBelow(needle string) tea.Cmd {
	pane := m.Pane()
	fsys, root := pane.FS, pane.Path
	needle = strings.ToLower(needle)
	if fsys == nil {
		return nil
	}
	m.mode = "SEARCHING"
	ctx := m.connectContext()

	return func() tea.Msg {
		found := findResultMsg{dir: root}
		visited := 0
		var walk func(dir string) bool
		walk = func(dir string) bool {
			if visited > findLimit || ctx.Err() != nil {
				return true
			}
			entries, err := fsys.ReadDir(dir)
			if err != nil {
				return false
			}
			for _, entry := range entries {
				visited++
				name := entry.Name()
				if strings.Contains(strings.ToLower(name), needle) {
					found.name, found.path, found.dir = name, fsys.Join(dir, name), dir
					return true
				}
				if entry.IsDir() {
					if walk(fsys.Join(dir, name)) {
						return true
					}
				}
			}
			return false
		}
		if !walk(root) {
			found.err = errNoMatch
		}
		found.count = visited
		return found
	}
}

type findError string

func (e findError) Error() string { return string(e) }

const errNoMatch = findError("no name matches")

// showFindResult jumps the pane to the match, selecting it.
func (m *Model) showFindResult(msg findResultMsg) tea.Cmd {
	m.mode = "NORMAL"
	if msg.err != nil {
		m.toastInfo("No match in this folder · " + strings.TrimSpace(msg.err.Error()))
		return nil
	}
	cmd := m.navigate(msg.dir)
	m.Panes[m.Focused].pendingCursor = msg.name
	return cmd
}
