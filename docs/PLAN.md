# omssh — Product Plan

**Status:** planning · **Target:** v1.0 · **Last updated:** 2026-10-16

## 1. Summary

`omssh` is a terminal UI SSH/SFTP client written in Go. It is aimed at people
who spend their day moving between servers and strongly dislike typing `scp`,
`rsync`, `tar -xzf` and remembering remote paths.

The reference point is [`ssh-x-term`](https://github.com/eugeniofciuvasile/ssh-x-term),
which proves the "many SSH sessions in one TUI" idea works. `omssh` keeps that
core and rebuilds the product around **file work**: the file manager is the
centre of the app, not a side screen, and every file operation works
identically on local paths, remote paths, and pairs of the two.

The tagline we design against:

> Move files between any two machines without thinking about which side is
> which.

## 2. Goals

| # | Goal | How we judge it |
| --- | --- | --- |
| G1 | Manage many SSH/SFTP connections in one window | A user can hold 10+ live sessions across 4 hosts and never lose which is which |
| G2 | Copy remote→local, local→remote, remote→remote, and local→local | One key, one dialog; no `scp` syntax, no path quoting, no temp dirs by hand |
| G3 | Copy many files at once | Space-toggle and visual-range multi-select; parallel transfers with a per-item queue |
| G4 | Extract an archive into a chosen directory on either side | Remote `.tar.gz` → local dir, local `.zip` → remote dir, in-app, streamed |
| G5 | Deploy to a specific remote reliably | Save a profile, preview the diff, run it, read the log, inspect history |
| G6 | Best-in-class folder walking | Fuzzy path jump, bookmarks, incremental filter, preview, history, no dead ends |
| G7 | Beautiful, modern, keyboard-first UI | Command palette, themes, mouse support, no layout breakage at any width |
| G8 | One static binary, cross-platform | macOS/Linux/Windows, no `ssh`, `scp`, `tar` or `unzip` required on either end |
| G9 | Intuitive without a manual | A new user can connect, copy to the server and undo a mistake using only what the screen shows: the action bar, the palette, the context menu and standard shortcuts |
| G10 | Safe by default | Nothing irreversible happens without a deliberate confirm, and delete is undoable via trash |

## 3. Personas

- **Sam, solo dev.** One VPS, a couple of projects. Wants to upload a folder,
  run a restart command, and read the logs. Will never read a manual page.
- **Priya, ops.** Twelve hosts, jump boxes, shared deploy targets. Wants
  multiple sessions side by side, consistent keybindings, and no surprise
  overwrites.
- **Kenji, data wrangler.** Pulls large logs off remote machines and unpacks
  archives in both directions several times a day. Cares about progress,
  resume, and not filling the disk with copies.

## 4. Features

### 4.1 Connection management

- Import from `~/.ssh/config`, including `Include`, wildcards, and `Host *`
  defaults. Changes written back as structured comments that OpenSSH ignores,
  so the file stays valid for `ssh(1)`.
- Auth in order: explicit key → `ssh-agent` → password/key passphrase from the
  OS keyring. Interactive keyboard-interactive (2FA prompts) supported.
- Host key verification against `known_hosts`, with a TOFU prompt showing the
  SHA256 fingerprint and `strict` / `accept-new` / `insecure` modes.
- `ProxyJump` / `ProxyCommand` chains, agent forwarding, custom `Port` and
  `User`, per-host environment and `SetEnv`.
- A connection pool: one `ssh.Client` per (host, credentials) pair, many
  channels on top; SFTP subsystem per needing-tab; keepalive every 30s and
  reconnect with exponential backoff, surfacing state in the UI
  (`connecting` → `ready` → `busy` → `degraded` → `error`).
- Sessions survive navigation: switching tabs, opening the file manager or
  running a deploy all reuse the same authenticated connection.

### 4.2 File manager (dual pane)

- Two panes, each bound to `local` or `host:path`. Left is local and right is
  remote by default, with the direction spelled out in the UI as **`Send →`**
  and **`Fetch ←`** rather than "copy". Either pane can be remote: a
  `remote ↔ remote` layout is a normal configuration, not a special mode.
- A **contextual action bar** under the active pane lists the verbs that apply
  to the current selection, with their keys, and every one is clickable; plus a
  `F9` context menu and a `Ctrl+K` command palette as discovery nets. Nothing is
  reachable only by memorising a key.
- Directory listing with the columns that matter (perms, size, mtime, owner),
  sortable, with hidden-file toggle and a live byte total of the selection.
- **Type-ahead navigation**: start typing and the cursor jumps to the matching
  name, with repeat-to-cycle — classic file-manager behaviour, no key to learn.
  Backed by an incremental filter (`Ctrl+F`) and a recursive find (`Alt+F`) for
  the cases type-ahead cannot reach.
- Multi-select: `space` to toggle, `Shift+arrows` to extend a run (like every
  GUI list), `Ctrl+A` all, `Alt+A` invert. Selection survives navigation and its
  count and total size are always in the status bar.
- **Undo and trash**: deletes move to a trash (XDG trash locally, an
  `.omssh-trash/<timestamp>/` directory on the remote so the move stays on the
  same filesystem and is instant). `Ctrl+Z` undoes delete, rename, move and
  overwrite; when a host has no writable home the dialog says so and requires an
  explicit permanent-delete confirmation. Retention and restore are available
  from `omssh trash` and the queue's Trash tab.
- Preview pane: syntax-highlighted text (Chroma), hex for binaries, image
  preview where the terminal supports graphics, archive content listing,
  directory tree summary.
- Symlinks handled honestly: shown, followed on `enter`, never followed during
  recursive operations unless asked.
- Async listing with per-directory cache and cancellation, so a slow remote
  never blocks typing.
- **Sync browse** (`Alt+S`): panes mirror the same relative path, so
  `local/src/` face `remote/app/src/` while you move through both.
- **New tab at this directory** (`Ctrl+T`): a terminal tab already `cd`'d into
  the highlighted remote directory.
- Edit remote files: download to a temp file, open `$EDITOR`, upload on change
  (mtime + size check before overwrite).

### 4.3 Transfers

- Operations: copy (up/down/sideways), move, delete, rename, mkdir, touch,
  chmod, symlink. Each works on the selection, not just the cursor line.
- A transfer engine with configurable parallelism (default 4 file workers,
  chunked single-stream per large file), per-item progress, aggregate progress,
  speed and ETA.
- Pause / resume / cancel / retry per item and for the whole queue. Large files
  resume from a `.part` offset rather than restarting.
- Conflict policy: ask, overwrite, keep-both (rename), skip, update-if-newer
  (size+mtime), or compare checksum. Choices can be applied to all remaining
  items, and the conflict dialog offers the reversible choice first.
- Two speeds for every operation: the quick path (`Ctrl+C`, `Tab`, `Ctrl+V`) and
  the guided path (`F5`/`F6` dialogs whose destination defaults to the other
  pane). Bulk or destructive operations confirm on one inline line rather than
  with a modal: `Copy 200 files → prod-api:/srv/api · Enter to start · Esc to
  cancel`.
- Undoable: `Ctrl+Z` reverses a move, rename or overwrite; deletes go to trash
  (§4.2) rather than being permanent.
- Preserve mode bits and mtimes; optional `fsync`-on-close for local writes.
- **Server-side copy**: when source and destination are on the same remote,
  try `cp -a` (or SFTP `copy-data` extension) first and only stream bytes if
  that fails.
- Remote→remote across different hosts streams through the client with
  bounded buffers; never writes a temp file locally.
- Clipboard integration: yank selection as a newline-separated path list to the
  system clipboard over OSC 52.

### 4.4 Archives

One engine, used everywhere, driven by the same two primitives as transfers
(an archive reader and a destination writer).

Supported for **reading (extract)**:

| Format | Extension / sniff | Implementation |
| --- | --- | --- |
| zip | `.zip` | `archive/zip` (incl. zip64) |
| tar | `.tar` | `archive/tar` |
| tar+gzip | `.tar.gz`, `.tgz` | `archive/tar` + `compress/gzip` |
| tar+zstd | `.tar.zst`, `.tzst` | `klauspost/compress/zstd` |
| tar+bzip2 | `.tar.bz2`, `.tbz2` | `compress/bzip2` |
| tar+xz | `.tar.xz`, `.txz` | `ulikunitz/xz` |
| gzip / zstd / xz / bzip2 single-file | `.gz`, `.zst`, `.xz`, `.bz2` | streamed single output file |
| 7z / rar | optional | only if a configured external tool exists; never required |

Writing (compress) supports zip, tar and tar+gz/zst/xz/bz2.

Places an archive can be read from and extracted to — all four combinations,
plus remote→remote:

```
source \ dest   local dir        remote dir
local archive   copy/extract     upload+extract (streamed)
remote archive  download+extract streamed to remote
```

- Destinations: same directory, a new directory named after the archive,
  or a picked directory (the same directory picker used by transfers). The
  destination **side** (local / this host / other host) is an explicit choice in
  the dialog, so both cross-machine directions are one screen.
- Pressing `Enter`/`F3` on an archive lists its contents without extracting,
  which is how most users will discover the feature.
- Live entry list with per-entry progress, an overall bar, and a "skip errors"
  policy.
- Compress: select files/dirs in a pane → `Ctrl+G` → choose format → the archive
  is created on that same side (or the other side, by choosing the destination).

Hard requirements (see §7 Security) — this is untrusted input:

- Reject absolute paths, `..` escapes, drive-relative names and names that
  normalise outside the destination root (**zip-slip**).
- Reject or quarantine symlinks/hardlinks that point outside the destination.
- Enforce limits: max total uncompressed bytes, max entry count, and a
  compression-ratio cap to stop decompression bombs.
- Clamp permission bits (no setuid/setgid/sticky from archives).
- Never overwrite without the configured conflict policy.

### 4.5 Deployment profiles

A profile answers "get this local tree onto that remote, then do the usual
things". It is declarative, previewable, and repeatable.

```toml
[deploy.profiles.prod-api]
host          = "prod-api"          # connection id
local_root    = "./dist"            # source tree
remote_root   = "/srv/api/current"  # destination
mode          = "sync"              # "sync" (delete extraneous) | "update"
exclude       = [".git/", "*.log", "node_modules/", ".env"]
include_hidden = false
follow_symlinks = false
atomic        = true                # upload to *.omssh-tmp then rename
preserve_perms = true
backup        = true                # keep one previous version of changed files
workers       = 4

[deploy.profiles.prod-api.hooks]
local_pre    = ["npm run build"]
local_post   = []
remote_pre   = ["mkdir -p /srv/api/current"]
remote_post  = ["sudo systemctl restart api", "curl -fsS localhost:8080/healthz"]
on_failure   = ["echo 'deploy failed' >> /var/log/omssh-deploy.log"]
```

- **Dry run first, always offered.** `p` computes and shows the plan:
  `create / update / delete / skip` per path with sizes, and a summary. Running
  a deploy that deletes files requires confirming the delete count.
- Excludes use gitignore syntax (via `doublestar`), evaluated relative to
  `local_root`.
- Hooks can be local or remote, run with a defined working directory, inherit
  the connection environment, and stream output into the deploy log. A failing
  `remote_post` marks the run **deployed but unhealthy** rather than silently
  green.
- Every run is recorded: profile, start/end, counts, bytes, hook exit codes,
  and the full log, kept under `~/.local/state/omssh/deploys/`. Re-runnable
  ("run again") and inspectable after the fact.
- Template variables in hooks: `{{host}}`, `{{profile}}`, `{{timestamp}}`,
  `{{local_root}}`, `{{remote_root}}`.
- Comparison mode: a read-only view that shows the diff between local tree and
  remote target without deploying — useful as a sanity check.

### 4.6 Interactive terminal

- Real PTY session over `session.RequestPty("xterm-256color", rows, cols)`,
  rendered by a built-in VT emulator (`charmbracelet/x/vt`) into a Bubble Tea
  view with a scrollback ring buffer (default 10k lines).
- Full-window resize propagation, alt-screen support, bracketed paste, mouse
  reporting passthrough, and OSC 52 clipboard.
- Multiple terminal tabs per host, opened at a chosen working directory.
- **Prefix key** (default `Ctrl+Space`) so every other key reaches the remote
  shell untouched; the prefix opens the app's own menu (new tab, split, close,
  copy selection).
