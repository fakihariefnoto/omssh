package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Recordings use asciinema's v2 format: one JSON header line, then one JSON array
// per event, [seconds, "o"|"i", data]. It is plain text, plays in `asciinema
// play`, and a recording nobody can replay would be a log rather than a session.

// RecordingHeader is the first line of a .cast file.
type RecordingHeader struct {
	Version   int    `json:"version"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Timestamp int64  `json:"timestamp"`
	Title     string `json:"title,omitempty"`
}

// RecordingEvent is one output or input chunk.
type RecordingEvent struct {
	Time float64
	Type string
	Data string
}

// Recorder writes a live session to a .cast file as it happens.
type Recorder struct {
	mu    sync.Mutex
	file  *os.File
	start time.Time
	err   error
}

// NewRecorder creates the file, mode 0600, and writes the header.
func NewRecorder(path string, width, height int, title string) (*Recorder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	header := RecordingHeader{Version: 2, Width: width, Height: height,
		Timestamp: time.Now().Unix(), Title: title}
	line, err := json.Marshal(header)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Recorder{file: file, start: time.Now()}, nil
}

// Output records bytes the server sent.
func (r *Recorder) Output(p []byte) { r.event("o", p) }

// Input records bytes the person typed, which is what makes a recording tell the
// whole story rather than half of it.
func (r *Recorder) Input(p []byte) { r.event("i", p) }

func (r *Recorder) event(kind string, p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil || r.err != nil {
		return
	}
	at := time.Since(r.start).Seconds()
	line, err := json.Marshal([]any{at, kind, string(p)})
	if err != nil {
		r.err = err
		return
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		r.err = err
	}
}

// Close flushes and closes the file. It is safe to call twice.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return r.err
	}
	err := r.file.Close()
	r.file = nil
	if r.err != nil {
		return r.err
	}
	return err
}

// Recording is a parsed .cast file.
type Recording struct {
	Path   string
	Header RecordingHeader
	Events []RecordingEvent
}

// LoadRecording reads and validates a .cast file.
func LoadRecording(path string) (*Recording, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	if !scanner.Scan() {
		return nil, errors.New("recording: the file is empty")
	}
	rec := &Recording{Path: path}
	if err := json.Unmarshal(scanner.Bytes(), &rec.Header); err != nil {
		return nil, fmt.Errorf("recording: header: %w", err)
	}
	if rec.Header.Version != 2 {
		return nil, fmt.Errorf("recording: version %d, want 2", rec.Header.Version)
	}
	if rec.Header.Width < 1 || rec.Header.Height < 1 {
		return nil, errors.New("recording: the header has no size")
	}
	for scanner.Scan() {
		var raw []json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue // a torn final line from a killed recorder
		}
		if len(raw) != 3 {
			continue
		}
		var at float64
		var kind, data string
		if json.Unmarshal(raw[0], &at) != nil || json.Unmarshal(raw[1], &kind) != nil ||
			json.Unmarshal(raw[2], &data) != nil {
			continue
		}
		rec.Events = append(rec.Events, RecordingEvent{Time: at, Type: kind, Data: data})
	}
	return rec, scanner.Err()
}

// Duration is the time of the last event.
func (r *Recording) Duration() time.Duration {
	if len(r.Events) == 0 {
		return 0
	}
	return time.Duration(r.Events[len(r.Events)-1].Time * float64(time.Second))
}

// Find returns the first event at or after at that contains the query, so a
// search can seek instead of playing through.
func (r *Recording) Find(query string, at time.Duration) int {
	for i, event := range r.Events {
		if time.Duration(event.Time*float64(time.Second)) < at {
			continue
		}
		if contains(event.Data, query) {
			return i
		}
	}
	return -1
}

// contains is strings.Contains, kept here so the scan is obvious at the call
// site and does not pull the package in for one call.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
