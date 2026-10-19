package keymap

import (
	"strings"
	"testing"

	"github.com/fakihariefnoto/omssh/internal/actions"
)

func build(t *testing.T, opts Options) *Map {
	t.Helper()
	m, err := Build(actions.Default(), opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

// The letters are the point: a file manager in a terminal has always worked this
// way, and it needs no function row.
func TestLetterBindingsOnTheFileScreen(t *testing.T) {
	m := build(t, Options{})
	cases := map[string]actions.ID{
		"h": "nav.parent",
		"j": "nav.down",
		"k": "nav.up",
		"l": "nav.open",
		"d": "file.delete",
		"r": "file.rename",
		"e": "file.edit",
		"v": "file.view",
		"y": "sel.mark",
		"x": "sel.cut",
		"p": "sel.paste",
		"n": "file.new",
		"N": "file.mkdir",
		".": "nav.hidden",
		"/": "nav.filter",
		"o": "nav.reveal",
		"G": "nav.last",
		"~": "nav.home",
		"-": "nav.last-dir",
	}
	for key, want := range cases {
		got, ok := m.Lookup(key, "files")
		if !ok {
			t.Errorf("%s is not bound on the file screen", key)
			continue
		}
		if got != want {
			t.Errorf("%s resolves to %s, want %s", key, got, want)
		}
	}
}

// The app-level keys keep their conventional homes.
func TestApplicationBindings(t *testing.T) {
	m := build(t, Options{})
	cases := map[string]actions.ID{
		"ctrl+q": "app.quit",
		"f10":    "app.quit",
		"esc":    "app.back",
		"ctrl+k": "app.palette",
		"ctrl+o": "session.open",
		"ctrl+l": "nav.path",
		"ctrl+r": "nav.refresh",
		"ctrl+a": "sel.all",
		"ctrl+e": "archive.extract",
		"ctrl+g": "archive.compress",
		"tab":    "nav.switch-pane",
		"space":  "sel.toggle",
	}
	for key, want := range cases {
		got, ok := m.Lookup(key, "files")
		if !ok {
			t.Errorf("%s is not bound", key)
			continue
		}
		if got != want {
			t.Errorf("%s resolves to %s, want %s", key, got, want)
		}
	}
}

// Digits switch tabs, which is what makes several live sessions navigable
// without a modifier dance.
func TestDigitsSwitchTabs(t *testing.T) {
	m := build(t, Options{})
	for _, digit := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"} {
		id, ok := m.Lookup(digit, "files")
		if !ok || id != "session.tab" {
			t.Errorf("%s should switch tab, got %q (%v)", digit, id, ok)
		}
	}
	if id, ok := m.Lookup("[", "files"); !ok || id != "session.tab-prev" {
		t.Errorf("[ should be the previous tab, got %q", id)
	}
	if id, ok := m.Lookup("]", "files"); !ok || id != "session.tab-next" {
		t.Errorf("] should be the next tab, got %q", id)
	}
}

// The same letter may mean different things on different screens, which is what
// lets both the file manager and the host list use "d" and "r" naturally.
func TestTheSameLetterOnTwoScreens(t *testing.T) {
	m := build(t, Options{})

	if id, _ := m.Lookup("d", "files"); id != "file.delete" {
		t.Errorf("d on files = %s, want file.delete", id)
	}
	if id, _ := m.Lookup("d", "connections"); id != "session.delete" {
		t.Errorf("d on connections = %s, want session.delete", id)
	}
	if id, _ := m.Lookup("r", "files"); id != "file.rename" {
		t.Errorf("r on files = %s, want file.rename", id)
	}
	if id, _ := m.Lookup("r", "connections"); id != "session.reload" {
		t.Errorf("r on connections = %s, want session.reload", id)
	}
	// A screen-scoped binding does not leak onto a screen that has no opinion.
	if id, ok := m.Lookup("c", "files"); ok {
		t.Errorf("c is only a connection action, but files resolved it to %s", id)
	}
	if id, ok := m.Lookup("c", "connections"); !ok || id != "session.connect" {
		t.Errorf("c on connections = %q (%v), want session.connect", id, ok)
	}
}

// A key bound twice on the same screen is an ambiguity, and is refused.
func TestDuplicateBindingOnOneScreenIsRefused(t *testing.T) {
	reg := actions.New(
		actions.Action{ID: "a", Label: "A", Category: actions.CatApp, Desc: "d", Default: "z", Screen: "files"},
		actions.Action{ID: "b", Label: "B", Category: actions.CatApp, Desc: "d", Default: "z", Screen: "files"},
	)
	if _, err := Build(reg, Options{}); err == nil {
		t.Fatal("two actions on one screen cannot share a key")
	}
	// The same key on different screens is not a conflict.
	reg = actions.New(
		actions.Action{ID: "a", Label: "A", Category: actions.CatApp, Desc: "d", Default: "z", Screen: "files"},
		actions.Action{ID: "b", Label: "B", Category: actions.CatApp, Desc: "d", Default: "z", Screen: "connections"},
	)
	if _, err := Build(reg, Options{}); err != nil {
		t.Errorf("the same key on two screens should be allowed: %v", err)
	}
}

// Esc must never look like quit, and neither can be rebound into the other.
func TestEscIsBackNotQuit(t *testing.T) {
	m := build(t, Options{})
	id, ok := m.Lookup("esc", "files")
	if !ok {
		t.Fatal("esc should be bound")
	}
	if id == "app.quit" {
		t.Fatal("esc must not be bound to quit")
	}
	if _, err := Build(actions.Default(), Options{Overrides: map[string]string{"app.quit": "esc"}}); err == nil {
		t.Fatal("binding quit to esc should be rejected")
	}
	if _, err := Build(actions.Default(), Options{Overrides: map[string]string{"app.back": "x"}}); err == nil {
		t.Fatal("rebinding esc should be rejected")
	}
}

// Keys terminals reserve are never bound, because a binding that cannot be
// delivered looks like a broken application.
func TestUndeliverableKeysAreRejected(t *testing.T) {
	for key := range Undeliverable {
		_, err := Build(actions.Default(), Options{Overrides: map[string]string{"nav.refresh": key}})
		if err == nil {
			t.Errorf("override to %s should be rejected", key)
		}
	}
	if dropped := build(t, Options{}).Dropped(); len(dropped) != 0 {
		t.Fatalf("the default bindings dropped: %+v", dropped)
	}
}

func TestCapabilityProbeDropsUndeliverableKeys(t *testing.T) {
	reg := actions.New(actions.Action{
		ID: "test.action", Label: "Test", Category: actions.CatApp,
		Desc: "d", Default: "ctrl+shift+p",
	})
	m, err := BuildWithDeliverer(reg, Options{}, terminalDeliverer{kitty: false})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dropped := m.Dropped()
	if len(dropped) != 1 {
		t.Fatalf("expected one dropped binding, got %+v", dropped)
	}
	if !strings.Contains(dropped[0].Reason, "modified keys") {
		t.Errorf("the reason should explain the terminal limit, got %q", dropped[0].Reason)
	}
}

type terminalDeliverer struct{ kitty bool }

func (d terminalDeliverer) CanDeliver(key string) (string, bool) {
	if d.kitty {
		return "", true
	}
	if strings.HasPrefix(key, "ctrl+shift+") {
		return "needs a terminal that reports modified keys", false
	}
	return "", true
}

// An override replaces the action's bindings rather than adding to them.
func TestOverridesWinOverDefaults(t *testing.T) {
	m := build(t, Options{Overrides: map[string]string{"file.delete": "D"}})
	if _, ok := m.Lookup("D", "files"); !ok {
		t.Fatal("the override should be bound")
	}
	if _, ok := m.Lookup("d", "files"); ok {
		t.Error("the old binding should be gone")
	}
	if keys := m.Keys("file.delete"); len(keys) != 1 {
		t.Errorf("expected a single binding, got %v", keys)
	}
}

func TestOverridesRejectUnknownActions(t *testing.T) {
	_, err := Build(actions.Default(), Options{Overrides: map[string]string{"nope.nothing": "ctrl+x"}})
	if err == nil {
		t.Fatal("an unknown action id should be rejected")
	}
	if !strings.Contains(err.Error(), "nope.nothing") {
		t.Errorf("the error should name the action, got %v", err)
	}
}

// A sequence is two keys in a row, and a dead prefix must not swallow the next
// key.
func TestSequences(t *testing.T) {
	m := build(t, Options{})
	resolver := m.Resolver("files")

	if out := resolver.Feed("g"); !out.Pending {
		t.Fatalf("g should start a sequence, got %+v", out)
	}
	out := resolver.Feed("g")
	if !out.Matched || out.ID != "nav.first" {
		t.Fatalf("g g should go to the first entry, got %+v", out)
	}

	// g then j is not a sequence: the j still means "down".
	if out := resolver.Feed("g"); !out.Pending {
		t.Fatalf("g should be pending, got %+v", out)
	}
	if out := resolver.Feed("j"); !out.Matched || out.ID != "nav.down" {
		t.Fatalf("after a dead prefix, j should move down, got %+v", out)
	}
	resolver.Reset()
	if resolver.Pending() != "" {
		t.Error("Reset should clear the pending sequence")
	}
}

// Moving screens drops a half-typed sequence: "g" means nothing on the
// connections screen.
func TestChangingScreenResetsThePrefix(t *testing.T) {
	m := build(t, Options{})
	resolver := m.Resolver("files")
	if out := resolver.Feed("g"); !out.Pending {
		t.Fatalf("g should be pending, got %+v", out)
	}
	resolver.SetScreen("connections")
	if resolver.Pending() != "" {
		t.Error("changing screen should drop the prefix")
	}
	// d on the connections screen is the connection action, not a completion of
	// the file-screen sequence.
	if out := resolver.Feed("d"); !out.Matched || out.ID != "session.delete" {
		t.Errorf("d after a screen change = %+v, want session.delete", out)
	}
}

// A scoped action shadows a global one on its own screen.
func TestScopedActionShadowsGlobal(t *testing.T) {
	reg := actions.New(
		actions.Action{ID: "global", Label: "Global", Category: actions.CatApp, Desc: "d", Default: "q"},
		actions.Action{ID: "scoped", Label: "Scoped", Category: actions.CatApp, Desc: "d", Default: "q", Screen: "files"},
	)
	m, err := Build(reg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := m.Lookup("q", "files"); id != "scoped" {
		t.Errorf("on files the scoped action should win, got %s", id)
	}
	if id, _ := m.Lookup("q", "home"); id != "global" {
		t.Errorf("elsewhere the global action should win, got %s", id)
	}
}

func TestPrimaryShowsAReadableKey(t *testing.T) {
	m := build(t, Options{})
	if got := m.Primary("app.quit"); got != "Ctrl+Q" {
		t.Errorf("Primary(app.quit) = %q, want %q", got, "Ctrl+Q")
	}
	if got := m.Primary("file.delete"); got != "d" {
		t.Errorf("Primary(file.delete) = %q, want %q", got, "d")
	}
	if got := m.Primary("nav.first"); got != "g g" {
		t.Errorf("Primary(nav.first) = %q, want %q", got, "g g")
	}
	if got := m.Primary("file.chmod"); got != "" {
		t.Errorf("an action with no binding should render as empty, got %q", got)
	}
}
