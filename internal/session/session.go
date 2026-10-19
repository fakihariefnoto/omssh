// Package session owns what the interface needs from a connection: reaching a
// host, answering whatever it asks for, handing back a filesystem, and keeping
// the connection until nothing wants it any more.
//
// It sits between the transport (internal/ssh) and the interface, so the TUI
// never dials anything itself, and the rules about credentials, consent and
// retrying live in one place rather than in a key handler.
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"github.com/fakihariefnoto/omssh/internal/keys"
	"github.com/fakihariefnoto/omssh/internal/log"
	"github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/vfs"
	sftpfs "github.com/fakihariefnoto/omssh/internal/vfs/sftp"
)

// State is the health of a session, which the interface shows on its tab.
type State int

// States, in the order a session moves through them.
const (
	StateConnecting State = iota
	StateReady
	StateBusy
	StateError
	StateClosed
)

// Label is a word for the state, so nothing relies on colour alone.
func (s State) Label() string {
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
		return "closed"
	}
}

// Prompter answers what a connection needs from a person.
//
// It blocks, by design: a connection runs in its own goroutine and waits for the
// answer. An interface that cannot block finishes its update loop first and
// answers from a channel, which is what the TUI does.
type Prompter interface {
	// ConsentHostKey is asked for a key that has never been seen.
	ConsentHostKey(unknown *ssh.UnknownHostError) (ssh.Consent, error)
	// Password is asked when no usable password is stored. retry is true when a
	// stored one was tried and refused.
	Password(target ssh.Target, retry bool) (log.Secret, bool, error)
	// Passphrase is asked for an encrypted key that the agent could not unlock.
	Passphrase(keyPath string) (log.Secret, bool, error)
}

// PrompterFunc adapts functions to the interface, which is what a test wants.
type PrompterFunc struct {
	ConsentFunc    func(*ssh.UnknownHostError) (ssh.Consent, error)
	PasswordFunc   func(ssh.Target, bool) (log.Secret, bool, error)
	PassphraseFunc func(string) (log.Secret, bool, error)
}

// ConsentHostKey defers to the function, or refuses when there is none: a prompt
// that cannot be shown must never be read as consent.
func (p PrompterFunc) ConsentHostKey(unknown *ssh.UnknownHostError) (ssh.Consent, error) {
	if p.ConsentFunc == nil {
		return ssh.ConsentRefuse, nil
	}
	return p.ConsentFunc(unknown)
}

// Password defers to the function, or reports that nothing was offered.
func (p PrompterFunc) Password(target ssh.Target, retry bool) (log.Secret, bool, error) {
	if p.PasswordFunc == nil {
		return "", false, errors.New("session: no password was offered")
	}
	return p.PasswordFunc(target, retry)
}

// Passphrase defers to the function, or reports that none was offered.
func (p PrompterFunc) Passphrase(keyPath string) (log.Secret, bool, error) {
	if p.PassphraseFunc == nil {
		return "", false, errors.New("session: no passphrase was offered")
	}
	return p.PassphraseFunc(keyPath)
}

// Session is one live connection with a filesystem on it.
type Session struct {
	ID     string
	Target ssh.Target
	Client *ssh.Client
	FS     vfs.FS
	State  State
	Err    error
	Opened time.Time
	// Root is the path inside the filesystem that a pane should show after
	// connecting. It is always "/", because a vfs.FS is rooted at the directory
	// it was opened over: the server-side path lives in Base, for display.
	Root string
	// Base is the server-side directory the filesystem is rooted at — the user's
	// home, in practice — for the interface to show and for the breadcrumb to
	// name.
	Base string
}

// Label names the session the way a tab does.
func (s *Session) Label() string { return s.Target.Alias }

// Manager owns the connection pool and the live sessions.
type Manager struct {
	pool   *ssh.Pool
	hosts  ssh.HostKeys
	store  *keys.Store
	prompt Prompter
	// identityFiles are tried after the one a config names, which is what ssh
	// itself does.
	identityFiles []string
	// openSFTP opens the subsystem, replaced in tests that have no server.
	openSFTP func(*ssh.Client) (*sftp.Client, string, error)

	mu       sync.Mutex
	sessions map[string]*Session
	// held keeps the pool reference for each live connection, so a session can
	// hand it back when it closes.
	held     map[string]func()
	sequence int
}

