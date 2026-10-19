package ssh

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Host key errors the interface branches on. A changed key is a hard stop, not a
// warning to click past: it can mean a rebuilt server or an attack, and only a
// person can tell those apart.
var (
	// ErrUnknownHost means no key is stored for this host yet: the first-connect
	// case, which asks for consent.
	ErrUnknownHost = errors.New("ssh: host not in known_hosts")
	// ErrHostKeyMismatch means the server presented a different key than the one
	// stored. This is the case that must never be waved through.
	ErrHostKeyMismatch = errors.New("ssh: host key changed")
)

// HostKeyMismatchError carries both fingerprints, because the dialog has to
// show them side by side.
type HostKeyMismatchError struct {
	Host        string
	Offered     string
	OfferedType string
	Stored      []string
	KnownHosts  string
}

// Error renders the mismatch without hiding either key.
func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("%s: the host key for %s has changed\noffered  %s %s\nstored   %s\nthe file %s has not been changed",
		ErrHostKeyMismatch, e.Host, e.OfferedType, e.Offered, strings.Join(e.Stored, ", "), e.KnownHosts)
}

// Unwrap lets errors.Is(err, ErrHostKeyMismatch) work.
func (e *HostKeyMismatchError) Unwrap() error { return ErrHostKeyMismatch }

// UnknownHostError carries what the first-connect dialog needs to show.
type UnknownHostError struct {
	Host        string
	Fingerprint string
	KeyType     string
	KnownHosts  string
}

// Error names the host and its fingerprint.
func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("%s: %s %s", ErrUnknownHost, e.Host, e.Fingerprint)
}

// Unwrap lets errors.Is(err, ErrUnknownHost) work.
func (e *UnknownHostError) Unwrap() error { return ErrUnknownHost }

// Consent is what someone decided about a host key that has never been seen.
// It lives here because it is a host-key concept, not an interface one: the
// handshake is what knows whether a key was stored.
type Consent int

// Consent answers, in the order a prompt usually offers them.
const (
	// ConsentRemember accepts the key and stores it, which is what "yes, and do
	// not ask again" means.
	ConsentRemember Consent = iota
	// ConsentOnce accepts it for this connection only. Nothing is written, so the
	// next connection asks again.
	ConsentOnce
	// ConsentRefuse aborts the connection.
	ConsentRefuse
)

// HostKeys is the known_hosts store: one file, read on demand, appended to after
// consent, never rewritten wholesale.
type HostKeys struct {
	path string
}

// HostKeysAt returns the store at a path. The default is ~/.ssh/known_hosts.
func HostKeysAt(path string) HostKeys { return HostKeys{path: path} }

// DefaultHostKeys is ~/.ssh/known_hosts.
func DefaultHostKeys() (HostKeys, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return HostKeys{}, fmt.Errorf("ssh: locating the home directory: %w", err)
	}
	return HostKeysAt(filepath.Join(home, ".ssh", "known_hosts")), nil
}

// Path is the file the store reads and appends to.
func (h HostKeys) Path() string { return h.path }

// reset clears an in-memory cache. The store re-reads the file, because another
// program (ssh itself, or a terminal beside us) may have changed it.
// check asks the known_hosts library about each name the host could be filed
// under, and reports the first verdict that is not "unknown".
//
// The library's callback insists on a host:port address in both arguments, so
// the port is formatted here rather than left to the caller: for port 22 that
// produces "host:22", which it normalises back to "host" — the same equivalence
// OpenSSH itself applies.
func (h HostKeys) check(names []string, port int, key ssh.PublicKey) error {
	unknown := func(name string) *UnknownHostError {
		return &UnknownHostError{
			Host:        name,
			Fingerprint: ssh.FingerprintSHA256(key),
			KeyType:     key.Type(),
			KnownHosts:  h.path,
		}
	}

	// A missing file is the first-run case, not an error: everything is unknown.
	callback, err := knownhosts.New(h.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return unknown(names[0])
		}
		// An unreadable or malformed file is worth saying out loud: silently
		// treating it as empty would accept a key that was previously pinned.
		return fmt.Errorf("ssh: reading %s: %w", h.path, err)
	}

	var lastUnknown error
	for _, name := range names {
		for _, address := range LookupAddresses(name, port) {
			err := callback(address, &netAddr{address: address}, key)
			if err == nil {
				return nil
			}
			var keyErr *knownhosts.KeyError
			if !errors.As(err, &keyErr) {
				return fmt.Errorf("ssh: verifying %s: %w", name, err)
			}
			if len(keyErr.Want) == 0 {
				// Unknown here; another name or form may still match.
				lastUnknown = unknown(name)
				continue
			}
			stored := make([]string, 0, len(keyErr.Want))
			for _, known := range keyErr.Want {
				stored = append(stored, ssh.FingerprintSHA256(known.Key))
			}
			return &HostKeyMismatchError{
				Host:        name,
				Offered:     ssh.FingerprintSHA256(key),
				OfferedType: key.Type(),
				Stored:      stored,
				KnownHosts:  h.path,
			}
		}
	}
	if lastUnknown != nil {
		return lastUnknown
	}
	return unknown(names[0])
}

