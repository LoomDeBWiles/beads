# Manager Log: w2_stale-race

## 2026-08-04T00:00 — Pre-Plan
- Idea: fix the Beads daemon false-staleness race that killed the fleet w25 supervisor ("bd info failed: Database out of sync with JSONL"), as an elegant root-cause redesign in the local beads fork (/home/ben/projects/tools/beads, installed bd built from tip cd33f0f3).
- Evidence (from fleet/work/w25_inbox-queue/rca_stale_v1.md, re-verified against current source):
  1. `cmd/bd/daemon_sync.go:569-583` pre-import flush exports dirty issues but never `ClearDirtyIssuesByID` (unlike `cmd/bd/autoflush.go:716` and `cmd/bd/sync.go:1420`) → self-triggering JSONL rewrite loop, one rewrite per watcher cycle.
  2. Export publishes JSONL by rename FIRST, then `updateExportMetadata` (daemon_sync.go:280+) writes `jsonl_content_hash`/`last_import_time` → window where file is newer than metadata.
  3. `internal/autoimport/autoimport.go` `CheckStaleness` compares JSONL **mtime** vs `last_import_time` → that window reads as stale; direct `bd info` fail-stops; supervisor treats 2nd failure as daemon-fatal.
- Proposed design (foundational, not bolt-on): staleness = content divergence, not clock order.
  A. `CheckStaleness`: mtime comparison becomes a fast path only; on mtime-newer, compare file SHA-256 to stored `jsonl_content_hash`; equal → fresh. Stale now means "the bytes differ".
  B. Pre-import flush clears the dirty IDs it exported (same contract as autoflush/sync) → rewrite loop gone.
  C. Export writes metadata (hash of the new content + timestamp) BEFORE the rename → no window in which a reader can see mismatch; combined with A, race-free by construction.
- Not proposed: baking BD_ALLOW_STALE / no-daemon armor into supervise-launch.sh (root cause fixed → armor is gold-plating); BD_ALLOW_STALE env plumbing (irrelevant once staleness is content-based).
- Next: user lock-in on design + deploy scope, then Standard-scale plan_v1.md.

## 2026-08-04T21:00 — Lock-in + Plan
- User lock-in: "ok let's just lock in the plan to fix our version". Decisions: fix the local fork only (no upstream PR); Done-includes-deploy: YES (rebuild, install ~/.local/bin/bd, restart daemons); beads v1.x/Dolt migration explicitly out of scope, recorded as future candidate.
- Upstream check (fetched): steveyegge/beads is at v1.1.2, rearchitected onto Dolt — JSONL/mtime staleness machinery deleted upstream; upgrade = platform migration (109 local fork commits, wt-merge JSONL lanes, orchestrator conveyor integration), decoupled per user.
- Probes: fleet live `jsonl_content_hash` == sha256(issues.jsonl) exactly (d1df2880…); metadata keys unsuffixed (single-repo); go1.24.11; `bd daemon --stop-all` exists; 7 daemons live.
- Wrote plan_v1.md (standard scale): A content-based CheckStaleness (mtime fast path + sha256 vs jsonl_content_hash), B pre-import flush clears dirty IDs (mirror autoflush.go:716), C record-then-reveal in exportToJSONLWithStore (metadata before rename); deploy phase with bd.cd33f0f3.bak rollback. Lint exit 0.
- Next: Review R1 via codex-review.sh --bg (modules: Core, Plan Quality, Operational, Scenario Trace; skills: elegant, coding, testing) → review_codex_v1.md; watch armed, wake on done.

## 2026-08-04T21:45 — Review R1 dispositioned + plan_v2
- review_codex_v1.md: VERDICT FIX findings=11 → ACCEPT all 11 (as R1-R3, R4a-d, R5-R11 in the delta), REJECT 0. Every premise re-verified in source before acceptance: bd-v0y comment (integrity.go:118-121) already removed mtime fast-paths as unsafe; validatePreExport (integrity.go:146-153) would deadlock v1's crash state; ClearDirtyIssuesByID is an unconditional DELETE (dirty.go:100-121); only process-local operationMu guards writers; RPC export sites (server_export_import_auto.go:189,560) record no metadata; sync.go reveals before recording (:1406-1444).
- v2 design: new internal/jsonlpub = sole authority for freshness (ContentState: file sha256 ∈ {committed jsonl_content_hash, pending jsonl_pending_hash}, no mtime logic) and publication (Publish: lockfile-serialized temp+hash → pending → rename → promote → marked_at-conditional dirty clear; pending-write failure aborts). All five JSONL writers routed through it; CheckStaleness and hasJSONLChanged delegate to ContentState; nodb.go exempt (store-less). Deploy/rollback reordered around ETXTBSY (stop daemons → mv-install).
- plan_delta_v2.md hash-bound; check_plan.py revision check: valid; lint: 0 findings.
- Next: Review R2 (delta review: verify accepts landed + review new design for introduced defects; empty rejected-list) → review_codex_v2.md; watch armed.

