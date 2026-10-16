# omssh — Roadmap

Seven milestones. Each ends with a binary that runs and does something real;
none is a private refactor milestone with nothing to show.

Ordering rationale: connections and the unified filesystem are prerequisites
for everything else, so they come first. The file manager is the product, so it
comes before deploy and terminal. Archive support rides on the file manager
because it shares the copy engine. Deploy is the last *feature* because it
depends on transfers, archives (for tarball deploys) and the connection pool
all being solid.

---

## M0 — Skeleton and visual identity

**Goal:** the app launches, looks like the product, and does nothing yet.

- `go.mod` with module `github.com/fakihariefnoto/omssh`, package layout from
  `ARCHITECTURE.md`.
- Config loading (strict YAML via `goccy/go-yaml`) and XDG paths, with
  `omssh config path|edit|check` reporting unknown and duplicate keys by line.
- Lip Gloss v2 theme engine with `midnight`/`dawn`/`mono`, colour-profile
  negotiation, `NO_COLOR`.
- `internal/actions` registry and `internal/keymap` presets, so the palette,
  context menu and action bar are renderers over one list from the start.
- App shell: session tab bar, sidebar, pane frame, **contextual action bar**,
  status bar, toast system, command palette (showing shortcuts), `F9` context
  menu, first-run onboarding card, responsive breakpoints.
- Terminal capability probe: detect undeliverable keys and drop them rather
  than binding something that does nothing.
- Logging with redaction, panic handler that restores the terminal.
- CI: build, `go vet`, `golangci-lint`, `go test -race`, gofmt check, plus a
  test asserting every registered action is reachable from the palette. Makefile
  targets working. `goreleaser` config checked in but not yet releasing.

**Acceptance:** `make run` shows the UI from `UX.md` §2 at 140 and 100 cols
without layout breakage; theme switch changes everything; `F1` shows the
searchable keymap; `Ctrl+K` finds a stub action and displays its shortcut;
`omssh keys --show` prints the effective map; with nothing selected, `Ctrl+C`
shows the `Ctrl+Q quits` hint rather than doing nothing; `Esc` from every screen
returns one level and the terminal is usable on exit.

---

## M1 — Connections

**Goal:** connect to a real host and see it is alive.

- `~/.ssh/config` parser (with `Include` and wildcards) and written-back
  metadata as OpenSSH-safe comments.
- Auth chain: explicit key → agent → keyring password; keyboard-interactive.
- `known_hosts` verification with a TOFU fingerprint dialog, `strict` mode.
- `ProxyJump` chains.
- Connection pool with refcounts, keepalive, reconnect with backoff, and state
  surfaced in the UI.
- Host list, add/edit/delete forms, quick connect, `omssh hosts list`.

**Acceptance:** connect to two hosts (one behind a jump host), see live state
dots, close and reopen tabs without re-authenticating, `omssh exec host -- uname -a`
prints remote output, wrong host key on a second connect is a hard stop with a
clear message.

---

## M2 — Unified filesystem and the file manager

**Goal:** browse local and remote directories with the walking experience from
`UX.md`.

- `vfs.FS` interface, `Local`, `SFTP` and `Mem` implementations.
- `vfstest` conformance suite; all three implementations pass.
- Dual-pane file manager: listings, columns, sorting, hidden toggle, async
  fetch with cancellation and per-directory cache.
- Navigation: history (`Alt+←/→`), breadcrumb with sibling dropdowns, editable
  path bar (`Ctrl+L`), fuzzy jump (`Ctrl+P`), type-ahead, incremental filter
  (`Ctrl+F`), recursive find (`Alt+F`).
- Selection: `Space`, `Shift+arrows`, select all, invert; survives navigation;
  count and size always in the status bar.
- Operations: mkdir, new file, rename, delete, chmod, symlink — the standard
  set (`F2`/`F3`/`F4`/`F5`/`F6`/`F7`/`F8`, `Ctrl+C/X/V`), plus the `F9` context
  menu and the action bar listing the verbs for the current selection.
- `internal/trash`: delete → trash (XDG locally, `.omssh-trash/` remotely) with
  `Ctrl+Z` undo for delete, rename and move; explicit permanent-delete path when
  a host has no writable home. `omssh trash list|restore|empty`.
