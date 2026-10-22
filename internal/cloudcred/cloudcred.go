// Package cloudcred keeps bucket credentials where secrets belong: the OS
// keyring, not the configuration file.
//
// The access key id is not a secret, but keeping the pair together means one
// place to forget it, and it keeps the config readable as a list of places rather
// than a list of credentials.
package cloudcred

import (
	"errors"
	"strings"

	"github.com/fakihariefnoto/omssh/internal/keys"
	"github.com/fakihariefnoto/omssh/internal/log"
)

// account names the keyring entry for one profile.
func account(profile string) string { return "cloud:" + profile }

// Store saves an access key pair for a profile.
func Store(store *keys.Store, profile, id, secret string) error {
	if store == nil || !store.Available() {
		return keys.ErrNoBackend
	}
	id = strings.TrimSpace(id)
	secret = strings.TrimSpace(secret)
	if id == "" || secret == "" {
		return errors.New("cloudcred: both the access key id and the secret are needed")
	}
	return store.SetPassword(account(profile), log.Secret(id+"\n"+secret))
}

// Load reads a pair, and reports whether one is stored.
func Load(store *keys.Store, profile string) (id, secret string, ok bool) {
	if store == nil || !store.Available() {
		return "", "", false
	}
	value, err := store.Password(account(profile))
	if err != nil || value == "" {
		return "", "", false
	}
	parts := strings.SplitN(string(value), "\n", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// Forget removes the pair for a profile.
func Forget(store *keys.Store, profile string) error {
	if store == nil || !store.Available() {
		return keys.ErrNoBackend
	}
	return store.Forget(account(profile))
}
