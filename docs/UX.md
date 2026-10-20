# omssh — UX and Interface

The UI has two jobs: make it obvious which machine you are touching and what is
about to happen, **and** make the next action guessable without reading a manual.
Everything below serves those two jobs.

This document is deliberately opinionated. §2 explains *why* the current design
is intuitive; §3 is the screen; §4–6 are the interaction rules; §7 is the full
keymap; §8–13 cover dialogs, feedback, safety, onboarding and terminal limits.

---

## 1. Design principles

| # | Principle | Consequence in the UI |
| --- | --- | --- |
| P1 | **Conventions before cleverness** | `Ctrl+C`/`Ctrl+X`/`Ctrl+V`, `F2`–`F8`, `Alt+←/→`, `Ctrl+F`, `Ctrl+L`, `Ctrl+Q` all mean what they mean everywhere else |
| P2 | **Nothing is key-only** | Every action is in the command palette, in the `F9` context menu, and clickable. The palette prints each action's shortcut, so shortcuts are *learned by using* |
| P3 | **The app shows you what it can do** | A contextual action bar under the active pane lists the relevant verbs for the current selection, with their keys |
| P4 | **Safe by default** | Destructive and bulk actions confirm before starting — and the confirmation says exactly what is at stake (items, size, path) |
| P5 | **Two speeds for everything** | Quick path (`Ctrl+C` → `Tab` → `Ctrl+V`) and guided path (`F5` opens a dialog with options). Neither is second-class |
| P6 | **Few modes, always labelled** | Modes: Normal, Search, Dialog, Terminal. Selection is *not* a mode. The current mode is named in the status bar |
| P7 | **Never a dead end** | `Esc` always goes back one level of context; every empty, loading and error state offers the action that fixes it |
| P8 | **A letter means a command** | Plain letters are commands, digits switch tabs, `Ctrl+letter` opens a dialog, `Alt+letter` toggles a view, `Esc` goes back. No mode where letters sometimes type |
| P9 | **Teaching empty states** | First run offers to import `~/.ssh/config`; an empty pane says what to press; a failed connect says what to try next |
| P10 | **Mouse parity** | Tabs, breadcrumbs, buttons, list rows, the action bar and the queue are all clickable; right-click opens the context menu; drag between panes copies |
| P11 | **Direction is spelled out** | The panes are labelled `LOCAL` and `REMOTE`, and the verbs are `Send` and `Fetch`, not "copy" |

### Why the old design was hard to learn

The first draft leaned on vim muscle memory and arbitrary letters
(`c` copy, `M` move, `x` extract, `z` compress, `Ctrl+S` sync). A new user had
to read a table and remember it, and even then `M` versus `c` versus `d` gave no
hint about which was destructive. The revision:

- replaces arbitrary letters with **standard shortcuts** that transfer from
  every editor and file manager (P1),
- makes **every key discoverable** through the palette, the context menu and the
  action bar, so nobody needs the table at all (P2, P3),
- keeps the old letters as an **optional vim navigation preset**, so the people
  who liked them lose nothing (P8, §7.3).

### How a new user actually gets things done

No prior knowledge, in the order they would try it:

| Task | How it is discoverable |
| --- | --- |
| Connect somewhere | First-run card: **"Import hosts from ~/.ssh/config"**. Otherwise `Ctrl+O` |
| Copy a file to the server | The action bar under the pane literally reads `Send → prod-api`. Click it, or `F5`, or `Ctrl+C`/`Ctrl+V` |
| Copy many files | Click rows or `Space` to select; the action bar shows `Send → prod-api (4 files · 1.2 MB)` |
| Work out what an archive holds | `Enter` or `F3` on it — contents are listed, no extraction |
| Extract it to the machine on the other side | Select it, `Ctrl+E`, pick the destination side in the dialog |
| Avoid deleting something you need | `F8` shows `Delete 3 items · 1.4 GB · prod-api:/srv/api` and waits — deletes are permanent, so the dialog is the safety net |
| Find a file | `/` and type — the listing narrows as you go |
| Find a file anywhere in the tree | `Alt+F` |
| Deploy | `Alt+D` opens the deploy card: pick a profile, review the diff, Enter deploys |
| Do anything else | `Ctrl+K` and type what you want in plain words |

