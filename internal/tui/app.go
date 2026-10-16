package tui

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/actions"
	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/keymap"
	"github.com/fakihariefnoto/omssh/internal/theme"
)

// Options configures a new interface.
type Options struct {
	Config   config.Config
	Profile  colorprofile.Profile
	HasDark  bool
	Registry *actions.Registry
	Keys     *keymap.Map
	// Dir is the directory the local pane starts in.
	Dir string
}

// Model is the whole interface state. There is one of these, and only Update
// changes it: rendering is a pure function of it.
type Model struct {
	cfg      config.Config
	st       theme.Styles
	icons    theme.Icons
	reg      *actions.Registry
	keys     *keymap.Map
	resolver *keymap.Resolver

	profile    colorprofile.Profile
	hasDark    bool
	themeName  string
	themeNames []string
	dropped    []keymap.Dropped

	width, height int
	started       bool

	tabs      []Tab
	activeTab int

	Panes   []Pane
	Focused int

	preview    bool
	sidebar    bool
	showHidden bool
	syncBrowse bool
	filtering  bool

	mode       string
	statusHint string
	quitHint   string

	typeAheadBuf  string
	typeAheadLast string

	Overlay  Overlay
	palette  paletteState
	menu     menuState
	help     helpState
	settings settingsState
	confirm  confirmState
	prompt   promptState

	staged stagedState

	toasts   []toast
	toastSeq int

	quit bool
}

type paletteState struct {
	input  input
	cursor int
}

type menuState struct{ cursor int }

type helpState struct {
	input input
}

type settingsState struct{ cursor int }

type confirmState struct {
	Title   string
	Body    []string
	Danger  bool
	Action  actions.ID
	Entries []Entry
	open    bool
}

type promptState struct {
	Title  string
	Hint   string
	Action actions.ID
	input  input
	open   bool
}

// stagedState is what Ctrl+C or Ctrl+X put on the "clipboard".
type stagedState struct {
	Paths []string
	Cut   bool
	From  int
}

// heartbeatMsg drives toast expiry and clears transient hints.
type heartbeatMsg struct{}

// heartbeat schedules the next tick. One recurring timer, so a burst of
// completions cannot leave timers behind.
func heartbeat() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return heartbeatMsg{} })
}

// New builds the interface. It never touches the network: panes are local until
// connections land in M1.
func New(opts Options) (*Model, error) {
	if opts.Registry == nil {
		opts.Registry = actions.Default()
	}
	cfg := opts.Config
	if cfg.Version == 0 {
		cfg = config.Default()
	}

	themeName := cfg.UI.Theme
	resolved, err := theme.Resolve(themeName, opts.Profile, opts.HasDark)
	if err != nil {
		return nil, err
	}

	paneKeys := opts.Keys
	if paneKeys == nil {
		paneKeys, err = keymap.Build(opts.Registry, keymap.Options{Preset: keymap.Preset(cfg.Keys.Preset)})
		if err != nil {
			return nil, err
		}
	}

	dir := opts.Dir
	if dir == "" {
		dir = "."
	}

	m := &Model{
		cfg:        cfg,
		st:         theme.NewStyles(resolved),
		icons:      theme.IconsFor(cfg.UI.Icons),
		reg:        opts.Registry,
		keys:       paneKeys,
		resolver:   paneKeys.Resolver(),
		profile:    opts.Profile,
		hasDark:    opts.HasDark,
		themeName:  resolved.Name,
		themeNames: theme.Names(),
		dropped:    paneKeys.Dropped(),
		tabs:       []Tab{{Title: "localhost", State: StateReady}},
		Panes:      []Pane{NewPane(SideLocal, dir), NewRemotePane()},
		Focused:    0,
		preview:    cfg.UI.Preview,
		sidebar:    cfg.UI.Sidebar,
		showHidden: cfg.UI.ShowHidden,
		mode:       "NORMAL",
	}
	m.Panes[0].Load(m.showHidden)
	if !cfg.UI.Onboarding {
		m.Overlay = OverlayOnboarding
	}
	return m, nil
}

