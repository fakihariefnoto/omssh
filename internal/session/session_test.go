package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fakihariefnoto/omssh/internal/keys"
	"github.com/fakihariefnoto/omssh/internal/log"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
	"github.com/fakihariefnoto/omssh/internal/testsupport/sshserver"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// harness is a manager wired to an in-process server, with everything a test
// needs to inspect: the store, the known_hosts file, the temp home.
type harness struct {
	server  *sshserver.Server
	manager *session.Manager
	store   *keys.Store
	backend *keys.MemBackend
	keys    omssh.HostKeys
	prompts prompts
	home    string
}

// prompts records what was asked, so a test can assert that nothing was asked
// twice and that a refusal was honoured.
type prompts struct {
	consents     int
	passwords    int
	passphrases  int
	lastPassword omssh.Target
	retrySeen    bool
}

func newHarness(t *testing.T, options sshserver.Options) *harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	server := sshserver.Start(t, options)
	hostKeys := omssh.HostKeysAt(filepath.Join(home, ".ssh", "known_hosts"))
	backend := keys.NewMemBackend()
	store := keys.NewStore(backend, "omssh-test")

	h := &harness{server: server, store: store, backend: backend, keys: hostKeys, home: home}

	prompter := session.PrompterFunc{
		ConsentFunc: func(*omssh.UnknownHostError) (omssh.Consent, error) {
			h.prompts.consents++
			return omssh.ConsentRemember, nil
		},
		PasswordFunc: func(target omssh.Target, retry bool) (log.Secret, bool, error) {
			h.prompts.passwords++
			h.prompts.lastPassword = target
			h.prompts.retrySeen = retry
			return log.Secret(optionsPassword), true, nil
		},
		PassphraseFunc: func(string) (log.Secret, bool, error) {
			h.prompts.passphrases++
			return log.Secret("passphrase"), false, nil
		},
	}

	manager, err := session.New(session.Options{
		HostKeys:  hostKeys,
		Store:     store,
		Prompter:  prompter,
		KeepAlive: -1, // heartbeats are the pool's own tests
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() { _ = manager.CloseAll() })
	h.manager = manager
	return h
}

// optionsPassword is what the harness prompter offers; the server is configured
// with the same value.
const optionsPassword = "hunter2"

