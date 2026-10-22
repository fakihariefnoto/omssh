package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// input is the one-line text editor shared by the palette, the filter, the path
// bar and every single-field prompt. One implementation means one set of editing
// keys to learn, everywhere.
type input struct {
	Value  string
	Cursor int // rune offset
	// Masked hides the value on screen while keeping it intact: a secret typed
	// into a form should not be readable over someone's shoulder, and should not
	// be repeated in a screenshot of the interface.
	Masked bool
	// selectAll is set when Set pre-filled the value. The next insertion then
	// replaces it, which is how a text field behaves when it takes focus: pasting
	// a path into a prompt that already holds one must not concatenate the two.
	// Any cursor movement cancels it, so editing a pre-filled name still works.
	selectAll bool
}

// Display is what the form draws: the value, or dots when it is a secret.
func (in input) Display() string {
	if !in.Masked {
		return in.Value
	}
	return strings.Repeat("•", len([]rune(in.Value)))
}

// Set replaces the value, puts the cursor at the end, and marks the value as
// selected so the next insertion replaces it.
func (in *input) Set(v string) {
	in.Value = v
	in.Cursor = len([]rune(v))
	in.selectAll = true
}

// SetForEditing replaces the value with the cursor at the end and no selection,
// for callers that are going to edit what is there rather than replace it.
func (in *input) SetForEditing(v string) {
	in.Set(v)
	in.End()
}

// Clear empties the input.
func (in *input) Clear() { in.Value, in.Cursor, in.selectAll = "", 0, false }

// Insert adds text at the cursor, replacing a pre-filled selection.
func (in *input) Insert(s string) {
	if s == "" {
		return
	}
	if in.selectAll {
		in.Value, in.Cursor, in.selectAll = "", 0, false
	}
	runes := []rune(in.Value)
	if in.Cursor > len(runes) {
		in.Cursor = len(runes)
	}
	out := make([]rune, 0, len(runes)+len(s))
	out = append(out, runes[:in.Cursor]...)
	out = append(out, []rune(s)...)
	out = append(out, runes[in.Cursor:]...)
	in.Value = string(out)
	in.Cursor += len([]rune(s))
}

// Backspace deletes the rune before the cursor.
func (in *input) Backspace() {
	in.selectAll = false
	runes := []rune(in.Value)
	if in.Cursor <= 0 || len(runes) == 0 {
		return
	}
	out := make([]rune, 0, len(runes)-1)
	out = append(out, runes[:in.Cursor-1]...)
	out = append(out, runes[in.Cursor:]...)
	in.Value = string(out)
	in.Cursor--
}

// Delete removes the rune under the cursor.
func (in *input) Delete() {
	in.selectAll = false
	runes := []rune(in.Value)
	if in.Cursor >= len(runes) {
		return
	}
	out := make([]rune, 0, len(runes)-1)
	out = append(out, runes[:in.Cursor]...)
	out = append(out, runes[in.Cursor+1:]...)
	in.Value = string(out)
}

// MoveLeft moves the cursor one rune left.
func (in *input) MoveLeft() {
	in.selectAll = false
	if in.Cursor > 0 {
		in.Cursor--
	}
}

// MoveRight moves the cursor one rune right.
func (in *input) MoveRight() {
	in.selectAll = false
	if in.Cursor < len([]rune(in.Value)) {
		in.Cursor++
	}
}

// Home moves the cursor to the start.
func (in *input) Home() { in.selectAll, in.Cursor = false, 0 }

// End moves the cursor to the end.
func (in *input) End() { in.selectAll, in.Cursor = false, len([]rune(in.Value)) }

// Handle applies an editing key. It reports whether the key was consumed.
func (in *input) Handle(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "backspace":
		in.Backspace()
	case "delete":
		in.Delete()
	case "left":
		in.MoveLeft()
	case "right":
		in.MoveRight()
	case "home", "ctrl+a":
		in.Home()
	case "end", "ctrl+e":
		in.End()
	case "ctrl+u":
		in.Clear()
	case "ctrl+w":
		in.deleteWord()
	case "space":
		in.Insert(" ")
	default:
		text := msg.Key().Text
		if text == "" {
			return false
		}
		// Swallow control characters that are not text.
		for _, r := range text {
			if unicode.IsControl(r) {
				return false
			}
		}
		in.Insert(text)
	}
	// A trailing newline from a paste would otherwise enter the value.
	in.Value = strings.TrimRight(in.Value, "\n")
	in.Cursor = clamp(in.Cursor, 0, len([]rune(in.Value)))
	return true
}

// InsertPaste inserts pasted text into a single-line field: the first line only,
// with control characters dropped, so a paste cannot smuggle a newline or an
// escape sequence into a host name or a path.
func (in *input) InsertPaste(s string) {
	if s == "" {
		return
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	in.Insert(sanitizePaste(s))
}

// sanitizePaste drops control characters and the escape character.
func sanitizePaste(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == 0x1b || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// deleteWord removes the word before the cursor.
func (in *input) deleteWord() {
	in.selectAll = false
	runes := []rune(in.Value)
	i := in.Cursor
	for i > 0 && unicode.IsSpace(runes[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(runes[i-1]) {
		i--
	}
	out := make([]rune, 0, len(runes)-(in.Cursor-i))
	out = append(out, runes[:i]...)
	out = append(out, runes[in.Cursor:]...)
	in.Value = string(out)
	in.Cursor = i
}

// Rendered returns the value with a cursor marker, for display in a text field.
func (in *input) Rendered() string {
	runes := []rune(in.Value)
	if in.Cursor > len(runes) {
		in.Cursor = len(runes)
	}
	before := string(runes[:in.Cursor])
	if in.Cursor >= len(runes) {
		return before + "▏"
	}
	return before + "▏" + string(runes[in.Cursor:])
}
