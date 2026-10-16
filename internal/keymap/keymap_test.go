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

func TestStandardPresetResolvesDocumentedKeys(t *testing.T) {
	m := build(t, Options{})
	cases := map[string]actions.ID{
		"ctrl+q":    "app.quit",
		"f10":       "app.quit",
		"esc":       "app.back",
		"ctrl+k":    "app.palette",
		"ctrl+c":    "file.copy",
		"ctrl+v":    "file.paste",
		"f5":        "file.copy-to",
		"f8":        "file.delete",
		"del":       "file.delete",
		"f2":        "file.rename",
		"f7":        "file.mkdir",
		"ctrl+e":    "archive.extract",
		"ctrl+g":    "archive.compress",
		"alt+d":     "deploy.open",
		"alt+h":     "nav.hidden",
		"alt+b":     "view.sidebar",
		"alt+left":  "nav.back",
		"ctrl+l":    "nav.path",
		"tab":       "nav.switch-pane",
		"shift+tab": "nav.switch-pane-prev",
		"space":     "sel.toggle",
	}
	for key, want := range cases {
		got, ok := m.Lookup(key)
		if !ok {
			t.Errorf("%s is not bound to anything", key)
			continue
		}
		if got != want {
			t.Errorf("%s resolves to %s, want %s", key, got, want)
		}
	}
}

// Esc must never look like quit, whichever way the user configures things. It is
// a single byte that terminals also use to begin escape sequences, and it already
// has three jobs: cancel, clear and go back (ADR 0006).
func TestEscIsBackNotQuit(t *testing.T) {
	m := build(t, Options{})
	id, ok := m.Lookup("esc")
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

// The keys terminals reserve must never be bound, because a binding that cannot
// be delivered looks like a broken application.
func TestUndeliverableKeysAreRejectedAndDropped(t *testing.T) {
	for key := range Undeliverable {
		_, err := Build(actions.Default(), Options{Overrides: map[string]string{"nav.refresh": key}})
		if err == nil {
			t.Errorf("override to %s should be rejected", key)
		}
	}
	// Nothing in the default bindings is undeliverable either.
	m := build(t, Options{})
	if dropped := m.Dropped(); len(dropped) != 0 {
		t.Fatalf("default bindings dropped: %+v", dropped)
	}
}

// Without the Kitty protocol, modified keys cannot be distinguished, so they are
// dropped with a reason rather than silently ignored.
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
	if _, ok := m.Lookup("ctrl+shift+p"); ok {
		t.Error("the undeliverable binding should not be indexed")
	}

	// With the Kitty protocol the same binding works.
	m, err = BuildWithDeliverer(reg, Options{}, terminalDeliverer{kitty: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(m.Dropped()) != 0 {
		t.Errorf("nothing should be dropped with Kitty support: %+v", m.Dropped())
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

// These stand in for the app's own deliverer so the probe behaviour is tested
// here too; the app's version is covered by the interface tests.

func TestOverridesWinOverDefaults(t *testing.T) {
	m := build(t, Options{Overrides: map[string]string{"file.delete": "ctrl+d"}})
	if _, ok := m.Lookup("ctrl+d"); !ok {
		t.Fatal("the override should be bound")
	}
	if _, ok := m.Lookup("f8"); ok {
		t.Error("the old binding should be gone")
	}
	// The action keeps only the new key, so the palette shows the truth.
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

func TestUnknownPresetIsRejected(t *testing.T) {
	if _, err := Build(actions.Default(), Options{Preset: "emacs"}); err == nil {
		t.Fatal("an unknown preset should be rejected")
	}
}

// The vim preset changes navigation only. Operations stay standard, so a
// shortcut always means one thing no matter which preset is active.
func TestVimPresetChangesNavigationOnly(t *testing.T) {
	vim := build(t, Options{Preset: Vim})
	navCases := map[string]actions.ID{
		"h": "nav.parent",
		"j": "nav.down",
		"k": "nav.up",
		"l": "nav.open",
		"~": "nav.home",
		"-": "nav.last-dir",
		"/": "nav.filter",
		"v": "sel.extend-down",
	}
	for key, want := range navCases {
		got, ok := vim.Lookup(key)
		if !ok {
			t.Errorf("vim preset: %s is not bound", key)
			continue
		}
		if got != want {
			t.Errorf("vim preset: %s resolves to %s, want %s", key, got, want)
		}
	}
	// Operations are untouched.
	for key, want := range map[string]actions.ID{
		"ctrl+c": "file.copy",
		"f8":     "file.delete",
		"f5":     "file.copy-to",
	} {
		if got, _ := vim.Lookup(key); got != want {
			t.Errorf("vim preset changed an operation: %s = %s, want %s", key, got, want)
		}
	}
	// gg is a sequence, and both keys must be recognised as its prefix.
	resolver := vim.Resolver()
	if out := resolver.Feed("g"); !out.Pending {
		t.Fatalf("g should start a sequence, got %+v", out)
	}
	out := resolver.Feed("g")
	if !out.Matched || out.ID != "nav.first" {
		t.Fatalf("g g should go to the first entry, got %+v", out)
	}
}

// A half-typed sequence must not swallow the next key forever.
func TestSequenceRecovery(t *testing.T) {
	m := build(t, Options{Preset: Vim})
	resolver := m.Resolver()
	if out := resolver.Feed("g"); !out.Pending {
		t.Fatalf("g should be pending, got %+v", out)
	}
	// "g" then "j" is not a sequence, so the j must still mean "down".
	out := resolver.Feed("j")
	if !out.Matched || out.ID != "nav.down" {
		t.Fatalf("after a dead prefix, j should move down, got %+v", out)
	}
	resolver.Reset()
	if resolver.Pending() != "" {
		t.Error("Reset should clear the pending sequence")
	}
}

// Prefix keys that are sequences in their own right must resolve on their own
// when the whole sequence exists.
func TestPrefixSequence(t *testing.T) {
	m := build(t, Options{})
	resolver := m.Resolver()
	if out := resolver.Feed("ctrl+space"); !out.Pending {
		t.Fatalf("ctrl+space should start a sequence, got %+v", out)
	}
	out := resolver.Feed("n")
	if !out.Matched || out.ID != "terminal.new" {
		t.Fatalf("ctrl+space n should open a terminal, got %+v", out)
	}
}

func TestPrimaryShowsAReadableKey(t *testing.T) {
	m := build(t, Options{})
	if got := m.Primary("app.quit"); got != "Ctrl+Q" {
		t.Errorf("Primary(app.quit) = %q, want %q", got, "Ctrl+Q")
	}
	if got := m.Primary("file.menu"); got != "F9" {
		t.Errorf("Primary(file.menu) = %q, want %q", got, "F9")
	}
	if got := m.Primary("file.chmod"); got != "" {
		t.Errorf("an action with no binding should render as empty, got %q", got)
	}
	if got := m.Primary("terminal.new"); got != "Ctrl+Space n" {
		t.Errorf("Primary(terminal.new) = %q, want %q", got, "Ctrl+Space n")
	}
}

// Actions that apply must be reachable with their own key: the action bar and
// the palette both look this up.
func TestActionForRespectsContext(t *testing.T) {
	m := build(t, Options{})
	if _, ok := m.ActionFor("ctrl+c", actions.Context{}); ok {
		t.Error("copy should not apply with nothing selected")
	}
	if id, ok := m.ActionFor("ctrl+c", actions.Context{Selection: 1, Local: true}); !ok || id != "file.copy" {
		t.Errorf("copy should apply with a selection, got %q %v", id, ok)
	}
}
