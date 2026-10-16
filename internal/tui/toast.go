package tui

import (
	"time"
)

// toastKind colours a notification and, more importantly, tells the screen
// reader in all of us whether something worked.
type toastKind int

const (
	toastInfo toastKind = iota
	toastSuccess
	toastWarning
	toastError
)

// toastLifetime is how long a notification stays on screen. Four seconds is the
// documented behaviour in UX.md §9.
const toastLifetime = 4 * time.Second

type toast struct {
	kind    toastKind
	text    string
	seq     int
	expires time.Time
}

// maxToasts keeps the corner readable when several things finish at once.
const maxToasts = 3

func (m *Model) pushToast(kind toastKind, text string) {
	if text == "" {
		return
	}
	m.toastSeq++
	m.toasts = append(m.toasts, toast{
		kind:    kind,
		text:    text,
		seq:     m.toastSeq,
		expires: time.Now().Add(toastLifetime),
	})
	if len(m.toasts) > maxToasts {
		m.toasts = m.toasts[len(m.toasts)-maxToasts:]
	}
}

// toastInfo reports something that happened, without judgement.
func (m *Model) toastInfo(text string) { m.pushToast(toastInfo, text) }

// toastSuccess is used when an operation completed.
func (m *Model) toastSuccess(text string) { m.pushToast(toastSuccess, text) }

// toastWarning is used for things that worked but deserve a look.
func (m *Model) toastWarning(text string) { m.pushToast(toastWarning, text) }

// toastError is used for failures, and always says what to do next.
func (m *Model) toastError(text string) { m.pushToast(toastError, text) }

// expireToasts drops notifications whose time is up. It is driven by a heartbeat
// from Init rather than a timer per toast, so a burst of completions cannot
// leave a goroutine behind.
func (m *Model) expireToasts() {
	if len(m.toasts) == 0 {
		return
	}
	now := time.Now()
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if t.expires.After(now) {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
}

// dismissToast removes the most recent notification, so Esc can clear the corner.
func (m *Model) dismissToast() bool {
	if len(m.toasts) == 0 {
		return false
	}
	m.toasts = m.toasts[:len(m.toasts)-1]
	return true
}
