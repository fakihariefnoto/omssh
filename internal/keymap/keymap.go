// Package keymap turns the action registry into a resolved key map: the built-in
// bindings, the user's overrides, and the check that no two actions claim the same
// key on the same screen.
//
// Bindings are strings in Bubble Tea v2's notation ("ctrl+q", "alt+h", "space",
// "esc"). A binding may be a sequence, written with a space: "g g" means the
// prefix key followed by g.
package keymap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fakihariefnoto/omssh/internal/actions"
)

// Options is everything that shapes the resulting map.
type Options struct {
	// Overrides rebind actions by id.
	Overrides map[string]string
}

// Dropped records a binding that was requested but cannot be delivered.
type Dropped struct {
	Action actions.ID
	Key    string
	Reason string
}

// Map is the resolved, immutable binding table.
type Map struct {
	reg      *actions.Registry
	byID     map[actions.ID][]string
	index    actions.KeyIndex
	prefixes map[string]bool
	dropped  []Dropped
	deliver  Deliverer
}

// Build resolves the bindings for the given registry.
func Build(reg *actions.Registry, opts Options) (*Map, error) {
	return BuildWithDeliverer(reg, opts, DeliverEverything{})
}

// Deliverer reports whether the terminal can actually send a key. Keys that
// cannot be delivered are dropped with a reason and reported by Dropped(),
// rather than bound to something that silently does nothing.
type Deliverer interface {
	CanDeliver(key string) (reason string, ok bool)
}

// DeliverEverything assumes every key works, which is the default before the
// capability probe has run.
type DeliverEverything struct{}

// CanDeliver always reports success.
func (DeliverEverything) CanDeliver(string) (string, bool) { return "", true }

// Undeliverable lists keys that terminals reserve or mangle (UX.md §13).
var Undeliverable = map[string]string{
	"ctrl+h": "sends Backspace on many terminals; use '.' for hidden files",
	"ctrl+i": "is Tab (0x09); use tab",
	"ctrl+j": "is Enter (0x0a) on many terminals; use enter",
	"ctrl+m": "is Enter (0x0d); use enter",
	"ctrl+s": "is XOFF flow control in some setups; use alt+s",
	"ctrl+[": "is Esc (0x1b); use esc",
}

// BuildWithDeliverer resolves bindings and drops keys the terminal cannot send.
func BuildWithDeliverer(reg *actions.Registry, opts Options, d Deliverer) (*Map, error) {
	if d == nil {
		d = DeliverEverything{}
	}
	m := &Map{
		reg:      reg,
		byID:     map[actions.ID][]string{},
		prefixes: map[string]bool{},
		deliver:  d,
	}

	for _, a := range reg.All() {
		for _, k := range a.Bindings() {
			m.add(a.ID, normalizeBinding(k))
		}
	}

	// Overrides replace an action's bindings rather than adding to them: someone
	// who rebinds delete to "D" does not expect "d" to keep deleting things.
	ids := make([]string, 0, len(opts.Overrides))
	for id := range opts.Overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, raw := range ids {
		id := actions.ID(raw)
		if _, ok := reg.Get(id); !ok {
			return nil, fmt.Errorf("keymap: keys.overrides names unknown action %q", raw)
		}
		if id == "app.back" {
			return nil, fmt.Errorf("keymap: Esc cannot be rebound: it dismisses, cancels and goes back")
		}
		key := normalizeBinding(opts.Overrides[raw])
		if key == "" {
			return nil, fmt.Errorf("keymap: keys.overrides.%s is empty", raw)
		}
		if reason, ok := Undeliverable[key]; ok {
			return nil, fmt.Errorf("keymap: keys.overrides.%s uses %q, which %s", raw, key, reason)
		}
		m.byID[id] = nil
		m.add(id, key)
	}
	if err := m.reindex(); err != nil {
		return nil, err
	}
	return m, nil
}

