package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// Client is one authenticated connection, with the pieces omssh needs on top:
// commands, sessions, keepalive, and the target it reached.
type Client struct {
	target Target

	// sshClient is the multiplexed connection. Sessions and subsystems are
	// channels on it.
	sshClient *ssh.Client

	// closeTransport closes whatever carried the connection: a TCP socket, or a
	// jump host's connection when this host was reached through one.
	closeTransport func() error

	created time.Time
}

// DialOptions configures a handshake.
type dialOptions struct {
	policy    HostKeyPolicy
	consent   func(*UnknownHostError) error
	keepAlive time.Duration
	dialer    func(ctx context.Context, network, address string) (net.Conn, error)
	resolver  func(alias string) (sshconfig.Spec, bool)
	banner    func(string)
	// maxJumpDepth stops a ProxyJump loop from nesting forever.
	maxJumpDepth int
}

// Option is a dial setting.
type Option func(*dialOptions)

// WithPolicy selects strict or asking host key behaviour.
func WithPolicy(policy HostKeyPolicy) Option {
	return func(o *dialOptions) { o.policy = policy }
}

// WithHostKeyConsent is called when a host is unknown. Returning nil accepts the
// key, and the client then stores it, which is exactly what "yes" on a
// first-connect prompt means. Returning an error aborts the connection.
func WithHostKeyConsent(consent func(*UnknownHostError) error) Option {
	return func(o *dialOptions) { o.consent = consent }
}

// WithKeepAlive sets the interval for keepalive requests. Zero disables them.
func WithKeepAlive(interval time.Duration) Option {
	return func(o *dialOptions) { o.keepAlive = interval }
}

// WithDialer replaces the network dialer, which is how tests reach an in-process
// server.
func WithDialer(dialer func(ctx context.Context, network, address string) (net.Conn, error)) Option {
	return func(o *dialOptions) { o.dialer = dialer }
}

// WithResolver replaces where a ProxyJump host's own settings come from.
func WithResolver(resolver func(alias string) (sshconfig.Spec, bool)) Option {
	return func(o *dialOptions) { o.resolver = resolver }
}

// WithBanner receives any pre-authentication banner text.
func WithBanner(fn func(string)) Option {
	return func(o *dialOptions) { o.banner = fn }
}

// DefaultKeepAlive is how often an idle connection is proved alive. OpenSSH's
// own ServerAliveInterval default is 0 (off); a terminal client benefits from
// noticing a dead link before the user does.
const DefaultKeepAlive = 30 * time.Second

