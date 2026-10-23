package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// A bucket appears in the same list as the hosts, and opening it binds a pane.
func TestCloudProfileOpensAPane(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{
		"backups": {Provider: "s3", Bucket: "my-backups", Region: "eu-central-1", Prefix: "omssh"},
	}
	m.loadConnections()

	out := ansi.Strip(render(m))
	if !strings.Contains(out, "backups") || !strings.Contains(out, "s3://my-backups/omssh") {
		t.Fatalf("the list should show the bucket:\n%s", out)
	}

	m.selectConnection("backups")
	cmd := m.connectToHighlighted()
	if cmd == nil {
		t.Fatalf("Enter on a bucket should open it: %v", m.toasts)
	}
	msg, ok := cmd().(cloudReadyMsg)
	if !ok {
		t.Fatalf("expected a cloudReadyMsg, got %T", msg)
	}
	if msg.err != nil {
		t.Fatalf("opening the bucket client should not need the network: %v", msg.err)
	}
	m.handleCloudReady(msg)
	if m.paneIndexFor("cloud:backups") < 0 {
		t.Error("the bucket should have a pane")
	}
	if m.screen != screenFiles {
		t.Error("opening a bucket should go to the dashboard")
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "backups") {
		t.Errorf("the pane should be named after the profile:\n%s", out)
	}
}

// A bucket can be added from the interface, not only by editing YAML.
func TestAddBucketForm(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.configPath = t.TempDir() + "/config.yaml"

	// a opens the kind chooser; the third entry is an R2 bucket.
	m.runAction("session.add")
	if m.Overlay != OverlayAdd {
		t.Fatalf("a should ask what to add, overlay=%v", m.Overlay)
	}
	m.add.cursor = 2
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayForm || m.form.kind != "cloud" {
		t.Fatalf("choosing R2 should open the bucket form, overlay=%v kind=%q", m.Overlay, m.form.kind)
	}
	if got := m.form.fields[1].in.Value; got != "r2" {
		t.Errorf("the type should be prefilled with r2, got %q", got)
	}
	values := map[string]string{
		"Name": "photos", "Provider": "r2", "Bucket": "my-photos",
		"Region": "", "Endpoint": "https://acct.r2.cloudflarestorage.com", "Prefix": "2026/",
	}
	for i := range m.form.fields {
		if value, ok := values[m.form.fields[i].key]; ok {
			m.form.fields[i].in.Set(value)
		}
	}

	if save := m.handleFormKey(keyNamed(tea.KeyEnter)); save != nil {
		_ = save
	}
	profile, ok := m.cfg.Cloud.Profiles["photos"]
	if !ok {
		t.Fatalf("the profile should be saved, got %+v", m.cfg.Cloud.Profiles)
	}
	if profile.Provider != "r2" || profile.Region != "auto" || profile.Prefix != "2026/" {
		t.Errorf("saved profile = %+v", profile)
	}
	if m.Overlay != OverlayNone {
		t.Error("saving should close the form")
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "photos") {
		t.Errorf("the new bucket should be in the list:\n%s", out)
	}

	// And it is on disk, not only in memory.
	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatalf("the config should be valid: %v", err)
	}
	if _, ok := cfg.Cloud.Profiles["photos"]; !ok {
		t.Error("the profile should be written to the config file")
	}
}

// A bucket profile that cannot work is refused with a reason, not saved.
func TestAddBucketValidates(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.runAction("session.add")
	m.add.cursor = 1
	press(t, m, keyNamed(tea.KeyEnter))
	m.form.fields[0].in.Set("broken")
	m.form.fields[1].in.Set("s3")
	m.form.fields[2].in.Set("")
	m.handleFormKey(keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayForm {
		t.Error("an invalid profile should keep the form open")
	}
	if m.form.err == "" {
		t.Error("the form should say what is wrong")
	}
}

// The manager has one screen per kind of place, and Tab moves between them.
func TestConnectionSections(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{
		"backups": {Provider: "s3", Bucket: "my-backups", Region: "eu-central-1"},
	}
	m.loadConnections()

	// Overview shows both kinds.
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "prod") || !strings.Contains(out, "backups") {
		t.Fatalf("the overview should list both:\n%s", out)
	}
	if !strings.Contains(out, "Overview") || !strings.Contains(out, "SSH") || !strings.Contains(out, "S3") {
		t.Errorf("the tabs should be visible:\n%s", out)
	}

	// SSH shows hosts only.
	press(t, m, run('\t', 0, ""))
	if m.conn.section != secSSH {
		t.Fatalf("Tab should reach SSH, section is %v", m.conn.section)
	}
	out = ansi.Strip(render(m))
	if strings.Contains(out, "backups") {
		t.Errorf("the SSH screen should not list buckets:\n%s", out)
	}

	// S3 shows buckets only.
	press(t, m, keyNamed(tea.KeyTab))
	if m.conn.section != secS3 {
		t.Fatalf("Tab should reach S3, section is %v", m.conn.section)
	}
	out = ansi.Strip(render(m))
	if strings.Contains(out, "prod") {
		t.Errorf("the S3 screen should not list SSH hosts:\n%s", out)
	}
	if !strings.Contains(out, "backups") {
		t.Errorf("the S3 screen should list the bucket:\n%s", out)
	}

	// A planned section says so instead of looking empty.
	press(t, m, keyNamed(tea.KeyTab))
	if m.conn.section != secWebDAV {
		t.Fatalf("Tab should reach WebDAV, section is %v", m.conn.section)
	}
	out = ansi.Strip(render(m))
	if !strings.Contains(out, "M10") {
		t.Errorf("a planned screen should say when it arrives:\n%s", out)
	}
	if _, ok := m.currentConnection(); ok {
		t.Error("a planned screen has nothing to choose")
	}
}

