# Roadmap

What omssh does today is in [DOCUMENTATION.md](DOCUMENTATION.md). This file is
what it does not do yet, in the order it should be done, with the decisions that
each item forces into the open.

Nothing here is a promise of a date. Each milestone is finished when its acceptance
criteria pass, not when its code merges.

---

## M7 — The local pane becomes optional  (shipped: the chooser can untick it)

Today every dashboard starts with a local pane, and the chooser will not untick
it. That is a good default for a file manager and a bad one for someone whose work
is three servers and a bucket.

**Deliverables**

- The local machine is a row in the pane chooser like any other, unticked by
  default **when at least one session is connected**, ticked when nothing else is.
- A dashboard of four remote panes is possible; so is one of a single remote pane.
- `Esc` from a pane with no local tab still has somewhere to go: the launcher, and
  the tab strip when several sessions are open.
- A new file-manager command, *Open local files* (`Ctrl+O` → the local row, or the
  launcher), so the local pane is never more than one keystroke away.

**Acceptance criteria**

- With four hosts connected, the chooser can produce four remote panes.
- With no sessions, the dashboard still opens on the local pane.
- Copying between two remote panes works with no local pane present.
- Nothing in the interface assumes pane 0 is local — the layout, the tab strip,
  sync browse, compare and deploy are checked against a remote-only dashboard.

**Risks**

- "Where is my local disk?" is a real question for a new user. Mitigation: the
  launcher keeps *Browse local files*, and the first-run tour says where it went.
- Several code paths use `m.Panes[0]` as the local pane. They are the work here,
  not the chooser.

---

## How many clients this needs

Seven services, **three clients**:

| Service | Protocol | Client | Notes |
| --- | --- | --- | --- |
| AWS S3 | S3 | S3 | the reference implementation |
| Cloudflare R2 | S3-compatible | S3 | per-account endpoint, path-style |
| Backblaze B2 | S3-compatible | S3 | per-region endpoint; keys are bucket-scoped |
| Alibaba Cloud OSS | S3-compatible | S3 | **virtual-hosted style only**; per-region endpoint |
| Google Cloud Storage | S3-compatible XML API | S3 | needs **HMAC keys**; service accounts need the native API |
| Azure Blob Storage | native REST | Azure Blob | no S3 compatibility at all |
| WebDAV | HTTP/WebDAV | WebDAV | real `MOVE`, ETags, no S3 in sight |

Everything above the client is one interface (`vfs.FS`), so the copy engine,
archive engine, deploy, preview and the four-pane dashboard do not know or care
which of these a pane is showing.

## M8 — Object storage: the `vfs` implementation  (shipped for S3 and R2)

Done: `internal/vfs/object` passes the conformance suite, opens in a pane, and is
reachable from `omssh cloud check|ls`. AWS S3 and Cloudflare R2 are configured;
any other S3-compatible endpoint works by hand with `endpoint` and `path_style`.
The remaining providers are M9 and M10.

S3, GCS, R2, OSS and B2 are the same shape at the level omssh uses them: a bucket
of keys, listing by prefix, GET and PUT, multipart for large objects. So there is
one implementation, not five.

**Deliverables**

- `internal/vfs/object`: an `vfs.FS` over the S3 API (`aws-sdk-go-v2`), so the
  existing copy engine, archive engine, deploy and preview work unchanged.
- Paths are keys with `/` as the separator; "folders" are prefixes. Listing
  paginates; a prefix with no objects is an empty directory.
- `Rename` is copy-then-delete, `Chmod` and `Symlink` report "not supported"
  rather than pretending, and the interface already degrades on capabilities.
- Streaming PUT, multipart above a configurable threshold, and `HEAD`-based size
  and modification time for the listing.
- Credentials: environment, shared config file, and the OS keyring; nothing is
  written to the config in plain text.
- A connection appears in the connections screen under **Cloud**, beside SSH
  hosts, and binds to a pane like a session does.

**Acceptance criteria**

- The `vfstest` conformance suite passes against MinIO, plus the capability-gated
  cases it already skips.
- Copy from S3 to a local folder, to an SSH host, and to another bucket works with
  progress, cancellation and per-item errors.
- Extract an archive that lives in a bucket into a local folder, and pack a local
  tree into a bucket.
- A bucket with 50,000 keys lists without freezing the interface.

