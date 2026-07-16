# w1_agents-md-guard: Stop `bd init` from writing AGENTS.md, and undo the existing pollution

> Delete the "Landing the Plane" AGENTS.md mutation from the bd fork at source, reinstall both bd binary copies, pin the fix with a regression test, and remove the boilerplate already committed in 14 repos.

## What Changed from v1

| Finding | Disposition | Change |
|---------|-------------|--------|
| F1 | ACCEPT | Identified `bd setup factory` as the second AGENTS.md writer and required closing both writers. |
| F1b | ACCEPT | Expanded the design with factory stdout behavior and no-touch regression coverage for init and factory setup. |
| F1c | ACCEPT | Replaced the broad Go-file claim with verified exclusions for cursor, aider, and all remaining files. |
| F1d | ACCEPT | Added factory implementation and test work to Phase 1 and widened its gate to both no-touch suites. |
| F2 | ACCEPT | Added the manager’s clean-worktree artifact commit and merge procedure. |
| F3 | ACCEPT | Moved binary backups to a persistent primary path, chained failures safely, and added hash verification. |
| F3b | ACCEPT | Updated binary rollback commands to use the persistent, hash-verified backups. |
| F4 | ACCEPT | Replaced hard deletion in the cleanup table with reversible trash plus pathspec staging. |
| F5 | ACCEPT | Added a per-file pre-deletion gate that revalidates generated templates at execution time. |
| F6 | ACCEPT | Corrected the teaching repository root and its nested AGENTS.md target path. |
| F7 | ACCEPT | Removed `--no-push` and documented automatic no-origin merge cleanup. |
| F7b | ACCEPT | Corrected the remote-less merge risk and added the Factory Droid behavior-change risk. |
| F8 | ACCEPT | Strengthened the fleet gate with an absolute traversal and two stable pollution signatures. |
| F9 | ACCEPT | Expanded init tests and installed-binary E2E coverage across default and quiet output modes. |
| F10 | ACCEPT | Rewrote verification as six exact, runnable proof checks. |

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

A second writer exists: `bd setup factory` (`cmd/bd/setup/factory.go:105-152`, `SetupFactory`) creates AGENTS.md from a template, appends a marked beads section to an existing file, or rewrites the section between its markers; `RemoveFactory` (factory.go:182+) edits the file to remove that section. Verified by grep across cmd/ and internal/: init.go and setup/factory.go are the only two Go files that write AGENTS.md (setup/cursor.go and setup/aider.go write .cursor/.aider files only). Both writers must be closed or the 'bd never touches AGENTS.md' guarantee is false.

## Key Insight

**bd on this machine is built from the user's own fork (`~/projects/tools/beads`), so the durable all-triggers fix is deleting the feature at source — and the fix does not ship unless BOTH stale binary copies are replaced.** `which bd` resolves `~/.local/bin/bd` (built Jan 28), while `make install` writes `~/go/bin/bd` (built Mar 16). Rebuilding without updating `~/.local/bin/bd` leaves the polluting binary live on PATH. Any wrapper-level guard (bash-guard row, hook) would only cover some agent lanes; the source deletion covers every future `bd init` from any caller. A Go regression test pins the behavior so a future upstream sync (fork is ~1586 commits behind; upstream still ships the feature) cannot silently reintroduce it.

## Design

