package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/tunnel"
)

// `omssh tunnel` forwards TCP through a host in ~/.ssh/config. It is the ssh -L /
// -R / -D set, with the one difference that it also writes the metadata an audit
// can use: addresses, byte counts, outcomes — never payload.

func newTunnelCmd(opts *Options, stdout, stderr io.Writer) *cobra.Command {
	var locals, remotes, dynamics []string
	var listen, to string
	cmd := &cobra.Command{
		Use:   "tunnel <host>",
		Short: "Forward TCP through a host: -L, -R, -D",
		Long: "Local (-L), remote (-R) and dynamic/SOCKS (-D) forwarding over the\n" +
			"same connection the interface uses. Metadata is written to the state\n" +
			"directory; traffic is never captured.\n\n" +
			"A local spec is [bind:]port:host:port; -R is the same shape with the\n" +
			"listener on the server; -D is [bind:]port.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTunnel(opts, args[0], locals, remotes, dynamics, listen, to, stdout, stderr)
		},
	}
	cmd.Flags().StringArrayVarP(&locals, "local", "L", nil, "local forward: [bind:]port:host:port")
	cmd.Flags().StringArrayVarP(&remotes, "remote", "R", nil, "remote forward: [bind:]port:host:port")
	cmd.Flags().StringArrayVarP(&dynamics, "dynamic", "D", nil, "SOCKS5 on [bind:]port")
	cmd.Flags().StringVar(&listen, "listen", "", "bastion: address to listen on")
	cmd.Flags().StringVar(&to, "to", "", "bastion: target host:port reached through the SSH host")
	return cmd
}

// tunnelSpec is one forward after parsing.
type tunnelSpec struct {
	kind   tunnel.Kind
	listen string
	target string
}

// parseForward reads [bind:]port:host:port. The default bind is loopback: a
// forward that listens on every interface by accident is how a private database
// ends up on a café network.
func parseForward(spec string, kind tunnel.Kind) (tunnelSpec, error) {
	parts := strings.Split(spec, ":")
	bind := "127.0.0.1"
	switch len(parts) {
	case 3:
		// port:host:port — the default bind.
	case 4:
		bind = parts[0]
		parts = parts[1:]
	default:
		return tunnelSpec{}, fmt.Errorf("tunnel: %q: want [bind:]port:host:port", spec)
	}
	port := strings.TrimSpace(parts[0])
	host := strings.TrimSpace(parts[1])
	targetPort := strings.TrimSpace(parts[2])
	if port == "" || host == "" || targetPort == "" {
		return tunnelSpec{}, fmt.Errorf("tunnel: %q is incomplete", spec)
	}
	return tunnelSpec{kind: kind, listen: net.JoinHostPort(bind, port),
		target: net.JoinHostPort(host, targetPort)}, nil
}

// parseDynamic reads [bind:]port.
func parseDynamic(spec string) (tunnelSpec, error) {
	bind, port := "127.0.0.1", spec
	if host, rest, ok := strings.Cut(spec, ":"); ok {
		bind, port = host, rest
	}
	if strings.TrimSpace(port) == "" {
		return tunnelSpec{}, fmt.Errorf("tunnel: %q: want [bind:]port", spec)
	}
	return tunnelSpec{kind: tunnel.Dynamic, listen: net.JoinHostPort(bind, port)}, nil
}

// tunnelDialer dials through the host and reconnects once if the connection has
// gone away: a laptop that slept should not need the command restarted.
type tunnelDialer struct {
	mu      sync.Mutex
	manager *session.Manager
	target  omssh.Target
	client  *omssh.Client
}

func (d *tunnelDialer) ensure(ctx context.Context) (*omssh.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		return d.client, nil
	}
	live, err := d.manager.Connect(ctx, d.target)
	if err != nil {
		return nil, err
	}
	d.client = live.Client
	return d.client, nil
}

func (d *tunnelDialer) drop() {
	d.mu.Lock()
	d.client = nil
	d.mu.Unlock()
}