// Dial connects to a host, verifying its key and authenticating. ProxyJump
// chains are followed, and each hop is verified in its own right.
func Dial(ctx context.Context, target Target, auth Auth, keys HostKeys, opts ...Option) (*Client, error) {
	options := dialOptions{
		policy:       PolicyAsk,
		keepAlive:    DefaultKeepAlive,
		dialer:       (&net.Dialer{}).DialContext,
		resolver:     defaultResolver,
		banner:       auth.Banner,
		maxJumpDepth: 8,
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.banner != nil {
		auth.Banner = options.banner
	}
	return dialChain(ctx, target, auth, keys, options, 0)
}

// dialChain dials one hop, and the hops it is reached through.
func dialChain(ctx context.Context, target Target, auth Auth, keys HostKeys, options dialOptions, depth int) (*Client, error) {
	if depth > options.maxJumpDepth {
		return nil, fmt.Errorf("ssh: ProxyJump chain for %s is too deep; check for a loop", target.Alias)
	}

	// The bastion first, so a failure there is reported as the bastion's, not as
	// the destination's.
	var bastion *Client
	if target.ProxyJump != "" {
		jumpTarget, err := resolveJump(target.ProxyJump, options)
		if err != nil {
			return nil, err
		}
		bastion, err = dialChain(ctx, jumpTarget, auth, keys, options, depth+1)
		if err != nil {
			return nil, fmt.Errorf("ssh: reaching %s through %s: %w", target.Alias, jumpTarget.Alias, err)
		}
	}

	dial := options.dialer
	if bastion != nil {
		// Through a bastion, the dialer is the bastion's own connection.
		dial = func(_ context.Context, network, address string) (net.Conn, error) {
			return bastion.sshClient.Dial(network, address)
		}
	}

	timeout := target.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := dial(dialCtx, "tcp", target.Address())
	if err != nil {
		closeQuietly(bastion)
		return nil, describeDialError(target, err)
	}

	// Verify before authenticating: a host key problem must not look like a
	// credential problem.
	hostKeyCallback := hostKeyCallbackFor(target, keys, options)

	config := &ssh.ClientConfig{
		User:            target.User,
		Auth:            auth.methods(),
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}
	if auth.Banner != nil {
		// The library asks for an error back; a banner is informational, so it
		// never fails the handshake.
		banner := auth.Banner
		config.BannerCallback = func(message string) error {
			banner(message)
			return nil
		}
	}

	sshConn, channels, requests, err := ssh.NewClientConn(conn, target.Address(), config)
	if err != nil {
		_ = conn.Close()
		closeQuietly(bastion)
		return nil, describeAuthError(target, err)
	}

	client := &Client{
		target:    target,
		sshClient: ssh.NewClient(sshConn, channels, requests),
		created:   time.Now(),
		closeTransport: func() error {
			err := conn.Close()
			// A bastion exists for this connection: when the connection ends so
			// does it, or an idle pool would keep a second host dialable for
			// nobody.
			closeQuietly(bastion)
			return err
		},
	}
	return client, nil
}

// hostKeyCallbackFor builds the verification callback, including the consent
// step for a key that has never been seen.
func hostKeyCallbackFor(target Target, keys HostKeys, options dialOptions) ssh.HostKeyCallback {
	names := target.HostKeyNames()
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		err := keys.checkWithPolicy(names, target.Port, key, options.policy)
		if err == nil {
			return nil
		}

		var unknown *UnknownHostError
		if errors.As(err, &unknown) && options.consent != nil {
			if consentErr := options.consent(unknown); consentErr != nil {
				return consentErr
			}
			// Consent given: store it under the alias people type, which is what
			// ssh itself does.
			if addErr := keys.Add(target.HostKeyAlias(), target.Port, key); addErr != nil {
				return addErr
			}
			return nil
		}
		return err
	}
}

// resolveJump turns a ProxyJump value into a target, using the user's config for
// the jump host so its user, port and key apply.
func resolveJump(value string, options dialOptions) (Target, error) {
	// ProxyJump accepts [user@]host[:port]. A bare user is not a config alias, so
	// it is applied on top of whatever the alias says.
	spec := sshconfig.Spec{}
	rest := strings.TrimSpace(value)
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		spec.User = rest[:at]
		rest = rest[at+1:]
	}
	host, port := rest, 0
	if h, p, err := net.SplitHostPort(rest); err == nil {
		host = h
		port, _ = strconv.Atoi(p)
	}
	spec.Alias = host
	spec.HostName = host
	spec.Port = port

	if options.resolver != nil {
		if configured, ok := options.resolver(host); ok {
			// The config fills in what the ProxyJump value did not state.
			if spec.User == "" {
				spec.User = configured.User
			}
			if spec.Port == 0 {
				spec.Port = configured.Port
			}
			if spec.HostName == host && configured.HostName != "" {
				spec.HostName = configured.HostName
			}
			if configured.IdentityFile != "" {
				spec.IdentityFile = configured.IdentityFile
			}
			if configured.ProxyJump != "" {
				spec.ProxyJump = configured.ProxyJump
			}
		}
	}
	target, err := TargetFrom(spec)
	if err != nil {
		return Target{}, fmt.Errorf("ssh: ProxyJump %q: %w", value, err)
	}
	return target, nil
}

// defaultResolver reads ~/.ssh/config, so a jump host named in a config is
// reached with that config's user, port and key.
func defaultResolver(alias string) (sshconfig.Spec, bool) {
	path, err := sshconfig.DefaultPath()
	if err != nil {
		return sshconfig.Spec{}, false
	}
	file, err := sshconfig.Load(path)
	if err != nil {
		return sshconfig.Spec{}, false
	}
	block, ok := file.Lookup(alias)
	if !ok {
		return sshconfig.Spec{}, false
	}
	return block.Spec(), true
}