- Optional tmux integration: attach-or-create a named session instead of a
  bare shell.

### 4.7 Modern UI

- Layout: session tabs on top, a collapsible left sidebar (hosts + bookmarks +
  transfer queue), the working pane in the middle, a **contextual action bar**
  under the active pane, an always-visible mode/selection status bar, and a
  toast area.
- **The action bar shows the verbs that apply right now, with their keys, and is
  clickable.** It is the primary way a new user learns the app.
- Command palette (`Ctrl+K`) over every action, bookmark, host and recent path,
  **printing each action's shortcut beside it** so shortcuts are learned by
  using rather than by reading.
- `F9` context menu and full mouse parity (tabs, breadcrumbs with sibling
  dropdowns, buttons, rows, drag between panes, right-click).
- First-run card offering to import `~/.ssh/config`, plus plain-language error
  cards whose primary content is the action that fixes the problem.
- Themes: `midnight`, `dawn`, `mist`, `ember`, `mono`, `contrast`, `auto`
  (follows terminal background detection). Per-theme `lipgloss` styles; true
  colour → 256 → 16 colour degradation driven by `colorprofile`, honouring
  `NO_COLOR` and `CLICOLOR_FORCE`.
- Nerd Font icons throughout with an automatic ASCII fallback when the font is
  not detected or `icons = "ascii"` is configured.
