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
- **Terminal** / **Tunnel** — choose a connected host, or a configured one that is
  connected on the way.
- **Dashboard** — asks which places it should show (local optional, hosts, buckets).
- **Local files** — one entry for the two ways into this machine: **Browse local
  files** opens the folder you were in, **Go to a folder…** asks for a path first.
  Either answer is followed by the dashboard's remote-pane chooser, up to four.
- **MCP for agents** — the stdio setup and the recent call log.
- **Settings, theme and keys** (`Ctrl+,`) — themes, icons, motion, and the keymap.

Nothing is modal. You can open the palette (`Ctrl+K`), the keymap (`?`), or the
connections screen from anywhere, and `Esc` takes you one step back. On the
launcher — the outermost screen — `Esc` leaves, after asking if a transfer is
still running.

## The launcher

```
    OMSHH
    Oh My SSH — SSH/SFTP with terminal UI

 ▸ [>] Manage connections        Ctrl+O
   [=] Dashboard                 choose panes
   [t] Terminal & Tunnel         choose terminal or tunnel
   [H] History                   terminals, tunnels, agent calls
   [~] Local files               browse or go to a folder
   [M] MCP for agents            setup and log
   [+] Settings, theme and keys  Ctrl+,
   [x] Quit                      Ctrl+Q
```

`↑↓` chooses, `Enter` opens, and `Esc` quits the program; with a transfer still
running it asks first, because quitting cancels it. `Ctrl+Q` and the Quit row do
the same thing, with the same check, and close SSH sessions, tunnels and embedded
terminals before leaving. Connected hosts
appear above the menu, so returning to the launcher also offers the way back to
where you were.

**Terminal & Tunnel** asks which job first — a shell, or a port forward — and
only then which host. The host chooser lists connected hosts first, then the rest
of `~/.ssh/config` marked *not connected*. For a terminal, `Enter` opens the
embedded one, `t` hands over to `ssh` (which works whether or not the session is
open here), and choosing an offline host **connects it first** and then opens what
you asked for.

A tunnel is two steps, because `L:8080:host:port` is ssh's syntax rather than a
person's. The first is the direction:

```
▸ Local    listen here; the server dials the target
  Remote   listen on the server; this machine dials
  Dynamic  a SOCKS5 proxy on this machine
```

`←/→` or `↑/↓` choose, `Enter` goes to the addresses. The second step is a form —
bind, port, target host, target port — with a default in every field and a hint
under it. Ports are suggested after probing for a free one, and a busy port is
caught before anything starts, with the next free port named in the error. A
remote forward's port is checked by the server and reported when it answers.

**Remote forwards and the public internet.** A remote forward listens on the
server, not here. With the default bind `127.0.0.1` only the server itself can
connect — `http://<server-ip>:9000/` will not work. To publish it:

1. set the server bind to `0.0.0.0`, the server port to the public port
   (`9000`), and the local port to the app on this machine (`8080`);
2. on the server, allow it:

   ```sh
   # /etc/ssh/sshd_config.d/omssh.conf
   AllowTcpForwarding yes
   GatewayPorts clientspecified     # or "yes" to force wildcard binds
   ```

   ```sh
   sudo sshd -t                     # validate before reloading
   sudo systemctl reload ssh        # or sshd, depending on the distribution
   ```

   `clientspecified` lets this client ask for `0.0.0.0`; `yes` makes every remote
   forward wildcard whether asked or not. `no` (the default) forces loopback.
   `AllowTcpForwarding` must not be `no`; `local` and `remote` are the useful
   middle grounds (`local` would block this direction entirely).
3. open the port in the provider's firewall/security group **and** in the
   server's own firewall:

   ```sh
   sudo ufw allow 9000/tcp                                    # ufw
   sudo firewall-cmd --permanent --add-port=9000/tcp &&      sudo firewall-cmd --reload                               # firewalld
   ```

4. confirm what the server actually bound after the tunnel connects:

   ```sh
   ss -ltn 'sport = :9000'
   # 0.0.0.0:9000   means public (GatewayPorts took effect)
   # 127.0.0.1:9000 means loopback only
   ```

   Then test from outside with `curl -v http://<server-ip>:9000/`.

