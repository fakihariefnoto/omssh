package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/actions"
)

// Side says which machine a pane is looking at.
type Side int

// Pane sides. Left is local by default and right is remote, so the direction of
// every operation is obvious from the layout.
const (
	SideLocal Side = iota
	SideRemote
)

// ConnState is the health of a session, shown as a coloured dot on its tab.
type ConnState int

// Connection states.
const (
	StateIdle ConnState = iota
	StateConnecting
	StateReady
	StateBusy
	StateError
)

// Label returns a word for the state, so the interface never relies on colour
// alone.
func (s ConnState) Label() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateReady:
		return "ready"
	case StateBusy:
		return "busy"
	case StateError:
		return "error"
	default:
		return "idle"
	}
}

// Tab is one session: a host, or the local machine.
type Tab struct {
	Title string
	Host  string
	State ConnState
}

// Entry is one row in a pane.
type Entry struct {
	Name    string
	Dir     bool
	Symlink bool
	Link    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
}

// Pane is one directory listing, local or remote.
type Pane struct {
	Side    Side
	Host    string
	Path    string
	Entries []Entry
	Cursor  int
	// Selected holds the names of selected entries, so a selection survives
	// navigating away and back.
	Selected map[string]bool
	// History is the visited-directory stack, with index for back and forward.
	History []string
	HIndex  int
	// LastDir is where "-" goes.
	LastDir string
	// Err holds a listing failure, which is shown as an explanatory row rather
	// than an error screen.
	Err error
	// Loading is true while an asynchronous listing is in flight.
	Loading bool
	// Filter is the incremental filter applied to the listing.
	Filter string
	// Offset is the first visible row, for scrolling.
	Offset int
}

// NewPane returns a pane rooted at dir.
func NewPane(side Side, dir string) Pane {
	return Pane{
		Side:     side,
		Path:     dir,
		Selected: map[string]bool{},
		History:  []string{dir},
		HIndex:   0,
	}
}

// NewRemotePane returns an unconnected remote pane.
func NewRemotePane() Pane {
	return Pane{
		Side:     SideRemote,
		Selected: map[string]bool{},
		History:  nil,
	}
}

// Connected reports whether the pane has somewhere to look.
func (p Pane) Connected() bool {
	return p.Side == SideLocal || p.Host != ""
}

// CursorEntry returns the entry under the cursor.
func (p Pane) CursorEntry() (Entry, bool) {
	if p.Cursor < 0 || p.Cursor >= len(p.Entries) {
		return Entry{}, false
	}
	return p.Entries[p.Cursor], true
}

// Selection returns the names selected in the pane. The cursor counts as
// selected for operations that act on "the selection if there is one, the cursor
// otherwise", which is how the interface stays usable one-handed.
func (p Pane) Selection() []string {
	out := make([]string, 0, len(p.Selected))
	for name := range p.Selected {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// SelectedCount is how many entries are explicitly selected.
func (p Pane) SelectedCount() int { return len(p.Selected) }

// SelectionBytes totals the size of the selection.
func (p Pane) SelectionBytes() int64 {
	var total int64
	for _, e := range p.Entries {
		if p.Selected[e.Name] {
			total += e.Size
		}
	}
	return total
}

// Visible returns the entries matching the filter.
func (p Pane) Visible() []Entry {
	if p.Filter == "" {
		return p.Entries
	}
	needle := strings.ToLower(p.Filter)
	out := make([]Entry, 0, len(p.Entries))
	for _, e := range p.Entries {
		if strings.Contains(strings.ToLower(e.Name), needle) {
			out = append(out, e)
		}
	}
	return out
}

// Load reads the local directory. Remote panes are loaded over SFTP from M2.
func (p *Pane) Load(hidden bool) {
	if p.Side != SideLocal {
		return
	}
	if p.Path == "" {
		p.Path = "."
	}
	entries, err := readDir(p.Path)
	p.Err = err
	if err != nil {
		p.Entries = nil
		return
	}
	visible := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if !hidden && strings.HasPrefix(e.Name, ".") {
			continue
		}
		visible = append(visible, e)
	}
	sortEntries(visible)
	p.Entries = visible
	if p.Cursor >= len(p.Entries) {
		p.Cursor = max(0, len(p.Entries)-1)
	}
}

func readDir(dir string) ([]Entry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			// An entry we cannot stat is still worth showing: it explains itself
			// on open instead of disappearing from the listing.
			out = append(out, Entry{Name: item.Name(), Dir: item.IsDir()})
			continue
		}
		e := Entry{
			Name:    item.Name(),
			Dir:     item.IsDir(),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
		}
		if info.Mode()&os.ModeSymlink != 0 {
			e.Symlink = true
			if target, err := os.Readlink(filepath.Join(dir, item.Name())); err == nil {
				e.Link = target
			}
			if resolved, err := os.Stat(filepath.Join(dir, item.Name())); err == nil {
				e.Dir = resolved.IsDir()
				e.Size = resolved.Size()
				e.ModTime = resolved.ModTime()
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// sortEntries puts directories first and then sorts by name, case-insensitively,
// which is what every file manager does and therefore what nobody has to learn.
func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

// Overlay identifies the modal layer on top of the panes, if any.
type Overlay int

// Overlays.
const (
	OverlayNone Overlay = iota
	OverlayPalette
	OverlayMenu
	OverlayHelp
	OverlayOnboarding
	OverlaySettings
	OverlayConfirm
	OverlayPrompt
	OverlayForm
)

// Name returns the identifier the action registry expects in Context.Overlay.
func (o Overlay) Name() string {
	switch o {
	case OverlayPalette:
		return "palette"
	case OverlayMenu:
		return "menu"
	case OverlayHelp:
		return "help"
	case OverlayOnboarding:
		return "onboarding"
	case OverlaySettings:
		return "settings"
	case OverlayConfirm, OverlayPrompt, OverlayForm:
		return "dialog"
	default:
		return ""
	}
}

// actionContext builds the state the registry uses to decide what applies.
func (m *Model) actionContext() actions.Context {
	ctx := actions.Context{
		Screen:   m.screenName(),
		Focused:  m.Focused,
		Panes:    len(m.Panes),
		Overlay:  m.Overlay.Name(),
		Jobs:     m.Jobs(),
		Staged:   len(m.staged.Paths),
		Terminal: false,
	}
	if ctx.Panes == 0 {
		return ctx
	}
	pane := m.Pane()
	ctx.Local = pane.Side == SideLocal
	ctx.Connected = pane.Connected()
	ctx.Selection = pane.SelectedCount()
	if e, ok := pane.CursorEntry(); ok {
		ctx.Cursor = true
		ctx.CursorIsDir = e.Dir
		ctx.CursorIsArchive = IsArchive(e.Name)
	}
	if m.screen == screenConnections {
		ctx.Connected = false
		if _, ok := m.currentConnection(); ok {
			ctx.Cursor = true
		}
		ctx.Selection = 0
	}
	ctx.Back = m.Overlay != OverlayNone || pane.SelectedCount() > 0 || pane.Filter != ""
	return ctx
}
