# Manager Log: w3_atomic-claim

## 2026-08-07 — Pre-Plan
- Idea: add an atomic claim verb to the bd v0.34 fork so claiming a bead is one preconditioned write (CAS), plus an owner/lease with expiry so stale-claim recovery becomes a preconditioned steal instead of a blind overwrite.
- Motivation (evidence from fleet record): claiming today is read-then-unconditional-write (w702 review F-9); compensating machinery grew around it and had its own bugs — w29 liveness sweep + mkdir-lock + recovered= markers (false-reset hazard, review_v3 F-1), w702 per-bead mkdir lock (no owner/lease, crash blocks bead forever, bugs supervisor.16/.18), w695 duplicate-builder dispatch, w813 two builders live, w788 recovery overwrites assignee blindly. w828 trial: br's atomic `update --claim` is the one measured capability gap vs our fork; migration to br rejected (license rider names Anthropic/OpenAI agents as Restricted Parties, applies to derivatives; adapter cost; single-author 0.2.x).
- User lock-in (this session): "let's try your /elegant suggestions" — (1) atomic claim verb with status precondition, nonzero exit on zero rows; (2) owner/lease with expiry in the same verb so expired-lease steal replaces blind recovery. Direction locked; design details settle by evidence.
- Scope decision: this item lives in tools/beads and ships the primitive (verb + storage + RPC + schema migration + JSONL round-trip + tests + docs + deployed binary). Caller migration (orchestrator supervisor claim path, w29 sweep, errfix claim) is cross-repo → named follow-up items in the plan's Execution Handoff, not this worktree.
- Stakes: standard (shared production tracker, 17 live stores).
- Deploy: bd installs via `go install ./cmd/bd/` to ~/.local/bin/bd (live binary dated Aug 5); done includes install + daemon restart verification. Done-includes-deploy: yes (repo CONTEXT.md names the install command; live daemons run this code).
- Worktree: ~/worktrees/beads/w3_atomic-claim (branch w3_atomic-claim); workdir work/w3_atomic-claim/.
- JANITOR relay (owner-only row, for the user): beads backup-2026-01-17 orphan 200d creator=unknown — inspect /home/ben/worktrees/beads/backup-2026-01-17.
- Next: assess scale after exploring update path, storage, RPC, migrations.

