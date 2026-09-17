Review plan_v2.md for beads work item w6_bd-parent-echo — identify gaps that would cause rework, leave the reported bug reachable through another path, or break an existing caller of the code it changes.

<instructions>
1. Read the plan, then read every referenced file to verify its claims. The plan cites specific file:line locations; check them.
2. For each checklist item, evaluate against the actual files, not against the plan's description of them.
3. YAGNI, both directions. For every guard, gate, validator, retry, and test the plan carries: if it does not serve a named failure or requested behavior, report a finding whose fix is to delete it. For your own findings: never propose adding machinery unless a failure you can cite demands it and nothing already in the design covers it — the best fix deletes or simplifies.
4. Only report issues that need fixing — skip observations, style, and low-probability concerns.
5. Add one finding row per issue (see output format).
6. Output the canonical verdict line: CLEAN or FIX.
</instructions>

<files>
Absolute paths. The worktree root is /home/ben/worktrees/beads/w6_bd-parent-echo.

The artifact under review:
- /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/plan_v2.md — the plan

Context and settled decisions:
- /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/manager_log.md — settled decisions, including what the user locked at pre-plan and what is out of scope
- /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/astra_report_v1.md — the root-cause investigation this plan is built on, with a reproduction against the installed binary
- /home/ben/worktrees/beads/w6_bd-parent-echo/CODEMAP.md — source file map
- /home/ben/worktrees/beads/w6_bd-parent-echo/CONTEXT.md — repo gotchas, including the w4 rule that one function decides ID prefix membership

Source the plan changes:
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/hash_ids.go — the allocator being replaced
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/issues.go — insertIssue / insertIssues / isUniqueConstraintError
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/queries.go — CreateIssue, its BEGIN IMMEDIATE transaction, DeleteIssue, RenameCounterPrefix
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/transaction.go — RunInTransaction, sqliteTxStorage.CreateIssue / CreateIssues / AddDependency
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/batch_ops.go — checkForExistingIDs, bulk insert path
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/resurrection.go — parent resurrection, the third insertIssue caller
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/ids.go — IsHierarchicalID, ValidateIssueIDPrefix
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/dependencies.go — AddDependency, cycle detection, parent-child direction rule
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/storage.go — the Storage and Transaction interfaces
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/memory/memory.go — the other Storage implementation
- /home/ben/worktrees/beads/w6_bd-parent-echo/cmd/bd/create.go — the CLI create path
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/rpc/server_issues_epics.go — the daemon create path
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/importer/importer.go — the import path the plan claims is unaffected
- /home/ben/worktrees/beads/w6_bd-parent-echo/cmd/bd/version.go — what `bd version --json` reports, which ship gate 10 depends on

Existing tests the plan rewrites:
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/child_id_test.go
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/storage/sqlite/child_counters_test.go
- /home/ben/worktrees/beads/w6_bd-parent-echo/internal/rpc/rpc_test.go

Commands that answer questions the files alone will not:
- `grep -rn "GetNextChildID\|insertIssue(\|insertIssues(\|isUniqueConstraintError" --include=*.go /home/ben/worktrees/beads/w6_bd-parent-echo` — every caller the interface change and the strict insert touch
- `cd /home/ben/worktrees/beads/w6_bd-parent-echo && go build ./... 2>&1 | head` — the current build state
</files>

<checklist>
USER INTENT
- Does the plan state what the user asked for, not what the agent decided to build? The ask: find and fix the cause of `bd create --parent` echoing existing IDs, designed as if collision-freedom had been foundational.
- Does every proposed change trace to that intent? Flag gold-plating.
- If the plan succeeds perfectly, is the reported bug gone from every path that can create an issue?

EVIDENCE
- Is every file:line citation in the Problem and Changes sections correct? Check them against the files.
- Is the Key Insight's claim true, that tombstones expire and `bd delete` hard-deletes, so a counter that only rises is needed to avoid reusing a dead child's number? Verify against types.go and queries.go.
- Is the claim that the farmplanner counter is at 33 with children up to .36 consistent with astra_report_v1.md?

MECHANISMS EXIST
- `RunInTransaction` (transaction.go): does it give the new `CreateChildIssue` what the plan assumes — one connection, BEGIN IMMEDIATE, rollback on error, no nested transaction when `sqliteTxStorage.CreateIssue` and `AddDependency` run inside it?
- Does `sqliteTxStorage.AddDependency` do everything the CLI's post-create `store.AddDependency` did, including the blocked-cache invalidation and the parent-child direction check? Name any behavior that would be lost by moving the edge inside the transaction.
- Does `sqliteTxStorage.CreateIssue` validate, hash, and set timestamps the way `SQLiteStorage.CreateIssue` does, so routing child creates through it changes nothing else?
- SQLite `LIKE ... ESCAPE` and the `ON CONFLICT DO UPDATE` upsert: confirm both work as the plan assumes on this driver (modernc.org/sqlite) and this schema.
- Does the plan's claim hold that `tryResurrectParentWithConn` returns early when the parent exists, so strict insert cannot break resurrection?

PROOF-TEST
- The plan asserts that no live caller depends on silent duplicate tolerance in `insertIssue` or `insertIssues`. Is that verified by the enumerated callers, or is it an unverified hypothesis that needs a test before the change lands?
- Does the plan depend on any other unverified claim about upstream behavior? If one cannot be tested in isolation, is it flagged with a fallback?

SCOPE
- Do the "Not in Scope" entries match the user's boundaries recorded in manager_log.md? Is anything excluded that the intent requires?
- Are the "Files NOT Affected" reasons true after reading those files? Specifically: does `checkForExistingIDs` really make strict insert a no-op for the import path, and does `transaction.go`'s `CreateIssues` really have no live callers?
- Deploy is included, so no `Done-includes-deploy: no` citation is needed. Confirm the deploy step targets the binary the fleet actually runs.

