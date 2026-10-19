package keys

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fakihariefnoto/omssh/internal/log"
)

func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(NewMemBackend(), "omssh-test")

	if _, err := store.Password("prod-api"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a host with no stored password should report ErrNotFound, got %v", err)
	}
	if err := store.SetPassword("prod-api", log.Secret("hunter2")); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	secret, err := store.Password("prod-api")
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if secret.Reveal() != "hunter2" {
		t.Errorf("Password = %q", secret.Reveal())
	}
	// The secret cannot be printed by accident.
	if rendered := fmt.Sprintf("%v %s %#v", secret, secret, secret); strings.Contains(rendered, "hunter2") {
		t.Errorf("a stored secret leaked into a format: %q", rendered)
	}

	// Another host is unaffected.
	if _, err := store.Password("staging"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a different host should have no password, got %v", err)
	}
}

func TestPassphraseIsSeparateFromPassword(t *testing.T) {
	store := NewStore(NewMemBackend(), "omssh-test")
	if err := store.SetPassword("prod-api", log.Secret("password")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassphrase("/keys/id_ed25519", log.Secret("passphrase")); err != nil {
		t.Fatal(err)
	}
	secret, err := store.Passphrase("/keys/id_ed25519")
	if err != nil || secret.Reveal() != "passphrase" {
		t.Errorf("Passphrase = %q (%v)", secret.Reveal(), err)
	}
	// A key file is not a host: the two namespaces do not collide.
	if _, err := store.Password("/keys/id_ed25519"); !errors.Is(err, ErrNotFound) {
		t.Error("a key path should not be readable as a host password")
	}
}

// Storing an empty secret removes it, which is what a person means by clearing a
// remembered password.
func TestEmptySecretRemoves(t *testing.T) {
	store := NewStore(NewMemBackend(), "omssh-test")
	if err := store.SetPassword("prod-api", log.Secret("hunter2")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword("prod-api", log.Secret("")); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if _, err := store.Password("prod-api"); !errors.Is(err, ErrNotFound) {
		t.Error("an empty secret should clear the stored one")
	}
}

func TestForget(t *testing.T) {
	backend := NewMemBackend()
	store := NewStore(backend, "omssh-test")
	if err := store.SetPassword("prod-api", log.Secret("hunter2")); err != nil {
		t.Fatal(err)
	}
	if err := store.Forget("prod-api"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, err := store.Password("prod-api"); !errors.Is(err, ErrNotFound) {
		t.Error("Forgot should leave nothing behind")
	}
	// Forgetting twice is not an error, and neither is forgetting a host that
	// never had anything.
	if err := store.Forget("prod-api"); err != nil {
		t.Errorf("Forget on a cleared host = %v, want nil", err)
	}
	if err := store.Forget("never-seen"); err != nil {
		t.Errorf("Forget on an unknown host = %v, want nil", err)
	}
	if backend.Len() != 0 {
		t.Errorf("the backend still holds %d secrets", backend.Len())
	}
}

// Without a keyring every call reports ErrNoBackend, so the interface prompts
// instead of failing.
func TestNoBackendIsReported(t *testing.T) {
	store := NewStore(nil, "omssh-test")
	if store.Available() {
		t.Error("a store with no backend should not report itself available")
	}
	if _, err := store.Password("prod-api"); !errors.Is(err, ErrNoBackend) {
		t.Errorf("Password = %v, want ErrNoBackend", err)
	}
	if err := store.SetPassword("prod-api", log.Secret("x")); !errors.Is(err, ErrNoBackend) {
		t.Errorf("SetPassword = %v, want ErrNoBackend", err)
	}
	if err := store.Forget("prod-api"); !errors.Is(err, ErrNoBackend) {
		t.Errorf("Forget = %v, want ErrNoBackend", err)
	}
}

// A nil store behaves like one with no backend rather than panicking: the
// interface may hold one before it knows whether a keyring exists.
func TestNilStoreIsSafe(t *testing.T) {
	var store *Store
	if store.Available() {
		t.Error("a nil store is not available")
	}
	if _, err := store.Password("prod-api"); !errors.Is(err, ErrNoBackend) {
		t.Errorf("Password = %v, want ErrNoBackend", err)
	}
}

// The default service name is stable: it names the entry in Keychain.
func TestDefaultServiceName(t *testing.T) {
	store := NewStore(NewMemBackend(), "")
	if store.service != "omssh" {
		t.Errorf("service = %q, want %q", store.service, "omssh")
	}
}

// Host account names are normalised, so a host added with different capitalisation
// finds the same secret.
func TestHostAccountIsNormalised(t *testing.T) {
	if got := HostAccount("  Prod-API "); got != "prod-api" {
		t.Errorf("HostAccount = %q", got)
	}
}