omssh cannot override sshd: it asks the server to listen, and the server decides
whether that is loopback or public. The form says which case you are in, and a
wildcard bind shows a reminder after starting. Exposing a port this way publishes a
service on the internet; leave `GatewayPorts no` and use a local forward from the
client when you only need access for yourself.

Below the fields is the **simulation**: the path the bytes take and what
`localhost` means at each end. For a local forward it reads like
`127.0.0.1:8080 ───SSH──▶ db.internal:5432`, with the sentence "localhost in the
target means the server, not your laptop". The form also names the real connection
(`via contabo · deploy@203.0.113.10:22`).

**Saving a tunnel.** `s` in the form asks for a name and writes the forward to
`tunnels.profiles` in the config. `Saved tunnels` appears in the tunnel host
chooser; from there `Enter` starts it, `e` edits it, and `x` deletes it (after
asking). Starting a saved tunnel also keeps any edits made in that form, so the
saved copy and what ran do not drift apart. A saved profile is what it says:

```yaml
tunnels:
  profiles:
    dev-db:
      host: contabo
      kind: local
      listen: 127.0.0.1:8080
      target: db.internal:5432
```

Starting one opens the **Tunnels dashboard**, a screen of its own rather than a
card over the launcher. The left column is every running forward with its live
counters; the right column is the selected one: where it listens, where it goes,
connections, bytes received and sent, and the recent connections with the address
that used it. `↑↓` chooses, `x` stops, `n` starts another on the same host, `p`
starts the same tunnel on **another free port** (useful when one service is already
forwarded), `h` opens History, and `Esc` returns to the launcher while the tunnels
keep running. **MCP** shows the stdio configuration, copies it with `c`, and lists
the last agent calls with `r` to refresh.

## History

**History** is one launcher row and one screen, split by tab: Terminal, Tunnel,
Agent, All. The launcher opens it on Terminal; Tab moves across. `Tab` changes the filter, `↑↓`
chooses, `Enter` opens the record, `v` reads a terminal conversation log, `d`
deletes the selected record after asking, and `Esc` returns to the launcher. The
rows are newest first.

- **Terminal** rows are `.cast` recordings. `Enter` replays one in the player;
  `v` shows the same conversation as plain text, with the escape sequences gone.
- **Tunnel** rows are sessions from `<state>/tunnels.jsonl`. Tunnel metadata is
  **recorded automatically** — there is nothing to enable, and it is what the
  audit log uses. A running session is marked `running`; `s` stops it without
  deleting the record, and `x` inside the tail does the same. `Enter` opens the
  **tail**: when it started, where it listened, every connection with its byte
  counts. If the tunnel is still running, `Enter` opens the **live dashboard**
  instead — counters and recent connections, not a historical record.
- **Agent** rows are MCP tool calls, always labelled `agent · <tool>` so they are
  never mistaken for something a person did. Deleting one removes that call from
  the log.

Deleting a record is scoped to its own store: a recording file, one tunnel
session's metadata, or one agent call. A running tunnel keeps running when its
metadata row is deleted, and says so in the confirmation.

The chooser is the dashboard's question — which place — with the connect step
folded in, so "I have not connected yet" is not a dead end: pick the host and the
right thing happens. "Manage connections…" at the bottom is still the way to add
or edit one.

**Local files** is one row rather than two: it asks whether to browse the current
folder or go somewhere, then opens the dashboard chooser so the remote panes can
be ticked beside it — the same question the **Dashboard** row asks, with the local
half already answered. `Esc`/`Enter` from a pane still goes straight to the files,
and `Ctrl+L` still jumps to a path directly.

## The dashboard

Each connection lives in a pane. One or two panes sit side by side; three or four
lay out 2×2 on terminals at least 140 columns wide.