- Responsive rules: below 100 columns the sidebar auto-collapses, below 80 the
  metadata columns drop to the essentials and the action bar collapses to
  `F9 More…`. The breadcrumb and the mode indicator are never dropped.
- Accessibility: no state conveyed by colour alone, `contrast` theme,
  `motion = "reduced"` to replace animation with static text, `--no-icons`.
- Keymap presets (`standard` default, `vim` for navigation) with validation in
  `omssh config check`, and `omssh keys --show` to print the effective map.

## 5. Command line

The TUI is the default when run with no arguments. Subcommands exist for
scripting and for people who want one operation without the UI.

```
omssh                          # launch the TUI
omssh <host>                   # TUI, focused on a host and connected
omssh hosts list|add|rm|show   # manage connections
omssh cp <src...> <dst>        # copy, either side may be host:path
omssh ls host:path             # list
omssh rm host:path...          # remove
omssh extract <archive> --to <dest> [--format auto] [--strip N]
omssh compress <path...> -o <archive> [--format tar.zst] [--level 3]
omssh trash list|restore <id>|empty   # deleted-file recovery on either side
omssh deploy <profile> [--dry-run] [--yes]
omssh exec <host> -- <cmd...>  # non-interactive, streams output
omssh exec <host>              # interactive PTY without the TUI
omssh keys --show              # print the effective keymap
omssh config path|edit|check
omssh version
```

