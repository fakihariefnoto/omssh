package tunnel

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// dialTest connects to a local listener under a context, as the linter asks.
func dialTest(t *testing.T, addr string) (net.Conn, error) {
	t.Helper()
	var dialer net.Dialer
	return dialer.DialContext(context.Background(), "tcp", addr)
}

// echoDialer returns a connection whose far end echoes: a whole network in one
// function, and it lets the tunnel run with no SSH.
type echoDialer struct{ targets chan string }

func newEchoDialer() *echoDialer { return &echoDialer{targets: make(chan string, 8)} }

func (d *echoDialer) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	d.targets <- addr
	server, client := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }()
		_, _ = io.Copy(server, server)
	}()
	return client, nil
}

func eventRecorder() (func(Event), *[]Event) {
	events := make([]Event, 0, 4)
	return func(event Event) { events = append(events, event) }, &events
}

// -L: a local listener, dialled through the transport.
func TestLocalForward(t *testing.T) {
	dialer := newEchoDialer()
	record, events := eventRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tunnel, err := Start(ctx, Options{Kind: Local, Listen: "127.0.0.1:0", Target: "db.internal:5432",
		Dialer: dialer, OnEvent: record})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tunnel.Close() }()

	client, err := dialTest(t, tunnel.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Errorf("echo = %q", buf)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if target := <-dialer.targets; target != "db.internal:5432" {
		t.Errorf("dialled %q", target)
	}
	if err := tunnel.Close(); err != nil {
		t.Fatal(err)
	}
	// The closing event carries the byte counts, never the bytes.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, event := range *events {
			if event.BytesIn == 4 && event.BytesOut == 4 {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no close event with byte counts: %+v", *events)
}

// -D: a SOCKS5 CONNECT is dialled through the transport.
func TestDynamicForwardSOCKS(t *testing.T) {
	dialer := newEchoDialer()
	tunnel, err := Start(context.Background(), Options{Kind: Dynamic, Listen: "127.0.0.1:0", Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tunnel.Close() }()

	conn, err := dialTest(t, tunnel.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Greeting: version 5, one method, no-auth.
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("greeting reply = %v", reply)
	}
	// CONNECT example.com:80, as a domain name.
	request := make([]byte, 0, 18)
	request = append(request, 0x05, 0x01, 0x00, 0x03, byte(len("example.com")))
	request = append(request, []byte("example.com")...)
	request = append(request, 0x00, 0x50)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 10)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0x00 {
		t.Fatalf("connect reply = %v", head)
	}
	if _, err := conn.Write([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "echo" {
		t.Errorf("echo = %q", buf)
	}
	if target := <-dialer.targets; target != "example.com:80" {
		t.Errorf("target = %q", target)
	}
}

// -R: the server accepts and the connection comes out here.
func TestRemoteForward(t *testing.T) {
	dialer := newEchoDialer()
	listener := &fakeRemoteListener{conns: make(chan net.Conn, 1)}
	tunnel, err := Start(context.Background(), Options{Kind: Remote, Listen: "0.0.0.0:9000",
		Target: "127.0.0.1:3000", Remote: listener, LocalDialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tunnel.Close() }()

	server, client := net.Pipe()
	listener.conns <- server
	if _, err := client.Write([]byte("from server")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("from server"))
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "from server" {
		t.Errorf("echo = %q", buf)
	}
	_ = client.Close()
	if got := <-dialer.targets; got != "127.0.0.1:3000" {
		t.Errorf("local target = %q", got)
	}
}

// A busy port fails with the address in the error, which is what a person needs
// to find the process holding it.
func TestListenCollisionNamesTheAddress(t *testing.T) {
	var listenConfig net.ListenConfig
	busy, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	_, err = Start(context.Background(), Options{Kind: Local, Listen: busy.Addr().String(),
		Target: "x:1", Dialer: newEchoDialer()})
	if err == nil || !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Fatalf("collision error = %v", err)
	}
}

// fakeRemoteListener stands in for *ssh.Client.Listen.
type fakeRemoteListener struct{ conns chan net.Conn }

func (f *fakeRemoteListener) Listen(_, _ string) (net.Listener, error) {
	return &fakeListener{conns: f.conns}, nil
}

type fakeListener struct {
	conns chan net.Conn
	once  bool
}

func (f *fakeListener) Accept() (net.Conn, error) {
	conn, ok := <-f.conns
	if !ok {
		return nil, io.EOF
	}
	return conn, nil
}

func (f *fakeListener) Close() error {
	if !f.once {
		f.once = true
		close(f.conns)
	}
	return nil
}

func (f *fakeListener) Addr() net.Addr { return fakeAddr("server:9000") }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }
