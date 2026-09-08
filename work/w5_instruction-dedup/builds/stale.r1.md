# Beads stale instruction removal

Result: PASS. Removed only the manifest v9 stale duplicate at `cmd/bd/@AGENTS.md`. The removal is recoverable.

## Changed paths

| Path | Change |
|---|---|
| `cmd/bd/@AGENTS.md` | Trashed after exact before-hash validation |
| `work/w5_instruction-dedup/builds/stale.r1.checks.txt` | Added exact verification and cleanup evidence |
| `work/w5_instruction-dedup/builds/stale.r1.md` | Added this build report |

No other source file changed.

## Manifest and trash record

- Manifest: `/home/ben/worktrees/shared-docs/w903_instruction-dedup/work/w903_instruction-dedup/approved_manifest_v9.json`
- Required before SHA-256: `bf83e48f58204bea5bee22b987a833b36deedd15253ddafd6bac106a78c1ded2`
- Worktree file before SHA-256: `bf83e48f58204bea5bee22b987a833b36deedd15253ddafd6bac106a78c1ded2`
- `HEAD` file before SHA-256: `bf83e48f58204bea5bee22b987a833b36deedd15253ddafd6bac106a78c1ded2`
- Trash batch: `/home/ben/.local/share/agent-trash/beads/20260908T161407Z-14615`
- Trashed copy SHA-256: `bf83e48f58204bea5bee22b987a833b36deedd15253ddafd6bac106a78c1ded2`

## Mirror preservation

| Source and mirror | Approved SHA-256 | Result |
|---|---|---|
| `CLAUDE.md`, `AGENTS.md` | `1cf4e952083829fa05bc9c101fccaabd74cc4a586ab8c0cd1088759391569201` | PASS |
| `cmd/bd/CLAUDE.md`, `cmd/bd/AGENTS.md` | `acc3b29f13976f898628166a6c0f1a9d31c3a7390bbb78a4074d0c78771eeaf1` | PASS |
| `docs/CLAUDE.md`, `docs/AGENTS.md` | `d356f50cfac06a5e8b9336ce901a04fa61c6afb00c3e5ffbe591333a3b678fcd` | PASS |

Each `AGENTS.md` remains a relative symlink whose literal target is `CLAUDE.md`.

## Verification

- Confirmed the target matched manifest v9 in the worktree and in `HEAD` before removal.
- Confirmed the trash copy exists and preserves the approved bytes.
- Confirmed only `cmd/bd/@AGENTS.md` was deleted among source paths.
- Confirmed the target is absent from the worktree before commit.
- Confirmed all six approved mirror files retained their hashes and link targets.
- Confirmed after commit that `cmd/bd/@AGENTS.md` is absent from `HEAD`, omitted by `git ls-tree`, and rejected by `git ls-files --error-unmatch`.
- Confirmed the implementation commit contains only `D cmd/bd/@AGENTS.md` and passes `git diff HEAD^ HEAD --check`.
- Application tests were not run, as required.

Exact commands and outputs are in `stale.r1.checks.txt`.

## Mistakes and corrections

1. The first combined required-document read exceeded the output limit while reading the plan. I reread the complete plan in bounded chunks before mutation.
2. The first combined inventory command searched existing dispatch logs and exceeded the output limit. I reran the relevant manifest, Git, hash, link, and status checks directly before mutation.

Neither mistake changed repository state.

## Cleanup and boundaries

- No temporary resource or process was created, so no runtime cleanup was required.
- The restorable trash batch remains available for two days.
- Existing untracked dispatch artifacts remain untouched and will not be staged.
- No subagent, primary checkout, merge, push, new worktree, service, hook, or application test was used.

## Commit

Implementation commit: `a50bf02c9c96d48c69354368d7fc596ad1f2f0fb`

Implementation command:

`UBS_NO_AUTO_UPDATE=1 git commit -m "Remove stale nested agent instructions" -- cmd/bd/@AGENTS.md`

The evidence and report are committed separately by their two explicit pathspecs so this report can record the implementation commit.
