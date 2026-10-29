// Package tunnel forwards TCP through an SSH connection: local (-L), remote (-R)
// and dynamic/SOCKS (-D), plus the metadata a bastion needs to say who connected
// and how much moved.
//
// Traffic is never captured. A tunnel moves bytes; the events here are addresses,
// byte counts and times, which is what an audit log can honestly claim.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Dialer reaches a target through the transport. An SSH client satisfies it.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// Listener is the remote side of a -R tunnel. *ssh.Client satisfies it: the
// server listens and hands back connections.
type Listener interface {
	Listen(network, addr string) (net.Listener, error)
}

// Kind is which direction the tunnel runs.
type Kind string

// The four shapes a forward can take.
const (
	Local   Kind = "local"
	Remote  Kind = "remote"
	Dynamic Kind = "dynamic"
	Bastion Kind = "bastion"
)

// Event is one metadata record. There is deliberately no payload field.
type Event struct {
	Time     time.Time `json:"time"`
	Session  string    `json:"session,omitempty"`
	Host     string    `json:"host,omitempty"`
	Kind     Kind      `json:"kind"`
	Listen   string    `json:"listen"`
	Target   string    `json:"target,omitempty"`
	Client   string    `json:"client,omitempty"`
	BytesIn  int64     `json:"bytes_in,omitempty"`
	BytesOut int64     `json:"bytes_out,omitempty"`
	Error    string    `json:"error,omitempty"`
	// Active marks a progress report for a connection that is still open; the
	// final event for that connection has it false. History ignores active
	// events so a long download is not counted twice.
	Active bool `json:"active,omitempty"`
}

// Options configures one tunnel.
type Options struct {
	Kind Kind
	// Listen is where the tunnel listens, in whatever namespace the kind implies:
	// this machine for local and dynamic, the server for remote.
	Listen string
	// Target is where accepted connections go: host:port. Empty for dynamic.
	Target string
	// Dialer reaches the target through SSH for local and dynamic tunnels.
	Dialer Dialer
	// Remote listens on the server for a remote tunnel.
	Remote Listener
	// Host is the SSH alias the tunnel runs through, for the history line.
	Host string
	// OnEvent receives one event per connection when it closes, plus the start.
	// It may be nil.
	OnEvent func(Event)
	// OnProgress receives live byte counts for open connections, about twice a
	// second. It is separate from OnEvent so the audit log does not grow a line
	// per tick.
	OnProgress func(Event)
	// LocalDialer is used by a remote tunnel to reach the target on this side;
	// nil means net.Dial.
	LocalDialer Dialer
}

// Tunnel is a running forward.
type Tunnel struct {
	id      string
	options Options
	ctx     context.Context
	cancel  context.CancelFunc
	ln      net.Listener
	wg      sync.WaitGroup

	mu     sync.Mutex
	closed bool
	// conns are the open pairs, so Close can unblock a bridge instead of waiting
	// for a client that may never hang up.
	conns map[net.Conn]struct{}
}

// Start opens the listener and begins accepting.
func Start(ctx context.Context, opts Options) (*Tunnel, error) {
	if opts.Kind == "" {
		opts.Kind = Local
	}
	if strings.TrimSpace(opts.Listen) == "" {
		return nil, errors.New("tunnel: a listen address is required")
	}
	if opts.Kind == Dynamic && opts.Dialer == nil {
		return nil, errors.New("tunnel: dynamic needs a dialer")
	}
	if (opts.Kind == Local || opts.Kind == Bastion) && (opts.Dialer == nil || opts.Target == "") {
		return nil, errors.New("tunnel: local needs a dialer and a target")
	}
	if opts.Kind == Remote && opts.Remote == nil {
		return nil, errors.New("tunnel: remote needs the server's listener")
	}

	var ln net.Listener
	var err error
	switch opts.Kind {
	case Remote:
		ln, err = opts.Remote.Listen("tcp", opts.Listen)
	default:
		var config net.ListenConfig
		ln, err = config.Listen(ctx, "tcp", opts.Listen)
	}
	if err != nil {
		return nil, fmt.Errorf("tunnel: %s: %w", opts.Listen, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	t := &Tunnel{id: newSessionID(), options: opts, ctx: runCtx, cancel: cancel, ln: ln,
		conns: map[net.Conn]struct{}{}}
	t.emit(Event{Time: time.Now(), Session: t.id, Host: opts.Host, Kind: opts.Kind,
		Listen: ln.Addr().String(), Target: opts.Target})
	t.wg.Add(1)
	go t.accept()
	return t, nil
}

// Addr is the address the tunnel actually bound, which matters when port 0 was
// requested.
func (t *Tunnel) Addr() net.Addr { return t.ln.Addr() }

// ID names the session in the event log, so a history can group what belongs
// together.
func (t *Tunnel) ID() string { return t.id }

// newSessionID is unique per process without importing a UUID for one line.
func newSessionID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), sessionCounter.Add(1))
}