Remote pane headers carry the host's **load, memory and disk** percentages
beside the item count, refreshed every ten seconds. The numbers are sampled in
the background; a pane never waits on a shell command to draw.

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
  panes, the chooser opens on entering the dashboard and asks once. In the
  chooser, **←/→ and ↑/↓** both move the selection, **Space** ticks or unticks a
  place, **Enter** applies it, and **Esc** goes back to where you came from — the
  launcher when Dashboard opened it, the files when `V` did.
- Host cards in the chooser show the same **load, memory and disk** numbers as
  the pane headers, sampled while the chooser is open and refreshed every few
  seconds. The local card says "this machine" instead: those commands are for
  the far side.
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

`Alt+J` opens the transfer screen: one block per job with a bar, the file being
copied, bytes done of total, average speed and elapsed time. It also opens by
itself when a paste is large — five files or 100 MB — unless something else is
already open. `Esc` closes it and the copy keeps going.

Copies run several files at once, as many as `transfers.workers` says (four by
default). One file is still one stream; the parallelism is across files, which is
what makes a folder of small files over a slow link bearable.

## Connections

`Ctrl+O` opens the connection manager. A bucket profile is edited in place with
`e` — the form opens filled, and leaving the secret blank keeps the stored one —
and deleted with `x`; editing `config.yaml` by hand is never required. The manager
has one screen per kind of place, and
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

**Password connections without ssh's file.** A host added with a password in the
form is saved under `hosts:` in omssh's own config, with the secret in the OS
keyring; `~/.ssh/config` is not touched. It appears in the same manager, marked
`omssh config`, and `e` edits it or `x` deletes it — no ssh file to find, no
password prompt on every connect. Leaving the password blank on edit keeps the
stored one.

```yaml
hosts:
  pw-db:
    host: 203.0.113.10
    user: deploy
    port: 22
```

Hosts from `~/.ssh/config` still work exactly as before; the two lists are merged
and the Source column says which is which.

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
| Google Cloud Storage | `gcs` | `https://storage.googleapis.com` (HMAC keys) or the native client |
| Alibaba Cloud OSS | `oss` | `https://s3.oss-<region>.aliyuncs.com` (derived) |
| Azure Blob Storage | `azure` | `https://<account>.blob.core.windows.net/` (overridable) |
| WebDAV | `webdav` | any `https://…/dav/` collection (Nextcloud, Box, SharePoint, a NAS) |

OSS rejects path-style requests, so it is always virtual-hosted; B2 and R2 are
path-style. The form needs a bucket and a region for AWS/B2/OSS, an account
endpoint for R2, and only a bucket for GCS.

**Google Cloud Storage has two clients.** `auth: s3` (the default) uses HMAC
keys through the S3-compatible XML API. The native client is chosen by a different
auth value and needs no HMAC key at all:

| `auth` | What it needs | Where the credential comes from |
| --- | --- | --- |
| `s3` (default) | an HMAC key id and secret | the OS keyring |
| `adc` | nothing in the profile | Application Default Credentials: `gcloud auth application-default login`, a metadata server, workload identity |
| `service_account` | `credentials_file` (a JSON path) or the JSON in the keyring | the file, or the profile's stored secret |
| `impersonate` | `service_account` (the target email) | ADC proves who you are; GCS sees the target account |

`credentials_file` and `service_account` are not secrets and live in the config;
a JSON key kept in the keyring is one value, like every other secret here.

**Azure Blob Storage is a different client too**, because it is a different
protocol: containers and a key-per-blob namespace, with three ways to prove access:

| `auth` | What it needs | Where the secret goes |
| --- | --- | --- |
| `key` | the storage account key | the OS keyring |
| `sas` | a shared access signature | the OS keyring |
| `entra` (default) | nothing in the profile | the environment's credential chain (az login, managed identity, workload identity) |

