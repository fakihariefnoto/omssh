package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

	cmd := m.runAction("session.add-cloud")
	_ = cmd
	if m.Overlay != OverlayForm || m.form.kind != "cloud" {
		t.Fatalf("b should open the bucket form, overlay=%v kind=%q", m.Overlay, m.form.kind)
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
	m.runAction("session.add-cloud")
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
