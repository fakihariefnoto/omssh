package tui

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// Recordings are session logs with a timeline. They are files first: anything
// that writes asciinema v2 can be replayed here, and anything recorded here can be
// replayed by another tool. History's Terminal tab is where they are listed.

type recordingEntry struct {
	path string
	size int64
	when time.Time
}

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
