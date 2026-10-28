package tunnel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// SOCKS5, the CONNECT command only. -D is a way to reach the far network, not a
// general proxy: no authentication (the tunnel is SSH), no BIND, no UDP ASSOCIATE.

const (
	socksVersion   = 0x05
	socksNoAuth    = 0x00
	socksConnect   = 0x01
	socksIPv4      = 0x01
	socksDomain    = 0x03
	socksIPv6      = 0x04
	socksSucceeded = 0x00
	socksFailure   = 0x01
)

// serveSOCKS completes the handshake on an accepted connection and returns the
// upstream connection and the target it chose.
func (t *Tunnel) serveSOCKS(client net.Conn) (net.Conn, string, error) {
	if err := socksHandshake(client); err != nil {
		return nil, "", err
	}
	target, err := socksRequest(client)
	if err != nil {
		_ = socksReply(client, socksFailure)
		return nil, target, err
	}
	upstream, err := t.options.Dialer.DialContext(context.Background(), "tcp", target)
	if err != nil {
		_ = socksReply(client, socksFailure)
		return nil, target, err
	}
	if err := socksReply(client, socksSucceeded); err != nil {
		_ = upstream.Close()
		return nil, target, err
	}
	return upstream, target, nil
}

// socksHandshake reads the greeting and accepts "no authentication".
func socksHandshake(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return fmt.Errorf("socks: version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	offered := false
	for _, method := range methods {
		if method == socksNoAuth {
			offered = true
		}
	}
	if !offered {
		_, _ = conn.Write([]byte{socksVersion, 0xff})
		return errors.New("socks: the client did not offer no-auth")
	}
	_, err := conn.Write([]byte{socksVersion, socksNoAuth})
	return err
}

// socksRequest reads a CONNECT request and returns "host:port".
func socksRequest(conn net.Conn) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[0] != socksVersion || header[1] != socksConnect {
		return "", fmt.Errorf("socks: command %d, want CONNECT", header[1])
	}
	var host string
	switch header[3] {
	case socksIPv4:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = net.IP(addr).String()
	case socksIPv6:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", err
		}
		host = net.IP(addr).String()
	case socksDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", err
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return "", err
		}
		host = string(name)
	default:
		return "", fmt.Errorf("socks: address type %d", header[3])
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), nil
}

// socksReply says whether the connection was made. The bound address is zero:
// the client does not need it for CONNECT.
func socksReply(conn net.Conn, status byte) error {
	_, err := conn.Write([]byte{socksVersion, status, 0x00, socksIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
