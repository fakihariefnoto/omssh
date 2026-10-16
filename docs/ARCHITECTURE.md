# omssh — Architecture

## 1. Shape of the program

```
cmd/omssh            thin main: version vars, calls internal/cli
internal/cli         cobra command tree, flag parsing, wiring, exit codes
internal/tui         Bubble Tea v2 app (the only package that imports charm UI libs)
internal/app         the domain layer: sessions, operations, events. No UI imports.
internal/ssh         dial, auth, host keys, ProxyJump, keepalive
internal/sftp        SFTP client wrapper -> a vfs.FS
internal/vfs         unified filesystem interface + local implementation
internal/xfer        copy engine: plan, queue, workers, resume, conflicts, progress
internal/archive     detect, read, write, extract with safety limits
internal/deploy      profiles, diff planner, runner, hooks, history
internal/trash       undo/redo, local + remote trash, retention, restore
internal/actions     the action registry: one entry per user-facing action
internal/keymap      keymap presets and validation, built from the registry
internal/config      config, ssh_config read/write, XDG paths
internal/keys        keyring, agent, key loading
internal/version     version/commit/date
internal/log         redacting structured logger
```

Two rules keep this maintainable:

1. **`internal/app` never imports `internal/tui`.** The domain exposes state and
   operations; the UI renders them. This is what makes the CLI subcommands and
   the TUI share one implementation instead of drifting apart.
2. **`internal/tui` never imports `internal/ssh`/`sftp` directly.** It talks to
   `app` and `vfs`. If Bubble Tea is ever replaced, `tui` is the only casualty.

## 2. The central idea: one filesystem interface

Everything the product promises — copy in four directions, extract from an
archive on side A into side B, deploy a tree, diff two trees — is the same
question asked in different clothes:

> given a source that yields bytes and a destination that accepts bytes, move
> them, and keep the shape.

So there is exactly one abstraction:

```go
// internal/vfs/vfs.go
package vfs

// FS is the intersection of behaviour we need, local or remote.
// Deliberately close to io/fs so that stdlib helpers keep working.
type FS interface {
    fs.FS                          // Open(name) (fs.File, error)
    fs.ReadDirFS                   // ReadDir(name) ([]fs.DirEntry, error)
    fs.StatFS                      // Stat(name) (fs.FileInfo, error)
    fs.ReadFileFS                  // ReadFile(name) ([]byte, error)

    Lstat(name string) (fs.FileInfo, error)
    Create(name string) (WFile, error)      // O_CREATE|O_TRUNC|O_WRONLY
    OpenFile(name string, flag int, perm fs.FileMode) (WFile, error)
    Mkdir(name string, perm fs.FileMode) error
    MkdirAll(name string, perm fs.FileMode) error
    Remove(name string) error
    RemoveAll(name string) error
    Rename(oldpath, newpath string) error
    Chmod(name string, mode fs.FileMode) error
    Chtimes(name string, atime, mtime time.Time) error
    Readlink(name string) (string, error)
    Symlink(target, name string) error
    Truncate(name string, size int64) error

    // Path helpers, since local uses filepath and remote uses slash.
    Join(elem ...string) string
    Dir(name string) string
    Base(name string) string
    Abs(name string) (string, error)
    Capabilities() Capabilities
}

type WFile interface {
    io.Writer
    io.Closer
    io.ReaderFrom // enables copy_file_range / io.Copy fast paths
    Sync() error
}

// Capabilities lets callers degrade: no symlinks on a server, no chmod on a
// FAT mount, no server-side copy extension.
type Capabilities struct {
    Symlink, Chmod, Chtimes bool
    CopyData                bool // SFTP copy-data extension
    ServerSideCopy          bool // can cp -a on the same host
}
```

Implementations:

- `vfs.Local` — `os` + `io/fs`, with path cleaning via `filepath`. Rooted at
  `/` (or a provided root) and hardened against escape when rooted elsewhere.
- `vfs.SFTP` — wraps `pkg/sftp.Client`, all paths POSIX-slash, Lstat-correct,
  with an `io/fs` adapter so `fs.WalkDir` works unchanged.
- `vfs.Mem` — in-memory, used only in tests, to exercise the same code paths
  the remote implementation uses without a network.

A conformance suite (`vfs/vfstest`) runs one set of ~60 assertions against
every implementation, including destructive and error cases. If `SFTP` and
`Mem` and `Local` all pass, the copy/extract/deploy code above them is
side-agnostic by construction.

### 2.1 How the features fall out of it

