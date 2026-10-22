package tui

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/log"
	"github.com/fakihariefnoto/omssh/internal/session"
	"github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// A connection needs three things from a person: consent for a host key, a
// password, a passphrase. The connection runs in its own goroutine and waits for
// the answer, because that is the honest shape of a handshake; the interface
// answers over a channel rather than blocking its update loop.

// promptKind is what a prompt is asking for.
type promptKind int

const (
	promptHostKey promptKind = iota
	promptPassword
	promptPassphrase
)

// promptRequest is one question from a connection, with the channel it is waiting
// on.
type promptRequest struct {
	kind    promptKind
	target  ssh.Target
	unknown *ssh.UnknownHostError
	keyPath string
	retry   bool
	answer  chan promptAnswer
}

// promptAnswer is what the interface decided.
type promptAnswer struct {
	consent  ssh.Consent
	secret   log.Secret
	remember bool
	err      error
}

// uiPrompter is the session.Prompter that asks the interface.
type uiPrompter struct{ requests chan *promptRequest }

func (p uiPrompter) ask(request *promptRequest) promptAnswer {
	request.answer = make(chan promptAnswer, 1)
	// The send is not selectable on a context: the connection goroutine owns no
	// context the interface can see, and a prompt that is never answered is
	// resolved when the program quits (see answerPendingPrompt).
	p.requests <- request
	return <-request.answer
}

// ConsentHostKey asks whether to trust a key that has never been seen.
func (p uiPrompter) ConsentHostKey(unknown *ssh.UnknownHostError) (ssh.Consent, error) {
	answer := p.ask(&promptRequest{kind: promptHostKey, unknown: unknown})
	return answer.consent, answer.err
}

// Password asks for a password.
func (p uiPrompter) Password(target ssh.Target, retry bool) (log.Secret, bool, error) {
	answer := p.ask(&promptRequest{kind: promptPassword, target: target, retry: retry})
	return answer.secret, answer.remember, answer.err
}

// Passphrase asks for a key's passphrase.
func (p uiPrompter) Passphrase(keyPath string) (log.Secret, bool, error) {
	answer := p.ask(&promptRequest{kind: promptPassphrase, keyPath: keyPath})
	return answer.secret, answer.remember, answer.err
}

// promptMsg delivers a question to the update loop.
type promptMsg struct{ request *promptRequest }

// waitForPrompt reads the next question from a connection.
func waitForPrompt(requests chan *promptRequest) tea.Cmd {
	return func() tea.Msg {
		request, ok := <-requests
		if !ok {
			return nil
		}
		return promptMsg{request: request}
	}
}

// connectedMsg reports the end of a connection attempt.
type connectedMsg struct {
	target  ssh.Target
	session *session.Session
	err     error
}

// connect starts a connection. It runs in a command, so the interface stays
// responsive while the handshake happens and while a prompt waits.
func (m *Model) connect(target ssh.Target) tea.Cmd {
	if m.sessions == nil {
		m.toastError("Connections are not available in this build")
		return nil
	}
	alias := target.Alias
	if m.connecting == nil {
		m.connecting = map[string]bool{}
	}
	if m.connecting[alias] {
		// Pressing Enter twice should not dial twice.
		return nil
	}
	m.connecting[alias] = true
	m.ensureTab(alias)
	m.tabState(alias, StateConnecting)
	m.toastInfo("Connecting to " + alias + "…")

	ctx := m.connectContext()
	manager := m.sessions
	return func() tea.Msg {
		live, err := manager.Connect(ctx, target)
		return connectedMsg{target: target, session: live, err: err}
	}
}

// connectContext is the context every connection shares, cancelled on exit, so a
// dial in flight does not outlive the program.
func (m *Model) connectContext() context.Context {
	if m.connectCtx == nil {
		m.connectCtx, m.cancelConnect = context.WithCancel(context.Background())
	}
	return m.connectCtx
}

