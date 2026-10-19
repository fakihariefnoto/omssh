package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// deployModel is a model with one deploy profile whose "remote" is a local
// directory: the same code path, without a server.
func deployModel(t *testing.T) (*Model, string, string) {
	t.Helper()
	localDir, remoteDir := t.TempDir(), t.TempDir()
	for path, body := range map[string]string{"index.html": "<html>new", "css/site.css": "body{}"} {
		full := filepath.Join(localDir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t, func(cfg *config.Config) {
		cfg.Deploy.Profiles = map[string]config.Profile{
			"site": {Host: "contabo", LocalRoot: localDir, RemoteRoot: "/", Mode: "sync"},
		}
	})
	remote, err := vfs.NewLocal(remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	m.rememberSession(&session.Session{
		ID: "s1", Target: omssh.Target{Alias: "contabo"}, FS: remote, Root: "/", Base: remoteDir,
	})
	return m, localDir, remoteDir
}

// The card reviews before it writes: the plan names what would change, and
// nothing has happened yet.
func TestDeployReviewsThenDeploys(t *testing.T) {
	m, _, remoteDir := deployModel(t)
	m.openDeploy()
	if m.Overlay != OverlayDeploy {
		t.Fatal("Alt+D should open the deploy card")
	}
	if out := ansi.Strip(render(m)); !strings.Contains(out, "site") || !strings.Contains(out, "contabo") {
		t.Fatalf("the card should list the profile:\n%s", out)
	}

	cmd := m.handleDeployKey(keyNamed(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter should start the review")
	}
	msg := cmd()
	planMsg, ok := msg.(deployPlanMsg)
	if !ok || planMsg.err != nil {
		t.Fatalf("the review should produce a plan: %+v err=%v", msg, planMsg.err)
	}
	m.deploy.loading = false
	m.deploy.plan = &planMsg.plan
	m.deploy.profile = "site"

	out := ansi.Strip(render(m))
	for _, want := range []string{"Deploy site", "2 create", "1 delete", "stale.txt", "Enter deploys"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan should say %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(remoteDir, "stale.txt")); err != nil {
		t.Fatal("reviewing must not write anything")
	}

	// Enter runs it, and the tree becomes a mirror of the local one.
	applyCmd := m.handleDeployKey(keyNamed(tea.KeyEnter))
	if applyCmd == nil {
		t.Fatal("Enter on a plan should deploy")
	}
	done, ok := applyCmd().(deployDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("the deploy should finish: %+v", done)
	}
	if got, err := os.ReadFile(filepath.Join(remoteDir, "index.html")); err != nil || string(got) != "<html>new" {
		t.Errorf("index.html should hold the local bytes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(remoteDir, "css", "site.css")); err != nil {
		t.Errorf("the new file should be there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(remoteDir, "stale.txt")); !os.IsNotExist(err) {
		t.Error("sync mode should have removed stale.txt")
	}
}

// With no profiles configured, the action explains where they live instead of
// opening an empty card.
func TestDeployWithoutProfilesExplainsItself(t *testing.T) {
	m := newTestModel(t)
	m.openDeploy()
	if m.Overlay == OverlayDeploy {
		t.Fatal("an empty deploy card is a dead end")
	}
	if len(m.toasts) == 0 {
		t.Error("it should say where profiles come from")
	}
}
