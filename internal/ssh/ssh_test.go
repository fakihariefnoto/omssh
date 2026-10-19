package ssh_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// --- an in-process SSH server, so the handshake is real ----------------------

// server is a minimal SSH server that answers session/exec requests. It is
// enough to exercise dialing, host key verification, the auth chain and the
// pool, with no network and no container.
type server struct {
	listener net.Listener
	signer   ssh.Signer
	config   *ssh.ServerConfig

	mu    sync.Mutex
	conns map[net.Conn]struct{}

	// accepted counts completed handshakes, which is how a test proves a
	// connection was reused instead of redialed.
	accepted atomic.Int32
	// commands records what was asked to run.
	commands chan string
}

type serverOptions struct {
	// authorizedKeys are the public keys accepted for authentication.
	authorizedKeys []ssh.PublicKey
	// password, when non-empty, is accepted as a password.
	password string
	// banner is sent before authentication.
	banner string
}

func newServer(t *testing.T, options serverOptions) *server {
	t.Helper()
	signer := generateSigner(t)
	srv := &server{
		signer:   signer,
		commands: make(chan string, 16),
		conns:    map[net.Conn]struct{}{},
	}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			for _, allowed := range options.authorizedKeys {
				if string(allowed.Marshal()) == string(key.Marshal()) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, fmt.Errorf("key refused")
		},
	}
	if options.password != "" {
		config.PasswordCallback = func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == options.password {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("password refused")
		}
	}
	if options.banner != "" {
		config.BannerCallback = func(ssh.ConnMetadata) string { return options.banner }
	}
	config.AddHostKey(signer)
	srv.config = config

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.listener = listener
	go srv.serve()
	t.Cleanup(srv.Close)
	return srv
}

func (s *server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	sshConn, channels, requests, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		_ = conn.Close()
		return
	}
	s.accepted.Add(1)
	go ssh.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			_ = channel.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		session, sessionRequests, err := channel.Accept()
		if err != nil {
			continue
		}
		go s.serveSession(session, sessionRequests)
	}
	_ = sshConn.Close()
}

func (s *server) serveSession(session ssh.Channel, requests <-chan *ssh.Request) {
	for request := range requests {
		switch request.Type {
		case "exec":
			var exec struct{ Command string }
			_ = ssh.Unmarshal(request.Payload, &exec)
			command := exec.Command
			select {
			case s.commands <- command:
			default:
			}
			_, _ = session.Write([]byte("ran: " + command + "\n"))
			_, _ = session.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			_ = request.Reply(true, nil)
			_ = session.Close()
			return
		case "keepalive@openssh.com":
			_ = request.Reply(true, nil)
		default:
			_ = request.Reply(false, nil)
		}
	}
	_ = session.Close()
}

