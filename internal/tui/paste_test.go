package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Ctrl+V in a text field pastes the system clipboard. In the file manager, where
// no field has focus, it keeps its documented meaning: stage the selection.
func TestCtrlVPastesIntoTheFocusedField(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	press(t, m, run('a', 0, "a")) // the form has focus on "name"

	_, cmd := m.Update(run('v', tea.ModCtrl, ""))
	if cmd == nil {
		t.Fatal("Ctrl+V in the form should read the clipboard")
	}

	// The read is asynchronous, so deliver its result the way the program would.
	_, _ = m.Update(clipboardMsg{text: "db.example.com\n"})
	if got := m.form.fields[0].in.Value; got != "db.example.com" {
		t.Errorf("the name field holds %q, want the pasted text", got)
	}
	if !strings.Contains(render(m), "db.example.com") {
		t.Error("the pasted text should be visible in the form")
	}
}

// The file manager has no text field, so Ctrl+V is still "paste the staged
// selection" and must not go looking for a clipboard.
func TestCtrlVInTheFileManagerIsNotAClipboardRead(t *testing.T) {
	m := newTestModel(t)
	if m.focusedInput() != nil {
		t.Fatal("no field should have focus in the file manager")
	}
	m.selectByName("README.md")
	press(t, m, keyNamed(tea.KeySpace))
	press(t, m, run('c', tea.ModCtrl, "")) // stage
	press(t, m, run('v', tea.ModCtrl, "")) // paste: stages nothing yet, so it explains
	if m.focusedInput() != nil {
		t.Error("Ctrl+V must not conjure a text field")
	}
	// And a clipboard result arriving with nothing focused is a no-op, not a
	// crash or a stray insertion.
	_, _ = m.Update(clipboardMsg{text: "stray"})
	if got := m.Pane().Filter; strings.Contains(got, "stray") {
		t.Errorf("clipboard text leaked into the pane filter: %q", got)
	}
}

// A terminal paste (bracketed paste) arrives as text and goes to the same place
// a keystroke would.
func TestBracketedPasteGoesToTheFocusedField(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	press(t, m, run('a', 0, "a"))
	press(t, m, tea.KeyPressMsg{Code: tea.KeyTab}) // to "host"
	_, _ = m.Update(tea.PasteMsg{Content: "203.0.113.10"})
	if got := m.form.fields[1].in.Value; got != "203.0.113.10" {
		t.Errorf("the host field holds %q", got)
	}

	// The filter on the connections screen accepts a paste too.
	m2, _ := newConnectionsModel(t, connFixture)
	press(t, m2, run('f', tea.ModCtrl, ""))
	_, _ = m2.Update(tea.PasteMsg{Content: "staging"})
	if got := m2.conn.filter.Value; got != "staging" {
		t.Errorf("the filter holds %q", got)
	}

	// So does the path prompt, which then re-suggests folders.
	m3 := newTestModel(t)
	dir := t.TempDir()
	mustWrite(t, dir+"/one.txt", "x\n")
	press(t, m3, run('l', tea.ModCtrl, ""))
	_, _ = m3.Update(tea.PasteMsg{Content: dir})
	if got := m3.prompt.input.Value; got != dir {
		t.Errorf("the path prompt holds %q, want %q", got, dir)
	}
}

// A field that was pre-filled replaces its value on the first input, the way a
// text field behaves when it takes focus. Without this, pasting a path into the
// prompt would concatenate it with the path already there.
func TestPrefilledFieldsAreReplacedNotAppended(t *testing.T) {
	var in input
	in.Set("~/Projects/old")

	// Typing replaces.
	in.Insert("n")
	if in.Value != "n" {
		t.Errorf("typing should replace the prefill, got %q", in.Value)
	}

	// Pasting replaces.
	in.Set("~/Projects/old")
	in.InsertPaste("~/new")
	if in.Value != "~/new" {
		t.Errorf("pasting should replace the prefill, got %q", in.Value)
	}

	// Moving the cursor first means editing, not replacing: the insert lands
	// where the cursor is, not over the whole value.
	in.Set("notes.txt")
	in.MoveLeft() // between "tx" and the final "t"
	in.Insert("_bak")
	if in.Value != "notes.tx_bakt" {
		t.Errorf("editing after a movement should insert at the cursor, got %q", in.Value)
	}

	// Backspace also cancels the selection, so the prefill can be trimmed.
	in.Set("notes.txt")
	in.Backspace()
	if in.Value != "notes.tx" {
		t.Errorf("backspace should trim the prefill, got %q", in.Value)
	}

	// SetForEditing is for callers that intend to edit what is there.
	in.SetForEditing("draft")
	in.Insert("!")
	if in.Value != "draft!" {
		t.Errorf("SetForEditing should not select, got %q", in.Value)
	}
}

// The rename prompt arrives pre-filled, so typing a new name replaces the old one
// rather than appending to it.
func TestRenamePromptReplacesTheOldName(t *testing.T) {
	m := newTestModel(t)
	m.selectByName("notes.txt")
	press(t, m, keyNamed(tea.KeyF2))
	if got := m.prompt.input.Value; got != "notes.txt" {
		t.Fatalf("the rename prompt should be pre-filled, got %q", got)
	}
	for _, ch := range "report" {
		press(t, m, run(ch, 0, string(ch)))
	}
	if got := m.prompt.input.Value; got != "report" {
		t.Errorf("typing should replace the old name, got %q", got)
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if _, err := os.Stat(filepath.Join(m.Pane().Path, "report")); err != nil {
		t.Errorf("the rename did not happen: %v", err)
	}
}

// A paste is text from the outside world: a newline or an escape sequence in it
// must not reach a field.
func TestPasteIsSanitised(t *testing.T) {
	var in input
	in.InsertPaste("first line\nsecond line")
	if in.Value != "first line" {
		t.Errorf("only the first line should be kept, got %q", in.Value)
	}
	in.Set("")
	in.InsertPaste("host\x1b[31mname\x07")
	if in.Value != "host[31mname" {
		t.Errorf("control characters should be dropped, got %q", in.Value)
	}
	in.Set("")
	in.InsertPaste("\n\r\n")
	if in.Value != "" {
		t.Errorf("a paste of nothing but newlines should insert nothing, got %q", in.Value)
	}
}

// A clipboard that cannot be read says so, and changes nothing.
func TestClipboardFailureIsReported(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	press(t, m, run('a', 0, "a"))
	_, _ = m.Update(clipboardMsg{err: clipboardError{}})
	if len(m.toasts) == 0 {
		t.Fatal("a clipboard failure should be reported")
	}
	if !strings.Contains(ansi.Strip(render(m)), "Clipboard unavailable") {
		t.Errorf("the toast should say what happened:\n%s", ansi.Strip(render(m)))
	}
	if got := m.form.fields[0].in.Value; got != "" {
		t.Errorf("the field should be untouched, it holds %q", got)
	}
}

type clipboardError struct{}

func (clipboardError) Error() string { return "no clipboard tool found" }