// Jobs is the number of running transfers. The transfer engine arrives in M3;
// until then there is never anything running, and the interface says so.
func (m *Model) Jobs() int { return 0 }

// Init starts the heartbeat and asks the terminal for its size.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestWindowSize, heartbeat())
}

// Update handles one message. Nothing here blocks on I/O, so typing is never
// delayed by a slow filesystem.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.started {
			m.started = true
		}
		m.ensureVisible()
		return m, nil

	case heartbeatMsg:
		m.expireToasts()
		m.quitHint = ""
		return m, heartbeat()

	case tea.KeyboardEnhancementsMsg:
		m.applyCapabilities(msg)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// handleKey routes a key press to the overlay, the filter, or the key map.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Keys that work everywhere, including inside overlays, so quitting and
	// searching never depend on where you are.
	switch key {
	case m.cfg.Keys.Quit, "f10":
		if m.Overlay == OverlayConfirm || m.Overlay == OverlayPrompt {
			break // let the dialog handle it first
		}
		m.quit = true
		return m, tea.Quit
	case "ctrl+k":
		m.togglePalette()
		return m, nil
	case "f1":
		m.toggleHelp()
		return m, nil
	}

	if m.handleOverlayKey(msg) {
		return m, nil
	}
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	return m.handleNormalKey(msg)
}

// handleOverlayKey handles keys aimed at an open overlay.
func (m *Model) handleOverlayKey(msg tea.KeyPressMsg) bool {
	switch m.Overlay {
	case OverlayNone:
		return false
	case OverlayPalette:
		switch msg.String() {
		case "esc":
			m.Overlay = OverlayNone
			m.palette.input.Clear()
		case "up", "ctrl+p":
			m.palette.cursor = max(0, m.palette.cursor-1)
		case "down", "ctrl+n":
			m.palette.cursor = min(max(0, len(m.paletteItems())-1), m.palette.cursor+1)
		case "enter":
			items := m.paletteItems()
			if m.palette.cursor < len(items) {
				id := items[m.palette.cursor].ID
				m.Overlay = OverlayNone
				m.palette.input.Clear()
				m.runAction(id)
			}
		default:
			if m.palette.input.Handle(msg) {
				m.palette.cursor = 0
			}
		}
		return true

	case OverlayMenu:
		items := m.menuItems()
		switch msg.String() {
		case "esc", "f9":
			m.Overlay = OverlayNone
		case "up", "k":
			m.menu.cursor = max(0, m.menu.cursor-1)
		case "down", "j":
			m.menu.cursor = min(max(0, len(items)-1), m.menu.cursor+1)
		case "enter":
			if m.menu.cursor < len(items) {
				id := items[m.menu.cursor].ID
				m.Overlay = OverlayNone
				m.runAction(id)
			}
		default:
		}
		return true

	case OverlayHelp:
		switch msg.String() {
		case "esc", "q":
			m.Overlay = OverlayNone
			m.help.input.Clear()
		default:
			m.help.input.Handle(msg)
		}
		return true

	case OverlaySettings:
		options := m.settingsOptions()
		switch msg.String() {
		case "esc", "ctrl+,", "enter":
			if msg.String() == "enter" && m.settings.cursor < len(options) {
				m.applyTheme(options[m.settings.cursor])
			}
			if msg.String() != "enter" {
				m.Overlay = OverlayNone
			}
		case "up", "k":
			m.settings.cursor = max(0, m.settings.cursor-1)
		case "down", "j":
			m.settings.cursor = min(max(0, len(options)-1), m.settings.cursor+1)
		}
		return true

	case OverlayConfirm:
		switch msg.String() {
		case "esc":
			m.confirm = confirmState{}
			m.Overlay = OverlayNone
		case "enter":
			entries := m.confirm.Entries
			action := m.confirm.Action
			m.confirm = confirmState{}
			m.Overlay = OverlayNone
			m.applyConfirmed(action, entries)
		}
		return true

	case OverlayPrompt:
		switch msg.String() {
		case "esc":
			m.prompt = promptState{}
			m.Overlay = OverlayNone
		case "enter":
			value := m.prompt.input.Value
			action := m.prompt.Action
			m.prompt = promptState{}
			m.Overlay = OverlayNone
			m.applyPrompt(action, value)
		default:
			m.prompt.input.Handle(msg)
		}
		return true

	case OverlayOnboarding:
		switch msg.String() {
		case "esc":
			m.Overlay = OverlayNone
			m.markOnboarded()
		case "enter":
			m.Overlay = OverlayNone
			m.markOnboarded()
			m.toastInfo("Host import arrives with connections (M1)")
		case "i":
			m.toastInfo("Host import arrives with connections (M1)")
		}
		return true
	}
	return false
}