// Close stops the server and drops the connections already established, which is
// what a host going away looks like from the client's side.
func (s *server) Close() {
	_ = s.listener.Close()
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.conns = map[net.Conn]struct{}{}
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// addr is the server's host:port.
func (s *server) addr() string { return s.listener.Addr().String() }

// generateSigner creates a fresh ed25519 signer, for a host key the test does
// not need the private half of.
func generateSigner(t *testing.T) ssh.Signer {
	t.Helper()
	signer, _ := newKey(t)
	return signer
}

// newKey returns a signer and its private half: ssh.Signer exposes only the
// public part, and a client key has to be written to disk to be used.
func newKey(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer, private
}

// writePrivateKey writes a client key in OpenSSH format, which is what
// IdentityFile points at.
func writePrivateKey(t *testing.T, path string, private ed25519.PrivateKey) {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(private, "omssh-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

// signerForTarget returns a target pointing at the server, with the host key
// pre-trusted so most tests are about auth rather than consent.
func (s *server) target(t *testing.T, keys omssh.HostKeys, identity string) omssh.Target {
	t.Helper()
	host, port := splitHostPort(t, s.addr())
	target, err := omssh.TargetFrom(sshconfig.Spec{
		Alias: "testhost", HostName: host, User: "tester", Port: port, IdentityFile: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port := 0
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	return host, port
}

// trust stores the server's key as if the user had accepted it.
func (s *server) trust(t *testing.T, keys omssh.HostKeys) {
	t.Helper()
	_, port := splitHostPort(t, s.addr())
	if err := keys.Add("testhost", port, s.signer.PublicKey()); err != nil {
		t.Fatal(err)
	}
}

func hostKeysAt(t *testing.T) omssh.HostKeys {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return omssh.HostKeysAt(filepath.Join(home, ".ssh", "known_hosts"))
}

// --- tests ------------------------------------------------------------------

// The happy path: a known host, key authentication, a command, and its output.
func TestDialWithKeyAndRunACommand(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	srv.trust(t, keys)

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)

	target := srv.target(t, keys, keyPath)
	client, err := omssh.Dial(context.Background(), target, omssh.Auth{Keys: []string{keyPath}}, keys)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	stdout, _, err := client.Run(context.Background(), "uname -a")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(stdout), "ran: uname -a") {
		t.Errorf("Run returned %q", stdout)
	}
	if got := client.Target().Alias; got != "testhost" {
		t.Errorf("Target().Alias = %q", got)
	}
	if client.Created().IsZero() {
		t.Error("Created should record when the handshake finished")
	}
}

// A host that has never been seen is refused with everything the dialog needs,
// and accepted once the user says yes.
func TestUnknownHostAsksAndRemembers(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)
	target := srv.target(t, keys, keyPath)

	// Without consent, the connection is refused rather than silently trusted.
	_, err := omssh.Dial(context.Background(), target, omssh.Auth{Keys: []string{keyPath}}, keys)
	if !errors.Is(err, omssh.ErrUnknownHost) {
		t.Fatalf("an unknown host should be refused with ErrUnknownHost, got %v", err)
	}
	var unknown *omssh.UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatal("the error should carry the details the dialog shows")
	}
	if unknown.Fingerprint == "" || !strings.HasPrefix(unknown.Fingerprint, "SHA256:") {
		t.Errorf("the fingerprint should be a SHA256 one, got %q", unknown.Fingerprint)
	}
	if unknown.KeyType == "" || unknown.KnownHosts != keys.Path() {
		t.Errorf("the dialog needs the key type and the file: %+v", unknown)
	}

	// Saying yes connects, and stores the key.
	var asked atomic.Int32
	client, err := omssh.Dial(context.Background(), target, omssh.Auth{Keys: []string{keyPath}}, keys,
		omssh.WithHostKeyConsent(func(host *omssh.UnknownHostError) (omssh.Consent, error) {
			asked.Add(1)
			if host.Fingerprint != unknown.Fingerprint {
				t.Errorf("the consent prompt showed a different key: %s vs %s", host.Fingerprint, unknown.Fingerprint)
			}
			return omssh.ConsentRemember, nil
		}))
	if err != nil {
		t.Fatalf("Dial with consent: %v", err)
	}
	_ = client.Close()
	if asked.Load() != 1 {
		t.Errorf("consent should be asked once, asked %d times", asked.Load())
	}

	// The key is now stored, so a second connection needs no prompt.
	again, err := omssh.Dial(context.Background(), target, omssh.Auth{Keys: []string{keyPath}}, keys)
	if err != nil {
		t.Fatalf("Dial after storing the key: %v", err)
	}
	_ = again.Close()

	stored, err := os.ReadFile(keys.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "testhost") {
		t.Errorf("the key was not stored under the alias:\n%s", stored)
	}
	if lines := strings.Count(strings.TrimSpace(string(stored)), "\n"); lines != 0 {
		t.Errorf("one connection should store one line, got:\n%s", stored)
	}
}

// Refusing consent aborts the connection and stores nothing.
func TestRefusedConsentStoresNothing(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)

	_, err := omssh.Dial(context.Background(), srv.target(t, keys, keyPath), omssh.Auth{Keys: []string{keyPath}}, keys,
		omssh.WithHostKeyConsent(func(*omssh.UnknownHostError) (omssh.Consent, error) {
			return omssh.ConsentRefuse, nil
		}))
	if err == nil {
		t.Fatal("a refused host key should abort the connection")
	}
	if _, statErr := os.Stat(keys.Path()); statErr == nil {
		if data, _ := os.ReadFile(keys.Path()); strings.Contains(string(data), "testhost") {
			t.Errorf("a refused key was stored anyway:\n%s", data)
		}
	}
}

