Review plan_v4.md for beads work item w6_bd-parent-echo — a fourth-round, fresh-eyes review by the agent that did the original root-cause investigation. Identify anything that would cause rework, leave the reported bug reachable through another path, break an existing caller, or make the plan's proof worthless.

You wrote `work/w6_bd-parent-echo/astra_report_v1.md`, the root-cause investigation this plan is built on. You already know this codebase and this bug. Three review rounds have run since, all on a different model, producing 24 findings that were all accepted. Your job is what those rounds could not do: judge the plan against the failure you actually reproduced, and say whether it closes it.

<instructions>
1. Read plan_v4.md, then read every file it proposes to change and verify its claims at the cited lines. The plan's citations are claims, not facts.
2. Read the three prior reviews and the manager log's dispositions. Do not re-raise a finding whose fix is present and correct. Say so briefly only if a fix is absent or wrong.
3. Re-run any measurement in the plan's Validated Assumptions table that you doubt. Report any that does not reproduce.
4. YAGNI, both directions. Every guard, statement, test and gate row in the plan: if it serves no named failure, the finding is to delete it. For your own findings, propose the smallest change that closes the defect, and never add machinery unless a failure you can cite demands it.
5. Only report issues that need fixing.
6. Output the canonical verdict line: CLEAN or FIX.
</instructions>

<files>
The worktree root is /home/ben/worktrees/beads/w6_bd-parent-echo. All paths below are relative to it.

Under review:
- work/w6_bd-parent-echo/plan_v4.md

Your own prior work and the review history:
- work/w6_bd-parent-echo/astra_report_v1.md — your root-cause report, including the scratch reproduction
- work/w6_bd-parent-echo/manager_log.md — every decision, every disposition, and the probe transcript behind the Validated Assumptions table
- work/w6_bd-parent-echo/review_codex_v1.md, review_codex_v2.md, review_codex_v3.md — the three prior rounds
- work/w6_bd-parent-echo/plan_v4.diff is not generated; diff plan_v3.md against plan_v4.md yourself if you want the last delta

Source the plan changes or depends on:
- internal/storage/sqlite/hash_ids.go, issues.go, queries.go, transaction.go, batch_ops.go, resurrection.go, ids.go, dependencies.go, util.go, store.go
- internal/storage/sqlite/migrations/014_child_counters_table.go
- internal/storage/storage.go, internal/storage/memory/memory.go
- cmd/bd/create.go, cmd/bd/nodb.go, cmd/bd/delete.go, cmd/bd/version.go
- internal/rpc/server_issues_epics.go
- internal/importer/importer.go, internal/molecules/molecules.go
- Makefile, CODEMAP.md, CONTEXT.md
</files>

<checklist>
THE BUG YOU FOUND
- Take the exact command sequence from your report that produced `bug=farmplanner-r3yq.8.32` and `task=farmplanner-r3yq.8.33`. Walk it through the plan's design, step by step. Does it now create two new issues, and is every one of the four consequences you documented (false ID echoed, no row written, false created event, edges landing on the old issue) closed?
- Your report identified a second door: JSONL import producing a database with no counter rows. Walk that through too.
- Is there a third door neither of us has named: any other code path that can produce an issue ID without going through the plan's insert, or that can reach `insertIssue` with a hierarchical ID whose parent floor then matters?

THE DESIGN
- The counter becomes a floor written only by `raiseChildFloor` at the insert, and allocation becomes a pure read. Is that coherent for every ordering, including two transactions racing, a parent created in the same transaction as its child, and a three-level ID whose middle ancestor is a tombstone?
- Is the guarded monotonic statement correct for every input `insertIssue` and `insertIssues` will hand it? Consider a batch mixing parents, a suffix with leading zeros, a suffix too wide for int64, and a parent ID that is itself a prefix of another parent ID.
- Does routing child creation through `RunInTransaction` plus `sqliteTxStorage` lose any behavior the old two-call path had? Name it if so.
- Is anything in the plan machinery without a named failure behind it?

WHAT THE PLAN CLAIMS ABOUT OTHER CODE
- `EnsureIDs` covering everything `checkForExistingIDs` did, so the latter can be deleted. Read both and every caller of `CreateIssuesWithFullOptions`. Name any input that reaches the insert which `EnsureIDs` does not reject.
- The importer's rename branch at `:472` being dead today and correct once live.
- Orphan-carrying imports surviving the floor write because of the `WHERE EXISTS` guard.
- `bd delete --hard` keeping a tombstone row, which is why ship gate 11 deletes the row directly.
- Memory storage being a production path through `bd --no-db`.

THE PROOF
- Ship gate rows 1 to 13: would each fail against the installed `0.34.0` binary and pass against this design? Recompute the IDs each block expects, in order, accounting for the floor the new binary leaves behind.
- Does any block depend on state an earlier block leaves, and is that dependency still correct after three revisions?
- Are the Phase 2 tests the right ones, and is any behavior the plan newly guarantees left untested?
- Is the deploy step correct, and does gate 10 actually prove the fleet binary is the merged commit?

SCOPE AND RISK
- Does the plan deliver what the user asked for: the cause found and fixed, designed as if collision-freedom had been foundational?
- Is any risk missing that would cause data loss or a wrong ID in a database on this machine?
</checklist>

<constraints>
- Read every file you cite. Verify before asserting.
- Settled by the user and not open for re-litigation: the counter stays as a monotonic floor; `CreateChildIssue` replaces `GetNextChildID` in the interface; inserts become strict; `--id` with `--parent` is legal when they agree; the farmplanner database repair belongs to another work item. Challenge these only with evidence of data loss or corruption.
- Do not modify the plan or any source file. Write only your review file.
- Do not run `bd` against /home/ben/projects/farmplanner/.beads/ in write mode; read it only with `sqlite3 "file:...?mode=ro&immutable=1"`.
- Scratch work goes under work/w6_bd-parent-echo/scratch/ only. Do not delete anything.
</constraints>

Write review to: /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/review_astra_v4.md

<output-format>
Output exactly this — the verdict line plus finding rows, no other prose:

VERDICT: CLEAN|FIX findings=N
| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|

- CLEAN requires findings=0; FIX requires one or more finding rows.
- sev: high = design or behavior must change; med = correctness risk in a specific case; low = wording, docs, cosmetic.
- N states the true total; the table lists every finding, no cap.
- Each defect sentence stands alone: name the mechanism and the consequence, grounded in file:line and, when needed, a number or a short quote.
- Each fix sentence is a concrete change to the plan.
</output-format>