The account name and container live in the config; a custom `endpoint` covers
Azurite (`http://127.0.0.1:10000/devstoreaccount1`) and sovereign clouds. Azure
REST has no server-side copy that does not need the source authorized, so a rename
streams the bytes through omssh and is not atomic — the same caveat as a bucket.

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
    gcs-hmac:                      # Google Cloud Storage (S3 XML API, HMAC keys)
      provider: gcs
      bucket: my-backups
      auth: s3
    gcs-backups:                   # Google Cloud Storage (native client)
      provider: gcs
      bucket: my-backups
      auth: adc
    oss-cn:                        # Alibaba Cloud OSS
      provider: oss
      bucket: my-bucket
      region: cn-hangzhou
    blob-site:                     # Azure Blob Storage
      provider: azure
      account: mystorageaccount
      bucket: site                 # the container
      auth: entra                  # key · sas · entra
      # endpoint: https://mystorageaccount.blob.core.windows.net/
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
`Enter` writes it to the config. The type is a chooser in the form — S3, R2, B2,
OSS — so the S3 screen is not an S3-only form. The GCS and Azure screens ask for
their auth mode instead of a region. The profiles appear in the list
after your hosts and `Enter` opens one. `t` says there is no shell, because there
is not.

**Object metadata in the preview.** A bucket file's preview starts with the
facts the listing does not carry: content type, size, modified time, storage
class and ETag. Local and SFTP files do not have those fields, so their preview
does not pretend to.

**Copy link** (`u`) signs a temporary download URL for the object. It asks for an
expiry in minutes (15 by default), copies the URL to the clipboard, and also shows
it in a prompt — a headless machine may have no clipboard, and a link nobody can
select would be a dead end. Only S3-compatible profiles can sign a link today:
Azure needs a delegation key and GCS a service-account key pair, and the action
says so rather than producing a URL that does not work.

**Copies inside one bucket stay in the bucket.** When both sides are the same
S3-compatible or GCS filesystem, the copy is one server-side request instead of a
download and an upload; the progress bar reports the item once. Across providers
or buckets the bytes travel as before.

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

`t` on the connections list (`Ctrl+O`, then the host), or `T` in a file pane,
hands the terminal to `ssh -t <alias>`. `Enter` on a host in that list browses its
files instead, and `U` opens the embedded terminal. It is the real thing: vim, top, tmux, colours and modes work because
nothing stands in their way. When you exit, the interface takes the terminal back
and refreshes the panes.

The alias is passed as-is, so ports, keys, users and jump hosts come from
`~/.ssh/config` rather than from a second, drifting implementation.

### The embedded terminal, and replay

`U` opens an **embedded** terminal on the focused pane's host: the same SSH
connection the pane uses, a real PTY, and a terminal emulator drawing the server's
answers inside omssh. `Ctrl+]` **detaches**: the shell, its scrollback and its
recording keep running, and **Terminal & Tunnel** → Terminal marks the host
`terminal detached · Enter reattaches`. `T` is unchanged, and remains the right
choice when you want the terminal handed over entirely.

Both kinds of history are **on by default**: embedded terminals are recorded
(and `Ctrl+,` has a *Record sessions* toggle for turning that off), while tunnel
sessions are metadata and are always written to `tunnels.jsonl`. Nothing is
captured from tunnel traffic. The
settings are

```yaml
sessions:
  record: true
  record_input: true     # keystrokes in the recording; false records output only
  keep_days: 0           # 0 keeps every recording
  max_mb: 512            # oldest go first when the folder exceeds this
```

and every embedded session writes an asciinema v2 `.cast` file to
`<state>/recordings/<host>-<timestamp>.cast`, mode `0600`. The folder is pruned
before a new recording starts: `keep_days` deletes by age, `max_mb` by total size,
oldest first.

**A recording contains what the remote side printed and, with `record_input`, what
you typed.** A password typed into the embedded shell is therefore in the file;
the same warning applies to any terminal recorder. omssh's own password prompts
happen outside the shell and are never recorded. The external terminal records
nothing, because omssh never sees those bytes.

Recordings are the **Terminal** tab of **History** on the launcher. `Alt+R`
replays the newest directly, or asks for a path.

