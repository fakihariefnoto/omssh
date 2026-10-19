package sftpfs_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
	sftpfs "github.com/fakihariefnoto/omssh/internal/vfs/sftp"
	"github.com/fakihariefnoto/omssh/internal/vfs/vfstest"
)

// sftpServer is an in-process SSH server that serves an in-memory SFTP
// subsystem. Using the in-memory filesystem rather than the real one means the
// conformance suite can create and delete anything it likes without touching
// this machine's disk.
type sftpServer struct {
	listener net.Listener
	signer   ssh.Signer
	config   *ssh.ServerConfig
}

func newSFTPServer(t *testing.T) *sftpServer {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}

	srv := &sftpServer{signer: hostSigner}
	config := &ssh.ServerConfig{
		// Any key is accepted: authentication is not what this test is about,
		// and internal/ssh has its own tests for it.
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	config.AddHostKey(hostSigner)
	srv.config = config

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.listener = listener
	go srv.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return srv
}

func (s *sftpServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *sftpServer) handle(conn net.Conn) {
	sshConn, channels, requests, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		_ = conn.Close()
		return
	}
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
		go s.serveSession(ch, channelRequests)
	}
	_ = sshConn.Close()
}

func (s *sftpServer) serveSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	for request := range requests {
		if request.Type != "subsystem" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Name string }
		_ = ssh.Unmarshal(request.Payload, &payload)
		if payload.Name != "sftp" {
			_ = request.Reply(false, nil)
			continue
		}
		_ = request.Reply(true, nil)
		// The in-memory handlers make this a real SFTP server with no disk
		// underneath it: create, rename, chmod, symlink and remove all work.
		server := sftp.NewRequestServer(channel, sftp.InMemHandler())
		_ = server.Serve()
		_ = server.Close()
		return
	}
	_ = channel.Close()
}

// dial connects through internal/ssh and returns an SFTP filesystem on it, which
// is exactly the path the interface will take.
func (s *sftpServer) dial(t *testing.T) *sftpfs.SFTP {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	keys := omssh.HostKeysAt(filepath.Join(home, ".ssh", "known_hosts"))
	if err := keys.Add("memhost", s.port(t), s.signer.PublicKey()); err != nil {
		t.Fatal(err)
	}

	// A client key, on disk, so the auth chain runs for real.
	clientKey, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = clientKey
	block, err := ssh.MarshalPrivateKey(private, "omssh-test")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(home, ".ssh", "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	host, port := s.hostPort(t)
	target, err := omssh.TargetFrom(sshconfig.Spec{
		Alias: "memhost", HostName: host, User: "tester", Port: port, IdentityFile: keyPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := omssh.Dial(ctx, target, omssh.Auth{Keys: []string{keyPath}}, keys)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	sftpClient, err := sftp.NewClient(client.SSH())
	if err != nil {
		t.Fatalf("opening the SFTP subsystem: %v", err)
	}
	fsys, err := sftpfs.New(sftpClient, "/", "memhost")
	if err != nil {
		t.Fatalf("wrapping the SFTP client: %v", err)
	}
	t.Cleanup(func() { _ = fsys.Close() })
	return fsys
}

func (s *sftpServer) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port := 0
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	return host, port
}

func (s *sftpServer) port(t *testing.T) int {
	t.Helper()
	_, port := s.hostPort(t)
	return port
}

// The whole point of the abstraction: the same suite the local filesystem
// passes, run over a network protocol, through a real handshake.
func TestSFTPConformance(t *testing.T) {
	srv := newSFTPServer(t)
	fsys := srv.dial(t)

	// The in-memory server is rooted at "/", and so is this filesystem.
	vfstest.Run(t, fsys)
}

// A rooted SFTP filesystem is confined to its base: a climbing path cannot reach
// the directory above it.
func TestSFTPConfinesToItsBase(t *testing.T) {
	srv := newSFTPServer(t)
	fsys := srv.dial(t)

	if fsys.Root() != "/" {
		t.Fatalf("this test assumes the in-memory server's root, got %q", fsys.Root())
	}
	// Renaming the root is refused before any request is sent.
	if err := fsys.RemoveAll("/"); err == nil {
		t.Error("RemoveAll on the root must be refused")
	}
	// A path that climbs lands back at the root rather than above it.
	if err := fsys.MkdirAll("/inside", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsys.MkdirAll("/inside/../../clamped", 0o755); err != nil {
		t.Fatalf("MkdirAll through a climbing path: %v", err)
	}
	if _, err := fsys.Stat("/clamped"); err != nil {
		t.Errorf("the clamped path should exist at the root: %v", err)
	}
}

// Path helpers must produce the interface's shape, not the server's.
func TestSFTPPathHelpers(t *testing.T) {
	srv := newSFTPServer(t)
	fsys := srv.dial(t)

	if got := fsys.Join("/dir", "sub", "file"); got != "/dir/sub/file" {
		t.Errorf("Join = %q", got)
	}
	if got := fsys.Dir("/dir/sub/file"); got != "/dir/sub" {
		t.Errorf("Dir = %q", got)
	}
	if got := fsys.Base("/dir/sub/file"); got != "file" {
		t.Errorf("Base = %q", got)
	}
	if got := fsys.String(); got != "memhost" {
		t.Errorf("String = %q, want the label", got)
	}
	abs, err := fsys.Abs("/dir/file")
	if err != nil || abs != "/dir/file" {
		t.Errorf("Abs = %q (%v)", abs, err)
	}
}
