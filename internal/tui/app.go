package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/actions"
	"github.com/fakihariefnoto/omssh/internal/clipboard"
	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/keymap"
	"github.com/fakihariefnoto/omssh/internal/keys"
	"github.com/fakihariefnoto/omssh/internal/remoteinfo"
	"github.com/fakihariefnoto/omssh/internal/session"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
	"github.com/fakihariefnoto/omssh/internal/theme"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Options configures a new interface.
type Options struct {
	Config  config.Config
	Profile colorprofile.Profile
	// HasDark is the terminal's background darkness, normally read from
	// COLORFGBG. When the terminal does not say, the dark themes are assumed and
	// the user can pick another in settings.
	//
	// The interface never asks the terminal for its background on startup. A
	// terminal query gates the first render until the reply arrives, so a
	// terminal that does not answer (macOS Terminal, some multiplexers) shows an
	// empty screen until the user presses a key. That reads as "the app is
	// broken", so the query is offered as an action instead, where a brief pause
	// is expected and the user asked for it.
	HasDark bool
	// OffersConnect enables the connection entries in the launcher and the
	// first-run card. It stays false until the SSH layer exists, because a menu
	// of dead ends teaches nothing.
	OffersConnect bool
	Registry      *actions.Registry
	Keys          *keymap.Map
	// Dir is the directory the local pane starts in.
	Dir string
	// Connect names a host to open as soon as the interface starts, so
	// "omssh prod" lands where the person meant to be.
	Connect string
	// SSHConfigPath overrides ~/.ssh/config. Tests use it; the CLI leaves it
	// empty so the real file is used.
	SSHConfigPath string
	// SecretStore is the keyring used for bucket credentials. Tests pass an
	// in-memory store; the CLI leaves it nil and the interface builds one.
	SecretStore *keys.Store
	// ConfigPath is where preferences are written back: favourites, hidden hosts
	// and recents. Empty means "do not persist" (tests, and a config-less run).
	ConfigPath string
	// Sessions is the connection manager. When nil the interface builds its own,
	// asking through dialogs; a test supplies one with its own prompter.
	Sessions *session.Manager
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
	sshPath    string
	configPath string
	secrets    *keys.Store
	themeName  string
	themeNames []string
	// bgSeq paints the theme's own background behind every line. Without it a
	// light theme on a dark terminal draws dark text on a dark screen — the menu
	// that "overlaps the background".
	bgSeq   string
	dropped []keymap.Dropped

	width, height int
	started       bool

	tabs      []Tab
	activeTab int
	// focusTabs is true while the tab strip has the focus: h/l choose a tab,
	// Enter dives into its pane, Escape goes up. Escape from a pane lands here
	// when there is more than one tab, so switching hosts is one key away.
	focusTabs bool

	Panes   []Pane
	Focused int

	preview    bool
	sidebar    bool
	showHidden bool
	syncBrowse bool
	filtering  bool

	// transfers are the copies running right now, for the sidebar. The copy itself
	// runs in a command; the progress arrives on a channel so a slow link never
	// freezes the update loop.
	transfers   map[int]transfer
	transferSeq int
	progress    chan transferUpdate
	// progressArmed is true once a reader is waiting on that channel: exactly one
	// is wanted, and it re-arms itself after every report.
	progressArmed bool

	// The preview is cached: it reads files, render runs on every keystroke, and a
	// remote read per frame is what made the interface blink. previewGen is bumped
	// whenever a listing changes, so the cache cannot outlive the truth.
	// frameTrimmed records that fitFrame had to cut a line a view measured wrong.
	frameTrimmed bool
	previewKey   string
	previewBody  string
	previewGen   int

	mode       string
	statusHint string
	quitHint   string

	typeAheadBuf  string
	typeAheadLast string

	// screen is the launcher, the file manager or the host manager.
	screen screen
	home   homeState
	// conn is the SSH host manager, and form is its add/edit card.
	conn *connectionsState
	form connForm
	// sessions owns the connections. The interface never dials anything itself.
	sessions *session.Manager
	// prompts carries questions from a connection to the interface, and
	// activePrompt is the one being asked.
	prompts      chan *promptRequest
	activePrompt *promptRequest
	// connecting is the set of hosts with a connection in flight.
	connecting map[string]bool
	// live is the sessions this interface is showing, by alias, so switching tabs
	// does not have to ask the manager.
	live map[string]*session.Session
	// secretInput and rememberSecret are the password dialog's state.
	secretInput    input
	rememberSecret bool
	// connectCtx is cancelled when the program exits, so a dial does not outlive
	// the interface.
	connectCtx    context.Context
	cancelConnect context.CancelFunc
	Overlay       Overlay
	palette       paletteState
	add           addState
	menu          menuState
	help          helpState
	settings      settingsState
	confirm       confirmState
	prompt        promptState
	onboarding    onboardingState
	// overlayCmd carries a command produced while handling an overlay key.
	overlayCmd tea.Cmd
	// connReturn remembers where the host manager was opened from.
	connReturn screen
	// confirmAlias carries the host name a confirmation is about.
	confirmAlias string

	staged stagedState
	// deploy is the deploy card's state, and pendingDeploy is the profile waiting
	// for its host to finish connecting.
	deploy deployState
	// panes is the pane chooser's state, and panesAsked records that the
	// question was answered once so it does not reopen on every return.
	panes         panesState
	panesAsked    bool
	pendingDeploy string
	// samples are the numbers on the session cards, and sampleSeq is the
	// repeating read while the chooser is open.
	samples   map[string]remoteinfo.Info
	sampleSeq tea.Cmd
	// autoConnect is the host named on the command line, opened by Init.
	autoConnect string

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

// onboardingState tracks which button of the first-run card has focus.
type onboardingState struct{ cursor int }

type confirmState struct {
	Title   string
	Body    []string
	Danger  bool
	Action  actions.ID
	Entries []Entry
	// Alias is set when the confirmation is about a connection.
	Alias string
	open  bool
}

type promptState struct {
	Title  string
	Hint   string
	Action actions.ID
	input  input
	open   bool
	// Path prompts suggest matching folders as you type.
	suggestions   []string
	suggestCursor int
	suggestPicked bool
}

// stagedState is what Ctrl+C or Ctrl+X put on the "clipboard".
type stagedState struct {
	Paths []string
	// Sizes are captured with the marks: the source pane may be looking at
	// another directory by the time the pasted copy is reviewed.
	Sizes map[string]int64
	// Dir is the directory the names are relative to. Marks outlive navigation —
	// marking in one folder and pasting in three others is one gesture, not three.
	Dir string
	Cut bool
	// From is the pane the marks came from; a copy has one source.
	From int
	// Policy and After are the paste options, changed with A.
	Policy int
	After  pasteAfter
}

// heartbeatMsg drives toast expiry and clears transient hints.
type heartbeatMsg struct{}

// clipboardMsg carries the system clipboard's text, or the reason it could not
// be read.
type clipboardMsg struct {
	text string
	err  error
}

// pasteClipboard reads the system clipboard in the background. It runs as a
// command rather than inline, because pbpaste is a process and spawning one
// inside Update would stall the interface.
func pasteClipboard() tea.Cmd {
	return func() tea.Msg {
		text, err := clipboard.Read(context.Background())
		return clipboardMsg{text: text, err: err}
	}
}

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
		paneKeys, err = keymap.Build(opts.Registry, keymap.Options{Overrides: cfg.Keys.Overrides})
		if err != nil {
			return nil, err
		}
	}

	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	// The local pane is a filesystem rooted at "/", the same as the remote one
	// will be: absolute paths, and one code path for both.
	localFS, err := vfs.NewLocal("/")
	if err != nil {
		return nil, fmt.Errorf("opening the local filesystem: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = dir
	}

	m := &Model{
		cfg:         cfg,
		st:          theme.NewStyles(resolved),
		icons:       theme.IconsFor(cfg.UI.Icons),
		reg:         opts.Registry,
		keys:        paneKeys,
		resolver:    paneKeys.Resolver(""),
		profile:     opts.Profile,
		hasDark:     opts.HasDark,
		sshPath:     opts.SSHConfigPath,
		secrets:     opts.SecretStore,
		themeName:   resolved.Name,
		themeNames:  theme.Names(),
		bgSeq:       backgroundSeq(resolved, opts.HasDark),
		dropped:     paneKeys.Dropped(),
		tabs:        []Tab{{Title: "localhost", State: StateReady}},
		Panes:       []Pane{NewPane(localFS, dir, home), NewRemotePane()},
		Focused:     0,
		preview:     cfg.UI.Preview,
		sidebar:     cfg.UI.Sidebar,
		showHidden:  cfg.UI.ShowHidden,
		mode:        "NORMAL",
		autoConnect: opts.Connect,
	}
	m.prompts = make(chan *promptRequest, 8)
	if opts.Sessions != nil {
		// A test supplies a manager with its own prompter, so these dialogs are
		// out of the way.
		m.sessions = opts.Sessions
	} else {
		manager, err := newSessionManager(m.prompts)
		if err != nil {
			// A machine with no ~/.ssh is not a reason to refuse to start: the
			// file manager still works, and connecting will say why.
			m.sessions = nil
		} else {
			m.sessions = manager
		}
	}
	m.Panes[0].Load(m.showHidden)
	m.screen = screenHome
	if cfg.UI.Start == "files" {
		m.screen = screenFiles
	}
	if opts.OffersConnect && !cfg.UI.Onboarding {
		m.Overlay = OverlayOnboarding
	}
	return m, nil
}

