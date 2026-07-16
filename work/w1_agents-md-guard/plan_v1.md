# w1_agents-md-guard: Stop `bd init` from writing AGENTS.md, and undo the existing pollution

> Delete the "Landing the Plane" AGENTS.md mutation from the bd fork at source, reinstall both bd binary copies, pin the fix with a regression test, and remove the boilerplate already committed in 14 repos.

## User Intent

"I can't have this bd init inserting text into my AGENTS.md file for every repo, make sure this doesn't happen." — bd must never again create or modify AGENTS.md, under any trigger (any agent lane, any repo), and the boilerplate it already left behind should be gone.

## Problem

`bd init` unconditionally (unless `--stealth`) calls `addLandingThePlaneInstructions()` (`cmd/bd/init.go:466` in the fork), which:
- APPENDS a 26-line "## Landing the Plane (Session Completion)" section to an existing `AGENTS.md` (`updateAgentFile`, init.go:1677), or
- CREATES a 40-line `AGENTS.md` ("# Agent Instructions" + bd quick reference + the section) when the file is missing.

There is no opt-out flag. `--stealth` skips it but also flips global git settings — unusable. Upstream (steveyegge/beads, checked at `upstream/main`) still has the same unconditional call (upstream init.go:548), so no upstream fix exists to adopt.

Measured blast radius on this machine (grep "Landing the Plane" in `AGENTS.md` across `~/projects`, verified 2026-07-16):
- **14 repos with the boilerplate COMMITTED**: 13 wholly bd-generated 40-line template files (acid, decisions, fleet, investing, lit-search, mine, patent-forge, patent-search, spec-code, tts-reader, mini-warriors-reborn, game/game-generator, teaching/548-2026/website) + 1 curated file with the section appended at line 167 of 191 (moltbot).
- shared-docs/AGENTS.md was hit on 2026-07-15 but has already been restored (0 matches, no diff vs HEAD) — out of scope.
- tools/beads' own `AGENTS.md` and `cmd/bd/AGENTS.md` contain the section as upstream project documentation (upstream commits fb16e504, aae234bb) — not pollution, excluded.

## Key Insight

**bd on this machine is built from the user's own fork (`~/projects/tools/beads`), so the durable all-triggers fix is deleting the feature at source — and the fix does not ship unless BOTH stale binary copies are replaced.** `which bd` resolves `~/.local/bin/bd` (built Jan 28), while `make install` writes `~/go/bin/bd` (built Mar 16). Rebuilding without updating `~/.local/bin/bd` leaves the polluting binary live on PATH. Any wrapper-level guard (bash-guard row, hook) would only cover some agent lanes; the source deletion covers every future `bd init` from any caller. A Go regression test pins the behavior so a future upstream sync (fork is ~1586 commits behind; upstream still ships the feature) cannot silently reintroduce it.

## Design

```
bd init (any caller, any repo)
  └─ cmd/bd/init.go
       BEFORE: !stealth → addLandingThePlaneInstructions() → creates/appends AGENTS.md
       AFTER:  (call + functions + template const deleted) → bd init never touches AGENTS.md
Guard: TestInitDoesNotTouchAgentsFile in cmd/bd/init_test.go
  case A: init in dir WITHOUT AGENTS.md → file must not exist afterward
  case B: init in dir WITH AGENTS.md (sentinel content) → bytes identical afterward
Install: make install → ~/go/bin/bd ; install -m755 ~/go/bin/bd ~/.local/bin/bd
Cleanup: 13 template AGENTS.md deleted; moltbot's trailing section stripped
```

Code to delete in `cmd/bd/init.go` (all references confined to this file — verified by grep across cmd/ and internal/):
- Call site: the `if !stealth { addLandingThePlaneInstructions(!quiet) }` block and its comment (lines 464-468).
- `landingThePlaneSection` const (lines ~1633-1661).
- `addLandingThePlaneInstructions()` (lines ~1663-1675).
- `updateAgentFile()` (lines ~1677-1738).
- Do NOT touch `setupClaudeSettings` or the `--stealth` machinery.