```
bd init (any caller, any repo)
  └─ cmd/bd/init.go
       BEFORE: !stealth → addLandingThePlaneInstructions() → creates/appends AGENTS.md
       AFTER:  (call + functions + template const deleted) → bd init never touches AGENTS.md
bd setup factory / bd setup factory --remove:
  BEFORE writes/edits AGENTS.md → AFTER prints the beads integration block (or removal instructions) to stdout for the user to apply manually; CheckFactory stays read-only.
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

Code to change in `cmd/bd/setup/factory.go`:
- `SetupFactory` — replace all three write branches (create/append/update, lines ~105-152) with printing the template block and instructions to stdout, no filesystem writes.
- `RemoveFactory` — replace file rewrite with printed instructions naming the marker lines to delete.
- `CheckFactory` unchanged (read-only).

Regression tests follow the existing `TestInitCommand` harness in `cmd/bd/init_test.go` (t.TempDir + t.Chdir + reset globals + run the command), asserting AGENTS.md filesystem state: `TestInitDoesNotTouchAgentsFile` runs four subtests — {no AGENTS.md, sentinel AGENTS.md} × {default output, --quiet} — asserting the file stays absent / byte-identical; a companion `TestSetupFactoryDoesNotTouchAgentsFile` covers `bd setup factory` for both file states.

Cleanup decision: the 13 template files are 100% bd-generated (header line `# Agent Instructions`, 39-40 lines); nothing references them (only moltbot's CLAUDE.md references AGENTS.md, and moltbot's file stays). Delete the 13 files entirely; strip only the trailing "## Landing the Plane" section from moltbot's curated file. Deletion is reversible via git history.

## Changes

### Phase 1: Remove the feature from the fork — Gate: `go build ./...` clean and `go test ./cmd/bd/... -run 'TestInit|TestSetupFactory' -v` passes, including the new no-touch tests

| File | Change | Why |
|------|--------|-----|
| `cmd/bd/init.go` | Delete call site (464-468), `landingThePlaneSection` const, `addLandingThePlaneInstructions`, `updateAgentFile` | The feature has no legitimate caller left; clean removal, no flag/shim |
| `cmd/bd/setup/factory.go` | SetupFactory prints the integration block instead of writing AGENTS.md; RemoveFactory prints removal instructions instead of editing the file | close the second AGENTS.md writer |
| `cmd/bd/init_test.go` | Add `TestInitDoesNotTouchAgentsFile` — four subtests: {no AGENTS.md, sentinel AGENTS.md} × {default, --quiet} | Pins behavior against upstream re-merge |
| `cmd/bd/setup/factory_test.go` (new file or extend existing setup tests) | `TestSetupFactoryDoesNotTouchAgentsFile`: both file states | pins the no-write behavior |
| `CODEMAP.md` (beads fork) | Note init.go no longer writes AGENTS.md, if init.go behavior is described there | Codemap stays truthful |

Executed by one Codex builder (`codex-build.sh`) in the existing worktree `~/worktrees/beads/w1_agents-md-guard`.

Merge step (manager): after the builder's `.done`, pathspec-commit the work-item artifacts on the work branch (`git add work/w1_agents-md-guard/ && git commit -m 'w1: manager artifacts' -- work/w1_agents-md-guard/`), require `git status --porcelain` empty, then `cd ~/projects/tools/beads && wt-merge.sh ~/worktrees/beads/w1_agents-md-guard`. wt-merge refuses dirty worktrees; nothing may remain uncommitted.

### Phase 2: Reinstall both binary copies + E2E — Gate: proof checks 1-4 below pass with pasted output

| Step | Command | Why |
|------|---------|-----|
| Backup current binaries | `mkdir -p ~/projects/tools/beads/work/w1_agents-md-guard/backup && cp ~/.local/bin/bd ~/projects/tools/beads/work/w1_agents-md-guard/backup/bd.local-bin && cp ~/go/bin/bd ~/projects/tools/beads/work/w1_agents-md-guard/backup/bd.go-bin && sha256sum ~/.local/bin/bd ~/projects/tools/beads/work/w1_agents-md-guard/backup/bd.local-bin ~/go/bin/bd ~/projects/tools/beads/work/w1_agents-md-guard/backup/bd.go-bin` | Rollback without rebuild; persistent primary path that exists after the worktree is merged and removed; backups stay untracked (never committed — 33 MB each); hashes prove the copies are intact |
| Build+install from the MERGED fork main | `cd ~/projects/tools/beads && make install` | Writes `~/go/bin/bd` with commit ldflags |
| Refresh PATH copy | `install -m755 ~/go/bin/bd ~/.local/bin/bd` | `which bd` = `~/.local/bin/bd`; without this the fix is not live |
| E2E scratch test | In a scratch dir: `git init`, write sentinel `AGENTS.md`, run default `bd init`, diff sentinel; repeat in a second scratch dir with no AGENTS.md, run `bd init -q`, assert absent | Proves the INSTALLED binary, not the tree; both default and quiet output modes are exercised |

