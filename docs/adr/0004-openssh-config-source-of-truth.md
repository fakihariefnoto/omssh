# 0004 — `~/.ssh/config` is the source of truth for connections

- **Status:** accepted
- **Date:** 2026-10-16

## Context

A connection manager needs somewhere to keep hosts, users, ports, keys and
jump hosts. Options: a private config file, a database, or the OpenSSH config
the user already has.

## Decision

Read and write `~/.ssh/config` (including `Include` and wildcards). Hosts
created in the app are appended as normal `Host` blocks with our own settings
in a structured comment that OpenSSH ignores (for example the deploy profile
name and a stable id), so the file stays valid for `ssh(1)`, `scp(1)` and every
other tool. Anything that is not a connection — UI preferences, themes,
deploy profiles — lives in `~/.config/omssh/config.toml`.

## Consequences

- Zero-import onboarding: a user's existing hosts are simply there, and edits
  made in a text editor show up in the app.
- Writes must be surgical: preserve comments, ordering and unknown directives;
  never rewrite the whole file. Covered by round-trip tests on real-world
  configs.
- Host key verification, `ProxyJump` and `IdentityFile` come for free with the
  same semantics as OpenSSH.
- Some OpenSSH directives are irrelevant to us and must be preserved verbatim
  rather than dropped.