// handleConnected folds a finished connection into the interface.
func (m *Model) handleConnected(msg connectedMsg) tea.Cmd {
	delete(m.connecting, msg.target.Alias)
	if msg.err != nil {
		m.tabState(msg.target.Alias, StateError)
		m.toastError(connectionMessage(msg.target, msg.err))
		return nil
	}

	// A session gets a tab and a pane of its own: with four panes, four sessions
	// can be on screen at once.
	m.addSessionTab(msg.session)
	if name := m.pendingDeploy; name != "" {
		m.pendingDeploy = ""
		if profile, ok := m.cfg.Deploy.Profiles[name]; ok && profile.Host == msg.target.Alias {
			m.Overlay = OverlayDeploy
			return m.reviewDeploy(name, profile, msg.session.FS)
		}
	}
	index := m.paneForSession(msg.session)
	m.Focused = index
	m.tabState(msg.target.Alias, StateReady)
	m.noteRecent(msg.target.Alias)
	m.mode = "NORMAL"
	// Four panes is the dashboard's limit; the fifth host gets a tab and waits.
	if m.paneCount() >= maxPanes && !m.paneShows(index, msg.session.Target.Alias) {
		m.toastInfo("Four panes are full · V chooses which sessions are shown")
	}
	m.toastSuccess("Connected to " + msg.target.Alias)
	// The listing goes off the update loop: a home directory on a slow link is
	// many round trips, and the interface has just become usable.
	return m.loadPaneAsync(index)
}

// paneIndexFor is the pane showing a host, or -1.
func (m *Model) paneIndexFor(alias string) int {
	for i := 0; i < m.paneCount(); i++ {
		if m.Panes[i].Host == alias {
			return i
		}
	}
	return -1
}

// maxPanes is the dashboard's limit: four sessions, two by two.
const maxPanes = 4

// paneCount is how many panes the model has, never more than maxPanes.
func (m *Model) paneCount() int { return min(len(m.Panes), maxPanes) }

// paneShows reports whether a pane is currently showing a host.
func (m *Model) paneShows(index int, alias string) bool {
	return index >= 0 && index < len(m.Panes) && m.Panes[index].Host == alias
}

// paneForSession finds the pane showing a session, binds an empty one, or makes a
// new one. When all four are taken it replaces the focused pane — the one the
// person is looking at — and says which host it displaced.
func (m *Model) paneForSession(s *session.Session) int {
	alias := s.Target.Alias
	for i := range m.Panes {
		if m.Panes[i].Host == alias {
			m.bindPane(i, s)
			return i
		}
	}
	for i := range m.Panes {
		if m.Panes[i].Side == SideRemote && m.Panes[i].Host == "" {
			m.bindPane(i, s)
			return i
		}
	}
	if len(m.Panes) < maxPanes {
		m.Panes = append(m.Panes, NewRemotePane())
		m.bindPane(len(m.Panes)-1, s)
		return len(m.Panes) - 1
	}
	// Full: replace the pane with the keys, or the last one when the keys are in
	// the local pane.
	index := m.Focused
	if m.Panes[index].Side == SideLocal {
		index = len(m.Panes) - 1
	}
	old := m.Panes[index].Host
	m.bindPane(index, s)
	if old != "" {
		m.toastInfo(alias + " replaced " + old + " in this pane · V to choose panes")
	}
	return index
}

// bindPane points a pane at a session.
func (m *Model) bindPane(index int, s *session.Session) {
	if index < 0 || index >= len(m.Panes) {
		return
	}
	m.Panes[index].Bind(s.FS, s.Target.Alias, s.Root)
	m.Panes[index].Base = s.Base
}

// addSessionTab adds or focuses the tab for a session.
func (m *Model) addSessionTab(s *session.Session) {
	for i, tab := range m.tabs {
		if tab.Host == s.Target.Alias {
			m.activeTab = i
			m.tabs[i].State = StateReady
			return
		}
	}
	m.tabs = append(m.tabs, Tab{Title: s.Target.Alias, Host: s.Target.Alias, State: StateReady})
	m.activeTab = len(m.tabs) - 1
	m.rememberSession(s)
}