Regression test follows the existing `TestInitCommand` harness in `cmd/bd/init_test.go` (t.TempDir + t.Chdir + reset globals + run initCmd), asserting on the AGENTS.md filesystem state, not on command output.

Cleanup decision: the 13 template files are 100% bd-generated (header line `# Agent Instructions`, 39-40 lines); nothing references them (only moltbot's CLAUDE.md references AGENTS.md, and moltbot's file stays). Delete the 13 files entirely; strip only the trailing "## Landing the Plane" section from moltbot's curated file. Deletion is reversible via git history.

## Changes

### Phase 1: Remove the feature from the fork — Gate: `go build ./...` clean and `go test ./cmd/bd -run 'TestInit'` passes, including the new regression test

| File | Change | Why |
|------|--------|-----|
| `cmd/bd/init.go` | Delete call site (464-468), `landingThePlaneSection` const, `addLandingThePlaneInstructions`, `updateAgentFile` | The feature has no legitimate caller left; clean removal, no flag/shim |
| `cmd/bd/init_test.go` | Add `TestInitDoesNotTouchAgentsFile` (cases A and B per Design) | Pins behavior against upstream re-merge |
| `CODEMAP.md` (beads fork) | Note init.go no longer writes AGENTS.md, if init.go behavior is described there | Codemap stays truthful |

Executed by one Codex builder (`codex-build.sh`) in the existing worktree `~/worktrees/beads/w1_agents-md-guard`.

### Phase 2: Reinstall both binary copies + E2E — Gate: proof checks 1-4 below pass with pasted output

| Step | Command | Why |
|------|---------|-----|
| Backup current binaries | `cp ~/.local/bin/bd {workdir}/backup/bd.local-bin; cp ~/go/bin/bd {workdir}/backup/bd.go-bin` | Rollback without rebuild |
| Build+install from the MERGED fork main | `cd ~/projects/tools/beads && make install` | Writes `~/go/bin/bd` with commit ldflags |
| Refresh PATH copy | `install -m755 ~/go/bin/bd ~/.local/bin/bd` | `which bd` = `~/.local/bin/bd`; without this the fix is not live |
| E2E scratch test | In a scratch dir: `git init`, write sentinel `AGENTS.md`, run `bd init -q`, diff sentinel; repeat in a second scratch dir with no AGENTS.md, assert absent | Proves the INSTALLED binary, not the tree |

Ordering: Phase 2's install runs AFTER the Phase-1 worktree is merged to the fork's main (wrap-up merge happens first), so the installed binary's commit hash is a main commit. Running daemons keep their old inode but are irrelevant — `bd init` always executes from a fresh CLI process.

### Phase 3: Fleet cleanup (14 repos) — Gate: fleet-wide sweep `grep -rl "Landing the Plane" ~/projects --include=AGENTS.md` returns only `tools/beads/AGENTS.md` and `tools/beads/cmd/bd/AGENTS.md`

Per repo: `wt-new.sh <repo> --next strip-bd-boilerplate` → edit → pathspec commit → `wt-merge.sh <worktree>` (append `--no-push` for the 3 repos with no remote: fleet, tts-reader, teaching/548-2026/website).

| Repo(s) | Change |
|---------|--------|
| acid, decisions, fleet, investing, lit-search, mine, patent-forge, patent-search, spec-code, tts-reader, mini-warriors-reborn, game/game-generator, teaching/548-2026/website | `git rm AGENTS.md` (wholly bd-generated template) |
| moltbot | Remove only the trailing `## Landing the Plane (Session Completion)` section (starts line 167); verify nothing follows it before cutting |

