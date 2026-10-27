package session

import (
	"errors"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Shell is one interactive shell on a live session's connection. It is the
// embedded terminal: the same SSH channel ssh itself would open, with a real PTY
// and the bytes forwarded by the interface instead of handed to another process.
type Shell struct {
	session *ssh.Session
	in      io.WriteCloser

	closeOnce sync.Once
	closeErr  error
}

// OpenShell opens a PTY and a login shell. Output from the server is written to
// the given writer as it arrives; that writer is where the screen emulator and,
// when recording, the .cast file sit.
func (s *Session) OpenShell(cols, rows int, output io.Writer) (*Shell, error) {
	if cols < 1 || rows < 1 {
		return nil, errors.New("session: a shell needs a positive size")
	}
	channel, err := s.Client.NewSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := channel.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = channel.Close()
		return nil, err
	}
	stdin, err := channel.StdinPipe()
	if err != nil {
		_ = channel.Close()
		return nil, err
	}
	stdout, err := channel.StdoutPipe()
	if err != nil {
		_ = channel.Close()
		return nil, err
	}
	stderr, err := channel.StderrPipe()
	if err != nil {
		_ = channel.Close()
		return nil, err
	}
	if err := channel.Shell(); err != nil {
		_ = channel.Close()
		return nil, err
	}
	// Both streams reach the same screen: a remote program's stderr on a PTY is
	// normally merged by the server, but a server that keeps them apart must not
	// lose half its output.
	go func() { _, _ = io.Copy(output, stdout) }()
	go func() { _, _ = io.Copy(output, stderr) }()
	return &Shell{session: channel, in: stdin}, nil
}

// Write sends keystrokes to the remote shell.
func (s *Shell) Write(p []byte) (int, error) { return s.in.Write(p) }

// Resize tells the remote side the window changed, so curses programs redraw
// instead of writing past the edge.
func (s *Shell) Resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return nil
	}
	return s.session.WindowChange(rows, cols)
}

// Wait blocks until the remote shell exits.
func (s *Shell) Wait() error { return s.session.Wait() }

// Close ends the shell. It is safe to call more than once and after Wait.
func (s *Shell) Close() error {
	s.closeOnce.Do(func() {
		_ = s.in.Close()
		s.closeErr = s.session.Close()
	})
	return s.closeErr
}
