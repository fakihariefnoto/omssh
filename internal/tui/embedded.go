package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
)

// An embedded session is the same SSH connection as a pane, with a PTY opened on
// it and a terminal emulator drawing the answers. It exists so a session can be
// recorded and replayed, which handing the terminal to ssh can never offer.

// embeddedState is one live shell and the screen it draws on.
type embeddedState struct {
	host    string
	emu     *vt.Emulator
	shell   *session.Shell
	rec     *session.Recorder
	recPath string
	width   int
	height  int
	// mu guards the emulator: two remote streams write it while the interface
	// reads it every frame.
	mu sync.Mutex
}

// embeddedExitMsg says the remote shell ended.
type embeddedExitMsg struct {
	host string
	err  error
}

// screenSink feeds the emulator and, when recording, the .cast file. Two remote
// streams can write at once, so the lock is what keeps a recording parseable.
type screenSink struct{ state *embeddedState }

func (s *screenSink) Write(p []byte) (int, error) {
	state := s.state
	state.mu.Lock()
	defer state.mu.Unlock()
	_, _ = state.emu.Write(p)
	if state.rec != nil {
		state.rec.Output(p)
	}
	return len(p), nil
}

// openEmbedded opens a shell on the focused pane's host.
func (m *Model) openEmbedded() tea.Cmd {
	host := m.Pane().Host
	if host == "" && m.activeTab >= 0 && m.activeTab < len(m.tabs) {
		host = m.tabs[m.activeTab].Host
	}
	if strings.TrimSpace(host) == "" {
		m.toastInfo("Open a host first · U opens its embedded terminal")
		return nil
	}
	if isCloudHost(host) {
		m.toastInfo("Object storage has no shell")
		return nil
	}
	live := m.live[host]
	if live == nil {
		m.toastInfo("Connect to " + host + " first · t opens the external terminal")
		return nil
	}
	cols, rows := m.width, m.height-1
	if cols < 20 || rows < 5 {
		cols, rows = max(20, cols), max(5, rows)
	}

	emu := vt.NewEmulator(cols, rows)
	state := &embeddedState{host: host, emu: emu, width: cols, height: rows}
	sink := &screenSink{state: state}

	if m.cfg.Sessions.Record {
		pruneRecordings(m.cfg.Sessions.KeepDays, m.cfg.Sessions.MaxMB)
		if dir, err := config.StateDir(); err == nil {
			path := filepath.Join(dir, "recordings",
				fmt.Sprintf("%s-%s.cast", safeName(host), time.Now().Format("20060102-150405")))
			rec, err := session.NewRecorder(path, cols, rows, "omssh · "+host)
			if err != nil {
				m.toastWarning("Not recording: " + err.Error())
			} else {
				state.rec, state.recPath = rec, path
			}
		}
	}

	shell, err := live.OpenShell(cols, rows, sink)
	if err != nil {
		if state.rec != nil {
			_ = state.rec.Close()
		}
		m.toastError("Shell: " + err.Error())
		return nil
	}
	state.shell = shell
	m.embedded = state
	m.Overlay = OverlayTerminal
	// The wait blocks until the shell exits; it is one command, not the update
	// loop, and its message is what brings the interface back.
	return func() tea.Msg {
		return embeddedExitMsg{host: host, err: shell.Wait()}
	}
}

// safeName keeps a host alias usable in a filename.
func safeName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, name)
}

// endEmbedded detaches and stops recording, leaving the SSH connection itself
// alone: panes on the same host keep working.
func (m *Model) endEmbedded() {
	state := m.embedded
	m.embedded = nil
	if m.Overlay == OverlayTerminal {
		m.Overlay = OverlayNone
	}
	if state == nil {
		return
	}
	if state.shell != nil {
		_ = state.shell.Close()
	}
	if state.rec != nil {
		_ = state.rec.Close()
	}
}

// handleEmbeddedKey forwards keys to the remote shell. Ctrl+] detaches first: the
// interface's own keys must be reachable from inside a session.
func (m *Model) handleEmbeddedKey(msg tea.KeyPressMsg) {
	if msg.String() == "ctrl+]" {
		m.endEmbedded()
		return
	}
	state := m.embedded
	if state == nil {
		return
	}
	data := keyBytes(msg)
	if len(data) == 0 {
		return
	}
	if _, err := state.shell.Write(data); err != nil {
		m.toastError("Shell: " + err.Error())
		return
	}
	if state.rec != nil && m.cfg.Sessions.RecordInput {
		state.rec.Input(data)
	}
}

