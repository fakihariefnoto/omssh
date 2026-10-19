// Package keymap turns the action registry into a resolved key map: default
// bindings, a navigation preset, the user's overrides, and the check that no
// two actions claim the same key.
//
// Bindings are strings in Bubble Tea v2's notation ("ctrl+q", "alt+h", "f5",
// "shift+up", "space", "esc"). A binding may be a sequence, written with a
// space: "ctrl+space n" means the prefix key followed by n.
package keymap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fakihariefnoto/omssh/internal/actions"
)

// Preset selects the navigation flavour.
type Preset string

// Presets. The vim preset changes navigation and selection only: operations keep
// the standard bindings, so a shortcut always means exactly one thing.
const (
	Standard Preset = "standard"
	Vim      Preset = "vim"
)

// Options is everything that shapes the resulting map.
type Options struct {
	Preset    Preset
	Overrides map[string]string // action id -> binding
}

// Dropped records a binding that was requested but cannot be delivered.
type Dropped struct {
	Action actions.ID
	Key    string
	Reason string
}

// Map is the resolved, immutable binding table.
type Map struct {
	preset    Preset
	reg       *actions.Registry
	byID      map[actions.ID][]string
	index     actions.KeyIndex
	prefixes  map[string]bool
	dropped   []Dropped
	deliverer Deliverer
}

// Build resolves the bindings for the given registry.
//
// Order of precedence, lowest first: built-in defaults, the preset's aliases,
// then the user's overrides. A later layer replaces an earlier binding for the
// same action, and a key claimed by two different actions is an error.
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

// Undeliverable lists keys that terminals reserve or mangle, from UX.md §13.
var Undeliverable = map[string]string{
	"ctrl+h": "sends Backspace on many terminals; use alt+h",
	"ctrl+i": "is Tab (0x09); use tab",
	"ctrl+j": "is Enter (0x0a) on many terminals; use alt+j",
	"ctrl+m": "is Enter (0x0d); use enter",
	"ctrl+s": "is XOFF flow control in some setups; use alt+s",
	"ctrl+[": "is Esc (0x1b); use esc",
}

func (m *Map) presetAliases() map[actions.ID][]string {
	if m.preset != Vim {
		return nil
	}
	// Esc is one key with layered meaning (dismiss, clear, go back), so the
	// registry binds it once, to app.back. Clearing a selection is what app.back
	// does when there is a selection; sel.clear exists for the palette and the
	// context menu.
	return map[actions.ID][]string{
		"nav.parent":      {"h"},
		"nav.down":        {"j"},
		"nav.up":          {"k"},
		"nav.open":        {"l"},
		"nav.first":       {"g g"},
		"nav.last":        {"G"},
		"nav.home":        {"~"},
		"nav.last-dir":    {"-"},
		"nav.filter":      {"/"},
		"nav.filter-next": {"n"},
		"nav.filter-prev": {"N"},
		// Ctrl+D is delete now, so the vim preset keeps only "v" for a range.
		// Half-page scrolling is one page key away and does not deserve a
		// collision with a destructive action.
		"sel.extend-down": {"v"},
	}
}

// BuildWithDeliverer resolves bindings and drops keys the terminal cannot send.
func BuildWithDeliverer(reg *actions.Registry, opts Options, d Deliverer) (*Map, error) {
	if d == nil {
		d = DeliverEverything{}
	}
	preset := opts.Preset
	if preset == "" {
		preset = Standard
	}
	if preset != Standard && preset != Vim {
		return nil, fmt.Errorf("keymap: unknown preset %q, want %q or %q", preset, Standard, Vim)
	}

	m := &Map{
		preset:    preset,
		reg:       reg,
		byID:      map[actions.ID][]string{},
		index:     actions.KeyIndex{},
		prefixes:  map[string]bool{},
		deliverer: d,
	}

	// Layer 1: defaults, in declaration order.
	for _, a := range reg.All() {
		for _, k := range a.Bindings() {
			m.add(a.ID, normalizeBinding(k))
		}
	}
	// Layer 2: preset aliases.
	for id, keys := range m.presetAliases() {
		if _, ok := reg.Get(id); !ok {
			return nil, fmt.Errorf("keymap: preset %s binds unknown action %q", preset, id)
		}
		for _, k := range keys {
			m.add(id, normalizeBinding(k))
		}
	}
	// Layer 3: the user's overrides.
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
		// An override replaces the action's bindings rather than adding to
		// them: someone who rebinds Delete to Ctrl+D does not expect F8 to keep
		// deleting things behind their back.
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

// reindex rebuilds the key lookup and reports conflicts between actions.
func (m *Map) reindex() error {
	idx := actions.KeyIndex{}
	for _, a := range m.reg.All() {
		keys := m.byID[a.ID]
		for _, k := range keys {
			if reason, undeliverable := Undeliverable[k]; undeliverable {
				m.dropped = append(m.dropped, Dropped{Action: a.ID, Key: k, Reason: reason})
				continue
			}
			if reason, ok := m.deliverer.CanDeliver(k); !ok {
				m.dropped = append(m.dropped, Dropped{Action: a.ID, Key: k, Reason: reason})
				continue
			}
			if owner, taken := idx[k]; taken && owner != a.ID {
				return fmt.Errorf("keymap: %s and %s are both bound to %q", owner, a.ID, k)
			}
			idx[k] = a.ID
			if prefix, _, ok := splitSequence(k); ok {
				m.prefixes[prefix] = true
			}
		}
	}
	m.index = idx
	sort.Slice(m.dropped, func(i, j int) bool { return m.dropped[i].Key < m.dropped[j].Key })
	return nil
}

// Preset returns the preset the map was built with.
func (m *Map) Preset() Preset { return m.preset }

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

// Lookup returns the action a single key is bound to, ignoring sequences.
func (m *Map) Lookup(key string) (actions.ID, bool) {
	id, ok := m.index[normalizeBinding(key)]
	return id, ok
}

// ActionFor resolves a key to the first applicable action, preferring the
// focused context. It is the convenience path for the UI; Resolver is the
// sequence-aware one.
func (m *Map) ActionFor(key string, ctx actions.Context) (actions.ID, bool) {
	id, ok := m.Lookup(key)
	if !ok {
		return "", false
	}
	a, ok := m.reg.Get(id)
	if !ok || !a.Applicable(ctx) {
		return "", false
	}
	return id, true
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
	pending string
}

// Resolver returns a stateful matcher for this map.
func (m *Map) Resolver() *Resolver { return &Resolver{m: m} }

// Pending reports the half-typed sequence, for the status bar.
func (r *Resolver) Pending() string { return display(r.pending) }

// Reset drops any half-typed sequence. It is called on Esc and when focus moves.
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
	if id, ok := r.m.index[seq]; ok {
		r.pending = ""
		return Outcome{ID: id, Matched: true}
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

// Validate reports problems with a set of overrides without building a map,
// for `omssh config check`.
func Validate(reg *actions.Registry, opts Options) error {
	_, err := Build(reg, opts)
	return err
}

// splitSequence splits "ctrl+space n" into its prefix and final key.
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
		// A bare letter keeps the case it was written with ("n" and "G" are
		// different keys); a letter with a modifier is shown as a keycap.
		if len([]rune(p)) == 1 && !hasMods {
			continue
		}
		parts[i] = strings.ToUpper(p)
	}
	return strings.Join(parts, "+")
}
