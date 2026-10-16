# omssh — UX and Interface

The UI has one job: make it obvious which machine you are touching and what is
about to happen. Everything below serves that.

## 1. Anatomy of the screen

```
┌ omssh ───────────────────────────────────────────────────────────────────────────┐
│  prod-api ●   staging ●   logs ⚠   localhost        ＋ (t)          ⌘ (ctrl+k)  │  session tabs
├───────────────┬──────────────────────────────────────────────────────────────────┤
│ HOSTS         │  ┌ LOCAL   ~/work/api/dist ─────────────────────────────────┐  │
│ ▾ prod        │  │ ..                                      <DIR>            │  │
│   prod-api  ● │  │ ▸ assets/                              4 items   1.2 MB │  │
│   prod-db   ○ │  │   main.js                            980.1 KB  09:12    │  │
│ ▾ staging     │  │ * server.js                           12.4 KB  09:12    │  │  ← * = selected
│   staging   ○ │  │   config.json                          1.1 KB  09:12    │  │
│               │  └──────────────────────────────────────────────────────┘  │
│ BOOKMARKS     │                    ⇅  copy  4 files → remote                 │
│  ⭐ /etc/nginx│  ┌ prod-api: /srv/api/current ──────────────────────────────┐  │
│  ⭐ ~/deploys │  │ ..                                      <DIR>            │  │
│               │  │ ▸ public/                              3 items   0.9 MB │  │
│ TRANSFERS  2  │  │   main.js                            940.2 KB  Oct 15   │  │
│  ↑ main.js 62%│  │   server.js                           12.4 KB  Oct 15   │  │
│  ↓ logs.tgz 12%  └──────────────────────────────────────────────────────┘  │
├───────────────┴──────────────────────────────────────────────────────────────────┤
│ space select · c copy · M move · x extract · z compress · t shell · ? help · ⠿ 2 │  status bar
└──────────────────────────────────────────────────────────────────────────────────┘
```

- **Session tabs** — one per connected host. A coloured dot carries state:
  green ready, amber busy, grey idle, red error. `1`–`9` jump; `Ctrl+W` closes.
- **Sidebar** — collapsible (`b`). Hosts (from `~/.ssh/config`, sorted, fuzzy),
  bookmarks, and a live transfer queue. Collapses automatically under 100 cols.
- **Panes** — the file manager, one or two. The active pane has a bright border
  and its title is the full `host:path`.
- **Status bar** — contextual key hints that change with mode, plus the
  selection summary and a spinner with the count of running jobs. Never a
  static wall of keys.
- **Toasts** — bottom right, 4 s, one line, for "copied 4 files", "host key
  changed", "deploy failed".

## 2. Modes

| Mode | Entered by | Leaves by |
| --- | --- | --- |
| Normal | default | any mode key |
| Filter (in-pane incremental search) | `/` | `Esc` clears, `Enter` keeps |
| Visual (range select) | `v` | `Esc`, or any action |
| Jump (fuzzy path / host) | `Ctrl+P`, `g/` | `Enter` or `Esc` |
| Command palette | `Ctrl+K` | `Run`, `Esc` |
| Modal dialog (conflict, prompt, confirm) | an action | its buttons |
| Terminal input passthrough | focus a terminal tab | `Ctrl+Space` prefix |
| Deploy | `D` on a host | `Esc` |

The mode name is always visible in the status bar. No mode is entered by
guessing.

## 3. Directory walking — the part that must be excellent

This is the feature people will use a thousand times a day, so it gets the most
attention.

**Moving**

| Key | Action |
| --- | --- |
| `j` `k` / `↑` `↓` | Move cursor |
| `J` `K` / `PgDn` `PgUp` | Move a page |
| `g` `G` | Top / bottom |
| `h` / `←` / `Backspace` | Parent directory |
| `l` / `→` / `Enter` | Enter directory (or preview file) |
| `~` | Home |
| `-` | Last directory (like `cd -`) |
| `Ctrl+O` / `Ctrl+I` | Back / forward through visit history |
| `g/` | Fuzzy jump to a path — type `…/nginx/conf.d` and land there |
| `z` | Frecency jump: type `ngx` and it ranks `~/work/nginx` above a fresh match |

**Finding**

