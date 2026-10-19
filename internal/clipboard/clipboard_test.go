package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToolDir creates a directory of stand-in clipboard tools and puts it on
// PATH. Each tool records its stdin beside itself and prints a fixed answer, so
// both directions can be tested with no desktop.
func fakeToolDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Prepended, not replaced: the stand-in scripts are /bin/sh scripts, and a
	// PATH holding only this directory leaves them unable to find cat or printf.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// addTool installs one stand-in. It returns the path the tool writes its stdin
// to. Both read and write tools are needed for a platform to work: pick() looks
// for the read tool and Write runs the write tool.
func addTool(t *testing.T, dir, name, output string) string {
	t.Helper()
	recorded := filepath.Join(dir, name+".stdin")
	script := "#!/bin/sh\ncat > " + recorded + "\nprintf '%s' '" +
		strings.ReplaceAll(output, "'", `'\''`) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return recorded
}

// usePlatform points the package at a platform's tool table, so the Linux order
// can be checked on macOS and vice versa.
func usePlatform(t *testing.T, goos string) {
	t.Helper()
	saved := table
	table = candidates(goos)
	t.Cleanup(func() { table = saved })
}

// The table must be platform-specific and ordered: on Linux, Wayland's tool
// comes before X11's, because a Wayland session with XWayland has both.
func TestCandidatesAreOrderedPerPlatform(t *testing.T) {
	darwin := candidates("darwin")
	if len(darwin) != 1 || darwin[0].read[0] != "pbpaste" || darwin[0].write[0] != "pbcopy" {
		t.Errorf("darwin should use pbpaste/pbcopy, got %+v", darwin)
	}
	linux := candidates("linux")
	if len(linux) < 3 {
		t.Fatalf("linux should have fallbacks, got %+v", linux)
	}
	if linux[0].read[0] != "wl-paste" || linux[0].write[0] != "wl-copy" {
		t.Errorf("linux should prefer the Wayland tools, got %+v", linux[0])
	}
	if linux[0].read[1] != "--no-newline" {
		t.Errorf("wl-paste should be asked not to add a newline, got %q", linux[0].read[1])
	}
	if got := candidates("windows"); got != nil {
		t.Errorf("windows is not a shipped target, so it should have no candidates: %+v", got)
	}
	if _, err := pick(); err != nil {
		t.Errorf("this platform should resolve a tool: %v", err)
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	usePlatform(t, "windows")
	if _, err := Read(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Read on an unsupported platform = %v, want ErrUnsupported", err)
	}
	if Available() {
		t.Error("Available should be false on an unsupported platform")
	}
}

// Trailing newlines are trimmed: every real clipboard carries them.
func TestReadTrimsTrailingNewlines(t *testing.T) {
	usePlatform(t, "linux")
	dir := fakeToolDir(t)
	addTool(t, dir, "wl-paste", "host.example.com\n")
	got, err := Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "host.example.com" {
		t.Errorf("Read = %q, want the text without the trailing newline", got)
	}
}

// Wayland's tool wins when both are installed, and X11 is the fallback.
func TestWaylandIsPreferredWhenBothExist(t *testing.T) {
	usePlatform(t, "linux")
	dir := fakeToolDir(t)
	for _, name := range []string{"wl-paste", "xclip"} {
		addTool(t, dir, name, name)
	}
	got, err := Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "wl-paste" {
		t.Errorf("Read used %q, want the Wayland tool first", got)
	}
}

func TestWriteFeedsTheTool(t *testing.T) {
	usePlatform(t, "darwin")
	dir := fakeToolDir(t)
	addTool(t, dir, "pbpaste", "")
	recorded := addTool(t, dir, "pbcopy", "")
	if err := Write(context.Background(), "203.0.113.10\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "203.0.113.10\n" {
		t.Errorf("the tool received %q", data)
	}
}

// A machine with no clipboard tool gets a message naming what to install.
func TestMissingToolExplainsItself(t *testing.T) {
	usePlatform(t, "linux")
	t.Setenv("PATH", t.TempDir())
	_, err := Read(context.Background())
	if err == nil {
		t.Fatal("expected an error with nothing on PATH")
	}
	if !strings.Contains(err.Error(), "wl-clipboard") {
		t.Errorf("the error should name a package to install, got %v", err)
	}
	if Available() {
		t.Error("Available should be false with nothing on PATH")
	}
	if err := Write(context.Background(), "x"); err == nil {
		t.Error("Write should fail the same way")
	}
}
