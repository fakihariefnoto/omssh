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
	m.connecting[alias] = true
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

	// A session gets a tab and the remote pane.
	m.addSessionTab(msg.session)
	if len(m.Panes) > 1 {
		m.Panes[1].Bind(msg.session.FS, msg.session.Target.Alias, msg.session.Root)
		m.Panes[1].Base = msg.session.Base
		m.Focused = 1
	}
	m.tabState(msg.target.Alias, StateReady)
	m.mode = "NORMAL"
	m.toastSuccess("Connected to " + msg.target.Alias)
	// The listing goes off the update loop: a home directory on a slow link is
	// many round trips, and the interface has just become usable.
	return m.loadPaneAsync(1)
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
	m.tabs = append(m.tabs, Tab{Title: alias, Host: alias, State: state})
	m.activeTab = len(m.tabs) - 1
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
func (m *Model) switchTab(index int) tea.Cmd {
	if len(m.tabs) == 0 {
		return nil
	}
	// Wrap, so ctrl+pgup from the first tab lands on the last.
	index = ((index % len(m.tabs)) + len(m.tabs)) % len(m.tabs)
	m.activeTab = index
	tab := m.tabs[index]
	if tab.Host == "" {
		// The local tab: focus the local pane, leave the remote as it was.
		m.Focused = 0
		return nil
	}
	sess, ok := m.live[tab.Host]
	if !ok || len(m.Panes) < 2 {
		m.toastInfo(tab.Host + " is no longer connected")
		return nil
	}
	m.Panes[1].Bind(sess.FS, sess.Target.Alias, sess.Root)
	m.Panes[1].Base = sess.Base
	m.Focused = 1
	return m.loadPaneAsync(1)
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