---

## 2. Screen anatomy

```
┌ omssh ────────────────────────────────────────────────────────────────────────────┐
│  1 prod-api ●   2 staging ○   3 logs ⚠   4 localhost            [ Deploy ]  ⌘ ⌃K  │  session tabs + actions
├──────────────┬────────────────────────────────────────────────────────────────────┤
│ ▾ HOSTS      │  LOCAL  ▸ ~ ▸ work ▸ api ▸ dist                   512 MB free      │  breadcrumb (clickable)
│   prod-api ● │ ┌───────────────────────────────────────────────────────────────┐  │
│   prod-db  ○ │ │ ..                                                ↑ 09:12  <DIR> │  │
│ ▾ staging    │ │ ▸ assets/                              4 items    1.2 MB       │  │
│   staging  ○ │ │   main.js                            980.1 KB    09:12  rw-r--r--│ │
│ ▾ BOOKMARKS  │ │ ▓ server.js                          512.4 KB    09:12  rw-r--r--│ │  ← selected
│   ⭐ /etc/n… │ │   config.json                          1.1 KB    09:12  rw-r--r--│ │
│              │ │   deploy.tar.gz                        2.4 MB    09:10  rw-r--r--│ │
│ TRANSFERS  2 │ └───────────────────────────────────────────────────────────────┘  │
│  ↑ main.js 62%│  Send → prod-api (2 files · 1.5 MB)   F3 View  F4 Edit  F2 Rename │  action bar
│  ↓ logs.tgz 12%  F5 Copy…  F6 Move…  F8 Delete  F9 More…                          │
│              ├────────────────────────────────────────────────────────────────────┤
│              │  REMOTE  prod-api ▸ / ▸ srv ▸ api ▸ current      28 GB free        │  breadcrumb
│              │ ┌───────────────────────────────────────────────────────────────┐  │
│              │ │ ..                                                Oct 15 <DIR> │  │
│              │ │ ▸ public/                              3 items    0.9 MB       │  │
│              │ │   main.js                            940.2 KB    Oct 15  rw-r--r--│ │
│              │ └───────────────────────────────────────────────────────────────┘  │
├──────────────┴────────────────────────────────────────────────────────────────────┤
│ NORMAL · 2 selected · 1.5 MB ·  ↑ sending 2 files · 24 MB/s · 1m 08s left  · ? help│  status bar
└───────────────────────────────────────────────────────────────────────────────────┘
```

- **Session tabs** carry a state dot (green ready, amber busy, grey idle, red
  error), a click target, and a right-click menu (close, close others,
  reconnect, deploy, tab colour).
- **Breadcrumb** is interactive: click a segment to jump, click a chevron for a
  dropdown of sibling directories (Finder-style). `Ctrl+L` turns it into an
  editable path.
- **Action bar** sits under the active pane. It is the single biggest
  discoverability change: it always shows the four to six verbs that apply right
  now, with keys, and every one is clickable. Select an archive and `Ctrl+E
  Extract` appears; select nothing and it shows pane-level verbs.
- **Status bar** names the mode, the selection summary, and live job state. It
  answers "what is happening and what mode am I in" without moving the eyes far.
- **Toasts** (bottom-right, 4 s) report completions in one line, and after a
  delete they say what was removed so a mis-click is noticed immediately.

### 2.1 The front door

Every launch opens on a launcher: the wordmark, one line saying what the program
is, and a menu of what this build can actually do. Each entry carries a plain
ASCII icon, so it reads the same on any terminal before a theme is applied.

```
        ██████  ███    ███ ██████  ██████  ██    ██
       ██    ██ ████  ████ ██      ██      ██    ██
       ██    ██ ██ ████ ██ █████   █████   ████████
       ██    ██ ██  ██  ██      ██      ██ ██    ██
        ██████  ██      ██ ██████  ██████  ██    ██

         Oh My SSH — SSH/SFTP with terminal UI

        ▸ [~] Browse local files        Enter
          [/] Go to a folder            Ctrl+L
          [+] Settings, theme and keys  Ctrl+,
          [x] Quit                      Ctrl+Q

        ↑↓ choose · Enter open · Ctrl+K actions
```