Ordering: Phase 2's install runs AFTER the Phase-1 worktree is merged to the fork's main (wrap-up merge happens first), so the installed binary's commit hash is a main commit. Running daemons keep their old inode but are irrelevant — `bd init` always executes from a fresh CLI process.

### Phase 3: Fleet cleanup (14 repos) — Gate: fleet-wide re-sweep `find /home/ben/projects -name AGENTS.md -not -path '*/.git/*' 2>/dev/null | xargs grep -l -e 'Landing the Plane' -e 'NEVER stop before pushing'` returns exactly `tools/beads/AGENTS.md` and `tools/beads/cmd/bd/AGENTS.md`. Traversal stderr from unreadable directories is suppressed and safe to ignore: bd runs as user ben, so bd-written pollution cannot live in a directory ben cannot read. The second pattern is a stable body phrase catching a copy whose heading was edited.

Per repo: `wt-new.sh <repo-root-name> --next strip-bd-boilerplate` → edit → pathspec commit → `wt-merge.sh <worktree>` (repos without an origin remote — fleet, tts-reader, teaching — need no flag: `wt-merge.sh` skips the push automatically when no origin exists and still completes merge + cleanup; `--no-push` must NOT be used, it leaves the worktree and branch in place).

Pre-deletion gate (per file, at execution time, inside the fresh worktree): the file must satisfy all of — first line is exactly `# Agent Instructions`, contains `## Landing the Plane`, and is ≤41 lines. Any mismatch → do not delete; stop and report that repo for manual review (classification is from planning time and could be stale).

| Repo(s) | Change |
|---------|--------|
| acid, decisions, fleet, investing, lit-search, mine, patent-forge, patent-search, spec-code, tts-reader, mini-warriors-reborn, game/game-generator, teaching (file: 548-2026/website/AGENTS.md) | `~/projects/shared-docs/scripts/trash.sh <worktree>/AGENTS.md` then `git add -u -- AGENTS.md` and pathspec-commit (wholly bd-generated template; trash gives a 14-day undo on top of git history) |
| moltbot | Remove only the trailing `## Landing the Plane (Session Completion)` section (starts line 167); verify nothing follows it before cutting |

teaching/548-2026/website is not its own repo — `git rev-parse --show-toplevel` = `~/projects/teaching`; the worktree is created for repo `teaching` and the target path inside it is `548-2026/website/AGENTS.md`.

Executor: the manager runs this phase directly. Rationale (deviation from "manager dispatches"): each repo change is one file deletion or one section strip, and the surrounding `wt-new`/`wt-merge` lifecycle is owner-only tooling that workers are forbidden to run; dispatching 14 builders for one-line edits inside manager-created worktrees adds cost and risk without review value. The edits are made per-file with the Edit tool / `git rm`, not a bulk script.

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `cmd/bd/init.go` `setupClaudeSettings` | Yes | Separate behavior (.claude/settings.local.json); user did not ask; noted as observation only |
| `tools/beads/AGENTS.md`, `tools/beads/cmd/bd/AGENTS.md` | Yes | Upstream project docs, not bd-init output; deleting them causes upstream merge conflicts |
| `~/projects/shared-docs/AGENTS.md` | Yes | Already restored to committed state (0 matches, clean diff) |
| `cmd/bd/setup/cursor.go`, `cmd/bd/setup/aider.go` | Yes | They write `.cursor` rules and aider config files, never AGENTS.md (only prose mentions) |
| All other fork Go files | Yes | grep across cmd/ and internal/ shows init.go and setup/factory.go are the only AGENTS.md writers, and `updateAgentFile`/`landingThePlaneSection` are referenced only in init.go |
| bd git hooks / merge driver installers | Yes | Distinct init features, not part of the complaint |

## Not in Scope

