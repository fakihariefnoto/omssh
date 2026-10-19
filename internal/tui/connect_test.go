package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// newConnectModel builds an interface over a fixture config whose connections
// screen can reach a session manager with its own prompter, so the dialogs are
// not in the way of testing what happens on either side of them.
func newConnectModel(t *testing.T, prompter session.Prompter) (*Model, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := home + "/.ssh/config"
	mustWrite(t, path, connFixture)

	manager, err := session.New(session.Options{
		HostKeys:  omssh.HostKeysAt(home + "/.ssh/known_hosts"),
		Prompter:  prompter,
		KeepAlive: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.CloseAll() })

	cfg := config.Default()
	cfg.UI.Start = "files"
	m, err := New(Options{
		Config: cfg, Profile: colorprofile.TrueColor, HasDark: true,
		Dir: t.TempDir(), SSHConfigPath: path, Sessions: manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 140, 40)
	m.runAction("session.open")
	return m, path
}

// A finished connection gives the remote pane a filesystem and the strip a tab.
func TestConnectedSessionBindsTheRemotePane(t *testing.T) {
	remote := t.TempDir()
	remoteFS, err := vfs.NewLocal(remote)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, remote+"/public/index.html", "<h1>hi</h1>\n")

	m, _ := newConnectModel(t, session.PrompterFunc{})
	m.conn.cursor = 0 // prod

	target := targetOf(t, m)
	s := &session.Session{ID: "s1", Target: target, FS: remoteFS, Root: "/", Base: remote, State: session.StateReady}

	// The interface handles the result of a connection the way the command does.
	m.handleConnected(connectedMsg{target: target, session: s})

	if m.Panes[1].FS == nil {
		t.Fatal("the remote pane should be bound to the session's filesystem")
	}
	if m.Panes[1].Path != "/" {
		t.Errorf("the remote pane should start at the filesystem's root, got %q", m.Panes[1].Path)
	}
	if m.Panes[1].Base != remote {
		t.Errorf("the pane should know the server-side base %q, got %q", remote, m.Panes[1].Base)
	}
	if m.Focused != 1 {
		t.Error("a fresh connection should focus the remote pane")
	}
	// It lists the remote directory, and says so in the tab strip.
	if len(m.Panes[1].Entries) != 1 || m.Panes[1].Entries[0].Name != "public" {
		t.Errorf("the remote pane lists %+v", m.Panes[1].Entries)
	}
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "prod") {
		t.Errorf("the strip should name the connected host:\n%s", out)
	}
	if strings.Contains(out, "No connection yet") {
		t.Errorf("the remote pane should no longer say it is unconnected:\n%s", out)
	}
}

// A failed connection says why and leaves the pane alone.
func TestFailedConnectionExplains(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{})
	target := targetOf(t, m)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unknown host", omssh.ErrUnknownHost, "host key confirmed"},
		{"changed key", omssh.ErrHostKeyMismatch, "host key changed"},
		{"timeout", context.DeadlineExceeded, "did not answer"},
		{"anything else", errors.New("connection refused"), "connection refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m2, _ := newConnectModel(t, session.PrompterFunc{})
			m2.handleConnected(connectedMsg{target: target, err: tc.err})
			if m2.Panes[1].FS != nil {
				t.Error("a failed connection must not bind the pane")
			}
			if len(m2.toasts) == 0 {
				t.Fatal("a failure should be reported")
			}
			last := m2.toasts[len(m2.toasts)-1]
			if last.kind != toastError {
				t.Errorf("a failure should be shown as an error, got kind %v", last.kind)
			}
			if !strings.Contains(last.text, tc.want) {
				t.Errorf("the message should mention %q, got %q", tc.want, last.text)
			}
		})
	}
}

// Enter on a host starts a connection; the tab says so while it is in flight.
func TestEnterOnAHostStartsConnecting(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{
		ConsentFunc: func(*omssh.UnknownHostError) (omssh.Consent, error) {
			return omssh.ConsentRefuse, nil
		},
	})
	m.conn.cursor = 0

	// The action builds a target from the highlighted host and returns a command.
	cmd := m.runAction("session.connect")
	if cmd == nil {
		t.Fatal("connecting should return a command, so the interface stays responsive")
	}
	if !m.connecting["prod"] {
		t.Error("the host should be marked as connecting")
	}
	var state ConnState
	for _, tab := range m.tabs {
		if tab.Host == "prod" {
			state = tab.State
		}
	}
	if state != StateConnecting {
		t.Errorf("the tab should show connecting, got %v", state)
	}
	if !strings.Contains(ansi.Strip(render(m)), "Connecting to prod") {
		t.Error("the interface should say what it is doing")
	}
}

