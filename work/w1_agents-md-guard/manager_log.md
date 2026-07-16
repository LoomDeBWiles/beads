# Manager Log: w1_agents-md-guard (repo: tools/beads fork)

## 2026-07-16 — Pre-Plan
- Idea: `bd init` appends "Landing the Plane" boilerplate to AGENTS.md (creates the file if missing) in every repo it initializes. User: "i can't have this bd init inserting text into my AGENTS.md file for every repo, make sure this doesn't happen."
- User instruction: load /manager + /plan, run the planning loop with reviewers to the human gate. Treated as lock-in on the goal; design decisions settled by investigation below.
- Locked in: yes → PLAN

### Investigation facts (verified this session)
- Mechanism: `cmd/bd/init.go` — `addLandingThePlaneInstructions()` (line 1663) called unconditionally at init.go:466 unless `--stealth`. `updateAgentFile()` (1678) APPENDS `landingThePlaneSection` (const at 1633) to an existing AGENTS.md, or CREATES AGENTS.md with a bd-onboard template when missing. Idempotency guard: `strings.Contains(content, "Landing the Plane")` (1712). No opt-out flag except `--stealth` (which also flips global git settings — not usable).
- bd provenance: user's fork `~/projects/tools/beads` (origin LoomDeBWiles/beads, upstream steveyegge/beads). Fork is ~1586 commits behind upstream, ~10 ahead with local fixes. Installed binary `~/.local/bin/bd` = 0.34.0 (dev: main@3165860a), built Jan 28; HEAD is 293caebe. Makefile: `make build` (go build), `make install` (go install with ldflags).
- Feature references confined to cmd/bd/init.go (grep: no other files reference updateAgentFile/landingThePlaneSection).
- Pollution inventory (grep "Landing the Plane" in AGENTS.md across ~/projects):
  - COMMITTED (14 repos to clean): acid, decisions, fleet, game/game-generator, investing, lit-search, mine, mini-warriors-reborn, moltbot, patent-forge, patent-search, spec-code, teaching/548-2026/website, tts-reader
  - EXCLUDED: tools/beads AGENTS.md + cmd/bd/AGENTS.md — upstream project docs (upstream commits fb16e504/aae234bb), not bd-init pollution
  - shared-docs/AGENTS.md: already restored (0 matches, no diff vs HEAD) — out of scope
- No shared-docs script/skill runs `bd init` directly (grep clean); triggers are agents running `bd init` ad hoc (e.g. w710-era init on shared-docs 2026-07-15 03:13). Fixing bd itself covers every trigger.

### Pre-Plan decisions (mine, from evidence)
- Fix at source in the fork: DELETE the call + `addLandingThePlaneInstructions` + `updateAgentFile` + `landingThePlaneSection` (no flag, no config — clean removal per no-compat-shims rule). Add a Go regression test asserting `bd init` never creates or modifies AGENTS.md, so a future upstream sync can't silently reintroduce it.
- Rebuild + reinstall `~/.local/bin/bd`; verify installed binary E2E in a scratch repo.
- Cleanup phase: strip the verbatim section from the 14 committed repos, one pathspec commit each.

## 2026-07-16 — Plan
- Next: load Skill(plan), read CODEMAP.md + context, write plan_v1.md

## 2026-07-16 — Plan written + Review R1 dispatched
- Scale: standard. plan_v1.md written (source deletion in fork + 2-binary reinstall + regression test + 14-repo cleanup; 13 template files deleted, moltbot section stripped).
- Facts added: `make install` → ~/go/bin/bd (Mar 16 copy); PATH hits ~/.local/bin/bd (Jan 28) — both stale, both must be refreshed. Upstream still ships the feature (upstream init.go:548). 3 cleanup repos have no remote (fleet, tts-reader, teaching/548-2026/website) → wt-merge --no-push. Only moltbot CLAUDE.md references AGENTS.md.
- R1 dispatched: codex-review.sh, prompt work/w1_agents-md-guard/review_prompt_v1.txt → review_codex_v1.md (self-writes .done). Watch armed on LITERAL path /home/ben/worktrees/beads/w1_agents-md-guard/work/w1_agents-md-guard/review_codex_v1.md.done
- Next: on wake, disposition findings → plan_v2 via delta+checker if accepted FIX, else HUMAN GATE.

