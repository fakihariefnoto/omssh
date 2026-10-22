package tui

import (
	"strings"
	"testing"

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
