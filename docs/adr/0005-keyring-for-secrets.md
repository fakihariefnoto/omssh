# 0005 — Secrets in the OS keyring, never on disk in plaintext

- **Status:** accepted
- **Date:** 2026-10-16

## Context

Password authentication and passphrase-protected keys need a secret at connect
time. Storing it in the app's config would put it in plaintext in a
well-known path, and a user's config directory is exactly what gets synced,
backed up and accidentally committed.

## Decision

- Prefer `ssh-agent`. If a key is usable through the agent, no secret is needed
  at all.
- Otherwise store the password or passphrase in the OS keyring
  (`zalando/go-keyring`: macOS Keychain, Linux libsecret/Secret Service,
  Windows Credential Manager) under `omssh/<connection-id>`.
- Never write a secret into `~/.ssh/config`, the app config, logs or history.
- Enforce this by type: secrets are `keys.Secret`, whose `String`/`Format`
  methods render `[redacted]`, so a credential cannot be logged by accident.
- If no keyring is available (headless box), fall back to prompting per session
  and state clearly that nothing is saved.

## Consequences

- Nothing sensitive is written to disk by us; backups and dotfile repos stay
  clean.
- Headless and container use works without a keyring, at the cost of prompting.
- A keyring unavailable at runtime is a normal, handled case rather than an
  error the user has to interpret.
- We depend on the system keyring being present on the developer's machine for
  some tests; those tests are skipped when it is absent.
