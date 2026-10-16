# 0002 — Bubble Tea v2 / Lip Gloss v2, confined to `internal/tui`

- **Status:** accepted
- **Date:** 2026-10-16

## Context

The UI is the product, so it should use the best-maintained TUI stack for Go
rather than a hand-rolled renderer. The Charm v2 line (Bubble Tea v2.1.0,
Lip Gloss v2.0.6, Bubbles v2.2.1, `x/ansi`, `colorprofile`) is current, has
first-class mouse, colour-profile and layout support, and ships `x/vt`, a real
VT emulator we can use for the terminal tabs. It is also, necessarily, a
dependency that will see breaking changes over time.

## Decision

Build on the Charm v2 stack, and keep every import of it inside
`internal/tui`. The domain layer (`internal/app`) exposes state and operations
with plain Go types; the UI renders them. Pin exact versions in `go.mod`.

## Consequences

- `go.mod` churn from a v2 minor release touches one package tree, not the
  application logic. The CLI and the domain never break because the UI library
  changed a signature.
- The same domain code serves the TUI, the CLI progress bars and the tests, so
  behaviour cannot drift between front ends.
- Accessibility and terminal-compatibility work (colour depth, `NO_COLOR`,
  icons, resize) is centralised in the theme layer instead of per view.
- Cost: a small amount of translation code at the `tui` ↔ `app` boundary, and
  a rule that reviewers must enforce.
