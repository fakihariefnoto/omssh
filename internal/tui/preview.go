package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/fakihariefnoto/omssh/internal/archive"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// previewBytes bounds how much of a file the preview reads, so opening a huge
// log does not stall the interface.
const previewBytes = 64 << 10

// hexBytes is how much of a binary the hex view shows.
const hexBytes = 256

// renderPreview draws the right half of the focused pane: the contents of the
// entry under the cursor, or an explanation of why it cannot be shown.
//
// It never reads anything. Rendering happens on every keystroke and every
// heartbeat, and on a remote pane a read is a network round trip: doing it here
// made every cursor move wait for the server. The body is fetched by a command
// (previewCmd) and cached; render only ever hands back what is already known.
func (m *Model) renderPreview(width, rows int) string {
	if width <= 0 || rows <= 0 {
		return ""
	}
	if m.previewBody == "" {
		message := "…"
		if _, ok := m.Pane().CursorEntry(); !ok {
			message = "Nothing under the cursor"
		}
		return m.previewLines([]string{m.st.PaneEmpty.Render(message)}, width, rows)
	}
	return m.previewBody
}

// previewKeyFor names everything the preview's answer depends on. A remote
// preview is allowed to be two seconds stale, which is cheaper than a read per
// frame and invisible in practice; refresh() bumps previewGen, so a listing
// change is never stale.
func (m *Model) previewKeyFor(width, rows int) (string, *Pane, Entry, bool) {
	pane := m.Pane()
	entry, ok := pane.CursorEntry()
	if !ok {
		return "", pane, Entry{}, false
	}
	bucket := int64(0)
	if pane.Side == SideRemote {
		bucket = time.Now().Unix() / 2
	}
	key := fmt.Sprintf("%d|%d|%s|%s|%d|%d|%d|%s|%d|%t",
		m.previewGen, m.Focused, pane.Path, entry.Name, entry.Size,
		width, rows, m.themeName, bucket, entry.Dir)
	return key, pane, entry, true
}

// previewCmd fetches the focused entry's preview off the update loop. It returns
// nil when what is cached is already what the cursor needs.
func (m *Model) previewCmd() tea.Cmd {
	if !m.preview {
		return nil
	}
	width, rows := m.measure().PreviewWidth, m.measure().PaneRows
	if width <= 0 || rows <= 0 {
		return nil
	}
	key, pane, entry, ok := m.previewKeyFor(width, rows)
	if !ok || key == m.previewKey {
		return nil
	}
	index := m.Focused
	return func() tea.Msg {
		body := m.renderPreviewNow(pane, entry, width, rows)
		return previewReadyMsg{key: key, index: index, body: body}
	}
}

// previewReadyMsg carries a fetched preview back to the interface.
type previewReadyMsg struct {
	key   string
	index int
	body  string
}

// applyPreview stores a fetched body, unless the cursor moved on while it was in
// flight.
func (m *Model) applyPreview(msg previewReadyMsg) {
	width, rows := m.measure().PreviewWidth, m.measure().PaneRows
	key, _, _, ok := m.previewKeyFor(width, rows)
	if !ok || key != msg.key {
		// Stale: the cursor is somewhere else now. Ask again for the new place.
		return
	}
	m.previewKey, m.previewBody = msg.key, msg.body
}

// renderPreviewNow does the reading that renderPreview caches.
func (m *Model) renderPreviewNow(pane *Pane, entry Entry, width, rows int) string {
	name := entry.Name
	switch {
	case entry.Dir:
		return m.previewLines(m.dirSummary(pane, entry, width), width, rows)
	case pane.FS == nil:
		return m.previewLines([]string{m.st.PaneEmpty.Render("This pane is not connected")}, width, rows)
	case IsArchive(name):
		return m.previewLines(m.archivePreview(pane, entry, width), width, rows)
	}

	target := pane.FS.Join(pane.Path, name)
	data, err := readHead(pane.FS, target, previewBytes)
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
	// A symlink says where it points, which is the one thing its contents cannot
	// tell you.
	if target := m.symlinkTarget(pane, entry); target != "" {
		body = append([]string{m.st.PaneSymlink.Render(truncate(target, width)), ""}, body...)
	}
	return m.previewLines(append([]string{header, ""}, body...), width, rows)
}

// archivePreview lists what an archive holds. Listings happen on the update
// path, so a remote archive is not opened here: one SFTP round trip per redraw
// would make the cursor stutter. It says how to unpack it instead.
func (m *Model) archivePreview(pane *Pane, entry Entry, width int) []string {
	header := m.st.Accent.Render(m.icons.Archive + " " + truncate(entry.Name, width-2))
	if pane.Side != SideLocal {
		return []string{
			header,
			m.st.CardText.Render(humanSize(entry.Size)),
			"",
			m.st.PaneEmpty.Render("Ctrl+E unpacks it where you choose"),
		}
	}
	entries, err := archive.List(pane.FS, pane.FS.Join(pane.Path, entry.Name))
	if err != nil {
		return []string{header, "", m.st.Danger.Render(truncate(err.Error(), width))}
	}
	lines := []string{header, m.st.CardMuted.Render(fmt.Sprintf("%d %s · %s",
		len(entries), plural(len(entries), "entry", "entries"), humanSize(entry.Size))), ""}
	limit := 40
	for i, member := range entries {
		if i >= limit {
			lines = append(lines, m.st.PaneEmpty.Render(fmt.Sprintf("… %d more", len(entries)-limit)))
			break
		}
		mark := "·"
		if member.Dir {
			mark = "▸"
		}
		lines = append(lines, m.st.CardText.Render(truncate(mark+" "+member.Name, width)))
	}
	lines = append(lines, "", m.st.PaneEmpty.Render("Ctrl+E to unpack"))
	return lines
}

// dirSummary describes a directory without opening it.
func (m *Model) dirSummary(pane *Pane, entry Entry, width int) []string {
	if pane.FS == nil {
		return []string{m.st.PaneEmpty.Render("This pane is not connected")}
	}
	children, err := pane.FS.ReadDir(pane.FS.Join(pane.Path, entry.Name))
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

// readHead reads at most n bytes from a file, over whichever filesystem it lives
// on.
func readHead(fsys vfs.FS, name string, n int64) ([]byte, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, n))
}

// symlinkTarget resolves a symlink for the detail line. It is a round trip, so
// it happens for the entry under the cursor rather than for every row.
func (m *Model) symlinkTarget(pane *Pane, entry Entry) string {
	if pane.FS == nil || !entry.Symlink {
		return ""
	}
	target, err := pane.FS.Readlink(pane.FS.Join(pane.Path, entry.Name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "(broken)"
		}
		return ""
	}
	if !path.IsAbs(target) {
		return "→ " + target
	}
	return "→ " + target
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
