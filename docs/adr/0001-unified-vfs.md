# 0001 — One filesystem interface for local and remote

- **Status:** accepted
- **Date:** 2026-10-16

## Context

The product promises copy in four directions, extract an archive from either
side into the other, compress a selection on either side, deploy a local tree
onto a remote, and show a diff between two trees. Written naively this becomes
`Upload`, `Download`, `ExtractRemoteToLocal`, `ExtractLocalToRemote`,
`DeployUpload`, plus a variant of each for the queue, the CLI and the dry run —
8 or more code paths sharing mistakes, and each new feature multiplying them.

## Decision

Define one interface, `vfs.FS`, that is close to `io/fs` and covers exactly
what we need on both sides (open/create/readdir/stat/lstat/mkdir/remove/rename/
chmod/chtimes/symlink/truncate, plus path helpers and a `Capabilities` struct).
Implement it three times: `Local` (over `os`), `SFTP` (over `pkg/sftp`), and
`Mem` (tests). Everything above the interface — copy, extract, compress,
deploy, diff — is written once against `vfs.FS` and never knows which side is
which.

There is deliberately **no** `Upload` and no `Download` function.

## Consequences

Good:

- The feature list grows in behaviour without growing in code. Remote→remote
  copy is not a feature; it is the copy engine given two remote handles.
- One conformance suite (`vfstest`) proves three implementations behave
  identically, including error and permission cases, without a network.
- Deploy's dry run and its execution are the same plan, so they cannot disagree.

Costs:

- The interface is the widest abstraction in the codebase and must be kept
  honest; adding a method means implementing it three times (four with tests).
- Per-format capability differences (no symlinks, no chmod, no `copy-data`
  extension) must be expressed up front in `Capabilities` rather than discovered
  as errors at runtime.
- Streaming performance work happens behind the interface, in the SFTP
  implementation, rather than in each feature.