`Alt+R` replays a recording, defaulting to the newest one. The player takes
`space` to play or pause, `←`/`→` to seek five seconds, `+`/`-` to change speed
from 0.25× to 8×, `/` to find text later in the file, `0` to restart, and `q` to
close. `.cast` files also play in `asciinema play` and in any asciinema-compatible
player — the format is the point, not a private log.

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
omssh tunnel <host> -L 8080:db:5432
omssh mcp                        # MCP server over stdio, for agents
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

### Tunnels

`omssh tunnel <host>` forwards TCP over the same connection the interface uses:

```sh
omssh tunnel contabo -L 8080:db.internal:5432      # this machine → far network
omssh tunnel contabo -R 9000:localhost:3000       # far machine → this network
omssh tunnel contabo -D 1080                      # SOCKS5 on 127.0.0.1:1080
omssh tunnel contabo -L 8080:db:5432 --json-lines # events as they happen
```

In the interface, **Tunnel** on the launcher (or `Alt+T`) asks for the same spec with a kind
prefix — `L:8080:db:5432`, `R:9000:localhost:3000`, `D:1080` — and `Alt+Y` opens
the list of running forwards with connection counts and bytes each way. `x`
stops one; `Esc` closes the list and leaves the tunnels running. Both actions are
in `Ctrl+K`, so they are reachable from any screen.

The shape is `ssh`'s: `[bind:]port:host:port` for `-L` and `-R`, `[bind:]port`
for `-D`. A forward with no bind address listens on **127.0.0.1**, not on every
interface — a private database should not appear on café Wi-Fi because a colon was
missing. Several `-L`/`-R`/`-D` flags may be given at once.

The connection is the pool's: when it drops, the next accepted connection
reconnects, and a dial that fails on a stale connection is retried once. A port
that is already taken fails with the address and, when `lsof` is available, the
program holding it.

**A bastion is a forwarding rule with a name.** `omssh bastion <host> --listen
127.0.0.1:2222 --to db.internal:22` accepts TCP locally and carries it through the
SSH host to the target. What it records is **metadata**: who connected, where it
went, how many bytes each way, and when. It does not capture traffic, and it does
not terminate SSH, so it cannot produce a `.cast` of the proxied session — that
would require implementing an SSH server. `--listen/--to` with `omssh tunnel`
does the same forwarding; `bastion` is the name that says what it is for.

Every tunnel event is appended to `<state>/tunnels.jsonl` (0600). `--json-lines`
also prints the events while the tunnel runs.

### Files from a script, or an agent

`omssh fs` is the same file manager with no screen around it. One grammar for
every place — `local`, a cloud profile, or an SSH host:

```sh
omssh fs ls prod:/srv/app
omssh fs ls local:~/Downloads --json
omssh fs stat site:/var/www/index.html
omssh fs get prod:/srv/app/dump.sql ./dump.sql
omssh fs put ./dist/index.html site:/var/www/index.html
omssh fs mkdir site:/var/www/releases/2026-10
omssh fs rm site:/var/www/releases/2025-01
```

A bare place means its root (`omssh fs ls prod`). `get` writes to stdout with
`-`; `put` reads stdin with `-`. `put` creates missing parent directories.

`--json` prints one document with stable field names:

```json
{
  "entries": [{"name": "index.html", "dir": false, "size": 5124,
               "modified": "2026-10-27T07:09:00Z"}],
  "path": "/var/www",
  "place": "site"
}
```

**Reads are free; writes are not.** Without `--allow-write` the mutating verbs
(`put`, `rm`, `mkdir`) exit `77` and change nothing — the flag belongs to the run,
not to the call, so nothing an agent passes in can grant it. `--allow-host` and
`--allow-path` narrow further, and are checked before a filesystem is opened:

```sh
omssh fs put ./build.tar.gz prod:/srv/app/build.tar.gz \
  --allow-write --allow-host prod --allow-path /srv/app
```

`--batch` never prompts. On an unknown host key it exits `65` with the
fingerprint on stderr instead of waiting; a password that is needed but not stored
exits `77`. The codes:

| code | meaning                                             |
| ---- | --------------------------------------------------- |
| 0    | success                                             |
| 1    | the operation failed                                |
| 64   | the command line was wrong                          |
| 65   | the host is not trusted yet (`--batch`)             |
| 77   | refused: no `--allow-write`, allowlist, credentials |

