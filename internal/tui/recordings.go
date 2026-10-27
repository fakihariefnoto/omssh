package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// Recordings are session logs with a timeline. They are files first: anything
// that writes asciinema v2 can be replayed here, and anything recorded here can be
// replayed by another tool. This screen is the list the launcher offers.

type recordingEntry struct {
	path string
	size int64
	when time.Time
}

type recordingsState struct{ cursor int }

// recordingsDir is where embedded sessions are written, beside the rest of the
// state rather than next to the config.
func recordingsDir() (string, error) {
	dir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "recordings"), nil
}

// recordingFiles lists .cast files, newest first.
func recordingFiles() []recordingEntry {
	dir, err := recordingsDir()
	if err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.cast"))
	entries := make([]recordingEntry, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		entries = append(entries, recordingEntry{path: path, size: info.Size(), when: info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].when.After(entries[j].when) })
	return entries
}

// openRecordings lists what can be replayed.
func (m *Model) openRecordings() {
	if len(recordingFiles()) == 0 {
		m.toastInfo("No recordings yet · set sessions.record: true and open U")
		return
	}
	m.recordings = recordingsState{}
	m.Overlay = OverlayRecordings
}

// handleRecordingsKey picks a recording, or leaves.
func (m *Model) handleRecordingsKey(msg tea.KeyPressMsg) {
	entries := recordingFiles()
	switch msg.String() {
	case "esc", "q":
		m.Overlay = OverlayNone
	case "up", "k":
		m.recordings.cursor = max(0, m.recordings.cursor-1)
	case "down", "j":
		m.recordings.cursor = min(max(0, len(entries)-1), m.recordings.cursor+1)
	case "enter":
		if m.recordings.cursor >= 0 && m.recordings.cursor < len(entries) {
			m.openReplay(entries[m.recordings.cursor].path)
		}
	}
}

// renderRecordings draws the card.
func (m *Model) renderRecordings(l layout) string {
	entries := recordingFiles()
	width := cardWidth(l, 56, 96)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Recordings"),
		m.st.CardMuted.Render(truncate(fmt.Sprintf("%d · asciinema .cast · Enter replays", len(entries)), content)),
		"",
	}
	limit := max(1, l.Height-10)
	for i, entry := range entries {
		if i >= limit {
			lines = append(lines, m.st.PaneEmpty.Render(fmt.Sprintf("… %d more", len(entries)-limit)))
			break
		}
		name := strings.TrimSuffix(filepath.Base(entry.path), ".cast")
		right := fmt.Sprintf("%s · %s", humanSize(entry.size), entry.when.Format("2006-01-02 15:04"))
		row := spread(m.st.CardText.Render(truncate(name, content/2)), m.st.CardMuted.Render(right), content)
		if i == m.recordings.cursor {
			row = m.st.Accent.Bold(true).Render(truncate("▸ "+name, content/2)) +
				spaces(max(1, content-content/2-len(right))) + m.st.CardMuted.Render(right)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", m.st.CardMuted.Render("↑↓ choose · Enter replay · Esc close"))
	return m.card(width, lines)
}

// pruneRecordings applies the configured age and size limits before a new
// recording starts, so a folder cannot grow without bound.
func pruneRecordings(keepDays, maxMB int) {
	entries := recordingFiles() // newest first
	if len(entries) == 0 {
		return
	}
	if maxMB <= 0 {
		maxMB = 512
	}
	if keepDays > 0 {
		cutoff := time.Now().Add(-time.Duration(keepDays) * 24 * time.Hour)
		kept := entries[:0]
		for _, entry := range entries {
			if entry.when.Before(cutoff) {
				_ = os.Remove(entry.path)
				continue
			}
			kept = append(kept, entry)
		}
		entries = kept
	}
	var total int64
	for _, entry := range entries {
		total += entry.size
	}
	limit := int64(maxMB) << 20
	for i := len(entries) - 1; i >= 0 && total > limit; i-- {
		if err := os.Remove(entries[i].path); err == nil {
			total -= entries[i].size
		}
	}
}
