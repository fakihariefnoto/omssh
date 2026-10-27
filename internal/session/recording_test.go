package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A recording is a header and timestamped events, and reading it back gives the
// same session.
func TestRecorderRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recordings", "host.cast")
	rec, err := NewRecorder(path, 120, 40, "omssh · test")
	if err != nil {
		t.Fatal(err)
	}
	rec.Output([]byte("hello"))
	time.Sleep(5 * time.Millisecond)
	rec.Input([]byte("ls\r"))
	rec.Output([]byte("\r\nworld"))
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("recording mode = %o, want 600", mode)
	}

	loaded, err := LoadRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.Version != 2 || loaded.Header.Width != 120 || loaded.Header.Height != 40 {
		t.Errorf("header = %+v", loaded.Header)
	}
	if loaded.Header.Title != "omssh · test" {
		t.Errorf("title = %q", loaded.Header.Title)
	}
	if len(loaded.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(loaded.Events))
	}
	if loaded.Events[0].Type != "o" || loaded.Events[0].Data != "hello" {
		t.Errorf("first event = %+v", loaded.Events[0])
	}
	if loaded.Events[1].Type != "i" || loaded.Events[1].Data != "ls\r" {
		t.Errorf("input event = %+v", loaded.Events[1])
	}
	if loaded.Duration() <= 0 {
		t.Errorf("duration = %v", loaded.Duration())
	}

	// Input is part of the recording, and a search can seek to it.
	if index := loaded.Find("ls", 0); index != 1 {
		t.Errorf("Find(ls) = %d, want the input event", index)
	}
	if index := loaded.Find("world", loaded.Duration()+time.Second); index != -1 {
		t.Errorf("a search past the end = %d, want -1", index)
	}
}

// A torn line from a killed recorder is ignored rather than failing the file.
func TestLoadRecordingSkipsTornLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "torn.cast")
	body := strings.Join([]string{
		`{"version":2,"width":80,"height":24,"timestamp":1}`,
		`[0.1,"o","ok"]`,
		`[0.2,"o"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Events) != 1 {
		t.Errorf("events = %d, want the one complete line", len(rec.Events))
	}
}

func TestLoadRecordingRejectsBadHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.cast")
	if err := os.WriteFile(path, []byte("{\"version\":1,\"width\":80,\"height\":24}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecording(path); err == nil {
		t.Error("version 1 should be refused")
	}
}
