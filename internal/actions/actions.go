// Package actions is omssh's single registry of user-facing actions.
//
// The command palette, the context menu and the pane action bar are three
// renderers over this one list, filtered by [Action.Enabled]. Adding a feature
// means adding an entry here; a key handler that exists on its own is a bug,
// because it would be invisible to everything the interface uses to teach
// itself. See ADR 0006.
package actions

import (
	"fmt"
	"sort"
	"strings"
)

// ID is a stable identifier, also used in `keys.overrides` in the config.
type ID string

// Category groups actions in the palette and the context menu.
type Category string

// Categories, in the order the palette shows them.
const (
	CatNavigation Category = "Navigation"
	CatSelection  Category = "Selection"
	CatFile       Category = "File"
	CatArchive    Category = "Archive"
	CatTransfer   Category = "Transfer"
	CatSession    Category = "Session"
	CatDeploy     Category = "Deploy"
	CatView       Category = "View"
	CatApp        Category = "App"
)

// Categories is the display order for grouped listings.
var Categories = []Category{
	CatNavigation, CatSelection, CatFile, CatArchive,
	CatTransfer, CatSession, CatDeploy, CatView, CatApp,
}

// Context is the slice of application state an action needs in order to know
// whether it applies. It deliberately holds plain values rather than the app
// itself, so the registry stays import-free and trivially testable.
type Context struct {
	// Focused is the index of the focused pane.
	Focused int
	// Panes is how many panes are visible (1 on narrow terminals).
	Panes int
	// Cursor is true when the focused pane has an entry under the cursor.
	Cursor bool
	// CursorIsDir is true when that entry is a directory.
	CursorIsDir bool
	// CursorIsArchive is true when that entry looks like an archive.
	CursorIsArchive bool
	// Selection is the number of selected entries in the focused pane.
	Selection int
	// Staged is the number of entries staged by a cut or copy.
	Staged int
	// Jobs is the number of transfers running or queued.
	Jobs int
	// Connected is true when the focused pane is a remote directory.
	Connected bool
	// Local is true when the focused pane is a local directory.
	Local bool
	// Terminal is true when a terminal tab has focus.
	Terminal bool
	// Overlay is the name of the open overlay ("palette", "menu", "dialog",
	// "help", "settings", "onboarding"), empty when none is open.
	Overlay string
	// Back is true when there is something for Esc to dismiss or go back from.
	Back bool
}

// Action is one thing the user can do.
type Action struct {
	ID       ID
	Label    string
	Category Category
	// Desc is a one-line explanation shown in the palette and the help screen.
	Desc string
	// Default is the primary key binding in the standard preset, "" if the
	// action is palette- and menu-only.
	Default string
	// Keys are further bindings for the same action (tabs, aliases).
	Keys []string
	// Danger marks destructive actions, which the palette colours and the menu
	// orders last.
	Danger bool
	// Enabled reports whether the action applies in the given context. Nil
	// means always.
	Enabled func(Context) bool
}

// Applicable reports whether the action applies in ctx.
func (a Action) Applicable(ctx Context) bool {
	if a.Enabled == nil {
		return true
	}
	return a.Enabled(ctx)
}

// Bindings returns every key bound to the action, primary first.
func (a Action) Bindings() []string {
	out := make([]string, 0, 1+len(a.Keys))
	if a.Default != "" {
		out = append(out, a.Default)
	}
	out = append(out, a.Keys...)
	return out
}

// Registry holds the actions and indexes them for lookup.
type Registry struct {
	order []Action
	byID  map[ID]Action
}

// New builds a registry. It panics on a duplicate ID, which can only happen
// while someone is editing the list in builtin.go.
func New(list ...Action) *Registry {
	r := &Registry{byID: make(map[ID]Action, len(list))}
	for _, a := range list {
		if _, dup := r.byID[a.ID]; dup {
			panic(fmt.Sprintf("actions: duplicate id %q", a.ID))
		}
		r.byID[a.ID] = a
		r.order = append(r.order, a)
	}
	return r
}

// Get returns the action with the given id.
func (r *Registry) Get(id ID) (Action, bool) {
	a, ok := r.byID[id]
	return a, ok
}

// All returns every action in declaration order.
func (r *Registry) All() []Action {
	out := make([]Action, len(r.order))
	copy(out, r.order)
	return out
}