Shared path syntax: `host:path` for remote, anything else for local, `-` for
stdin/stdout where it makes sense. `--json` on the non-interactive commands.

## 6. Non-goals for v1

- Port-forwarding manager, topology diagrams and packet inspection. Valuable,
  but a separate surface; tracked as post-1.0.
- SFTP server or "share this directory" mode.
- Synchronising a local directory continuously (a watcher daemon).
- Mobile/Web front ends, and a Windows-native GUI.
- A file browser for protocols other than SSH: no S3, WebDAV, FTP.
- A fully vim-modal preset (`dd`, `yy`, `p`). The vim preset changes navigation
  only, so a shortcut keeps exactly one meaning across presets (ADR 0006).

## 7. Security

- Secrets never touch disk in plaintext: passwords and passphrases go to the OS
  keyring (Keychain / libsecret / Credential Manager); `ssh-agent` is preferred
  and used first when available.
- Host keys verified; `known_hosts` honoured and updated atomically. A changed
  key is a hard stop, not a warning.
- Archives are treated as untrusted input (§4.4): path traversal, link escape,
  bombs and permission escalation all blocked, with a test suite per attack.
- Remote paths are always shell-quoted when a shell is involved, and we prefer
  the SFTP API over shelling out; the few shell uses (`cp -a`, `mkdir -p`,
  hooks) go through a single audited quoting helper.