func (d *tunnelDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	client, err := d.ensure(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := client.SSH().Dial(network, addr)
	if err == nil {
		return conn, nil
	}
	// The pool's connection may have been replaced under us; one retry is what
	// turns a stale keepalive into a working forward.
	d.drop()
	client, retryErr := d.ensure(ctx)
	if retryErr != nil {
		return nil, retryErr
	}
	return client.SSH().Dial(network, addr)
}

// runTunnel starts every requested forward and waits for a signal.
func runTunnel(opts *Options, host string, locals, remotes, dynamics []string,
	listen, to string, stdout, stderr io.Writer) error {
	specs := make([]tunnelSpec, 0, len(locals)+len(remotes)+len(dynamics)+1)
	for _, spec := range locals {
		parsed, err := parseForward(spec, tunnel.Local)
		if err != nil {
			return err
		}
		specs = append(specs, parsed)
	}
	for _, spec := range dynamics {
		parsed, err := parseDynamic(spec)
		if err != nil {
			return err
		}
		specs = append(specs, parsed)
	}
	for _, spec := range remotes {
		parsed, err := parseForward(spec, tunnel.Remote)
		if err != nil {
			return err
		}
		specs = append(specs, parsed)
	}
	if listen != "" || to != "" {
		if listen == "" || to == "" {
			return errors.New("bastion needs both --listen and --to")
		}
		specs = append(specs, tunnelSpec{kind: tunnel.Bastion, listen: listen, target: to})
	}
	if len(specs) == 0 {
		return errors.New("tunnel: give at least one -L, -R, -D or --listen/--to")
	}

	target, err := execTarget(host)
	if err != nil {
		return err
	}
	manager, err := session.New(session.Options{
		Prompter: &cliPrompter{in: os.Stdin, err: stderr, batch: opts.Batch},
	})
	if err != nil {
		return err
	}
	defer func() { _ = manager.CloseAll() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dialer := &tunnelDialer{manager: manager, target: target}
	if _, err := dialer.ensure(ctx); err != nil {
		return err
	}
	live := dialer.client

	events := func(event tunnel.Event) { recordTunnelEvent(event, opts, stdout) }
	started := make([]*tunnel.Tunnel, 0, len(specs))
	for _, spec := range specs {
		options := tunnel.Options{Kind: spec.kind, Listen: spec.listen, Target: spec.target,
			Dialer: dialer, OnEvent: events}
		if spec.kind == tunnel.Remote {
			options.Remote = live.SSH()
		}
		if spec.kind == tunnel.Bastion {
			fmt.Fprintln(stderr, "bastion: metadata only; the proxied SSH traffic is not captured")
		}
		liveTunnel, err := tunnel.Start(ctx, options)
		if err != nil {
			for _, startedTunnel := range started {
				_ = startedTunnel.Close()
			}
			if owner := portOwner(spec.listen); owner != "" {
				return fmt.Errorf("%w (held by %s)", err, owner)
			}
			return err
		}
		started = append(started, liveTunnel)
		if !opts.JSONLines {
			fmt.Fprintf(stdout, "%s %s", spec.kind, liveTunnel.Addr())
			if spec.target != "" {
				fmt.Fprintf(stdout, " → %s", spec.target)
			}
			fmt.Fprintln(stdout)
		}
	}
	fmt.Fprintln(stderr, "tunnels running · Ctrl+C stops them")
	<-ctx.Done()
	for _, liveTunnel := range started {
		_ = liveTunnel.Close()
	}
	return nil
}

// portOwner asks the platform which process holds a port, because "address
// already in use" names the port but not the thing to stop.
func portOwner(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN").Output() //nolint:gosec // the port is a number from a spec
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return ""
	}
	// "COMMAND PID USER ..." then one line per listener; the command name is what
	// a person needs.
	return strings.Fields(lines[1])[0]
}

// recordTunnelEvent writes metadata to the audit log and, with --json-lines, to
// stdout so a caller can watch connections without polling the file.
func recordTunnelEvent(event tunnel.Event, opts *Options, stdout io.Writer) {
	if opts.JSONLines {
		if line, err := json.Marshal(event); err == nil {
			_, _ = stdout.Write(append(line, '\n'))
		}
	}
	if dir, err := config.StateDir(); err == nil {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			if file, err := os.OpenFile(filepath.Join(dir, "tunnels.jsonl"),
				os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				if line, err := json.Marshal(event); err == nil {
					_, _ = file.Write(append(line, '\n'))
				}
				_ = file.Close()
			}
		}
	}
}