- Upstream PR to steveyegge/beads (can be offered later; fork fix is what protects this machine).
- Removing `.beads/` directories or git hooks that past `bd init` runs created.
- `setupClaudeSettings`'s `.claude/settings.local.json` injection (observed, not requested).
- shared-docs AGENTS.md (already clean).
- **Deploy-exclusion rule:** not applicable — this repo has no live service; "done = installed binary verified" is in-plan (Phase 2), and manager_log.md Pre-Plan records installation as part of done.

## Rollback

- **Fork code:** `git revert` the w1 commit on beads main.
- **Binaries:** `install -m755 ~/projects/tools/beads/work/w1_agents-md-guard/backup/bd.local-bin ~/.local/bin/bd && install -m755 ~/projects/tools/beads/work/w1_agents-md-guard/backup/bd.go-bin ~/go/bin/bd` (backups verified by sha256 at Phase 2).
- **Cleanup commits:** each repo has one isolated commit — `git revert <sha>` per repo restores its AGENTS.md.

## Risks

| Risk | Mitigation |
|------|------------|
| Future upstream sync re-adds the feature | `TestInitDoesNotTouchAgentsFile` fails at the next test run; note in fork CODEMAP/CONTEXT |
| `~/.local/bin/bd` regenerated by some updater | No auto-update mechanism found; proof check 4 verifies the PATH binary's commit hash after install |
| moltbot section not actually trailing | Builder verifies content after line 191 before cutting; if anything follows, strip only lines 167..section-end |
| assuming remote-less repos need special flags | wt-merge skips push when no origin exists and completes cleanup; no flag passed (verified at wt-merge.sh push_primary) |
| `bd setup factory` behavior change surprises a future Factory Droid setup | command still prints the exact block to paste; no silent loss, and no-touch test documents the contract |
| A 15th polluted AGENTS.md missed by the inventory | Phase-3 gate is a fleet-wide re-sweep, not the cached list |
| Two writers in the beads worktree | Phase 1 has exactly one builder; manager touches the worktree only after the builder's `.done` |

## Verification

**Tests:** `cd ~/worktrees/beads/w1_agents-md-guard && go build ./... && go test ./cmd/bd/... -run 'TestInit|TestSetupFactory' -v`.

**E2E verification (installed binary, not the tree):** scratch repo with sentinel AGENTS.md using default `bd init` + scratch repo without one using `bd init -q`; assert byte-identical / absent. This exercises both output modes on the exact user-facing failure path (an agent running `bd init` in a repo with a curated AGENTS.md).

**Proof of behavior** (execute and paste output before declaring done):

| # | Check | Command | Expected |
|---|-------|---------|----------|
| 1 | Unit + regression tests | `go test ./cmd/bd/... -run 'TestInit|TestSetupFactory' -v` | all PASS incl. TestInitDoesNotTouchAgentsFile + TestSetupFactoryDoesNotTouchAgentsFile |
| 2 | Existing AGENTS.md untouched | `d=$(mktemp -d) && cd $d && git init -q && printf 'SENTINEL-w1\n' > AGENTS.md && cp AGENTS.md /tmp/sentinel.ref && bd init && cmp AGENTS.md /tmp/sentinel.ref && echo UNCHANGED` | `UNCHANGED` |
| 3 | No AGENTS.md created | `d2=$(mktemp -d) && cd $d2 && git init -q && bd init -q && test ! -e AGENTS.md && echo ABSENT` | `ABSENT` |
| 4 | PATH binary is the fixed build | `command -v bd && bd version && git -C ~/projects/tools/beads rev-parse --short HEAD` | `/home/ben/.local/bin/bd`, and the version's embedded commit equals the printed merged-main SHA |
| 5 | Factory setup does not write (run inside `$d2`) | `bd setup factory && test ! -e AGENTS.md && echo NOWRITE` | prints integration block, `NOWRITE` |
| 6 | Fleet clean | `find /home/ben/projects -name AGENTS.md -not -path '*/.git/*' 2>/dev/null | xargs grep -l -e 'Landing the Plane' -e 'NEVER stop before pushing'` | exactly `/home/ben/projects/tools/beads/AGENTS.md` and `/home/ben/projects/tools/beads/cmd/bd/AGENTS.md` |
