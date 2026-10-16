# 0003 — Archives handled in-process, never by shelling out

- **Status:** accepted
- **Date:** 2026-10-16

## Context

Extraction is usually delegated to `tar`, `unzip`, `7z` or `tar --to-command`.
That is the easy path, and it is wrong here:

- It requires those tools on both ends, which breaks the "one static binary,
  nothing required on the server" promise and on minimal containers (Alpine,
  distroless) fails outright.
- It forces a temp file or a shell pipeline for every cross-side extraction,
  which is exactly the case the product exists to make pleasant.
- It makes security a matter of quoting, which is where zip-slip bugs live.
- It prevents real progress reporting and per-entry error policy.

## Decision

Implement archive reading, writing and extraction in Go, in
`internal/archive`, streaming over `io.Reader` / `io.Writer`:

- Read: `archive/zip`, `archive/tar` plus `compress/gzip`,
  `klauspost/compress/zstd`, `compress/bzip2`, `ulikunitz/xz`.
- Write: zip, tar, tar+gz, tar+zst, tar+xz.
- One entry iterator normalises every format; extraction writes each entry
  through `vfs.FS`, so local and remote destinations share one loop.
- All path safety goes through a single pure function, `safeJoin`, with a
  dedicated attack corpus in tests. Limits (`MaxBytes`, `MaxEntries`,
  `MaxRatio`, `AllowSymlinks`) are enforced centrally.
- Formats outside this set (7z, rar) are supported only if an external tool is
  explicitly configured, and are clearly labelled as such.

## Consequences

- Cross-side extraction streams with no temp file on either end and gives
  per-entry progress for free.
- The zip-slip, symlink-escape and decompression-bomb defences are implemented
  once, tested once, and cannot be bypassed by choosing a different format.
- We carry more code than a `tar` wrapper, and 7z/rar users need an external
  tool (or a future pure-Go implementation).
- bzip2 is decompress-only in the standard library, so bzip2 writing is out of
  scope rather than half-supported.