**Risks**

- Prefix listing is not a directory read: paging, delimiters and "directory"
  markers need care or every folder looks empty.
- No atomic rename. A move that fails halfway leaves a copy; the report must say so.

---

## M9 — The compatible providers  (shipped: s3, r2, b2, gcs, oss)

R2, OSS, GCS and B2 speak S3-compatible APIs, with differences that are
configuration, not code.

**Deliverables**

- Provider presets that fill the endpoint, region and path-style settings:
  - **Cloudflare R2** — `https://<account>.r2.cloudflarestorage.com`, path-style.
  - **AWS S3** — regional endpoints, virtual-hosted style by default.
  - **Alibaba Cloud OSS** — `https://s3.oss-<region>.aliyuncs.com`, **virtual-hosted
    style only** (OSS rejects path-style). New accounts in Chinese mainland regions
    must use a bound custom domain for data operations, so the profile needs a
    field for one.
  - **Google Cloud Storage** — the S3-compatible XML API with HMAC keys, and
    service-account credentials via the native API as a second path if the
    compatibility layer proves limiting.
  - **Backblaze B2** — `https://s3.<region>.backblazeb2.com`; application keys are
    scoped to one bucket, so the key and the bucket in the profile must agree.
- A connection form that asks for provider first, then only the fields that
  provider needs.
- A check that costs nothing to run: `omssh cloud check <name>` lists the bucket
  root and reports what the credentials can actually do.

**Acceptance criteria**

- The same bucket operations (list, upload, download, copy, delete, extract) pass
  against R2, OSS, GCS and B2 test buckets, not only MinIO.
- Path-style and virtual-hosted style both work where the provider allows them; a
  wrong combination fails with a message that names the setting, not an XML error.
- Large-object uploads above the multipart threshold retry a failed part and
  report which part failed, instead of starting over.

**Risks**

- GCS's S3 compatibility needs HMAC keys; a user with only a service account will
  not have them. The native path is the answer if that becomes the common case.
- B2 keys are bucket-scoped and its S3 endpoint is region-specific; a mismatched
  profile fails as a 403 that says nothing useful, which is what the check command
  is for.
- Signing differences between providers surface as opaque 403s. Same answer.

---

## M10 — Providers with their own protocol  (shipped: WebDAV, Azure Blob, native GCS)

Two of the services people ask for are not S3-shaped, and pretending otherwise
would be the wrong kind of reuse.

**Azure Blob — shipped** in `internal/vfs/azure`: containers as filesystems, a
prefix as root, sync-blockblob uploads through a local buffer, and three auth
modes (account key, SAS, Entra ID through the default credential chain). The
endpoint is overridable for Azurite and sovereign clouds. A rename streams bytes
because a server-side copy would need the source authorized, and the capability
report says so.

**WebDAV — shipped** in `internal/vfs/webdav`: PROPFIND with Depth 0 and 1, GET,
PUT, MKCOL, DELETE and MOVE, with basic auth, passing the conformance suite. It is
in the connection manager as its own screen and in the cloud form as its own
provider.

**Google Cloud Storage — shipped** in `internal/vfs/gcs`: the JSON API rather than
the S3-compatible one, with ADC, a service-account JSON and impersonation as auth
modes. HMAC keys remain available as `provider: gcs, auth: s3`. GCS has a real
server-side copy, so a rename is a copy and a delete with the bytes staying in the
bucket; it is still two calls, and the capability report says copy, not atomic
rename.


**WebDAV** — a protocol, not an API:

- `PROPFIND` with Depth 1 lists a directory; `MKCOL` makes one; `GET`/`PUT` move
  bytes; `MOVE` renames — a **real** rename, unlike every object store here — and
  `COPY` duplicates.
- Servers differ: Nextcloud, ownCloud, Box, SharePoint and generic Apache
  implementations disagree about properties, locking and `If-Match`. The
  implementation targets the common subset and reports what a server refuses
  rather than failing silently.
- ETags and `If-Match` are used for conflict-safe overwrite, so "keep what is
  there" and "overwrite" mean the same thing they do for SSH.
- Authentication: basic and bearer; digest only if a server demands it.
- No permissions and no symlinks; capabilities say so.

**Acceptance criteria**