var sessionCounter atomic.Int64

func (t *Tunnel) accept() {
	defer t.wg.Done()
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			t.mu.Lock()
			closed := t.closed
			t.mu.Unlock()
			if !closed && t.ctx.Err() == nil {
				t.emit(Event{Time: time.Now(), Session: t.id, Host: t.options.Host,
					Kind: t.options.Kind, Error: err.Error()})
			}
			return
		}
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.handle(conn)
		}()
	}
}

// handle serves one accepted connection.
func (t *Tunnel) handle(client net.Conn) {
	defer func() { _ = client.Close() }()
	started := time.Now()

	var upstream net.Conn
	var target string
	var err error
	switch t.options.Kind {
	case Dynamic:
		upstream, target, err = t.serveSOCKS(client)
	default:
		target = t.options.Target
		upstream, err = t.dial(t.ctx, target)
	}
	if err != nil {
		t.emit(Event{Time: started, Session: t.id, Host: t.options.Host,
			Kind: t.options.Kind, Listen: t.ln.Addr().String(),
			Target: target, Client: client.RemoteAddr().String(), Error: err.Error()})
		return
	}
	t.track(client, upstream)
	defer func() {
		t.untrack(client, upstream)
		_ = upstream.Close()
	}()

	clientAddr := client.RemoteAddr().String()
	in, out := bridge(client, upstream, func(in, out int64) {
		t.progress(Event{Time: time.Now(), Session: t.id, Host: t.options.Host,
			Kind: t.options.Kind, Listen: t.ln.Addr().String(), Target: target,
			Client: clientAddr, BytesIn: in, BytesOut: out, Active: true})
	})
	t.emit(Event{Time: time.Now(), Session: t.id, Host: t.options.Host,
		Kind: t.options.Kind, Listen: t.ln.Addr().String(),
		Target: target, Client: clientAddr, BytesIn: in, BytesOut: out})
}

// dial reaches a target, using the local dialer for a remote tunnel and the SSH
// dialer for everything else.
func (t *Tunnel) dial(ctx context.Context, target string) (net.Conn, error) {
	if t.options.Kind == Remote {
		dialer := t.options.LocalDialer
		if dialer == nil {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", target)
		}
		return dialer.DialContext(ctx, "tcp", target)
	}
	return t.options.Dialer.DialContext(ctx, "tcp", target)
}

// bridge copies both ways and returns bytes from the client and to the client.
// progress, when not nil, is called with the running totals about twice a second
// so a long-lived connection is visible before it closes.
func bridge(client, upstream net.Conn, progress func(in, out int64)) (in, out int64) {
	done := make(chan struct{})
	if progress != nil {
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					progress(atomic.LoadInt64(&in), atomic.LoadInt64(&out))
				case <-done:
					return
				}
			}
		}()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(upstream, client)
		atomic.StoreInt64(&out, n)
		_ = upstream.Close()
	}()
	go func() {
		defer wg.Done()
		n, _ := io.Copy(client, upstream)
		atomic.StoreInt64(&in, n)
		_ = client.Close()
	}()
	wg.Wait()
	close(done)
	return in, out
}

// track remembers a connection pair so Close can end it.
func (t *Tunnel) track(conns ...net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, conn := range conns {
		t.conns[conn] = struct{}{}
	}
}

func (t *Tunnel) untrack(conns ...net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, conn := range conns {
		delete(t.conns, conn)
	}
}

// Close stops accepting and ends open connections. It is bounded: a quit must
// not wait for a browser keep-alive, and a listener on a dead link must not block
// the process. A connection still busy after the deadline is abandoned to the
// OS, which will close it with the process.
func (t *Tunnel) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conns := make([]net.Conn, 0, len(t.conns))
	for conn := range t.conns {
		conns = append(conns, conn)
	}
	t.mu.Unlock()
	t.cancel()

	// The listener's Close can itself be a network round trip for a remote
	// forward; do not hold the caller for it.
	closed := make(chan struct{})
	go func() {
		_ = t.ln.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(300 * time.Millisecond):
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

func (t *Tunnel) emit(event Event) {
	if t.options.OnEvent != nil {
		t.options.OnEvent(event)
	}
}

func (t *Tunnel) progress(event Event) {
	if t.options.OnProgress != nil {
		t.options.OnProgress(event)
	}
}