- Preview pane: syntax-highlighted text, hex, archive listing, dir summary.
- Sync browse (`Alt+S`), reveal in other pane (`Alt+O`), `Send →` / `Fetch ←`
  quick actions, drag between panes to copy, new-tab-at-this-directory
  placeholder.
- Bookmarks, and teaching empty/loading/error states with the fixing action.

**Acceptance:** a 5,000-file remote directory lists in under a second and stays
responsive while scrolling; every key in `UX.md` §5 and §7.1 works; the cursor
is restored on returning to a directory; a permission-denied directory shows an
explanatory row with actions instead of an error screen; a new user can copy a
file to the server and undo the delete of another using only the action bar,
the context menu or the palette — without reading `UX.md`.

---

## M3 — Transfers

**Goal:** move files in all four directions comfortably, many at a time.

- `xfer.Engine`: planning, worker pool, per-item and aggregate progress, speed
  and ETA, throttled progress events.
- Copy / move in every direction including remote→remote, with the `F5`/`F6`
  dialog whose destination defaults to the other pane.
- Quick path (`Ctrl+C` → `Tab` → `Ctrl+V`) with a staged-selection indicator.
- Conflict policies with the conflict dialog (reversible choice first) and
  apply-to-all.
- Inline confirmation line for bulk or destructive operations; operations under
  the threshold start immediately with an undoable toast.
- Pause / resume / cancel / retry; `.part` resume for large files.
- Fast paths: SFTP rename, `copy-data`, server-side `cp -a` — in that order.
- Transfer queue panel with item detail.
- Preserve mode bits and mtimes; atomic local writes.
- Clipboard yank as a path list.

**Acceptance:** select 200 files and copy remote→local with 4 workers, watching
correct aggregate progress; cancel mid-way and confirm no partial files remain;
kill the connection during a 1 GB transfer, reconnect, retry, and confirm it
resumes rather than restarting; remote→remote copy of a directory tree works
without a local temp file.

---

## M4 — Archives

**Goal:** extract and create archives across sides, safely.

- Format detection (extension + magic bytes).
- Readers: zip, tar, tar+gz, tar+zst, tar+bz2, tar+xz, and single-file
  gz/zst/xz/bz2.
- Writers: zip, tar, tar+gz, tar+zst, tar+xz.
- `archive.Extract(ctx, dst vfs.FS, dir, src io.Reader, Limits)` used by the
  UI, the CLI (`omssh extract`) and the deploy runner.
- `safeJoin` with the attack test corpus (zip-slip, absolute paths, `..`,
  symlink escape, bombs, oversize entries, permission escalation).
- Extract-to dialog with destination filter, destination **side** (local / this
  host / other host), strip components, "new folder named after the archive",
  and the safety summary.
- `Enter`/`F3` on an archive lists its entries in the preview, so the feature is
  discoverable without knowing a key.
- Compress selection (`Ctrl+G`) to either side; `Ctrl+E` extracts.
- `omssh extract` / `omssh compress` CLI commands with `--json`.

**Acceptance:** extract a remote `.tar.gz` into a chosen local directory and a
local `.zip` onto the remote, both with live per-entry progress; a crafted
zip-slip archive is refused with a precise message and nothing is written
outside the destination; a 40 GB-expanding bomb is stopped by the ratio/size
cap; a single-file `.gz` extracts to one file with the right name; a user who
has never extracted an archive finds `Ctrl+E Extract` in the action bar with an
archive selected.

---

## M5 — Interactive terminal

**Goal:** a real shell in a tab, good enough for `vim` and `htop`.

- PTY sessions with resize propagation, over the connection pool.
- `charmbracelet/x/vt` rendering into a Bubble Tea view, 10k-line scrollback
  ring buffer, and a resize-safe re-render.
- Prefix-key command surface (`Ctrl+Space`) so remote keys pass through.
- Multiple terminal tabs per host, opened at a directory, per-tab lifecycle.
- Copy mode with selection and OSC 52 clipboard.
- Alt-screen, bracketed paste, mouse passthrough.
- Escape-sequence conformance corpus with golden expectations.
- Fallback: `omssh exec <host>` hands the real terminal to a raw PTY session,
  bypassing the emulator, for rescue use.

