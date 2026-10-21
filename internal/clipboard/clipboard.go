// Package clipboard reads and writes the system clipboard.
//
// omssh does not implement a clipboard client of its own: it drives the tool the
// platform already ships, because that is what the rest of the desktop uses.
// Reading is best-effort — a headless Linux box has no clipboard, and that is a
// message to show once rather than an error to fail over.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// command is one way to reach the clipboard, with the arguments to read and
// write.
type command struct {
	read  []string
	write []string
}

// candidates returns the tools to try, in order, for a platform. The order
// matters on Linux: Wayland's own tool is tried before X11's, because a Wayland
// session running XWayland has both and only one of them is the real clipboard.
func candidates(goos string) []command {
	switch goos {
	case "darwin":
		return []command{{read: []string{"pbpaste"}, write: []string{"pbcopy"}}}
	case "linux":
		return []command{
			{read: []string{"wl-paste", "--no-newline"}, write: []string{"wl-copy"}},
			{read: []string{"xclip", "-selection", "clipboard", "-o"}, write: []string{"xclip", "-selection", "clipboard"}},
			{read: []string{"xsel", "-b", "-o"}, write: []string{"xsel", "-b", "-i"}},
		}
	default:
		// Windows is not a shipped target yet. When it is, this becomes the console
		// clipboard API rather than an external tool.
		return nil
	}
}

// table is this platform's tools. Tests replace it so both platform paths can be
// exercised on any machine.
var table = candidates(runtime.GOOS)

// ErrUnsupported is returned when a platform has no clipboard path at all.
var ErrUnsupported = errors.New("clipboard: not supported on this platform")

// ErrNoTool is returned when the platform has tools but none is installed.
var ErrNoTool = errors.New("clipboard: no clipboard tool found; install wl-clipboard, xclip or xsel")

// Available reports whether a clipboard tool is on PATH.
func Available() bool {
	_, err := pick()
	return err == nil
}

// pick returns the first tool that exists.
func pick() (command, error) {
	if len(table) == 0 {
		return command{}, ErrUnsupported
	}
	for _, candidate := range table {
		if _, err := exec.LookPath(candidate.read[0]); err == nil {
			return candidate, nil
		}
	}
	return command{}, ErrNoTool
}

// Read returns the clipboard's text. Trailing newlines are trimmed: a copied
// line almost always carries one and it is never wanted.
func Read(ctx context.Context) (string, error) {
	candidate, err := pick()
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, candidate.read[0], candidate.read[1:]...).Output() //nolint:gosec // the tool comes from our own table
	if err != nil {
		return "", fmt.Errorf("clipboard: reading with %s: %w", candidate.read[0], err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// Write puts text on the clipboard.
func Write(ctx context.Context, text string) error {
	candidate, err := pick()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, candidate.write[0], candidate.write[1:]...) //nolint:gosec // our own table
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard: writing with %s: %w", candidate.write[0], err)
	}
	return nil
}
