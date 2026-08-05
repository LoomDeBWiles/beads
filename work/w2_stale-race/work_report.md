# Work Report: w2_stale-race

## Summary

Beads no longer reports "Database out of sync with JSONL" on a healthy repository. Shipped from `plan_v5.md`: freshness is now decided by comparing file content against what the database recorded, not by comparing a file's modification time against a timestamp, and the export protocol records the new content's hash before it reveals the file rather than after.

## Dispatch Ledger

| Backend | Dispatches | Where |
|---------|-----------|-------|
| Codex | 1 | builder r1 — died on a usage limit before doing any work; Codex is quota-blocked until 2026-08-11 |
| Claude | 8 | builder ×5 (r2 implementation, r3-r6 review fixes), final review ×3 |

Codex was the roster's default for both lanes and was unavailable for the entire item. Every dispatch after r1 used the roster's sanctioned Claude fallback.

## Acceptance

Five agent-verifiable criteria from the plan's Proof of Behavior table, all passing (`builds/build.md`, logs `builds/step1.log`-`step5.log`):

| # | Check | Result |
|---|-------|--------|
| 1 | Publisher state machine at every failpoint | PASS |
| 2 | Mid-export mutation survives the conditional dirty clear | PASS |
| 3 | Regression pins the original failure shape, restored-old-file, and import coherence | PASS |
| 4 | The daemon's once-per-second rewrite loop is dead at unit level | PASS |
| 5 | No new suite failures vs. the recorded baseline | PASS — `comm -13` empty across all four post-change runs |

The two failures present in both the baseline and every later run are pre-existing and environmental: a test makes a git config read-only and expects the next write to fail, which does not happen for a user who can still write it.

Four criteria remain PENDING because they require the deployed binary. They are the plan's Phase 3 and are executed by the conductor, not the builder:

| # | Check | Command |
|---|-------|---------|
| 6 | Deployed binary identity | `~/.local/bin/bd version` — must report the new short HEAD, not `cd33f0f3` |
| 7 | The original failure is gone live | `touch ~/projects/fleet/.beads/issues.jsonl && ~/.local/bin/bd --no-daemon --db ~/projects/fleet/.beads/beads.db --no-auto-import info --json >/dev/null; echo $?` — expect `0` |
| 8 | Daemon quiet on fleet | after one real bead mutation, count `JSONL file created` lines in fleet's daemon log over 60 s — expect ≤1 |
| 9 | Genuine divergence still caught | the E2E script's divergence step — expect a nonzero exit and an out-of-sync message |

`work/w2_stale-race/e2e_scratch.sh` is the plan's literal E2E script, copied byte-for-byte and executable. It runs against the built binary before installation and must print `E2E_PASS`.

## Deviations

**One import path the plan never listed, found twice.** The plan inventoried the six places that write the JSONL and the places that record an import, and routed them all through the new publisher. Review round 1 found that `cmd/bd/autoflush.go` has its own auto-import — the one that runs before nearly every CLI command — deciding freshness with a hand-rolled hash comparison that knew nothing about the new pending-hash key, and writing the recorded hash outside the publish lock. Fixing that surfaced the same defect a second time in `internal/autoimport`, so the fix was taken to the root: every freshness decision in the tree now goes through the one authority, and the closure was proven by a grep with a per-hit verdict, independently re-derived by the reviewer.

**A regression I introduced by over-specifying a fix.** When dispatching the second of those fixes I told the builder to preserve a call that re-records the content hash on the "nothing changed" path. Its stated purpose was to refresh an import timestamp so a touched file stops looking new — a rationale this work item abolishes. Preserved, it became a defect: the hash being recorded comes from the caller's read of the file while the freshness verdict comes from the protocol's own independent re-read, so a file replaced between them makes the database commit a hash describing content that exists nowhere and destroy the record describing what is actually on disk. Review round 2 caught it; the call was removed. The import timestamp has no reader anywhere in non-test code, verified twice independently.

**Eleven implementation deviations in the main commit**, all recorded in `builds/build.md` with reasoning. The substantive ones: sorting moved into the publisher so that "same content produces the same hash" is a property of the protocol rather than a convention every caller must remember; dirty-marker clearing removed from exports that write to a non-canonical file, since clearing them would discard the record of work the real JSONL has not yet received; and a missing `RecordImport` added to a multi-repo import path the plan's change list did not name.

## Follow-Up Required

- **`internal/lockfile`'s pre-existing blocking API remains** alongside the new context-aware one, kept for its current callers. Not a defect; a future item could migrate them and delete the old entry point.
- **Two dead functions found during review**: `isJSONLNewer` and `isJSONLNewerWithStore` (`cmd/bd/integrity.go:29-70`) are the last mtime-comparison helpers, and their only callers are each other. Left in place because deleting them is outside this item's scope. Removing them would retire the mtime predicate from the codebase entirely.
- **The fork is not gofmt-clean** (~190 files at baseline, unrelated to this work). Only `cmd/bd/export.go` was formatted here, because it was rewritten anyway.

## Notes

- The plan's Risks table disclosed that its final round of fixes never got a Codex review, because the planning cycle hit its four-round ceiling. Three Claude review rounds during execution covered that ground: they accepted four findings and rejected none, and the last round's only finding was about test strength rather than shipped behavior.
- Every regression test added in the fix rounds was proven to fail on the pre-fix code before being accepted, by restoring the old code in place rather than checking it out.
- Upgrading to upstream beads v1.x, which replaced this entire JSONL/SQLite mirroring architecture with Dolt and deletes this bug class outright, remains the recorded future candidate. It is a platform migration, decoupled by user decision at plan lock-in.
