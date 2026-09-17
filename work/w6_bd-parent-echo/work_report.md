# Work Report: w6_bd-parent-echo

## Summary
`bd create --parent` can no longer hand out an ID that already exists, and no create anywhere in bd can report success without writing a row. Shipped from plan_v6.md.

## Dispatch Ledger

| Backend | Dispatches | Where |
|---------|-----------|-------|
| OpenCode (Muse Spark 1.3, `opencode-go/muse-spark-1.3-contributor`, xhigh) | 2 | builder ×2 |
| Claude (Opus, medium) | 1 | final review ×1 |

## What changed

Allocation, insertion and the parent edge became one storage operation inside one `BEGIN IMMEDIATE` transaction, `CreateChildIssue`. The `child_counters` value was demoted from the authority on child numbers to a monotonic floor, and the issues table became the authority, read by a LIKE scan that counts closed issues and tombstones. `GetNextChildID` left the `Storage` interface entirely, so allocation without insertion is no longer reachable. `INSERT OR IGNORE` on `issues` became a plain `INSERT` in both insert helpers, and a collision now returns `issue <id> already exists` wrapping the driver's text.

The floor is maintained by every path that writes an issue row — `insertIssue`, `insertIssues`, and the multi-repo hydration pass — because a floor only one path maintains protects only the numbers the scan would have found anyway. `RenameCounterPrefix`, a no-op stub whose comment claimed hash IDs do not use counters, now rewrites the floors a prefix rename would otherwise strand.

## Acceptance

All 14 ship-gate rows pass (13 requirements, ids 1-13 with 3a): 14 Y, 0 N, 0 PENDING. The gate was executed at the final tree by `ship-gate.py run`, not read from the build report. No PENDING rows, so nothing is waiting on the user.

The two commands from the original incident now behave as intended: a `--parent` create against a counter lagging ten numbers behind its children allocates above the highest existing child (gate 13, `repaired=yes`), and a duplicate explicit ID exits 1 having written nothing and left the old issue's title and created-event count untouched (gates 3, 3a, 4). The `--id` plus `--parent` workaround the farmplanner agent needed is no longer necessary: the pair is accepted when the ID names a child of that parent (gate 5) and refused before any write when it does not (gate 6).

## Residual Risk

| Item | Kind | What is left open, and why shipping is still right |
|------|------|----------------------------------------------------|
| F1 | residual finding | Refuted rather than deferred: the claimed divergence between the SQLite and memory backends over a non-numeric explicit child ID does not exist, because `IsHierarchicalID` is itself a numeric-suffix policy and both backends reject such an ID with the same message. Nothing is open. |
| F2 | residual finding | Floor seeding is broader than the plan specified — per row in `insertIssue`, and a full issue-ID scan per batch. Behavior is correct because floors only ever rise, and the measured cost of a 2000-fold table growth is about 9ms per create against 58ms of process startup. The proposed narrowing would reopen the orphan-first ordering hole that review round R4 closed. |

No gate residues, no gate revisions.

## Follow-Up Required

One, found by the final reviewer and out of this plan's scope. When a JSONL import renames an issue to a target that already exists **with different content**, the old row has already been deleted by the time the now-strict `CreateIssue` returns the collision, so the import aborts having lost it. The loss predates this change; strict inserts make it loud instead of silent, which is why it is visible now. Root cause: `internal/importer/importer.go` renames by delete-then-create across two transactions, and the recovery branch only handles the matching-content case. Suggested approach: make the rename one transaction, or extend the recovery branch to restore the old row when the content hashes differ.

The farmplanner database still carries four stray dependency edges and two false `created` events on `farmplanner-r3yq.8.32` and `.33` from the original incident. That repair was handed to that item's manager on 2026-09-09; both issues are closed, so nothing is blocked.

## Notes

The fleet binary at `~/.local/libexec/bd-real` was rebuilt at the shipped commit with the ldflags the plan specifies — a plain `go build -o` produces no `commit` key in `bd version --json` at all, which is what ship gate 10 reads.

Two process notes worth carrying forward. First, the review round that caught the most important defect was the one added last: Astra's R4 found that multi-repo hydration writes issue rows through neither insert helper, which three Opus rounds had missed and which would have left a whole class of child numbers reissuable. Second, this item's builder correctly refused an accepted finding and proved the manager wrong with code citations; the finding had been accepted because the manager verified the two call sites without opening the shared function they both depend on.