// keyBytes turns one key press into the bytes a terminal would send.
func keyBytes(msg tea.KeyPressMsg) []byte {
	if msg.Mod.Contains(tea.ModCtrl) {
		if msg.Code >= 'a' && msg.Code <= 'z' {
			return []byte{byte(msg.Code-'a') + 1}
		}
		if msg.Code == tea.KeySpace {
			return []byte{0}
		}
	}
	if msg.Mod.Contains(tea.ModAlt) && msg.Text != "" {
		return append([]byte{0x1b}, msg.Text...)
	}
	switch msg.Code {
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeySpace:
		return []byte(" ")
	}
	if msg.Text != "" {
		return []byte(msg.Text)
	}
	return nil
}

// renderEmbedded draws the shell's screen and a status line.
func (m *Model) renderEmbedded(l layout) string {
	state := m.embedded
	if state == nil {
		return ""
	}
	height := max(1, l.Height-1)
	state.mu.Lock()
	screen := state.emu.Render()
	state.mu.Unlock()
	lines := strings.Split(screen, "\n")
	out := make([]string, 0, l.Height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, padVisible(ansi.Truncate(line, l.Width, ""), l.Width))
	}
	status := fmt.Sprintf("%s · embedded · Ctrl+] detach", state.host)
	if state.recPath != "" {
		status += " · recording " + filepath.Base(state.recPath)
	}
	out = append(out, m.st.StatusMuted.Render(padVisible(truncate(status, l.Width), l.Width)))
	return strings.Join(out, "\n")
}

// resizeEmbedded follows the terminal: the remote PTY and the screen both move.
func (m *Model) resizeEmbedded(width, height int) {
	state := m.embedded
	if state == nil {
		return
	}
	rows := max(1, height-1)
	cols := max(1, width)
	if cols == state.width && rows == state.height {
		return
	}
	state.width, state.height = cols, rows
	state.mu.Lock()
	state.emu.Resize(cols, rows)
	state.mu.Unlock()
	if state.shell != nil {
		_ = state.shell.Resize(cols, rows)
	}
}

// ---- replay ---------------------------------------------------------------

// replayState is a .cast file being played into a fresh emulator.
type replayState struct {
	rec     *session.Recording
	emu     *vt.Emulator
	at      time.Duration
	index   int
	gen     int
	speed   float64
	playing bool
}

// replayTickMsg advances a playing recording.
type replayTickMsg struct{ gen int }

// replaySeq numbers players, so a tick from a closed recording cannot advance the
// next one.
var replaySeq int

const replayFrame = 50 * time.Millisecond

// openReplayPrompt asks which recording to play, defaulting to the newest.
func (m *Model) openReplayPrompt() {
	dir, err := config.StateDir()
	if err != nil {
		m.toastError(err.Error())
		return
	}
	latest := ""
	matches, _ := filepath.Glob(filepath.Join(dir, "recordings", "*.cast"))
	if len(matches) > 0 {
		latest = matches[len(matches)-1]
	}
	if latest == "" {
		m.toastInfo("No recordings yet · set sessions.record: true and open U")
		return
	}
	m.prompt = promptState{
		Title:  "Replay a session",
		Hint:   "Path to a .cast file · Enter plays it",
		Action: "prompt.replay",
		open:   true,
	}
	m.prompt.input.Set(latest)
	m.Overlay = OverlayPrompt
}

// openReplay loads a recording and shows the player, paused at the start.
func (m *Model) openReplay(path string) {
	rec, err := session.LoadRecording(strings.TrimSpace(path))
	if err != nil {
		m.toastError(err.Error())
		return
	}
	replaySeq++
	state := &replayState{rec: rec, speed: 1, gen: replaySeq}
	state.seek(0)
	m.replay = state
	m.Overlay = OverlayReplay
}