// The first-connect dialog offers three answers, and Escape refuses rather than
// storing anything.
func TestHostKeyDialogAnswers(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{})
	unknown := &omssh.UnknownHostError{
		Host:        "prod",
		Fingerprint: "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		KeyType:     "ssh-ed25519",
		KnownHosts:  m.conn.path,
	}
	request := &promptRequest{kind: promptHostKey, unknown: unknown, answer: make(chan promptAnswer, 1)}
	m.handlePromptMsg(promptMsg{request: request})
	if m.Overlay != OverlayHostKey {
		t.Fatal("an unknown host key should open its dialog")
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"Unknown host key", unknown.Fingerprint, "ssh-ed25519", "Remember", "Esc refuse"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dialog should show %q:\n%s", want, out)
		}
	}

	// Enter remembers.
	press(t, m, keyNamed(tea.KeyEnter))
	if answer := <-request.answer; answer.consent != omssh.ConsentRemember {
		t.Errorf("Enter should remember, got %v", answer.consent)
	}
	if m.Overlay != OverlayNone {
		t.Error("answering should close the dialog")
	}

	// Escape refuses.
	second := &promptRequest{kind: promptHostKey, unknown: unknown, answer: make(chan promptAnswer, 1)}
	m.handlePromptMsg(promptMsg{request: second})
	press(t, m, keyNamed(tea.KeyEsc))
	if answer := <-second.answer; answer.consent != omssh.ConsentRefuse {
		t.Errorf("Escape should refuse, got %v", answer.consent)
	}
}

// The password dialog masks what is typed and can ask for it to be remembered.
func TestPasswordDialogMasksAndRemembers(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{})
	request := &promptRequest{kind: promptPassword, target: targetOf(t, m), answer: make(chan promptAnswer, 1)}
	m.handlePromptMsg(promptMsg{request: request})
	if m.Overlay != OverlaySecret {
		t.Fatal("a password request should open its dialog")
	}

	for _, ch := range "hunter2" {
		press(t, m, run(ch, 0, string(ch)))
	}
	out := ansi.Strip(render(m))
	if strings.Contains(out, "hunter2") {
		t.Errorf("the password should be masked on screen:\n%s", out)
	}
	if !strings.Contains(out, "•••••••") {
		t.Errorf("the dialog should show one dot per character:\n%s", out)
	}

	press(t, m, keyNamed(tea.KeyTab)) // remember
	press(t, m, keyNamed(tea.KeyEnter))
	answer := <-request.answer
	if answer.secret.Reveal() != "hunter2" {
		t.Errorf("the dialog should send what was typed, got %q", answer.secret.Reveal())
	}
	if !answer.remember {
		t.Error("Tab should have asked for it to be remembered")
	}
}

// A password prompt with nothing typed sends nothing, so Enter cannot
// accidentally submit an empty secret.
func TestEmptyPasswordIsNotSubmitted(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{})
	request := &promptRequest{kind: promptPassword, answer: make(chan promptAnswer, 1)}
	m.handlePromptMsg(promptMsg{request: request})
	press(t, m, keyNamed(tea.KeyEnter))
	select {
	case answer := <-request.answer:
		t.Fatalf("an empty password should not be sent, got %+v", answer)
	case <-time.After(20 * time.Millisecond):
	}
	if m.Overlay != OverlaySecret {
		t.Error("the dialog should stay open")
	}
}

// Quitting resolves a prompt nobody can answer, so a connection goroutine is not
// left waiting forever.
func TestQuittingAnswersPendingPrompts(t *testing.T) {
	m, _ := newConnectModel(t, session.PrompterFunc{})
	request := &promptRequest{kind: promptPassword, answer: make(chan promptAnswer, 1)}
	m.handlePromptMsg(promptMsg{request: request})

	m.answerPendingPrompt()
	select {
	case answer := <-request.answer:
		if answer.err == nil {
			t.Error("a prompt abandoned at exit should report why")
		}
	case <-time.After(time.Second):
		t.Fatal("the waiting goroutine was never answered")
	}
}

// targetOf is the highlighted host as a target, which is what the interface
// builds before connecting.
func targetOf(t *testing.T, m *Model) omssh.Target {
	t.Helper()
	entry, ok := m.currentConnection()
	if !ok {
		t.Fatal("no host is highlighted")
	}
	target, err := omssh.TargetFrom(sshconfig.Spec{
		Alias: entry.Alias, HostName: entry.HostName, User: entry.User,
		Port: entry.Port, IdentityFile: entry.Identity, ProxyJump: entry.ProxyJump,
	})
	if err != nil {
		t.Fatal(err)
	}
	return target
}