// Options configures a manager.
type Options struct {
	// HostKeys is the known_hosts store. Defaults to ~/.ssh/known_hosts.
	HostKeys ssh.HostKeys
	// Store is the secret store. Defaults to the OS keyring; when there is none,
	// every secret is asked for instead.
	Store *keys.Store
	// Prompter answers what cannot be answered from storage. Required.
	Prompter Prompter
	// IdentityFiles are tried when a config names no key.
	IdentityFiles []string
	// KeepAlive is the pool's heartbeat interval: zero means the default, a
	// negative value disables heartbeats.
	KeepAlive time.Duration
	// DialOptions are passed to every dial, for tests.
	DialOptions []ssh.Option
	// SFTPRoot overrides the remote directory "/" maps to. Empty means the
	// server's working directory, which is the user's home.
	SFTPRoot string
}

// New builds a manager and its pool.
func New(options Options) (*Manager, error) {
	hosts := options.HostKeys
	if hosts.Path() == "" {
		defaultHosts, err := ssh.DefaultHostKeys()
		if err != nil {
			return nil, err
		}
		hosts = defaultHosts
	}
	store := options.Store
	if store == nil {
		store = keys.Default("omssh")
	}
	identityFiles := options.IdentityFiles
	if len(identityFiles) == 0 {
		identityFiles = defaultIdentityFiles()
	}

	manager := &Manager{
		hosts:         hosts,
		store:         store,
		prompt:        options.Prompter,
		identityFiles: identityFiles,
		sessions:      map[string]*Session{},
		openSFTP: func(client *ssh.Client) (*sftp.Client, string, error) {
			system, err := sftp.NewClient(client.SSH())
			if err != nil {
				return nil, "", fmt.Errorf("session: starting the SFTP subsystem on %s: %w", client.Target().Alias, err)
			}
			return system, options.SFTPRoot, nil
		},
	}
	dial := append([]ssh.Option{ssh.WithHostKeyConsent(manager.consent)}, options.DialOptions...)
	manager.pool = ssh.NewPool(ssh.PoolOptions{
		AuthFor:   manager.poolAuthFor,
		Keys:      hosts,
		Dial:      dial,
		KeepAlive: options.KeepAlive,
	})
	return manager, nil
}

// Sessions returns the live sessions, newest last.
func (m *Manager) Sessions() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		out = append(out, session)
	}
	return out
}

// Get returns a session by id.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	return session, ok
}

// Len is how many sessions are live.
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// HostKeys is the store the manager verifies against, so the interface can offer
// "forget this host" without rebuilding the manager.
func (m *Manager) HostKeys() ssh.HostKeys { return m.hosts }

// Store is the secret store, for the same reason.
func (m *Manager) Store() *keys.Store { return m.store }

// Connect reaches a host and hands back a session with a filesystem on it.
//
// The order of business is deliberate: credentials that are already stored, a
// prompt for whatever is missing, consent for a key that has never been seen,
// and the subsystem last. A failure anywhere leaves nothing half-open.
func (m *Manager) Connect(ctx context.Context, target ssh.Target) (*Session, error) {
	if m.prompt == nil {
		return nil, errors.New("session: no prompter, so nothing can be asked of the user")
	}

	_, usedStoredSecret := m.authFor(target)
	client, err := m.acquire(ctx, target)
	if err != nil {
		// A stored secret that is refused is stale — the server was rebuilt, or
		// someone changed the password — so it is dropped and the person is asked
		// once more rather than being told to go and clear it by hand.
		if usedStoredSecret && isAuthFailure(err) {
			_ = m.store.Forget(target.HostKeyAlias())
			client, err = m.acquire(ctx, target)
		}
		if err != nil {
			return nil, err
		}
	}

	system, base, err := m.openSFTP(client)
	if err != nil {
		m.release(client)
		return nil, err
	}
	fsys, err := sftpfs.New(system, base, client.Target().Alias)
	if err != nil {
		_ = system.Close()
		m.release(client)
		return nil, err
	}

	return m.addSession(target, client, fsys), nil
}

