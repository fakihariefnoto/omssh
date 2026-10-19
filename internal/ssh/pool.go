package ssh

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PoolOptions configures a pool. The zero value is not useful: Auth and Keys are
// required, and the defaults below are sensible.
type PoolOptions struct {
	Auth Auth
	Keys HostKeys
	// Dial options applied to every connection the pool opens.
	Dial []Option
	// Idle is how long an unused connection is kept before it is closed. Zero
	// means DefaultIdle.
	Idle time.Duration
	// KeepAlive is the heartbeat interval. Zero means DefaultKeepAlive; a
	// negative value disables heartbeats.
	KeepAlive time.Duration
	// Now is the clock, replaced in tests.
	Now func() time.Time
	// Sweep is how often idle connections are looked at. Zero means a tenth of
	// Idle, with a floor, which is frequent enough to be prompt and rare enough
	// to be free.
	Sweep time.Duration
}

// DefaultIdle is how long a connection with no tabs on it is kept. Long enough
// that closing and reopening a tab does not redial, short enough that a pool of
// twenty hosts is not twenty idle sockets all afternoon.
const DefaultIdle = 5 * time.Minute

// Pool shares connections by identity, so two tabs onto the same host are one
// TCP connection and one authenticated session, and keeps them alive until they
// are no longer wanted.
type Pool struct {
	options PoolOptions

	mu       sync.Mutex
	entries  map[string]*poolEntry
	inflight map[string]chan struct{}
	closed   bool
	done     chan struct{}
	wg       sync.WaitGroup
}

type poolEntry struct {
	client *Client
	refs   int
	// idleSince is when the last holder released it.
	idleSince time.Time
	// cancel stops the keepalive goroutine for this connection.
	cancel context.CancelFunc
}

// NewPool builds a pool and starts its reaper.
func NewPool(options PoolOptions) *Pool {
	if options.Idle == 0 {
		options.Idle = DefaultIdle
	}
	if options.KeepAlive == 0 {
		options.KeepAlive = DefaultKeepAlive
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Sweep == 0 {
		options.Sweep = options.Idle / 10
		if options.Sweep < 50*time.Millisecond {
			options.Sweep = 50 * time.Millisecond
		}
	}
	pool := &Pool{
		options:  options,
		entries:  map[string]*poolEntry{},
		inflight: map[string]chan struct{}{},
		done:     make(chan struct{}),
	}
	pool.wg.Add(1)
	go pool.reap()
	return pool
}

// Acquire returns a connection for the target, dialing if the pool does not have
// one. The returned release func must be called when the caller is finished; the
// connection stays open until it has been idle for the configured time.
func (p *Pool) Acquire(ctx context.Context, target Target) (*Client, func(), error) {
	key := target.PoolKey()

	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, nil, fmt.Errorf("ssh: the connection pool is closed")
		}
		if entry, ok := p.entries[key]; ok && entry.client != nil {
			entry.refs++
			entry.idleSince = time.Time{}
			client := entry.client
			p.mu.Unlock()
			return client, p.releaseFunc(key), nil
		}
		if wait, ok := p.inflight[key]; ok {
			// Someone else is dialing this exact host. Wait for them rather than
			// opening a second connection to the same place.
			p.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		wait := make(chan struct{})
		p.inflight[key] = wait
		p.mu.Unlock()

		client, err := Dial(ctx, target, p.options.Auth, p.options.Keys, p.options.Dial...)

		p.mu.Lock()
		delete(p.inflight, key)
		close(wait)
		if err != nil {
			p.mu.Unlock()
			return nil, nil, err
		}
		entry := &poolEntry{client: client, refs: 1}
		p.entries[key] = entry
		p.mu.Unlock()

		p.startKeepAlive(key, entry)
		return client, p.releaseFunc(key), nil
	}
}

// releaseFunc decrements the reference count for one holder.
func (p *Pool) releaseFunc(key string) func() {
	released := false
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if released {
			return
		}
		released = true
		entry, ok := p.entries[key]
		if !ok {
			return
		}
		if entry.refs > 0 {
			entry.refs--
		}
		if entry.refs == 0 {
			entry.idleSince = p.options.Now()
		}
	}
}

// startKeepAlive heartbeats a connection until it is dropped or the pool closes.
func (p *Pool) startKeepAlive(key string, entry *poolEntry) {
	if p.options.KeepAlive <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	entry.cancel = cancel
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		if err := entry.client.KeepAlive(ctx, p.options.KeepAlive); err != nil {
			// A failed heartbeat means the connection is gone: drop it now rather
			// than hand a dead socket to the next tab.
			p.drop(key, entry)
		}
	}()
}

// reap closes connections that have been idle for longer than the configured
// time, so a pool of hosts does not hold sockets for nobody.
func (p *Pool) reap() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.options.Sweep)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.sweepIdle()
		}
	}
}

func (p *Pool) sweepIdle() {
	now := p.options.Now()
	p.mu.Lock()
	victims := make([]*poolEntry, 0, len(p.entries))
	for key, entry := range p.entries {
		if entry.refs > 0 || entry.idleSince.IsZero() {
			continue
		}
		if now.Sub(entry.idleSince) >= p.options.Idle {
			delete(p.entries, key)
			victims = append(victims, entry)
		}
	}
	p.mu.Unlock()

	for _, entry := range victims {
		if entry.cancel != nil {
			entry.cancel()
		}
		_ = entry.client.Close()
	}
}

// drop removes a connection that has failed.
func (p *Pool) drop(key string, entry *poolEntry) {
	p.mu.Lock()
	if current, ok := p.entries[key]; ok && current == entry {
		delete(p.entries, key)
	}
	p.mu.Unlock()
	if entry.cancel != nil {
		entry.cancel()
	}
	_ = entry.client.Close()
}

// Len is how many connections the pool is holding, for the interface and tests.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Refs is how many holders a target has, which the interface shows as "2 tabs".
func (p *Pool) Refs(target Target) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.entries[target.PoolKey()]; ok {
		return entry.refs
	}
	return 0
}

// Close drops every connection and stops the reaper.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	entries := make([]*poolEntry, 0, len(p.entries))
	for _, entry := range p.entries {
		entries = append(entries, entry)
	}
	p.entries = map[string]*poolEntry{}
	p.mu.Unlock()

	close(p.done)
	for _, entry := range entries {
		if entry.cancel != nil {
			entry.cancel()
		}
		_ = entry.client.Close()
	}
	p.wg.Wait()
	return nil
}