// seek rebuilds the screen at a time. Replaying from the beginning is the only
// honest way to honour every escape sequence, and a recording is small.
func (r *replayState) seek(at time.Duration) {
	if at < 0 {
		at = 0
	}
	if end := r.rec.Duration(); at > end {
		at = end
	}
	r.at = at
	r.index = 0
	r.emu = vt.NewEmulator(r.rec.Header.Width, r.rec.Header.Height)
	for r.index < len(r.rec.Events) && r.eventTime(r.index) <= at {
		r.apply(r.index)
		r.index++
	}
}

func (r *replayState) eventTime(i int) time.Duration {
	return time.Duration(r.rec.Events[i].Time * float64(time.Second))
}

func (r *replayState) apply(i int) {
	if r.rec.Events[i].Type == "o" {
		_, _ = r.emu.WriteString(r.rec.Events[i].Data)
	}
}

// advance feeds the screen up to the new time.
func (r *replayState) advance(at time.Duration) {
	if at > r.rec.Duration() {
		at = r.rec.Duration()
	}
	r.at = at
	for r.index < len(r.rec.Events) && r.eventTime(r.index) <= at {
		r.apply(r.index)
		r.index++
	}
}

// handleReplayKey is the player: space plays, arrows seek, +/- changes speed.
func (m *Model) handleReplayKey(msg tea.KeyPressMsg) tea.Cmd {
	state := m.replay
	if state == nil {
		return nil
	}
	switch msg.String() {
	case "esc", "q":
		m.replay = nil
		if m.Overlay == OverlayReplay {
			m.Overlay = OverlayNone
		}
		return nil
	case " ", "space", "enter":
		state.playing = !state.playing
		if state.playing {
			if state.at >= state.rec.Duration() {
				state.seek(0)
			}
			return replayTick(state.gen)
		}
	case "left":
		state.playing = false
		state.seek(state.at - 5*time.Second)
	case "right":
		state.playing = false
		state.seek(state.at + 5*time.Second)
	case "0":
		state.seek(0)
	case "+", "=":
		state.speed = min(8, state.speed*2)
	case "-":
		state.speed = max(0.25, state.speed/2)
	case "/":
		m.prompt = promptState{Title: "Find in recording", Hint: "Text to seek to from here", Action: "prompt.replay-search", open: true}
		m.Overlay = OverlayPrompt
	}
	return nil
}

// replayTick schedules the next frame of a playing recording.
func replayTick(gen int) tea.Cmd {
	return tea.Tick(replayFrame, func(time.Time) tea.Msg { return replayTickMsg{gen: gen} })
}

// advanceReplay moves the player and asks for the next frame.
func (m *Model) advanceReplay(msg replayTickMsg) tea.Cmd {
	state := m.replay
	if state == nil || !state.playing || msg.gen != state.gen {
		return nil
	}
	state.advance(state.at + time.Duration(float64(replayFrame)*state.speed))
	if state.at >= state.rec.Duration() {
		state.playing = false
		return nil
	}
	return replayTick(state.gen)
}

// searchReplay seeks to the first later event containing the query.
func (m *Model) searchReplay(query string) {
	state := m.replay
	if state == nil {
		return
	}
	at := state.at
	if at > 0 {
		at += time.Millisecond // start after the current frame
	}
	index := state.rec.Find(query, at)
	if index < 0 {
		m.toastInfo("Not found after " + state.at.Round(time.Second).String())
		return
	}
	state.playing = false
	state.seek(state.eventTime(index))
}

// renderReplay draws the recorded screen and the player's status.
func (m *Model) renderReplay(l layout) string {
	state := m.replay
	if state == nil {
		return ""
	}
	height := max(1, l.Height-1)
	lines := strings.Split(state.emu.Render(), "\n")
	out := make([]string, 0, l.Height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, padVisible(ansi.Truncate(line, l.Width, ""), l.Width))
	}
	status := fmt.Sprintf("replay %s / %s · %.2fx · %s · space play · ←/→ seek · +/- speed · / find · q close",
		state.at.Round(100*time.Millisecond), state.rec.Duration().Round(100*time.Millisecond),
		state.speed, map[bool]string{true: "playing", false: "paused"}[state.playing])
	out = append(out, m.st.StatusMuted.Render(padVisible(truncate(status, l.Width), l.Width)))
	return strings.Join(out, "\n")
}
