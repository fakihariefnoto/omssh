# Roadmap

What omssh does today is in [DOCUMENTATION.md](DOCUMENTATION.md). This file is
what it does not do yet, in the order it should be done, with the decisions that
each item forces into the open.

Nothing here is a promise of a date. Each milestone is finished when its acceptance
criteria pass, not when its code merges.

---

## M7 — The local pane becomes optional

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

## M9 — The compatible providers  (partly done: R2 preset exists)

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

## M10 — Providers with their own protocol

Two of the services people ask for are not S3-shaped, and pretending otherwise
would be the wrong kind of reuse.

**Azure Blob Storage** — the native REST API:

- Containers behave as buckets, blobs as objects, `/` in a name is a virtual
  folder, and the listing is by prefix with XML paging.
- Authentication: account key, SAS token, or Entra ID. All three end up in the
  config as *a credential reference*, never a secret in plain text.
- Large uploads use block lists (stage blocks, commit); a failed block retries
  without restarting the object.
- Azure Data Lake Storage Gen2 (hierarchical namespace) has real directories and
  atomic rename; it is a separate capability, not an assumption.

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
  else.
- A rename on WebDAV is one `MOVE`, not a copy and a delete.
- Azure block upload above the multipart threshold survives one failed block.
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

## Not planned

Written down so the list stays honest:

- A built-in editor. `$EDITOR` is the editor.
- A built-in terminal emulator. `ssh` is the terminal (see DOCUMENTATION.md).
- Windows. The terminal and credential paths have never been exercised there.
- Full POSIX semantics for object storage: no permissions, no symlinks, no
  atomic rename, and the interface says so rather than faking them.