**Acceptance:** `vim` opens, edits, saves and quits correctly; `htop` renders
and responds to keys; `Ctrl+C` interrupts a remote process; resizing the
terminal mid-`less` reflows correctly; scrollback holds and searches.

---

## M6 — Deployment

**Goal:** `Alt+D`, preview, run, done — repeatably.

- Profile schema, parsing, validation, and the add/edit forms.
- Entry points that are all discoverable: `Alt+D`, a `Deploy` button in the
  session header, the tab's right-click menu, the palette and the action bar.
- `deploy.Plan`: walk the local tree and the remote tree, apply excludes
  (gitignore syntax) and the mode (`sync` vs `update`), resolve actions.
- Dry-run rendered first by default, with per-action groups and a summary.
- Danger guard: a root-ish `remote_root` (`/`, `/etc`, `$HOME`, a mount point)
  requires typing the path to confirm.
- Runner: execute via `xfer`, atomic writes, optional backups, live log with
  follow, per-hook streaming output and exit codes.
- Hooks: `local_pre`, `local_post`, `remote_pre`, `remote_post`, `on_failure`,
  with template variables and defined working directories.
- Run history with records, logs on disk, re-run, and status
  (`success` / `deployed but unhealthy` / `failed`).
- Comparison mode (diff without running) via `Alt+C` and the palette.
- `omssh deploy <profile> [--dry-run] [--yes]`.

**Acceptance:** a profile with a `local_pre` build, a sync with excludes and one
deletion, and a `remote_post` health check runs green end to end; the same
profile with a failing health check is recorded as deployed-but-unhealthy and
is visibly different in history; `--dry-run` lists exactly what a run then
does; a mid-run disconnection leaves no half-written files in the target;
setting `remote_root = "/"` cannot be run without typing the path.

---

## M7 — Polish and release

**Goal:** ship it.

- Remaining themes (`mist`, `ember`, `contrast`), full icon fallback audit,
  mouse coverage across every view, `motion = "reduced"`.
- Settings screen; `omssh config check` with line numbers and keymap validation;
  `omssh keys --show`; example config; `Ctrl+K` → `Tour`.
- Accessibility pass: verify no state is carried by colour alone, verify the
  `contrast` theme and `--no-icons` at 16 colours, and audit every failure
  message for "what happened / why / what to do".
- Discoverability audit: every action reachable from the palette, and the
  palette/keymap test extended so this cannot regress.
- Performance pass: cold start under 150 ms, listing of 10k entries smooth,
  memory ceiling under 100 MB during a large transfer; pprof-driven.
- Cross-host copy UX (source and destination on different remotes).
- Documentation: README with real screenshots/recording, man page, shell
  completions, `docs/` reviewed against the shipped behaviour.
- Release engineering: goreleaser archives and checksums for
  `darwin/linux/windows` × `amd64/arm64`, Homebrew tap, tagged `v1.0.0`.
- Optional, only if the above lands early: port-forwarding manager
  (`-L`/`-R`/`-D`) — the first post-1.0 feature.

**Acceptance:** `brew install` then `omssh` on a clean macOS and a clean Linux
box connects to a host and moves files with no additional tooling; the recorded
demo covers connect → browse → copy → extract → deploy → terminal; a fresh user
reaches each of those in the demo **without being shown a shortcut**, using the
palette, the action bar or the mouse.

---

## Dependency note

M3 depends on M2 (panes and `vfs`). M4 depends on M3 (the same stream
primitives). M6 depends on M3 and M4 (sync plus tarball handles). M5 is
independent of M3/M4 and could be built in parallel by a second pair of hands
after M1.

## Definition of done (every milestone)

1. `make check` passes: gofmt, `go vet`, `golangci-lint`, `go test -race`.
2. New public behaviour has tests, including at least one failure path.
3. Every new user-facing action is added to `internal/actions` (palette,
   context menu and action bar pick it up automatically) — a stray key handler
   is a review failure.
4. Any new destructive action has a confirm, an undo, or a documented reason it
   cannot.
5. `docs/` updated where behaviour differs from what is written here.
6. Conventional commits, milestone tagged `v0.<n>.0`.
7. The binary from that milestone is genuinely usable for what it covers —
   no "coming soon" dialogs in shipped screens.
