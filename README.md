# omssh

A modern SSH/SFTP client for the terminal. Built in Go with
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
- **Reversible and safe** — deletes go to a trash on either side, `Ctrl+Z`
  undoes, bulk and destructive operations confirm before starting.
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
- **Many hosts at once** — tabbed sessions, a connection pool with keepalive
  and reconnect, imported from `~/.ssh/config` and written back compatibly.
- **Modern UI** — contextual action bar, command palette that shows every
  shortcut, themes with light/dark adaptation, honest degradation to 256/16
  colours, Nerd Font icons with ASCII fallback, full mouse support, and a
  `contrast` theme plus reduced-motion mode.

> Status: **planning**. This repository currently contains the design and
> implementation plan. See [`docs/PLAN.md`](docs/PLAN.md).

## Documentation

| Document | Contents |
| --- | --- |
| [`docs/PLAN.md`](docs/PLAN.md) | Product plan: goals, features, non-goals, risks, open questions |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Packages, the unified filesystem abstraction, concurrency, security |
| [`docs/UX.md`](docs/UX.md) | Screen layouts, keybindings, themes, folder-walking UX |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Milestones with deliverables and acceptance criteria |
| [`docs/adr/`](docs/adr) | Architecture decision records |

## Building

```sh
make build      # -> ./omssh
make test
make lint
```

Requires Go 1.25+.

## Licence

MIT — see [`LICENSE`](LICENSE).
