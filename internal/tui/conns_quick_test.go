package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/config"
)

// selectConnection puts the cursor on an alias.
func (m *Model) selectConnection(alias string) {
	for i, e := range m.visibleConnections() {
		if e.Alias == alias {
			m.conn.cursor = i
			return
		}
	}
}

const codeHostFixture = `Host prod
  HostName 203.0.113.10
  User deploy

Host github.com
  HostName github.com
  User git

Host bitbucket.org
  HostName bitbucket.org
  User git

Host staging
  HostName staging.example.com
  User ubuntu
`

// A git remote is not a machine you log into, and it should not push the hosts you
// do use off the list.
func TestCodeHostsAreHiddenUntilAskedFor(t *testing.T) {
	m, _ := newConnectionsModel(t, codeHostFixture)
	out := ansi.Strip(render(m))
	if strings.Contains(out, "bitbucket.org") {
		t.Errorf("bitbucket should be hidden by default:\n%s", out)
	}
	if !strings.Contains(out, "prod") || !strings.Contains(out, "staging") {
		t.Errorf("the real hosts should be listed:\n%s", out)
	}

	press(t, m, run('H', 0, "H"))
	if out := ansi.Strip(render(m)); !strings.Contains(out, "bitbucket.org") {
		t.Errorf("H should show every host:\n%s", out)
	}
}

// Pinning puts a host first, and the choice is saved.
func TestFavouritesSortToTheTop(t *testing.T) {
	m, _ := newConnectionsModel(t, codeHostFixture)
	m.configPath = t.TempDir() + "/config.yaml"

	m.selectConnection("staging")
	press(t, m, run('f', 0, "f"))
	if len(m.cfg.Connections.Favorites) == 0 || m.cfg.Connections.Favorites[0] != "staging" {
		t.Fatalf("staging should be pinned, got %v", m.cfg.Connections.Favorites)
	}
	if got := m.visibleConnections()[0].Alias; got != "staging" {
		t.Errorf("the pinned host should be first, got %q", got)
	}

	// Pinning again unpins.
	m.selectConnection("staging")
	press(t, m, run('f', 0, "f"))
	if len(m.cfg.Connections.Favorites) != 0 {
		t.Errorf("pinning twice should unpin, got %v", m.cfg.Connections.Favorites)
	}

	// And the file on disk has it.
	cfg, err := config.Load(m.configPath)
	if err != nil {
		t.Fatalf("the preferences should be saved: %v", err)
	}
	if len(cfg.Connections.Favorites) != 0 {
		t.Errorf("the saved config still pins something: %v", cfg.Connections.Favorites)
	}
}

// Recent hosts come before untouched ones, newest first, and the list is capped.
func TestRecentHostsComeFirst(t *testing.T) {
	m, _ := newConnectionsModel(t, codeHostFixture)
	m.noteRecent("staging")
	m.noteRecent("prod")
	if got := m.visibleConnections()[0].Alias; got != "prod" {
		t.Errorf("the newest host should be first, got %q", got)
	}
	if m.recentRank("staging") != 1 {
		t.Errorf("staging should be second, rank %d", m.recentRank("staging"))
	}

	for i := 0; i < config.RecentLimit+5; i++ {
		m.noteRecent("host" + string(rune('a'+i)))
	}
	if len(m.cfg.Connections.Recent) != config.RecentLimit {
		t.Errorf("recent should be capped at %d, got %d", config.RecentLimit, len(m.cfg.Connections.Recent))
	}
}

// Hiding is the manual version of the same idea.
func TestHidingAHost(t *testing.T) {
	m, _ := newConnectionsModel(t, codeHostFixture)
	m.selectConnection("staging")
	press(t, m, run('x', 0, "x"))
	for _, e := range m.visibleConnections() {
		if e.Alias == "staging" {
			t.Fatalf("staging should be hidden, visible: %+v", m.visibleConnections())
		}
	}
	press(t, m, run('H', 0, "H"))
	found := false
	for _, e := range m.visibleConnections() {
		if e.Alias == "staging" {
			found = true
		}
	}
	if !found {
		t.Error("H should bring a hidden host back into view")
	}
	_ = tea.KeyEnter
}
