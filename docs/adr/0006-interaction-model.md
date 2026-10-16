# 0006 — Interaction model: discoverable, convention-first, reversible

- **Status:** accepted
- **Date:** 2026-10-16
- **Supersedes:** the keymap in the first revision of `docs/UX.md`

## Context

The first UX draft was vim-first and letter-keyed: `c` copy, `M` move,
`x` extract, `z` compress, `Ctrl+S` sync browse. Reviewing it against the goal
"best folder-walking experience" exposed three problems:

1. **Not learnable.** Nothing about `M` says "move", and nothing anywhere in the
   UI says "press M". A user had to read a table and memorise it.
2. **No safety net.** Delete was permanent, and `d` sat one key away from `c`
   with no confirmation and no undo.
3. **Key collisions with the terminal.** `Ctrl+S` is often `XOFF`, `Ctrl+H` is
   `Backspace`, `Ctrl+M` is `Enter`. A keymap is only usable if it survives the
   terminals people actually run.

## Decision

1. **Conventions before cleverness.** File operations use the shortcuts that
   already exist in editors and file managers: `Ctrl+C/Ctrl+X/Ctrl+V`,
   `F2` rename, `F3` view, `F4` edit, `F5` copy, `F6` move, `F7` new folder,
   `F8` delete, `F9` context menu, `F10` quit, `Ctrl+F` find, `Ctrl+L` path,
   `Ctrl+R` refresh, `Alt+←/→` history, `Ctrl+Q` quit.
2. **Nothing is key-only.** Every action is reachable from the command palette,
   the `F9` context menu, and the mouse. The palette prints shortcuts next to
   actions, so shortcuts are learned by using the app.
3. **Contextual action bar** under the active pane lists the verbs that apply to
   the current selection, with their keys, and is clickable. This is the primary
   teaching surface.
4. **A modifier grammar** so new keys are guessable: `Ctrl+letter` = action or
   dialog, `Alt+letter` = view toggle, `F-keys` = classic file ops, plain
   letters = type-ahead, `Shift+arrows` = extend selection, `Esc` = back.
5. **Reversible where it is cheap, explicit where it is not.** Rename and move
   are naturally reversible by doing the opposite. Delete is **not** reversible
   in v1 — there is no trash and `Ctrl+Z` is unbound — so delete always confirms
   with the facts (items, size, path), states depth and counts for recursive
   targets, and requires typing the path for root-ish targets.
6. **Direction is spelled out.** Panes are `LOCAL` and `REMOTE`; the verbs are
   `Send` and `Fetch`.
7. **Vim is a navigation preset, not a fork.** `keys.preset = "vim"` restores
   `hjkl`/`gg`/`G`/`v`/`~`/`-`/`/` and leaves operations standard, so a shortcut
   still means exactly one thing across presets. A fully vim-modal preset is
   out of scope for v1.
8. **Only bind keys the terminal can deliver.** Unbindable keys are avoided
   rather than half-supported, and capabilities are probed at startup.
9. **Quitting is explicit.** `Ctrl+Q` (or `F10`) quits and is rebindable;
   `Esc` only ever goes back, and at the top level it says how to quit. `Ctrl+C`
   stays *copy* with an explanatory hint when it has nothing to act on.

## Consequences

- A new user can copy a file to a server without reading documentation: the
  action bar offers it by name and `F5`/`Ctrl+C`/`Ctrl+V` already mean that.
- The palette, context menu and action bar are three views over one action
  registry, so a new feature is discoverable by default — and an action that is
  missing from the registry is a bug, not an oversight.
- Without undo, the confirm dialog carries the whole safety burden, so it is
  specified precisely (facts, depth, counts, type-the-path) and is never
  bypassable in the TUI. The CLI's `--yes` flag is the only skip, and it is
  explicit.
- `Ctrl+C` means copy, which is a deliberate, documented break from "quit". The
  mitigation is a toast that names `Ctrl+Q`, plus a configurable alternative.
- `Esc` never quits, so quitting has to be learned (`Ctrl+Q`/`F10`). The top
  level answers `Esc` with the shortcut, which is disclosure at the moment of
  need rather than a manual to read up front.
- Vim users lose their muscle memory for file operations unless they rebind
  them; they keep navigation, which is the part that is actually habitual.