| Key | Action |
| --- | --- |
| `/` | Incremental filter of the current listing; type, it narrows live |
| `n` / `N` | Next / previous match of the kept filter |
| `f` | Find recursively under the cursor with a preview of hits |
| `H` | Toggle hidden files (`.git` stays hidden by a built-in default list) |
| `s` | Sort menu: name, size, time, extension, folders-first |

**Selecting**

| Key | Action |
| --- | --- |
| `space` | Toggle select, move down (so selection is one key, repeated) |
| `v` | Visual range from the cursor |
| `%` | Select by glob, e.g. `*.log` |
| `*` | Invert the selection |
| `V` | Select all · `u` clear selection |
| `Esc` | Clear selection (when not in another mode) |

**Preview** — on by default in the right half of a pane under 160 cols; a
keypress (`p`) toggles. Text gets syntax highlighting (Chroma), binaries a hex
dump, archives their entry listing, images a graphics-protocol preview where
supported, and directories a tree summary with sizes.

**The rules that make it feel right**

1. The cursor is never lost. Entering and leaving a directory restores the row
   you came from, highlighted.
2. Nothing blocks typing. Listings are fetched asynchronously; a slow remote
   shows the previous listing with a subtle loading mark rather than freezing.
3. Unreachable is not fatal. A `Permission denied` row is shown, selectable,
   and explains itself on `Enter`; it does not abort the listing.
4. Symlinks are visible as symlinks (`→ target`), followed only when asked.
5. The path bar is editable — click it or `Ctrl+L`, type a path, `Enter`.
6. Every operation works on the selection if there is one, and on the cursor
   otherwise. No operation is cursor-only.

## 4. Keybindings — file manager

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Switch pane |
| `c` | Copy selection → other pane (queued, conflicts resolved) |
| `M` | Move selection → other pane |
| `d` | Delete (confirm, with a size/count summary) |
| `r` | Rename (or batch-rename with a pattern when multi-selected) |
| `a` / `A` | New file / new directory |
| `C` | chmod (octal input, with a readable preview `rwxr-xr-x`) |
| `L` | New symlink |
| `e` | Edit: local opens `$EDITOR`; remote downloads, edits, re-uploads |
| `y` | Yank paths to the system clipboard (OSC 52) |
| `x` | Extract here · `X` extract to… (destination picker) |
| `z` | Compress selection → archive (format + destination) |
| `t` | Open a terminal tab at this directory |
| `Ctrl+S` | Toggle sync browse (panes mirror the relative path) |
| `Ctrl+D` | Diff the two panes (read-only view) |
| `R` | Refresh · `w` toggle auto-refresh (watch the local dir) |
| `b` | Toggle sidebar · `B` bookmark the current directory |
| `o` | Reveal in the other pane (jump there, keep the file under the cursor) |
| `?` | Full keymap |

## 5. Keybindings — global, transfers, deploy, terminal

**Global**

| Key | Action |
| --- | --- |
| `Ctrl+K` | Command palette (every action, host, bookmark, recent path) |
| `Ctrl+P` | Fuzzy jump (paths and hosts) |
| `1`–`9` | Switch session tab |
| `N` | New connection (pick from `~/.ssh/config` or add one) |
| `T` | New local tab (pure local two-pane work, no SSH needed) |
| `Q` | Toggle the transfer queue panel |
| `D` | Deploy menu for the current host |
| `Ctrl+,` | Settings |
| `?` | Help · `Esc` dismiss · `Ctrl+C`/`q` quit (confirm when jobs run) |

**Transfer queue**

| Key | Action |
| --- | --- |
| `j` `k` | Move | 
| `p` | Pause / resume selected · `P` all |
| `x` | Cancel · `X` cancel all |
| `r` | Retry failed · `R` retry all failed |
| `C` | Clear finished |
| `+` `-` | Workers up / down (applies to queued items) |
| `Enter` | Item detail: bytes, speed, ETA, per-chunk state, errors |

**Deploy**

| Key | Action |
| --- | --- |
| `p` | Compute and show the plan (dry run) — the default view |
| `R` | Run the plan (confirm if it contains deletes) |
| `a` `e` `d` | Add / edit / delete a profile |
| `h` | Run history · `Enter` re-run · `L` open a run's log |
| `f` | Follow / unfollow the live log |
| `s` | Switch profile |

