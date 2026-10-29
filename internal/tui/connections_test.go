package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/keys"
)

const connFixture = `# my servers
Host prod
    HostName 203.0.113.10
    User deploy
    Port 2222

Host staging
    HostName staging.example.com
    User ubuntu
`

// isolateHome points HOME at a temporary directory.
//
// This is the guard that matters: an earlier version of these tests wrote to the
// real ~/.ssh/config because a configured path was not threaded through, and a
// test must never be able to touch the user's own files. With HOME redirected,
// even a path that ignores the option lands in a temp directory.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// newConnectionsModel opens the host manager over a fixture config.
func newConnectionsModel(t *testing.T, body string) (*Model, string) {
	t.Helper()
	home := isolateHome(t)
	dir := t.TempDir()
	// The fixture lives at $HOME/.ssh/config, which is both the realistic layout
	// and the one the display shortens to ~/.ssh/config.
	path := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UI.Start = "files"
	m, err := New(Options{
		Config: cfg, Profile: colorprofile.TrueColor, HasDark: true,
		Dir: dir, SSHConfigPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 140, 40)
	if m.sshPath != path {
		t.Fatalf("the configured SSH config path was ignored: sshPath is %q, want %q", m.sshPath, path)
	}
	m.runAction("session.open")
	return m, path
}

// The manager must read and write exactly the file it was pointed at, and never
// the real ~/.ssh/config. This is the regression test for the bug that let a test
// run modify the user's own SSH config.
func TestConnectionsUsesOnlyTheConfiguredPath(t *testing.T) {
	home := isolateHome(t)
	m, path := newConnectionsModel(t, connFixture)

	if m.conn.path != path {
		t.Fatalf("the manager is looking at %q, not %q", m.conn.path, path)
	}
	for _, entry := range m.conn.entries {
		if entry.Source != path {
			t.Errorf("a host came from %q, which is not the configured file", entry.Source)
		}
	}

	// Adding writes to the configured file and nowhere else. `a` asks what kind
	// of place; the first choice is an SSH host.
	press(t, m, run('a', 0, "a"))
	press(t, m, keyNamed(tea.KeyEnter))
	for _, ch := range "isolated" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyEnter))

	if !strings.Contains(readText(t, path), "Host isolated") {
		t.Error("the host was not written to the configured file")
	}
	// The temporary home is the only other place anything could have gone.
	if _, err := os.Stat(filepath.Join(home, ".ssh", "config")); err == nil {
		t.Error("a file was written under HOME even though a path was configured")
	}
}

// And when no path is configured, the default really is inside HOME, which is
// what makes the isolation above effective.
func TestDefaultConfigPathFollowsHome(t *testing.T) {
	home := isolateHome(t)
	cfg := config.Default()
	cfg.UI.Start = "files"
	m, err := New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: true, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 120, 30)
	m.loadConnections()
	want := filepath.Join(home, ".ssh", "config")
	if m.conn.path != want {
		t.Errorf("the default path is %q, want %q", m.conn.path, want)
	}
}

func TestConnectionsListsHostsFromSSHConfig(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	if m.screen != screenConnections {
		t.Fatal("Ctrl+O should open the connections screen")
	}
	if len(m.conn.entries) != 2 {
		t.Fatalf("expected two hosts, got %+v", m.conn.entries)
	}
	out := ansi.Strip(render(m))
	for _, want := range []string{"prod", "203.0.113.10", "deploy", "staging", "ubuntu", "~/.ssh/config"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list is missing %q:\n%s", want, out)
		}
	}
	// The detail panel shows the highlighted host.
	m.conn.cursor = 0
	if out := ansi.Strip(render(m)); !strings.Contains(out, "2222") {
		t.Errorf("the detail panel should show the port:\n%s", out)
	}
}

