package actions

import (
	"strings"
	"testing"
)

// The palette, the context menu and the action bar are all renderers over this
// registry, so an action without a label, a description or a known category
// would be invisible or unreadable in one of them.
func TestRegistryValidate(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("the built-in registry is invalid: %v", err)
	}
}

// Every action must be reachable from the palette, which is what keeps the
// "nothing is key-only" promise from decaying as the app grows (ADR 0006).
func TestEveryActionIsDiscoverable(t *testing.T) {
	reg := Default()
	reg2 := New(reg.All()...) // the palette is built from All(), so this is the path
	palette := map[ID]bool{}
	for _, a := range reg2.Palette() {
		palette[a.ID] = true
	}
	for _, a := range reg.All() {
		if !palette[a.ID] {
			t.Errorf("%s is missing from the palette", a.ID)
		}
	}
}

func TestRegistryRejectsIncompleteActions(t *testing.T) {
	cases := map[string]Action{
		"no label":     {ID: "x.one", Category: CatApp, Desc: "does something"},
		"no desc":      {ID: "x.two", Label: "Do", Category: CatApp},
		"bad category": {ID: "x.three", Label: "Do", Category: "Nowhere", Desc: "does something"},
	}
	for name, a := range cases {
		if err := New(a).Validate(); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestRegistryRejectsDuplicateIDs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic on duplicate ids")
		}
	}()
	New(
		Action{ID: "dup", Label: "One", Category: CatApp, Desc: "d"},
		Action{ID: "dup", Label: "Two", Category: CatApp, Desc: "d"},
	)
}

// The palette is grouped by category in the documented order, so what a person
// reaches for first is not scattered.
func TestPaletteOrdering(t *testing.T) {
	list := Default().Palette()
	last := -1
	rank := map[Category]int{}
	for i, c := range Categories {
		rank[c] = i
	}
	for _, a := range list {
		r := rank[a.Category]
		if r < last {
			t.Fatalf("%s (%s) appears after a later category", a.ID, a.Category)
		}
		last = r
	}
}

// Actions whose predicates do not mention the overlay stay applicable while one
// is open: the palette lists what you could do, not what the overlay allows.
func TestApplicability(t *testing.T) {
	reg := Default()
	empty := Context{Overlay: "", Panes: 2, Local: true}
	withSelection := Context{Overlay: "", Panes: 2, Local: true, Selection: 3, Cursor: true}

	copyAction, ok := reg.Get("sel.mark")
	if !ok {
		t.Fatal("sel.mark is missing")
	}
	if copyAction.Applicable(empty) {
		t.Error("marking should not apply with nothing selected")
	}
	if !copyAction.Applicable(withSelection) {
		t.Error("marking should apply with a selection")
	}

	paste, _ := reg.Get("sel.paste")
	if paste.Applicable(empty) {
		t.Error("pasting should not apply with nothing marked")
	}
	if !paste.Applicable(Context{Staged: 2}) {
		t.Error("pasting should apply once something is marked")
	}

	extract, _ := reg.Get("archive.extract")
	if extract.Applicable(empty) {
		t.Error("archive.extract should need an archive")
	}
	if !extract.Applicable(Context{Cursor: true, CursorIsArchive: true, Local: true}) {
		t.Error("archive.extract should apply to an archive")
	}

	// An open overlay does not change what is possible, or the palette would
	// grey out the very actions it exists to offer.
	if !copyAction.Applicable(Context{Overlay: "palette", Selection: 2, Local: true}) {
		t.Error("an open overlay should not suppress pane actions")
	}

	// A terminal tab is different: pane actions do not apply there, because the
	// keys belong to the remote shell.
	if copyAction.Applicable(Context{Terminal: true, Selection: 2, Local: true}) {
		t.Error("pane actions should not apply in a terminal tab")
	}
}

// Nothing is key-only, and nothing is palette-only either: an action the action
// bar hides must still be reachable.
func TestActionsWithoutBindingsAreReachable(t *testing.T) {
	reg := Default()
	unbound := 0
	for _, a := range reg.All() {
		if len(a.Bindings()) == 0 {
			unbound++
			if a.Label == "" || a.Desc == "" {
				t.Errorf("%s has no binding and no way to describe itself", a.ID)
			}
		}
	}
	if unbound == 0 {
		t.Error("expected some actions to be palette-only (send, fetch, chmod, symlink)")
	}
}

func TestNormalizeKey(t *testing.T) {
	cases := map[string]string{
		"Shift+Tab":    "shift+tab",
		"TAB":          "tab",
		"Ctrl+Q":       "ctrl+q",
		"control+q":    "ctrl+q",
		"option+h":     "alt+h",
		"Escape":       "esc",
		"Delete":       "del",
		"shift+ctrl+a": "ctrl+shift+a",
		" ":            "",
	}
	for in, want := range cases {
		if got := NormalizeKey(in); got != want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// A key bound twice is a bug the user would experience as "that key does the
// wrong thing", so Index refuses it.
func TestIndexRejectsConflictingBindings(t *testing.T) {
	reg := New(
		Action{ID: "a", Label: "A", Category: CatApp, Desc: "d", Default: "ctrl+x"},
		Action{ID: "b", Label: "B", Category: CatApp, Desc: "d", Default: "ctrl+x"},
	)
	_, err := reg.Index()
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	if !strings.Contains(err.Error(), "ctrl+x") {
		t.Errorf("the error should name the key, got %v", err)
	}
}

// The built-in bindings are free of conflicts, which the app depends on.
func TestDefaultBindingsHaveNoConflicts(t *testing.T) {
	if _, err := Default().Index(); err != nil {
		t.Fatalf("built-in bindings conflict: %v", err)
	}
}

// Buttons and bindings must be consistent: every Danger action is destructive
// enough to need a confirmation, and the tests for that live with the app.
func TestDangerActions(t *testing.T) {
	dangerous := map[ID]bool{}
	for _, a := range Default().All() {
		if a.Danger {
			dangerous[a.ID] = true
		}
	}
	for _, want := range []ID{"file.delete", "deploy.run"} {
		if !dangerous[want] {
			t.Errorf("%s should be marked destructive", want)
		}
	}
}