// handleFilterKey drives the in-pane incremental filter.
func (m *Model) handleFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.Pane().Filter = ""
		m.mode = "NORMAL"
		m.Pane().Cursor, m.Pane().Offset = 0, 0
	case "enter":
		m.filtering = false
		m.mode = "NORMAL"
	default:
		var buf input
		buf.Set(m.Pane().Filter)
		buf.End()
		if buf.Handle(msg) {
			m.Pane().Filter = buf.Value
			m.Pane().Cursor, m.Pane().Offset = 0, 0
		}
	}
	return m, nil
}

// handleNormalKey matches a key against the keymap and runs what it means.
func (m *Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	out := m.resolver.Feed(msg.String())
	switch {
	case out.Matched:
		m.resetTypeAhead()
		m.runAction(out.ID)
		return m, nil
	case out.Pending:
		// Half of a sequence is in; wait for the rest.
		return m, nil
	}

	// Nothing bound: treat printable input as type-ahead, which is how a file
	// manager has always let you find a name without knowing a key.
	key := msg.Key()
	if key.Text == "" || msg.String() == "space" {
		return m, nil
	}
	if key.Mod.Contains(tea.ModCtrl) || key.Mod.Contains(tea.ModAlt) {
		return m, nil
	}
	m.typeAhead(key.Text)
	return m, nil
}

// handleMouse makes the interface usable with a mouse: wheel scrolls, click
// focuses a pane and picks a row.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.Overlay != OverlayNone {
		return m, nil
	}
	l := m.measure()
	switch e := msg.(type) {
	case tea.MouseWheelMsg:
		switch e.Mouse().Button {
		case ansi.MouseWheelUp:
			m.moveCursor(-3)
		case ansi.MouseWheelDown:
			m.moveCursor(3)
		}
	case tea.MouseClickMsg:
		me := e.Mouse()
		if me.Button != ansi.MouseLeft {
			return m, nil
		}
		m.clickAt(me.X, me.Y, l)
	}
	return m, nil
}

// clickAt focuses the pane under the cursor and selects the row that was clicked.
func (m *Model) clickAt(x, y int, l layout) {
	bodyTop := 0
	if l.Tabs {
		bodyTop++
	}
	if y < bodyTop {
		return
	}
	left := 0
	if l.Sidebar {
		if x < l.SidebarWidth {
			return
		}
		left = l.SidebarWidth
	}
	index := 0
	if l.Panes > 1 {
		if x-left >= l.PaneWidth {
			index = 1
		}
	}
	m.Focused = min(index, len(m.Panes)-1)

	// Row 0 of a pane is the breadcrumb, row 1 is the top border.
	row := y - bodyTop - 2
	if row < 0 {
		return
	}
	pane := m.Pane()
	visible := pane.Visible()
	if row >= len(visible) || row >= l.PaneRows {
		return
	}
	pane.Cursor = pane.Offset + row
	pane.Cursor = clamp(pane.Cursor, 0, max(0, len(visible)-1))
}

