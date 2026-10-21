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

## M8 — Object storage: the `vfs` implementation

S3, GCS, R2 and OSS are the same shape at the level omssh uses them: a bucket of
keys, listing by prefix, GET and PUT, multipart for large objects. So there is one
implementation, not four.

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

## M9 — The compatible providers

R2, OSS and GCS speak S3-compatible APIs, with differences that are configuration,
not code.

**Deliverables**

- Provider presets that fill the endpoint, region and path-style settings:
  - **Cloudflare R2** — `https://<account>.r2.cloudflarestorage.com`, path-style.
  - **AWS S3** — regional endpoints, virtual-hosted style by default.
  - **Alibaba Cloud OSS** — `https://<region>.aliyuncs.com`, path-style.
  - **Google Cloud Storage** — the S3-compatible XML API with HMAC keys, and
    service-account credentials via the native API as a second path if the
    compatibility layer proves limiting.
- A connection form that asks for provider first, then only the fields that
  provider needs.
- A `omssh exec`-style check: `omssh cloud check <name>` lists the bucket root and
  reports what the credentials can actually do.

**Acceptance criteria**

- The same bucket operations (list, upload, download, copy, delete, extract) pass
  against R2, OSS and GCS test buckets, not only MinIO.
- Path-style and virtual-hosted style both work; a wrong combination fails with a
  message that names the setting, not an XML error.
- Large-object uploads above the multipart threshold resume nothing but do retry
  a failed part, and report which part failed.

**Risks**

- GCS's S3 compatibility needs HMAC keys; a user with only a service account will
  not have them. The native path is the answer if that becomes the common case.
- Signing differences between providers surface as opaque 403s. The check command
  exists for that reason.

---

## M10 — Storage polish

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
