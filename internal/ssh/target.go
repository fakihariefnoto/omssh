// Package ssh reaches hosts with a credential chain, verifies their keys, and
// keeps the connections alive so many tabs share one.
//
// It is deliberately thin: everything interesting is either configuration
// (internal/sshconfig), credentials (internal/keys, later), or the filesystem
// built on top (internal/vfs/sftp). This package owns the handshake, the host
// key decision, and the pool.
package ssh

import (
	"fmt"
	"net"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// Target is everything needed to reach one host, resolved from ~/.ssh/config
// plus the defaults ssh(1) itself applies.
type Target struct {
	Alias     string
	Host      string
	Port      int
	User      string
	Identity  string
	ProxyJump string
	Timeout   time.Duration
}

// DefaultTimeout bounds a dial. A host that does not answer in this long is
// treated as unreachable rather than left hanging in the connection list.
const DefaultTimeout = 15 * time.Second

// TargetFrom resolves a config block into a target, applying ssh's own defaults:
// the alias is the address when no HostName is given, the port is 22, and the
// user is the one running omssh.
func TargetFrom(spec sshconfig.Spec) (Target, error) {
	alias := strings.TrimSpace(spec.Alias)
	if alias == "" {
		return Target{}, fmt.Errorf("ssh: a host name is required")
	}
	target := Target{
		Alias:     alias,
		Host:      strings.TrimSpace(spec.HostName),
		Port:      spec.Port,
		User:      strings.TrimSpace(spec.User),
		Identity:  strings.TrimSpace(spec.IdentityFile),
		ProxyJump: strings.TrimSpace(spec.ProxyJump),
		Timeout:   DefaultTimeout,
	}
	if target.Host == "" {
		// `Host example.com` with no HostName is the common short form.
		target.Host = alias
	}
	if target.Port == 0 {
		target.Port = 22
	}
	if target.User == "" {
		if account, err := currentUser(); err == nil {
			target.User = account
		}
	}
	return target, nil
}

// currentUser is the account running omssh, which ssh uses when a config names
// no user. It is a variable so tests are not at the mercy of the machine.
var currentUser = func() (string, error) {
	account, err := user.Current()
	if err != nil {
		return "", err
	}
	return account.Username, nil
}

// Address is host:port, the form net.Dial wants.
func (t Target) Address() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// String names the target the way the interface does: user@host:port, with the
// alias when it differs from the address.
func (t Target) String() string {
	label := t.Host
	if t.Alias != "" && t.Alias != t.Host {
		label = t.Alias + " (" + t.Host + ")"
	}
	account := t.User
	if account == "" {
		account = "?"
	}
	if t.Port != 22 {
		return fmt.Sprintf("%s@%s:%d", account, label, t.Port)
	}
	return account + "@" + label
}

// PoolKey is how the connection pool decides two targets are the same
// connection: same address, user, key and proxy chain, or they are not shared.
func (t Target) PoolKey() string {
	return strings.Join([]string{
		strings.ToLower(t.Host),
		strconv.Itoa(t.Port),
		t.User,
		t.Identity,
		strings.ToLower(t.ProxyJump),
	}, "\x00")
}

// HostKeyAlias is the name a host key is stored under. OpenSSH writes the Host
// pattern, not the resolved address, which is why an alias with a HostName of
// 10.0.0.5 still stores its key under the alias.
func (t Target) HostKeyAlias() string {
	if t.Alias != "" {
		return t.Alias
	}
	return t.Host
}

// HostKeyNames are the names a stored key may be filed under, in the order ssh
// tries them: the alias people type, then the resolved address. The port is not
// part of a name — the store formats that, the way known_hosts itself does.
func (t Target) HostKeyNames() []string {
	names := []string{t.HostKeyAlias()}
	if t.Host != t.HostKeyAlias() {
		names = append(names, t.Host)
	}
	return names
}