// Jobs is the number of running transfers. The transfer engine arrives in M3;
// until then there is never anything running, and the interface says so.
func (m *Model) Jobs() int { return len(m.transfers) }

// Init starts the heartbeat and asks the terminal for its size. Nothing here
// waits for a reply, so the first frame is painted immediately: a startup query
// is a startup delay at best and a blank screen at worst.
func (m *Model) Init() tea.Cmd {
	commands := []tea.Cmd{tea.RequestWindowSize, heartbeat()}
	if cmd := m.armProgress(); cmd != nil {
		commands = append(commands, cmd)
	}
	if m.sessions != nil {
		// The connection goroutines ask through this channel while they wait.
		commands = append(commands, waitForPrompt(m.prompts))
	}
	if m.autoConnect != "" {
		if cmd := m.connectOnStartup(); cmd != nil {
			commands = append(commands, cmd)
		}
	}
	return tea.Batch(commands...)
}

// connectOnStartup opens the host named on the command line. A name that is not
// in the config is still a host: ssh's own defaults know how to reach it.
func (m *Model) connectOnStartup() tea.Cmd {
	alias := m.autoConnect
	m.autoConnect = ""
	if m.sessions == nil {
		m.toastError("Connections are not available: " + alias + " was not opened")
		return nil
	}
	target, ok := m.targetForAlias(alias)
	if !ok {
		m.toastError("Cannot open " + alias)
		return nil
	}
	m.screen = screenFiles
	return m.connect(target)
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
		// The preview needs a width before it can be fetched, and this is the
		// first message that has one.
		cmd := m.previewCmd()
		return m, cmd

	case heartbeatMsg:
		m.expireToasts()
		m.pruneTransfers(time.Now())
		m.quitHint = ""
		// Anything that changed the cursor or a listing without going through the
		// keyboard lands here within half a second; the keyboard path asks at once.
		cmd := m.previewCmd()
		return m, tea.Batch(heartbeat(), cmd)

	case tea.KeyboardEnhancementsMsg:
		m.applyCapabilities(msg)
		return m, nil

	case tea.BackgroundColorMsg:
		// Only reached when the user asked to follow the terminal background.
		m.hasDark = msg.IsDark()
		// The mismatch rule depends on the terminal, so the background is
		// recomputed even when the theme itself does not change.
		m.bgSeq = backgroundSeq(m.st.Theme, m.hasDark)
		switch {
		case m.cfg.UI.Theme == "auto":
			m.applyTheme("auto")
		case m.hasDark:
			m.toastInfo("The terminal background is dark")
		default:
			m.toastInfo("The terminal background is light")
		}
		return m, nil

	case tea.PasteMsg:
		// The terminal's own paste (bracketed paste). It arrives as text, not as
		// keystrokes, so it goes straight into whatever field has focus.
		m.handlePaste(msg.Content)
		return m, nil

	case promptMsg:
		m.handlePromptMsg(msg)
		return m, nil

	case cloudReadyMsg:
		cmd := m.handleCloudReady(msg)
		return m, cmd

	case connectedMsg:
		cmd := m.handleConnected(msg)
		return m, cmd

	case copyReport:
		cmd := m.handleCopyReport(msg)
		return m, cmd

	case dirLoadedMsg:
		m.applyListing(msg)
		return m, nil

	case deployPlanMsg:
		m.deploy.loading = false
		if msg.err != nil {
			m.deploy.err = msg.err.Error()
			return m, nil
		}
		m.deploy.plan = &msg.plan
		return m, nil

	case deployDoneMsg:
		m.deploy.loading = false
		m.deploy.applied = true
		if msg.err != nil {
			m.deploy.err = msg.err.Error()
			m.toastError("Deploy finished with errors: " + msg.err.Error())
			return m, nil
		}
		create, update, remove := msg.plan.Counts()
		m.toastSuccess(fmt.Sprintf("Deployed %s: %d created, %d updated, %d deleted",
			msg.profile, create, update, remove))
		m.Overlay = OverlayNone
		m.mode = "NORMAL"
		cmd := m.refreshAll()
		return m, cmd

	case terminalDoneMsg:
		m.mode = "NORMAL"
		if msg.err != nil {
			m.toastError("Terminal: " + msg.err.Error())
		} else {
			m.toastInfo("Left " + msg.host)
		}
		cmd := m.refreshAll()
		return m, cmd

	case extractDoneMsg:
		m.mode = "NORMAL"
		m.finishTransfer(msg.id)
		if msg.err != nil {
			m.toastError("Extract failed: " + msg.err.Error())
			return m, nil
		}
		m.toastSuccess(fmt.Sprintf("Extracted %d %s into %s",
			msg.count, plural(msg.count, "file", "files"), msg.dst))
		cmd := m.refreshAll()
		return m, cmd

	case compressDoneMsg:
		m.mode = "NORMAL"
		m.finishTransfer(msg.id)
		if msg.err != nil {
			m.toastError("Compress failed: " + msg.err.Error())
			return m, nil
		}
		m.toastSuccess(fmt.Sprintf("Wrote %s with %d %s", msg.dst,
			msg.count, plural(msg.count, "entry", "entries")))
		cmd := m.refreshAll()
		return m, cmd

	case editReadyMsg:
		cmd := m.openRemoteEditor(msg)
		return m, cmd

	case editClosedMsg:
		cmd := m.uploadEditedFile(msg)
		return m, cmd

	case editFailedMsg:
		m.mode = "NORMAL"
		m.toastError("Edit failed: " + msg.err.Error())
		return m, nil

	case findResultMsg:
		cmd := m.showFindResult(msg)
		return m, cmd

	case previewReadyMsg:
		m.applyPreview(msg)
		return m, nil

	case transferUpdate:
		if job, ok := m.transfers[msg.id]; ok {
			job.done, job.total, job.name = msg.done, msg.total, msg.name
			m.transfers[msg.id] = job
		}
		cmd := waitForTransfer(m.progress)
		return m, cmd

	case sessionSampleMsg:
		if m.samples == nil {
			m.samples = map[string]remoteinfo.Info{}
		}
		m.samples[msg.alias] = msg.info
		return m, nil

	case sampleTickMsg:
		if m.Overlay != OverlayPanes {
			return m, nil
		}
		cmd := m.sampleSessions()
		return m, cmd

	case editReloadMsg:
		m.mode = "NORMAL"
		m.toastSuccess("Editor closed")
		cmd := m.loadPaneAsync(msg.dirIdx)
		return m, cmd

	case syncPaneMsg:
		cmd := m.followInPane(msg)
		return m, cmd

	case clipboardMsg:
		if msg.err != nil {
			m.toastWarning("Clipboard unavailable: " + msg.err.Error())
			return m, nil
		}
		m.handlePaste(msg.text)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// handleKey routes a key press to the overlay, the filter, or the key map.
// handleKey wraps the keymap handler and asks for the preview the new cursor
// needs. Rendering cannot ask, and asking here means a moved cursor does not wait
// for the heartbeat to fetch its preview.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	model, cmd := m.handleKeyNow(msg)
	if fetch := m.previewCmd(); fetch != nil {
		cmd = tea.Batch(cmd, fetch)
	}
	return model, cmd
}