// Adding from a section skips the kind chooser: the section already answered it.
func TestAddFromASectionSkipsTheChooser(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.conn.section = secS3
	m.runAction("session.add")
	if m.Overlay != OverlayForm || m.form.kind != "cloud" {
		t.Fatalf("a in S3 should open the bucket form directly, overlay=%v kind=%q", m.Overlay, m.form.kind)
	}

	m.Overlay = OverlayNone
	m.conn.section = secSSH
	m.runAction("session.add")
	if m.Overlay != OverlayForm || m.form.kind == "cloud" {
		t.Fatalf("a in SSH should open the host form, overlay=%v kind=%q", m.Overlay, m.form.kind)
	}

	m.Overlay = OverlayNone
	m.conn.section = secWebDAV
	m.runAction("session.add")
	if m.Overlay == OverlayForm {
		t.Error("a planned section should not open a form it cannot save")
	}
}

// A bucket on the launcher is called by its profile name, not its internal tab
// key: "anofac", not "cloud:anofac".
func TestLauncherNamesBucketsWithoutThePrefix(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{
		"anofac": {Provider: "s3", Bucket: "anofac", Region: "eu-central-1"},
	}
	m.loadConnections()
	m.selectConnection("anofac")
	cmd := m.connectToHighlighted()
	if cmd == nil {
		t.Fatalf("opening the bucket should start: %v", m.toasts)
	}
	m.handleCloudReady(cmd().(cloudReadyMsg))
	m.screen = screenHome

	out := ansi.Strip(render(m))
	if strings.Contains(out, "cloud:anofac") {
		t.Errorf("the launcher should not leak the tab key:\n%s", out)
	}
	if !strings.Contains(out, "anofac") {
		t.Errorf("the launcher should name the bucket:\n%s", out)
	}
	if !strings.Contains(out, "bucket") {
		t.Errorf("the row should say it is a bucket:\n%s", out)
	}
}

// A config with only code hosts used to look like an empty connection list. It
// says what is hidden, and H is the answer.
func TestHiddenHostsAreExplained(t *testing.T) {
	m, _ := newConnectionsModel(t, "Host github.com\n  HostName github.com\n  User git\n\nHost bitbucket.org\n  HostName bitbucket.org\n  User git\n")
	out := ansi.Strip(render(m))
	if !strings.Contains(out, "hidden") {
		t.Fatalf("the screen should say hosts are hidden:\n%s", out)
	}
	if !strings.Contains(out, "H shows everything") {
		t.Errorf("it should name the key that reveals them:\n%s", out)
	}
	press(t, m, run('H', 0, "H"))
	if out := ansi.Strip(render(m)); !strings.Contains(out, "github.com") {
		t.Errorf("H should show the code hosts:\n%s", out)
	}
}

// Labels sit in one column with a blank cell before their field, and no label is
// long enough to push the value off the card.
func TestFormLayout(t *testing.T) {
	fields := []connField{
		{label: "name"}, {label: "host"}, {label: "user"}, {label: "port"},
		{label: "key"}, {label: "jump"}, {label: "endpoint"}, {label: "secret"},
	}
	for _, content := range []int{46, 60, 70} {
		labelWidth, valueWidth := formLayout(content, fields)
		if valueWidth < 8 {
			t.Errorf("content %d leaves %d cells for the value", content, valueWidth)
		}
		for _, field := range fields {
			if lipgloss.Width(field.label) > labelWidth {
				t.Errorf("label %q is wider than the %d-cell column", field.label, labelWidth)
			}
		}
		if labelWidth+valueWidth > content {
			t.Errorf("content %d: columns are %d wide together", content, labelWidth+valueWidth)
		}
	}
}

// A bucket added in one run is there in the next: the profile is written to the
// config, and a fresh model built from that file lists it.
func TestBucketSurvivesARestart(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.configPath = t.TempDir() + "/config.yaml"

	m.runAction("session.add")
	m.add.cursor = 1 // AWS S3
	press(t, m, keyNamed(tea.KeyEnter))
	m.form.fields[0].in.Set("backups")
	m.form.fields[2].in.Set("my-backups")
	m.form.fields[3].in.Set("eu-central-1")
	m.handleFormKey(keyNamed(tea.KeyEnter))

	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatalf("the saved config should load: %v", err)
	}
	if _, ok := cfg.Cloud.Profiles["backups"]; !ok {
		t.Fatalf("the profile was not written: %+v", cfg.Cloud.Profiles)
	}

	// A new interface, same file.
	next, err := New(Options{
		Config: cfg, Profile: colorprofile.TrueColor, HasDark: true, Dir: t.TempDir(),
		SSHConfigPath: m.sshPath, ConfigPath: m.configPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	next.loadConnections()
	found := false
	for _, e := range next.visibleConnections() {
		if e.Alias == "backups" && e.Cloud {
			found = true
		}
	}
	if !found {
		t.Errorf("the restarted interface should list the bucket: %+v", next.visibleConnections())
	}
}