| Feature | Implementation |
| --- | --- |
| Copy src→dst (any direction) | `xfer.Copy(ctx, dst vfs.FS, dstPath, src vfs.FS, srcPath)` |
| Extract archive into dir | `archive.Extract(ctx, dst vfs.FS, dstDir, src io.Reader)` |
| Extract without a temp file | The archive is an `io.Reader`; over SFTP it is the remote file handle, locally an `*os.File` |
| Compress a selection | `archive.Create(ctx, w io.Writer, fmt, entries)` where entries are streamed from the pane's `FS` |
| Deploy a tree | `deploy.Plan` walks `local_root` and `remote_root` with `fs.WalkDir`, then `xfer` runs the plan |
| Dry-run diff | The plan, rendered. No execution path involved. |
| Remote→remote | Two different `vfs.FS` handles; the copy engine notices and streams through |
| Delete + undo | `trash.Discard` **renames** into the trash (`vfs.FS.Rename`), so undo is a rename back — no special I/O path |

There is no "upload" function and no "download" function. There is one copy
function. This is the decision that keeps the feature list small in code and
large in behaviour — see `docs/adr/0001-unified-vfs.md`.

## 3. The app layer

```go
// internal/app
type App struct {
    cfg      *config.Config
    pool     *Pool          // ssh connections
    xfers    *xfer.Engine
    deploys  *deploy.Runner
    events   chan Event     // single fan-in channel to the UI
    log      *slog.Logger
}

func (a *App) OpenPane(host string, path string) (*Pane, error)
func (a *App) FS(host string) (vfs.FS, error)   // "" => local
func (a *App) Copy(ctx context.Context, req CopyRequest, report func(Progress)) error
func (a *App) Extract(ctx context.Context, req ExtractRequest, report func(Progress)) error
func (a *App) RunDeploy(ctx context.Context, profile string, opt DeployOptions, report func(Progress)) error
```

- `Pool` keys connections by a canonical identity (host, user, port, key, proxy
  chain) so two tabs onto the same host share one TCP connection and one
  authenticated session, with refcounting. Idle connections close after a
  timeout; keepalive keeps live ones alive.
- Long operations take a `context.Context` and a progress callback. The TUI
  passes a closure that posts to the Bubble Tea program; the CLI passes one
  that draws a progress bar; a test passes one that counts. One implementation,
  three front ends.

### 3.1 The action registry

`internal/actions` holds one record per user-facing action — its id, label,
category, the operation it invokes, and its default binding:

```go
type Action struct {
    ID       string   // "file.copy"
    Label    string   // "Copy"
    Category string   // "File"
    Default  key.Binding
    Enabled  func(app.State) bool   // contextual availability
    Run      func(ctx context.Context, a *app.App, s app.State) tea.Msg
}
```

The palette (`Ctrl+K`), the `F9` context menu and the contextual action bar are
**three renderers over this one list**, filtered by `Enabled`. A feature that
exists only as a stray key handler is not added to the app: it is added to the
registry, and it becomes discoverable, searchable and clickable for free. A test
asserts every registered action is reachable from the palette, which is what
keeps the "nothing is key-only" promise from decaying as the app grows.

`internal/keymap` resolves bindings for the active preset, detects duplicates and
unbindable keys, and feeds `omssh config check` and `omssh keys --show`.

## 4. Concurrency model

- **One goroutine per connection** owns the `ssh.Client` and its SFTP client,
  because SFTP requests are multiplexed by the library but the higher-level
  state (current dir, open handles) is not free-threading.
- **Worker pool per transfer job**: `workers` file workers, each pulling items
  off the plan. Large files use one worker with parallel chunk offsets within
  the file. Concurrency is bounded end to end so a 10k-file directory does not
  spawn 10k goroutines.
- **Cancellation everywhere**: every operation takes a context; cancelling a
  transfer closes open handles, removes `.part` files per policy, and reports
  partial progress.
- **Events, not shared state**: progress, logs, state changes and toasts are
  sent on a single buffered channel into the UI. The UI model is the only thing
  that mutates display state, so there is no mutex in the render path.
- **Bubble Tea v2 discipline**: blocking work happens in `tea.Cmd`s; the
  `Update` function never blocks on I/O. Streaming progress arrives as
  `tea.Msg`s from a goroutine that reads the events channel.
- Tests use `goleak` to assert no goroutine outlives its operation.

## 5. Transfer engine detail

```go
type Item struct {
    Src vfs.FS; SrcPath string
    Dst vfs.FS; DstPath string
    Size    int64
    Mode    fs.FileMode
    MTime   time.Time
    Action  Action // Create | Update | Skip | Delete | Rename
}

type Plan struct {
    Items   []Item
    Bytes   int64
    Counts  map[Action]int
}
```

1. **Plan** — resolve conflicts, apply excludes and policies, decide
   `Create/Update/Skip/Delete`. For deploys this is computed against the remote
   listing first and shown as the dry run.
2. **Execute** — workers take `Item`s; each does
   `open src → create dst(.part if resumable) → io.CopyBuffer with a pooled
   1 MiB buffer → chmod → chtimes → rename`. Progress is emitted per chunk,
   throttled to ~10 ms so the UI never floods.