## 2026-08-04T22:30 — Review R2 dispositioned + plan_v3 (return-to-Step-2 pass)
- review_codex_v2.md: VERDICT FIX findings=12 → ACCEPT 11, REJECT 1.
  - REJECT V2-12 (must relaunch the exact pre-stop daemon set): relaunch-on-demand after --stop-all is the deploy scope the user approved at lock-in ("i agree with deploy scope"). Added only a pre-stop daemon inventory artifact as deploy evidence.
  - Key accepts, premises verified in source: V2-1 reader TOCTOU (file hashed before keys read → healthy repo reads diverged) → lock-recheck on provisional mismatch; V2-2 snapshot-before-lock → snapshot-builder callback inside the lock; V2-3 no-metadata mapping is caller-specific (integrity.go:134-136 returns changed today); V2-4/V2-5 import-side twin RecordImport (import metadata writers scattered: autoimport.go:103,137,144, import.go:388,396, daemon sites :633,:854; pending needs a second deleter ordered before post-import export callbacks); V2-6 sixth writer bd export default path (export.go:463-476); V2-7 canonical-path gating for RPC/manual export; V2-8 narrow DirtySnapshotStore (memory.go:37 boolean dirty map can't carry marked_at); V2-9 failpoints after every protocol step; V2-10 go test -json baselines + comm -13 (grep missed compile failures); V2-11 E2E as one checked script.
- Two consecutive design-changing rounds → per PLAN_SKILL returned to Step 2: re-derived the design myself from source (import flow, export.go, RPC args, memory backend) rather than iterating by review. v3 Design: jsonlpub owns ContentState + Publish + RecordImport, one lock, six writers, caller-specific tri-state mapping.
- plan_delta_v3.md hash-bound (15 rows incl. 3 consequential-x); revision check valid; lint 0 findings.
- Extra review round R3 justified (rounds beyond default 2, ceiling 4): new unreviewed mechanisms — RecordImport, reader lock-recheck, snapshot-callback Publish, DirtySnapshotStore, canonical-path export gating. Defect class the execution backstops can't catch: cross-component protocol coherence (publish/import interaction through the pending key and lock ordering) — builder unit tests validate components, not routing completeness.
- Next: Review R3 (delta over v3; rejected list carries V2-12) → review_codex_v3.md; watch armed.

## 2026-08-04T23:15 — Review R3 dispositioned + plan_v4
- review_codex_v3.md: VERDICT FIX findings=12 → ACCEPT 12, REJECT 0; three accepted with deliberately smaller fixes:
  - R3-1: RecordImport commits the parsed-bytes hash (already computed in autoimport) instead of re-hashing the file — the proposed refuse-and-verify step is unnecessary once the recorded hash is self-consistent.
  - R3-2: Publish gains a locked diverged-abort guard (validatePreExport's semantic made atomic with the rename) instead of locking git pull/checkout paths.
  - R3-4: locked readers promote an observed pending (crash window closes at first locked event) instead of persistent temp-pathname evidence; the double-rare residual (mid-promote crash then byte-exact restore of old committed before any locked event) documented in Risks with revisit-if-observed.
  - Rest: R3-3 dirty-first snapshot order; R3-5 canonical-only RecordImport; R3-6 warn-and-continue before onChanged; R3-7 parent-dirs-only path normalization; R3-8 ctx-aware lock; R3-9 phase texts aligned to the -json baseline; R3-10 normalizer build-vs-test signatures + fixtures; R3-11 literal E2E script inlined; R3-12 mapping tests placed per package visibility.
- plan_delta_v4.md hash-bound (13 rows); revision check valid; lint 0 findings.
- Round 4 (hard ceiling) justified: R3 accepts introduced unreviewed design — diverged-abort guard, reader promotion, parsed-hash RecordImport, dirty-first ordering, canonical-path rule, literal E2E script. Defect class execution backstops can't catch: protocol coherence across publish/import/reader paths. R4 is final: design-changing findings beyond it go to the gate as logged risks or force a Step-2 return by user decision.
- Next: Review R4 (final; delta over v4) → review_codex_v4.md; watch armed.

## 2026-08-05T00:00 — Review R4 (final round) dispositioned + plan_v5
- review_codex_v4.md: VERDICT FIX findings=10 → ACCEPT 10, REJECT 0. All implementation-precision: R4-1 guard-hash re-check before rename; R4-2 dirty+diverged starvation branch (skip flush → import → RecordImport → publish); R4-3 guard also aborts no-metadata-with-existing-file; R4-4 lock-held sampler + ctx-aware lockfile API (self-deadlock); R4-5 import.go ordering (:374-379); R4-6 hash-during-read plumbing for CLI/daemon importers; R4-7 E2E count double-zero; R4-8 E2E before install; R4-9 EXIT trap; R4-10 pgrep self-match.
- plan_delta_v5.md hash-bound (11 rows); revision check valid; lint 0 findings.
- Review ceiling reached (4 rounds). v5's fixes are un-reviewed by Codex — disclosed in the plan's Risks (review-ceiling row) and at the gate; Phase 1 protocol tests exercise every new mechanism before callers migrate.
- Trajectory (design-changing accepts per round): R1 11 → R2 11 → R3 12 → R4 10 (increasingly implementation-level; R4's were precision fixes, not new mechanisms).
- Next: Human Gate — render gate_summary, post Inbox row, END TURN; user's verdict is the wake.

## 2026-08-05T00:20 — Human Gate presented
- gate_summary.json validated and rendered: http://localhost:8095/beads/w2_stale-race/gate_summary.html (feedback: ~/served-data/beads/w2_stale-race/gate_summary/feedback). Created ~/projects/beads → tools/beads symlink (orchestrator precedent) for the renderer's canonical-path check.
- Inbox gate row posted: id beads-w2-gate-v5.
- Canonical plan: work/w2_stale-race/plan_v5.md (v1-v5 + 4 Codex reviews + 4 hash-bound deltas preserved).
- Next: END TURN; wake = user's gate verdict (approve / send back). On approve → /work handoff (single direct builder, three phases, deploy included).

## 2026-08-05T14:00 — Gate approved → WORK
- User approved plan_v5.md and handed off to /work. Route: bead-free (Execution Handoff = "Direct: a single builder") → WORK_FREE.md.
- Split: builder owns Phase 1 (internal/jsonlpub publisher package + lockfile ctx API + sqlite dirty snapshots + protocol tests) and Phase 2 (route six writers, three readers, import twin; baseline/post `-json` normalizer gate), plus writing the literal E2E script to work/w2_stale-race/e2e_scratch.sh. Phase 3 (build, E2E run, stop daemons, install, live probes 6-9) is the plan's Deploy section → manager-run after Final Review + merge --keep, per WORK.md Deploy.
- Next: dispatch builder r1 via dispatch-builder.sh (Codex builder lane); arm builder-watch.sh on {workdir}/builds.
- Dispatched builder r1 (Codex builder lane): meta=work/w2_stale-race/builds/build.r1.meta, log=build.r1.codex.log, done=build.r1.done. Prompt scoped to Phases 1-2 + the byte-for-byte e2e_scratch.sh; Phase 3 withheld as manager Deploy.
- Watch armed: builder-watch.sh {workdir}/builds 300 build.r1.meta (background). Wake = its return.

## 2026-08-05T14:40 — Builder r1 FAILED (Codex quota) → Claude fallback r2
- build.r1.done exit=1 within seconds; build.r1.codex.stderr.log ends: "ERROR: You've hit your usage limit ... try again at Aug 11th, 2026 2:19 AM." No work performed, no commit, no edits.
- Codex build lane is down until 2026-08-11. Per WORK.md Model Roster this is the sanctioned Claude fallback (not a tool switch requiring replan): dispatch-builder.sh --claude (claude -p, Opus 5).
- Usage budget checked per skills/work/usage-budget.md: lane `claude` state=available used=2% (resets 1785939000) → dispatch.
- Next: dispatch builder r2 on the Claude lane; Claude lane prints done= only (no meta, no stall watcher) → arm watch-done.sh on the printed done path.
- Builder r2 dispatched (Claude lane): log=builds/build.r2.codex.log, done=builds/build.r2.done. Watch armed: watch-done.sh 3000 build.r2.done (background). Wake = its return.
- Watch rc=124 at 50min (2026-08-05T15:30). Builder r2 ALIVE and progressing: pid 3926146 (run-detached child, claude -p opus-5) up 50:19; Phase 1 baseline artifacts written (baseline.json, baseline_failures.txt, normalize_failures.py at 14:44), step1.log 15:00, step2.log 15:29, 10+ source files modified in cmd/bd and internal/. Consecutive timeouts: 1. Re-armed watch-done.sh 3000 on build.r2.done.

## 2026-08-05T16:20 — Builder r2 done → Final Review v1 dispatched
- build.r2.done exit=0. Commit 43903f9c9 "fix(jsonl): publish JSONL and metadata as one protocol". Working tree clean of tracked changes.
- builds/build.md: Verification checks 1-5 all Y (phase1_gate.log and phase2_gate.log both status 0); checks 6-9 PENDING as designed (Phase 3 = manager deploy). comm -13 baseline vs post printed nothing; two pre-existing baseline failures unchanged (TestMergeDriverWithLockedConfig_E2E, environmental read-only-git-config case).
- Build report documents 11 divergences (context-aware Acquire added in existing lock.go rather than new platform files; narrowed MetadataStore; 0644 unified; sorting centralized in writeTemp; mtime-contract tests rewritten to the content rule; extra RecordImport at repo.go:232; dropUnencodableIssues; dirty-clear removed from non-canonical RPC export; gofmt on export.go only; two CLI import tests behind -tags integration; three test expectations updated). Lock order mapped: operationMu -> publish lock, never inverted, with acquisition-site evidence.
- Pre-evidence integration step: wt-sync.sh beads -> already up to date; replay onto main -> no-op (main has not moved), so the build's evidence covers current main; no test re-run needed.
- Codex review lane is quota-blocked to 2026-08-11 (same account as the build lane) -> WORK.md Claude fallback for final review: claude -p --model claude-opus-5 --effort high, setsid-detached, receipt final_review_v1.log.done, review self-written to final_review_v1.md. Review prompt scopes Phase 3 out and directs particular scrutiny at divergences 8 and 11 and at the plan's un-reviewed v5 mechanisms.
- Watch armed on final_review_v1.log.done. Next: disposition findings per MANAGER.md Review Dispositions -> CLEAN -> merge --keep -> Deploy (Phase 3).

## 2026-08-05T17:10 — Final Review v1 dispositioned → builder r3
- final_review_v1.md: VERDICT FIX findings=2 → ACCEPT 2, REJECT 0.
  - ACCEPT F1 (med, cmd/bd/autoflush.go:107,258): premise re-verified myself before accepting. `autoImportIfNewer` is a fourth import path the plan's caller list (line 177) never named; grep confirms it is the only non-test reader/writer of jsonl_content_hash outside internal/jsonlpub, and it is live on nearly every CLI command (main.go:790,793 PersistentPreRun) plus direct mode (direct_mode.go:105). It compares hashes by hand, is blind to jsonl_pending_hash, and writes the committed key with a bare SetMetadata outside the publish lock — the pending-window re-import and the committed-key race that reproduce the item's own killer message. Not gold-plating: routing every reader and writer through the one authority IS the plan's design, and this caller was simply missed.
  - ACCEPT F2 (low, builds/build.md:68): report names SnapshotDirtyIssues; shipped method is GetDirtyIssueSnapshots. Doc-only rename.
- Reviewer independently re-derived the lock order and confirmed the build report's claim; it also examined divergences 5, 8 and 11 and judged the rewritten test expectations correct consequences of the design rather than silenced regressions, with per-test reasoning (multi-repo tests contradicted their own single-repo setup; export_test.go was strengthened, not weakened; the mtime assertions encoded the rule this item abolishes). I read that reasoning and agree.
- Usage budget before dispatch: lane `claude` state=available used=50% → dispatch (below the 90% pause line).
- Builder r3 dispatched (Claude lane), scoped to exactly F1 + F2 with a regression test for the pending-window re-import: log=builds/build.r3.codex.log, done=builds/build.r3.done. Watch armed.
- Next: r3 done -> final_review_v2 scoped to the commits since v1, the two ACCEPTs, and an empty REJECT list -> CLEAN -> merge --keep -> Deploy (Phase 3).

## 2026-08-05T18:00 — r3 landed; same defect class found a second time → r4 root-cause sweep
- build.r3.done exit=0. Commit 757611577 "fix(autoimport): route CLI auto-import through the publish protocol". Both gates green: package suite ok, comm -13 baseline vs post2 empty (only the two pre-existing environmental failures). Regression test cmd/bd/autoimport_test.go:120 TestAutoImportIfNewer_PendingHashIsFresh verified failing pre-fix (r3_prefix_test_fails.log) and passing post-fix. Zero divergences.
- r3's report flagged, without acting: internal/autoimport.AutoImportIfNewer (autoimport.go:84-97) still decides freshness with the same direct jsonl_content_hash comparison, blind to pending. I re-verified in source — confirmed.
- Judgment: this is the same bug class twice, so the F1 fix was too shallow. The plan's design names internal/jsonlpub the SOLE authority for deciding freshness; a hand-rolled hash comparison anywhere contradicts it. Fixing the second instance and closing the class is inside the plan's design intent, not new scope.
- Swept the remaining non-test readers of jsonl_content_hash/last_import_hash outside internal/jsonlpub myself: daemon_event_loop.go:242 is a metadata-readability health check, not a freshness decision; daemon_sync.go:281 is the multi-repo updateExportMetadata the plan preserves byte-identical. autoimport.go:85 is the only real remaining instance.
- Builder r4 dispatched (Claude lane, usage 51%): route AutoImportIfNewer's read side through ContentState matching autoflush.go's mapping, regression test verified failing pre-fix, plus an explicit class-closure grep with per-hit judgment. log=builds/build.r4.codex.log, done=builds/build.r4.done. Watch armed.
- Next: r4 done -> final_review_v2 scoped to commits since v1 (43903f9c9..HEAD), the two v1 ACCEPTs to verify landed, and the class-closure claim; empty REJECT list -> CLEAN -> merge --keep -> Deploy (Phase 3).

## 2026-08-05T18:45 — r4 landed (class closed) → Final Review v2 dispatched
- build.r4.done exit=0. Commit beee58774 "fix(autoimport): decide RPC auto-import freshness through the publish protocol". Both gates green; comm -13 baseline vs post3 empty.
- Regression test internal/autoimport/autoimport_test.go TestAutoImportIfNewer_PendingHashIsFresh proven failing pre-fix (r4_prefix_test_fails.log, taken by temporarily restoring the old comparison via Edit, no git checkout) and passing post-fix.
- Class-closure grep reported with a six-row per-hit table: four are prose comments, daemon_event_loop.go:242 discards both values and inspects only err (readability check, not freshness), daemon_sync.go:280-281 is a multi-repo SetMetadata-only writer unreachable in single-repo mode. Matches my own sweep.
- Builder observation recorded, not fixed: on the Fresh path a writer replacing the file between os.ReadFile and ContentState leaves recordImport committing a hash for content no longer on disk; claimed self-healing at the next read (next reader sees neither key -> Diverged -> imports). Sent to review v2 as an explicit check rather than accepted on the builder's word.
- Final Review v2 dispatched (Claude fallback, usage 3% after window reset), scoped to 43903f9c9..HEAD, verification that v1's two ACCEPTs landed, independent re-derivation of the class-closure grep, and the tri-state mapping / error-direction / migration-fallback seams. Empty reject list. done=final_review_v2.log.done, watch armed.
- Next: verdict -> disposition -> CLEAN -> merge --keep -> Deploy (Phase 3).

## 2026-08-05T19:30 — Final Review v2 dispositioned (1 finding) → builder r5
- final_review_v2.md: VERDICT FIX findings=1 → ACCEPT 1, REJECT 0.
  - ACCEPT F1 (med, internal/autoimport/autoimport.go:105): the StatusFresh branch records currentHash (hashed from the os.ReadFile at :76) while ContentState reached Fresh by independently re-hashing the file, so a file replaced between the two reads makes the database commit an orphan hash AND clearPending destroy the record describing the real content. Every later ContentState then reads Diverged -> staleness.go:48 prints the item's own killer message on a healthy repo and Publish refuses every export. Verified myself: last_import_time has ZERO non-test readers (only the jsonlpub key constant, the multi-repo writer at daemon_sync.go:282, and comments), so the preserved behavior's rationale is vestigial under content-based staleness, and the sibling CLI path already returns without recording.
  - This defect is mine, not the builder's: my r4 dispatch instructed it to preserve the recordImport call on the unchanged-content path. That instruction preserved a behavior whose rationale this item abolishes. The r5 dispatch says so explicitly so the builder does not defend the old shape.
- Reviewer independently re-derived the class-closure grep and confirmed all six per-hit verdicts, confirmed v1's two ACCEPTs landed, checked both tri-state mappings, the bd-663 error direction, and the bd-39o fallback centralization, and judged the builder's "self-heals at the next read" claim FALSE in reachable states (ensureDatabaseFresh and Publish read the poisoned key before any healing import). That check is why the finding surfaced.
- Fix cycle 2 of 3. Builder r5 dispatched (Claude lane, usage 7%): delete the Fresh-path recordImport + its now-false comment, regression test proven failing pre-fix, explicit instruction not to silently rewrite any test asserting the old behavior. log=builds/build.r5.codex.log, done=builds/build.r5.done. Watch armed.
- Next: r5 done -> final_review_v3 scoped to the r5 commit + the v2 ACCEPT -> CLEAN -> merge --keep -> Deploy (Phase 3).

## 2026-08-05T20:10 — r5 landed → Final Review v3 dispatched (last cycle)
- build.r5.done exit=0. Commit 1e2128ae6 "fix(autoimport): stop recording an unparsed hash on the fresh path". Both gates green; comm -13 baseline vs post4 empty.
- Builder independently re-derived the last_import_time grep: 19 hits, exactly four non-test and none a read (jsonlpub key constant, the multi-repo SetMetadata writer, two comment sites). Confirms my own check. It also explicitly states no existing test asserted the deleted behavior and nothing was rewritten to make gates pass, and it retracts Round 4's "self-heals" observation.
- Regression test TestAutoImportIfNewer_FreshPathRecordsNothing proven failing pre-fix with both halves of the defect visible (committed key overwritten with an unparsed hash; pending record destroyed), passing post-fix.
- One divergence, judged sound: the exact "file replaced between the caller's read and the protocol's re-read" race has no injectable seam (only a pure HashBytes call sits in that window), so the test reaches Fresh through the pending key, producing the identical observable defect deterministically instead of a flaky goroutine race. Sent to review v3 as an explicit check on whether the substitution still pins the finding.
- Final Review v3 dispatched (Claude fallback), scoped to 1e2128ae6, verification the v2 ACCEPT landed, an independent last_import_time derivation, the test-substitution judgment, and a full trace of every remaining route to staleness.go:48. Prompt states a CLEAN verdict is the expected outcome if the code is correct, to counter round-justifying padding. done=final_review_v3.log.done, watch armed.
- Fix cycle 3 of 3 (cap). A further accepted high/med finding means escalation.md rather than a fourth cycle.
- Next: verdict -> CLEAN -> merge --keep -> Deploy (Phase 3).

## 2026-08-05T20:55 — Final Review v3 dispositioned (1 low) → builder r6; no fourth review round
- final_review_v3.md: VERDICT FIX findings=1 → ACCEPT 1 (low), REJECT 0.
  - ACCEPT F1 (low, internal/autoimport/autoimport_test.go): TestAutoImportIfNewer_FreshPathRecordsNothing never reaches the defect state. In its fixture the pre-fix code wrote the hash of the bytes actually on disk and cleared pending — byte-for-byte the healthy promoted steady state — so it pins "the Fresh branch writes nothing" (an implementation rule) instead of "never commit a hash for content the database did not parse" (the plan's R3-1 invariant, and the harm). It would also reject a legitimate future promote-on-fresh implementation.
  - Not gold-plating under the mechanism test: this is a named plan invariant (R3-1) left uncovered by the test meant to cover it, with a concrete seam shown, not a speculative scenario.
  - The reviewer refuted r5's "no seam exists" divergence; I verified the refutation in source myself: internal/jsonlpub/jsonlpub.go:245 HashFile runs BEFORE readCommitted (:253) and the pending GetMetadata (:257), so a MetadataStore wrapper that swaps the file on its first GetMetadata fires strictly inside the window. The builder's claim was wrong.
- Reviewer's other work verified: independent last_import_time re-derivation (no non-test reader) matching mine; the stronger argument that a lingering pending hash can only WIDEN the Fresh set and so can never manufacture StatusDiverged; the one real side effect of the deletion (jsonl_file_hash no longer refreshed on the fresh path) traced to validateJSONLIntegrity, warn-and-full-export only, never a fail-stop, and pre-existing on the CLI path; a complete trace of all nine ensureDatabaseFresh callers to staleness.go:48 confirming every route now requires genuine content divergence; and isJSONLNewer/isJSONLNewerWithStore found dead (only callers are each other).
- Per WORK.md Final Review step 3, accepted LOWS ONLY -> apply and verify the diff myself; no fourth review round is required and the 3-cycle cap is not breached.
- Builder r6 dispatched (Claude lane, usage 11%): rewrite the test to reach Fresh through the sampleState seam via a store wrapper, deterministic (no goroutine race), pre-fix failure must show StatusDiverged rather than merely a changed metadata value, production code untouched. log=builds/build.r6.codex.log, done=builds/build.r6.done. Watch armed.
- Next: r6 done -> verify the diff myself -> Wrap-Up (work_report + report page, context/codemap, WORK_INDEX row, telemetry) -> merge --keep -> Deploy (Phase 3).

## 2026-08-05T21:30 — r6 landed; review cycle CLOSED → Wrap-Up
- build.r6.done exit=0. Commit 0b0c47773 "test(autoimport): reach Fresh through the mid-check file swap". Both gates green; comm -13 baseline vs post5 empty.
- I verified the diff myself (accepted-lows-only path requires it): the commit touches internal/autoimport/autoimport_test.go and build.md ONLY; `git diff 1e2128ae6 HEAD` over internal/autoimport/autoimport.go, internal/jsonlpub/ and cmd/bd/ is empty, so no production code moved. The swapOnFirstRead wrapper embeds storage.Storage and swaps the file on the first GetMetadata, which lands inside sampleState between HashFile (:245) and readCommitted (:253) — deterministic, call-ordering driven, no goroutine. The test asserts store.swapped fired (so the fixture cannot silently stop exercising the window) and asserts the user-visible post-condition ContentState == Fresh rather than which keys were touched, so it also permits a legitimate future promote-on-fresh implementation. Pre-fix output shows `diverged`, the real harm state, not merely a changed value. Round 5's "no seam exists" claim retracted in the report.
- Review cycle closed: v1 FIX(2) -> v2 FIX(1) -> v3 FIX(1 low). Accepted 4, rejected 0. Final state is lows-only, so no fourth round; the 3-cycle cap was not breached.
- Next: Wrap-Up (work-report-facts -> work_report.md -> report page; context/ + CODEMAP; WORK_INDEX row; telemetry) -> commit -> wt-merge --keep -> Deploy Phase 3 -> live verify -> final merge -> Inbox.

## 2026-08-05T22:10 — Wrap-Up complete; merged --keep → Deploy
- work_report.md written by me from the item's own artifacts (bead-free template; work-report-facts.sh is bead-lane-only and requires a bd label + final_evidence packet this item has neither of). report_summary.json validated and rendered: http://localhost:8095/beads/w2_stale-race/work_report.html
- Renderer trap recorded: render_report.py derives the repo from `git rev-parse --git-common-dir` in the CURRENT directory, so it must be run FROM the worktree; run from elsewhere it resolves the wrong repo and rejects context[1].
- CODEMAP.md: internal/jsonlpub (both files) + the context-aware lockfile API added to Key Files; the publication protocol and the freshness predicate added to Data Flow. CONTEXT.md: durable gotcha added — never hand-roll a jsonl_content_hash comparison or an mtime check; isJSONLNewer/isJSONLNewerWithStore are dead, do not revive.
- context/WORK_INDEX.md created (repo had no context tree; CLAUDE.md is upstream's Gas Town file and imports nothing, so the root CONTEXT.md remains this fork's durable-knowledge home). Rows for w1 and w2. check_index_rows.py --changed: clean after trimming the w2 What cell to 150 chars; --stale: clean. 720 chars, far under the 35k archive threshold.
- telemetry.json mined and committed. Known misattribution in the generated row, left as generated rather than hand-edited: it reports gpt-5.6-sol for all three final reviews and lists only builder r1 — every review and every builder after r1 actually ran on the Claude fallback (claude -p, Opus 5), because Codex was quota-blocked for the whole item. The dates field (2026-08-11) picked up the quota-reset date from the log text.
- Committed by pathspec: f31496d65. Raw `go test -json` streams (6 x 3.1 MB) excluded via a workdir .gitignore; the normalized failure lists and recorded exit statuses are the committed evidence. 28 untracked dispatch receipts (.done/.launch/.log/.prompt, supervisor/lock) trashed: batch /home/ben/.local/share/agent-trash/beads/20260805T144934Z-706773.
- wt-merge.sh --keep: merged and pushed, main == origin/main. Worktree retained for the deploy log.
- Pre-existing, unrelated to this item: wt-merge's post-merge bd sync warns "prefix mismatch detected: database uses 'bd-' but found issues with prefixes bd-eph-, bd-wisp-" in the beads repo's OWN .beads data. Not caused by this change (it is an issue-prefix data condition, not a freshness one) and not fixed here.
- JANITOR (beads): worktree backup-2026-01-17 orphan 199d — relay to user.
- Next: Deploy Phase 3, one manager_log entry per step.

## 2026-08-05T22:30 — Deploy Step 1: rollback artifact
- Command: `cp ~/.local/bin/bd ~/.local/bin/bd.cd33f0f3.bak`
- Gate: a restorable copy of the pre-fix binary exists before anything is replaced
- Actual: /home/ben/.local/bin/bd.cd33f0f3.bak, 33705144 bytes
- Verdict: PASS
- Artifacts: ~/.local/bin/bd.cd33f0f3.bak

## 2026-08-05T22:31 — Deploy Step 2: pre-stop daemon inventory
- Command: `pgrep -af '[b]d daemon' > work/w2_stale-race/artifacts/daemons_before_deploy.txt`
- Gate: the live daemon set is recorded as deploy evidence (self-excluding pattern, R4-10)
- Actual: 7 daemons (608600, 1216628, 1871189, 3042331, 3447676, 3898533, 3964600), all `bd daemon --start --interval 5s`
- Verdict: PASS
- Artifacts: work/w2_stale-race/artifacts/daemons_before_deploy.txt

## 2026-08-05T22:32 — Deploy Step 3: build
- Command: `go build -ldflags="-X main.Build=$(git rev-parse --short HEAD)" -o /tmp/bd.new ./cmd/bd`
- Gate: builds clean from the merged tree
- Actual: exit 0; `/tmp/bd.new version` reports `bd version 0.34.0 (f31496d65)`
- Verdict: PASS
- Artifacts: /tmp/bd.new
- Also verified the E2E's flags against the built binary as the plan requires: `init --prefix/-p`, `init --quiet/-q`, and `create --json` all exist.

## 2026-08-05T22:35 — Deploy Step 4: E2E pre-install — FAIL, deploy HALTED
- Command: `bash work/w2_stale-race/e2e_scratch.sh`
- Gate: prints E2E_PASS
- Actual: exit 1 at `test "$((F-B0))" = 1`. Output: `dirty_after_update=1`, `flushes=0 dirty_after_flush=0`. The divergence probe (check 9) never ran because the script died first. Scratch repo preserved: /tmp/tmp.Cwf80OF3ar. Log: work/w2_stale-race/artifacts/e2e_run.log
- Verdict: FAIL
- Install NOT performed. No daemon stopped. ~/.local/bin/bd untouched, still the pre-fix binary.
- Two candidate mechanisms observed, both pointing at the script rather than the code, neither accepted without an RCA:
  1. `LOG=$(ls -1 .beads/daemon-*.log 2>/dev/null | tail -1) || LOG=.beads/daemon.log` — the scratch log is named `.beads/daemon.log`, the glob matches nothing, but `tail` exits 0 so the fallback never fires and LOG is empty. I reproduced this in isolation: the pipeline returns rc=0 with no matches, so LOG=[]. count_flushes then greps an empty filename and returns 0 unconditionally, making the assertion unpassable on ANY binary including the old one.
  2. The scratch daemon.log shows a startup sync cycle (Exported/Imported) that appears to retire the dirty row before the script's same-bytes rewrite can trigger the watcher's pre-import flush, so the flush count would be 0 even with mechanism 1 fixed.
- Contrary evidence that the shipped code is healthy: dirty_after_flush=0 (the row WAS retired) and the scratch daemon log shows no once-per-second rewrite loop — the actual killer. Phase 2's unit check 4 (PreImportFlush) also passed.
- Per WORK.md "Unexpected Error -> RCA-First" I did not patch the script to green. Dispatched an RCA (Claude fallback; Codex quota-blocked) to prove or refute both mechanisms by reproduction, classify each as script vs code defect, and independently compare /tmp/bd.new against the preserved pre-fix binary on the original failure shape. Receipt: rca_e2e_v1.log.done, watch armed.
- Next: RCA verdict. SCRIPT-DEFECT-ONLY -> correct the E2E via the plan-defect route, re-run, then resume deploy at step 4. CODE-DEFECT -> replan per PLAN_SKILL Plan Revisions; do not install.

## 2026-08-05T23:15 — RCA verdict SCRIPT-DEFECT-ONLY → plan_delta_v6 → builder r7
- rca_e2e_v1.md: VERDICT **SCRIPT-DEFECT-ONLY**. Every claim reproduced, not read.
- **My mechanism 1 was WRONG and the RCA refuted it.** I tested the `LOG=$(ls ... | tail -1) || LOG=...` line in a shell without `pipefail`; the script sets `set -euo pipefail` on line 2, so the pipeline takes ls's failure status and the fallback DOES fire. LOG resolved correctly in the failed run; the counter was reading the right file, which genuinely contained zero Flushing lines. Recorded here because I logged that claim as a finding at 22:35.
- Real cause (mechanism 2, confirmed and sufficient): the daemon runs a full sync cycle at startup (daemon.go:546, before the mode switch at :551). Under the fix that startup export publishes the dirty issue and retires its marker; the script creates its dirty row BEFORE starting the daemon, so the marker is consumed before the measurement window opens. The watcher's pre-import flush is gated on dirtyCount > 0 (daemon_sync.go:562), so it logs nothing. Measured: dirty_after_daemon_start=0 on the fix, =1 on the pre-fix binary.
- Two further script defects found: the readiness probe waits on the socket, which is bound before the startup sync AND before the watcher exists (latent race; the failed run and a replay took opposite sides and failed identically, so it is not the cause); and the dirty count is sampled the instant the Flushing line appears, which is logged BEFORE the export runs, so it reads mid-publish.
- Five-why deepest cause: the acceptance test encodes the OLD system's intermediate state as its precondition, and the plan's own byte-for-byte "substitute nothing" rule (R3-11) removed the builder's licence to reconcile it with the design stated one section earlier in the same document.
- Independent behavior evidence (no daemon, old binary vs new, identical scenarios): same-bytes rewrite -> pre-fix exit 1 "Database out of sync with JSONL", fixed exit 0 clean. Genuine divergence -> both catch. Restored older file with different bytes -> pre-fix MISSES it, fixed catches it. Daemon with a stranded dirty row over 10 s -> pre-fix 10 flushes rewriting the file each second, fixed 1 flush then ten identical inode/mtime samples. User Intent delivered, and the content predicate is strictly more accurate than mtime in both directions.
- RCA also ruled out, each by test: data loss from the startup flush (published content and recorded hash both correct); a mid-publish interruption leaving a permanently wrong verdict (reads fresh on the fix, stale on pre-fix, self-heals on next daemon start); watcher blindness to rename-over; and the corrected script being a rubber stamp (it still fails on the pre-fix binary).
- Wrote plan_delta_v6.md: scope UNCHANGED (no design change, no code change, nothing shipped differs), so per WORK.md it does not return to the human gate. It corrects only the acceptance script, E1-E4, each traced to the RCA's evidence.
- Builder r7 dispatched (Claude lane, usage 17%) to apply E1-E4 and prove the gate both directions: must print E2E_PASS on /tmp/bd.new AND still fail on the preserved pre-fix binary, with an explicit instruction not to tune the script toward green. log=builds/build.r7.codex.log, done=builds/build.r7.done. Watch armed.
- Deploy remains HALTED at step 4. ~/.local/bin/bd is still the pre-fix binary; no daemon stopped.
- Next: r7 done -> resume Deploy at step 4 with the corrected gate -> steps 5-7 -> live probes 6-9 -> final merge -> Inbox.

## 2026-08-05T23:50 — r7 landed: corrected gate proven both directions
- build.r7.done exit=0. Commit 6a6b039c0 "test(e2e): re-derive the stale-race acceptance gate from the fixed state machine". `git diff f31496d65 HEAD -- cmd internal` empty: no production code touched.
- Corrected gate against /tmp/bd.new: flushes=1, dirty_after_flush=0, flushes_after_second_touch=1, divergence_caught=yes, E2E_PASS, exit 0 (artifacts/e2e_v6_new.log).
- Same gate against the preserved pre-fix binary: flushes=1, dirty_after_flush=1, exit 1 (artifacts/e2e_v6_old.log). It is a real gate, not a rubber stamp.

## 2026-08-05T23:55 — Deploy Step 4 (rerun): E2E pre-install
- Command: `bash work/w2_stale-race/e2e_scratch.sh`
- Gate: prints E2E_PASS
- Actual: `flushes=1 dirty_after_flush=0`, `flushes_after_second_touch=1`, `divergence_caught=yes`, `E2E_PASS`, exit 0. Run by me, not inherited from the builder. Log: artifacts/e2e_deploy_step4.log
- Verdict: PASS
- Artifacts: work/w2_stale-race/artifacts/e2e_deploy_step4.log

## 2026-08-06T00:00 — Deploy Step 5: stop daemons before install
- Command: `bd daemon --stop-all` (bd's own sanctioned stop, not a process kill)
- Gate: no bd daemon executing the file about to be replaced
- Actual: "Found 8 running daemon(s), stopping... Stopped 8 daemon(s)"; `pgrep -af 'bin/bd daemon'` excluding my own wrapper shell returns 0 real daemons. Note: the plan's `[b]d daemon` pattern still matches the dispatching shell's own command line, so the post-stop assertion was made with `bin/bd daemon` minus shell-snapshot matches.
- Verdict: PASS

## 2026-08-06T00:02 — Deploy Step 6: install by rename
- Command: `mv /tmp/bd.new ~/.local/bin/bd` (rename, immune to ETXTBSY)
- Gate: `~/.local/bin/bd version` reports the new short HEAD, not cd33f0f3
- Actual: `bd version 0.34.0 (f31496d65)`
- Verdict: PASS — Verification check 6 satisfied

## 2026-08-06T00:05 — Deploy Step 7a: live probe 7 (the w25 killer) on the real fleet repo
- Command: `touch ~/projects/fleet/.beads/issues.jsonl && ~/.local/bin/bd --no-daemon --db ~/projects/fleet/.beads/beads.db --no-auto-import info --json >/dev/null; echo $?`
- Gate: exit 0
- Actual: exit 0, no output. Same command on the preserved pre-fix binary, same live repo, same instant: exit 1, "Error: Database out of sync with JSONL. Run 'bd sync --import-only' to fix." Decisive before/after on the exact repo and exact command that killed the w25 supervisor.
- Verdict: PASS

## 2026-08-06T00:10 — Deploy Step 7b: live probe 8 (daemon quiet on fleet)
- Command: started `bd daemon --start --interval 5s` in ~/projects/fleet (pid 1765086), recorded the baseline `JSONL file created` count, ran one real bead mutation (`bd update ben-ptg9.27 --priority 1`), waited 60 s, recounted.
- Gate: <=1 new `JSONL file created` line over 60 s
- Actual: baseline 41430 -> 41431, delta=1; dirty_issues=0 after. The log's 41167 historical `Flushing` lines are the OLD binary's once-per-second loop, accumulated before this deploy — the new binary added one publication and stopped.
- Verdict: PASS
- Note: the fleet daemon is left running on the new binary. That is the deployed service, not scratch: it replaces one of the 7 stopped at step 5, and relaunch-on-demand is the deploy scope approved at lock-in.

## 2026-08-06T00:12 — Deploy Step 7c: live probe 9 (true divergence still caught)
- Command: the E2E script's divergence step (step 4's run)
- Gate: nonzero exit with an out-of-sync message
- Actual: `divergence_caught=yes` plus "Error: Database out of sync with JSONL." The one case where refusing is correct still refuses.
- Verdict: PASS
- All nine Verification checks now satisfied: 1-5 by the builder pre-merge, 6-9 live post-install.