// target points at the server, with a client key on disk so the auth chain runs
// for real.
func (h *harness) target(t *testing.T) omssh.Target {
	t.Helper()
	_, keyPath := sshserver.WriteClientKey(t, filepath.Join(h.home, ".ssh"))
	host, port := h.server.HostPort(t)
	target, err := omssh.TargetFrom(sshconfig.Spec{
		Alias: "prod-api", HostName: host, Port: port, User: "tester", IdentityFile: keyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// trust stores the server's key, as a previous connection would have.
func (h *harness) trust(t *testing.T, alias string) {
	t.Helper()
	_, port := h.server.HostPort(t)
	if err := h.keys.Add(alias, port, h.server.Signer.PublicKey()); err != nil {
		t.Fatal(err)
	}
}

// --- tests ------------------------------------------------------------------

// The happy path: connect, get a filesystem, use it.
func TestConnectGivesAWorkingFilesystem(t *testing.T) {
	h := newHarness(t, sshserver.Options{SFTP: true, AcceptAnyKey: true})
	h.trust(t, "prod-api")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := h.manager.Connect(ctx, h.target(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if s.State != session.StateReady {
		t.Errorf("a fresh session should be ready, got %s", s.State.Label())
	}
	if s.FS == nil || s.Client == nil {
		t.Fatal("a session should carry a client and a filesystem")
	}
	if s.ID == "" || s.Target.Alias != "prod-api" {
		t.Errorf("the session should be identified: %+v", s)
	}
	if _, ok := h.manager.Get(s.ID); !ok {
		t.Error("the manager should know about its own session")
	}

	// The filesystem is real: make a directory and read it back.
	if err := s.FS.MkdirAll("/work", 0o755); err != nil {
		t.Fatalf("MkdirAll over SFTP: %v", err)
	}
	if err := vfs.WriteFile(s.FS, "/work/notes.txt", []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile over SFTP: %v", err)
	}
	entries, err := s.FS.ReadDir("/work")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "notes.txt" {
		t.Errorf("ReadDir returned %+v", entries)
	}

	// And a command runs on the same connection.
	stdout, _, err := s.Client.Run(ctx, "uname")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(stdout), "ran: uname") {
		t.Errorf("Run returned %q", stdout)
	}
}

// A host that has never been seen asks, and remembering means we are not asked
// again.
func TestUnknownHostAsksOnceWhenRemembered(t *testing.T) {
	h := newHarness(t, sshserver.Options{SFTP: true, AcceptAnyKey: true})
	target := h.target(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first, err := h.manager.Connect(ctx, target)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if h.prompts.consents != 1 {
		t.Errorf("the unknown host should be asked about once, asked %d times", h.prompts.consents)
	}

	// A second connection to the same host is not asked again, because the key is
	// now stored.
	second, err := h.manager.Connect(ctx, target)
	if err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	if h.prompts.consents != 1 {
		t.Errorf("a stored key should not be asked about twice, asked %d times", h.prompts.consents)
	}
	if first.ID == second.ID {
		t.Error("each connection should be its own session")
	}
	if h.manager.Len() != 2 {
		t.Errorf("the manager holds %d sessions, want 2", h.manager.Len())
	}
	// Both sessions share one connection: the server saw one handshake.
	if accepted := h.server.Accepted.Load(); accepted != 1 {
		t.Errorf("the server accepted %d connections, want 1 — two sessions should share", accepted)
	}
}

// A refusal aborts, and nothing is stored.
func TestRefusedHostKeyAborts(t *testing.T) {
	h := newHarness(t, sshserver.Options{SFTP: true, AcceptAnyKey: true})
	manager, err := session.New(session.Options{
		HostKeys:  h.keys,
		Store:     h.store,
		KeepAlive: -1,
		Prompter: session.PrompterFunc{
			ConsentFunc: func(*omssh.UnknownHostError) (omssh.Consent, error) {
				return omssh.ConsentRefuse, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.CloseAll() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = manager.Connect(ctx, h.target(t))
	if err == nil {
		t.Fatal("refusing the host key should abort the connection")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("the error should say it was refused, got %v", err)
	}
	if manager.Len() != 0 {
		t.Error("a refused connection should leave no session")
	}
	if err := h.keys.Verify([]string{"prod-api"}, h.serverPort(t), h.server.Signer.PublicKey()); !errors.Is(err, omssh.ErrUnknownHost) {
		t.Error("a refused key must not have been stored")
	}
}

// ConsentOnce connects without writing anything, so the next connection asks
// again.
func TestConsentOnceDoesNotStore(t *testing.T) {
	h := newHarness(t, sshserver.Options{SFTP: true, AcceptAnyKey: true})
	manager, err := session.New(session.Options{
		HostKeys:  h.keys,
		Store:     h.store,
		KeepAlive: -1,
		Prompter: session.PrompterFunc{
			ConsentFunc: func(*omssh.UnknownHostError) (omssh.Consent, error) {
				h.prompts.consents++
				return omssh.ConsentOnce, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.CloseAll() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := manager.Connect(ctx, h.target(t)); err != nil {
		t.Fatalf("Connect with one-time consent: %v", err)
	}
	if h.prompts.consents != 1 {
		t.Errorf("consent should have been asked once, asked %d times", h.prompts.consents)
	}
	if err := h.keys.Verify([]string{"prod-api"}, h.serverPort(t), h.server.Signer.PublicKey()); !errors.Is(err, omssh.ErrUnknownHost) {
		t.Error("one-time consent must not write the key")
	}
}

// Password authentication, and remembering it: the second connect does not ask.
func TestPasswordIsAskedAndRemembered(t *testing.T) {
	h := newHarness(t, sshserver.Options{SFTP: true, Password: optionsPassword})
	h.trust(t, "prod-api")
	target := h.target(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := h.manager.Connect(ctx, target); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if h.prompts.passwords != 1 {
		t.Errorf("the password should have been asked once, asked %d times", h.prompts.passwords)
	}
	if h.backend.Len() == 0 {
		t.Error("remembering should have stored the password")
	}

	// Closing the session releases the pool reference; a new connection uses the
	// stored password and does not ask.
	for _, session := range h.manager.Sessions() {
		if err := h.manager.Close(session.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.manager.Connect(ctx, target); err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	if h.prompts.passwords != 1 {
		t.Errorf("a stored password should not be asked for again, asked %d times", h.prompts.passwords)
	}
}

// A stored password the server refuses is stale: it is dropped and the person is
// asked once more, rather than being told to go and clear it by hand.
func TestStalePasswordIsForgottenAndReasked(t *testing.T) {
	h := newHarness(t, sshserver.Options{SFTP: true, Password: optionsPassword})
	h.trust(t, "prod-api")
	// Store the wrong password, as a changed server would leave behind.
	if err := h.store.SetPassword("prod-api", log.Secret("stale")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := h.manager.Connect(ctx, h.target(t))
	if err != nil {
		t.Fatalf("Connect with a stale stored password: %v", err)
	}
	_ = s
	if h.prompts.passwords != 1 {
		t.Errorf("the stale password should have been replaced by one prompt, asked %d times", h.prompts.passwords)
	}
	// The new one is what is stored now.
	secret, err := h.store.Password("prod-api")
	if err != nil {
		t.Fatalf("the corrected password should be stored: %v", err)
	}
	if secret.Reveal() != optionsPassword {
		t.Errorf("the store holds %q, want the password that worked", secret.Reveal())
	}
}

// Connecting without a prompter is a programming error, not a silent hang.
func TestManagerNeedsAPrompter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manager, err := session.New(session.Options{
		HostKeys:  omssh.HostKeysAt(filepath.Join(home, "known_hosts")),
		Store:     keys.NewStore(keys.NewMemBackend(), "test"),
		KeepAlive: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.CloseAll() }()
	if _, err := manager.Connect(context.Background(), omssh.Target{Alias: "x", Host: "127.0.0.1"}); err == nil {
		t.Error("a manager with no prompter should refuse to connect")
	}
}

// Closing a session ends its filesystem and hands the connection back, so the
// pool can reap it.
func TestCloseEndsTheSession(t *testing.T) {
	h := newHarness(t, sshserver.Options{SFTP: true, AcceptAnyKey: true})
	h.trust(t, "prod-api")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := h.manager.Connect(ctx, h.target(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := h.manager.Close(s.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if h.manager.Len() != 0 {
		t.Error("a closed session should be gone")
	}
	if s.State != session.StateClosed {
		t.Errorf("the session should report itself closed, got %s", s.State.Label())
	}
	// Closing twice is harmless.
	if err := h.manager.Close(s.ID); err != nil {
		t.Errorf("closing twice = %v, want nil", err)
	}
	// The filesystem is unusable afterwards, rather than silently answering.
	if err := s.FS.MkdirAll("/after-close", 0o755); err == nil {
		t.Error("a closed session's filesystem should refuse further work")
	}
}

// serverPort is the port the known_hosts entries are filed under.
func (h *harness) serverPort(t *testing.T) int {
	t.Helper()
	_, port := h.server.HostPort(t)
	return port
}
