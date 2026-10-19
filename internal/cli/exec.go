package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/fakihariefnoto/omssh/internal/log"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// newExecCmd runs one command on a host and prints its output, for the times a
// person wants the answer rather than a workspace:
//
//	omssh exec prod -- systemctl status api
//
// It exits with the remote command's status, so it composes in a pipeline.
func newExecCmd(opts *Options, stdout, stderr io.Writer) *cobra.Command {
	var wantTTY bool
	cmd := &cobra.Command{
		Use:   "exec <host> -- <command> [args...]",
		Short: "Run a command on a host and print the result",
		Long: "Run one command over SSH without starting the interface.\n" +
			"The exit status is the remote command's, so `omssh exec prod -- true`\n" +
			"is safe in a script. --tty allocates a terminal for commands that want\n" +
			"one, like top or a remote shell.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmdObj *cobra.Command, args []string) error {
			host := args[0]
			command := args[1:]
			if dot := cmdObj.ArgsLenAtDash(); dot >= 0 {
				// Everything after "--" is the command, including flags meant for
				// the remote side rather than for us.
				host = args[0]
				command = args[dot:]
			}
			return runExec(opts, host, command, wantTTY, stdout, stderr)
		},
	}
	cmd.Flags().BoolVar(&wantTTY, "tty", false, "allocate a terminal on the remote side")
	return cmd
}

// runExec connects, runs the command, and reports its exit status.
func runExec(opts *Options, host string, command []string, wantTTY bool, stdout, stderr io.Writer) error {
	cfg, _, err := loadConfig(opts)
	if err != nil {
		return err
	}
	_ = cfg

	target, err := execTarget(host)
	if err != nil {
		return err
	}
	manager, err := session.New(session.Options{Prompter: &cliPrompter{in: os.Stdin, err: stderr}})
	if err != nil {
		return err
	}
	defer func() { _ = manager.CloseAll() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	live, err := manager.Connect(ctx, target)
	if err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}

	remote, err := live.Client.SSH().NewSession()
	if err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}
	defer func() { _ = remote.Close() }()

	remote.Stdin = os.Stdin
	remote.Stdout = stdout
	remote.Stderr = stderr
	if wantTTY {
		width, height := 80, 24
		modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
		if err := remote.RequestPty("xterm-256color", height, width, modes); err != nil {
			return fmt.Errorf("%s: cannot allocate a terminal: %w", host, err)
		}
	}

	line := shellJoin(command)
	if err := remote.Run(line); err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return &remoteExitError{code: exit.ExitStatus()}
		}
		return fmt.Errorf("%s: %w", host, err)
	}
	return nil
}

// remoteExitError carries a remote exit code out of the command.
type remoteExitError struct{ code int }

func (e *remoteExitError) Error() string {
	return fmt.Sprintf("remote command exited with status %d", e.code)
}

// ExitCode reports the status a failed exec should leave the process with.
func (e *remoteExitError) ExitCode() int { return e.code }

// execTarget resolves a host through ~/.ssh/config, so ports, users, keys and
// jump hosts come from the same place ssh would read them.
func execTarget(host string) (omssh.Target, error) {
	path, err := sshconfig.DefaultPath()
	if err != nil {
		return omssh.TargetFrom(sshconfig.Spec{Alias: host})
	}
	file, err := sshconfig.Load(path)
	if err != nil {
		return omssh.TargetFrom(sshconfig.Spec{Alias: host})
	}
	if block, ok := file.Lookup(host); ok {
		return omssh.TargetFrom(block.Spec())
	}
	// A name that is not in the config is still a host: ssh's defaults apply.
	return omssh.TargetFrom(sshconfig.Spec{Alias: host})
}

// shellJoin quotes arguments so the remote shell sees what was typed.
func shellJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			parts = append(parts, "''")
			continue
		}
		if isPlainWord(arg) {
			parts = append(parts, arg)
			continue
		}
		parts = append(parts, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return strings.Join(parts, " ")
}

// isPlainWord reports whether a word can be passed to the remote shell unquoted.
func isPlainWord(word string) bool {
	for _, r := range word {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/', r == ':', r == '@', r == '=':
		default:
			return false
		}
	}
	return true
}

// cliPrompter answers what a connection needs when there is no interface.
type cliPrompter struct {
	in  *os.File
	err io.Writer
}

// ConsentHostKey prints the fingerprint and asks. A key that has never been seen
// is a decision, so the answer is a word rather than a keystroke: "yes" stores it,
// "once" does not, anything else refuses.
func (p *cliPrompter) ConsentHostKey(unknown *omssh.UnknownHostError) (omssh.Consent, error) {
	fmt.Fprintf(p.err, "The authenticity of %s cannot be established.\n%s key fingerprint is %s.\n"+
		"Trust it? [y]es, [o]nce, [N]o: ", unknown.Host, unknown.KeyType, unknown.Fingerprint)
	answer, err := readLine(p.in)
	if err != nil {
		return omssh.ConsentRefuse, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return omssh.ConsentRemember, nil
	case "o", "once":
		return omssh.ConsentOnce, nil
	default:
		return omssh.ConsentRefuse, nil
	}
}

// Password reads a password without echoing it.
func (p *cliPrompter) Password(target omssh.Target, retry bool) (log.Secret, bool, error) {
	prefix := target.User + "@" + target.Host + "'s password: "
	if retry {
		prefix = "Permission denied, try again. " + prefix
	}
	value, err := readSecret(prefix)
	if err != nil {
		return "", false, err
	}
	return log.Secret(value), true, nil
}

// Passphrase reads the passphrase for an encrypted key.
func (p *cliPrompter) Passphrase(keyPath string) (log.Secret, bool, error) {
	value, err := readSecret("Passphrase for " + keyPath + ": ")
	if err != nil {
		return "", false, err
	}
	return log.Secret(value), true, nil
}

// readSecret reads a secret without echoing it. stty is used rather than a
// terminal library because every target has it and the alternative is a
// dependency for one keystroke of feedback.
func readSecret(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("no terminal to ask for a password: %w", err)
	}
	defer func() { _ = tty.Close() }()

	fmt.Fprint(tty, prompt)
	off := exec.CommandContext(context.Background(), "stty", "-echo")
	off.Stdin = tty
	_ = off.Run()
	line, readErr := readLine(tty)
	on := exec.CommandContext(context.Background(), "stty", "echo")
	on.Stdin = tty
	_ = on.Run()
	fmt.Fprintln(tty)
	if readErr != nil {
		return "", readErr
	}
	return strings.TrimSpace(line), nil
}

// readLine reads one line from the terminal.
func readLine(in *os.File) (string, error) {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return string(line), nil
			}
			return "", err
		}
		if n == 0 {
			continue
		}
		if buf[0] == '\n' {
			return string(line), nil
		}
		if buf[0] != '\r' {
			line = append(line, buf[0])
		}
	}
}