// rememberSession records a session so its tab can be switched back to.
func (m *Model) rememberSession(s *session.Session) {
	if m.live == nil {
		m.live = map[string]*session.Session{}
	}
	m.live[s.Target.Alias] = s
}

// tabState updates a tab's connection state, adding it if it is not there yet so
// a failure is visible on the tab that was connecting.
func (m *Model) tabState(alias string, state ConnState) {
	for i := range m.tabs {
		if m.tabs[i].Host == alias {
			m.tabs[i].State = state
			return
		}
	}
	// No tab: nothing to mark. A tab is created when a connection starts, so a
	// failure always has one to land on; inventing one here left phantom tabs for
	// hosts that never connected, and choosing them said "no longer connected".
}

// ensureTab makes sure a host has a tab, without changing the open one. It is
// called when a connection starts: the tab is the promise, the session is the
// result.
func (m *Model) ensureTab(alias string) {
	for i := range m.tabs {
		if m.tabs[i].Host == alias {
			return
		}
	}
	m.tabs = append(m.tabs, Tab{Title: alias, Host: alias, State: StateConnecting})
}

// connectionMessage turns a connection failure into something a person can act
// on: the cause first, then what to try.
func connectionMessage(target ssh.Target, err error) string {
	switch {
	case errors.Is(err, ssh.ErrUnknownHost):
		return target.Alias + " needs its host key confirmed (see the dialog)"
	case errors.Is(err, ssh.ErrHostKeyMismatch):
		return target.Alias + ": the host key changed. Nothing was stored; investigate before connecting"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return target.Alias + " did not answer in time"
	default:
		return target.Alias + ": " + err.Error()
	}
}

// connectToHighlighted connects to the host under the cursor on the connections
// screen, using its config for everything else.
func (m *Model) connectToHighlighted() tea.Cmd {
	entry, ok := m.currentConnection()
	if !ok {
		return nil
	}
	if entry.Cloud {
		return m.openCloudPane(entry.Alias)
	}
	spec := sshconfig.Spec{
		Alias:        entry.Alias,
		HostName:     entry.HostName,
		User:         entry.User,
		Port:         entry.Port,
		IdentityFile: entry.Identity,
		ProxyJump:    entry.ProxyJump,
	}
	target, err := ssh.TargetFrom(spec)
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	return m.connect(target)
}

// answerPrompt resolves the dialog that is open and re-arms the listener.
func (m *Model) answerPrompt(answer promptAnswer) tea.Cmd {
	request := m.activePrompt
	m.activePrompt = nil
	m.Overlay = OverlayNone
	m.secretInput.Clear()
	if request != nil {
		request.answer <- answer
	}
	if m.sessions != nil {
		return waitForPrompt(m.prompts)
	}
	return nil
}

// answerPendingPrompt resolves a prompt nobody can answer any more, so a
// connection goroutine does not wait forever when the program quits.
func (m *Model) answerPendingPrompt() {
	if m.activePrompt == nil {
		return
	}
	m.activePrompt.answer <- promptAnswer{err: errors.New("the interface closed")}
	m.activePrompt = nil
}

// handlePromptMsg opens the dialog a connection is waiting on.
func (m *Model) handlePromptMsg(msg promptMsg) {
	m.activePrompt = msg.request
	m.secretInput.Clear()
	switch msg.request.kind {
	case promptHostKey:
		m.Overlay = OverlayHostKey
	case promptPassword, promptPassphrase:
		m.Overlay = OverlaySecret
	}
}

// handleHostKeyKey drives the first-connect dialog. There is no default that
// stores a key: remembering is a deliberate keypress, and Escape refuses.
func (m *Model) handleHostKeyKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter", "r":
		return m.answerPrompt(promptAnswer{consent: ssh.ConsentRemember})
	case "o":
		return m.answerPrompt(promptAnswer{consent: ssh.ConsentOnce})
	case "esc", "q":
		return m.answerPrompt(promptAnswer{consent: ssh.ConsentRefuse})
	}
	return nil
}