- `↑↓`/`Tab` move and `Enter` opens. The launcher is the bottom of the stack:
  `Esc` twice from anywhere lands here, and `Esc` here names `Ctrl+Q` rather than
  bouncing back into the files.
- **Only things that work appear.** A menu of dead ends teaches nothing, so
  `Connect` joins this list when the SSH layer lands (M1), and `Deploy` when
  profiles do (M6).
- The plate and the menu are **two centred units**. They are different widths,
  and padding the narrow one into the wide one's box leaves the wordmark visibly
  left of centre; inside the menu, rows are padded to a common width so the
  icons form a column. Centring each row separately is the other failure: every
  line then has a different left edge.
- `ui.start: files` skips the launcher for people who do not want a front door.
- The whole menu is clickable.

### 2.2 The connections screen

`Ctrl+O`, or **SSH connections** on the launcher, opens the host manager. It edits
`~/.ssh/config` directly, so what it shows is what `ssh` will use.

```
SSH connections                                          ~/.ssh/config · 3 hosts
────────────────────────────────────────────────────────────────────────────────
Ctrl+F to filter
▸ prod-api        203.0.113.10          deploy      :2222
  staging         staging.example.com   ubuntu
  orb             127.0.0.1             default           (from include)
────────────────────────────────────────────────────────────────────────────────
prod-api
  host     203.0.113.10
  user     deploy
  port     2222
  key      ~/.ssh/id_ed25519
  jump     —
  source   ~/.ssh/config

Enter/e edit · a add · d delete · r reload · o edit file · Ctrl+F filter · Esc back
```

- **Enter edits**, because managing hosts is what this screen is for; connecting
  is a separate action and arrives with the SSH layer.
- `a` adds a form: name, host, user, port, key, jump host — with the same
  validation `ssh` would apply, so a typo is caught here rather than at connect
  time.
- `d` deletes, behind the usual confirmation stating which host and which file.
- `r` reloads, `o` opens the file in `$EDITOR` (omssh suspends and resumes), `Esc`
  goes back to where it was opened from.
- Hosts that come from an `Include` are listed and marked: they are **read-only**
  here, because the write would land in the wrong file. `o` is how they are
  edited.
- An empty file is an invitation, not an error: *"No hosts yet · a adds one"*.

### 2.3 The tab strip

The strip is labelled `sessions` and its tabs are separated by rules, so the row
is self-explanatory. The active tab is marked with colour **and an underline**,
never a filled block: a solid chip reads as a floating badge or a tooltip rather
than as chrome. The local tab shows no state dot (it is always available); a
remote tab wears its connection state.

### Panes and direction

Two panes. By default the **left pane is local, the right pane is remote**, and
the titles say so. Either pane can be local or remote — remote-on-remote is a
normal layout, not a special mode — but the default is the one that covers the
common case.

Verbs are directional and plain: **`Send →`** moves the selection to the other
pane, **`Fetch ←`** brings it here. "Copy" survives as the concept in the docs
and CLI (`omssh cp`), but the UI never makes the user work out which way is
which.

---

## 3. Modes

| Mode | Entered by | Leaves by | Visible as |
| --- | --- | --- | --- |
| Normal | default | — | `NORMAL` |
| Search | `/` or `Ctrl+F` (in-pane filter) | `Esc` | `SEARCH` + a query chip |
| Dialog | a dialog opens | `Esc` or a button | the dialog card |
| Palette | `Ctrl+K` | `Enter` / `Esc` | the palette |
| Terminal | focus a terminal tab | prefix key | terminal chrome bar |
| Deploy | `Alt+D` | `Esc` | deploy tab |

Selection is **not** a mode: `Space` toggles, `Shift+↑/↓` extends like every GUI
list, and the count is always in the status bar.

---

## 4. The modifier grammar

Learn this once and the rest of the app is guessable:

