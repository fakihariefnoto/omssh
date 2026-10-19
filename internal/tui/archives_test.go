package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fakihariefnoto/omssh/internal/archive"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Compressing the selection and extracting it somewhere else is the whole
// archive promise, driven through the same functions the prompts call.
func TestCompressThenExtractThroughTheInterface(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path

	m.selectByName("assets")
	cmd := m.compressTargets("local:" + filepath.Join(root, "bundle.tar.gz"))
	if cmd == nil {
		t.Fatalf("compress should start: %v", m.toasts)
	}
	if done, ok := cmd().(compressDoneMsg); !ok || done.err != nil {
		t.Fatalf("compress failed: %+v", done)
	}
	if _, err := os.Stat(filepath.Join(root, "bundle.tar.gz")); err != nil {
		t.Fatalf("the archive should exist: %v", err)
	}

	// Extract it into a fresh directory on the same side.
	outDir := t.TempDir()
	outFS, err := vfs.NewLocal(outDir)
	if err != nil {
		t.Fatal(err)
	}
	// Put the archive in a filesystem the interface can read, then point the
	// cursor at it.
	m.refresh()
	m.selectByName("bundle.tar.gz")

	// Extract through the prompt's action, into an absolute local path.
	extractCmd := m.extractCursor("local:" + outDir)
	if extractCmd == nil {
		t.Fatalf("extract should start: %v", m.toasts)
	}
	done, ok := extractCmd().(extractDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("extract failed: %+v", done)
	}
	if got, err := os.ReadFile(filepath.Join(outDir, "assets", "logo.svg")); err != nil || !strings.Contains(string(got), "svg") {
		t.Errorf("the extracted file should be there: %v", err)
	}
	_ = outFS
}

// A prompt value can name the side, and a value without one stays on the pane
// that has the focus.
func TestDestinationNamesTheSide(t *testing.T) {
	m := newTestModel(t)
	root := m.Pane().Path

	fsys, path, err := m.parseDestination("local:/tmp")
	if err != nil || fsys == nil || path != "/tmp" {
		t.Errorf("local:/tmp should resolve to the local filesystem, got %q %v", path, err)
	}

	fsys, path, err = m.parseDestination("notes.txt")
	if err != nil || path != fsys.Join(root, "notes.txt") {
		t.Errorf("a bare name should stay on this pane, got %q %v", path, err)
	}

	if _, _, err := m.parseDestination("elsewhere:/tmp"); err == nil {
		t.Error("an unknown side should be refused")
	}
	if _, _, err := m.parseDestination("remote:/tmp"); err == nil {
		t.Error("an unconnected side should be refused")
	}
}

// An extraction the format cannot describe is refused with a name, not a panic.
func TestCompressNeedsAFormatThroughTheInterface(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("README.md")
	if cmd := m.compressTargets("local:" + filepath.Join(m.Pane().Path, "backup")); cmd != nil {
		t.Error("an archive with no format should not start")
	}
	if len(m.toasts) == 0 {
		t.Error("refusing should say why")
	}
}

// The archive action is only offered for something that looks like an archive.
func TestExtractPromptRefusesNonArchives(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("notes.txt")
	m.openExtractPrompt()
	if m.Overlay == OverlayPrompt {
		t.Error("a text file should not open the extract prompt")
	}
	if len(m.toasts) == 0 && m.quitHint == "" {
		t.Error("refusing should say why")
	}
}

var _ = archive.Detect