// A changed key is a hard stop: it is never accepted, even with a consent
// callback in place, because consent is for the unknown case only.
func TestChangedHostKeyIsAHardStop(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)

	// Store a different key under the same name, as if the server had been
	// rebuilt behind our back.
	impostor := generateSigner(t)
	_, port := splitHostPort(t, srv.addr())
	if err := keys.Add("testhost", port, impostor.PublicKey()); err != nil {
		t.Fatal(err)
	}

	consented := false
	_, err := omssh.Dial(context.Background(), srv.target(t, keys, keyPath), omssh.Auth{Keys: []string{keyPath}}, keys,
		omssh.WithHostKeyConsent(func(*omssh.UnknownHostError) (omssh.Consent, error) {
			consented = true
			return omssh.ConsentRemember, nil
		}))
	if !errors.Is(err, omssh.ErrHostKeyMismatch) {
		t.Fatalf("a changed host key should be ErrHostKeyMismatch, got %v", err)
	}
	if consented {
		t.Error("a changed key must not be offered as a first-connect prompt")
	}
	var mismatch *omssh.HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatal("the error should carry both fingerprints")
	}
	if mismatch.Offered == "" || len(mismatch.Stored) == 0 {
		t.Errorf("the dialog needs both keys: %+v", mismatch)
	}
	if !strings.Contains(err.Error(), "changed") {
		t.Errorf("the message should say the key changed: %v", err)
	}
}

// Strict mode refuses an unknown host outright, for people who manage
// known_hosts themselves.
func TestStrictPolicyRefusesUnknownHosts(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)

	_, err := omssh.Dial(context.Background(), srv.target(t, keys, keyPath), omssh.Auth{Keys: []string{keyPath}}, keys,
		omssh.WithPolicy(omssh.PolicyStrict))
	if !errors.Is(err, omssh.ErrUnknownHost) {
		t.Fatalf("strict mode should refuse an unknown host, got %v", err)
	}
	if !strings.Contains(err.Error(), "strict") {
		t.Errorf("the message should say why: %v", err)
	}
}

// Password authentication is the fallback when no key is accepted, and it goes
// through the prompt the interface supplies.
func TestPasswordAuthentication(t *testing.T) {
	srv := newServer(t, serverOptions{password: "hunter2"})
	keys := hostKeysAt(t)
	srv.trust(t, keys)

	target := srv.target(t, keys, "")
	asked := 0
	client, err := omssh.Dial(context.Background(), target, omssh.Auth{
		Password: func() (string, error) {
			asked++
			return "hunter2", nil
		},
	}, keys)
	if err != nil {
		t.Fatalf("Dial with a password: %v", err)
	}
	defer func() { _ = client.Close() }()
	if asked == 0 {
		t.Error("the password prompt should have been used")
	}
	if _, _, err := client.Run(context.Background(), "id"); err != nil {
		t.Errorf("Run after password auth: %v", err)
	}
}