// runAction executes an action by id, explaining rather than ignoring when it
// does not apply.
func (m *Model) runAction(id actions.ID) {
	a, ok := m.reg.Get(id)
	if !ok {
		return
	}
	if !a.Applicable(m.actionContext()) {
		m.explainUnavailable(a)
		return
	}

	switch id {
	case "app.quit":
		m.quit = true
	case "app.back":
		m.goBack()
	case "app.palette":
		m.togglePalette()
	case "app.help":
		m.toggleHelp()
	case "app.settings":
		m.Overlay = OverlaySettings
		m.settings.cursor = indexOf(m.themeNames, m.themeName)
	case "app.tour", "app.onboarding":
		m.Overlay = OverlayOnboarding
	case "app.version":
		m.toastInfo(m.versionLine())

	// Navigation
	case "nav.up":
		m.moveCursor(-1)
	case "nav.down":
		m.moveCursor(1)
	case "nav.parent":
		m.parent()
	case "nav.open":
		m.open()
	case "nav.page-up":
		m.page(-1)
	case "nav.page-down":
		m.page(1)
	case "nav.first":
		m.toFirst()
	case "nav.last":
		m.toLast()
	case "nav.back":
		m.historyBack()
	case "nav.forward":
		m.historyForward()
	case "nav.home":
		m.goHome()
	case "nav.last-dir":
		m.goLastDir()
	case "nav.path":
		m.openPrompt("prompt.path")
	case "nav.refresh":
		m.refresh()
		m.toastInfo("Refreshed")
	case "nav.filter":
		m.filtering = true
		m.mode = "SEARCH"
	case "nav.hidden":
		m.showHidden = !m.showHidden
		m.refresh()
		m.toastInfo(hiddenMessage(m.showHidden))
	case "nav.preview":
		m.preview = !m.preview
	case "nav.switch-pane", "nav.switch-pane-prev":
		delta := 1
		if id == "nav.switch-pane-prev" {
			delta = -1
		}
		m.switchPane(delta)
	case "nav.reveal":
		m.revealInOtherPane()
	case "nav.refresh-dir":
		m.refresh()

	// Selection
	case "sel.toggle":
		m.toggleSelect()
	case "sel.extend-up":
		m.extendSelection(-1)
	case "sel.extend-down":
		m.extendSelection(1)
	case "sel.all":
		m.selectAll()
		m.mode = "SELECT"
	case "sel.invert":
		m.invertSelection()
	case "sel.clear":
		m.clearSelection()
		m.mode = "NORMAL"

	// File operations available without a connection
	case "file.view":
		m.preview = true
		m.mode = "PREVIEW"
	case "file.menu":
		m.Overlay = OverlayMenu
		m.menu.cursor = 0
	case "file.mkdir":
		m.openPrompt("prompt.mkdir")
	case "file.new":
		m.openPrompt("prompt.new")
	case "file.rename":
		m.openPrompt("prompt.rename")
	case "file.delete":
		m.openDeleteConfirm()

	// View toggles
	case "view.sidebar":
		m.sidebar = !m.sidebar
	case "view.jobs":
		m.toastInfo("No transfers are running")
	case "view.sync":
		m.syncBrowse = !m.syncBrowse
		m.toastInfo(syncMessage(m.syncBrowse))
	case "view.compare":
		m.comparePanes()

	case "file.copy":
		m.quitHint = "Nothing to copy · Ctrl+Q quits"
	default:
		m.toastWarning(a.Label + " arrives with " + m.stageOf(id))
	}
}

// stageOf names the milestone a not-yet-implemented action belongs to, so the
// milestone builds stay honest about what they cannot do yet.
func (m *Model) stageOf(id actions.ID) string {
	switch {
	case strings.HasPrefix(string(id), "deploy."):
		return "deployments (M6)"
	case strings.HasPrefix(string(id), "archive."):
		return "the archive engine (M4)"
	case strings.HasPrefix(string(id), "terminal."):
		return "the terminal (M5)"
	case strings.HasPrefix(string(id), "session."):
		return "connections (M1)"
	case id == "file.edit", id == "file.chmod", id == "file.symlink":
		return "the file tooling in M2"
	default:
		return "the transfer engine (M3)"
	}
}