// add records a binding for an action, replacing an earlier one for the same key.
func (m *Map) add(id actions.ID, key string) {
	if key == "" {
		return
	}
	for _, existing := range m.byID[id] {
		if existing == key {
			return
		}
	}
	m.byID[id] = append(m.byID[id], key)
}

// reindex rebuilds the key lookup and reports conflicts between actions on the
// same screen.
func (m *Map) reindex() error {
	// key -> screen -> action, so the same letter can serve two screens.
	scopes := map[string]map[string]actions.ID{}
	idx := actions.KeyIndex{}
	for _, a := range m.reg.All() {
		for _, k := range m.byID[a.ID] {
			if reason, undeliverable := Undeliverable[k]; undeliverable {
				m.dropped = append(m.dropped, Dropped{Action: a.ID, Key: k, Reason: reason})
				continue
			}
			if reason, ok := m.deliver.CanDeliver(k); !ok {
				m.dropped = append(m.dropped, Dropped{Action: a.ID, Key: k, Reason: reason})
				continue
			}
			if scopes[k] == nil {
				scopes[k] = map[string]actions.ID{}
			}
			if owner, taken := scopes[k][a.Screen]; taken && owner != a.ID {
				return fmt.Errorf("keymap: %s and %s are both bound to %q on the %q screen", owner, a.ID, k, a.Screen)
			}
			scopes[k][a.Screen] = a.ID
			idx[k] = append(idx[k], a.ID)
			if prefix, _, ok := splitSequence(k); ok {
				m.prefixes[prefix] = true
			}
		}
	}
	// Scoped bindings are tried first, so "d" on the connections screen reaches
	// the connection action before the file one.
	for k, ids := range idx {
		sort.SliceStable(ids, func(i, j int) bool {
			ai, _ := m.reg.Get(ids[i])
			aj, _ := m.reg.Get(ids[j])
			return ai.Screen != "" && aj.Screen == ""
		})
		idx[k] = ids
	}
	m.index = idx
	sort.Slice(m.dropped, func(i, j int) bool { return m.dropped[i].Key < m.dropped[j].Key })
	return nil
}

// Registry returns the action registry behind the map.
func (m *Map) Registry() *actions.Registry { return m.reg }

// Dropped returns the bindings that were discarded because the terminal cannot
// deliver them, so the UI can say so once instead of doing nothing.
func (m *Map) Dropped() []Dropped {
	out := make([]Dropped, len(m.dropped))
	copy(out, m.dropped)
	return out
}

// Keys returns every binding for an action, primary first.
func (m *Map) Keys(id actions.ID) []string {
	keys := m.byID[id]
	out := make([]string, len(keys))
	copy(out, keys)
	return out
}

// Primary returns the binding shown next to an action in the palette.
func (m *Map) Primary(id actions.ID) string {
	if keys := m.byID[id]; len(keys) > 0 {
		return display(keys[0])
	}
	return ""
}

// Lookup returns the first action a single key is bound to on a screen,
// ignoring sequences. An empty screen means "the global bindings only".
func (m *Map) Lookup(key, screen string) (actions.ID, bool) {
	for _, id := range m.index[normalizeBinding(key)] {
		a, ok := m.reg.Get(id)
		if !ok {
			continue
		}
		if a.Screen == "" || a.Screen == screen {
			return id, true
		}
	}
	return "", false
}

// ActionFor resolves a key to the first applicable action on a screen.
func (m *Map) ActionFor(key, screen string, ctx actions.Context) (actions.ID, bool) {
	for _, id := range m.index[normalizeBinding(key)] {
		a, ok := m.reg.Get(id)
		if !ok {
			continue
		}
		if a.Screen != "" && a.Screen != screen {
			continue
		}
		if !a.Applicable(ctx) {
			continue
		}
		return id, true
	}
	return "", false
}