| Prefix | Meaning | Examples |
| --- | --- | --- |
| **Plain letters** | A **command**, the way ranger and lf work | `d` delete, `r` rename, `y` copy, `x` cut, `p` paste |
| **Digits** | Switch to that session tab | `1` local, `2` the first host |
| **Arrows / Shift+arrows** | Move, extend selection, `←` up a level, `→` open | `Shift+↓` selects a run |
| **Alt + letter** | A *view* toggle or a named place | `Alt+B` sidebar, `Alt+J` jobs, `Alt+S` marked files, `Alt+D` deploy |
| **Ctrl + letter** | An *action* or a *dialog* | `Ctrl+K` palette, `Ctrl+E` extract, `Ctrl+L` path, `Ctrl+Q` quit |
| **Esc** | Back one level: pane → tab strip → launcher | never quits |

There is **no type-ahead**. A letter that sometimes navigates and sometimes types
is a letter you cannot learn, so letters are commands and finding a file is `/`
or `Ctrl+F`. Function keys survive only as aliases for people whose hands already
know them; nothing in the interface advertises them.

`Enter` opens or confirms. `Esc` goes back one level of context, never destroys
anything and never quits. Pressed twice from anywhere it lands on the launcher —
the first leaves the file manager, the second does nothing but name the quit key,
so the stack has a bottom rather than bouncing between two screens. `Tab`
switches pane.

---

## 5. Directory walking — the part that must be excellent

Used a thousand times a day, so it gets the most attention.

**Moving** — `↑`/`↓` move, `→`/`Enter` open, `←` parent, `Home`/`End` and
`PgUp`/`PgDn`, `Ctrl+Home`/`Ctrl+End` for first/last. `Alt+←`/`Alt+→` walk the
visit history (same as a browser). `Ctrl+L` edits the path with completion.
`Ctrl+R` refreshes.

**Marking** — `y` or `x` marks the selection, and a bar appears under the panes:

```
▸ 3 items · 12.4 MB copy → contabo  /var/www      p paste · s review · Esc cancel
```

It says what is marked, where it is going, and the three keys that decide it, so
the answer to "what am I about to do" never needs a screen of its own. The review
screen (`s`) is there when the list is long or the conflict rule matters.

**Finding** — two levels:

1. **Filter** — `/` (or `Ctrl+F`) narrows the current listing incrementally as
   you type, `Esc` clears. One key, no guessing about whether a letter is a
   command or a search.
2. **Recursive find** — `Alt+F` searches the subtree and shows results with
   paths, sizes and match highlighting; `Enter` reveals a result in the pane.

Hidden files: `Alt+H`, with a built-in default ignore list (`.git`,
`node_modules`, `__pycache__`, …) that `Alt+H` shows anyway.

**Selecting** — `Space` toggles and advances, `Shift+arrows` extends a run, the
same as any GUI list. `Ctrl+A` selects all, `Alt+A` inverts. The status bar
always shows `N selected · <total size>`. Selection survives navigation, and
every operation applies to the selection when there is one and to the cursor
otherwise.

**Preview** — the right half of the active pane below 160 columns, toggled with
`Alt+P`: syntax-highlighted text (Chroma), hex for binaries, an entry listing for
archives, a size tree for directories, and image preview where the terminal
supports graphics. Preview cost is bounded: files over 1 MB stream the head and
say so.

**Unknown metadata reads as unknown.** A server that omits file attributes leaves
the mode at zero; the listing shows one `?` in the permission column rather than
ten, and the entry is asked about directly so a directory is still a directory.

**The rules that make it feel right**

1. **The cursor is never lost.** Returning to a directory restores the row you
   left, highlighted.
2. **Nothing blocks typing.** Listings are fetched asynchronously; a slow remote
   shows the previous listing with a small loading mark.
3. **Unreachable is not fatal.** An unreadable entry is a selectable row that
   explains itself and offers `Retry` / `Open as root` / `Copy path`.
4. **Symlinks look like symlinks** (`→ target`) and are followed only when
   asked, never during recursive operations.