// Verify is the callback the handshake uses.
func (h HostKeys) Verify(names []string, port int, key ssh.PublicKey) error {
	return h.check(names, port, key)
}

// LookupAddresses are the forms a host may be filed under, in the order to try.
//
// A known_hosts entry is per (name, port): the plain name means the default port
// and the bracketed form means a specific one, which is what OpenSSH writes. A
// plain entry is checked second as well as first, because it may have been
// written by hand or by an earlier connection on port 22, and refusing it would
// lock someone out of a host they had already accepted.
func LookupAddresses(name string, port int) []string {
	addresses := make([]string, 0, 2)
	if port != 0 && port != 22 {
		addresses = append(addresses, net.JoinHostPort(name, strconv.Itoa(port)))
	}
	// The plain name means the default port. The library's callback parses a
	// host:port either way, so the fallback is spelled with 22 — which it
	// normalises straight back to the plain form.
	return append(addresses, net.JoinHostPort(name, "22"))
}

// HostKeyLine is the name to write for a host and port, in OpenSSH's format.
func HostKeyLine(name string, port int) string {
	if port == 22 || port == 0 {
		return name
	}
	return net.JoinHostPort(name, strconv.Itoa(port))
}

// Add appends a key, which is what "yes" on the first-connect dialog means. It
// appends rather than rewrites: ssh itself may be mid-write, and a rewrite would
// clobber entries we do not manage.
func (h HostKeys) Add(host string, port int, key ssh.PublicKey) error {
	if host == "" {
		return fmt.Errorf("ssh: cannot store a host key without a name")
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return fmt.Errorf("ssh: creating %s: %w", filepath.Dir(h.path), err)
	}
	line := knownhosts.Line([]string{HostKeyLine(host, port)}, key)
	file, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("ssh: opening %s: %w", h.path, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("ssh: writing %s: %w", h.path, err)
	}
	return nil
}

// netAddr is the minimal net.Addr the knownhosts callback wants, in the host:port
// form it parses.
type netAddr struct{ address string }

// Network reports the transport, for the callback's benefit.
func (a *netAddr) Network() string { return "tcp" }

// String is the address the callback splits into host and port.
func (a *netAddr) String() string { return a.address }

// HostKeyPolicy decides what an unknown host means. The plan's modes are strict,
// accept-new and insecure; the interface offers the first two — asking, and
// refusing — and this keeps that decision in one place.
type HostKeyPolicy int

// Policies.
const (
	// PolicyAsk prompts on an unknown host and refuses a changed key. The default.
	PolicyAsk HostKeyPolicy = iota
	// PolicyStrict refuses an unknown host as well, for people who manage
	// known_hosts themselves.
	PolicyStrict
)

// check applies a policy to the store's verdict.
func (h HostKeys) checkWithPolicy(names []string, port int, key ssh.PublicKey, policy HostKeyPolicy) error {
	err := h.check(names, port, key)
	if err == nil {
		return nil
	}
	if policy == PolicyStrict && errors.Is(err, ErrUnknownHost) {
		return fmt.Errorf("ssh: %w (strict host key checking)", err)
	}
	return err
}