// handleSecretKey drives the password and passphrase dialogs.
func (m *Model) handleSecretKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return m.answerPrompt(promptAnswer{err: errors.New("cancelled")})
	case "enter":
		if strings.TrimSpace(m.secretInput.Value) == "" {
			return nil
		}
		return m.answerPrompt(promptAnswer{secret: log.Secret(m.secretInput.Value), remember: m.rememberSecret})
	case "tab":
		m.rememberSecret = !m.rememberSecret
	case "ctrl+u":
		m.secretInput.Clear()
	default:
		m.secretInput.Handle(msg)
	}
	return nil
}

// renderHostKey draws the first-connect dialog: the facts, both choices, and no
// default that stores anything silently.
func (m *Model) renderHostKey(l layout) string {
	width := cardWidth(l, 52, 78)
	content := cardContent(width)
	unknown := m.activePrompt.unknown

	lines := []string{
		m.st.CardTitle.Render("Unknown host key"),
		"",
		m.st.CardText.Render(truncate(unknown.Host+" is not in "+unknown.KnownHosts, content)),
		"",
		m.st.StatusMuted.Render(padRight("key type", 12)) + m.st.CardText.Render(unknown.KeyType),
		m.st.StatusMuted.Render(padRight("fingerprint", 12)) + m.st.Accent.Bold(true).Render(unknown.Fingerprint),
		"",
		m.st.CardMuted.Render(truncate("Compare it with the server's own key before trusting it.", content)),
		"",
		m.button(true, "Remember") + "  " + m.button(false, "Only this time") + "  " + m.button(false, "Refuse"),
	}
	if m.activePrompt.target.Alias != "" {
		lines = append(lines, "", m.st.CardMuted.Render("for "+m.activePrompt.target.Alias))
	}
	lines = append(lines, "", m.st.CardMuted.Render("Enter remember · o once · Esc refuse"))
	return m.card(width, lines)
}

// renderSecret draws the password and passphrase dialogs. The value is masked:
// a password on screen is a password in someone's shoulder-surfing range.
func (m *Model) renderSecret(l layout) string {
	width := cardWidth(l, 46, 70)
	content := cardContent(width)

	title := "Password"
	hint := "for " + m.activePrompt.target.String()
	if m.activePrompt.kind == promptPassphrase {
		title = "Key passphrase"
		hint = "for " + m.activePrompt.keyPath
	}
	if m.activePrompt.retry {
		hint = "the stored password was refused; " + hint
	}

	masked := strings.Repeat("•", len([]rune(m.secretInput.Value)))
	remember := "[ ] remember in the keyring"
	if m.rememberSecret {
		remember = "[x] remember in the keyring"
	}
	if !m.keyringAvailable() {
		remember = m.st.CardMuted.Render("no keyring here, so nothing can be remembered")
	}

	lines := []string{
		m.st.CardTitle.Render(title),
		"",
		m.st.CardMuted.Render(truncate(hint, content)),
		m.st.PathInput.Width(content).Render(truncate(masked+"▏", content)),
		"",
		m.st.CardText.Render(remember),
		"",
		m.st.CardMuted.Render("Enter send · Tab remember · Esc cancel"),
	}
	return m.card(width, lines)
}

// keyringAvailable reports whether secrets can be stored, so the dialog does not
// offer something that cannot happen.
func (m *Model) keyringAvailable() bool {
	if m.sessions == nil {
		return false
	}
	return m.sessions.Store().Available()
}

// newSessionManager builds the manager the interface talks to.
func newSessionManager(requests chan *promptRequest) (*session.Manager, error) {
	return session.New(session.Options{Prompter: uiPrompter{requests: requests}})
}

// switchTab moves to another session tab, binding the remote pane to that
// session. The local tab keeps the remote pane where it is, so a copy between
// the two sides survives a look at the local machine.
//
// dive decides whether the focus moves into the pane as well: choosing a tab from
// the strip previews it, and Enter dives in.
func (m *Model) switchTab(index int) tea.Cmd {
	return m.selectTab(index, true)
}