// Applicable returns the actions that apply in ctx, grouped by category order
// and sorted by label inside each category.
func (r *Registry) Applicable(ctx Context) []Action {
	out := make([]Action, 0, len(r.order))
	for _, a := range r.order {
		if a.Applicable(ctx) {
			out = append(out, a)
		}
	}
	sortByCategory(out)
	return out
}

// Palette returns every action, grouped and sorted, for the command palette.
// The palette lists actions that do not apply yet in a muted state rather than
// hiding them, so the app remains browsable.
func (r *Registry) Palette() []Action {
	out := r.All()
	sortByCategory(out)
	return out
}

func sortByCategory(list []Action) {
	rank := make(map[Category]int, len(Categories))
	for i, c := range Categories {
		rank[c] = i
	}
	sort.SliceStable(list, func(i, j int) bool {
		ri, rj := rank[list[i].Category], rank[list[j].Category]
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(list[i].Label) < strings.ToLower(list[j].Label)
	})
}

// KeyIndex maps every bound key to its action id. It is built once, from the
// registry plus whatever preset and overrides the user chose, in package
// keymap.
type KeyIndex map[string]ID

// Index builds a KeyIndex from the registry's default bindings. Later keys win,
// so a caller can layer presets and overrides on top.
func (r *Registry) Index() (KeyIndex, error) {
	idx := make(KeyIndex, len(r.order))
	for _, a := range r.order {
		for _, k := range a.Bindings() {
			k = NormalizeKey(k)
			if k == "" {
				continue
			}
			if owner, taken := idx[k]; taken && owner != a.ID {
				return nil, fmt.Errorf("%s and %s both bound to %q", owner, a.ID, k)
			}
			idx[k] = a.ID
		}
	}
	return idx, nil
}

// Validate checks the invariants the palette-completeness test relies on.
func (r *Registry) Validate() error {
	if len(r.order) == 0 {
		return fmt.Errorf("actions: registry is empty")
	}
	seen := make(map[ID]bool, len(r.order))
	for _, a := range r.order {
		if a.ID == "" {
			return fmt.Errorf("actions: %q has no id", a.Label)
		}
		if seen[a.ID] {
			return fmt.Errorf("actions: duplicate id %q", a.ID)
		}
		seen[a.ID] = true
		if strings.TrimSpace(a.Label) == "" {
			return fmt.Errorf("actions: %s has no label", a.ID)
		}
		if strings.TrimSpace(a.Desc) == "" {
			return fmt.Errorf("actions: %s has no description", a.ID)
		}
		if !knownCategory(a.Category) {
			return fmt.Errorf("actions: %s has unknown category %q", a.ID, a.Category)
		}
	}
	return nil
}

func knownCategory(c Category) bool {
	for _, k := range Categories {
		if k == c {
			return true
		}
	}
	return false
}

// NormalizeKey tidies a binding so "Shift+Tab" and "shift+tab" are one key.
//
// Case is preserved for a bare single letter, because a terminal reports
// shift+a as "A": collapsing that to "a" would make n/N and g/G — which real
// bindings use — impossible to express. Modifier names are always lowercased,
// and a letter carrying modifiers is lowercased too, since the terminal reports
// the shifted form through the shift modifier instead.
func NormalizeKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	parts := strings.Split(k, "+")
	mods := make([]string, 0, len(parts))
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "control", "ctrl":
			p = "ctrl"
		case "option", "opt":
			p = "alt"
		default:
			p = strings.ToLower(p)
		}
		mods = append(mods, p)
	}
	order := []string{"ctrl", "alt", "shift", "meta", "hyper", "super"}
	sort.SliceStable(mods, func(i, j int) bool {
		return modRank(mods[i], order) < modRank(mods[j], order)
	})

	key := parts[len(parts)-1]
	switch strings.ToLower(key) {
	case "escape":
		key = "esc"
	case "delete":
		key = "del"
	case "return":
		key = "enter"
	}
	bare := len(mods) == 0
	if !bare || len([]rune(key)) != 1 {
		key = strings.ToLower(key)
	}
	out := make([]string, 0, len(mods)+1)
	out = append(out, mods...)
	out = append(out, key)
	return strings.Join(out, "+")
}

func modRank(m string, order []string) int {
	for i, o := range order {
		if m == o {
			return i
		}
	}
	return len(order)
}
