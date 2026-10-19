package ssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Question is one prompt from keyboard-interactive authentication: a password, a
// one-time code, or a yes/no.
type Question struct {
	Prompt string
	Echo   bool
}

// Auth is the credential chain, tried in the order that avoids asking a person
// anything: the agent first, then the identity files, and only then a password.
// A two-factor server is handled by keyboard-interactive, which is also where
// many password setups end up.
type Auth struct {
	// Agent offers the keys held by $SSH_AUTH_SOCK.
	Agent bool
	// Keys are identity files to try, in order.
	Keys []string
	// Passphrase is called for an encrypted key the agent cannot unlock. Return
	// an error to abandon that key.
	Passphrase func(keyPath string) (string, error)
	// Password is called for password authentication.
	Password func() (string, error)
	// KeyboardInteractive answers prompts. When nil, Password is used for
	// single-question prompts.
	KeyboardInteractive func(questions []Question) ([]string, error)
	// Banner receives the text a server sends before authentication.
	Banner func(text string)
}

// methods builds the authentication list. Only mechanisms that can actually
// produce a secret are included: offering an empty password method makes some
// servers log a failed attempt before the key is tried.
func (a Auth) methods() []ssh.AuthMethod {
	methods := make([]ssh.AuthMethod, 0, 4)

	if a.Agent {
		if signers := a.agentSigners(); len(signers) > 0 {
			methods = append(methods, ssh.PublicKeys(signers...))
		}
	}
	if signers := a.keySigners(); len(signers) > 0 {
		methods = append(methods, ssh.PublicKeys(signers...))
	}
	if a.KeyboardInteractive != nil {
		methods = append(methods, ssh.KeyboardInteractive(func(_, _ string, questions []string, echos []bool) ([]string, error) {
			asked := make([]Question, 0, len(questions))
			for i, q := range questions {
				echo := false
				if i < len(echos) {
					echo = echos[i]
				}
				asked = append(asked, Question{Prompt: q, Echo: echo})
			}
			return a.KeyboardInteractive(asked)
		}))
	} else if a.Password != nil {
		methods = append(methods, ssh.KeyboardInteractive(a.answerWithPassword))
	}
	if a.Password != nil {
		methods = append(methods, ssh.PasswordCallback(func() (string, error) {
			return a.Password()
		}))
	}
	return methods
}

// answerWithPassword answers a single-question keyboard-interactive challenge
// with the password, which is how many servers ask for one.
func (a Auth) answerWithPassword(_, _ string, questions []string, _ []bool) ([]string, error) {
	password, err := a.Password()
	if err != nil {
		return nil, err
	}
	answers := make([]string, len(questions))
	for i := range answers {
		answers[i] = password
	}
	return answers, nil
}

// agentSigners lists the keys the running agent holds. A missing or unhelpful
// agent is not an error: it just contributes nothing.
func (a Auth) agentSigners() []ssh.Signer {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil
	}
	// The socket path is the user's own environment, not input from anywhere else,
	// so dialing it is the point rather than a risk.
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "unix", socket)
	if err != nil {
		return nil
	}
	// The client is kept alive by the connection, which lives for the handshake;
	// the signers hold what they need.
	client := agent.NewClient(conn)
	signers, err := client.Signers()
	if err != nil {
		_ = conn.Close()
		return nil
	}
	return signers
}

// keySigners loads the identity files. A key that cannot be read or unlocked is
// skipped with a reason recorded for the caller, rather than failing the whole
// attempt: one stale path in a config should not stop a working key from being
// tried.
func (a Auth) keySigners() []ssh.Signer {
	signers := make([]ssh.Signer, 0, len(a.Keys))
	for _, path := range a.Keys {
		if signer, err := loadSigner(path, a.Passphrase); err == nil {
			signers = append(signers, signer)
		}
	}
	return signers
}

// loadSigner reads and, when needed, decrypts an identity file.
func loadSigner(path string, passphrase func(string) (string, error)) (ssh.Signer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("ssh: empty identity path")
	}
	expanded := expandHome(path)
	pem, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("ssh: reading identity %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, fmt.Errorf("ssh: parsing identity %s: %w", path, err)
	}
	if passphrase == nil {
		return nil, fmt.Errorf("ssh: identity %s is encrypted and no passphrase was offered", path)
	}
	secret, err := passphrase(path)
	if err != nil {
		return nil, fmt.Errorf("ssh: unlocking %s: %w", path, err)
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(secret))
	if err != nil {
		return nil, fmt.Errorf("ssh: unlocking %s: %w", path, err)
	}
	return signer, nil
}

// expandHome turns a leading ~ into the home directory, the way ssh itself does
// for IdentityFile.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return home + path[1:]
}