Executor: the manager runs this phase directly. Rationale (deviation from "manager dispatches"): each repo change is one file deletion or one section strip, and the surrounding `wt-new`/`wt-merge` lifecycle is owner-only tooling that workers are forbidden to run; dispatching 14 builders for one-line edits inside manager-created worktrees adds cost and risk without review value. The edits are made per-file with the Edit tool / `git rm`, not a bulk script.

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `cmd/bd/init.go` `setupClaudeSettings` | Yes | Separate behavior (.claude/settings.local.json); user did not ask; noted as observation only |
| `tools/beads/AGENTS.md`, `tools/beads/cmd/bd/AGENTS.md` | Yes | Upstream project docs, not bd-init output; deleting them causes upstream merge conflicts |
| `~/projects/shared-docs/AGENTS.md` | Yes | Already restored to committed state (0 matches, clean diff) |
| Other fork Go files | Yes | grep: `updateAgentFile`/`landingThePlaneSection` referenced only in init.go; no test references the feature |
| bd git hooks / merge driver installers | Yes | Distinct init features, not part of the complaint |

## Not in Scope

- Upstream PR to steveyegge/beads (can be offered later; fork fix is what protects this machine).
- Removing `.beads/` directories or git hooks that past `bd init` runs created.
- `setupClaudeSettings`'s `.claude/settings.local.json` injection (observed, not requested).
- shared-docs AGENTS.md (already clean).
- **Deploy-exclusion rule:** not applicable — this repo has no live service; "done = installed binary verified" is in-plan (Phase 2), and manager_log.md Pre-Plan records installation as part of done.

## Rollback

- **Fork code:** `git revert` the w1 commit on beads main.
- **Binaries:** restore from `{workdir}/backup/` (`install -m755 backup/bd.local-bin ~/.local/bin/bd`, same for `~/go/bin/bd`).
- **Cleanup commits:** each repo has one isolated commit — `git revert <sha>` per repo restores its AGENTS.md.

## Risks

| Risk | Mitigation |
|------|------------|
| Future upstream sync re-adds the feature | `TestInitDoesNotTouchAgentsFile` fails at the next test run; note in fork CODEMAP/CONTEXT |
| `~/.local/bin/bd` regenerated by some updater | No auto-update mechanism found; proof check 4 verifies the PATH binary's commit hash after install |
| moltbot section not actually trailing | Builder verifies content after line 191 before cutting; if anything follows, strip only lines 167..section-end |
| wt-merge push fails on the 3 remote-less repos | Use `--no-push`; merge is local-only, matching those repos' existing workflow |
| A 15th polluted AGENTS.md missed by the inventory | Phase-3 gate is a fleet-wide re-sweep, not the cached list |
| Two writers in the beads worktree | Phase 1 has exactly one builder; manager touches the worktree only after the builder's `.done` |

## Verification

**Tests:** `cd ~/worktrees/beads/w1_agents-md-guard && go build ./... && go test ./cmd/bd -run 'TestInit' -v` — all pass, including `TestInitDoesNotTouchAgentsFile`.

**E2E verification (installed binary, not the tree):** scratch repo with sentinel AGENTS.md + scratch repo without one; `bd init -q` in each; assert byte-identical / absent. This exercises the exact user-facing failure path (an agent running `bd init` in a repo with a curated AGENTS.md).

**Proof of behavior** (execute and paste output before declaring done):

| # | Check | Command | Expected |
|---|-------|---------|----------|
| 1 | Unit + regression tests | `go test ./cmd/bd -run 'TestInit' -v` | PASS incl. TestInitDoesNotTouchAgentsFile |
| 2 | Existing AGENTS.md untouched | scratch: `printf 'SENTINEL-w1\n' > AGENTS.md; bd init -q; cat AGENTS.md` | exactly `SENTINEL-w1` |
| 3 | No AGENTS.md created | scratch2: `bd init -q; ls AGENTS.md` | `ls: cannot access 'AGENTS.md': No such file or directory` |
| 4 | PATH binary is the fixed build | `which bd && bd version` | `~/.local/bin/bd`, version shows post-fix main commit |
| 5 | Fleet clean | `grep -rl "Landing the Plane" ~/projects --include=AGENTS.md` | only the two tools/beads upstream docs |