## 2026-08-07 — Explore + Probes
- Scale: standard (multi-file: CLI + RPC + storage + migration + merge; one design decision set: claim semantics/lease). Quick test fails (couldn't list files without reading), domain known.
- Race confirmed at storage layer: sqlite UpdateIssue (queries.go:637) does GetIssue OUTSIDE any tx, then unconditional `UPDATE issues ... WHERE id = ?` in a tx (queries.go:757-778). No precondition anywhere.
- PROBE 1 (reproduce end to end): sandbox store, issue cr-qpg, two concurrent `bd --no-daemon update cr-qpg --status in_progress --assignee agent-{A,B}`. Result: A exit=0, B exit=0, final assignee=agent-A — B's acknowledged claim silently lost. Script: scratchpad/claimrace/race.sh.
- PROBE 2 (migrations): RunMigrations (migrations.go:107) runs the full idempotent list at every DB open under BEGIN EXCLUSIVE — new migration auto-applies to all live stores on first touch by the new binary. 26 existing migrations, pattern 0NN_name.go registered in migrationsList.
- PROBE 3 (atomicity primitive): RunInTransaction (transaction.go:29-41) uses BEGIN IMMEDIATE — read-decide-write inside it is serialized against all other write txs. busy_timeout configured (store.go:113). RowsAffected used at 8 existing sites.
- FINDING (merge driver drops fields): internal/merge/merge.go Issue struct models only 14 fields; mergeIssue rebuilds conflicting issues from that struct — assignee is NOT modeled, so a true 3-way conflict drops assignee today; any new claim field would be dropped the same way. In-scope fix: model assignee + claim_expires_at in merge.Issue/mergeIssue.
- Key surfaces mapped: update cmd cmd/bd/show.go:461; RPC UpdateArgs protocol.go:85, OpUpdate; storage iface storage.go:52; content hash types.go:67 (includes assignee+status → claim must refresh content_hash); manageClosedAt queries.go:592 (pattern for lease clearing on status exit); daemon routing main.go:615-682; 6 live bd daemons; installed binary ~/.local/bin/bd (Aug 5); merge driver vendored MIT (neongreen).
- Design settled: new `bd claim <id>` verb (not an update flag); storage ClaimIssue via RunInTransaction; precondition open OR self-renew OR expired-lease steal; exit 0 claim/renew/steal, exit 3 denied (holder printed, --json object), exit 1 error; new nullable column claim_expires_at via migration 027; lease optional (--lease DURATION, absent = no expiry); status-exit clears lease; JSONL round-trip via json tag; merge driver models assignee + claim_expires_at; RPC OpClaim + ClaimArgs + handler + client.
- Deploy precedent: w2 (build → literal E2E script → install ~/.local/bin/bd → daemon restart → live verify). Done-includes-deploy: yes.
- Next: write plan_v1.md (standard template), lint, dispatch Codex review R1.

## 2026-08-07 — Plan v1 + Review R1 dispatched
- plan_v1.md written (standard template); check_plan.py --lint exit 0 (fixed 4 path findings: New: prefixes, glob expansion).
- Review prompt: review_prompt_v1.txt (Core + Plan Quality + Implementation + Scenario Trace + Operational; skills: elegant, coding, testing, beads).
- Dispatched: codex-review.sh --bg; done=work/w3_atomic-claim/review_codex_v1.md.done.
- Next: watch review_codex_v1.md.done → disposition findings → plan_v2 → R2 delta review → gate. Round cap: 2 default, ceiling 4.

## 2026-08-07 — R1 Codex failed → Claude fallback
- codex-review.sh run failed: "You've hit your usage limit ... try again at Aug 11th" (review_codex_v1.md.log). Codex lane down until 2026-08-11.
- Fallback per MANAGER.md Model Roster: `claude -p --model claude-fable-5 --effort high`, same prompt (review_prompt_v1.txt), same output file review_codex_v1.md; envelope receipt review_codex_v1.md.done; stdout → review_codex_v1.md.claude.log; dispatch line in review_codex_v1.md.dispatch.log; pid 3480627 (setsid).
- Known telemetry defect note (w828, bead shared-docs-tij): fallback runs attributed to Codex because mine_item.py reads <artifact>.log — dispatch.log records the true lane.
- Next: watch review_codex_v1.md.done (fresh) → disposition findings → plan_v2.

## 2026-08-07 — R1 dispositioned → plan_v2
- review_codex_v1.md (Claude Fable fallback): FIX findings=6 → ACCEPT 6, REJECT 0.
  - F1 (high) ACCEPT: --assignee default → two defaulted claimants collide in RENEW cell, both exit 0. Fix: required flag. Verified actor helper exists (main.go:542-550 claim plausible; behavior change regardless).
  - F2 (high) ACCEPT: importer update maps (:561-587,:656-682) omit new fields → lease dropped on JSONL sync into existing store. Verified in source. importer.go moved into Phase 3 + allowedUpdateFields + existing-store round-trip test.
  - F3 ACCEPT: MemoryStorage implements full Storage interface (verified internal/storage/memory/memory.go exists; backs --no-db) → compile break without ClaimIssue. Row added.
  - F4 ACCEPT: INSERT column lists live in issues.go (:44,:74) → row added; change table is builder authority, not pinned grep.
  - F5 ACCEPT: in_progress ∧ assignee=="" cell defined as CLAIM (legacy idiom rows) + test case.
  - F6 ACCEPT: merge drop happens on every rewritten both-live row (Merge3Way re-marshals) not just conflicts; wording fixed + pass-through test.
- plan_delta_v2.md (12 hash-bound rows; F2a/F6b bound to parent Changes because Phase 3's v1 heading contains raw pipes the delta table cannot express; Phase 3 gate reworded pipe-free under that binding). check_plan.py: valid plan revision. Lint v2: clean.
- Rounds: R1 accepts change design/behavior → R2 delta review required (round 2 of 2 default).
- Next: dispatch R2 delta review (Claude fallback lane, Codex quota-down until Aug 11) → review_codex_v2.md.

## 2026-08-07 — R2 delta review dispatched
- Dispatched: Claude Fable fallback (Codex quota-down), prompt review_prompt_v2.txt (delta review: verify F1a-F6b landed + new-defect scan of changed sections; empty <rejected> trail).
- Receipt: review_codex_v2.md.done; stdout log review_codex_v2.md.claude.log; dispatch line recorded.
- Cap: round 2 of 2 default. Zero design-changing accepts → gate; low-sev accepts → plan_v3 straight to gate; design-changing accepts → weigh extra round per PLAN_SKILL Rounds rule.
- Next: watch review_codex_v2.md.done → disposition → gate.

## 2026-08-07 — R2 fallback re-dispatch (Fable limit → Opus 5)
- First R2 dispatch (Fable) died instantly: "You've reached your Fable 5 limit" (review_codex_v2.md.claude.log); stale .done exit=1 trashed (batch 20260807T142537Z-147101).
- Session note: rehomed to a different lane mid-item; watches lost and re-armed from recorded receipts.
- Re-dispatched per roster fallback order (Opus 5 when Fable unavailable): claude -p --model claude-opus-5 --effort high, same prompt review_prompt_v2.txt, same output review_codex_v2.md; process confirmed live.
- Next: watch review_codex_v2.md.done → disposition → gate.

## 2026-08-07 — R2 dispositioned → plan_v3
- review_codex_v2.md (Opus 5 fallback): FIX findings=8 → ACCEPT 8, REJECT 0. Source-verified G1 (pinned in ready.go:114/:250, dependencies scanners, multirepo:305), G3 (checkFieldChanged default:false utils.go:148), G4 (handleRename map :370-381) before accepting.
  - G1 (high): +ready.go/dependencies.go/multirepo.go rows; authority note reworded (table = minimum set, grep enumerates remainder).
  - G2: matrix cells split — closed∨tombstone→ERROR exit 1; other status→DENY exit 3 naming status; contradiction with exit-code sentence removed.
  - G3/G3r: checkFieldChanged case + renewal-lag risk row (content-hash short-circuit importer.go:602-606).
  - G4: three importer update maps named.
  - G5/G5r: assumptions+risk rows corrected to all-rewritten-rows mechanism.
  - G6/G6d: E2E step 4 + deploy line spell --assignee.
  - G7: Phase 3 gate = one runnable command, TestClaimRoundTrip named.
  - G8: ladder top-down first-match; self-renew precedes expiry-steal.
- plan_delta_v3.md 11 hash-bound rows; check_plan.py: valid plan revision; lint v3 clean.
- Rounds decision: R3 granted (ceiling 4). New-design pieces no round reviewed: (a) closed→exit1 vs other-status→exit3 split, (b) first-match ladder ordering, (c) checkFieldChanged lease propagation. Defect class backstops can't catch: self-consistent-but-wrong contract encoded into the item's own tests; silent enumeration gaps (nil lease reads compile clean).
- Next: dispatch R3 delta review (Opus 5 fallback; Codex quota-down, Fable limited) scoped to changed sections + the three named pieces. Round 3 of 4. Then gate.

## 2026-08-07 — R3 dispatched
- Dispatched: Opus 5 fallback, review_prompt_v3.txt (verify G-rows landed + the three named pieces: exit-code split, first-match ladder, lease propagation). Round 3 of 4; final unless a design defect surfaces.
- Receipt: review_codex_v3.md.done; process confirmed live.
- Next: watch .done → disposition → gate (zero design-changing accepts → gate directly; low-sev → plan_v4 straight to gate).

## 2026-08-07 — R3 dispositioned → plan_v4
- review_codex_v3.md (Opus 5): FIX findings=7 (0 high, 6 med, 1 low) → ACCEPT 7, REJECT 0.
  - H1/H1b: ClaimOutcome gains DenyReason (held|status); exit-3 contract reworded (retry vs skip) — a migrated supervisor would otherwise retry blocked/pinned beads forever.
  - H2/H2b: --assignee non-empty after trim (cobra required = presence only; "" matches legacy CLAIM cell → race reborn); empty-value test.
  - H3: blocked-status→deny-exit-3 cases in claim_test + scripttest (cell was untested).
  - H4: equalPtrTime (time.Time.Equal, nil-aware) — string helpers would report unconditional change → import churn.
  - H5/H5r: external_ref lease-only renewal round-trip case; renewal-lag risk scoped to no-external_ref rows.
  - H6: multirepo UPDATE branch (:330-347) added — claim always changes hash → copy path strands never-stealable lease.
  - H7: claim.go row mirrors "holder+expiry only when present".
- plan_delta_v4.md 10 hash-bound rows; check_plan.py valid; lint clean.
- Trajectory: design-changing accepts R1 6 → R2 8 → R3 7, severity high 2 → 1 → 0. Core design (one verb, CAS in IMMEDIATE tx, lease) unchanged since v1; rounds are converging on contract precision and enumeration closure.
- Rounds: R4 = hard ceiling. Dispatching R4 as landing-verification delta only (verify H-rows landed; new-defect scan limited to the reworded exit-contract text). Any design-changing find at cap → logged in risk table, gate carries the trail.
- Next: R4 dispatch → gate.

## 2026-08-07 — R4 CLEAN → Human Gate
- review_codex_v4.md (Opus 5): CLEAN findings=0. All ten H-bindings verified landed; DenyReason vocabulary, empty-assignee rules, and importer-lag mechanism consistent throughout.
- Trajectory: design-changing accepts R1 6 → R2 8 → R3 7 → R4 0 (severity: high 2 → 1 → 0 → 0). Review lanes: R1 Fable, R2-R4 Opus 5 (Codex quota-down all rounds).
- Plan ready per PLAN_SKILL: final version plan_v4.md.
- Next: gate_summary.json → render_gate.py → inbox-post → END TURN (user verdict is the wake).

## 2026-08-07 — Human Gate presented
- gate_summary.json (gate_id w3-v4, prompt_changes: CONTEXT.md claim-pattern note) → render_gate.py OK: http://localhost:8095/beads/w3_atomic-claim/gate_summary.html
- Renderer result: "gate armed: auto-dispatch on approve" — approval auto-dispatches a /work session for plan_v4.md and closes this planning session; send_back wakes this session for revision.
- Inbox posted: beads-w3-gate-v4.
- END TURN. User verdict is the wake.

## 2026-08-07 — WORK start: manual-beads entry checklist + authoring dispatched
- Plan approved by user this session ("This is your approved work item execute it per the skill process through done .../plan_v4.md"). Execution route: Beads.
- Manual Fallback Gate unlocked by the standing approval in WORK_BEADS.md Supervised Work: "**ORCHESTRATOR DISABLED (2026-08-04, Ben, until fixed).** Do NOT launch the supervisor daemon. Skip this whole section and run the item via `~/projects/shared-docs/WORK_BEADS_MANUAL.md`. This notice is the standing user approval the Manual Fallback Gate requires — no `orchestrator_error.md` needed; quote this line in manager_log.md as the approval."
- Entry checklist: supervisor/lock probed with flock -n → free (released immediately, receipts untouched). BEADS_DB=/home/ben/projects/beads/.beads/beads.db, bd info --json database_path confirms it. Approved plan path: work/w3_atomic-claim/plan_v4.md. Worktree tip c6076387c, clean except untracked work/w3_atomic-claim/.
- Usage budget: claude-s state=available used=32% → dispatch.
- Baseline suite running detached: baseline_test.log / baseline_test.done (scripts/test.sh).
- Bead authoring dispatched (review lane, claude default; Codex quota-down): prompt bead_authoring_prompt_v1.txt, done=bead_authoring_v1.md.done.
- Next: watch bead_authoring_v1.md.done + baseline_test.done → read manifest → bead QA round 1.

## 2026-08-07 — Baseline not green (pre-existing) + beads authored + QA R1 dispatched
- Baseline suite: 1 pre-existing failure, everything else green. `cmd/bd/doctor/fix` → TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file: "expected error when git config is read-only" (baseline_test.log). Not caused by this item — worktree tip c6076387c has zero code changes.
- Root cause probed (scratchpad/probe_gitconfig.sh, git 2.43.0): `git config` writes through a lock file plus rename inside `.git/`, so a 0444 `.git/config` is still rewritten when `.git/` itself is writable — git_config_exit=0, value landed. The test's premise (read-only config file blocks the write) is false on this host; it chmods the file but not the directory.
- Disposition: fix it inside this item as a baseline-repair bead — AGENTS.md baseline-zero, and plan_v4 Verification requires `go test ./...` all pass, which this failure blocks. Bead to be added under the Phase 4 epic, blocking the deploy bead.
- Bead authoring v1 complete (exit=0): manifest bd-ok4pr.1,bd-ok4pr.2,bd-ok4pr.3,bd-ok4pr.4 under root bd-ok4pr; 7 tasks (1.1 storage core, 1.2 claim tests+race, 2.1 RPC+CLI, 3.1 importer+round-trip, 3.2 merge driver, 4.1 docs, 4.2 E2E+deploy).
- Authoring agent flagged two items for me: (a) deploy bead runs `bd daemon --stop-all`, which touches daemons this session did not start — approved in plan_v4 Deploy (w2 precedent, documented lifecycle op, daemons autostart); kept. (b) bead 3.1's round-trip test needs types.ClaimExpiresAt from 1.1 to compile but was left unblocked — a real dependency gap; QA should catch it, and I will add the edge regardless.
- Bead QA R1 dispatched (review lane): prompt bead_qa_prompt_v1.txt, done=bead_review_codex_v1.md.done.
- Next: watch bead_review_codex_v1.md.done → disposition findings → add 3.1←1.1 edge + baseline-repair bead → per-bead loop.

## 2026-08-07 — Bead QA R1 dispositioned → authoring R2 dispatched
- bead_review_codex_v1.md: FIX findings=9 (1 high, 7 med, 1 low) → ACCEPT 9, REJECT 0.
  - F1 (high) ACCEPT: 3.1 unblocked but its round-trip test needs types.ClaimExpiresAt/migration 027/ClaimIssue from 1.1 → dep add. Same gap the authoring agent self-reported.
  - F2 ACCEPT, source-verified: scripttest runner globs "testdata/*.txt" (cmd/bd/scripttest_test.go:43) and cmd/bd/testdata/ is flat — scripts under testdata/script/ would never run, so the CLI exit-code gate would pass while testing nothing.
  - F3/F4 ACCEPT: multi-file `grep -q` exits 0 on the FIRST matching file, so the storage bead could pass with the memory backend and tx twin unimplemented, and the RPC bead with the handler or client missing. Per-file loop instead.
  - F5 ACCEPT: no internal/storage/sqlite/testdata/ exists; migration test builds the pre-027 schema in-code, no committed fixture DB.
  - F6 ACCEPT: 4.2 asserted an artifact no acceptance command wrote (both E2E runs piped into tail/grep) — a correct build would fail the gate.
  - F7 ACCEPT, source-verified: bare `bd version` exits 0 against the old v0.34 binary, so a silently failed `go install` passes. `version --json` carries a build-time `commit` from resolveCommitHash (version.go:116-130 — ldflags/VCS stamp, not a runtime git call), so grepping short HEAD is a real identity check.
  - F8 ACCEPT: `bd daemon --stop-all` hits all six live daemons on this shared host; bead now records `daemons list --json` before and re-checks `daemons health --json` after. Approved deploy step, but it gets a record-then-verify trail.
  - F9 (low) ACCEPT: 1.1 spanned 12 files across migration, types, interface, two backends and four read-path files. Split: core keeps migration/types/interface/claim.go/queries.go/issues.go/memory/transaction; new sibling carries ready.go+dependencies.go+multirepo.go read-path plumbing, blocked by the core, grep-closure acceptance on the `pinned` precedent.
- Manager-added bead (deliberate plan deviation, rationale logged above): baseline repair of TestMergeDriverWithLockedConfig_E2E under epic bd-ok4pr.4, blocking 4.2. Cause is in the test, not the product code: it chmods .git/config to 0444 but leaves .git/ writable, and git rewrites config by lock-file+rename.
- Authoring R2 dispatched with all nine fixes + the baseline bead: prompt bead_authoring_prompt_v2.txt, done=bead_authoring_v2.md.done. QA cap 2 rounds → R2 QA next.
- Next: watch bead_authoring_v2.md.done → QA R2 → per-bead loop.

## 2026-08-07 — Authoring R2 applied → QA R2 dispatched
- bead_authoring_v2.md exit=0; manifest unchanged: bd-ok4pr.1,bd-ok4pr.2,bd-ok4pr.3,bd-ok4pr.4. Tree now 9 tasks: 1.1 core, 1.2 tests+race, 1.3 read-path plumbing (F9 split), 2.1 RPC+CLI, 3.1 importer+round-trip, 3.2 merge driver, 4.1 docs, 4.2 E2E+deploy, 4.3 baseline repair.
- QA R2 dispatched (cap round 2 of 2): prompt bead_qa_prompt_v2.txt, verifies the nine round-1 fixes landed, reviews the two new beads (1.3, 4.3) in full, scans for defects the fixes introduced. done=bead_review_codex_v2.md.done.
- Exit rule: zero accepted FIX → per-bead loop; at cap, remaining findings logged as accepted risks.
- Next: watch bead_review_codex_v2.md.done → disposition → start per-bead loop with bd-ok4pr.1.1.

## 2026-08-07 — QA R2 dispositioned (6 ACCEPT / 2 REJECT) → fixes dispatched
- bead_review_codex_v2.md: FIX findings=8 (4 high, 4 med) → ACCEPT 6, REJECT 2. Four highs source-verified before accepting.
  - F1 (high) ACCEPT, verified: `go env GOBIN` empty, GOPATH=/home/ben/go → bare `go install ./cmd/bd/` writes /home/ben/go/bin/bd (stale, 2025-07-18) while PATH resolves /home/ben/.local/bin/bd (Aug 5) first. plan_v4's deploy line carries this defect; the bead now uses `GOBIN=$HOME/.local/bin go install ./cmd/bd/`. Without it the deploy would report success and leave all 17 stores on the old binary. Plan-text deviation, logged here rather than replanned: the plan's own live-verify line already names ~/.local/bin/bd as the target, so this corrects the command to the plan's stated intent.
  - F2 (high) ACCEPT, verified labels.go:156-166: GetIssuesByLabel's fixed 26-column SELECT feeds the shared scanIssues that bead 1.3 modifies; labels.go was in no bead's Touches → every label query would die on a column-count mismatch with no bead legally able to fix it.
  - F3 (high) ACCEPT, verified queries.go:1644-1662 ends in `return s.scanIssues(ctx, rows)`: SearchIssues's SELECT list and the shared scanner must move in the SAME bead, else bead 1.1's own gate breaks. 1.1 now excludes the scanIssues-delegating lists; 1.3 owns queries.go's SearchIssues list.
  - F4 (high) ACCEPT: the ladder's `in_progress ∧ assignee==""` cell is about the STORED row; the empty-ARGUMENT rejection was only in the CLI bead, so a core built to spec would have contradicted 1.2's required matrix. Contract line added at the storage layer, which is the authority.
  - F5 ACCEPT, verified dependencies.go:512/:553: the recursive dep-tree CTEs carry deliberately partial column lists (no pinned, no ephemeral); the plan's ":513, :554" pointer would have produced a runtime Scan error. Bead cites :252, :274, :827-:837, :930-:941 and states the CTEs stay unchanged.
  - F8 ACCEPT: prior HEAD now written to work/w3_atomic-claim/artifacts/prior_head.txt with an acceptance assert — the plan's rollback depends on that value being durable.
  - F6, F7 REJECT: both assume sibling builders share one worktree. They do not — WORK_BEADS_MANUAL.md "Parallelism: one run worktree per builder" (wt-run.sh fork per run, merge-backs serialize, exit 3 forces a re-gate when the tip moved). A sibling's in-flight files are never visible to another bead's gate, so neither the extra 3.1←2.1 edge nor deleting 4.3's full-suite acceptance is warranted; 4.3's whole-repo run IS its proof that baseline reaches zero.
- QA cap reached (2 rounds). The six accepted fixes are corrections to bead text, not a new review round: dispatched authoring R3 to apply them (bead_authoring_prompt_v3.txt, done=bead_authoring_v3.md.done); I verify the edits myself instead of spending a third QA round.
- Next: watch bead_authoring_v3.md.done → verify the six edits by reading the beads → start per-bead loop at bd-ok4pr.1.1.

## 2026-08-07 — Bead edits verified → build wave 1 dispatched (3 builders)
- All six accepted QA-R2 fixes verified in the bead text myself (bd show grep): 4.2 carries GOBIN=$HOME/.local/bin + prior_head.txt assert + version --json identity check; 1.3 Touches now include labels.go and queries.go with the SearchIssues/:1644 line and the ":512/:553 stay unchanged" statement; 1.1 carries both the scanIssues exclusion paragraph and the empty/whitespace assignee-argument rule.
- Dependency edges confirmed directly in the DB (dependencies table): 1.2, 1.3, 2.1, 3.1, 4.2 ← 1.1; 4.2 ← 1.2, 1.3, 2.1, 3.1, 3.2, 4.1, 4.3. Note: `bd dep tree --direction=up` renders 4.2 as [READY] despite eight blockers — display quirk, the rows are correct; I dispatch in dependency order myself.
- Wave 1 = the three beads with no blockers: 1.1 storage core, 3.2 merge driver, 4.3 baseline repair. 4.1 (docs) deliberately deferred to wave 2 so CODEMAP/CONTEXT rows describe files that already exist.
- Run worktrees forked from tip c6076387c: .runs/bd-ok4pr.1.1.1786118144043329613, .runs/bd-ok4pr.3.2.1786118144677961643, .runs/bd-ok4pr.4.3.1786118145278812025. Beads set in_progress. Builders dispatched on the claude lane; prompts builder_prompt_<bead>.txt carry the worker contract and the build-report contract verbatim.
- Usage budget before the wave: claude-s available, 42%.
- Next: watch the three builds/<bead>.<run>.done receipts → verify each by the five durable-state checks → merge back one at a time → per-bead review.

## 2026-08-07 — Wave 1 verified + merged; wave 2 + 3 reviews dispatched
- Wave 1 receipts: all three .done exit=0. Durable-state checks on each: bead closed, one (bead)-tagged commit, gate green, build report + step logs present.
  - 1.1 → 634e7d8c5, 10 files. 3.2 → e561be38d (post-merge a7e12fb35), 2 files. 4.3 → 901a0ab07 (post-merge 69d9a27b9), 1 file.
- Touches deviation, ACCEPTED: 1.1 also changed internal/storage/sqlite/migrations_test.go, which its Touches omitted. Inspected the diff: TestMigrateContentHashColumn rebuilds the issues table by hand, so it needs the new column plus a NULL in its INSERT ... SELECT or the suite breaks after migration 027. Required collateral, squarely inside the bead's responsibility, and the plan's proof table asks for migration evidence. Bead-text defect, not builder overreach; no reopen.
- Evidence relocation: builders 3.2 and 4.3 wrote build reports and step logs relative to their RUN tree (work/w3_atomic-claim/builds/) because my prompt gave a relative path. Moved the files unchanged into the item workdir builds/ before merge-back (wt-run merge relocates a run tree's ignored files to trash). Contents untouched. Wave-2 prompts now give the absolute path.
- MY ERROR, corrected: the merge of 1.1 hit exit 5 (dirty run tree) on an untracked .bdc.conf each builder had written. I trashed those files as scratch, then discovered why they existed — bdc has NO default gate for this Go repo ("no gate: ... declare GATE_CMD in .bdc.conf"), so every builder had to invent one, and each narrowed the gate to its own packages. Restored one file to confirm, then fixed the root cause instead: committed a tracked repo-level .bdc.conf at the item tip (fae879839) with GATE_CMD='go build ./... && go test ./... && go vet ./...'. Every run tree now inherits one consistent full-suite gate, no per-builder invention, no untracked dirt blocking merges. Manager-written config, not product code; logged as a deliberate small addition beyond plan_v4.
- Merge order forced by that fix: 4.3 first (its own repair is what makes the new full-suite gate pass), then 3.2. Both re-gated green under the full suite after rebase (wt-run merge exit 3 → bdc test → --regated). Item tip a7e12fb35.
- BASELINE IS NOW ZERO: full `go test ./...` green in the 4.3 run tree at item tip. The pre-existing failure recorded at the entry checklist is gone.
- Wave 2 dispatched (forked from a7e12fb35): 1.2 claim matrix+race (run 1786119304126143397), 1.3 read-path plumbing (1786119304518562984), 2.1 RPC+CLI (1786119304934325447).
- Per-bead reviews dispatched for all three merged beads (read-only, item worktree): reviews/review_bd-ok4pr.{1.1,3.2,4.3}_v1.md.
- Usage budget before the batch: claude-s available, 48%. Held 3.1 and 4.1 for wave 3 to pace the window.
- Next: watch six receipts (3 builds + 3 reviews) → disposition review findings → verify+merge wave 2 → dispatch 3.1, 4.1.

## 2026-08-07 — Wave 1 reviews dispositioned; wave 2 merged; wave 3 dispatched
- Review of 4.3: CLEAN. Review of 3.2: bead contract fully met (one out-of-scope finding, below). Review of 1.1: FIX findings=4 → ACCEPT 4, all in files 1.1 was forbidden to touch, so they become one fix bead rather than a reopen.
  - 1.1-F1 (med) ACCEPT: schema_probe.go expectedSchema["issues"] stops at `pinned`, so a store whose migration 027 did not apply PASSES the probe whose whole job is to trigger the migration retry (store.go:158-172) and then fails at runtime on every GetIssue/SearchIssues, which now select the column. A diagnosable startup error turned into a total read-path failure.
  - 1.1-F2 (med) ACCEPT: memory backend UpdateIssue has no claim_expires_at case while sqlite admits the key via allowedUpdateFields, so in --no-db mode any update carrying a lease (including the import path 3.1 builds on) silently drops it, leaving the row in_progress with no expiry — permanently unstealable there.
  - 1.1-F3 (low) ACCEPT: memory claim event carries nil payload, so a --no-db steal records no prior holder while sqlite's does; the claim contract says the steal event carries it.
  - 1.1-F4 (low) ACCEPT: schema.go CREATE TABLE omits the column although `pinned` (023) and `is_template` (026) appear in both constant and migration; a fresh DB gets the column only because RunMigrations happens to follow db.Exec(schema).
  - Fix bead bd-ok4pr.1.4 authored under epic bd-ok4pr.1 carrying all four with their mechanisms; wired as a blocker of 4.2.
- 3.2-R1 (med) REJECT for this item, ESCALATED as a finding to surface: the same struct-rebuild mechanism this bead fixed still drops five keys that exist in the live store today — close_reason (600 rows), wisp (107), labels (65), external_ref (16), comments (13). Merge3Way re-marshals every row it rewrites from merge.Issue, so any merge that rewrites a row silently deletes those fields from issues.jsonl. PRE-EXISTING and outside this item's scope (the reviewer says so explicitly: "no rework of this commit"), and our change strictly reduces it by modeling assignee. It is real data loss in the fleet's tracker, so it goes in the work report's residual-risk table, a follow-up item, and my closing report to the user rather than being silently absorbed.
- Wave 2 verified by the five durable-state checks and merged in order: 1.3 read-path plumbing → 9c7651f9d (5 files), 1.2 claim matrix+race → d4f3b2b19 (claim_test.go), 2.1 RPC+CLI → 94105d544 (8 files incl. cmd/bd/testdata/claim.txt, flat testdata as required). Each rebased + re-gated green on the full suite before the ff.
- Ledger rows appended for the two CLEAN beads (4.3, 3.2) with their post-merge tagged SHAs. 1.1's row waits until 1.4 lands and closes its four findings.
- Wave 3 dispatched (forked from 94105d544): 1.4 fix bead (1786120664782510009), 3.1 importer+round-trip (1786120665171689172), 4.1 docs (1786120665573585553).
- Usage budget before the wave: claude-s available, 62%. Held the three wave-2 reviews back to keep concurrency at three and pace the window.
- Next: watch three build receipts → merge → dispatch reviews for 1.2, 1.3, 2.1, 1.4, 3.1, 4.1 → then 4.2 (E2E + deploy) last.

## 2026-08-07 — Wave 3 merged; six per-bead reviews dispatched
- Wave 3 verified by the five durable-state checks and merged in order: 1.4 fix bead → a0d51ee71 (schema.go, schema_probe.go, memory.go, memory_test.go), 3.1 importer+round-trip → c481f0290 (importer.go, utils.go, export_test.go), 4.1 docs → 41069aa0c (CODEMAP.md, CONTEXT.md). Each rebased and re-gated green on the full suite before its ff. All Touches respected, no dirty trees.
- Every implementation bead is now merged. Only 4.2 (E2E + deploy) remains, and it is blocked until all eight others are reviewed.
- Six reviewers dispatched over the merged ranges: 1.2 (9c7651f9d..d4f3b2b19), 1.3 (a7e12fb35..9c7651f9d), 2.1 (d4f3b2b19..94105d544), 1.4 (94105d544..a0d51ee71), 3.1 (a0d51ee71..c481f0290), 4.1 (c481f0290..41069aa0c). Each prompt names the bead's own defect class rather than asking for a generic pass: race-test strength for 1.2, grep-closure against the `pinned` precedent for 1.3, the exit-code and trim rules for 2.1, probe-actually-fails for 1.4, all-three-update-maps for 3.1, accuracy-over-presence for 4.1.
- Usage budget: claude-s 66% with the window resetting inside three minutes, so the six-review batch lands mostly in the fresh window.
- Next: watch six review receipts → disposition → ledger rows → 4.2 (E2E + deploy) → Completion pass.

## 2026-08-07 — Six per-bead reviews dispositioned → five fix beads dispatched
- Verdicts: 1.3 CLEAN. 1.2 FIX=3, 2.1 FIX=5, 1.4 FIX=5, 3.1 FIX=1, 4.1 FIX=3. Total 17 findings → ACCEPT 16, REJECT 1. These reviewers ran MUTATION checks (delete an implementation line, re-run the suite), which is why so many landed: nearly every accepted finding is a contract the plan states and no test protected.
  - 1.2-F1/F2/F3 ACCEPT: no test compares stored expiry against the REQUESTED lease (an implementation using `now.Add(lease*7)` passes everything — a dead builder's bead would stay unstealable for 7x the contracted lease); the tombstone rung is asserted only in a comment (deleting `|| issue.IsTombstone()` stays green, so a tombstoned issue could be resurrected as in_progress); "not found" untested.
  - 2.1-F1 (HIGH) ACCEPT: `cmd/bd/scripttest_test.go` is behind `//go:build scripttests`, so the gate `go test ./cmd/bd/` NEVER runs testdata/claim.txt (`-run Script` → "no tests to run"). The exit-code contract — the user-visible product of this item — had zero enforced coverage. This is the same illusory-coverage class bead QA caught at the path level, one layer deeper. Fix is a tag-free Go test, NOT adding the tag to the gate.
  - 2.1-F2 ACCEPT: `bd` silently falls back to direct mode ("Daemon took too long to start (>5s)"), so a daemon-mode assertion can pass while the RPC path is broken. 2.1-F3 ACCEPT: `int64(lease.Seconds())` truncation makes `--lease 90s500ms` expire differently depending on whether a daemon is running. 2.1-F4 ACCEPT: handleClaim accepts zero/negative LeaseSeconds (instantly-stealable claim) while the CLI rejects <1s.
  - 2.1-F5 REJECT (scope): three scripttest files (help.txt, init.txt, dep_add.txt) fail today under `-tags scripttests`. Pre-existing, in a tag-gated suite that is NOT part of the default gate, so it is not this item's baseline. The F1 fix deliberately avoids dragging them in. Follow-up + residual-risk row.
  - 1.4-F1/F3 ACCEPT: the probe entry and the schema constant both have NO test behind them (mutation-verified green after deletion), so the fix can silently fall back out; F3's test executes the schema constant with no migrations, which is what makes the next late-added column unable to drift. 1.4-F2 ACCEPT: `is_template` is now the only unprobed column — same mechanism left open one column over, one word to fix. 1.4-F4 ACCEPT: claimEventValue is a verbatim second copy across backends. 1.4-F5 ACCEPT: memory's update case silently ignores a `time.Time` passed by value, which sqlite accepts — the same silent-drop the case was added to remove.
  - 3.1-R1 ACCEPT: handleRename's lease mapping is the one map no test reaches (mutation-verified).
  - 4.1 x3 ACCEPT: CODEMAP says "26 migrations" where the code now registers 27; CONTEXT implies UpdateIssue evaluates ownership and loses a race when it has no precondition at all; the renewal note omits that a renewal without --lease CLEARS the expiry, which defeats the note's own advice.
- Five fix beads authored from the finding text and wired as blockers of 4.2: 1.5 (claim matrix tests), 2.2 (exit-code gate + RPC lease rules, P0), 1.6 (probe/constant tests + shared event payload), 3.3 (rename-collision test), 4.4 (doc corrections). Dispatched in parallel from tip 41069aa0c.
- Ledger row appended for 1.3 (CLEAN).
- Usage budget: window reset, claude-s at 3% — the five-builder wave fits comfortably.
- Next: watch five receipts → merge → re-review each fix range → ledger rows for all beads → 4.2 (E2E + deploy) → Completion.

## 2026-08-07 — Five fix beads merged; scoped re-reviews dispatched
- All five verified by the five durable-state checks and merged in order, each rebased and re-gated green on the full suite: 1.5 → 9aec04dcf, 1.6 → a4497cb8b, 2.2 → f2f2c5ecf, 3.3 → 46305de95, 4.4 → 339b2dd1f. Item tip 339b2dd1f.
- Self-check on the highest-severity finding before handing it to a reviewer: `go test ./cmd/bd/ -run Claim -v` now executes TestClaimExitCodesInDirectMode and TestClaimExitCodesInDaemonMode (cmd/bd/claim_test.go:333, :376), which build the binary and assert real process exit codes, plus the sub-second truncation and non-positive-lease cases. The exit-code contract is now enforced by the DEFAULT gate, with no build tag. That was the point of 2.2.
- Five scoped re-reviews dispatched over each fix range. Each prompt requires the reviewer to confirm every numbered Contract item is genuinely closed and to repeat the MUTATION standard the first round used: break the protected line, confirm the new test fails, restore. A test that passes either way means the item is not closed regardless of the diff.
- Next: watch five receipts → disposition → ledger rows for all fourteen beads → 4.2 (E2E + deploy) → Completion pass.

## 2026-08-07 — Re-reviews dispositioned: 1 CLEAN, 8 findings → round-3 fixes dispatched
- Verdicts: 1.5 CLEAN (ledger row appended). 1.6 FIX=2, 2.2 FIX=3, 3.3 FIX=1, 4.4 FIX=2 → ACCEPT 8, REJECT 0.
  - 1.6-F1 (med) ACCEPT — a real bug the previous fix introduced: the new `default` arm returns from INSIDE the update loop, and Go randomizes map iteration, so keys already applied stay written while the dirty mark and event row are skipped. Probe: a mutated title/notes survived in 17 of 20 runs. A silently half-updated issue with no audit trail. Fix validates before the loop so a rejected update changes nothing.
  - 1.6-F2 (med) ACCEPT: the `is_template` probe entry has no test (mutation-verified green after deleting it) — the same mechanism, one column over. Fix generalizes the single-column test into a loop over every probed column, which makes future columns self-protecting instead of needing a new test each time.
  - 2.2-F1 (HIGH) ACCEPT: the denial assertion compares the observed exit against the package's OWN `claimDeniedExit` constant, so it is tautological — redefining `const claimDeniedExit = 1` leaves the whole gate green (mutation-verified). The exit-3 contract that bd-ok4pr.2.2 existed to enforce was still unasserted. Fix asserts the literal 3 and audits the file for the same pattern.
  - 2.2-F2 (med) ACCEPT: `claimTestBinary` builds a 33 MB binary into a temp dir nothing removes — 23 directories, ~760 MB, had accumulated in this worktree (the reviewer trashed them). Matches the known /tmp test-leak class in context/entities/disk-hygiene.md. Cleanup must run even when tests fail.
  - 2.2-F3 (low) ACCEPT: the "direct mode" stderr guard catches only the unconditional auto-start-timeout warning; connect-failed and health-failed print through emitVerboseWarning, gated on BD_VERBOSE, which the test never sets. Set it, and say plainly that the metrics check is the primary proof of the RPC route.
  - 3.3-1 (low) ACCEPT: the rename subtest's status/assignee assertion is seeded true — ComputeContentHash folds Status and Assignee in, so the twin MUST already carry them to route through handleRename at all. It advertises coverage the subtest does not have.
  - 4.4-F1/F2 (low) ACCEPT: the CONTEXT citation points at the UPDATE's SQL text rather than the clearing decision at claim.go:121-125, and the new clause starts with a lowercase sentence mid-note.
- SYSTEMIC OBSERVATION, acted on structurally rather than by repeating an instruction: this item has now produced three distinct tests that could not fail — an assertion compared against a constant from the code under test, an assertion seeded true by its own fixture, and a whole suite behind a build tag that never ran. Root cause is builders asserting the implementation rather than the contract. Every round-3 bead now carries a "Test discipline" section requiring literal expected values (never a constant imported from the code under test) and a per-test mutation check recorded in the build report: what was broken, which test failed, that the file was restored.
- Round-3 fix beads created and dispatched from tip 339b2dd1f, all wired as blockers of 4.2: 1.7 (mid-loop abort + probe loop), 2.3 (literal exit code + binary leak, P0), 3.4 (seeded assertion + CONTEXT note).
- Usage budget: claude-s 11%.
- Next: watch three receipts → merge → re-review → ledger rows → 4.2 (E2E + deploy) → Completion.

## 2026-08-07 — Round-3 fixes merged; third-round re-reviews dispatched
- All three verified and merged, each rebased and re-gated green: 1.7 → ff94580f1, 2.3 → f1613bdf9, 3.4 → d17a99556. Item tip d17a99556.
- My own spot-check of the four load-bearing changes before dispatching reviewers: cmd/bd/claim_test.go:381 and :446 now assert the LITERAL `denied.exit != 3` with the constant only in the message; TestMain (claim_test.go:243) removes the 33 MB build directory; memory.go validates the claim_expires_at value type at :372, ahead of the apply loop at :449; schema_probe_test.go gained TestProbeSchema_DetectsEveryDroppedIssuesColumn (a loop over every probed column) and TestSchemaConstantCoversProbedIssueColumns.
- Third-round re-reviews dispatched. Each prompt makes the mutation standard the core of the review, not a footnote: for EVERY test in range, decide whether it can fail at all and prove it by breaking the protected line; report any test that passes either way; restore every file and confirm `git status --porcelain` is clean before finishing. The 2.3 prompt names the exact mutation (`const claimDeniedExit = 1`) that must now break the gate, and the 1.7 prompt requires ten randomized-order runs to prove a rejected update writes nothing.
- Fix-cycle accounting: this is cycle 3 on the 1.x and 2.x lineages. If this round returns another defect INTRODUCED by a fix, the fix process itself becomes the hazard (WORK.md Final Review trajectory test) and I stop and escalate rather than dispatching a fourth round.
- Next: watch three receipts → disposition → ledger rows for all seventeen beads → 4.2 (E2E + deploy) → Completion pass.

## 2026-08-07 — Round-3 re-reviews: no new defects, four LOW findings → one comment bead
- Verdicts: 1.7 FIX=1 (low), 2.3 FIX=2 (low), 3.4 FIX=1 (low) → ACCEPT 4, REJECT 0. TRAJECTORY TEST PASSES: zero findings above `low`, and — the thing I was watching for — not one defect INTRODUCED by a round-3 fix. The mid-loop abort, the tautological exit assertion and the seeded-true rename assertion are all confirmed closed by mutation. The fix process is converging, not oscillating, so no escalation.
- All four remaining findings are comment text that misleads a reader into editing the wrong thing: a doc comment naming a function that does not exist (`...LeaveIssue...` vs `...LeavesIssue...`), a comment claiming "every fallback message says 'direct mode'" when two of seven reasons print nothing, and a TestClaimRoundTrip comment saying "three import paths" for four subtests while naming `insertIssue` where the fresh store actually writes through the batch `insertIssues` (reviewer proved the distinction by mutation: nulling the lease in insertIssue stays green, nulling it in insertIssues fails).
- 2.3-F2 was not a code finding: four leaked /tmp/bd-claim-e2e-* directories (132 MB) from the builder's own PRE-fix runs, which the new TestMain cleanup cannot retroactively remove. I trashed them myself (batch 20260807T174107Z-3114983) — debris from runs this session started. Verified gone.
- Consolidated the four into ONE comment-only bead, bd-ok4pr.4.5, rather than a fourth code round: no statements, assertions or signatures change, so it carries no risk of introducing a defect, and shipping knowingly-wrong comments would mislead the next reader of the exact code this item just hardened.
- Ledger: 17 rows now cover every reviewed bead with its post-merge tagged SHA, each carrying the fix beads its findings produced.
- Next: watch 4.5 → merge + review → 4.2 (E2E + deploy, the last bead) → Completion pass.

## 2026-08-07 — Comment bead merged; 4.2 rescoped to pre-merge proof; E2E dispatched
- 4.5 merged → dcbe1a612. I verified this one myself instead of spending a reviewer round: the change is three comments, and I read the full diff. All three corrections are accurate — the function-name typo is fixed, the fallback comment now states only what is true and names the two silent reasons, and the round-trip comment says four paths, names the batch `insertIssues`, and describes the rename-collision writer. Ledger row recorded as CLEAN with that rationale.
- Rebase before evidence: `wt-sync.sh beads` → all-verified, `git rebase main` → already up to date. Main has not moved since the item started, so the evidence covers current main. Item tip dcbe1a612.
- RESCOPED bd-ok4pr.4.2 before dispatching it (retitled "Write and run the E2E claim proof"). As authored it bundled `go install`, `bd daemon --stop-all`, the daemon inventory/health records and the live verify into a builder run. That is wrong on two counts: WORK.md puts deploy steps in the manager's hands with one log entry per step, and those commands mutate SHARED HOST STATE — they replace the fleet's `bd` binary and stop roughly six daemons belonging to other agents' workspaces. A builder in a run worktree is the wrong actor for that, and the plan itself puts the live verify post-merge. The bead now ends at the executed E2E proof plus prior_head.txt; the deploy sequence moves to the Deploy phase I run and log after the merge. The scope change is recorded in the bead body so the reviewer sees why those commands are absent.
- Builder prompt carries hard shared-host boundaries: the script uses its own mktemp store with --no-daemon, never touches the fleet store or ~/.local/bin/bd or any live daemon, and cleans up on every exit path.
- Next: watch the E2E receipt → merge → review 4.2 → epic close → evidence packet → Final Review → Wrap-Up → Deploy.

## 2026-08-07 — E2E FAILED, real defect found → RCA-first, dependent steps STOPPED
- bd-ok4pr.4.2's E2E script runs and its step 2 PASSES — two concurrent claims give exactly one exit 0 and one exit 3, stored assignee equals the winner. That is the original probe scenario, now fixed: the race this item exists to remove is gone.
- Steps 3, 4 and 6 FAIL. The builder correctly did NOT close the bead and did not run `bdc` gate-and-close; it committed the script plus its failing run and wrote a `blocked` block in the build report with a root-cause hypothesis. Bead stays in_progress. This is the behavior the prompts asked for and it is the right call.
- Symptom: an owner lease written by one `bd` process is NULL by the time the next `bd` process reads it, so an expired lease is never stealable — the second claimant reads NULL as "never expires" and is denied. Renewal fails the same way.
- I REPRODUCED IT MYSELF before accepting the diagnosis (scratchpad/probe_wipe.sh, scratch store, worktree binary /tmp/bd-w3): after `claim --lease 600s` the column holds 2026-08-07T20:09:53; after a plain `bd --no-daemon list` it reads NULL. A `pinned=1` I set by hand reset to 0 in the same step. `updated_at` is UNCHANGED, so nothing about the row looks modified.
- Builder's hypothesis, now under independent RCA: migrations 019 and 022 fight on every database open — 019 re-adds four edge columns whenever missing, 022 then sees them and rebuilds the issues table from a hard-coded 28-column list, so every column added after 022 (pinned/023, is_template/026, claim_expires_at/027) is re-created EMPTY on each open. The builder reports the same wipe on the INSTALLED baseline binary, which would make this pre-existing and unrelated to this item's code.
- Why the unit tests never caught it: they exercise ClaimIssue inside a single already-open store, where the 019/022 pair has already run. Only a SECOND process exposes the wipe. That gap is exactly what the E2E proof was written to close, and it closed it.
- Per WORK.md Unexpected Error → RCA-First: dependent steps are STOPPED. No epic close, no evidence packet, no Final Review, no merge, and above all NO DEPLOY — installing a binary while this is unresolved would change nothing about the defect but would put the fleet on an untested build.
- RCA dispatched on the review lane (rca_prompt_v1.txt → rca_v1.md): confirm or refute the mechanism against source, state the blast radius across the 17 live stores, and give 2-3 candidate fixes weighed by risk to existing data, plus the regression test that would have caught it.
- Next: read rca_v1.md → verify the mechanism myself → decide scope (bug bead inside this item vs plan revision) → surface to the user, since any fix changes migration behavior for every store on this host.

## 2026-08-07 — RCA confirmed; migration fix taken into scope and dispatched
- User direction this session: "What you need to figure out is how you can get this job done ... if you had trouble with the infrastructure, then include that as a follow up in your work report. job has to get done." So: fix it here, finish the item, deploy, and record the infrastructure friction as follow-up rather than escalating.
- rca_v1.md CONFIRMS the builder's hypothesis and sharpens it. Root cause: `RunMigrations` (migrations.go:141-145) re-executes ALL 27 migrations on every database open because there is no applied-migration ledger anywhere in the repo (grep for schema_migrations/user_version/applied_migrations returns nothing), and 019/022 compose into a cycle — 019 re-adds four edge columns, 022's guard therefore never holds, and 022 rebuilds `issues` from a HARD-CODED 28-column list (022:129-145), dropping and renaming the table (:151, :157). Columns added after 022 are re-created empty. `updated_at` is copied verbatim, so the row looks untouched.
- Decisive evidence in the RCA, beyond my own probe: a sentinel column AND a trigger on `issues` both vanish after one `bd list`, while a trigger on `events` survives. Only a table rebuild explains that — it rules out any UPDATE or struct-writeback theory. The RCA also states what would have falsified each conclusion and confirms it looked.
- Blast radius: `pinned` (023), `is_template` (024), `claim_expires_at` (027) lose data on EVERY `bd` invocation including read-only ones, on every store, since v0.30.7 (2025-12-19) — verified against the INSTALLED baseline binary, so it predates this item by eight months. `bd pin` has been silently non-functional across processes that whole time. Two indexes (idx_issues_ephemeral, idx_issues_sender) are also permanently absent at rest, so `sender` queries run unindexed, and every command needlessly rewrites the whole issues table.
- Scope decision: IN SCOPE. This item's core semantics (lease, steal, renew) cannot work while the column is wiped between processes, so the item cannot deliver its User Intent without the fix. Bead bd-ok4pr.1.8 (P0) authored from the RCA's recommendation — fix B (022 copies columns discovered from pragma_table_info) plus fix C (schema_migrations ledger), B first, in one change, with the reopen-survival regression test enumerating columns from pragma_table_info.
- Explicitly OUT of scope, per the RCA's own constraint: fix A (stopping 019 re-adding the columns). `021_migrate_edge_fields.go:27-30` queries those columns with NO existence guard, so 019's re-add is currently the only thing keeping 021 from failing with "no such column". Removing it without guarding 021 breaks every `bd` command. Separate follow-up item; the bead says do not touch either file.
- Builder prompt carries the stakes plainly (17 live stores), the exact backfill rule ("run everything once more, then record everything" — never detect-and-skip, which can half-migrate a store), hard boundaries against any store outside its own temp dirs, and a requirement to paste a real two-process reproduction transcript, since the unit suite passed throughout while this bug was live.
- Follow-ups for the work report, not this item: (1) fix A cleanup with 021 guarded; (2) recovery of `pinned`/`is_template` values from `git log -p .beads/issues.jsonl` on the 17 live stores — the RCA verified values reach the JSONL but are clobbered by the next export, so anything never committed is gone; (3) the merge driver still dropping close_reason/wisp/labels/external_ref/comments; (4) three stale tag-gated scripttest files.
- Next: watch the receipt → verify by two-process reproduction MYSELF → merge → review → re-run the E2E (4.2) → Completion.

## 2026-08-07 — Migration fix merged; E2E now PASSES
- bd-ok4pr.1.8 merged → fa5407105 (migrations.go, 022_drop_edge_columns.go, migrations_test.go, schema.go, claim_test.go). Merge first hit exit 5 on an untracked path that was MY doing — I had copied rca_v1.md into the run tree so the builder could read it. Trashed just that directory (the first attempt correctly refused: my target included 139 tracked files from other work items' dirs).
- Builder evidence is the strongest of the item: a before/after reproduction using a pre-fix binary built from `git archive HEAD` into the same temp dir, so the contrast is the same steps against the same code minus this change. PRE-FIX: pinned=1 and claim_expires_at set, then one `bd list` → pinned=0, claim_expires_at=NULL, probe trigger gone. AFTER: all values and the trigger survive two further opens, ledger holds 27 migrations, and idx_issues_ephemeral/idx_issues_sender exist at rest again.
- The legacy-store case was tested explicitly, and it is the one that matters for the 17 live stores: a store created by the PRE-fix binary has no ledger, exactly like production. Opening it once with the fixed binary is the single backfill pass and it loses nothing — values set before the upgrade survive it and every open after.
- Falsification check the builder ran: with 022 restored from HEAD and the ledger skip disabled, the new TestIssueColumnsSurviveReopen fails naming exactly the RCA's three columns plus the rebuild itself. The test bites.
- Touches deviation ACCEPTED: claim_test.go (5 lines) was outside the allowlist. TestClaimMigrationExistingStore built its pre-027 fixture on the premise "migrations are idempotent and re-run at every open", which the ledger makes false, so it failed the bead's own `go test ./...`. The fixture now also removes the claim_expires_at_column ledger row, which is what a genuine pre-027 store looks like. No assertion weakened. Required collateral of the fix.
- E2E RE-RUN: **E2E_PASS**. Every step green — concurrent claims give one exit 0 and one exit 3 with the winner stored; the expired lease is STOLEN with previous_holder=agent-C in the event; the holder RENEWS; leaving in_progress clears the lease; and the lease round-trips into both a fresh store and one already holding the issue.
- One false alarm worth recording: my first re-run still failed, because e2e_claim.sh defaults to the binary at /tmp/bd-w3 and I had built that path BEFORE the fix. Rebuilding from the fixed tree turned all 8 failures green. The script was right both times.
- E2E script commit merged → 9a59bda9a. Fresh run 1786127113258652334 dispatched to execute the acceptance commands for the record, regenerate the artifacts from its own run, and close the bead properly through bdc.
- Next: watch → merge → review 4.2 → epic close → evidence packet → Final Review → Wrap-Up → Deploy.

## 2026-08-07 — Reviews of the migration fix and E2E: 7 findings, 2 blocking deploy
- bd-ok4pr.4.2: E2E script itself reviewed FIX=1 — but the finding is not about the script. prior_head.txt recorded 9a59bda9a, a commit on THIS branch that already contains the entire implementation, so the plan's rollback (`git checkout <prior HEAD> && go install`, plan_v4.md:209) would rebuild the very binary it is meant to remove and leave the fleet on the new code believing it had rolled back. Caught before deploy, which is exactly when it had to be caught. ACCEPT.
- bd-ok4pr.1.8: FIX=6 → ACCEPT 6. Two matter a great deal:
  - F1 (high): TestIssueColumnsSurviveReopen claims to guard 022's frozen column list but only exercises the LEDGER. The reviewer restored the parent commit's hard-coded 28-column 022 and that test plus two others ALL still passed, because the second RunMigrations skips 022 entirely. The 295-line runtime-column rewrite — the part that must be right on the legacy stores where 022 actually runs — had zero regression coverage. Same illusory-coverage class as the build-tag finding earlier, in a much more dangerous place.
  - F2 (med): runtime column names are interpolated UNQUOTED, so a column needing quotes makes the store PERMANENTLY UNOPENABLE. Reproduced by the reviewer with a column named `order`: every invocation fails inside the migration, correctly rolled back, with no way forward.
  - F3 (med): the ledger silently killed Open's schema self-repair. Reviewer's before/after: with `pinned` dropped from a scratch store the PARENT binary re-added it and opened normally; this build refuses with "schema probe failed after migration retry ... Database may be corrupted". A regression THIS item introduced.
  - F4/F5/F6 (low): a hand-typed duplicate column list (the same two-lists-by-hand defect the change exists to remove); recordMigration uses db.Exec on a pool sized NumCPU()+1, so its "same transaction" guarantee holds only by pool luck and a connection swap would autocommit a ledger row for a migration that later rolls back; a now-false comment plus `bd migrate --inspect` showing no ledger contents.
- Bead bd-ok4pr.1.9 (P0) dispatched with all seven. DEPLOY IS BLOCKED until it lands: F2 and F3 are both "a live store stops opening" class, and F7 is the rollback anchor I would need if the deploy went wrong.
- Trajectory note: 1.8 was a fix that introduced a regression (F3), which is cycle 2 on the migration lineage. It is not the oscillation pattern that would force escalation — the findings are converging (six here, two of them one-line quoting/comment fixes) and each round is catching a genuinely different defect rather than re-fighting the same one. The reviewers are doing exactly what they should: F1, F2 and F3 were each proven by running the code, not by reading it.
- Next: watch → verify myself → merge → review 1.9 → evidence packet → Final Review → Wrap-Up → Deploy.

## 2026-08-07 — Hardening bead merged; final review of it dispatched
- bd-ok4pr.1.9 merged → 29003e307. Wide diff (34 files): every migration's signature now takes a shared connection, plus a new migrations/db.go. That reaches well past the bead's Touches list, but it is REQUIRED COLLATERAL of finding 5, which asked for the whole pass to run on one *sql.Conn passed to the migration functions — that cannot be done without touching every migration. ACCEPTED, logged here.
- My own checks on the final tip before handing it to a reviewer:
  - E2E re-run against a binary built from this tip: **E2E_PASS**.
  - Rollback anchor: prior_head.txt now holds c6076387c, and `git cat-file -e c6076387c:internal/storage/sqlite/claim.go` FAILS — the anchor genuinely predates the item, so the plan's rollback would remove this work rather than reinstall it. That was the point of finding 7.
- Review of 1.9 dispatched with the deploy stakes stated plainly and every item tied to its reproduction: restore 022 and prove the new test fails; build a store with a column named `order` and confirm it now migrates; drop `pinned` and confirm the store heals instead of refusing to open; confirm no path still reaches the connection pool; and test — not reason about — a store shaped like the 17 live ones.
- Next: disposition → evidence packet → Final Review → three_test_decision.json → gate → Wrap-Up → Deploy.

## 2026-08-07 — 1.9 reviewed: 3 findings, all RESIDUAL; per-bead loop closed
- review_bd-ok4pr.1.9_v1.md: FIX=3 (1 med, 2 low) → all three dispositioned RESIDUAL, none fixed. Every blocking item from the previous round is confirmed closed; nothing here is a regression or a broken contract.
  - F1 (med) RESIDUAL, with evidence I gathered myself: the rebuild replays surviving indexes but not triggers or user views, so the ONE backfill pass on a ledgerless store would drop them. Two facts make this a documented residual rather than another cycle. First, it is strictly better than today: without this item, 022 runs on EVERY `bd` invocation and destroys those objects every time — the fix reduces that to at most one pass, ever. Second, the population is empty on this fleet: I queried all 11 live project stores for `sqlite_master` rows of type trigger or view with tbl_name='issues' and every one returned 0, and bd's own two views (ready_issues, blocked_issues) are already replayed explicitly by 022 at :331 and :363. The only trigger the reviewer could destroy was one it created itself for the probe.
  - F2 (low) RESIDUAL: `applied_at` has one-second granularity, so a backfill stamps all 27 ledger rows identically and `bd migrate --inspect` lists them alphabetically under a heading that reads as application order. Cosmetic ordering in a diagnostic command.
  - F3 (low) RESIDUAL: two hand-maintained readers of the ledger table with duplicated queries. Real duplication, no behavioral consequence.
- All three go in the work report's residual-risk table with this reasoning. Per-bead review loop is CLOSED: every bead has a CLEAN ledger row against its post-merge tagged SHA, 20 rows total.
- Next: evidence packet → Final Review → three_test_decision.json → gate → Wrap-Up → Deploy.

## 2026-08-07 — Evidence packet green; Final Review dispatched
- Full suite on the final tip: exit 0 (final_suite.log).
- The packet failed three times before passing, all on ARTIFACT NAMING, not on the work. Recording the friction here because the user asked for infrastructure trouble to land in the work report:
  1. `reviews/` is a CLOSED filename namespace: only `ledger.jsonl` plus the dispatcher's own `{bead}.{run}.*` receipts. My review prompts told reviewers to write `reviews/review_{bead}_v{N}.md`, which the packet rejects by name. Moved all 20 reports to `review_reports/` (still committed, still readable) and wrote the `{bead}.{run}.review.md` envelope the packet actually wants, pairing each run to its report by chronological order — same content, correct name.
  2. A CLEAN ledger row with a non-empty `bugs[]` is read as unresolved work. I had been putting the fix-bead IDs there for traceability; those beads are all closed and merged, so the rows now carry `bugs: []` and the traceability lives in this log and the work report.
  3. `build_proof` compares each build report's `commits` against the bead's tagged commit in the ITEM tree — but every run that was rebased at merge-back had its SHA rewritten, so 13 of 22 reports pointed at commits that no longer exist. Remapped that one field to the surviving SHA, pairing tagged commits to reports positionally per bead (scratchpad/remap_shas.py). No claim changed: acceptance entries, exit codes and step logs are untouched, and each new SHA is the same commit after rebase. This is a structural conflict between the packet's check and the manual playbook's own rebase-before-merge flow — follow-up for the work report, not a defect in this item.
- `build-final-evidence.py` now EXITS 0.
- Final Review dispatched (final_review_v1.md): plan_v4 as the contract, the RCA and the evidence packet as extra inputs, with the migration-signature/ledger seam called out and the E2E's stale-binary trap spelled out so it does not produce a false failure. Deploy's absence is explicitly not a finding — it is the manager's post-merge step.
- Next: disposition findings by the three tests → three_test_decision.json → gate with --phase gate → Wrap-Up → Deploy.

## 2026-08-07 — Final Review dispositioned; gate PASS; merged to main
- final_review_v1.md: FINDINGS n=2. E2E re-run by the reviewer prints E2E_PASS; full suite green; zero FAIL lines. Checks 1 (User Intent) and 4 (integration seams) found nothing to raise — the reviewer traced every explicit column list, both INSERT sites, and the total migrations.DB signature change.
  - F1 (corruption, high) → three tests: contract INTACT, blast radius CONTAINED, trajectory CLEAR → disposition FIXED. The defect was not in shipped code: plan_v4's rollback text claimed old binaries "ignore the extra column". The reviewer's mixed-binary probe disproves it — an old binary re-runs migration 022, blanks pinned/is_template and every lease, and the new binary's self-repair then re-adds them as NULL/0, so the loss leaves NO TRACE. Shipping forward destroys nothing; only following the wrong rollback procedure would. I corrected the procedure in CONTEXT.md (commit 48bec3eb2): a downgrade destroys those columns, and a rollback must restore the store from .beads/issues.jsonl or git after reinstalling the old binary.
  - F2 (neither, med) → RESIDUAL: the merge driver drops unmodeled fields on rewritten rows. Pre-existing, scoped out by plan_v4's risk row, reduced by this item. Leads the follow-up list.
- three_test_decision.json written, outcome SHIP. `build-final-evidence.py --phase gate` EXITS 0.
- Wrap-Up: work_report.md written from work-report-facts.sh output; report_summary.json rendered to http://localhost:8095/beads/w3_atomic-claim/work_report.html; CODEMAP.md gained the migrations/db.go row, the ledger note and a warning on the "add a migration" workflow row; context/WORK_INDEX.md gained the w3 and RCA rows (row-cap check clean after trimming the RCA row); telemetry.json mined.
- Two more pieces of tool friction hit during wrap-up, both recorded in the work report: work-report-facts.sh resolves beads by a `w3` LABEL that nothing in the bead flow applies (I labelled all 26), and it requires director-protocol decision records (e2e_confirm / final_audit / queue_drained) that the manual playbook never creates (I wrote them, each stating plainly that the manager performed the check directly and citing the real evidence).
- Merged to main with --keep: main is now 00d653c0f. wt-merge printed one warning — `bd sync` after merge failed with a PRE-EXISTING prefix mismatch in the beads store (bd-eph- 1 issue, bd-wisp- 87 issues vs the bd- prefix). Unrelated to this item, does not affect the merge; noted as follow-up.

## 2026-08-07 — Deploy Step 1: record pre-deploy state
- Command: `git rev-parse HEAD`; `cat work/w3_atomic-claim/artifacts/prior_head.txt`; `~/.local/bin/bd version`; `bd daemons list --json > artifacts/daemons_before.json`
- Gate: rollback anchor must predate the item; daemon inventory recorded before any stop
- Actual: main HEAD 00d653c0f. Anchor c6076387c (verified earlier: it does not contain internal/storage/sqlite/claim.go). Installed binary before deploy: `bd version 0.34.0 (f31496d65)`. Three live daemons — /home/ben/projects/tools/beads (pid 684732), /home/ben/projects/shared-docs (pid 875252), /home/ben/projects/teaching (pid 1978167), all alive.
- Verdict: PASS
- Artifacts: work/w3_atomic-claim/artifacts/{prior_head.txt,daemons_before.json}

## 2026-08-07 — Deploy Step 2: install the binary
- Command: `GOBIN=$HOME/.local/bin go install ./cmd/bd/`
- Gate: installed binary reports this worktree's HEAD (a bare `go install` would write ~/go/bin/bd and leave the fleet on the old binary — the defect bead QA caught)
- Actual: INSTALL_OK; `~/.local/bin/bd version` → `bd version 0.34.0 (dev: main@00d653c0f475)`, matching main HEAD exactly.
- Verdict: PASS
- Artifacts: none beyond the binary

## 2026-08-07 — Deploy Step 3: restart the daemons
- Command: `bd daemon --stop-all`; then `bd daemons health --json > artifacts/daemons_after.json`
- Gate: every recorded daemon stops; survivors healthy; daemons autostart on next invocation
- Actual: "Found 3 running daemon(s), stopping... ✓ Stopped 3 daemon(s)". Health after: total 1, healthy 1, stale 0, mismatched 0, unresponsive 0 — the one entry is /home/ben/projects/tools/beads, already autostarted under a fresh pid (576470) and healthy. The other two workspaces will autostart on their next `bd` command, as designed. This step is required, not cosmetic: a daemon still running the OLD binary would keep re-running the column-wiping migrations against its store.
- Verdict: PASS
- Artifacts: work/w3_atomic-claim/artifacts/daemons_after.json

## 2026-08-07 — Deploy Step 4: live verify against the running binary
- Command: `bash scratchpad/live_verify.sh` (identity, claim cycle on a scratch store, second-process lease check, rival denial, release, fleet read)
- Gate: plan_v4's E2E assertions must hold against the INSTALLED binary, not the tree
- Actual, all green:
  - IDENTITY_OK — `bd version --json` reports commit 00d653c0f475..., equal to main HEAD.
  - `bd claim live-j30 --assignee deploy-verify --lease 600s` → exit 0, "✓ Claimed issue".
  - THE FIXED DEFECT, verified live: after a second `bd` process ran against the store, `claim_expires_at` still reads 2026-08-07T21:36:22 — LEASE_SURVIVES_OK. Before this item it would have read NULL.
  - A rival claim on the held issue → exit 3, as contracted.
  - `bd update --status open` → exit 0; the issue reads `open` with `claim_expires_at` NULL, so the lease clears on leaving in_progress.
  - `bd --no-daemon info --json` against the fleet store → exit 0 (read-only).
- Verdict: PASS
- Artifacts: work/w3_atomic-claim/artifacts/live_verify.txt