func (m *Model) selectTab(index int, dive bool) tea.Cmd {
	if len(m.tabs) == 0 {
		return nil
	}
	// Wrap, so [ from the first tab lands on the last.
	index = ((index % len(m.tabs)) + len(m.tabs)) % len(m.tabs)
	m.activeTab = index
	tab := m.tabs[index]
	if tab.Host == "" {
		// The local tab: show the local pane, leave the remote as it was.
		if dive {
			m.Focused = 0
		}
		return nil
	}
	sess, ok := m.live[tab.Host]
	if !ok {
		// The session ended (the server dropped it, or it was never finished).
		// Choosing its tab again is a request to have it back, not a dead end.
		target, found := m.targetForAlias(tab.Host)
		if !found {
			m.toastError(tab.Host + " is no longer connected, and is not in ~/.ssh/config")
			return nil
		}
		m.toastInfo("Reconnecting to " + tab.Host + "…")
		return m.connect(target)
	}
	// The pane may already be showing this session. Rebuilding its listing on
	// every keystroke is what made browsing tabs feel slow: a tab switch is a
	// change of focus, not a reason to ask the server again.
	paneIndex := m.paneIndexFor(tab.Host)
	already := paneIndex >= 0 && len(m.Panes[paneIndex].Entries) > 0
	if paneIndex < 0 {
		paneIndex = m.paneForSession(sess)
	}
	if dive {
		m.Focused = paneIndex
	}
	if already {
		m.ensureVisible()
		return nil
	}
	return m.loadPaneAsync(paneIndex)
}

// handleTabKey drives the tab strip once it has the focus: choose with h/l or
// Tab, dive with Enter, leave with Escape.
func (m *Model) handleTabKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "h", "left", "shift+tab", "k", "up":
		cmd := m.selectTab(m.activeTab-1, false)
		return m, cmd
	case "l", "right", "tab", "j", "down":
		cmd := m.selectTab(m.activeTab+1, false)
		return m, cmd
	case "home", "g":
		cmd := m.selectTab(0, false)
		return m, cmd
	case "end", "G":
		cmd := m.selectTab(len(m.tabs)-1, false)
		return m, cmd
	case "enter", "space":
		m.focusTabs = false
		m.mode = "NORMAL"
		cmd := m.selectTab(m.activeTab, true)
		return m, cmd
	case "esc":
		m.focusTabs = false
		m.screen = screenHome
		m.home.cursor = 0
		m.mode = "NORMAL"
		return m, nil
	case "d":
		// Closing the chosen tab is the one destructive thing the strip offers.
		cmd := m.closeTab(m.activeTab)
		return m, cmd
	}
	// Digits and the global keys still work from the strip.
	return m.handleNormalKey(msg)
}

// closeTab drops a session tab, closing the session it belongs to.
func (m *Model) closeTab(index int) tea.Cmd {
	if index < 0 || index >= len(m.tabs) {
		return nil
	}
	tab := m.tabs[index]
	if tab.Host == "" {
		m.toastInfo("The local tab stays: it is where you started")
		return nil
	}
	if sess, ok := m.live[tab.Host]; ok && m.sessions != nil {
		_ = m.sessions.Close(sess.ID)
	}
	delete(m.live, tab.Host)
	m.tabs = append(m.tabs[:index], m.tabs[index+1:]...)
	if m.activeTab >= len(m.tabs) {
		m.activeTab = max(0, len(m.tabs)-1)
	}
	if len(m.tabs) == 1 {
		m.focusTabs = false
	}
	cmd := m.selectTab(m.activeTab, false)
	m.toastInfo("Closed " + tab.Host)
	return cmd
}

// tabIndexFromKey reads the digit out of "alt+3".
func tabIndexFromKey(key string) int {
	for _, r := range key {
		if r >= '1' && r <= '9' {
			return int(r - '1')
		}
	}
	return 0
}
