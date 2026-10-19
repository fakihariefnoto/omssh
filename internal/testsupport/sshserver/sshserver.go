// Package sshserver is an in-process SSH server for tests.
//
// Three packages need one (the SSH client, the SFTP filesystem and the session
// manager), and a real handshake is worth more than a mock, so the server lives
// here rather than being copy-pasted three times. It serves a real SFTP
// subsystem over an in-memory filesystem when asked, so tests can create and
// delete anything without touching the machine's disk.
package sshserver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Options configures a server.
type Options struct {
	// Password, when set, is accepted for password authentication.
	Password string
	// AcceptAnyKey accepts any public key. Set it when the test is about
	// something other than authentication.
	AcceptAnyKey bool
	// AuthorizedKeys are the keys accepted when AcceptAnyKey is false. With
	// neither set, every public key is refused, which is how a test forces
	// password authentication.
	AuthorizedKeys []ssh.PublicKey
	// Banner is sent before authentication.
	Banner string
	// SFTP serves the sftp subsystem over an in-memory filesystem.
	SFTP bool
}

// Server is a running test server.
type Server struct {
	Listener net.Listener
	Signer   ssh.Signer
	config   *ssh.ServerConfig

	// Accepted counts completed handshakes, so a test can prove a connection was
	// reused rather than redialed.
	Accepted atomic.Int32
	// Commands records exec requests.
	Commands chan string

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Start brings up a server on a free port. It is closed when the test ends.
func Start(t *testing.T, options Options) *Server {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		Signer:   signer,
		Commands: make(chan string, 32),
		conns:    map[net.Conn]struct{}{},
	}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if options.AcceptAnyKey {
				return &ssh.Permissions{}, nil
			}
			for _, allowed := range options.AuthorizedKeys {
				if bytes.Equal(allowed.Marshal(), key.Marshal()) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, errors.New("key refused")
		},
	}
	if options.Password != "" {
		config.PasswordCallback = func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == options.Password {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("password refused")
		}
	}
	if options.Banner != "" {
		banner := options.Banner
		config.BannerCallback = func(ssh.ConnMetadata) string { return banner }
	}
	config.AddHostKey(signer)
	server.config = config

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	go server.serve(options)
	t.Cleanup(server.Close)
	return server
}

func (s *Server) serve(options Options) {
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.handle(conn, options)
	}
}

func (s *Server) handle(conn net.Conn, options Options) {
	sshConn, channels, requests, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		_ = conn.Close()
		return
	}
	s.Accepted.Add(1)
	go ssh.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			_ = channel.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, channelRequests, err := channel.Accept()
		if err != nil {
			continue
		}
		go s.serveSession(ch, channelRequests, options)
	}
	_ = sshConn.Close()
}

func (s *Server) serveSession(channel ssh.Channel, requests <-chan *ssh.Request, options Options) {
	for request := range requests {
		switch request.Type {
		case "exec":
			var payload struct{ Command string }
			_ = ssh.Unmarshal(request.Payload, &payload)
			select {
			case s.Commands <- payload.Command:
			default:
			}
			_, _ = channel.Write([]byte("ran: " + payload.Command + "\n"))
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			_ = request.Reply(true, nil)
			_ = channel.Close()
			return
		case "subsystem":
			var payload struct{ Name string }
			_ = ssh.Unmarshal(request.Payload, &payload)
			if payload.Name != "sftp" || !options.SFTP {
				_ = request.Reply(false, nil)
				continue
			}
			_ = request.Reply(true, nil)
			server := sftp.NewRequestServer(channel, sftp.InMemHandler())
			_ = server.Serve()
			_ = server.Close()
			return
		case "keepalive@openssh.com":
			_ = request.Reply(true, nil)
		default:
			_ = request.Reply(false, nil)
		}
	}
	_ = channel.Close()
}

// Close stops the server and drops the connections already established, which is
// what a host going away looks like from a client's side.
func (s *Server) Close() {
	_ = s.Listener.Close()
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

// Addr is the listening address.
func (s *Server) Addr() string { return s.Listener.Addr().String() }

// HostPort splits the listening address.
func (s *Server) HostPort(t *testing.T) (host string, port int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	port, err = strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

// WriteClientKey writes an OpenSSH private key and returns the signer and path.
func WriteClientKey(t *testing.T, dir string) (signer ssh.Signer, path string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err = ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "omssh-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return signer, path
}