// explainUnavailable replaces silence with the reason and the way out.
func (m *Model) explainUnavailable(a actions.Action) {
	switch a.ID {
	case "file.copy", "file.cut":
		m.quitHint = "Nothing selected · Space selects · Ctrl+Q quits"
	case "file.delete":
		m.quitHint = "Nothing selected to delete"
	case "archive.extract":
		m.quitHint = "Put the cursor on an archive first"
	default:
		m.toastInfo(a.Label + " needs " + m.unavailableReason(a))
	}
}

// goBack implements Esc: dismiss, cancel, or clear, in that order. It never
// quits, and it never destroys anything.
func (m *Model) goBack() {
	switch {
	case m.Overlay != OverlayNone:
		m.confirm, m.prompt = confirmState{}, promptState{}
		m.Overlay = OverlayNone
	case m.filtering:
		m.filtering = false
		m.Pane().Filter = ""
		m.mode = "NORMAL"
	case m.typeAheadBuf != "":
		m.resetTypeAhead()
	case m.Pane().SelectedCount() > 0:
		m.clearSelection()
		m.mode = "NORMAL"
	case m.resolver.Pending() != "":
		m.resolver.Reset()
	case m.dismissToast():
	default:
		m.quitHint = "Nothing to dismiss · Ctrl+Q to quit"
	}
}

// openPrompt opens one of the named prompts.
func (m *Model) openPrompt(action actions.ID) {
	switch action {
	case "prompt.mkdir":
		m.prompt = promptState{Title: "New folder", Hint: "Name for the new directory", Action: action, open: true}
	case "prompt.new":
		m.prompt = promptState{Title: "New file", Hint: "Name for the new file", Action: action, open: true}
	case "prompt.rename":
		entry, ok := m.Pane().CursorEntry()
		if !ok {
			return
		}
		m.prompt = promptState{Title: "Rename", Hint: "New name for " + entry.Name, Action: action, open: true}
		m.prompt.input.Set(entry.Name)
	case "prompt.path":
		m.prompt = promptState{Title: "Go to path", Hint: "Directory to open", Action: action, open: true}
		m.prompt.input.Set(m.Pane().Path)
	default:
		return
	}
	m.Overlay = OverlayPrompt
}

// applyPrompt runs whatever the prompt was for.
func (m *Model) applyPrompt(action actions.ID, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch action {
	case "prompt.mkdir":
		m.createDir(value)
	case "prompt.new":
		m.createFile(value)
	case "prompt.rename":
		m.renameCursor(value)
	case "prompt.path":
		m.navigate(value)
	}
}