func (m *Model) handleKeyNow(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Ctrl+V pastes into a text field when one has focus. In the file manager no
	// field has focus, so Ctrl+V keeps its documented meaning there: stage the
	// selection for pasting into the other pane.
	if key == "ctrl+v" && m.focusedInput() != nil {
		return m, pasteClipboard()
	}

	// Keys that work everywhere, including inside overlays, so quitting and
	// searching never depend on where you are.
	switch key {
	case m.cfg.Keys.Quit, "f10":
		if m.Overlay == OverlayConfirm || m.Overlay == OverlayPrompt {
			break // let the dialog handle it first
		}
		m.quit = true
		m.answerPendingPrompt()
		if m.cancelConnect != nil {
			m.cancelConnect()
		}
		return m, tea.Quit
	case "ctrl+k":
		m.togglePalette()
		return m, nil
	case "f1":
		m.toggleHelp()
		return m, nil
	}

	if m.handleOverlayKey(msg) {
		cmd := m.overlayCmd
		m.overlayCmd = nil
		return m, cmd
	}
	if m.screen == screenHome {
		if cmd, handled := m.handleHomeKey(msg); handled {
			return m, cmd
		}
	}
	if m.screen == screenConnections {
		if cmd, handled := m.handleConnectionsKey(msg); handled {
			return m, cmd
		}
	}
	if m.focusTabs {
		return m.handleTabKey(msg)
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
				m.overlayCmd = m.runAction(id)
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
				m.overlayCmd = m.runAction(id)
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
		// The last row is the keymap, which is why the range is one longer.
		last := len(options)
		switch msg.String() {
		case "esc", "ctrl+,":
			m.Overlay = OverlayNone
		case "enter":
			if m.settings.cursor == last {
				m.Overlay = OverlayHelp
				m.help.input.Clear()
				break
			}
			if m.settings.cursor < len(options) {
				m.applyTheme(options[m.settings.cursor])
			}
		case "?":
			m.Overlay = OverlayHelp
			m.help.input.Clear()
		case "up", "k":
			m.settings.cursor = max(0, m.settings.cursor-1)
		case "down", "j":
			m.settings.cursor = min(last, m.settings.cursor+1)
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
			m.confirmAlias = m.confirm.Alias
			m.confirm = confirmState{}
			m.Overlay = OverlayNone
			m.overlayCmd = m.applyConfirmed(action, entries)
		}
		return true

	case OverlayPrompt:
		switch msg.String() {
		case "esc":
			m.prompt = promptState{}
			m.Overlay = OverlayNone
		case "up", "shift+tab":
			if m.hasSuggestions() {
				m.prompt.suggestCursor = (m.prompt.suggestCursor - 1 + len(m.prompt.suggestions)) % len(m.prompt.suggestions)
				m.prompt.suggestPicked = true
			}
		case "down":
			if m.hasSuggestions() {
				m.prompt.suggestCursor = (m.prompt.suggestCursor + 1) % len(m.prompt.suggestions)
				m.prompt.suggestPicked = true
			}
		case "tab":
			if m.hasSuggestions() {
				m.prompt.input.Set(m.prompt.suggestions[m.prompt.suggestCursor])
				m.prompt.input.End()
				m.rebuildPathSuggestions()
			}
		case "enter":
			value := m.prompt.input.Value
			// Enter takes the highlighted suggestion when the user moved into the
			// list; otherwise it takes what they typed.
			if m.hasSuggestions() && m.prompt.suggestPicked {
				value = strings.TrimSuffix(m.prompt.suggestions[m.prompt.suggestCursor], "/")
			}
			action := m.prompt.Action
			m.prompt = promptState{}
			m.Overlay = OverlayNone
			m.overlayCmd = m.applyPrompt(action, value)
		default:
			if m.prompt.input.Handle(msg) {
				m.rebuildPathSuggestions()
			}
		}
		return true

	case OverlayForm:
		m.overlayCmd = m.handleFormKey(msg)
		return true

	case OverlayDeploy:
		m.overlayCmd = m.handleDeployKey(msg)
		return true

	case OverlayPanes:
		m.handlePanesKey(msg)
		return true

	case OverlayPaste:
		m.overlayCmd = m.handlePasteOptionsKey(msg)
		return true

	case OverlayAdd:
		m.handleAddKey(msg)
		return true

	case OverlayStaged:
		switch msg.String() {
		case "esc":
			m.Overlay = OverlayNone
		case "u":
			m.clearStaged()
			m.Overlay = OverlayNone
			m.toastInfo("Nothing is marked now")
		case "enter":
			m.Overlay = OverlayNone
			m.overlayCmd = m.pasteStaged()
		}
		return true

	case OverlayHostKey:
		m.overlayCmd = m.handleHostKeyKey(msg)
		return true

	case OverlaySecret:
		m.overlayCmd = m.handleSecretKey(msg)
		return true

	case OverlayOnboarding:
		// The card is a small menu, not a wall: Tab and the arrow keys move
		// between its buttons, Enter takes the focused one. Esc always gets out.
		switch msg.String() {
		case "esc", "q":
			m.Overlay = OverlayNone
			m.markOnboarded()
		case "left", "shift+tab", "up", "k":
			m.onboarding.cursor = (m.onboarding.cursor + 2) % 3
		case "right", "tab", "down", "j":
			m.onboarding.cursor = (m.onboarding.cursor + 1) % 3
		case "i":
			m.onboarding.cursor = 0
			m.activateOnboarding()
		case "a":
			m.onboarding.cursor = 1
			m.activateOnboarding()
		case "enter", "space":
			m.activateOnboarding()
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
		buf.SetForEditing(m.Pane().Filter)
		if buf.Handle(msg) {
			m.Pane().Filter = buf.Value
			m.Pane().Cursor, m.Pane().Offset = 0, 0
		}
	}
	return m, nil
}

// handleNormalKey matches a key against the keymap and runs what it means.
//
// There is no type-ahead fallback: plain letters are commands, so an unbound
// letter does nothing rather than jumping the cursor. Searching is "/" and
// Ctrl+F, which is one key to learn and never guesses.
func (m *Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.resolver.SetScreen(m.screenName())
	out := m.resolver.Feed(msg.String())
	switch {
	case out.Matched:
		// Alt+1…9 all resolve to "switch tab"; the number is in the key itself.
		if out.ID == "session.tab" {
			m.resetTypeAhead()
			cmd := m.switchTab(tabIndexFromKey(msg.String()))
			return m, cmd
		}
		m.resetTypeAhead()
		cmd := m.runAction(out.ID)
		return m, cmd
	case out.Pending:
		// Half of a sequence is in; wait for the rest.
		return m, nil
	}
	return m, nil
}

// handleMouse makes the interface usable with a mouse: wheel scrolls, click
// focuses a pane and picks a row.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.Overlay != OverlayNone {
		return m, nil
	}
	l := m.measure()
	if m.screen == screenHome {
		if click, ok := msg.(tea.MouseClickMsg); ok && click.Mouse().Button == ansi.MouseLeft {
			_, top, itemStart := m.homeGeometry(l)
			index := click.Mouse().Y - top - itemStart
			if items := m.homeItems(); index >= 0 && index < len(items) {
				m.home.cursor = index
				cmd := m.activateHome(items[index])
				return m, cmd
			}
		}
		return m, nil
	}
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
	column := 0
	if l.Panes > 1 && x-left >= l.PaneWidth {
		column = 1
	}

	// In the grid the second row of panes starts after one block (breadcrumb,
	// two borders, the listing rows) and one row of air.
	top := y - bodyTop
	if l.Grid {
		blockHeight := l.PaneRows + 3
		if top >= blockHeight+1 {
			column += 2
			top -= blockHeight + 1
		}
	}
	index := column
	m.Focused = min(index, len(m.Panes)-1)
	m.syncActiveTab()

	// Row 0 of a pane is the breadcrumb, row 1 is the top border.
	row := top - 2
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
// does not apply. It may return a command, because a few actions ask the
// terminal a question.
func (m *Model) runAction(id actions.ID) tea.Cmd {
	a, ok := m.reg.Get(id)
	if !ok {
		return nil
	}
	if !a.Applicable(m.actionContext()) {
		m.explainUnavailable(a)
		return nil
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

	case "app.files":
		m.openFiles()
	case "app.dashboard":
		m.openDashboard()

	case "session.open":
		m.loadConnections()
		m.connReturn = m.screen
		m.screen = screenConnections
		m.mode = "CONNECTIONS"
	case "session.add":
		switch m.connSectionNow() {
		case secSSH:
			m.openConnectionForm("", sshconfig.Spec{})
		case secS3:
			m.openCloudForm("s3")
		case secOverview:
			m.openAddChooser()
		default:
			m.toastInfo(m.connSectionNow().title() + " is planned · see ROADMAP.md")
		}

	case "session.edit":
		if entry, ok := m.currentConnection(); ok {
			if !entry.Editable {
				m.toastWarning(entry.Alias + " lives in " + entry.Source + "; edit that file")
				break
			}
			block, _ := m.conn.main.Lookup(entry.Alias)
			m.openConnectionForm(entry.Alias, block.Spec())
		}
	case "session.delete":
		m.deleteConnection()
	case "session.reload":
		m.loadConnections()
		m.toastInfo("Reloaded ~/.ssh/config")
	case "session.open-config":
		return m.editConfigFile()
	case "session.close-tab":
		return m.closeTab(m.activeTab)
	case "session.tab-next":
		return m.switchTab(m.activeTab + 1)
	case "session.tab-prev":
		return m.switchTab(m.activeTab - 1)
	case "session.connect":
		return m.connectToHighlighted()
	case "app.home":
		m.screen = screenHome
		m.home.cursor = 0

	// Navigation
	case "nav.up":
		m.moveCursor(-1)
	case "nav.down":
		m.moveCursor(1)
	case "nav.parent":
		return m.parent()
	case "nav.open":
		return m.open()
	case "nav.page-up":
		m.page(-1)
	case "nav.page-down":
		m.page(1)
	case "nav.first":
		m.toFirst()
	case "nav.last":
		m.toLast()
	case "nav.back":
		return m.historyBack()
	case "nav.forward":
		return m.historyForward()
	case "nav.home":
		return m.goHome()
	case "nav.last-dir":
		return m.goLastDir()
	case "nav.path":
		m.openPrompt("prompt.path")
	case "nav.jump":
		m.openPrompt("prompt.jump")
	case "nav.find":
		return m.findBelow()
	case "nav.send":
		return m.copyCursorTo(m.nextPane(m.Focused), false)
	case "nav.fetch":
		return m.fetchFromOther()
	case "file.copy-to":
		m.openCopyTo(false)
	case "file.move-to":
		m.openCopyTo(true)
	case "session.new-tab":
		m.runAction("session.open")
	case "deploy.preview":
		m.openDeploy()
	case "deploy.run":
		return m.deployCursor()
	case "nav.refresh":
		if m.screen == screenConnections {
			m.loadConnections()
			m.toastInfo("Reloaded ~/.ssh/config")
			break
		}
		m.toastInfo("Refreshed")
		return m.refresh()
	case "nav.filter":
		if m.screen == screenConnections {
			if m.conn == nil {
				m.loadConnections()
			}
			m.conn.filtering = true
			m.mode = "FILTER"
			break
		}
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
		return m.revealInOtherPane()
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
	case "file.edit":
		return m.editCursor()
	case "file.chmod":
		m.openPrompt("prompt.chmod")
	case "file.symlink":
		m.openPrompt("prompt.symlink")
	case "deploy.open":
		m.openDeploy()
	case "view.panes":
		m.openPanes()
	case "terminal.new":
		return m.openTerminalForPane()
	case "session.disconnect":
		if m.screen == screenConnections {
			if entry, ok := m.currentConnection(); ok {
				return m.disconnectHost(entry.Alias)
			}
			m.quitHint = "Put the cursor on a host first"
			break
		}
		return m.disconnectPane()
	case "session.terminal":
		entry, ok := m.currentConnection()
		if !ok {
			m.quitHint = "Put the cursor on a host first"
			break
		}
		return m.openTerminal(entry.Alias)

	case "archive.extract", "archive.extract-here":
		if id == "archive.extract-here" {
			return m.extractCursor(sidePrefix(m.Pane()) + m.Pane().Path)
		}
		m.openExtractPrompt()
	case "archive.compress":
		m.openCompressPrompt()
	case "archive.contents":
		m.preview = true
		m.mode = "PREVIEW"
	case "file.delete":
		m.openDeleteConfirm()

	// View toggles
	case "view.sidebar":
		m.sidebar = !m.sidebar
	case "view.jobs":
		if m.Jobs() == 0 {
			m.toastInfo("No transfers are running")
			break
		}
		m.toastInfo(m.transferSummary())
	case "view.sync":
		m.syncBrowse = !m.syncBrowse
		m.toastInfo(syncMessage(m.syncBrowse))
	case "view.compare":
		m.comparePanes()
	case "view.follow-background":
		// Deliberate: the user asked, so a brief pause is expected. The theme is
		// switched to auto first, so the answer is applied when it arrives.
		m.cfg.UI.Theme = "auto"
		m.Overlay = OverlayNone
		m.toastInfo("Asking the terminal for its background")
		return tea.RequestBackgroundColor

	case "sel.mark":
		m.stage(false)
	case "sel.cut":
		m.stage(true)
	case "sel.paste":
		return m.pasteStaged()
	case "sel.paste-options":
		m.openPasteOptions()
	case "view.staged":
		m.Overlay = OverlayStaged
	default:
		// Nothing should reach here: every action in the registry has a handler.
		// Saying so plainly is better than naming a milestone that has shipped.
		m.toastWarning(a.Label + " is not available in this build")
	}
	return nil
}