5. **The path is editable.** Click the breadcrumb or `Ctrl+L`, type, `Enter`,
   with completion.
6. **Every operation works on a selection**, so "copy 200 files" is the same
   gesture as "copy one file".

---

## 6. Operations: two speeds, always previewed

**Quick path** — `Ctrl+C` (or `Ctrl+X`), `Tab`, `Ctrl+V`. Standard, immediate,
no dialog. The status bar shows `2 files staged · Ctrl+V to paste into REMOTE`.

**Guided path** — `F5` Copy / `F6` Move open the same dialog:

```
┌ Copy ────────────────────────────────────────────────────────────┐
│  2 files · 1.5 MB                                                │
│    main.js                           980.1 KB   09:12            │
│    server.js                         512.4 KB   09:12            │
│                                                                  │
│  Destination   ( ) Other pane  ●  prod-api:/srv/api/current      │
│                ( ) Same directory                                │
│                ( ) Bookmark…  ( ) Recent…  ( ) Pick a directory… │
│                                                                  │
│  Options       Conflict: update if newer ▾   Preserve: mode+mtime│
│                [ ] Skip files that already match                 │
│                                                                  │
│              [ Show plan ]   [ Cancel ]   [ Copy 2 files ]       │
└──────────────────────────────────────────────────────────────────┘
```

- Destination defaults to **the other pane's directory** — the answer 90% of the
  time — and the alternatives are one click: same directory, a bookmark, a
  recent destination, or the directory picker.
- `Show plan` renders exactly what will happen (create / update / skip), which is
  the same preview used by deploy and by extraction.
- **Bulk or destructive operations confirm inline**, not with a modal. After
  `Ctrl+V` of 200 files: `Copy 200 files → prod-api:/srv/api · Enter to start ·
  Esc to cancel · o options`. One line, no interruption, still a deliberate
  confirmation. Operations under ~20 files or under 10 MB skip the prompt
  entirely and just start.

**Deleting is permanent, so deleting is deliberate.** There is no trash and no
undo (`Ctrl+Z` is unbound in v1). Instead:

- `F8`/`Del` always opens a confirmation showing what is at stake:
  `Delete 3 items · 1.4 GB · prod-api:/srv/api/current` with the list expanded
  when it is short, and `Enter` on the confirm button never the default action
  for a multi-item or large selection.
- Recursive deletes state the depth and counts: `Delete public/ (412 files,
  38 MB)`. Confirming a directory is never ambiguous.
- Root-ish targets (`/`, `/etc`, `$HOME`, a mount point) switch to
  **type-the-path-to-confirm**, per §8.
- The toast afterwards is plain: `Deleted 3 items`, with no undo affordance
  offered.
- Anything that *can* be undone cheaply still is not framed as undo: rename and
  move are simply reversible by doing the opposite, and `F6`/`F2` are available
  immediately.

---

## 7. Keymap

### 7.1 Navigation