// Outcome is the result of feeding one key to a Resolver.
type Outcome struct {
	// ID is the matched action, empty when nothing matched.
	ID actions.ID
	// Matched is true when a complete binding was recognised.
	Matched bool
	// Pending is true when the key started a sequence and more keys are needed.
	Pending bool
}

// Resolver matches keys, holding the half-typed state of a key sequence.
type Resolver struct {
	m       *Map
	screen  string
	pending string
}

// Resolver returns a stateful matcher for this map on a screen.
func (m *Map) Resolver(screen string) *Resolver {
	return &Resolver{m: m, screen: screen}
}

// SetScreen moves the resolver to another screen, dropping any half-typed
// sequence: "g" on one screen is not a prefix on another.
func (r *Resolver) SetScreen(screen string) {
	if r.screen != screen {
		r.screen = screen
		r.pending = ""
	}
}

// Pending reports the half-typed sequence, for the status bar.
func (r *Resolver) Pending() string { return display(r.pending) }

// Reset drops any half-typed sequence.
func (r *Resolver) Reset() { r.pending = "" }

// Feed matches one key and returns what it meant.
func (r *Resolver) Feed(key string) Outcome {
	key = normalizeBinding(key)
	if key == "" {
		return Outcome{}
	}
	seq := key
	if r.pending != "" {
		seq = r.pending + " " + key
	}
	if ids := r.m.idsFor(seq, r.screen); len(ids) > 0 {
		r.pending = ""
		return Outcome{ID: ids[0], Matched: true}
	}
	if r.m.prefixes[seq] {
		r.pending = seq
		return Outcome{Pending: true}
	}
	if r.pending != "" {
		// The pending prefix led nowhere; treat this key as a fresh start.
		r.pending = ""
		return r.Feed(key)
	}
	return Outcome{}
}

// idsFor returns the actions a binding names on a screen, scoped first.
func (m *Map) idsFor(binding, screen string) []actions.ID {
	out := make([]actions.ID, 0, 2)
	for _, id := range m.index[binding] {
		a, ok := m.reg.Get(id)
		if !ok {
			continue
		}
		if a.Screen == "" || a.Screen == screen {
			out = append(out, id)
		}
	}
	return out
}

// Validate reports problems with a set of overrides without building a map.
func Validate(reg *actions.Registry, opts Options) error {
	_, err := Build(reg, opts)
	return err
}

// splitSequence splits "g g" into its prefix and final key.
func splitSequence(binding string) (prefix, rest string, ok bool) {
	if i := strings.Index(binding, " "); i >= 0 {
		return binding[:i], binding[i+1:], true
	}
	return binding, "", false
}

func normalizeBinding(binding string) string {
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return ""
	}
	parts := strings.Split(binding, " ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if n := actions.NormalizeKey(p); n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}

// display renders a binding the way the user should press it.
func display(binding string) string {
	parts := strings.Split(binding, " ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, titleKey(p))
	}
	return strings.Join(out, " ")
}

func titleKey(key string) string {
	parts := strings.Split(key, "+")
	hasMods := len(parts) > 1
	for i, p := range parts {
		switch p {
		case "ctrl":
			parts[i] = "Ctrl"
			continue
		case "alt":
			parts[i] = "Alt"
			continue
		case "shift":
			parts[i] = "Shift"
			continue
		case "esc", "Esc":
			parts[i] = "Esc"
			continue
		case "enter":
			parts[i] = "Enter"
			continue
		case "tab":
			parts[i] = "Tab"
			continue
		case "space":
			parts[i] = "Space"
			continue
		case "up", "down", "left", "right", "home", "end", "pgup", "pgdown", "del":
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
			continue
		}
		// A bare letter keeps the case it was written with ("n" and "N" are
		// different keys); a letter with a modifier is shown as a keycap.
		if len([]rune(p)) == 1 && !hasMods {
			continue
		}
		parts[i] = strings.ToUpper(p)
	}
	return strings.Join(parts, "+")
}