// acquire takes a connection from the pool, holding a reference for the session.
func (m *Manager) acquire(ctx context.Context, target ssh.Target) (*ssh.Client, error) {
	client, release, err := m.pool.Acquire(ctx, target)
	if err != nil {
		return nil, err
	}
	// The reference is held until the session closes, which is what makes a
	// second tab onto the same host cheap.
	m.mu.Lock()
	if m.held == nil {
		m.held = map[string]func(){}
	}
	m.held[client.Target().PoolKey()] = release
	m.mu.Unlock()
	return client, nil
}

// release drops the pool reference for a connection.
func (m *Manager) release(client *ssh.Client) {
	key := client.Target().PoolKey()
	m.mu.Lock()
	release, ok := m.held[key]
	if ok {
		delete(m.held, key)
	}
	m.mu.Unlock()
	if ok {
		release()
	}
}

// consent asks the prompter, which is the only thing that can decide whether a
// key should be stored.
func (m *Manager) consent(unknown *ssh.UnknownHostError) (ssh.Consent, error) {
	return m.prompt.ConsentHostKey(unknown)
}

// poolAuthFor is the credential function the pool calls when it needs to dial.
func (m *Manager) poolAuthFor(target ssh.Target) ssh.Auth {
	auth, _ := m.authFor(target)
	return auth
}

// authFor builds the credential chain for a target from storage and prompts. The
// second result says whether a stored password was used, which is what tells
// Connect that a refusal means stale storage rather than a wrong entry.
func (m *Manager) authFor(target ssh.Target) (ssh.Auth, bool) {
	auth := ssh.Auth{Agent: true}

	identities := make([]string, 0, len(m.identityFiles)+1)
	if target.Identity != "" {
		identities = append(identities, target.Identity)
	}
	for _, path := range m.identityFiles {
		if path != target.Identity {
			identities = append(identities, path)
		}
	}
	auth.Keys = identities

	auth.Passphrase = func(keyPath string) (string, error) {
		if secret, err := m.store.Passphrase(keyPath); err == nil {
			return secret.Reveal(), nil
		}
		secret, remember, err := m.prompt.Passphrase(keyPath)
		if err != nil {
			return "", err
		}
		if remember {
			_ = m.store.SetPassphrase(keyPath, secret)
		}
		return secret.Reveal(), nil
	}

	usedStored := false
	if secret, err := m.store.Password(target.HostKeyAlias()); err == nil {
		usedStored = true
		auth.Password = func() (string, error) { return secret.Reveal(), nil }
	} else {
		auth.Password = func() (string, error) {
			secret, remember, err := m.prompt.Password(target, false)
			if err != nil {
				return "", err
			}
			if remember {
				_ = m.store.SetPassword(target.HostKeyAlias(), secret)
			}
			return secret.Reveal(), nil
		}
	}
	return auth, usedStored
}

// addSession records a live session and gives it an id.
func (m *Manager) addSession(target ssh.Target, client *ssh.Client, fsys vfs.FS) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequence++
	session := &Session{
		ID:     fmt.Sprintf("s%d", m.sequence),
		Target: target,
		Client: client,
		FS:     fsys,
		State:  StateReady,
		Opened: time.Now(),
		// A pane starts at the filesystem's own root, which is the directory the
		// session was opened over.
		Root: "/",
		Base: baseOf(fsys),
	}
	m.sessions[session.ID] = session
	return session
}

// Close ends a session and its filesystem. The connection stays in the pool until
// it has been idle, so reopening a tab does not redial.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	session.State = StateClosed
	if closer, ok := session.FS.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	m.release(session.Client)
	return nil
}

// CloseAll ends every session and drops the pool.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Close(id)
	}
	return m.pool.Close()
}

// baseOf asks a filesystem for the directory it is rooted at, when it knows.
func baseOf(fsys vfs.FS) string {
	if rooted, ok := fsys.(interface{ Root() string }); ok {
		return rooted.Root()
	}
	return ""
}

// isAuthFailure distinguishes "the server said no" from "the network broke",
// because only the first is worth retrying with a fresh credential.
func isAuthFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), "rejected the credentials")
}

// defaultIdentityFiles are the keys ssh itself would try when a config names
// none, in its own order.
func defaultIdentityFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		home + "/.ssh/id_ed25519",
		home + "/.ssh/id_ecdsa",
		home + "/.ssh/id_rsa",
	}
}