// A wrong password is reported as rejected credentials, not as a broken
// connection.
func TestWrongPasswordIsReportedClearly(t *testing.T) {
	srv := newServer(t, serverOptions{password: "hunter2"})
	keys := hostKeysAt(t)
	srv.trust(t, keys)

	_, err := omssh.Dial(context.Background(), srv.target(t, keys, ""), omssh.Auth{
		Password: func() (string, error) { return "wrong", nil },
	}, keys)
	if err == nil {
		t.Fatal("a wrong password should fail")
	}
	if !strings.Contains(err.Error(), "rejected the credentials") {
		t.Errorf("the message should say the credentials were refused, got %v", err)
	}
}

// A key the server does not accept is reported the same way, rather than as a
// network problem.
func TestUnacceptedKeyIsReportedClearly(t *testing.T) {
	authorized := generateSigner(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{authorized.PublicKey()}})
	keys := hostKeysAt(t)
	srv.trust(t, keys)

	other, otherPrivate := newKey(t)
	keyPath := filepath.Join(t.TempDir(), "other")
	writePrivateKey(t, keyPath, otherPrivate)
	_ = other

	_, err := omssh.Dial(context.Background(), srv.target(t, keys, keyPath), omssh.Auth{Keys: []string{keyPath}}, keys)
	if err == nil {
		t.Fatal("a key the server does not accept should fail")
	}
	if !strings.Contains(err.Error(), "rejected the credentials") {
		t.Errorf("the message should point at the credentials, got %v", err)
	}
}

// A banner is handed to the caller, because some servers explain themselves
// there.
func TestBannerIsPassedOn(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{
		authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()},
		banner:         "authorised users only",
	})
	keys := hostKeysAt(t)
	srv.trust(t, keys)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)

	var seen []string
	client, err := omssh.Dial(context.Background(), srv.target(t, keys, keyPath), omssh.Auth{
		Keys:   []string{keyPath},
		Banner: func(text string) { seen = append(seen, text) },
	}, keys, omssh.WithBanner(func(text string) { seen = append(seen, text) }))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	if len(seen) == 0 || !strings.Contains(seen[0], "authorised users only") {
		t.Errorf("the banner was not reported: %v", seen)
	}
}

// The pool shares one connection between callers and closes it when the last
// one lets go and it has been idle.
func TestPoolSharesAndReapsConnections(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	srv.trust(t, keys)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)
	target := srv.target(t, keys, keyPath)

	clock := &fakeClock{now: time.Now()}
	pool := omssh.NewPool(omssh.PoolOptions{
		AuthFor:   fixedAuth(omssh.Auth{Keys: []string{keyPath}}),
		Keys:      keys,
		Idle:      50 * time.Millisecond,
		KeepAlive: -1, // heartbeats are tested separately
		Now:       clock.Now,
		Sweep:     5 * time.Millisecond,
	})
	defer func() { _ = pool.Close() }()

	ctx := context.Background()
	first, releaseFirst, err := pool.Acquire(ctx, target)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	second, releaseSecond, err := pool.Acquire(ctx, target)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if first != second {
		t.Error("two callers for one target should share a connection")
	}
	if pool.Len() != 1 {
		t.Errorf("the pool holds %d connections, want 1", pool.Len())
	}
	if got := pool.Refs(target); got != 2 {
		t.Errorf("Refs = %d, want 2", got)
	}
	if accepted := srv.accepted.Load(); accepted != 1 {
		t.Errorf("the server accepted %d connections, want 1", accepted)
	}

	releaseFirst()
	releaseFirst() // releasing twice must not underflow
	if got := pool.Refs(target); got != 1 {
		t.Errorf("after one release Refs = %d, want 1", got)
	}
	releaseSecond()
	if got := pool.Refs(target); got != 0 {
		t.Errorf("after both releases Refs = %d, want 0", got)
	}

	// Idle long enough and the connection goes away.
	clock.advance(time.Second)
	waitFor(t, time.Second, func() bool { return pool.Len() == 0 })

	// A later acquire dials again.
	third, releaseThird, err := pool.Acquire(ctx, target)
	if err != nil {
		t.Fatalf("Acquire after the idle timeout: %v", err)
	}
	defer releaseThird()
	if third == first {
		t.Error("a reaped connection should not be handed out again")
	}
	if accepted := srv.accepted.Load(); accepted != 2 {
		t.Errorf("the server accepted %d connections, want 2", accepted)
	}
}