// openDeleteConfirm states the facts before deleting, because there is no undo.
func (m *Model) openDeleteConfirm() {
	entries := m.targets()
	if len(entries) == 0 {
		return
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	body := []string{fmt.Sprintf("Delete %d %s · %s · %s",
		len(entries), plural(len(entries), "item", "items"), humanSize(total), m.Pane().Path)}
	if len(entries) <= 8 {
		body = append(body, "")
		for _, e := range entries {
			name := e.Name
			if e.Dir {
				name += "/"
			}
			body = append(body, "  "+name)
		}
	}
	m.confirm = confirmState{
		Title:   "Delete permanently?",
		Body:    body,
		Danger:  true,
		Action:  "file.delete",
		Entries: entries,
		open:    true,
	}
	m.Overlay = OverlayConfirm
}

// applyConfirmed runs the action a confirmation dialog approved. Deletion is the
// only destructive action in this build; the others arrive with their features.
func (m *Model) applyConfirmed(action actions.ID, entries []Entry) {
	if action == "file.delete" {
		m.deleteTargets(entries)
	}
}

// comparePanes shows a read-only diff of the two listings.
func (m *Model) comparePanes() {
	if len(m.Panes) < 2 {
		return
	}
	left, right := m.Panes[0], m.Panes[1]
	leftNames := map[string]bool{}
	for _, e := range left.Entries {
		leftNames[e.Name] = true
	}
	rightNames := map[string]bool{}
	for _, e := range right.Entries {
		rightNames[e.Name] = true
	}
	var onlyLeft, onlyRight, both int
	for name := range leftNames {
		if rightNames[name] {
			both++
		} else {
			onlyLeft++
		}
	}
	for name := range rightNames {
		if !leftNames[name] {
			onlyRight++
		}
	}
	m.toastInfo(fmt.Sprintf("Compare: %d only here · %d only there · %d in both", onlyLeft, onlyRight, both))
}

// applyTheme switches theme immediately, so the settings card is its own
// preview.
func (m *Model) applyTheme(name string) {
	resolved, err := theme.Resolve(name, m.profile, m.hasDark)
	if err != nil {
		m.toastError(err.Error())
		return
	}
	m.st = theme.NewStyles(resolved)
	m.themeName = resolved.Name
	m.cfg.UI.Theme = name
	m.settings.cursor = indexOf(m.themeNames, resolved.Name)
	m.toastSuccess("Theme: " + resolved.Name)
}

// applyCapabilities rebuilds the keymap once the terminal has told us what it
// can send, dropping bindings it cannot deliver instead of ignoring them.
func (m *Model) applyCapabilities(msg tea.KeyboardEnhancementsMsg) {
	kitty := msg.SupportsKeyDisambiguation()
	rebuilt, err := keymap.BuildWithDeliverer(m.reg, keymap.Options{
		Preset:    keymap.Preset(m.cfg.Keys.Preset),
		Overrides: m.cfg.Keys.Overrides,
	}, terminalDeliverer{kitty: kitty})
	if err != nil {
		return
	}
	m.keys = rebuilt
	m.resolver = rebuilt.Resolver()
	m.dropped = rebuilt.Dropped()
}

// terminalDeliverer reports what this terminal can actually send.
type terminalDeliverer struct{ kitty bool }

// CanDeliver refuses modified keys that need the Kitty protocol to distinguish.
func (d terminalDeliverer) CanDeliver(key string) (string, bool) {
	if d.kitty {
		return "", true
	}
	if strings.HasPrefix(key, "ctrl+shift+") {
		return "needs a terminal that reports modified keys", false
	}
	return "", true
}

// markOnboarded records that the first-run card has been seen.
func (m *Model) markOnboarded() {
	m.cfg.UI.Onboarding = true
}

// togglePalette opens or closes the command palette.
func (m *Model) togglePalette() {
	if m.Overlay == OverlayPalette {
		m.Overlay = OverlayNone
		m.palette.input.Clear()
		return
	}
	m.Overlay = OverlayPalette
	m.palette = paletteState{}
}

// toggleHelp opens or closes the help screen.
func (m *Model) toggleHelp() {
	if m.Overlay == OverlayHelp {
		m.Overlay = OverlayNone
		m.help.input.Clear()
		return
	}
	m.Overlay = OverlayHelp
	m.help = helpState{}
}

// indexOf returns the index of s in list, or zero.
func indexOf(list []string, s string) int {
	for i, item := range list {
		if item == s {
			return i
		}
	}
	return 0
}

func hiddenMessage(show bool) string {
	if show {
		return "Showing hidden files"
	}
	return "Hiding dotfiles"
}

func syncMessage(on bool) string {
	if on {
		return "Sync browse on: panes follow the same path"
	}
	return "Sync browse off"
}

// ProfileFor detects the terminal's colour profile, honouring NO_COLOR.
func ProfileFor(out *os.File, env []string) colorprofile.Profile {
	if os.Getenv("NO_COLOR") != "" {
		return colorprofile.ASCII
	}
	return colorprofile.Detect(out, env)
}

// HasDarkBackground asks the terminal whether its background is dark, falling
// back to dark when it cannot say.
func HasDarkBackground() bool {
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
}

var _ = color.RGBA{} // keep image/color imported for future theme work
