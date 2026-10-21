# omssh

**Oh My SSH** — a modern SSH and SFTP client for the terminal.

Many machines, one window. `omssh` puts up to four hosts on screen as a 2×2
grid, moves files between any two of them — local to remote, remote to remote,
folder into archive, archive into folder — and keeps the whole thing one key
away from a shell.

<p align="center">
  <img src="omssh-dashboard.png" alt="omssh showing three hosts in a 2×2 grid with the paste options card open" width="100%">
</p>

## What it does

- **Four panes, one screen.** Local plus three hosts, or four hosts, each with
  its own path, selection and history. `w` moves between them; `V` chooses
  which sessions are on screen.
- **Copying that respects you.** Mark files once, paste into any pane. Copy as
  files, as an unpacked tree, or as an archive — with the conflict rule chosen
  in one card instead of a soup of flags.
- **Archive handling on both sides.** Extract a tarball that lives on the server
  into a local folder; pack a local selection straight onto the server. No
  `tar`/`unzip` on either end.
- **Deploy profiles.** A local tree to a remote root, with excludes, a reviewed
  diff, and a sync mode that mirrors. A second run with nothing changed does
  nothing.
- **A real terminal.** `t` hands the terminal to `ssh` for that host — vim, top,
  tmux and colours work because nothing stands in their way.
- **Honest about state.** Live transfers, marked files, pinned hosts and recent
  connections are visible in the sidebar; every destructive action asks first.
- **Keyboard-first, mouse-friendly.** Plain letters are commands, digits switch
  sessions, `Ctrl+K` finds any action, and `Esc` always goes back — it never
  quits.

<p align="center">
  <img src="omssh.png" alt="omssh launcher" width="70%">
</p>

## Install

```sh
brew tap fakihariefnoto/tap && brew install fakihariefnoto/tap/omssh
```

or

```sh
go install github.com/fakihariefnoto/omssh/cmd/omssh@latest
```

Prebuilt archives for macOS and Linux (amd64 and arm64) are on the
[releases page](https://github.com/fakihariefnoto/omssh/releases).

From source: Go 1.26 or newer.

```sh
git clone https://github.com/fakihariefnoto/omssh
cd omssh
make run
```

## First five minutes

```sh
omssh              # the launcher
omssh prod         # the interface, already connected to prod
```

1. `Ctrl+O` lists the hosts in your `~/.ssh/config` — `Enter` browses one, `t`
   opens a terminal on it.
2. Each connection gets a pane. Open a second and a third; at four they lay out
   2×2. `w` moves the focus, `Tab` cycles sessions.
3. Mark a file with `y`, move to another pane, press `p`. `A` opens the paste
   options if you want it unpacked or packed instead.
4. `Ctrl+E` extracts an archive into either side, `Ctrl+G` compresses the
   selection, `Alt+D` deploys a profile from your config.
5. `Ctrl+K` finds every action; `?` shows the keymap; `Ctrl+Q` quits.

The full guide — every key, every screen, configuration — is in
**[DOCUMENTATION.md](DOCUMENTATION.md)**.

## Configuration

Config lives at `$XDG_CONFIG_HOME/omssh/config.yaml`. Start from the commented
example:

```sh
omssh config init     # write it
omssh config check    # validate it, with line numbers
omssh config path     # where it is
```

Connections are not duplicated here: `omssh` reads and writes your
`~/.ssh/config` in place, leaving comments and directives it does not manage
untouched. The config file holds preferences — theme, keymap overrides,
transfer behaviour, deploy profiles, pinned and hidden hosts.

## Command line

```sh
omssh                            # the interface
omssh <host>                     # the interface, connected on startup
omssh exec <host> -- <command>   # one command, its exit status
omssh keys                       # the effective keymap
omssh config path|init|check
omssh version
```

`exec` allocates no terminal unless you ask with `--tty`, and answers what a
connection asks: it prints an unknown host key's fingerprint before trusting it,
and reads passwords without echoing.

## Development

```sh
make run              # build and launch
make build            # -> ./omssh
make check            # gofmt, vet, golangci-lint, tests with -race
make snapshot         # release archives, locally
make help             # every target
```

Ships for macOS and Linux. Windows is not claimed: the terminal and credential
paths have not been exercised there.

## Licence

MIT — see [LICENSE](LICENSE).