func TestConnectionsFilter(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	press(t, m, run('f', tea.ModCtrl, ""))
	if !m.conn.filtering {
		t.Fatal("Ctrl+F should start filtering")
	}
	for _, ch := range "stag" {
		press(t, m, run(ch, 0, string(ch)))
	}
	visible := m.visibleConnections()
	if len(visible) != 1 || visible[0].Alias != "staging" {
		t.Fatalf("filter should leave staging, got %+v", visible)
	}
	press(t, m, keyNamed(tea.KeyEsc))
	if m.conn.filtering {
		t.Error("Esc should stop filtering")
	}
}

// Adding a host writes it to the file and shows it immediately.
func TestConnectionsAddHost(t *testing.T) {
	m, path := newConnectionsModel(t, connFixture)
	press(t, m, run('a', 0, "a"))
	press(t, m, keyNamed(tea.KeyEnter)) // SSH host is the first choice
	if m.Overlay != OverlayForm {
		t.Fatal("a should open the form")
	}

	// Alias, host, user, port fields in order.
	press(t, m, run('d', 0, "d"))
	press(t, m, run('b', 0, "b"))
	press(t, m, keyNamed(tea.KeyTab))
	for _, ch := range "db.example.com" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyTab))
	for _, ch := range "postgres" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyTab))
	for _, ch := range "5432" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyEnter))

	if m.Overlay != OverlayNone {
		t.Errorf("saving should close the form, overlay is %v (err %q)", m.Overlay, m.form.err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Host db") || !strings.Contains(string(data), "HostName db.example.com") {
		t.Errorf("the host was not written:\n%s", data)
	}
	// The original comment and hosts are untouched.
	if !strings.Contains(string(data), "# my servers") || !strings.Contains(string(data), "Host staging") {
		t.Errorf("the existing file was damaged:\n%s", data)
	}
	found := false
	for _, e := range m.conn.entries {
		if e.Alias == "db" {
			found = true
		}
	}
	if !found {
		t.Error("the new host is not in the list")
	}
}

// The form must actually be drawn: a form that exists only in the model is a
// key that appears to do nothing.
func TestConnectionsFormIsRendered(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	press(t, m, run('a', 0, "a"))
	press(t, m, keyNamed(tea.KeyEnter))
	out := ansi.Strip(render(m))
	for _, want := range []string{"Add connection", "name", "host", "user", "port", "key", "jump", "Enter save"} {
		if !strings.Contains(out, want) {
			t.Errorf("the form is missing %q:\n%s", want, out)
		}
	}
	// Editing a host says which one.
	m.conn.cursor = 0
	press(t, m, keyNamed(tea.KeyEsc))
	press(t, m, run('e', 0, "e"))
	if out := ansi.Strip(render(m)); !strings.Contains(out, "Edit prod") {
		t.Errorf("the edit form should name the host:\n%s", out)
	}
}

// The form refuses nonsense and says why, without closing.
func TestConnectionsFormValidation(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	press(t, m, run('a', 0, "a"))
	press(t, m, keyNamed(tea.KeyEnter)) // choose SSH host
	// An empty name is rejected.
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayForm {
		t.Fatal("an invalid form should stay open")
	}
	if !strings.Contains(m.form.err, "name") {
		t.Errorf("the error should mention the name, got %q", m.form.err)
	}

	// A duplicate alias is rejected.
	for _, ch := range "prod" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if !strings.Contains(m.form.err, "already exists") {
		t.Errorf("a duplicate should be refused, got %q", m.form.err)
	}
}

