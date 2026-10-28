package tui

import (
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/actions"
	"github.com/fakihariefnoto/omssh/internal/vfs"
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
	Mode    fs.FileMode
	ModTime time.Time
}

// Pane is one directory listing, over whatever filesystem it was given. The same
// code shows a local directory and a remote one, because both are vfs.FS
// (ADR 0001).
type Pane struct {
	Side Side
	Host string
	// FS is what this pane is looking at. Nil means there is nothing to look at
	// yet — an unconnected remote — which the interface says plainly rather than
	// showing an empty directory.
	FS vfs.FS
	// Home is where Ctrl+Home and the "go home" action land: the user's home
	// locally, the session's root remotely.
	Home string
	// Base names the remote directory "/" means, for the breadcrumb: a pane path
	// is always inside its filesystem, and people think in server paths.
	Base    string
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
	// pendingCursor is the entry to put the cursor back on when an
	// asynchronous listing arrives, so refreshing a remote directory does not
	// lose the row you were looking at.
	pendingCursor string
	// Offset is the first visible row, for scrolling.
	Offset int
}

// NewPane returns a local pane over fsys, showing dir.
func NewPane(fsys vfs.FS, dir, home string) Pane {
	return Pane{
		Side:     SideLocal,
		FS:       fsys,
		Home:     home,
		Path:     dir,
		Selected: map[string]bool{},
		History:  []string{dir},
		HIndex:   0,
	}
}

// NewRemotePane returns a pane that is not connected to anything yet.
func NewRemotePane() Pane {
	return Pane{
		Side:     SideRemote,
		Selected: map[string]bool{},
	}
}

// Bind points the pane at a filesystem: what connecting a host does.
func (p *Pane) Bind(fsys vfs.FS, host, root string) {
	p.FS = fsys
	p.Host = host
	p.Home = root
	p.Path = root
	if p.Path == "" {
		p.Path = "/"
	}
	p.Entries = nil
	p.Cursor, p.Offset, p.Filter, p.Err = 0, 0, "", nil
	p.Selected = map[string]bool{}
	p.History = []string{p.Path}
	p.HIndex = 0
}

// Connected reports whether the pane has somewhere to look.
func (p Pane) Connected() bool { return p.FS != nil }

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

// Load lists the pane's directory over its filesystem.
func (p *Pane) Load(hidden bool) {
	if p.FS == nil {
		// Nothing to show, and nothing wrong: the interface draws the
		// not-connected state from this.
		p.Entries, p.Err = nil, nil
		return
	}
	if p.Path == "" {
		p.Path = "/"
	}
	entries, err := readDir(p.FS, p.Path)
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

// readDir turns a filesystem listing into entries.
//
// A symlink's target is deliberately not resolved here: over SFTP that is a round
// trip per row, and a directory of a thousand entries must not cost a thousand
// requests. The preview resolves it when the cursor lands on one.
func readDir(fsys vfs.FS, dir string) ([]Entry, error) {
	items, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			// An entry we cannot stat is still worth showing: it explains itself
			// when opened, instead of vanishing from the listing.
			out = append(out, Entry{Name: item.Name(), Dir: item.IsDir()})
			continue
		}
		out = append(out, Entry{
			Name:    item.Name(),
			Dir:     item.IsDir(),
			Symlink: info.Mode()&fs.ModeSymlink != 0,
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
		})
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
	OverlayHostKey
	OverlaySecret
	OverlayStaged
	OverlayDeploy
	OverlayPanes
	OverlayPaste
	OverlayAdd
	OverlayTransfers
	OverlayTerminal
	OverlayReplay
	OverlayTunnels
	OverlayMCP
	OverlaySessionChoose
	OverlayLocalChoose
	OverlayTunnelForm
	OverlayHistoryDetail
	OverlaySessionKind
	OverlayTunnelSaved
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
	case OverlayConfirm, OverlayPrompt, OverlayForm, OverlayHostKey, OverlaySecret, OverlayStaged:
		return "dialog"
	case OverlayDeploy:
		return "deploy"
	case OverlayPanes:
		return "panes"
	case OverlayPaste:
		return "paste"
	case OverlayAdd:
		return "add"
	case OverlayTransfers:
		return "transfers"
	case OverlayTerminal:
		return "terminal"
	case OverlayReplay:
		return "replay"
	case OverlayTunnels:
		return "tunnels"
	case OverlayMCP:
		return "mcp"
	case OverlaySessionChoose:
		return "choose"
	case OverlayLocalChoose:
		return "local"
	case OverlayTunnelForm:
		return "tunnel-form"
	case OverlayHistoryDetail:
		return "history-detail"
	case OverlaySessionKind:
		return "session-kind"
	case OverlayTunnelSaved:
		return "tunnel-saved"
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