## 2026-07-16 — Review R1 dispositions
- review_codex_v1.md: VERDICT FIX findings=10. All verified against source before disposition.
- ACCEPT F1 (HIGH): confirmed — cmd/bd/setup/factory.go:105-145 SetupFactory creates/appends/rewrites AGENTS.md; RemoveFactory (l.182+) edits it. Grep sweep: only init.go + setup/factory.go write AGENTS.md (cursor.go/aider.go write other files). Plan's "never touches AGENTS.md" invariant requires fixing factory too: emit instructions to stdout, no file writes; add no-touch tests.
- ACCEPT F2 (HIGH): wt-merge refuses dirty worktrees; fix = pathspec-commit work/w1_agents-md-guard/ artifacts on the work branch before merge (standard flow), not relocating them.
- ACCEPT F3 (HIGH): backup dir never created, `;` masks failures, {workdir} is inside the worktree wt-merge removes. Fix: persistent post-merge path ~/projects/tools/beads/work/w1_agents-md-guard/backup/ (untracked), && chaining, sha256 verification.
- ACCEPT F4 (HIGH): replace `git rm` with `trash.sh <path>` + `git add -u -- <path>` — complies with trash-only deletion rail, adds 14-day undo on top of git history.
- ACCEPT F5 (HIGH): TOCTOU — re-verify template classification at execution time (head -1 == "# Agent Instructions" AND contains "Landing the Plane" AND ≤41 lines); mismatch → stop for review.
- ACCEPT F6 (MED): confirmed — `git -C ~/projects/teaching/548-2026/website rev-parse --show-toplevel` = ~/projects/teaching. Worktree is for repo `teaching`; path 548-2026/website/AGENTS.md.
- ACCEPT F7 (MED): confirmed — wt-merge push_primary returns 0 when no origin (has_origin_remote check); --no-push would LEAVE worktree+branch in place. Drop --no-push.
- ACCEPT F8 (MED): sweep must not depend on unreadable-dir traversal exit codes; narrowed fix: two-pattern grep (heading + stable body phrase) with stderr rationale (bd writes as ben; root-only dirs can't hold ben-written pollution); reject the full body-signature/allowlist machinery as overkill (bd writes the section verbatim; single writer).
- ACCEPT F9 (MED): four subtests {missing,sentinel} × {default,--quiet}; Phase-2 E2E runs default (non-quiet) mode at least once.
- ACCEPT F10 (MED): proof checks become exact runnable commands (mktemp -d, cmp, test ! -e, command -v bd, version-SHA compare vs merged main).
- Next: plan_delta_v2.md → Codex materializes plan_v2.md → check_plan.py → Review R2.

## 2026-07-16 — Revision v2 dispatched
- plan_delta_v2.md written: 15 rows (F1,F1b,F1c,F1d,F2,F3,F3b,F4,F5,F6,F7,F7b,F8,F9,F10), all ACCEPT, hash-bound to plan_v1 sections.
- Materializer dispatched (codex-review.sh) → plan_v2.md; watch on /home/ben/worktrees/beads/w1_agents-md-guard/work/w1_agents-md-guard/plan_v2.md.done
- Next: check_plan.py plan_v1.md plan_delta_v2.md plan_v2.md must pass, then Review R2 with <rejected> trail (none rejected) — R2 is final round (cap 2).

## 2026-07-16 — v2 verified + Review R2 dispatched
- Materializer's plan_v2.md PASSED check_plan.py ("valid plan revision") after delta fix: original plan_delta_v2.md had unescaped `|` inside table cells (TestInit|TestSetupFactory regexes, find|xargs pipe) — only 4/15 rows parsed. plan_delta_v2_fix1.md escapes them (\|); all 15 rows parse; checker rc=0. Both delta files retained.
- Spot-check: factory.go closure, 4-subtest matrix, backup/&&/sha256, trash.sh+git add -u, teaching path note, no-push removal, robust sweep gate — all landed.
- R2 (final round, cap 2) dispatched via codex-review.sh → review_codex_v2.md; prompt review_prompt_v2.txt notes all R1 findings accepted+applied and names 2 cosmetic residuals. Watch on /home/ben/worktrees/beads/w1_agents-md-guard/work/w1_agents-md-guard/review_codex_v2.md.done
- Next: disposition R2. Zero accepted FIX → HUMAN GATE (gate_summary.md + /explain page). Accepted FIX → plan_v3 via delta+checker, then reclassify any remainder as accepted risks (cap reached) → HUMAN GATE.

## 2026-07-16 — Review R2 dispositions + v3 dispatched
- review_codex_v2.md: VERDICT FIX findings=5. All verified, all ACCEPT:
- ACCEPT F1 (HIGH): factory no-touch test must cover all former mutation paths — install×{absent,plain,marked} + --remove×{marked-only,curated-marked} (5 subtests, renamed TestFactoryDoesNotTouchAgentsFile).
- ACCEPT F2 (HIGH): confirmed the 13 template files hash to exactly 2 values (12× bf83e48f…, 1× 92c1bfa3… spec-code 39-line). Pre-deletion gate becomes exact-hash match against those two; any mismatch stops. Also fixed stale "git rm" mention in Executor rationale.
- ACCEPT F3 (MED): confirmed function is InstallFactory (factory.go:104), not SetupFactory; cmd/bd/setup.go:75-78+122 help/flag text claims AGENTS.md mutation — added to Phase 1 scope.
- ACCEPT F4 (MED): backup made write-once via MANIFEST.sha256 guard (resume re-verifies, never overwrites); Phase 2 gate now "checks 1-5".
- ACCEPT F5 (MED): proof check 4 now asserts two-copy hash equality + version-commit prefix of merged HEAD; check 5 gets own mktemp repo.
- Cap 2 reached → after v3 checker passes: HUMAN GATE (no R3).
- Materializer dispatched → plan_v3.md; watch on /home/ben/worktrees/beads/w1_agents-md-guard/work/w1_agents-md-guard/plan_v3.md.done

## 2026-07-16 — Human Gate
- plan_v3.md passed check_plan.py first try ("valid plan revision"); spot-check confirmed InstallFactory naming, 5-subtest factory matrix, exact-hash pre-deletion gate (bf83e48f…/92c1bfa3…), MANIFEST write-once backup, checks 1-5 gate.
- Review cadence complete: R1 10 findings, R2 5 findings — all 15 ACCEPTED, zero rejected; cap 2 reached; no R3.
- gate_summary.md written; rendered branded gate page → ~/served/beads/w1_agents-md-guard/gate_summary.html (data-gate=w1_agents-md-guard-v3).
- Presented: http://localhost:8095/beads/w1_agents-md-guard/gate_summary.html + plan_v3.md. Canonical plan = plan_v3.md.
- AT HUMAN GATE — awaiting approve / send back. Next on approve: no beads (3 phases, single builder + manager ops — under bead threshold); Phase 1 builder dispatch via codex-build.sh in ~/worktrees/beads/w1_agents-md-guard.

## 2026-07-16 — Gate APPROVED → Phase 1 dispatch
- User: "This is your approved work item execute it per the skill process through done." → gate approved, canonical plan = plan_v3.md.
- No beads (3 phases, single builder + manager ops — under bead threshold). Bead-free path (WORK.md templates/bead-free.md).
- File-lease MCP :8766 liveness OK.
- Phase 1: one Codex builder (codex-build.sh) in worktree ~/worktrees/beads/w1_agents-md-guard. Gate: `go build ./...` clean + `go test ./cmd/bd/... -run 'TestInit|TestFactory' -v` passes incl. new no-touch tests.
- NOTE: Go project — builder uses `go build`/`go test`, NOT pytest (template's pytest line N/A here).
- Next: dispatch builder non-blocking, arm bg watch on builds/plan.md.done, end turn.

## 2026-07-16 — Phase 1 build DONE + verified
- Builder exit=0, commit 23c265b5 "w1 Phase 1: bd never writes AGENTS.md...". Worktree clean (no non-work dirt).
- Independent gate verify (manager): `go build ./...` BUILD OK; `go test ./cmd/bd/... -run 'TestInit|TestFactory'` all ok (cmd/bd 3.1s, setup 0.003s). Deleted symbols (landingThePlaneSection/addLandingThePlaneInstructions/updateAgentFile) = 0 refs repo-wide. New tests present+passing: TestInitDoesNotTouchAgentsFile (init_test.go:184), TestFactoryDoesNotTouchAgentsFile (setup/factory_test.go:12, 5 subtests PASS).
- Next: dispatch Codex reviewer on commit 23c265b5 (bead-free analog: findings→file, manager dispositions; no manifest for bug beads).

## 2026-07-16 — Phase 1 review CLEAN + merge (--keep)
- Codex reviewer on commit 23c265b5: VERDICT CLEAN. Verified: init.go no longer writes AGENTS.md by any path; InstallFactory/RemoveFactory print-only, CheckFactory read-only; both no-touch tests assert real contract (absent-stays-absent / byte-identical), full subtest matrix; setup.go help truthful; scope limited to 5 files (no setupClaudeSettings/--stealth/cursor/aider); gate passed. Zero findings.
- Trashed 10 raw Codex transcripts/ephemeral files (materialize_v2/v3.log, review_codex_v1/v2.log, builds/*.codex*.log+stderr 732KB, watch logs, stall-state, reviews/*.log) — over the 2000-line human-lane cap, reproducible, not deliverables. Batch 20260716T144839Z-4003975 (14-day undo). Kept .md/.txt/step1.log deliverables.
- DEVIATION (manager mechanics, not plan scope): merge Phase 1 with `wt-merge.sh <wt> --keep` instead of plain merge. Rationale: plan runs Phase 2 (binary install) + Phase 3 (fleet) AFTER the Phase-1 merge; --keep gives main the Phase-1 commit now (so the installed binary's hash is a main commit, satisfying proof check 4) while retaining the worktree to log Phase 2/3 deploy steps + work_report. Final plain wt-merge at wrap-up removes it. Matches MANAGER.md deploy convention (log in retained worktree).
- Next: commit work/ artifacts by pathspec → wt-merge --keep → Phase 2 (backup+build+install+E2E proof 1-5).
