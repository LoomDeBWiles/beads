# Work Report: w1_agents-md-guard

## Summary

bd (the user's fork at `~/projects/tools/beads`) can no longer create or modify AGENTS.md under any trigger, and the boilerplate it left in 14 repos is gone. Plan plan_v3.md executed through done: fork source fix + regression tests (Phase 1), both installed binaries replaced + E2E (Phase 2), 14-repo fleet cleanup (Phase 3). All 6 proof checks PASS.

## Dispatch Ledger

| Backend | Dispatches | Where |
|---------|-----------|-------|
| Claude | 0 | none (no fallback needed) |
| Codex | 8 | plan reviews R1+R2, plan materializers v2+v2fix+v3, Phase 1 builder, Phase 1 commit reviewer |

## Beads Completed

N/A — plan executed without beads (review confirmed: 3 phases, single builder + manager ops, under bead threshold).

## Phase Outcomes

| Phase | Result | Evidence |
|-------|--------|----------|
| 1 — delete feature at source | Commit 23c265b5 on beads main (merged d426f635, pushed). init.go call site + `landingThePlaneSection` + `addLandingThePlaneInstructions` + `updateAgentFile` deleted; `InstallFactory`/`RemoveFactory` print-only; setup.go help truthful. Reviewer verdict CLEAN (reviews/plan_review_codex.md) | builds/plan.md, builds/step1.log |
| 2 — reinstall + E2E | `~/.local/bin/bd` and `~/go/bin/bd` both sha256 `19ccc433…` (equal), version `main@d426f6353c31` = merged HEAD prefix. Write-once backups + MANIFEST verified at `~/projects/tools/beads/work/w1_agents-md-guard/backup/` | proof checks 1-5 in manager_log.md |
| 3 — fleet cleanup | 14/14 repos: 13 template AGENTS.md trashed (14-day undo) + committed; moltbot section stripped (191→165 lines). Per-file hash gate PASS at execution time in every fresh worktree | proof check 6: sweep returns only the 2 upstream-doc exclusions |

## Proof Checks (all PASS)

| # | Check | Result |
|---|-------|--------|
| 1 | `go test ./cmd/bd/... -run 'TestInit\|TestFactory'` | all ok, incl. TestInitDoesNotTouchAgentsFile (4 subtests) + TestFactoryDoesNotTouchAgentsFile (5 subtests) |
| 2 | sentinel AGENTS.md + default `bd init` | UNCHANGED |
| 3 | no AGENTS.md + `bd init -q` | ABSENT |
| 4 | PATH binary identity | both copies sha256-equal; version commit prefixes merged HEAD d426f6353c31… |
| 5 | `bd setup factory` | prints block + "bd never writes AGENTS.md"; NOWRITE |
| 6 | fleet sweep | exactly tools/beads/AGENTS.md + tools/beads/cmd/bd/AGENTS.md remain (upstream docs, excluded by plan) |

## Cleanup Commits (one per repo)

acid 4ac0c15b0 · decisions d7039b1 · fleet 01889cd (no origin) · investing 21bbfba · lit-search 55281ac · mine cff22b0 · patent-forge 64bb2a6 · patent-search ebb07fc · spec-code c777281 · tts-reader 11c754e (no origin) · mini-warriors-reborn cb40722 · game-generator 287af72 · teaching 52b8e96 (548-2026/website/AGENTS.md, no origin) · moltbot 8d721d7 (section strip). Rollback: `git revert <sha>` per repo; trashed files also restorable from agent trash for 14 days.

## Deviations

- **wt-merge --keep for Phase 1** (manager mechanics): plan runs Phases 2-3 after the Phase-1 merge; `--keep` put the fix on main immediately (so the installed binary hash is a main commit, proof check 4) while retaining the worktree for Phase 2/3 logging. Final plain merge at wrap-up.
- **mine push unblock**: mine's push was blocked by its pre-push mass-change gate on a PRE-EXISTING unpushed commit 6b204147 ("untrack runtime caches", 1924 paths, verified untrack-only with files intact on disk). Used the hook's own documented override `ALLOW_MASS_COMMIT=1` once; mine went from 5+ stuck unpushed commits to fully synced.
- **wt.lock fd leak (incident, resolved self-serve)**: first Phase-1 merge timed out 600s on beads `.git/wt.lock` — a dead wt-script's `bd daemon` child (orphaned, PPID 1) had inherited the lock fd. Resolved with `bd daemon --stop` (bd's own lifecycle command; no kill). Root-cause class: wt.lock fd inherited without O_CLOEXEC by daemons spawned under wt scripts — follow-up candidate below.

## Follow-Up Required

| Bug/Issue | Root Cause Found | Empirical Data | Suggested Approach |
|-----------|-----------------|----------------|-------------------|
| wt.lock fd leak wedges all beads wt-ops | yes — bd daemon inherits wt.lock fd from parent wt-script; advisory flock survives owner death | dead owner 1793195; orphan daemon 1794314 held fd 9 for 1h32m; every wt-merge blocked | set O_CLOEXEC on wt.lock in wt-*.sh (or close inherited fds in bd daemon spawn) |
| beads fork dogfood DB prefix mismatch | not investigated (pre-existing, out of scope) | wt-merge post-merge bd sync warns: `bd-wisp-` (87) / `bd-eph-` (1) vs `bd-` prefix; import exit 1 | `bd doctor --fix` or `--rename-on-import` in tools/beads |

## Remaining

Queue empty — no beads used. All planned work completed.

## Notes

- Regression pin: a future upstream sync (fork ~1586 commits behind; upstream still ships the feature at init.go:548) breaks `TestInitDoesNotTouchAgentsFile`/`TestFactoryDoesNotTouchAgentsFile` at the next test run instead of silently re-polluting.
- Rollback of the binaries: `install -m755 ~/projects/tools/beads/work/w1_agents-md-guard/backup/bd.local-bin ~/.local/bin/bd && install -m755 .../bd.go-bin ~/go/bin/bd` (sha256 MANIFEST-verified).
- `bd setup factory` behavior change: now prints the integration block for manual application — no silent loss for a future Factory Droid setup.