- Copy between WebDAV and SSH, WebDAV and Azure, and either and the local disk,
  with progress and cancellation, through the same `xfer` engine as everything
  else. (WebDAV half done: conformance passes; cross-protocol copy will be checked
  against a live server.)
- A rename on WebDAV is one `MOVE`, not a copy and a delete.
- A large Azure upload survives a failed request; the SDK's block upload retries
  without restarting the blob. (Not yet checked against a live container.)
- Delete and overwrite ask the same questions they ask on a server.

**Risks**

- Two new protocol stacks is real surface area. They are separate packages with
  the `vfstest` suite against them, and neither is allowed to leak into the copy
  engine.
- WebDAV quality varies more than SSH does; the acceptance tests use Nextcloud and
  one generic server, and the roadmap does not claim the rest.

---

## M11 — Storage polish

Only after M8 and M9 are honest.

- Presigned URLs: *Copy link* for an object, with an expiry chosen in the dialog.
- Object metadata in the preview: content type, size, modified, storage class,
  ETag.
- Bucket administration is explicitly **out of scope**: omssh browses and moves
  objects; it does not create buckets, set lifecycle rules or manage IAM.
- Server-side copy when source and destination are the same provider and region,
  instead of streaming bytes through the laptop.

---

## M12 — Sessions that can be read back

Today the terminal is `ssh` handed the real terminal (`T`). That is the right
default: vim, top and tmux work because nothing stands in their way. It also means
nothing is recorded, because the app never sees a byte after the handoff.

This milestone makes a *recorded* session an option rather than the only way.

**Deliverables**

- An embedded session: a PTY over the existing SSH connection, a VT screen for
  drawing, and the keyboard forwarded to it. `T` keeps the external `ssh`; a new
  action opens the embedded one, and the connections screen says which is which.
- Recording, in `asciinema`'s `.cast` format: one JSON header, then timestamped
  output frames. It replays with timing, colour and window size — a plain `.txt`
  transcript cannot, and a recording nobody can replay is a log, not a session.
  A `.txt` transcript is exported beside it for grep and for people who want text.
- Replay: play, pause, scrub, speed, and a text search that seeks. Recordings are
  listed from the launcher ("Recordings") and from the session's own menu.
- After connecting, the launcher gains a section for the live session: files,
  terminal, recordings, tunnels, disconnect.
- Input is recorded too, with one exception that matters: anything the SSH layer
  knows is a secret (a password or passphrase prompt) is not written. The recorder
  is told when a prompt is a secret, not left to guess from the text.

**Acceptance criteria**

- A recorded session replays in `asciinema play` and in omssh, with the same
  screen at the same timestamps.
- A session that runs vim and top replays both, including alternate-screen
  switches and resizes.
- Recordings live under the state directory, mode 0600, with a configured
  retention (default: keep, with a size cap).
- The external terminal still works and records nothing, and the interface says
  which mode is which.

**Risks**

- An embedded terminal is a large surface: escape sequences, resizes, mouse,
  paste. It is opt-in for that reason, and the external `ssh` stays the default
  until the emulator has earned it.
- Recordings contain everything typed and printed. Secrets are excluded at the
  source, the files are 0600, and the UI says plainly that a recording is a log of
  the session, not a private one.

---

## M13 — Tunnels, and a proxy that can be audited

Tunnelling is the other half of "this app is the intermediary": forwarding ports,
and — when someone deliberately asks for it — terminating the SSH connection so
the traffic can be recorded.

**Deliverables**

- Port forwarding over the existing pool: local (`-L`), remote (`-R`) and dynamic
  SOCKS (`-D`). Profiles in the config, a launcher screen listing running tunnels
  with bytes and uptime, start/stop, and `omssh tunnel list|start|stop`.
- A recorded bastion mode: omssh listens on a port, the user's client connects to
  it, omssh authenticates the client, opens the real session to the target, and
  records both directions as an M12 `.cast` plus a metadata log (who, when, from,
  to, bytes, exit). This is how a jump host is normally used, with the recording
  turned on.
- Tunnel traffic capture is **metadata only** by default: connection events, byte
  counts, destinations. Payload capture for a forwarded port happens only with an
  explicit flag per tunnel and a visible banner, because a port tunnel has no
  session boundary to make "recording" honest about what it contains.
- The session recording of a bastion is the plaintext omssh itself handles — the
  same thing the embedded terminal sees.

