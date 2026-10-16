// Package log provides omssh's structured logger and the redaction that keeps
// secrets out of log files.
//
// Redaction is a property of the type, not of the caller: a credential is a
// [Secret], and [Secret] cannot be formatted into anything but "[redacted]".
// Values that arrive as plain strings (a password typed into a prompt, a token
// found in a config file) are covered by the [Redactor], which rewrites the
// message and every attribute before the record reaches a handler.
package log

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// Secret is a credential. Formatting it can only ever produce "[redacted]".
type Secret string

const redacted = "[redacted]"

// String renders the secret without revealing it.
func (Secret) String() string { return redacted }

// GoString renders the secret without revealing it, for %#v.
func (Secret) GoString() string { return redacted }

// LogValue renders the secret without revealing it, for slog.
func (Secret) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// MarshalJSON renders the secret without revealing it, for encoding/json.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the underlying value. It exists so exactly one call site has to
// be found when auditing who can read a credential.
func (s Secret) Reveal() string { return string(s) }

// Redactor scrubs registered secret values out of anything logged.
type Redactor struct {
	mu     sync.RWMutex
	values []string
}

// NewRedactor returns an empty redactor. Sharing one across the process is the
// point: register a credential once, and it can no longer be logged anywhere.
func NewRedactor() *Redactor { return &Redactor{} }

// Add registers a literal value to scrub. Empty and very short values are
// ignored, because scrubbing every "a" would make logs useless.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) < 3 {
			continue
		}
		r.values = append(r.values, v)
	}
}

// Redact replaces every registered value with "[redacted]".
func (r *Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.values {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, redacted)
		}
	}
	return s
}

// IsEmpty reports whether anything has been registered, so callers can skip the
// work in the common case.
func (r *Redactor) IsEmpty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.values) == 0
}

// Options configures a logger.
type Options struct {
	Writer   io.Writer
	Level    slog.Level
	Redactor *Redactor
}

// New returns a text logger that scrubs registered secrets and renders
// [Secret] values as "[redacted]".
func New(opts Options) *slog.Logger {
	if opts.Writer == nil {
		opts.Writer = io.Discard
	}
	if opts.Redactor == nil {
		opts.Redactor = NewRedactor()
	}
	inner := slog.NewTextHandler(opts.Writer, &slog.HandlerOptions{Level: opts.Level})
	return slog.New(&redactingHandler{inner: inner, redactor: opts.Redactor})
}

type redactingHandler struct {
	inner    slog.Handler
	redactor *Redactor
}

func (h *redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	if h.redactor.IsEmpty() {
		return h.inner.Handle(ctx, rec)
	}
	clean := slog.NewRecord(rec.Time, rec.Level, h.redactor.Redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(redactAttr(a, h.redactor))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		clean = append(clean, redactAttr(a, h.redactor))
	}
	return &redactingHandler{inner: h.inner.WithAttrs(clean), redactor: h.redactor}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name), redactor: h.redactor}
}

func redactAttr(a slog.Attr, r *Redactor) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(r.Redact(a.Value.String()))
	case slog.KindGroup:
		group := a.Value.Group()
		clean := make([]slog.Attr, 0, len(group))
		for _, g := range group {
			clean = append(clean, redactAttr(g, r))
		}
		a.Value = slog.GroupValue(clean...)
	default:
	}
	return a
}
