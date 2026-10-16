package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
)

// previewBytes bounds how much of a file the preview reads, so opening a huge
// log does not stall the interface.
const previewBytes = 64 << 10

// hexBytes is how much of a binary the hex view shows.
const hexBytes = 256

// renderPreview draws the right half of the focused pane: the contents of the
// entry under the cursor, or an explanation of why it cannot be shown.
func (m *Model) renderPreview(width, rows int) string {
	if width <= 0 || rows <= 0 {
		return ""
	}
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok {
		return m.previewLines([]string{m.st.PaneEmpty.Render("Nothing under the cursor")}, width, rows)
	}

	name := entry.Name
	switch {
	case entry.Dir:
		return m.previewLines(m.dirSummary(pane, entry, width), width, rows)
	case pane.Side != SideLocal:
		return m.previewLines([]string{m.st.PaneEmpty.Render("Preview arrives with connections")}, width, rows)
	case IsArchive(name):
		return m.previewLines([]string{
			m.st.Accent.Render(m.icons.Archive + " " + truncate(name, width-2)),
			m.st.CardText.Render(humanSize(entry.Size)),
			"",
			m.st.PaneEmpty.Render("Archive contents arrive with"),
			m.st.PaneEmpty.Render("the archive engine (M4)."),
		}, width, rows)
	}

	path := filepath.Join(pane.Path, name)
	data, err := readHead(path, previewBytes)
	if err != nil {
		return m.previewLines([]string{
			m.st.Danger.Render("Cannot read the file"),
			m.st.PaneEmpty.Render(truncate(err.Error(), width)),
		}, width, rows)
	}
	header := m.st.CardMuted.Render(truncate(name, width) + "  " + humanSize(entry.Size))
	var body []string
	if isBinary(data) {
		body = hexDump(data, hexBytes)
	} else {
		body = textLines(data)
	}
	return m.previewLines(append([]string{header, ""}, body...), width, rows)
}

// dirSummary describes a directory without opening it.
func (m *Model) dirSummary(pane *Pane, entry Entry, width int) []string {
	path := filepath.Join(pane.Path, entry.Name)
	children, err := os.ReadDir(path)
	if err != nil {
		return []string{
			m.st.Accent.Render(m.icons.Folder + " " + truncate(entry.Name, width-2)),
			"",
			m.st.Danger.Render("Cannot read it"),
			m.st.PaneEmpty.Render(truncate(err.Error(), width)),
			"",
			m.st.PaneEmpty.Render("Right arrow to try anyway"),
		}
	}
	var files, dirs int
	var total int64
	for _, child := range children {
		if child.IsDir() {
			dirs++
			continue
		}
		files++
		if info, err := child.Info(); err == nil {
			total += info.Size()
		}
	}
	return []string{
		m.st.Accent.Render(m.icons.Folder + " " + truncate(entry.Name, width-2)),
		"",
		m.st.CardText.Render(fmt.Sprintf("%d %s", dirs, plural(dirs, "folder", "folders"))),
		m.st.CardText.Render(fmt.Sprintf("%d %s", files, plural(files, "file", "files"))),
		m.st.CardText.Render(humanSize(total)),
		"",
		m.st.PaneEmpty.Render("Right arrow to open"),
	}
}

// previewLines joins preview lines to exactly the available rows and width.
func (m *Model) previewLines(lines []string, width, rows int) string {
	out := make([]string, 0, rows)
	for _, line := range lines {
		if len(out) >= rows {
			break
		}
		out = append(out, lipgloss.NewStyle().Width(width).MaxWidth(width).Render(truncate(line, width)))
	}
	for len(out) < rows {
		out = append(out, lipgloss.NewStyle().Width(width).Render(""))
	}
	return strings.Join(out, "\n")
}

// readHead reads at most n bytes from a file.
func readHead(path string, n int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, n))
}

// isBinary reports whether data looks like a binary file, by the usual test: a
// NUL byte in the head.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0
}

// textLines splits text into displayable lines, expanding tabs and stripping
// carriage returns so a file written on Windows does not scroll sideways.
func textLines(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	text = strings.ReplaceAll(text, "\r", "")
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		out = append(out, stripControl(line))
	}
	// A trailing newline should not produce a phantom blank row.
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// stripControl removes control characters that would move the cursor.
func stripControl(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == 0x1b {
			continue
		}
		if r < 0x20 && r != 0x09 {
			b.WriteRune('·')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hexDump formats up to limit bytes as the offset/hex/ascii table everyone
// recognises from a hex editor.
func hexDump(data []byte, limit int) []string {
	if len(data) > limit {
		data = data[:limit]
	}
	lines := make([]string, 0, len(data)/16+1)
	for offset := 0; offset < len(data); offset += 16 {
		end := min(offset+16, len(data))
		chunk := data[offset:end]

		var hexPart strings.Builder
		var asciiPart strings.Builder
		for i := 0; i < 16; i++ {
			if i < len(chunk) {
				fmt.Fprintf(&hexPart, "%02x ", chunk[i])
				if chunk[i] >= 0x20 && chunk[i] < 0x7f {
					asciiPart.WriteByte(chunk[i])
				} else {
					asciiPart.WriteByte('.')
				}
			} else {
				hexPart.WriteString("   ")
			}
			if i == 7 {
				hexPart.WriteByte(' ')
			}
		}
		lines = append(lines, fmt.Sprintf("%08x  %s |%s|", offset, hexPart.String(), asciiPart.String()))
	}
	return lines
}