**Acceptance criteria**

- `-L` and `-D` survive a dropped connection with the pool's reconnect, and the
  tunnel screen says when a tunnel is reconnecting rather than silently dead.
- A bastion session records to `.cast`, replays, and its metadata names the client
  key or password identity that was used.
- Starting a tunnel on a busy port fails with the port and the conflicting process
  named where the OS allows it.
- Nothing is captured unless the tunnel or the bastion was created with capture
  on, and that state is visible from the launcher at all times.

**Risks and non-goals**

- **No silent interception.** omssh never proxies someone else's SSH connection
  without their client connecting to omssh's own host key, and it never records a
  connection it was not asked to record. A tool that quietly decrypts traffic is a
  different product with different consent.
- No transparent MITM of a connection that was pointed at the real server: the
  host key would not match, and making it match is an attack, not a feature.
- Recordings are evidence only if the machine and the files are trustworthy; the
  roadmap claims capture, not chain of custody.

---

## M14 — An interface for agents  (shipped: `omssh fs`, `--json`, `--batch`, `--allow-write`, allowlists, audit log; still missing: `--json-lines`, `omssh mcp`)

A person drives a screen. An agent cannot: it needs a command that does not ask
questions, output it can parse, and a safety model that fails closed. This is the
milestone that turns everything above into something an AI agent can use without a
pseudo-terminal.

**Deliverables**

- One path grammar for every place: `omssh fs <verb> <place>:<path>`, where a
  place is a host alias or a cloud profile. Verbs: `ls`, `stat`, `get`, `put`,
  `mv`, `rm`, `mkdir`, `extract`. SSH and buckets stop being different shapes to a
  caller.
- `--json` on every command: one document, stable field names. `--json-lines` for
  long operations: one object per event (`start`, `progress`, `item-error`,
  `done`), so a copy is observable while it runs.
- `--batch`: never prompt. An unknown host key exits 65 with the fingerprint on
  stderr; a password that is needed and not stored exits 77; nothing waits for a
  terminal that is not there.
- `omssh mcp`: an MCP server over stdio, exposing the same operations as tools
  with JSON schemas. Local transport, no network listener, credentials resolved
  exactly as the TUI resolves them.
- An audit log in the state directory: one JSON line per mutating action with
  place, path, bytes and outcome.

**Safety, because it is the whole feature**

- **Read-only by default.** Writes need `--allow-write` for the run, not a flag on
  a tool call an agent can set itself.
- **Allowlists** for hosts and paths (`--allow-host prod`, `--allow-path
  /srv/app`), enforced once, before a filesystem is handed to a tool.
- **Destructive calls return a plan** unless `--yes` was given — the same plan the
  TUI shows before a copy, move or deploy.
- **No secrets in arguments.** Keys come from the agent, the keyring or the
  environment, never from a tool parameter that lands in a transcript.
- **`exec` is off by default** and, when enabled, runs an allowlisted command. A
  remote shell is the most dangerous thing this program can offer.
- Every mutating call is audited, including the ones that failed.

**Acceptance criteria**

- An MCP client lists places, reads a file, copies a tree with progress, and
  extracts an archive, with no TTY anywhere.
- Against an unknown host, `--batch` exits 65 before any timeout with the
  fingerprint on stderr, rather than hanging on a prompt.
- A write without `--allow-write` exits 77 and changes nothing.
- The audit log names every mutating call, and its path is on `omssh config path`
  output.

**Risks**

- Tool descriptions are part of the contract: each must say whether it reads or
  writes, and what it will refuse. They need versioning when they change.
- An agent with `--allow-write` and a broad allowlist is a shell with extra steps.
  The defaults are the product.

**Not planned:** driving the TUI from an agent. Screen-scraping a terminal UI is
neither stable nor honest; the CLI and MCP are the interfaces, and the TUI stays
for people.

---

## Not planned

Written down so the list stays honest:

- A built-in editor. `$EDITOR` is the editor.
- A built-in terminal as the *only* terminal. `ssh` stays the default; the embedded
  one exists to be recorded (M12), not to replace it.
- Windows. The terminal and credential paths have never been exercised there.
- Full POSIX semantics for object storage: no permissions, no symlinks, no
  atomic rename, and the interface says so rather than faking them.
