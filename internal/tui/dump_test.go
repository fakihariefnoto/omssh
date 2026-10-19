package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// TestDumpRender is a development aid: it writes rendered screens to
// $OMSSH_DUMP so a human can look at the interface without a terminal. It does
// nothing unless the variable is set.
func TestDumpRender(t *testing.T) {
	out := os.Getenv("OMSSH_DUMP")
	if out == "" {
		t.Skip("set OMSSH_DUMP=<dir> to write renders")
	}

	write := func(name string, m *Model, width, height int) {
		t.Helper()
		resize(t, m, width, height)
		if err := os.WriteFile(filepath.Join(out, name+".txt"), []byte(m.render()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Wide: the layout from UX.md §2, with a selection so the action bar has
	// something to say.
	wide := dumpModel(t)
	wide.selectByName("README.md")
	fire(t, wide, "space")
	write("wide", wide, 150, 38)

	// Narrow: one pane, compact columns, action bar collapsed.
	write("narrow", dumpModel(t), 76, 22)

	// The three discovery surfaces.
	menu := dumpModel(t)
	menu.selectByName("README.md")
	fire(t, menu, "space")
	fire(t, menu, "f9")
	write("menu", menu, 120, 34)

	palette := dumpModel(t)
	fire(t, palette, "ctrl+k")
	for _, ch := range "extr" {
		fire(t, palette, string(ch))
	}
	write("palette", palette, 120, 34)

	help := dumpModel(t)
	fire(t, help, "f1")
	write("help", help, 120, 38)

	// The delete confirmation, which carries the whole safety burden.
	confirm := dumpModel(t)
	fire(t, confirm, "ctrl+a")
	fire(t, confirm, "f8")
	write("confirm", confirm, 130, 34)

	// First run, with a preview of a text file.
	onboard := dumpModel(t)
	onboard.cfg.UI.Onboarding = false
	onboard.Overlay = OverlayOnboarding
	write("onboarding", onboard, 130, 36)

	preview := dumpModel(t)
	preview.selectByName("notes.txt")
	fire(t, preview, "f3")
	write("preview", preview, 170, 30)

	// The connections screen and its form.
	conn := dumpModel(t)
	conn.sshPath = filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(conn.sshPath, []byte("Host prod-api\n    HostName 203.0.113.10\n    User deploy\n    Port 2222\n    IdentityFile ~/.ssh/id_ed25519\n\nHost staging\n    HostName staging.example.com\n    User ubuntu\n    ProxyJump prod-api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fire(t, conn, "ctrl+o")
	write("connections", conn, 140, 34)
	fire(t, conn, "a")
	write("connections-form", conn, 140, 34)

	// The front door, at the sizes where its layout has to change.
	for _, size := range []struct {
		name          string
		width, height int
		keys          []string
	}{
		{"home-wide", 140, 40, nil},
		{"home-medium", 100, 30, nil},
		{"home-narrow", 60, 20, nil},
		{"home-selection", 120, 36, []string{"down", "down"}},
	} {
		m := dumpModel(t)
		m.screen = screenHome
		for _, k := range size.keys {
			fire(t, m, k)
		}
		write(size.name, m, size.width, size.height)
	}
}

// dumpModel builds an interface over a fixture directory with a known shape.
func dumpModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"README.md":       "# omssh\n\nA modern SSH/SFTP client for the terminal.\n\n- dual-pane file manager\n- archive extract, both directions\n- deploy profiles\n",
		"notes.txt":       "one\ntwo\nthree\n",
		"go.mod":          "module example.com/app\n\ngo 1.26\n",
		"release.tar.gz":  "not really a tarball",
		"assets/logo.svg": "<svg/>\n",
		"assets/icon.png": "binary-ish",
		".env":            "SECRET=redacted\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.UI.Theme = "midnight"
	cfg.UI.Icons = "nerd"
	cfg.UI.Onboarding = true

	m, err := New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: true, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 120, 34)
	return m
}

// fire sends a key described as text, the way a person would name it.
func fire(t *testing.T, m *Model, key string) {
	t.Helper()
	_, _ = m.Update(dumpKey(key))
}

func dumpKey(key string) tea.KeyPressMsg {
	named := map[string]rune{
		"space": tea.KeySpace, "enter": tea.KeyEnter, "esc": tea.KeyEsc,
		"f1": tea.KeyF1, "f8": tea.KeyF8, "f9": tea.KeyF9, "up": tea.KeyUp, "down": tea.KeyDown,
	}
	for name, code := range named {
		if key == name {
			return tea.KeyPressMsg{Code: code}
		}
	}
	if len(key) == 1 {
		return tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	}
	// Any ctrl+<letter>, so dumps can drive the whole interface.
	if letter, ok := strings.CutPrefix(key, "ctrl+"); ok && len(letter) == 1 {
		return tea.KeyPressMsg{Code: rune(letter[0]), Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{}
}
