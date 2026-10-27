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
	if m.form.provider != "r2" {
		t.Errorf("the form should be an r2 form, provider is %q", m.form.provider)
	}
	// The type is a chooser, not a typed value: focus it and the arrows show.
	for i := range m.form.fields {
		if m.form.fields[i].key == "Type" {
			m.form.focus = i
		}
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "‹ r2 ›") {
		t.Errorf("the form should show the type it is adding:\n%s", out)
	}
	values := map[string]string{
		"Name": "photos", "Bucket": "my-photos",
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
	// The bucket is the second field now that "type" is fixed.
	m.form.fields[1].in.Set("")
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

	// GCP is real: it lists gcs profiles and no longer says it is planned.
	press(t, m, keyNamed(tea.KeyTab))
	if m.conn.section != secGCP {
		t.Fatalf("Tab should reach GCP, section is %v", m.conn.section)
	}
	out = ansi.Strip(render(m))
	if strings.Contains(out, "backups") {
		t.Errorf("the GCP screen should not list an S3 bucket:\n%s", out)
	}

	// Azure is real too, and comes before WebDAV, which is last.
	press(t, m, keyNamed(tea.KeyTab))
	if m.conn.section != secAzure {
		t.Fatalf("Tab should reach the Azure screen, section is %v", m.conn.section)
	}
	press(t, m, keyNamed(tea.KeyTab))
	if m.conn.section != secWebDAV {
		t.Fatalf("Tab should end on WebDAV, section is %v", m.conn.section)
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
	m.conn.section = secAzure
	m.runAction("session.add")
	if m.Overlay != OverlayForm || m.form.provider != "azure" {
		t.Fatalf("a in Azure should open an azure form, overlay=%v provider=%q", m.Overlay, m.form.provider)
	}
	// Entra needs no secret; key and sas do.
	m.form.fields = append(m.form.fields, connField{key: "Name", label: "name"})
	for i := range m.form.fields {
		switch m.form.fields[i].key {
		case "Name":
			m.form.fields[i].in.Set("blobs")
		case "Container":
			m.form.fields[i].in.Set("site")
		case "Account":
			m.form.fields[i].in.Set("acct")
		case "Auth":
			m.form.fields[i].in.Set("key")
		}
	}
	m.saveCloudForm()
	if !strings.Contains(m.form.err, "account key") {
		t.Errorf("key auth without a secret should say what is missing, err=%q", m.form.err)
	}
	// Entra needs no secret, so it saves without touching the keyring.
	for i := range m.form.fields {
		if m.form.fields[i].key == "Auth" {
			m.form.fields[i].in.Set("entra")
		}
	}
	m.saveCloudForm()
	if m.form.err != "" {
		t.Fatalf("the azure form should save: %v", m.form.err)
	}
	if got := m.cfg.Cloud.Profiles["blobs"]; got.Provider != "azure" || got.Bucket != "site" || got.Auth != "entra" {
		t.Errorf("saved profile = %+v", got)
	}

	// WebDAV is real: a there opens its form directly, typed.
	m.Overlay = OverlayNone
	m.conn.section = secWebDAV
	m.runAction("session.add")
	if m.Overlay != OverlayForm || m.form.provider != "webdav" {
		t.Fatalf("a in WebDAV should open a webdav form, overlay=%v provider=%q", m.Overlay, m.form.provider)
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
	for i := range m.form.fields {
		switch m.form.fields[i].key {
		case "Name":
			m.form.fields[i].in.Set("backups")
		case "Bucket":
			m.form.fields[i].in.Set("my-backups")
		case "Region":
			m.form.fields[i].in.Set("eu-central-1")
		}
	}
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

// The S3 screen's form is not an S3 form: its type is a chooser, and cycling it
// changes the provider that gets saved.
func TestBucketFormChoosesTheProvider(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.configPath = t.TempDir() + "/config.yaml"
	m.conn.section = secS3
	m.runAction("session.add")
	if m.Overlay != OverlayForm || m.form.provider != "s3" {
		t.Fatalf("the S3 section should open its form, overlay=%v provider=%q", m.Overlay, m.form.provider)
	}

	typeAt := -1
	for i := range m.form.fields {
		if m.form.fields[i].key == "Type" {
			typeAt = i
		}
	}
	if typeAt < 0 {
		t.Fatal("the bucket form should let the provider be chosen")
	}
	m.form.focus = typeAt
	press(t, m, keyNamed(tea.KeyRight))
	if got := m.form.fields[typeAt].in.Value; got != "r2" {
		t.Fatalf("→ should move s3 to r2, got %q", got)
	}
	press(t, m, keyNamed(tea.KeyLeft))
	press(t, m, keyNamed(tea.KeyLeft))
	if got := m.form.fields[typeAt].in.Value; got != "oss" {
		t.Fatalf("← from s3 should wrap to oss, got %q", got)
	}
	// oss → s3 → r2 → b2
	press(t, m, keyNamed(tea.KeyRight))
	press(t, m, keyNamed(tea.KeyRight))
	press(t, m, keyNamed(tea.KeyRight))
	if got := m.form.fields[typeAt].in.Value; got != "b2" {
		t.Fatalf("→ should reach b2, got %q", got)
	}

	for i := range m.form.fields {
		switch m.form.fields[i].key {
		case "Name":
			m.form.fields[i].in.Set("archive")
		case "Bucket":
			m.form.fields[i].in.Set("my-archive")
		case "Region":
			m.form.fields[i].in.Set("us-west-004")
		}
	}
	m.handleFormKey(keyNamed(tea.KeyEnter))
	if got := m.cfg.Cloud.Profiles["archive"].Provider; got != "b2" {
		t.Fatalf("the chosen provider should be saved, got %q", got)
	}
}

// The native GCS modes are separate entries in the chooser, and ADC needs nothing
// but a bucket.
func TestGCSNativeFormSaves(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.configPath = t.TempDir() + "/config.yaml"

	m.runAction("session.add")
	at := -1
	for i, kind := range addKinds() {
		if kind.provider == "gcs" && kind.auth == "adc" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the chooser should offer GCS with application default credentials")
	}
	m.add.cursor = at
	press(t, m, keyNamed(tea.KeyEnter))
	if m.Overlay != OverlayForm || m.form.provider != "gcs" {
		t.Fatalf("choosing ADC should open the GCS form, overlay=%v provider=%q", m.Overlay, m.form.provider)
	}
	for i := range m.form.fields {
		switch m.form.fields[i].key {
		case "Name":
			m.form.fields[i].in.Set("gcs-backups")
		case "Bucket":
			m.form.fields[i].in.Set("my-backups")
		}
	}
	m.handleFormKey(keyNamed(tea.KeyEnter))
	profile, ok := m.cfg.Cloud.Profiles["gcs-backups"]
	if !ok {
		t.Fatalf("the profile should be saved: %+v", m.cfg.Cloud.Profiles)
	}
	if profile.Provider != "gcs" || profile.Auth != "adc" {
		t.Errorf("saved profile = %+v, want gcs/adc", profile)
	}
}

// A bucket profile is editable from the manager. A bad R2 endpoint is exactly the
// case: the form opens filled, the fix is one field, and the secret stays put.
func TestEditCloudProfile(t *testing.T) {
	m, _ := newConnectionsModel(t, connFixture)
	m.configPath = t.TempDir() + "/config.yaml"
	m.cfg.Cloud.Profiles = map[string]config.CloudProfile{
		"assets": {Provider: "r2", Bucket: "assets", Region: "auto", Prefix: "2026/"},
	}
	m.loadConnections()

	for i, entry := range m.visibleConnections() {
		if entry.Alias == "assets" {
			m.conn.cursor = i
		}
	}
	m.runAction("session.edit")
	if m.Overlay != OverlayForm || m.form.original != "assets" {
		t.Fatalf("e on a bucket should open its profile, overlay=%v original=%q", m.Overlay, m.form.original)
	}
	values := map[string]string{}
	for _, field := range m.form.fields {
		values[field.key] = field.in.Value
	}
	if values["Type"] != "r2" || values["Bucket"] != "assets" || values["Prefix"] != "2026/" {
		t.Fatalf("the form should be filled from the profile: %+v", values)
	}

	for i := range m.form.fields {
		if m.form.fields[i].key == "Endpoint" {
			m.form.fields[i].in.Set("https://acct.r2.cloudflarestorage.com")
		}
	}
	m.handleFormKey(keyNamed(tea.KeyEnter))
	profile, ok := m.cfg.Cloud.Profiles["assets"]
	if !ok {
		t.Fatalf("the profile should still exist: %+v", m.cfg.Cloud.Profiles)
	}
	if profile.Endpoint != "https://acct.r2.cloudflarestorage.com" {
		t.Errorf("the endpoint was not saved: %+v", profile)
	}
	if profile.Prefix != "2026/" || profile.Bucket != "assets" {
		t.Errorf("editing one field should keep the rest: %+v", profile)
	}
}
