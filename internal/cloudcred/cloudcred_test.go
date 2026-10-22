package cloudcred

import (
	"testing"

	"github.com/fakihariefnoto/omssh/internal/keys"
)

func testStore() *keys.Store {
	return keys.NewStore(keys.NewMemBackend(), "omssh-test")
}

// A pair goes in and comes out, and the id and secret stay apart.
func TestRoundTrip(t *testing.T) {
	store := testStore()
	if err := Store(store, "backups", "AKIA123", "s3cret"); err != nil {
		t.Fatal(err)
	}
	id, secret, ok := Load(store, "backups")
	if !ok || id != "AKIA123" || secret != "s3cret" {
		t.Fatalf("Load = %q %q %v", id, secret, ok)
	}

	// A different profile does not see it.
	if _, _, ok := Load(store, "other"); ok {
		t.Error("credentials leaked between profiles")
	}

	if err := Forget(store, "backups"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Load(store, "backups"); ok {
		t.Error("forget should remove the pair")
	}
}

// Half a pair is not a pair.
func TestHalfAPairIsRefused(t *testing.T) {
	store := testStore()
	if err := Store(store, "backups", "AKIA123", ""); err == nil {
		t.Error("a missing secret should be refused")
	}
	if err := Store(store, "backups", "", "s3cret"); err == nil {
		t.Error("a missing id should be refused")
	}
}

// No keyring is a state, not a crash.
func TestNoKeyring(t *testing.T) {
	if err := Store(nil, "backups", "a", "b"); err == nil {
		t.Error("a nil store should report that there is nowhere to save")
	}
	if _, _, ok := Load(nil, "backups"); ok {
		t.Error("a nil store has nothing stored")
	}
}
