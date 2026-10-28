package tunnel

import (
	"fmt"
	"net"
	"strings"
)

// Spec is one forward written the way a person types it.
type Spec struct {
	Kind   Kind
	Listen string
	Target string
}

// ParseSpec reads "L:8080:host:port", "R:9000:host:port" or "D:1080". A spec
// without a prefix is a local forward, the common case.
//
// The bind defaults to loopback for the same reason ssh -L does not: a forward
// that listens everywhere because a colon was missing is a surprise nobody wants.
func ParseSpec(input string) (Spec, error) {
	spec := strings.TrimSpace(input)
	if spec == "" {
		return Spec{}, fmt.Errorf("tunnel: a spec is required")
	}
	kind := Local
	if len(spec) > 1 && spec[1] == ':' {
		switch strings.ToUpper(spec[:1]) {
		case "L":
			kind = Local
		case "R":
			kind = Remote
		case "D":
			kind = Dynamic
		default:
			return Spec{}, fmt.Errorf("tunnel: %q: kind must be L, R or D", input)
		}
		spec = spec[2:]
	}
	if kind == Dynamic {
		bind, port, hasBind := strings.Cut(spec, ":")
		if !hasBind {
			bind, port = "127.0.0.1", spec
		}
		if strings.TrimSpace(port) == "" || strings.TrimSpace(bind) == "" {
			return Spec{}, fmt.Errorf("tunnel: %q: dynamic wants port or bind:port", input)
		}
		return Spec{Kind: Dynamic, Listen: net.JoinHostPort(bind, port)}, nil
	}

	parts := strings.Split(spec, ":")
	bind := "127.0.0.1"
	switch len(parts) {
	case 3:
		// port:host:port
	case 4:
		bind = parts[0]
		parts = parts[1:]
	default:
		return Spec{}, fmt.Errorf("tunnel: %q: want port:host:port or bind:port:host:port", input)
	}
	port, host, targetPort := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
	if port == "" || host == "" || targetPort == "" {
		return Spec{}, fmt.Errorf("tunnel: %q is incomplete", input)
	}
	return Spec{Kind: kind, Listen: net.JoinHostPort(bind, port),
		Target: net.JoinHostPort(host, targetPort)}, nil
}