**Terminal**

| Key | Action |
| --- | --- |
| `Ctrl+Space` | Prefix — the next key is an omssh command, not sent remotely |
| `Ctrl+Space n` | New terminal tab · `Ctrl+Space w` close |
| `Ctrl+Space s` | Split vertically · `v` split horizontally |
| `Ctrl+Space [` | Enter copy mode: `hjkl` + `v` + `y` to copy selection |
| `Ctrl+Space q` | Leave the terminal, back to files (session stays alive) |

Inside a terminal tab every other key goes to the remote shell verbatim,
including `Ctrl+C`, `Ctrl+Z` and mouse events.

## 6. Dialogs

All dialogs share one visual language: a bordered card, a title, a body, and
right-aligned buttons with the destructive one coloured and never default.

**Conflict dialog** — shown per file, with an "apply to all" checkbox and a
side-by-side compare (size, mtime, and a checksum on demand):

```
┌ Conflict ──────────────────────────────────────────────────────┐
│  main.js already exists                                        │
│                                                                │
│   local    ~/work/api/dist/main.js                             │
│            980.1 KB   Oct 16 09:12   ● newer                   │
│   remote   prod-api:/srv/api/current/main.js                   │
│            940.2 KB   Oct 15 14:40                             │
│                                                                │
│  [ Overwrite ]  [ Skip ]  [ Rename ]  [ Update if newer ]  …   │
│  ☐ apply to all 12 remaining                                   │
└────────────────────────────────────────────────────────────────┘
```

**Extract to…** — a small form: source archive (read-only), format (detected,
overridable), destination side (local / this host / other host), destination
path (with the directory picker and a "new folder named after the archive"
shortcut), strip components, and the safety summary (entry count, total
uncompressed size, or a warning when limits are exceeded).

**Deploy plan** — a scrollable, filterable list of `+ create`, `~ update`,
`- delete`, `= skip` entries grouped by action, with a summary footer
(`2 create · 5 update · 1 delete · 1.4 MB`) and the delete rows in red.

## 7. Themes and colour

- Built-ins: `midnight` (default dark), `dawn` (light), `mist` (low-contrast
  grey), `ember` (warm dark), `mono` (no colour, for monochrome terminals),
  `contrast` (accessibility), and `auto`.
- `auto` reads the terminal background (`COLORFGBG`, or an OSC 11 query on
  startup when supported) and picks `midnight`/`dawn`.
- Colour depth is negotiated once via `colorprofile` and every style is
  defined for true colour, 256, and 16 colours, so `mono` and 16-colour
  terminals are a supported configuration, not a degraded one.
- `NO_COLOR` is honoured. `CLICOLOR_FORCE` overrides.
- Nerd Font icons are used when the terminal advertises one or the user sets
  `icons = "nerd"`; otherwise a curated ASCII/Unicode set is used. The app
  never renders a tofu box.
- Semantic colours, not hard-coded ones: `success`, `danger`, `warning`,
  `muted`, `accent`, `selection`, `created`, `deleted`. Swapping a theme swaps
  intent, not hex values scattered in views.

## 8. Responsiveness

| Width | Behaviour |
| --- | --- |
| ≥ 140 | Two panes, sidebar open, preview on, all metadata columns |
| 100–139 | Two panes, sidebar collapsed to an icon rail, preview on demand |
| 80–99 | One pane at a time with `Tab` switching, minimal columns (name + size) |
| < 80 | Single column, abbreviated status bar, dialogs take the full width |

Height is handled the same way: the transfer queue shows three rows by default
and grows up to a third of the screen when focused.

## 9. Accessibility and manners

- Every action is reachable by keyboard alone, and every dialog is dismissible
  with `Esc`.
- Destructive actions never bind to a single unmodified key on a multi-select
  (`d` requires confirmation; `Ctrl+Shift+D` is the no-dialog escape hatch).
- Long operations are always cancellable and never leave the file system in a
  half-written state (atomic writes, `.part` files, cleanup).
- Progress is shown for anything over ~200 ms; nothing silently hangs.
- The app restores the terminal cleanly on exit and on panic, and never leaves
  the alt-screen buffer enabled.