3. **Retry/resume** — a failed item keeps its `.part` and recorded offset;
   retry resumes from there. SFTP has no native resume, so we implement it at
   the item level.
4. **Fast paths**, tried in order:
   - same `FS` instance and destination is a directory → SFTP `Rename`
   - same host, SFTP `copy-data` extension available → server-side copy
   - same host, shell available → `cp -a -- <src> <dst>` through the audited
     quoting helper
   - otherwise → stream
5. **Cleanup** — cancelled or failed items leave no half-written destination
   unless the policy says otherwise; `.part` files are swept on next run.

## 6. Archive engine detail

- **Detection**: extension first, then magic bytes (`ReadAt` of 512 bytes) so a
  misnamed file still works and a `.tar.gz` is never guessed as `.gz`.
- **Reading**: every format is normalised to an iterator of entries
  `{Name, Mode, ModTime, Size, Open() io.ReadCloser}`. tar and zip map
  naturally; single-file codecs (`.gz` on a non-tar stream) synthesise one
  entry named after the archive minus its extension.
- **Writing**: the same entry iterator is consumed by a tar/zip writer over an
  optional compressor.
- **Extract** walks entries and writes through `vfs.FS`, so extracting to a
  remote destination is the same loop as extracting locally.
- **Zip-slip and friends** are checked in one place,
  `archive.safeJoin(root, entryName)`, which is pure, table-tested, and used by
  every format so no format can bypass it.
- **Limits** are `Limits{MaxBytes, MaxEntries, MaxRatio, AllowSymlinks}`,
  defaulted conservatively and overridable.
- **Strip components** (`--strip N`) and an optional prefix filter make
  "extract into this dir, minus the top folder" a flag, not a manual rename.

## 7. Config, paths, storage

| Thing | Location |
| --- | --- |
| Main config | `$XDG_CONFIG_HOME/omssh/config.toml` (default `~/.config/omssh/`) |
| Themes | `.../omssh/themes/*.toml`, built-ins embedded |
| Connections | `~/.ssh/config` (read/write, OpenSSH-compatible) |
| Secrets | OS keyring, keys under `omssh/<connection-id>` |
| Local trash | freedesktop trash when present, else `~/.local/share/omssh/trash/` |
| Remote trash | `<remote home>/.omssh-trash/<timestamp>/`, 7-day retention, swept on connect |
| State (history, deploy logs, undo log) | `$XDG_STATE_HOME/omssh/` |
| Cache (previews, listings) | `$XDG_CACHE_HOME/omssh/` |
| Logs | `.../omssh/logs/`, rotated, redacted |

Config is TOML, loaded with `pelletier/go-toml/v2`. Unknown keys are an error
(`DisallowUnknownFields`) so typos surface early; `omssh config check` reports
them with line numbers.

## 8. Error handling and logging

- Sentinels in `internal/app/errs.go` for the cases the UI branches on:
  `ErrAuthFailed`, `ErrHostKeyMismatch`, `ErrPermissionDenied`,
  `ErrNoSpace`, `ErrCancelled`, `ErrConflict`, `ErrUnsupported`.
- All errors are wrapped with `%w` and context; `errors.Is/As` drives recovery.
- A user-facing message and a developer detail are separate fields, so the UI
  can show "Permission denied: /etc/shadow" while the log holds the syscall and
  the remote errno.
- The logger redacts by type: any field of type `keys.Secret` renders as
  `[redacted]`. Credentials cannot be logged by accident because they cannot be
  formatted.

## 9. Testing strategy

| Layer | How it is tested |
| --- | --- |
| `vfs` | Conformance suite run against `Local`, `Mem`, and `SFTP`-over-`MemSFTP` |
| `sftp`/`ssh` | `pkg/sftp`'s in-process server + `charmbracelet/ssh` test server; no network, no docker for unit tests |
| `xfer` | Table tests for planning, conflict policy and resume; fault injection for mid-transfer failures |
| `archive` | A per-format corpus plus a dedicated attack corpus (zip-slip, links, bombs, bombs-in-zip, oversize) |
| `deploy` | Plan snapshots (golden files) and a full run against the in-process server |
| `tui` | `teatest` v2 golden renders for layouts at several widths; keymap-level tests for navigation and selection |
| End to end | `-tags integration` with a real `openssh-server` container: connect, copy, extract, deploy |

`make test` runs everything except the tagged integration suite and is expected
to stay under a minute with `-race`.

## 10. Packaging

- `goreleaser` for `darwin/linux/windows` × `amd64/arm64`, producing archives,
  checksums and a Homebrew tap formula.
- `go install github.com/fakih/omssh/cmd/omssh@latest` works because the app is
  pure Go with no cgo.
- Shell completions and a man page generated from cobra at release time.
