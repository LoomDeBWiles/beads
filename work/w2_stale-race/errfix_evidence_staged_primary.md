# Environment defect: no self-serve route to unstage a file in a primary checkout

Repo: beads (`~/projects/tools/beads`). Item: w2_stale-race. Date: 2026-08-05.

## What is blocked

A tracked file in the primary checkout is **staged** (`M `) rather than merely modified
(` M`). Every sanctioned recovery refuses it, so the primary cannot be returned to clean
and `wt-merge.sh` cannot land the remaining commit.

## Verbatim denials

`git restore --staged` and `git commit`, from the primary:

```
bash-guard: primary git mutation is blocked - run it from the managed worktree for this
item; use wt-sync.sh only for primary sync; dirty paths declared `[restorable]` in
.wt-lanes can be self-served via `primary-restore.sh <repo>` (reversible, 14-day undo).
Row: 15. Token: restore. Target: /home/ben/projects/tools/beads.
Escalation: cd into the managed worktree for this item and rerun the git mutation there;
ask the user only if the primary checkout itself must be rewritten.
```

The guard's own named remedy, `primary-restore.sh`, then refuses the same path — even
with `work/*/manager_log.md` declared `[restorable]` in `.wt-lanes`:

```
primary-restore.sh: error{code=unsupported_status}: refusing restore; every dirty tracked
path must be status=" M" inside a restorable or generated lane
bucket{unsupported_status}:
  status=M  path=/home/ben/projects/tools/beads/work/w2_stale-race/manager_log.md
resolve real-work and deleted/renamed/copied paths first; this command touched nothing
```

And `wt-merge.sh` refuses to merge while that path is dirty:

```
wt-merge: primary dirty tracked paths overlap branch/origin changes:
wt-merge:   work/w2_stale-race/manager_log.md
wt-merge: recovery: bucket{unsupported_status}: status=M
wt-merge: recovery: resolve real-work first, then run primary-restore.sh.
```

## The gap

The guard's escalation text points at `primary-restore.sh`; `primary-restore.sh` points
back at "resolve real-work first"; and the only operation that would resolve it —
unstaging — is itself blocked by the guard (`restore`, `reset`, `stash` are all row-15
tokens). There is no self-serve exit from a staged primary path, even when the path sits
in a declared `[restorable]` lane and the content is already committed elsewhere.

This is a recovery-path gap, not a policy question: the content is safe (committed on
branch `w3_w2-deploylog` as `b90dd693e`), and the desired end state — primary clean, with
that commit merged — is ordinary.

## How it was reached

Wrap-Up's final deploy-log entry was written after the item worktree had already been
removed by `wt-merge.sh --keep`'s successor merge, so the entry had to be authored in the
primary. `git add` there produced the staged state. WORK.md's stated safety net for this
("repos with a Deploy target lane `work/*/manager_log.md` in `.wt-lanes`") did not apply,
because the beads repo carried no `.wt-lanes` file at all — one was added during this
recovery.

## Frequency

Structural. Any Deploy-carrying item whose repo lacks `.wt-lanes` reaches the same state
if the closing entry is written after the final merge, and once staged there is no exit.

## Suggested fixes (for the fixer session, not applied here)

1. `primary-restore.sh` accepts `M ` (staged, unmodified-since-stage) inside a declared
   lane by unstaging then restoring — the content is trashed first either way, so the
   reversibility guarantee is unchanged.
2. Or: bash-guard permits `git restore --staged -- <path>` in primary for paths inside a
   declared `[restorable]` lane, since it mutates only the index, not tracked content,
   history, or refs.
3. Independently: `repo-new.sh` / `wt-new.sh` could seed a default `.wt-lanes` so the
   Deploy safety net exists everywhere it is assumed to.