// Target is the host this client reached.
func (c *Client) Target() Target { return c.target }

// Created is when the handshake completed, which the interface shows as uptime.
func (c *Client) Created() time.Time { return c.created }

// SSH exposes the underlying connection for the SFTP subsystem and anything else
// that speaks through a channel.
func (c *Client) SSH() *ssh.Client { return c.sshClient }

// NewSession opens a session channel. Callers close it.
func (c *Client) NewSession() (*ssh.Session, error) {
	if c.sshClient == nil {
		return nil, fmt.Errorf("ssh: client for %s is closed", c.target.Alias)
	}
	return c.sshClient.NewSession()
}

// Run executes a command and returns its streams. A non-zero exit status is
// reported through *ssh.ExitError, which callers can inspect: a deploy hook that
// fails is not the same as an unreachable host.
func (c *Client) Run(ctx context.Context, command string) (stdout, stderr []byte, err error) {
	session, err := c.NewSession()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = session.Close() }()

	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case <-ctx.Done():
		// Closing the session sends a signal to the remote process, which is the
		// only way to stop it: an SSH channel has no cancel of its own.
		_ = session.Close()
		<-done
		return outBuf.Bytes(), errBuf.Bytes(), ctx.Err()
	case err := <-done:
		return outBuf.Bytes(), errBuf.Bytes(), err
	}
}

// ExitStatus returns the remote exit code, or -1 when the command did not run to
// completion.
func ExitStatus(err error) int {
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus()
	}
	return -1
}

// IsReachable reports whether a cheap command still succeeds, which is how the
// interface decides a connection is degraded rather than dead.
func (c *Client) IsReachable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, err := c.Run(ctx, "true")
	return err == nil
}

// KeepAlive proves the connection is alive every interval until ctx is done.
//
// It asks the server a question rather than measuring silence, because a quiet
// connection is normal: someone reading a log produces no traffic for minutes.
func (c *Client) KeepAlive(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, _, err := c.sshClient.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return fmt.Errorf("ssh: keepalive to %s failed: %w", c.target.Alias, err)
			}
		}
	}
}

// Close ends the connection and whatever carried it.
func (c *Client) Close() error {
	if c.sshClient == nil {
		return nil
	}
	err := c.sshClient.Close()
	if c.closeTransport != nil {
		if transportErr := c.closeTransport(); err == nil {
			err = transportErr
		}
	}
	c.sshClient = nil
	return err
}

// describeDialError turns a network failure into something a person can act on,
// while keeping the original error for the log.
func describeDialError(target Target, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return fmt.Errorf("ssh: %s did not answer within %s: %w", target.Alias, target.Timeout, err)
	case isConnectionRefused(err):
		return fmt.Errorf("ssh: %s refused the connection on port %d; is sshd listening?: %w", target.Host, target.Port, err)
	default:
		return fmt.Errorf("ssh: reaching %s: %w", target.Alias, err)
	}
}

// describeAuthError separates "the server said no" from "the handshake broke",
// because only one of those is worth retrying with a different credential.
//
// The library does not export a type for a rejected credential, so the message
// is matched. It is a stable, documented string, and the alternative is showing
// everyone the handshake internals.
func describeAuthError(target Target, err error) error {
	if errors.Is(err, ErrHostKeyMismatch) || errors.Is(err, ErrUnknownHost) {
		return err
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "unable to authenticate"),
		strings.Contains(message, "no supported methods remain"):
		return fmt.Errorf("ssh: %s rejected the credentials offered; check the user, the key and the agent: %w", target.Alias, err)
	case strings.Contains(message, "knownhosts"):
		return fmt.Errorf("ssh: verifying %s against known_hosts: %w", target.Alias, err)
	default:
		return fmt.Errorf("ssh: connecting to %s: %w", target.Alias, err)
	}
}

// isConnectionRefused recognises the one dial failure worth its own message:
// nothing is listening, which is nearly always "the host is up but sshd is not".
func isConnectionRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return errors.Is(opErr.Err, syscall.ECONNREFUSED)
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

func closeQuietly(client *Client) {
	if client != nil {
		_ = client.Close()
	}
}
