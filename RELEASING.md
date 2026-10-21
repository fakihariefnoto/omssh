# Releasing

GoReleaser drives all three channels: GitHub releases, the Homebrew formula in
[fakihariefnoto/homebrew-tap](https://github.com/fakihariefnoto/homebrew-tap),
and `go install` via the module path.

```
git tag v0.2.0  ──▶  goreleaser  ──▶  GitHub release (archives + checksums)
                                  ──▶  Formula/omssh.rb committed to the tap
                                  ──▶  go install github.com/fakihariefnoto/omssh/cmd/omssh@v0.2.0
```

One command, from a clean checkout of `main`:

```sh
make release V=minor      # or V=patch, V=major, V=0.2.0
```

That runs the test suite, tags, pushes the tag, and hands over to GoReleaser.

## The first release, and the one thing that must exist first

GoReleaser publishes to `github.com/fakihariefnoto/omssh` and pushes the formula
to the tap, so both repositories must exist and this one must have an `origin`
before the script will run (it refuses otherwise, because a tag has to point at
pushed code):

```sh
gh repo create fakihariefnoto/omssh --public --source=. --remote=origin
git push -u origin main
make release V=minor        # the first tag comes from the script, not a file
```

The tap must contain a `Formula/` directory (it already does for the other
projects in this account). GoReleaser writes `Formula/omssh.rb` through the API,
so a local clone is not needed.

## Credentials

Publishing needs a credential that can create a release on `omssh` and push to
`homebrew-tap`. **You probably already have one**: if `git push` works from this
machine, git stores a credential for github.com, and it can do both. No personal
access token has to exist.

The script resolves one in this order:

1. `TAP_GITHUB_TOKEN` in the environment
2. a token saved with `make token-store`, in the macOS keychain
3. **the credential git already stores for github.com** — the same one `git
   push` uses

Check what you have:

```sh
printf 'protocol=https\nhost=github.com\n\n' | git credential fill
```

If that prints a `password=`, releases work as-is. If it prints nothing, either
`make token-store` (a fine-grained PAT with **Contents: read and write** on
`homebrew-tap` and `omssh`) or export `TAP_GITHUB_TOKEN` for one shell.

## What the script checks before it does anything

| Check | Why |
|---|---|
| working tree is clean | an uncommitted change would not be in the release |
| on `main` | releases come from the trunk |
| not ahead of `origin/main` | the tag must point at pushed code |
| tag does not exist, locally or remotely | re-tagging is how versions get lost |
| token present | otherwise goreleaser fails after the tag is already pushed |

`TAP_GITHUB_TOKEN` in the environment overrides everything, which is useful in
CI. GoReleaser commits the formula through the API, so a stale local clone of the
tap cannot disagree with what gets published.

Plan a release without doing it:

```sh
./scripts/release.sh --dry-run
```

### In GitHub Actions, nothing on your machine

Add the PAT as a repository secret named `TAP_GITHUB_TOKEN`, then either push a
tag or run the **release** workflow manually with the tag as input. The workflow
runs the test suite first and refuses to publish if it fails.

Repository secrets are not readable by anyone, including you: once saved they are
encrypted, shown as a name, and GitHub redacts them from logs. The real trust
boundary is *who can write to the repository*, which is why the PAT should be
fine-grained and scoped to the single tap repository.

## What gets built

| | |
|---|---|
| targets | darwin and linux, amd64 and arm64 |
| archive | `tar.gz`, with `LICENSE` and `README.md` beside the binary |
| cgo | disabled, so one machine cross-compiles everything |
| version | injected with `-ldflags -X main.version/commit/date` |
| checksums | `checksums.txt` per release |

Windows is not built. The terminal handling and the credential path have never
been exercised there, so it is not advertised rather than shipped untested.

Archives deliberately have no wrapping directory: the formula does
`bin.install "omssh"` and expects the binary at the archive root, matching the
other formulae in the same tap.

## Verifying a release without publishing

```sh
make snapshot     # goreleaser release --snapshot --clean --skip=publish
```

Builds the archives and the formula into `dist/` and publishes nothing. This is
how the release configuration is checked before the first tag: four binaries,
four archives, a formula with both `on_macos` and `on_linux` blocks, and the
extracted binary reporting its injected version.

## If a release goes wrong

The tag is the problem, not the workflow. Delete it and start again:

```sh
git tag -d v0.2.0 && git push origin :refs/tags/v0.2.0
```

If the formula was already pushed to the tap, revert that commit there too, or
the two will disagree about what `v0.2.0` is.

## Pre-release checklist

```sh
make check                  # gofmt, vet, lint, tests
make snapshot               # confirm the artifacts build
git status                  # clean
git log origin/main..HEAD   # nothing unpushed
```