func TestConnectionsEditHost(t *testing.T) {
	m, path := newConnectionsModel(t, connFixture)
	m.conn.cursor = 0 // prod
	press(t, m, run('e', 0, "e"))
	if m.Overlay != OverlayForm {
		t.Fatal("e should open the form")
	}
	// Move to the user field and replace it.
	press(t, m, keyNamed(tea.KeyTab))
	press(t, m, keyNamed(tea.KeyTab))
	press(t, m, run('u', tea.ModCtrl, ""))
	for _, ch := range "root" {
		press(t, m, run(ch, 0, string(ch)))
	}
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayNone {
		t.Fatalf("saving should close the form (err %q)", m.form.err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "User root") {
		t.Errorf("the user was not updated:\n%s", data)
	}
	if !strings.Contains(string(data), "# my servers") || !strings.Contains(string(data), "Host staging") {
		t.Errorf("the rest of the file was disturbed:\n%s", data)
	}
}

// Deleting asks first, and removing a host leaves the others alone.
func TestConnectionsDeleteAsksFirst(t *testing.T) {
	m, path := newConnectionsModel(t, connFixture)
	m.conn.cursor = 0
	press(t, m, run('d', 0, "d"))
	if m.Overlay != OverlayConfirm {
		t.Fatal("d should ask first")
	}
	if !strings.Contains(ansi.Strip(render(m)), "prod") {
		t.Error("the confirmation should name the host")
	}
	press(t, m, keyNamed(tea.KeyEsc))
	if !strings.Contains(readText(t, path), "Host prod") {
		t.Error("cancelling deleted the host")
	}

	press(t, m, run('d', 0, "d"))
	press(t, m, keyNamed(tea.KeyRight))
	press(t, m, keyNamed(tea.KeyEnter))
	text := readText(t, path)
	if strings.Contains(text, "Host prod") {
		t.Errorf("the host was not deleted:\n%s", text)
	}
	if !strings.Contains(text, "Host staging") || !strings.Contains(text, "# my servers") {
		t.Errorf("deleting damaged the file:\n%s", text)
	}
}

// Esc leaves the manager and returns where it was opened from.
func TestConnectionsEscReturns(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	press(t, m, keyNamed(tea.KeyEsc))
	if m.screen != screenHome {
		t.Errorf("Esc should return to the launcher, screen is %v", m.screen)
	}
}

// A missing config is an empty list with a way forward, not an error screen.
func TestConnectionsEmptyState(t *testing.T) {
	m, path := newConnectionsModel(t, "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m.loadConnections()
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "No hosts yet") {
		t.Errorf("an empty config should say so:\n%s", out)
	}
	if !strings.Contains(out, "a adds one") {
		t.Errorf("an empty config should say how to add one:\n%s", out)
	}
}

// A host that comes from an Include is listed but not edited here, because the
// write would land in the wrong file.
func TestConnectionsIncludeHostsAreReadOnly(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	dropin := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(dropin, 0o700); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "config")
	if err := os.WriteFile(main, []byte("Host local\n    HostName local.example.com\nInclude "+dropin+"/*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropin, "extra"), []byte("Host inherited\n    HostName inherited.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.UI.Start = "files"
	m, err := New(Options{Config: cfg, Profile: colorprofile.TrueColor, HasDark: true, Dir: dir, SSHConfigPath: main})
	if err != nil {
		t.Fatal(err)
	}
	resize(t, m, 140, 40)
	m.runAction("session.open")

	var inherited connEntry
	for _, e := range m.conn.entries {
		if e.Alias == "inherited" {
			inherited = e
		}
	}
	if inherited.Alias == "" {
		t.Fatalf("the included host should be listed: %+v", m.conn.entries)
	}
	if inherited.Editable {
		t.Error("a host from an include must not be editable from the main file")
	}
	if !strings.Contains(ansi.Strip(render(m)), "from include") {
		t.Error("the list should mark included hosts")
	}

	// Trying to edit it explains where it lives.
	for i, e := range m.visibleConnections() {
		if e.Alias == "inherited" {
			m.conn.cursor = i
		}
	}
	press(t, m, run('e', 0, "e"))
	if m.Overlay == OverlayForm {
		t.Error("editing an included host should be refused")
	}
	if len(m.toasts) == 0 || !strings.Contains(m.toasts[len(m.toasts)-1].text, "edit that file") {
		t.Errorf("the refusal should say what to do: %+v", m.toasts)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The key legend is the only place this screen explains itself, so it must not be
// the row that gets clipped when the layout is tight.
func TestConnectionsKeepsItsKeyLegend(t *testing.T) {
	for _, size := range []struct{ w, h int }{{140, 40}, {100, 24}, {90, 20}} {
		m, _ := newConnectionsModel(t, connFixture)
		resize(t, m, size.w, size.h)
		lines := strings.Split(ansi.Strip(render(m)), "\n")
		if len(lines) != size.h {
			t.Errorf("%dx%d: the screen should fill exactly, got %d lines", size.w, size.h, len(lines))
		}
		joined := strings.Join(lines, "\n")
		for _, want := range []string{"t terminal", "Enter", "Esc"} {
			if !strings.Contains(joined, want) {
				t.Errorf("%dx%d: the legend is missing %q:\n%s", size.w, size.h, want, joined)
			}
		}
	}
}

// An SSH connection with a password is saved in omssh's config, with the secret
// in the keyring — ~/.ssh/config is not touched. It can be edited and deleted
// without ssh.
func TestManagedPasswordConnection(t *testing.T) {
	m, sshPath := newConnectionsModel(t, connFixture)
	before, err := os.ReadFile(sshPath)
	if err != nil {
		t.Fatal(err)
	}
	store := keys.NewStore(keys.NewMemBackend(), "test")
	m.secrets = store
	m.configPath = t.TempDir() + "/config.yaml"

	m.conn.section = secSSH
	m.runAction("session.add")
	if m.Overlay != OverlayForm {
		t.Fatalf("a in SSH should open the host form, overlay=%v", m.Overlay)
	}
	found := false
	for _, field := range m.form.fields {
		if field.key == "Password" {
			found = true
		}
	}
	if !found {
		t.Fatal("the host form should offer a password field")
	}
	values := map[string]string{
		"Alias": "pw", "HostName": "192.0.2.10", "User": "deploy", "Port": "2222",
		"Password": "s3cret",
	}
	for i := range m.form.fields {
		if value, ok := values[m.form.fields[i].key]; ok {
			m.form.fields[i].in.Set(value)
		}
	}
	m.saveConnectionForm()

	host, ok := m.cfg.Hosts["pw"]
	if !ok {
		t.Fatalf("the managed host should be saved: %+v", m.cfg.Hosts)
	}
	if host.Host != "192.0.2.10" || host.User != "deploy" || host.Port != 2222 {
		t.Errorf("managed host = %+v", host)
	}
	secret, err := store.Password("pw")
	if err != nil || secret.Reveal() != "s3cret" {
		t.Errorf("keyring password = %q, %v", secret.Reveal(), err)
	}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Hosts["pw"]; !ok {
		t.Error("the managed host should be in the config file")
	}
	after, err := os.ReadFile(sshPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("a password connection must not edit ~/.ssh/config")
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "pw") {
		t.Errorf("the manager should list the managed host:\n%s", out)
	}

	// e opens it from omssh's config, with the password left blank.
	m.conn.section = secSSH
	for i, entry := range m.visibleConnections() {
		if entry.Alias == "pw" {
			m.conn.cursor = i
		}
	}
	m.runAction("session.edit")
	if m.Overlay != OverlayForm || !m.form.managed {
		t.Fatalf("e should edit the managed host, overlay=%v managed=%v", m.Overlay, m.form.managed)
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "stored in omssh config") {
		t.Errorf("the form should say where it lives:\n%s", out)
	}

	// x deletes it and forgets the secret.
	m.Overlay = OverlayNone
	for i, entry := range m.visibleConnections() {
		if entry.Alias == "pw" {
			m.conn.cursor = i
		}
	}
	m.applyDeleteConnection("pw")
	if _, ok := m.cfg.Hosts["pw"]; ok {
		t.Error("the managed host should be deleted")
	}
	if _, err := store.Password("pw"); err == nil {
		t.Error("the stored password should be forgotten")
	}
}
