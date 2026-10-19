# omssh

**Oh My SSH** — a modern SSH/SFTP client for the terminal. Built in Go with
[Bubble Tea v2](https://github.com/charmbracelet/bubbletea) and
[Lip Gloss v2](https://github.com/charmbracelet/lipgloss).

`omssh` is a single binary that manages many SSH/SFTP connections at once and
makes moving files between local and remote machines pleasant:

- **Dual-pane file manager** built around a first-class directory-walking
  experience: type-ahead navigation, incremental filter, recursive find,
  bookmarks, clickable breadcrumbs, preview pane, and standard multi-select.
- **Discoverable by design** — a contextual action bar, a command palette and a
  context menu over one action registry, plus the shortcuts you already know
  (`Ctrl+C`/`Ctrl+V`, `F2`–`F8`, `Alt+←/→`, `Ctrl+Z`). No manual required.
- **Safe** — bulk and destructive operations confirm with the facts before
  starting, and root-ish targets need the path typed out.
- **Transfers that behave** — queue, parallel workers, pause/resume, retry,
  conflict policies, server-side copy when both ends are the same host.
- **Archives both directions** — extract an archive that lives on the remote
  into a local directory, extract a local archive onto the remote, or
  compress a selection into an archive on either side. Streamed, never
  shelling out to `tar`/`unzip`.
- **Deploy profiles** — rsync-like sync of a local tree to a remote target
  with excludes, dry-run preview, pre/post hooks, and a run history.
- **Interactive terminal tabs** — real PTY sessions with a built-in VT
  emulator, scrollback, resize and clipboard.
- **Manage `~/.ssh/config` in place** — list, add, edit and delete hosts from a
  screen (`Ctrl+O`), including filtering and "open in `$EDITOR`". Writes are
  surgical: comments and directives omssh does not manage survive untouched.
  (Authenticating and connecting arrives with the SSH layer — see the roadmap.)
- **Many hosts at once** — tabbed sessions, a connection pool with keepalive
  and reconnect, shared with `~/.ssh/config`.
- **Modern UI** — contextual action bar, command palette that shows every
  shortcut, themes with light/dark adaptation, honest degradation to 256/16
  colours, Nerd Font icons with ASCII fallback, full mouse support, and a
  `contrast` theme plus reduced-motion mode.

> Status: **M0 shipped** (`v0.1.0`) — the interface shell, action registry,
> keymap, theme engine and strict config are in place. Files are browsable in
> the local pane; connections arrive next. See
> [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Documentation

| Document | Contents |
| --- | --- |
| [`docs/PLAN.md`](docs/PLAN.md) | Product plan: goals, features, non-goals, risks, open questions |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Packages, the unified filesystem abstraction, concurrency, security |
| [`docs/UX.md`](docs/UX.md) | Screen layouts, keybindings, themes, folder-walking UX |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Milestones with deliverables and acceptance criteria |
| [`RELEASING.md`](RELEASING.md) | How a release is cut: tags, Homebrew tap, credentials |
| [`docs/adr/`](docs/adr) | Architecture decision records |

## Building and running

```sh
make run              # build and launch the interface
make build            # -> ./omssh
make check            # gofmt, vet, golangci-lint, tests with -race
make help             # every target
```

Requires Go 1.26+ (the Charm v2 stack needs it).

Install a release:

```sh
brew tap fakihariefnoto/tap && brew install fakihariefnoto/tap/omssh
go install github.com/fakihariefnoto/omssh/cmd/omssh@latest
```

Ships for macOS and Linux, amd64 and arm64. Windows is planned, not claimed:
the terminal and credential paths have not been exercised there yet.

```sh
omssh                 # the interface, in your current directory
omssh keys            # print the effective keymap, including anything dropped
omssh config path     # where the configuration lives
omssh config init     # write a commented example config
omssh config check    # validate it, with line numbers
omssh version
```

`omssh` opens on a launcher: pick "Browse local files", or press `Esc` to skip
straight into the file manager. While it is running, `Ctrl+K` finds every action,
`F1` shows the keymap, `F9` opens the context menu for the selection, `Ctrl+Q`
quits, and `Esc` goes back — never quits.

## Licence

MIT — see [`LICENSE`](LICENSE).