// explainUnavailable replaces silence with the reason and the way out.
func (m *Model) explainUnavailable(a actions.Action) {
	switch a.ID {
	case "sel.mark", "sel.cut":
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
	case m.stagedCount() > 0:
		// Escape cancels the pending copy before it leaves the screen: the bar
		// promises it, so it has to be true.
		m.clearStaged()
		m.toastInfo("Marked files cleared")
	case m.Pane().SelectedCount() > 0:
		m.clearSelection()
		m.mode = "NORMAL"
	case m.screen == screenConnections:
		m.screen = m.connReturn
		m.mode = "NORMAL"
	case m.focusTabs:
		// Up out of the tab strip: the launcher.
		m.focusTabs = false
		m.screen = screenHome
		m.home.cursor = 0
		m.mode = "NORMAL"
	case m.screen == screenFiles && len(m.tabs) > 1:
		// With several sessions there is something to choose between, so Escape
		// moves the focus to the tabs rather than leaving the file manager.
		m.focusTabs = true
		m.mode = "TABS"
	case m.screen == screenFiles:
		// A single local tab: the launcher is the only thing above.
		m.screen = screenHome
		m.home.cursor = 0
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
		// The field starts empty with the current path in the hint: prefilling it
		// meant typing "~/Documents" appended to the old path and produced
		// "/Users/me/project~/Documents", which reads as a bug in the app.
		m.prompt = promptState{
			Title:  "Go to path",
			Hint:   "Directory to open · Enter keeps " + m.Pane().Path,
			Action: action,
			open:   true,
		}
	case "prompt.jump":
		m.prompt = promptState{
			Title:  "Jump to path",
			Hint:   "Type a path, ~ for home, or pick a folder below",
			Action: action,
			open:   true,
		}
	case "prompt.extract":
		m.openExtractPrompt()
		return
	case "prompt.compress":
		m.openCompressPrompt()
		return
	case "prompt.copy-to", "prompt.move-to":
		m.openCopyTo(action == "prompt.move-to")
		return
	case "prompt.chmod":
		m.prompt = promptState{Title: "Permissions", Hint: "Octal mode, like 644 or 755", Action: action, open: true}
		m.prompt.input.Set("644")
	case "prompt.symlink":
		entry, ok := m.Pane().CursorEntry()
		if !ok {
			return
		}
		m.prompt = promptState{Title: "Link to " + entry.Name, Hint: "Name for the link", Action: action, open: true}
		m.prompt.input.Set(entry.Name + "-link")
	default:
		return
	}
	m.rebuildPathSuggestions()
	m.Overlay = OverlayPrompt
}

