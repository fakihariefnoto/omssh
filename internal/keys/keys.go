// Package keys stores the secrets a connection needs, in the operating system's
// own keyring (ADR 0005).
//
// Two rules shape it. First, a secret is a log.Secret: it cannot be formatted
// into anything but "[redacted]", so it cannot reach a log by accident, and the
// one place that needs the plaintext has to say so out loud. Second, the keyring
// is optional: a headless machine has none, and that is a prompt rather than a
// failure.
package keys

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/fakihariefnoto/omssh/internal/log"
)

// Backend is a secret store: three operations, no policy.
type Backend interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

// ErrNotFound is returned when a secret has never been stored, which is not a
// failure: it is the first connection to a host.
var ErrNotFound = errors.New("keys: no stored secret")

// ErrNoBackend is returned when the platform has no keyring at all.
var ErrNoBackend = errors.New("keys: no keyring on this machine")

// Store keeps secrets in a backend, keyed by service and account.
type Store struct {
	backend Backend
	service string
}

// NewStore returns a store. A nil backend means no keyring is available, and
// every lookup reports ErrNoBackend so the caller prompts instead.
func NewStore(backend Backend, service string) *Store {
	if service == "" {
		service = "omssh"
	}
	return &Store{backend: backend, service: service}
}

// Available reports whether there is anywhere to store secrets.
func (s *Store) Available() bool { return s != nil && s.backend != nil }

// Password returns the password for a host, if one is stored.
func (s *Store) Password(host string) (log.Secret, error) {
	return s.get("password:" + host)
}

// SetPassword stores a password, which is what "remember" on a prompt means.
func (s *Store) SetPassword(host string, secret log.Secret) error {
	return s.set("password:"+host, secret)
}

// Passphrase returns the passphrase for a key file, if one is stored.
func (s *Store) Passphrase(keyPath string) (log.Secret, error) {
	return s.get("passphrase:" + keyPath)
}

// SetPassphrase stores a passphrase for a key file.
func (s *Store) SetPassphrase(keyPath string, secret log.Secret) error {
	return s.set("passphrase:"+keyPath, secret)
}

// Forget removes everything stored for a host, which is what a plain "forget
// this host" in the interface means.
func (s *Store) Forget(host string) error {
	if !s.Available() {
		return ErrNoBackend
	}
	err := s.backend.Delete(s.service, "password:"+host)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (s *Store) get(account string) (log.Secret, error) {
	if !s.Available() {
		return "", ErrNoBackend
	}
	value, err := s.backend.Get(s.service, account)
	if err != nil {
		return "", err
	}
	return log.Secret(value), nil
}

func (s *Store) set(account string, secret log.Secret) error {
	if !s.Available() {
		return ErrNoBackend
	}
	if secret.Reveal() == "" {
		return s.backend.Delete(s.service, account)
	}
	return s.backend.Set(s.service, account, secret.Reveal())
}

// Keyring is the operating system's keyring: Keychain on macOS, Secret Service
// on Linux, Credential Manager on Windows.
type Keyring struct{}

// Get reads a secret from the OS keyring.
func (Keyring) Get(service, account string) (string, error) {
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("keys: reading from the keyring: %w", err)
	}
	return value, nil
}

// Set writes a secret to the OS keyring.
func (Keyring) Set(service, account, secret string) error {
	if err := keyring.Set(service, account, secret); err != nil {
		return fmt.Errorf("keys: writing to the keyring: %w", err)
	}
	return nil
}

// Delete removes a secret from the OS keyring.
func (Keyring) Delete(service, account string) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("keys: removing from the keyring: %w", err)
	}
	return nil
}

// Default returns a store over the OS keyring, or one that reports ErrNoBackend
// when the platform has none (or the keyring is locked and unreachable).
func Default(service string) *Store {
	if !keyringAvailable() {
		return NewStore(nil, service)
	}
	return NewStore(Keyring{}, service)
}

// keyringAvailable probes the keyring once. On Linux this is a D-Bus call that
// can fail in a container or over a bare SSH session, which is exactly the case
// the prompt fallback exists for.
func keyringAvailable() bool {
	const probe = "omssh-keyring-probe"
	if err := keyring.Set(probe, probe, "probe"); err != nil {
		return false
	}
	_ = keyring.Delete(probe, probe)
	return true
}

// MemBackend is an in-memory store, for tests.
type MemBackend struct {
	values map[string]string
}

// NewMemBackend returns an empty in-memory store.
func NewMemBackend() *MemBackend {
	return &MemBackend{values: map[string]string{}}
}

// Get returns a stored secret.
func (m *MemBackend) Get(service, account string) (string, error) {
	value, ok := m.values[service+"\x00"+account]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// Set stores a secret.
func (m *MemBackend) Set(service, account, secret string) error {
	m.values[service+"\x00"+account] = secret
	return nil
}

// Delete removes a secret.
func (m *MemBackend) Delete(service, account string) error {
	key := service + "\x00" + account
	if _, ok := m.values[key]; !ok {
		return ErrNotFound
	}
	delete(m.values, key)
	return nil
}

// Len is how many secrets are stored, for tests.
func (m *MemBackend) Len() int { return len(m.values) }

// HostAccount names the account a host's secret is filed under. It is kept in one
// place so a rename of the scheme cannot half-happen.
func HostAccount(target string) string {
	return strings.ToLower(strings.TrimSpace(target))
}