MINIMALITY
- Which named failure justifies each piece of new machinery: the counter write-back, the LIKE-escape, the error-string translation, the memory-storage implementation? Anything without one is a finding whose fix is deletion.
- Is the four-phase split the smallest coherent change, or does the plan carry work that could be dropped without leaving the bug reachable?
- Does keeping both `checkForExistingIDs` and a strict insert amount to two mechanisms for one guarantee? If you think one should go, say which and why the remaining one covers the batch case.

KEY INSIGHT
- Is it non-obvious and does it say what breaks if forgotten, or is it a restatement of the problem?

ALGORITHM
- The allocation trace uses the real farmplanner data. Is the arithmetic right, and does it match what the code in the plan would actually do?
- What assumption must the "skip unparseable suffixes" rule hold for it to be safe? Is that assumption stated and true?
- Is there a child ID shape the parse rule mishandles: leading zeros, a suffix wider than int64, a parent whose own ID contains a dot, a three-level ID?

PHASE GATES
- Is each phase gate a threshold a builder can evaluate without judgment, and are the phases ordered so each passes before the next starts?

RISKS & ROLLBACK
- Is the rollback claim true that old and new binaries interoperate because the counter table's shape is unchanged? Consider what an old binary does after the new one has written a higher floor.
- Are the listed risks the real ones? Name any risk the plan misses that would cause data loss or a wrong ID.

VERIFICATION
- Ship gate blocks 1 through 10: are the commands runnable as written, in row order, on this machine? Check every flag against the actual command definitions in cmd/bd — `--no-daemon`, `--no-auto-import`, `--no-auto-flush`, `--db`, `init --quiet --prefix --skip-hooks --skip-merge-driver`, `create --silent --id --parent --deps --description`, `close`, `export -o`, `import -i`.
- Is each Expected value the literal substring the block will actually print, given the plan's own design? Recompute the IDs the gate expects, in order.
- Do the gate blocks leave state that a later block depends on, and is that dependency correct?
- Does gate 10's check work given what `resolveCommitHash` returns in version.go?
- Is there at least one proof of behavior beyond "tests pass", and could a builder read pass or fail without interpretation?
- Do the Phase 3 tests cover the behaviors the bug violated, and does the plan's demand that each new test fail against pre-change source make sense for every one of them?

OPERATIONAL
- Is the deploy command correct for this machine, given that `~/.local/bin/bd` is a shell wrapper that executes `~/.local/libexec/bd-real`?
- Does the plan account for other bd databases and running processes on this machine at deploy time?
</checklist>

<revision-context>
This plan revises the version the previous round reviewed. Read the whole plan.

- Lines that changed: /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/plan_v2.diff
- Previous review: /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/review_codex_v1.md

For each change: confirm it resolves the finding it answers, then check that the rest of the plan still agrees with it (a changed row in one section can break a gate, table, or handoff in another). Re-raise a previous finding only if its fix is absent or wrong.

All ten R1 findings were accepted. Three were answered differently from the fix you proposed, and those answers are what this round must judge:
- F1: rather than adding an importer change row, the collision error now wraps the driver error with `%w`, so the message names the ID and still contains `UNIQUE constraint failed` for `internal/importer/importer.go:472` to match. Check that this holds for the error text modernc.org/sqlite actually produces, and that the duplicate helper removal does not change what either call site matches.
- F5: rather than raising the floor in the hierarchical branch of the two `CreateIssue` implementations, `insertIssue` and `insertIssues` raise it, on the argument that the insert is the one choke point every create passes through. Check that this reaches every create path, that it cannot double-count or lower a floor, that grouping by parent in the batch path is correct, and that the extra write per insert is acceptable on the import path's volumes.
- F9: the epic-direction abort is documented and tested rather than suppressed. Check that no other dependency-validation rule inside `sqliteTxStorage.AddDependency` becomes a create-abort without being named in the plan.

Also judge the two new ship gate rows, 11 and 12, on whether they would actually fail against the current source and pass against the plan's design.
</revision-context>

<constraints>
- Load the elegant skill, the coding skill, and the testing skill before evaluating, and apply them as checklist lenses: foundational fit versus bolted-on, naming and locality, test quality and coverage gaps.
- Read every file listed in <files>. The plan's citations are claims to verify, not facts.
- Decisions recorded in manager_log.md are settled — do not re-open them absent safety evidence. In particular the user locked: the counter stays as a monotonic floor, `CreateChildIssue` replaces `GetNextChildID` in the interface, inserts become strict, `--id` with `--parent` becomes legal when they agree, and the farmplanner database repair belongs to another item.
- Only report issues that will cause rework, wrong behavior, degraded result, data corruption, or miss user intent.
- Do not suggest scope expansion — evaluate the plan as written.
- Do not create or modify the plan — write only your review file.
</constraints>

Write review to: /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/review_codex_v2.md

<output-format>
Output exactly this — the verdict line plus finding rows, no other prose:

VERDICT: CLEAN|FIX findings=N
| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|

- CLEAN requires findings=0; FIX requires one or more finding rows.
- sev: high = design or behavior must change; med = correctness risk in a specific case; low = wording, docs, cosmetic.
- N states the true total; the table lists every finding, no cap.
- Each defect sentence stands alone — the manager dispositions it with no follow-up round: name the mechanism and the consequence, grounded in file:line and, when needed, a number or a short quote.
- Each fix sentence is a concrete change to the plan.
</output-format>