- Deploy writes are atomic (`.omssh-tmp` + rename) so a dropped connection
  never leaves a half-written file in place.
- Nothing sensitive is logged; deploy and transfer logs record paths and byte
  counts, never credentials. Redaction is enforced in the logger, not by
  convention.

## 8. Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| VT emulator gaps (`x/vt` is young) break interactive programs | Terminal tab feels broken for `vim`/`htop` | Conformance tests against a recorded corpus of escape sequences; option to hand the real terminal to `ssh(1)` as a fallback path |
| Bubble Tea v2 API churn | Rework mid-build | Pin exact versions; keep TUI dependencies behind `internal/tui` so the app core does not import them |
| SFTP throughput over high-latency links | Slow transfers, bad first impression | Chunked parallel reads/writes, pipelined requests, configurable window; server-side copy where possible |
| Archive breadth (7z/rar) | Feature expectations unmet | Ship the pure-Go set as first-class; external tools strictly optional and clearly labelled |
| Scope creep across six big features | Never ships | Milestones in `ROADMAP.md` are ordered and each ends in a usable binary; deploy and terminal are firmly after the file manager |
| Multiple concurrent sessions leaking resources | Hangs, fd exhaustion | Connection pool with refcounts, per-session context cancellation, goroutine-leak tests (`goleak`) in CI |
| Terminals do not deliver modified keys (`Ctrl+Shift+…`, `Ctrl+Tab`, F-keys behind `Fn`) | A shortcut silently does nothing | Probe capabilities at startup, drop undeliverable shortcuts instead of ignoring them, avoid keys terminals reserve (§13 of `UX.md`), and make the palette/context menu the universal fallback |
| Discoverability decays as features land | New actions become key-only again | Palette, context menu and action bar are three views over **one action registry**; an action missing from the registry is a bug. Enforced by a test that every registered action is reachable from the palette |
| Undo/trash grows into its own product (retention, quota, cross-device) | Scope creep, and a trash that fails on read-only hosts | Bounded v1: single-level undo per session, trash on the same filesystem only, explicit permanent-delete path when trash is unavailable, 7-day retention, CLI management |

## 9. Open questions

1. **Module path** — `github.com/fakih/omssh` or the vanity `fakih.net/omssh`?
   Affects imports everywhere, so decide before M0 lands.
2. **Config format** — TOML is assumed (comments + tables suit profiles well).
   Confirm no preference for YAML.
3. **Deploy "atomic" default** — on, at the cost of a second copy on disk
   during upload. On by default, or opt-in per profile?
4. **Remote→remote across hosts** — stream through the client (assumed) or
   require one host to be able to reach the other (`ssh` from the bastion)?
5. **Colour identity** — pick a signature accent for the default theme, or stay
   entirely terminal-palette-driven (16-colour friendly)?
6. **Trash defaults** — 7-day retention and `.omssh-trash/` in the remote home
   are assumed; also, should deletes prefer the freedesktop trash spec where it
   exists, or always use our own directory for predictability?
7. **`Ctrl+C`** — the plan binds it to copy (ADR 0006) and prints a "Ctrl+Q
   quits" hint when nothing is selected. Confirm that is the right default over
   the alternative (`Ctrl+C` quits, `Ctrl+Insert` copies).