| Key | Action |
| --- | --- |
| `j` `k` / `↑` `↓` | Move cursor |
| `h` / `←` / `Backspace` | Parent directory |
| `l` / `→` / `Enter` | Open (directory) or view (file) |
| `PgUp` `PgDn` `Home` `End` | Page / first / last |
| `g g` / `G` | First / last entry |
| `~` / `-` | Home / last directory |
| `Alt+←` `Alt+→` | History back / forward |
| `Ctrl+L` | Go to a path (`~` is this pane's home, plain names are relative) |
| `Ctrl+R` | Refresh |
| `Ctrl+P` | Fuzzy jump to a path |
| `Alt+F` | Recursive find in the subtree |
| `Y` | Sync browse: the other pane follows the same path |
| `/` or `Ctrl+F` | Filter the listing |
| `.` | Toggle hidden files |
| `Alt+P` | Toggle preview |
| `w` | Switch pane (the keys for `w` = window) |
| `Tab` `Shift+Tab` | Next / previous host tab (the session strip follows) |
| `o` | Reveal the selection in the other pane |
| `1`…`9` | Switch to that session tab |
| `[` `]` | Previous / next tab (aliases for Shift+Tab / Tab) |
| `Ctrl+O` | Open a connection (host picker) |

### 7.2 File operations

| Key | Action | Notes |
| --- | --- | --- |
| `Space` | Toggle select and advance | selection is not a mode |
| `Shift+↑` `Shift+↓` | Extend selection | like every GUI list |
| `Ctrl+A` / `Alt+A` | Select all / invert | |
| `y` / `Ctrl+C` | Mark for copy | a bar under the panes shows where they go |
| `x` / `Ctrl+X` | Mark for move | the same bar says `move →` |
| `p` / `Ctrl+V` | Paste into this pane | copies or moves, to the other pane |
| `C` | Copy to… | choose the destination instead of using the other pane |
| `M` | Move to… | same, for a move |
| `v` / `F3` | View (preview pane) | archives list their contents |
| `e` / `F4` | Edit in `$EDITOR` (remote: fetch, edit, send back; >16 MB refused) | |

| `r` / `F2` | Rename (batch rename when multi-selected) | |
| `n` / `N` | New file / new folder | |
| `d` / `Ctrl+D` / `Del` | Delete → always asks | no trash, so the dialog is the safety net |
| `Ctrl+E` | Extract… | `local:/path` or `remote:/path`; the other pane is the default |
| `Ctrl+G` | Compress… | format follows the name (`.tar.gz`, `.tar`, `.zip`) |
| `P` | Change permissions (octal) | |
| `L` | Create a link to the cursor entry | |
| `Ctrl+G` | Compress selection… | format + destination side |
| `s` / `Alt+S` | Review marked files | what, where, and the conflict rule |
| `Esc` | Cancel the marked set | the bar says so, and Escape keeps that promise |
| `Alt+C` | Compare the two panes (read-only diff) | |
| `Alt+B` `Alt+J` `Alt+D` | Sidebar / jobs queue / deploy | view toggles |
| `F9` / right-click | Context menu — every operation for the selection | discovery net |
| `Ctrl+K` | Command palette — every action, searchable, with its shortcut | discovery net |
| `?` / `F1` | Help — searchable keymap grouped by task | discovery net |
| `T` | Full SSH terminal (or a local shell) — the terminal is handed to `ssh` | |
| `Ctrl+Q` / `F10` | Quit (confirms if jobs are running) | |
| `Esc` | Back / dismiss / clear | never destructive, **never quits** |

### 7.3 The tab strip has focus too

`Esc` from a pane moves the focus **to the tab strip** when there is more than one
tab — choosing another host is then one key, not a remembered shortcut:

| Key | Action |
| --- | --- |
| `h` `l` / `Tab` `Shift+Tab` | Choose a tab (the pane follows, without taking focus) |
| `Enter` | Dive into the chosen tab's pane |
| `d` | Close that session tab |
| `Esc` | Up to the launcher |
| `1`…`9` | Jump straight to a tab from anywhere |

The strip marks the chosen tab with `▸`, and the status bar says `TABS`.

---

## 8. Dialogs

One visual language: a bordered card, a sentence-case title, the facts, and
right-aligned buttons where the destructive one is coloured and never the
default. `Esc` cancels, `Enter` confirms the default.

**Destination picker** — one component reused by copy, move, extract, compress
and deploy. A single list of "the other pane", "same directory", bookmarks,
recent destinations, then a browsable tree with a "new folder" row. Typing
filters; the current choice is always spelled out as a full `host:path`.

**Conflict** — shown per file with a side-by-side compare (size, mtime, optional
checksum) and a real decision:

```
┌ File already exists ─────────────────────────────────────────────┐
│  main.js                                                         │
│   local   ~/work/api/dist/main.js    980.1 KB  Oct 16 09:12  newer│
│   remote  prod-api:/srv/api/main.js  940.2 KB  Oct 15 14:40       │
│                                                                  │
│  [ Overwrite ]  [ Keep both ]  [ Skip ]  [ Always use newer ]     │
│  ☐ apply to the other 12 files                                   │
└──────────────────────────────────────────────────────────────────┘
```

`Keep both` renames to `main (2).js` — the safe default for people who don't
want to think, and the button order puts the reversible choice in reach.

**Extract** — source (read-only), detected format (overridable), destination
side, destination directory (picker, plus a "new folder named after the
archive" shortcut), strip components, and a safety summary: entry count, total
uncompressed size, and a red warning when a limit would be hit.

**Deploy plan** — a filterable list of `+ create`, `~ update`, `- delete`,
`= skip` with a summary footer and delete rows in red. Deletes are the reason
this dialog exists; they are never hidden behind a scroll.

**Dangerous targets** — replacing or deleting at a root-ish path
(`/`, `/etc`, `$HOME`, a mount point) turns the confirm button into a
type-the-path confirmation. Cheap, and it stops the one mistake that cannot be
undone. Since v1 has no trash and no undo, this confirmation *is* the safety
net, so it is not skippable and not remembered as "always allow".

---

## 9. Feedback and timing

| Duration | What the user sees |
| --- | --- |
| < 200 ms | Nothing (no flash of spinner) |
| 200 ms – 2 s | A spinner in the status bar, the action named |
| > 2 s | Determinate progress, speed, ETA, and cancel |
| Finished | A one-line toast naming what happened (`Deleted 3 items`, `Copied 200 files`) |

**Nothing omssh sends can hold the first frame.** The interface queries the
terminal only when asked to: an unanswered query holds the first paint and eats
the keys pressed while waiting, which a new user reads as "the app is broken".
The background colour comes from `COLORFGBG` when the terminal sets it, and
`Follow terminal background` performs that query on demand, where a brief pause
is expected. (Bubble Tea may make one capability probe of its own; it does not
hold input, and `ARCHITECTURE.md` §4 records when.)

A slim aggregate progress strip sits above the status bar whenever anything is
running, so a transfer is visible even when the queue is collapsed. `Alt+J`
opens the full queue (per-item rows, per-item pause/retry, item detail with
per-chunk state). Animations honour `motion = "reduced"`, which swaps spinners
for static text.

Errors are written as **what happened, why, and what to do**, with the action
clickable:

```
┌ Connection failed ───────────────────────────────────────────────┐
│  prod-api — could not start a session                            │
│  The server refused authentication for user 'deploy'.            │
│  Password authentication is disabled and no key was accepted.    │
│                                                                  │
│  [ Try a password ]  [ Pick a key ]  [ Edit host ]  [ Details ]   │
└──────────────────────────────────────────────────────────────────┘
```

A host-key change is never a warning to click past: it explains that this can
mean a rebuilt server *or* an attack, shows both fingerprints, and requires
choosing `Forget and reconnect` deliberately.

---

## 10. Onboarding

First run shows a single card, dismissible with `Esc`, that offers the two
things that actually matter:

```
┌ Welcome to omssh ────────────────────────────────────────────────┐
│  You have 7 hosts in ~/.ssh/config.                              │
│                                                                  │
│  [ Import 7 hosts ]   [ Add a connection ]   [ Skip for now ]     │
│                                                                  │
│  Getting around:                                                 │
│   • Ctrl+K  find any action        • F5      copy to the server   │
│   • Ctrl+C / Ctrl+V  copy & paste  • Ctrl+E  extract an archive   │
│   • Ctrl+Q  quit                   • F1/?    full help            │
└──────────────────────────────────────────────────────────────────┘
```

After that, the app teaches by doing: the palette prints shortcuts beside every
action, the action bar shows the verbs and their keys, and toasts name the
shortcut for what just happened. There is no mandatory tour and no modal
tutorial. `Ctrl+K` → `Tour` replays the six-line overview for anyone who wants
it.

---

## 11. Themes, colour and accessibility

- Built-ins: `midnight` (default dark), `dawn` (light), `mist` (low contrast),
  **`amber` (the default — warm, with an amber accent of its own)**, `mono` (no
  colour), `contrast` (high contrast), `auto` (follows the terminal background,
  read from `COLORFGBG`).

### 11.1 The signature accent

`omssh` has one signature colour, used for the focused pane border, the active
breadcrumb segment, the primary button, the current tab and the cursor line —
nothing else, so "where am I" is answered by the accent alone.

| Token | True colour | 256 | 16-colour |
| --- | --- | --- | --- |
| `accent` | `#57D9C9` (aqua/teal) | `80` | `bright cyan` |
| `accent.emphasis` | `#8FF5E6` | `123` | `bright cyan`, bold |

Teal rather than the usual violet: it reads as instrument-panel rather than
generic-AI-purple, it survives every colour profile, and it stays clear of the
semantic colours, which are reserved:

| Token | Meaning | Never used for |
| --- | --- | --- |
| `success` | ready, done, healthy | decoration |
| `warning` | unhealthy deploy, degraded link, unusual permissions | — |
| `danger` | delete, errors, changed host key | — |
| `muted` | metadata, hints, disabled | text you must read |

- Every style is defined for true colour, 256 and 16 colours, so a 16-colour
  terminal is a supported configuration rather than a broken one. `NO_COLOR` is
  honoured; `CLICOLOR_FORCE` overrides.
- Nerd Font icons when the terminal advertises one (or `icons = "nerd"`),
  otherwise a curated Unicode/ASCII set. No tofu boxes, ever.
- Accessibility: every action keyboard-reachable, every dialog `Esc`-dismissible,
  `motion = "reduced"`, `contrast` theme, optional `--no-icons`, and no
  information carried by colour alone (state dots always pair with a word or a
  glyph shape such as `● ready`, `○ idle`, `✖ error`).
- Selection and cursor are visually distinct from each other **and** from the
  focused pane, which matters more than any palette choice.

---

## 12. Responsiveness

| Width | Behaviour |
| --- | --- |
| ≥ 140 | Two panes, sidebar, preview, full metadata |
| 100–139 | Two panes, sidebar as an icon rail, preview on demand |
| 80–99 | One pane with `Tab` switching, name + size columns, abbreviated action bar |
| < 80 | Single column, action bar collapses to `F9 More…`, dialogs take the width |

The action bar degrades first, because it is redundant with the context menu and
the palette; the breadcrumb and the mode indicator are never dropped.

---

## 13. Terminal key limits (honesty section)

Some keys cannot be bound because terminals already use them. The app avoids
them rather than binding something that works for half the users:

| Not used | Why | Bound instead |
| --- | --- | --- |
| `Ctrl+H` | sends `Backspace` (0x08) on many terminals | `Alt+H` |
| `Ctrl+I` | is `Tab` (0x09) | — |
| `Ctrl+J` | is `Enter`/`LF` (0x0A) on many terminals | `Alt+J` |
| `Ctrl+M` | is `Enter`/`CR` (0x0D) | — |
| `Ctrl+S` | is `XOFF` flow control in some setups | `Alt+S` |
| `Ctrl+[` | is `Esc` | `Esc` |
| `Ctrl+B` | is the tmux prefix | `Alt+B` |
| `Ctrl+C` | conventionally quits, but is also the universal *copy* | **copy**; with nothing selected it shows `Nothing to copy · Ctrl+Q quits`, and `keys.copy = "ctrl+insert"` is offered for anyone who wants `Ctrl+C` to mean quit |

Tab, pane-switch and terminal-tab switching depend on the terminal emitting
modified keys. `Ctrl+PgUp/PgDn` and `Alt+1…9` cover most, and the palette covers
the rest; on startup the app probes capabilities and drops the shortcuts the
terminal cannot express instead of silently doing nothing.

**Quit is `Ctrl+Q` (or `F10`), never `Esc`.** `Esc` has three jobs already —
cancel a dialog, leave search, go up a level — and it is a single `0x1B` byte
that terminals also use to begin escape sequences, so it can arrive late or
ambiguously. Binding it to quit would mean that a reflexive `Esc`-`Esc` after a
search, or one stray byte from an arrow key, closes the app mid-transfer. `Esc`
therefore never quits; at the top level it answers with `Ctrl+Q to quit`, which
teaches the shortcut at exactly the moment someone wants it. Anyone who insists
can set `keys.quit = "esc"`.
