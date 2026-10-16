# Architecture Decision Records

Short records of the decisions that shape the code, so a future reader knows
*why* and what it would cost to change.

| ADR | Decision |
| --- | --- |
| [0001](0001-unified-vfs.md) | One filesystem interface for local and remote |
| [0002](0002-bubbletea-v2-ui-stack.md) | Bubble Tea v2 / Lip Gloss v2, confined to `internal/tui` |
| [0003](0003-in-process-archives.md) | Archives handled in-process, never by shelling out |
| [0004](0004-openssh-config-source-of-truth.md) | `~/.ssh/config` is the source of truth for connections |
| [0005](0005-keyring-for-secrets.md) | Secrets in the OS keyring, never on disk in plaintext |

Format: Status, Context, Decision, Consequences. One screen each; if a record
needs more, it is a design document, not an ADR.