// focusedInput returns the text field that should receive typed or pasted text,
// or nil when the focus is not on one.
func (m *Model) focusedInput() *input {
	switch m.Overlay {
	case OverlayForm:
		if len(m.form.fields) > 0 {
			return &m.form.fields[m.form.focus].in
		}
	case OverlayPrompt:
		return &m.prompt.input
	case OverlayPalette:
		return &m.palette.input
	case OverlayHelp:
		return &m.help.input
	case OverlaySecret:
		// The password dialog has a field of its own, kept separate so it can be
		// masked and never rendered into a general-purpose input.
		return &m.secretInput
	case OverlayAdd:
		// The kind chooser has no text field.
	case OverlayNone, OverlayMenu, OverlayOnboarding, OverlaySettings, OverlayConfirm, OverlayHostKey, OverlayStaged, OverlayDeploy, OverlayPanes, OverlayPaste:
		// These have no text field of their own. The connections filter below is
		// the only field outside an overlay.
	}
	if m.screen == screenConnections && m.conn != nil && m.conn.filtering {
		return &m.conn.filter
	}
	return nil
}

// handlePaste puts pasted text into the focused field.
func (m *Model) handlePaste(text string) {
	target := m.focusedInput()
	if target == nil {
		return
	}
	target.InsertPaste(text)
	// A path prompt has to re-suggest, or the list would describe what was typed
	// before the paste.
	if m.prompt.Action == "prompt.path" {
		m.rebuildPathSuggestions()
	}
}

