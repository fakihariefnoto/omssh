# omssh documentation

Everything you need to use `omssh`: the screens, every key, the command line and
the configuration file.

If you are looking for installation, see the [README](README.md).

- [Getting started](#getting-started)
- [The launcher](#the-launcher)
- [The dashboard](#the-dashboard)
- [Moving around](#moving-around)
- [Selecting and marking](#selecting-and-marking)
- [Copying and moving](#copying-and-moving)
- [Archives](#archives)
- [Editing, permissions and links](#editing-permissions-and-links)
- [Transfers](#transfers)
- [Connections](#connections)
- [Object storage](#object-storage)
- [Terminal](#terminal)
- [Deploy](#deploy)
- [Command line](#command-line)
- [Configuration](#configuration)
- [Themes and icons](#themes-and-icons)
- [Troubleshooting](#troubleshooting)
- [Keymap reference](#keymap-reference)

What is not here yet is in [ROADMAP.md](ROADMAP.md).

---

## Getting started

```sh
omssh            # the launcher
omssh prod       # the interface, connected to prod on startup
```

`omssh` starts on a launcher, not in a menu-less void. From there:

- **Manage connections** (`Ctrl+O`) — hosts and buckets, one screen per kind.
  `Esc` there returns here, to the launcher.
- **Dashboard** — asks which places it should show (local optional, hosts, buckets).
- **Browse local files** — skips the question and brings the local folder to the
  front, adding it back if a remote-only dashboard had removed it.
- **Browse local files** (`Esc` or `Enter`) — the file manager at the directory
  you started in.
- **Settings, theme and keys** (`Ctrl+,`) — themes, icons, motion, and the keymap.

Nothing is modal. You can open the palette (`Ctrl+K`), the keymap (`?`), or the
connections screen from anywhere, and `Esc` always takes you one step back.

## The launcher

```
    OMSHH
    Oh My SSH — SSH/SFTP with terminal UI

 ▸ [>] Manage connections        Ctrl+O
   [=] Dashboard                 choose panes
   [~] Browse local files        Enter
   [/] Go to a folder            Ctrl+L
   [+] Settings, theme and keys  Ctrl+,
   [x] Quit                      Ctrl+Q
```

`↑↓` chooses, `Enter` opens, `Esc` goes straight to the files. Connected hosts
appear above the menu, so returning to the launcher also offers the way back to
where you were.

## The dashboard

Each connection lives in a pane. One or two panes sit side by side; three or four
lay out 2×2 on terminals at least 140 columns wide.

```
sessions  │  1 localhost  │  ● 2 contabo  │  ● ▸ 3 ovh
┌──────────────────────────┐ ┌──────────────────────────────────┐
│LOCAL  /Users/me/project  │ │contabo  /srv/app                 │
│  ▸ cmd                   │ │  - index.html                    │
└──────────────────────────┘ └──────────────────────────────────┘
┌──────────────────────────┐ ┌──────────────────────────────────┐
│▌ovh  /home/deploy        │ │empty  no session                 │
│  ▸ logs                  │ │V chooses the panes               │
└──────────────────────────┘ └──────────────────────────────────┘
```

- **`w`** moves the focus between panes. The tab strip's highlight follows it, so
  you can see which of the four you are typing into.
- **`Tab` / `Shift+Tab`** switch sessions. **`1`…`9`** jump straight to one.
- **`V`** chooses which sessions are on screen. When you have more sessions than
  panes, the chooser opens on entering the dashboard and asks once.
- Each pane keeps its own path, selection and history. Switching panes never
  disturbs the other one.

The **sidebar** (left, hidden below 140 columns) shows:

| Section | Meaning |
| --- | --- |
| HOSTS | Live sessions, numbered like the tabs, `▸` on the open one |
| TRANSFERS | Running copies, with a bar, percentage and current file |
| MARKED | What is marked, its destination and size |
| BOOKMARKS | Placeholder for saved folders |
| THEME | The active theme |

## Moving around

These keys work in whichever pane has the focus.

| Key | Action |
| --- | --- |
| `j` `k` / `↓` `↑` | Move the cursor |
| `h` / `←` / `Backspace` | Parent directory |
| `l` / `→` / `Enter` | Open a folder, or view a file |
| `PgUp` `PgDn` | Page |
| `g g` / `Home` | First entry |
| `G` / `End` | Last entry |
| `~` | Home of this pane (yours locally, the session's remotely) |
| `-` | Back to the previous directory |
| `Alt+←` / `Alt+→` | History back and forward |
| `Ctrl+L` | Go to a path (`~` is home, names are relative) |
| `Ctrl+P` | Jump to a path, with suggestions |
| `Alt+F` | Find part of a name below the cursor |
| `/` / `Ctrl+F` | Filter this listing as you type; `Esc` clears |
| `.` | Hide or show dotfiles (visible by default) |
| `Alt+P` | Preview on the right of the focused pane |
| `o` | Reveal the cursor entry in the next pane |
| `Alt+C` | Compare the two panes (counts: only here, only there, both) |
| `Y` | Sync browse: the next pane follows the same path |
| `Ctrl+R` | Refresh |

Typed paths are forgiving: `~` means the pane's home, `~/Documents` is inside it,
and a bare name is relative to where the pane is. A path that is not a folder says
so and leaves the pane where it was.

## Selecting and marking

There are two different things, and both are useful:

- **Selecting** (`Space`, `Shift+↑/↓`, `Ctrl+A`, `Alt+A`) builds a set inside the
  pane, shown with a `*` marker. Actions that take several files use it.
- **Marking** (`y`, `x`) builds the copy set, which survives moving around and is
  what the paste uses.

Marking is cumulative: mark a file, walk to the next, mark that too. With files
selected, one `y` marks all of them. `y` on a file that is already marked takes
the mark back; `y` then `x` on the same file turns it into a move.

Marked files appear twice — names and sizes in the sidebar, count, destination
and keys in the bar under the panes:

```
▸ 3 items · 12.4 MB copy → contabo  /var/www    p paste · A options · s review · Esc cancel
```

`Esc` cancels the marked set. `s` reviews it (what, where, the conflict rule).

## Copying and moving

`p` pastes the marked files into the **focused** pane. If you marked and pasted
without moving, it uses the next pane instead.

| Key | Action |
| --- | --- |
| `y` / `Ctrl+C` | Mark for copy |
| `x` / `Ctrl+X` | Mark for move |
| `p` / `Ctrl+V` | Paste into the focused pane |
| `A` | Paste options |
| `C` / `M` | Copy to… / Move to…, choosing any folder on either side |
| `o` | Reveal the cursor entry in the next pane |

`C` and `M` accept a side: `local:/path` or `remote:/path`. Without a side the
current pane's filesystem is assumed.

### Paste options

`A` opens one card with the three decisions that matter:

```
Paste options
3 items · copy → contabo  /var/www

m  mode       copy
u  conflicts  [x] update if newer
o             [ ] overwrite
s             [ ] keep what is there

n  after      [x] the files themselves
e             [ ] unpacked into site/
c             [ ] packed into assets.tar.gz
Enter pastes · Esc cancels
```

- **mode** — copy or move, without re-marking.
- **conflicts** — what happens when the destination already has the file:
  update if newer (default), overwrite, or keep what is there.
- **after** — the shape of the result:
  - *the files themselves* — an ordinary copy;
  - *unpacked into site/* — one marked archive arrives already extracted, into a
    folder named after it;
  - *packed into assets.tar.gz* — the destination gets one archive instead of the
    loose files. Nothing is copied and then compressed: the archive is written
    straight from the source.

A move deletes the sources only after they arrive, and clears the marks. A copy
keeps them, so the same set can go to a second place.

## Archives

Supported: `.zip`, `.tar`, `.tar.gz`/`.tgz`, `.tar.bz2`.

| Key | Action |
| --- | --- |
| `Ctrl+E` | Extract… — asks `local:/path` or `remote:/path`, defaulting to the next pane |
| `Ctrl+G` | Compress the selection… — the format follows the name you type |
| `v` on an archive | List its contents, with sizes |

Extraction writes into a folder named after the archive's stem (`site.tar.gz` →
`site/`). Members that would escape the destination are refused before anything is
written, and a symlink whose target leaves the archive is refused too. Entries
that cannot be created (devices, fifos) are skipped.

Zip needs random access, so a remote zip is spooled to a temporary local file
first — one extra copy in exchange for working over SFTP at all.

## Editing, permissions and links

| Key | Action |
| --- | --- |
| `e` | Edit in `$EDITOR` |
| `P` | Change permissions (octal, e.g. `644`) |
| `L` | Create a symlink to the cursor entry |
| `n` | New file |
| `N` | New folder |
| `r` | Rename |
| `d` | Delete (always asks; there is no trash and no undo) |
| `m` | Context menu: every file action |
| `v` | View: preview pane, or an archive's contents |

**Remote editing** fetches the file to a temporary path that keeps its name (so
your editor's syntax highlighting still works), runs the editor, and writes it
back. Files over 16 MB are refused with their size, because "download it, hold it
in RAM, upload it" is not a favour.

## Transfers

Every copy, move, extraction and archive write registers a transfer:

```
TRANSFERS
copy → contabo
██████░░  75%
assets/logo.svg
```

Reports arrive per item, and every 100 ms inside a large file. A finished job
stays visible for ten seconds saying `done`. The first running job also appears in
the status bar (`copy → contabo 30%`), so it is never invisible on a narrow
terminal. Transfers run off the update loop; the interface keeps answering.

## Connections

`Ctrl+O` opens the connection manager. It has one screen per kind of place, and
`Tab` / `Shift+Tab` moves between them:

```
[1 Overview]  2 SSH  3 S3  4 GCP·soon  5 Azure·soon  6 WebDAV
```

The **Overview** lists everything, hosts and buckets together. **SSH**, **S3** and
**WebDAV** each list their own. GCP and Azure are tabs that say when they arrive
instead of showing a convincing empty list — GCS already works through the S3 tab,
with HMAC keys.

`a` adds whatever the current screen is about — directly on SSH, S3 and WebDAV, and
with a chooser on the overview:

```
Add
What kind of place?
▸ SSH host               a machine in ~/.ssh/config
  AWS S3 bucket          region, bucket, optional prefix
  Cloudflare R2 bucket   account endpoint, bucket
  Backblaze B2 bucket    region, bucket (S3 API)
  Google Cloud Storage   bucket, HMAC keys (S3 API)
  Alibaba Cloud OSS      region, bucket (S3 API)
  WebDAV server          Nextcloud, Box, SharePoint, a NAS
```

The kind is chosen here and shown in the form as `type`; it is not a field you
type, so a bucket profile cannot end up claiming a provider it was not given.

`Ctrl+O` lists the hosts in `~/.ssh/config`. Nothing is copied into another file.

| Key | Action |
| --- | --- |
| `Enter` | Browse the host (SFTP) |
| `t` | Open a full terminal on it |
| `f` | Pin/unpin — `★`, pinned hosts sort first |
| `x` | Hide this host |
| `H` | Show everything, including hidden and code hosts |
| `Q` | Disconnect, keeping the tab |
| `e` | Edit the host block |
| `a` | Add a host |
| `d` | Delete the host block (asks) |
| `r` | Reload the file |
| `o` | Open the file in `$EDITOR` |
| `/` | Filter |

**Ordering** is favourites first, then recently used hosts (newest first, ten
remembered), then the rest in file order. Favourites, hidden hosts and recents are
saved to your config.

**Code hosts** — `github.com`, `gitlab.com`, `bitbucket.org`, `codeberg.org`,
`git.sr.ht`, `ssh.dev.azure.com`, or any entry whose user is `git` — are hidden
by default. They are in the file for `git push`, not for logging in. `H` shows
them.

**Authentication** uses your agent and keys, then stored passwords (OS keyring),
then asks. A host key that has never been seen is a decision: `Remember`, `Once`
or `Refuse`, with the fingerprint shown. A **changed** key is never a prompt — it
is a hard stop, because that is what a changed key means.

## Object storage

S3-compatible storage is a place like a host: a pane, a tab and the same copy,
archive and deploy behaviour as a server. One client covers five providers, because
they differ in endpoint and addressing style, not in the operations omssh uses:

| Provider | `provider` | Endpoint |
| --- | --- | --- |
| AWS S3 | `s3` | regional, from the SDK |
| Cloudflare R2 | `r2` | `https://<account>.r2.cloudflarestorage.com` (required) |
| Backblaze B2 | `b2` | `https://s3.<region>.backblazeb2.com` (derived) |
| Google Cloud Storage | `gcs` | `https://storage.googleapis.com`, HMAC keys |
| Alibaba Cloud OSS | `oss` | `https://s3.oss-<region>.aliyuncs.com` (derived) |
| WebDAV | `webdav` | any `https://…/dav/` collection (Nextcloud, Box, SharePoint, a NAS) |

OSS rejects path-style requests, so it is always virtual-hosted; B2 and R2 are
path-style. The form needs a bucket and a region for AWS/B2/OSS, an account
endpoint for R2, and only a bucket for GCS.

**WebDAV is a different client**, because it is a different protocol: real
directories, a real `MOVE` (an actual rename, not copy-then-delete), and ETags. It
needs a URL and optionally a username and password, which are stored in the keyring
like a bucket's key pair.

```yaml
cloud:
  profiles:
    backups:                       # AWS S3
      provider: s3
      bucket: my-backups
      region: eu-central-1
      prefix: omssh                # optional: root the pane inside the bucket
    r2-assets:                     # Cloudflare R2
      provider: r2
      bucket: assets
      endpoint: https://<account>.r2.cloudflarestorage.com
      region: auto
    b2-archive:                    # Backblaze B2
      provider: b2
      bucket: my-archive
      region: us-west-004
    gcs-backups:                   # Google Cloud Storage (S3 XML API, HMAC keys)
      provider: gcs
      bucket: my-backups
    oss-cn:                        # Alibaba Cloud OSS
      provider: oss
      bucket: my-bucket
      region: cn-hangzhou
    nextcloud:                     # WebDAV
      provider: webdav
      endpoint: https://cloud.example.com/remote.php/dav/files/me/
      username: me                 # the password waits in the keyring
```

**Credentials** are either entered in the form or taken from the standard AWS
chain: `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, `~/.aws/credentials`, SSO,
or an instance role. Nothing secret belongs in the config file.

A pair entered in the form goes to the **OS keyring**: service `omssh`, account
`password:cloud:<profile>`, the value being the id and the secret. On macOS that is
Keychain Access (search "omssh"); on Linux it is the Secret Service. If the machine
has no keyring, nothing is stored and the chain is used — the form says so instead
of pretending it saved. `omssh cloud forget <profile>` removes the pair.

**Adding one from the interface:** `Ctrl+O`, then `a` (or `a` on the S3 screen,
which fills the type in for you). The form asks for a name, the type (`s3` or
`r2`), the bucket, the region, the endpoint (R2 only) and an optional prefix;
`Enter` writes it to the config. The profiles appear in the list
after your hosts and `Enter` opens one. `t` says there is no shell, because there
is not.

**What object storage cannot do**, and the interface says so instead of failing
halfway:

- buckets have no permissions and no symlinks (`P` and `L` report "not supported");
- a bucket rename is copy-then-delete, and a failure between the two leaves both
  copies. A WebDAV rename is one `MOVE`, which is atomic on every server that
  implements the specification;
- no append or partial write: a write is buffered locally and put on close;
- directories are prefixes. An empty folder exists only as a marker object.

From the shell, without the interface:

```sh
omssh cloud list
omssh cloud check backups      # lists the root, and explains a 403 vs a 404
omssh cloud ls backups logs/
```

`check` exists because "wrong bucket", "wrong region" and "this key cannot see
it" look identical from the outside: it separates them.

## Terminal

`t` on the connections list, or `T` in a file pane, hands the terminal to `ssh -t
<alias>`. It is the real thing: vim, top, tmux, colours and modes work because
nothing stands in their way. When you exit, the interface takes the terminal back
and refreshes the panes.

The alias is passed as-is, so ports, keys, users and jump hosts come from
`~/.ssh/config` rather than from a second, drifting implementation.

## Deploy

A deploy is a copy with an opinion: this local tree goes to that remote root.

```yaml
deploy:
  profiles:
    site:
      host: contabo
      local_root: ~/Projects/site
      remote_root: /var/www/site
      mode: sync            # sync (mirror) | update (overlay)
      exclude:
        - node_modules
        - "*.log"
        - .env
      include_hidden: false
```

`Alt+D` lists profiles, `Enter` diffs the local tree against the remote one and
shows what would happen:

```
Deploy site
2 create · 1 update · 1 delete · 12.4 MB
+ index.html
+ css/site.css
~ about.html
- stale.html
Enter deploys · Esc back
```

`Enter` on the plan writes it. In `sync` mode, files the local tree does not have
are removed; an exclusion protects in both directions. A deploy with nothing
changed does nothing, which is what makes it safe to press twice. Every file is
re-checked at apply time rather than trusting the plan.

## Command line

```sh
omssh                            # the interface, in this directory
omssh <host>                     # the interface, connected on startup
omssh exec <host> -- <command>   # one command, its exit status
omssh keys                       # the effective keymap
omssh config path                # where the config lives
omssh config init                # write a commented example
omssh config check               # validate it, with line numbers
omssh version
```

`exec` wires stdin, stdout and stderr; `--tty` allocates a terminal on the remote
side for commands that want one. It exits with the remote command's status, so it
composes in a script. An unknown host key prints its fingerprint and asks before
trusting it; passwords are read with echo off.

## Configuration

Dotfiles are visible by default. `show_hidden: false` starts with them hidden, and
`.` toggles them at any time in the focused pane.

`~/.config/omssh/config.yaml` — or `$XDG_CONFIG_HOME/omssh/config.yaml` when that
variable is set. The same location is used on macOS and Linux, so one config
works on both; `omssh config path` prints it. `--config <path>` overrides it.
Unknown keys are an error, not a shrug.

```yaml
version: 1

ui:
  theme: amber          # amber | midnight | dawn | mist | ember | mono | contrast | auto
  icons: unicode        # unicode | ascii | nerd
  motion: full          # full | reduced
  sidebar: true
  preview: true
  show_hidden: true     # dotfiles visible; "." toggles them
  paint_background: auto  # auto | always | never (see Themes)
  start: home           # home | files
  width_margin: 0       # see Troubleshooting

connections:
  favorites: []         # pinned hosts, in order
  hidden: []            # hosts left out of the list
  recent: []            # filled in as you connect

transfers:
  workers: 4

deploy:
  profiles: {}
```

`omssh config init` writes this with comments.

## Themes and icons

`Ctrl+,` opens settings: the theme list, the background mode, **hidden files**,
icons, motion, and the keymap. Every choice is written to `ui.theme` and its
neighbours in `~/.config/omssh/config.yaml`, so it survives the process:

| Row | Enter |
| --- | --- |
| Theme | applies it |
| Background | auto → always → never |
| Hidden files | hidden ⇄ shown (the same as `.`) |
| Icons | unicode → ascii → nerd |
| Keyboard and help | opens the keymap | Themes adapt to light and dark terminals; `auto` follows the
terminal's background on demand (`Ctrl+K` → *Follow terminal background*).

The card also shows what the terminal says about itself (`terminal reads as dark`,
`light` or `unknown`), because that is what the background rule acts on. Each
theme is labelled `light` or `dark` for the same reason: a light theme on a dark
terminal can only be readable if it paints its own background.

If the answer is wrong, set *Background* to `never` and the theme leaves the
terminal's own colours alone — but then pick a theme that matches it. Dawn on a
dark terminal without painting is dark text on a dark screen, and the settings card
says so in words.

**Backgrounds.** A theme paints its background (and its foreground) only when it
disagrees with the terminal — a light theme on a dark one — and only when the
terminal said which it is (via `COLORFGBG` or *Follow terminal background* in the
palette). Otherwise the terminal's own background, transparency included, is left
alone. `ui.paint_background: always` forces it, `never` turns it off.

Icons come in three sets: `unicode` (default, good on any font), `ascii`, and
`nerd` (Nerd Font required — shows `?` without one). Nothing in the interface
depends on a Nerd Font.

## Troubleshooting

**The layout shifts while typing, or a line looks wrapped.** Some terminals draw
"ambiguous width" characters one cell wider than omssh counts them. Run
`omssh --width-margin 2`, or set `ui.width_margin: 2`. The frame is otherwise
always exactly the terminal's size.

**`Ctrl+K`/`Ctrl+C` do nothing on macOS.** Use **Control**, not Command. ⌘K is
"Clear to Start" in Terminal and iTerm2, and ⌘C is Copy; the terminal consumes
both and the app never sees them.

**Icons show `?`.** Your font is not a Nerd Font. Keep `icons: unicode` (the
default) or set `ascii`.

**A host key changed.** omssh refuses instead of asking. Check whether the server
was reinstalled or rebuilt; if so, remove the old entry from `~/.ssh/known_hosts`
and reconnect.

**Passwords are asked every time.** There is no keyring available, or the stored
password was refused and has been forgotten. `omssh` never prompts for a keyring
at startup; it uses one if the OS provides it.

**`~` in a path.** Inside the interface `~` always means the pane's home — yours
locally, the session's remotely. In a deploy profile, `local_root` may use `~`,
but `remote_root` must be absolute, because `~` over SFTP would resolve to
something the interface cannot see.

**A transfer seems to do nothing.** Watch the status bar or the sidebar's
TRANSFERS section; a small copy finishes in milliseconds and stays as `done` for
ten seconds. `Alt+J` reports the running job.

## Keymap reference

The full list is always available in the app with `?`, and on the command line
with `omssh keys`. The grammar is small:

- **plain letters** are commands;
- **digits** switch session tabs;
- **`Ctrl`+letter** opens a dialog or does an application action;
- **`Alt`+letter** toggles a view;
- **`Esc`** goes back one level — it never quits.

### Files

| Key | Action |
| --- | --- |
| `j` `k` / `↓` `↑` | Move |
| `h` `l` | Parent / open |
| `Space` | Select and advance |
| `Shift+↑` `Shift+↓` | Extend selection |
| `Ctrl+A` / `Alt+A` | Select all / invert |
| `y` / `x` | Mark for copy / move |
| `p` | Paste into the focused pane |
| `A` | Paste options |
| `C` / `M` | Copy to… / Move to… |
| `v` / `e` | View / edit |
| `r` / `d` | Rename / delete |
| `n` / `N` | New file / folder |
| `P` / `L` | Permissions / symlink |
| `Ctrl+E` / `Ctrl+G` | Extract / compress |
| `m` | Context menu |
| `s` | Review marked files |
| `.` | Hidden files |
| `Alt+P` | Preview |
| `Alt+C` | Compare panes |
| `Y` | Sync browse |
| `w` | Switch pane |
| `o` | Reveal in next pane |
| `V` | Choose panes |

### Sessions

| Key | Action |
| --- | --- |
| `Ctrl+O` | Connections |
| — | The launcher's Dashboard entry asks which places to show |
| `Tab` / `Shift+Tab` | Next / previous session |
| `1`…`9` | Jump to a session |
| `[` / `]` | Previous / next session |
| `w` | Switch pane |
| `T` | Terminal on this host |
| `Q` | Disconnect, keeping the tab |
| `Ctrl+W` | Close the tab |
| `Esc` | Tab strip (with several tabs), then the launcher |

### Application

| Key | Action |
| --- | --- |
| `Ctrl+K` | Command palette — every action, searchable |
| `?` / `F1` | Keymap and help |
| `Ctrl+,` | Settings, theme and keys |
| `Ctrl+Q` | Quit |
| `Alt+B` | Sidebar |
| `Alt+J` | Running transfers |
| `Alt+D` | Deploy |
| `Esc` | Back |
