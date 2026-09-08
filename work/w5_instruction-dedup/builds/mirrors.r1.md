# Beads instruction mirror build

Result: PASS. Applied every approved destination at or below `/home/ben/projects/tools/beads` to the assigned worktree. No application or Go behavior changed.

## Commit

Implementation commit: `82f917e7f93d08d87a8ee8e046aa941efe745182`

The report and evidence are committed separately so this report can record the implementation commit exactly. The report commit is returned to the dispatcher after creation.

## Changed paths

| Path | Final state |
|---|---|
| `.github/copilot-instructions.md` | SHA-256 `f7127f982c309905b7d116ba6d11679751e72f1cae2400173fc80d6b47860938` |
| `@AGENTS.md` | Trashed; old SHA-256 `bf83e48f58204bea5bee22b987a833b36deedd15253ddafd6bac106a78c1ded2` |
| `AGENTS.md` | Relative symlink to `CLAUDE.md` |
| `AGENT_INSTRUCTIONS.md` | SHA-256 `69f149d3257fa1ba8afc1e8a8326b4e6b50e886ebb199816d927704936d1619c` |
| `CLAUDE.md` | SHA-256 `1cf4e952083829fa05bc9c101fccaabd74cc4a586ab8c0cd1088759391569201` |
| `CODEMAP.md` | SHA-256 `205dd70d30401f0a94fbc1661a1b86a3665df04531f6dd1355da8d0266fa26bb` |
| `CONTEXT.md` | SHA-256 `3a2b8371474cd5fa32da1a0b200043c7cbc50223e6b20e2ca03fb4d2ea39106e` |
| `cmd/bd/AGENTS.md` | Relative symlink to `CLAUDE.md` |
| `cmd/bd/CLAUDE.md` | SHA-256 `acc3b29f13976f898628166a6c0f1a9d31c3a7390bbb78a4074d0c78771eeaf1` |
| `context/INDEX.md` | SHA-256 `b6d914fc5b2571708ebe5db9cbe724727583fc4e6bc5eb7a099c1f026647235f` |
| `docs/AGENTS.md` | Relative symlink to `CLAUDE.md` |
| `docs/BD_AGENT_REFERENCE.md` | SHA-256 `a20578a103def6e8e492b36b45b2532a95d73551b70277f29befd1f40db9d8c8` |
| `docs/BD_ISSUE_GUIDE.md` | SHA-256 `df335814d0e6542a016ff2a5001e810c1b5672ce60050eff9fc0a4f2f95efbbb` |
| `docs/CLAUDE.md` | SHA-256 `d356f50cfac06a5e8b9336ce901a04fa61c6afb00c3e5ffbe591333a3b678fcd` |

The selected manifest contains no preserve obligations. Every write matches the proposal byte for byte.

## Trash batches

| Original path | Reversible batch |
|---|---|
| `AGENTS.md` | `/home/ben/.local/share/agent-trash/beads/20260908T045801Z-541279` |
| `cmd/bd/AGENTS.md` | `/home/ben/.local/share/agent-trash/beads/20260908T045818Z-583981` |
| `@AGENTS.md` | `/home/ben/.local/share/agent-trash/beads/20260908T045844Z-642767` |

All three saved files exist in their batches and match their approved before hashes.

## Verification

- The approved migration verifier passed 16 scoped text obligations and 3 scoped mirror actions against the actual worktree mapping.
- `check_index_rows.py context/INDEX.md --strict-size` passed. All seven context routes resolve. The root `CLAUDE.md` retains `@context/INDEX.md`.
- `git diff --check` passed before commit. `git diff HEAD^ HEAD --check` passed after commit.
- No changed path ends in `.go`.
- `.worktree-check` is absent, so no repository merge-gate script was available.
- Application tests were not run because this is an instruction-only migration.

Evidence: `builds/mirrors.r1.preflight.txt` and `builds/mirrors.r1.checks.txt`.

## Mistakes and corrections

1. The first combined document read exceeded the tool output limit. I re-read the plan and both manifests separately before the first mutation.
2. The first route script expected backticks around index paths, so it selected no rows. I replaced that assumption with parsing of the table's first cell; the corrected check resolved 7 of 7 routes.
3. The first scoped manifest assertion expected 13 entries and failed with `ENTRY_COUNT expected=13 actual=14`. I accounted for 10 writes, 3 symlinks, and 1 trash entry, changed the assertion to 14, and reran the complete check successfully.
4. The first final tracking check passed deleted `@AGENTS.md` to `git ls-files`, which rejected it because deleted paths do not exist in `HEAD`. I checked the 16 current paths with `git ls-files` and the deletion with `git diff --diff-filter=D`; both passed.

The failed checks made no repository changes.

## Resources and boundaries

- Temporary scoped manifests and mapping were created at `/tmp/beads-w5-mirror.rgHJpn`, checked, and moved to trash batch `/home/ben/.local/share/agent-trash/misc/20260908T050214Z-1101937`.
- No subagents, other repositories, worktrees, merges, pushes, runtime proofs, live configuration, hooks, servers, or background processes were used or changed.
- Existing untracked dispatch artifacts under `work/w5_instruction-dedup/builds/` were preserved and excluded from staging.