// hasSuggestions reports whether the prompt is showing a folder list.
func (m *Model) hasSuggestions() bool {
	return m.prompt.Action == "prompt.path" && len(m.prompt.suggestions) > 0
}

// rebuildPathSuggestions refreshes the folder list under the typed path.
func (m *Model) rebuildPathSuggestions() {
	m.prompt.suggestCursor = 0
	m.prompt.suggestPicked = false
	if m.prompt.Action != "prompt.path" {
		m.prompt.suggestions = nil
		return
	}
	m.prompt.suggestions = pathSuggestions(m.prompt.input.Value, 8)
}

// applyPrompt runs whatever the prompt was for.
func (m *Model) applyPrompt(action actions.ID, value string) tea.Cmd {
	value = strings.TrimSpace(value)
	switch action {
	case "prompt.mkdir":
		return m.createDir(value)
	case "prompt.new":
		return m.createFile(value)
	case "prompt.rename":
		return m.renameCursor(value)
	case "prompt.path", "prompt.jump":
		// Typed paths go through the resolver: ~ is the pane's home and a bare
		// name is relative, instead of being glued onto the current path.
		m.goToPath(value)
		return nil
	case "prompt.copy-to":
		return m.copyToPath(value, false)
	case "prompt.move-to":
		return m.copyToPath(value, true)
	case "prompt.find":
		return m.searchBelow(value)
	case "prompt.extract":
		return m.extractCursor(value)
	case "prompt.compress":
		return m.compressTargets(value)
	case "prompt.chmod":
		return m.chmodTargets(value)
	case "prompt.symlink":
		return m.makeSymlink(value)
	}
	return nil
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
func (m *Model) applyConfirmed(action actions.ID, entries []Entry) tea.Cmd {
	switch action {
	case "file.delete":
		return m.deleteTargets(entries)
	case "session.delete":
		m.applyDeleteConnection(m.confirmAlias)
	}
	return nil
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
	m.bgSeq = backgroundSeq(resolved, m.hasDark)
	m.cfg.UI.Theme = name
	m.settings.cursor = indexOf(m.themeNames, resolved.Name)
	m.toastSuccess("Theme: " + resolved.Name)
}

// applyCapabilities rebuilds the keymap once the terminal has told us what it
// can send, dropping bindings it cannot deliver instead of ignoring them.
func (m *Model) applyCapabilities(msg tea.KeyboardEnhancementsMsg) {
	kitty := msg.SupportsKeyDisambiguation()
	rebuilt, err := keymap.BuildWithDeliverer(m.reg, keymap.Options{
		Overrides: m.cfg.Keys.Overrides,
	}, terminalDeliverer{kitty: kitty})
	if err != nil {
		return
	}
	m.keys = rebuilt
	m.resolver = rebuilt.Resolver(m.screenName())
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

// activateOnboarding runs the focused button of the first-run card.
func (m *Model) activateOnboarding() {
	choice := m.onboarding.cursor
	m.Overlay = OverlayNone
	m.markOnboarded()
	switch choice {
	case 0:
		m.toastInfo("Host import arrives with connections (M1)")
	case 1:
		m.toastInfo("Adding a connection arrives with connections (M1)")
	default:
		m.toastInfo("Starting in " + m.Pane().Path)
	}
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

// BackgroundFromEnv reads the terminal background darkness from COLORFGBG,
// which most terminals set and which costs nothing to read. It reports whether
// the answer is known; when it is not, the interface asks the terminal itself
// once it is running (Options.DetectBackground).
func BackgroundFromEnv(env []string) (dark, known bool) {
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name != "COLORFGBG" {
			continue
		}
		// The convention is "foreground;background", either as colour indices
		// (0-15) or, in some terminals, as "default;default".
		parts := strings.Split(value, ";")
		last := strings.TrimSpace(parts[len(parts)-1])
		index, err := strconv.Atoi(last)
		if err != nil {
			continue
		}
		return index < 8, true
	}
	return true, false
}