**Destructive calls return a plan.** `fs rm` without `--yes` prints what it would
remove and changes nothing; with `--yes` it removes and audits. Like
`--allow-write`, `--yes` belongs to the run, so a tool call cannot grant itself
the right to delete.

**Long operations stream.** `--json-lines` writes one object per line:
`{"event":"start",…}`, `progress` (at most ten a second), `done`, or `error`. A
single document at the end is useless while a copy runs.

### MCP: an agent's interface

`omssh mcp` is a Model Context Protocol server over **stdio** — no listener, no
network, no token to leak. It exposes the same places and the same safety model:

```json
{
  "mcpServers": {
    "omssh": {
      "command": "omssh",
      "args": ["mcp", "--allow-write", "--allow-host", "prod",
               "--allow-path", "/srv/app"]
    }
  }
}
```

In the interface, **MCP for agents** on the launcher (or `Alt+M`) opens the card with this snippet; `c` copies it.
The server is a stdio process the agent starts, so it is configured outside omssh
— the card exists so the flags are not something to remember.

That is a Claude Desktop / Cursor / any MCP client's configuration. Drop the
`--allow-write` and the server can only read; add `--yes` and `rm` performs
instead of returning a plan.

| Tool | What it does |
| --- | --- |
| `places` | List `local` and the cloud profiles |
| `ls` | List a directory |
| `stat` | Describe one path |
| `get` | Read a file as text, or `base64:…` when binary |
| `put` | Write a file, creating parents |
| `mkdir` | Create a directory |
| `copy` | Copy a file or a whole tree between places |
| `extract` | Extract an archive into a place |
| `rm` | Remove, as a plan unless the server runs with `--yes` |

Every call goes through the same allowlists, the same audit log and the same
keyring as the interface. Nothing prompts: an unknown host key is an error, not a
dialog. The server speaks protocol version `2024-11-05`.

The `--json-lines` events and the MCP tools are the two interfaces an agent
should use. Driving the TUI by scraping its screen is neither stable nor honest,
and is not supported.

Every change appends a line to `audit.jsonl` in the state directory
(`omssh config path` shows where configuration lives; the state directory is its
sibling under `~/.local/state/omssh`):

```json
{"time":"2026-10-27T07:10:00Z","action":"put","place":"prod",
 "path":"/srv/app/build.tar.gz","bytes":24813,"ok":true}
```

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

hosts: {}                 # password connections kept by omssh

tunnels:
  profiles: {}            # saved forwards

transfers:
  workers: 4

sessions:
  record: true          # embedded terminals write asciinema .cast files
  record_input: true    # include keystrokes
  keep_days: 0          # 0 keeps recordings; otherwise delete older ones
  max_mb: 512           # folder cap, oldest first

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

### History

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Filter: Terminal, Tunnel, Agent, All |
| `↑` / `↓` | Choose a record |
| `Enter` | Replay, watch the tunnel tail, or show the agent call |
| `v` | Read a terminal recording as a conversation log |
| `d` | Delete the selected record (after asking) |
| `Esc` | Back to the launcher (on the launcher, quit) |

### Sessions

| Key | Action |
| --- | --- |
| `Ctrl+O` | Connections |
| — | The launcher's Dashboard entry asks which places to show |
| `Tab` / `Shift+Tab` | Next / previous session |
| `1`…`9` | Jump to a session |
| `[` / `]` | Previous / next session |
| `w` | Switch pane |
| `T` | Terminal on this host, handed over to ssh |
| `U` | Embedded terminal on this host (recordable) |
| `Alt+T` | Start a tunnel through this host |
| `Alt+Y` | Watch or stop running tunnels |
| `Alt+M` | MCP configuration for agents |
| `Alt+R` | Replay the newest recording, or choose a path |

| `Q` | Disconnect, keeping the tab |
| `Ctrl+W` | Close the tab |
| `Esc` | Tab strip (with several tabs), then the launcher; on the launcher, quit |

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