// A connection that fails its heartbeat is dropped, so the next caller dials
// rather than inheriting a dead socket.
func TestPoolDropsConnectionsThatFailTheirHeartbeat(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	srv.trust(t, keys)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)
	target := srv.target(t, keys, keyPath)

	pool := omssh.NewPool(omssh.PoolOptions{
		AuthFor:   fixedAuth(omssh.Auth{Keys: []string{keyPath}}),
		Keys:      keys,
		Idle:      time.Minute,
		KeepAlive: 10 * time.Millisecond,
	})
	defer func() { _ = pool.Close() }()

	client, release, err := pool.Acquire(context.Background(), target)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()
	if pool.Len() != 1 {
		t.Fatalf("the pool holds %d connections, want 1", pool.Len())
	}

	// Kill the server: the next heartbeat fails and the pool drops the entry.
	srv.Close()
	_ = client // the client is the pool's to manage from here
	waitFor(t, 2*time.Second, func() bool { return pool.Len() == 0 })
}

// differentUsersShareNothing: two users on one host are two connections.
func TestPoolSeparatesTargetsThatAreNotTheSame(t *testing.T) {
	keySigner, keyPrivate := newKey(t)
	srv := newServer(t, serverOptions{authorizedKeys: []ssh.PublicKey{keySigner.PublicKey()}})
	keys := hostKeysAt(t)
	srv.trust(t, keys)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writePrivateKey(t, keyPath, keyPrivate)

	host, port := splitHostPort(t, srv.addr())
	// This test names the host differently, so the key is filed under that name
	// too: known_hosts entries are per name, not per address.
	if err := keys.Add("shared", port, srv.signer.PublicKey()); err != nil {
		t.Fatal(err)
	}
	pool := omssh.NewPool(omssh.PoolOptions{
		AuthFor: fixedAuth(omssh.Auth{Keys: []string{keyPath}}), Keys: keys, Idle: time.Minute, KeepAlive: -1,
	})
	defer func() { _ = pool.Close() }()

	for _, user := range []string{"alice", "bob"} {
		target, err := omssh.TargetFrom(sshconfig.Spec{
			Alias: "shared", HostName: host, Port: port, User: user, IdentityFile: keyPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, release, err := pool.Acquire(context.Background(), target); err != nil {
			t.Fatalf("Acquire as %s: %v", user, err)
		} else {
			defer release()
		}
	}
	if pool.Len() != 2 {
		t.Errorf("two users should be two connections, pool holds %d", pool.Len())
	}
}

// A closed pool refuses new work rather than dialing into the void.
func TestClosedPoolRefusesAcquisition(t *testing.T) {
	keys := hostKeysAt(t)
	pool := omssh.NewPool(omssh.PoolOptions{
		AuthFor:   fixedAuth(omssh.Auth{}),
		Keys:      keys,
		KeepAlive: -1,
	})
	target, err := omssh.TargetFrom(sshconfig.Spec{Alias: "anyhost", HostName: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pool.Acquire(context.Background(), target); err == nil {
		t.Error("a closed pool should refuse acquisition")
	}
	if err := pool.Close(); err != nil {
		t.Errorf("closing twice should be harmless, got %v", err)
	}
}

// --- helpers ----------------------------------------------------------------

// fixedAuth turns a chain into the per-target function the pool asks for.
func fixedAuth(auth omssh.Auth) func(omssh.Target) omssh.Auth {
	return func(omssh.Target) omssh.Auth { return auth }
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func waitFor(t *testing.T, limit time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition was not met within %s", limit)
}
