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
}

// Set replaces the value and puts the cursor at the end.
func (in *input) Set(v string) {
	in.Value = v
	in.Cursor = len([]rune(v))
}

// Clear empties the input.
func (in *input) Clear() { in.Value, in.Cursor = "", 0 }

// Insert adds text at the cursor.
func (in *input) Insert(s string) {
	if s == "" {
		return
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
	if in.Cursor > 0 {
		in.Cursor--
	}
}

// MoveRight moves the cursor one rune right.
func (in *input) MoveRight() {
	if in.Cursor < len([]rune(in.Value)) {
		in.Cursor++
	}
}

// Home moves the cursor to the start.
func (in *input) Home() { in.Cursor = 0 }

// End moves the cursor to the end.
func (in *input) End() { in.Cursor = len([]rune(in.Value)) }

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

// deleteWord removes the word before the cursor.
func (in *input) deleteWord() {
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
