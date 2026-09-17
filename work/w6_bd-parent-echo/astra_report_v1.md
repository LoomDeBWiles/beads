# Summary

All three pre-existing children, `farmplanner-r3yq.8.32`, `.33`, and `.34`, were created with explicit `--id`; the manager transcript proves this, and that path never advances `child_counters`.
`--parent` trusts that counter without checking existing child IDs, so a counter of 31 allocates 32 even when 34 exists.
The shared single-row insertion helper uses `INSERT OR IGNORE`, discards the affected-row result, and suppresses UNIQUE errors, so the old issue survives while create reports success, records a false creation event, and adds dependencies to it.
The installed `0.34.0 (02e0ab0fe)` binary reproduced both reported warnings and exit-zero returns against a scratch database.
Fix creation as one atomic storage operation that allocates above both the existing numeric children and the counter, inserts strictly, and commits its requested relationships only with the new issue; this repairs stale counters during allocation without requiring a counter-resync migration.

# Evidence

Source paths below are relative to `/home/ben/worktrees/beads/w6_bd-parent-echo`. Checkout HEAD was `ad55e1298e9dd1dbd6ae5943e9586e4496c520eb`. `git diff 02e0ab0fe -- cmd/bd/create.go internal/storage/sqlite` returned no differences. The reproduction used `/home/ben/.local/libexec/bd-real`, whose version output identifies `02e0ab0fe`; no rebuilt binary was substituted.

## ID allocation and false success

`cmd/bd/create.go:151` rejects simultaneous `--id` and `--parent`. In direct mode, `create.go:159` checks the parent, calls `store.GetNextChildID` at line 169, and puts the generated value into `explicitID` at line 173. The later storage call therefore receives an already populated issue ID, indistinguishable from a user-supplied one.

`internal/storage/sqlite/hash_ids.go:11` implements `getNextChildNumber` with this entire allocation query:

```sql
INSERT INTO child_counters (parent_id, last_child)
VALUES (?, 1)
ON CONFLICT(parent_id) DO UPDATE SET
    last_child = last_child + 1
RETURNING last_child
```

`GetNextChildID`, at `hash_ids.go:29`, checks parent existence and hierarchy depth, calls that function at line 58, and formats `parentID.counter` at line 64. It never searches children, checks the candidate ID, or considers their status. A row `(farmplanner-r3yq.8,31)` necessarily returns `.32`. The allocator's SQL statement is atomic, but it runs separately from insertion. It protects two counter allocations from returning the same number, not allocation against another path inserting a supplied ID.

`internal/storage/sqlite/queries.go:77`, `CreateIssue`, begins an immediate transaction at line 142. For a supplied ID it validates the prefix and checks/resurrects the parent chain at lines 175-193. It does not check that the issue ID is unused. It calls `insertIssue` at line 197.

`internal/storage/sqlite/issues.go:43` discards the SQL result and executes `INSERT OR IGNORE INTO issues`. Lines 60-68 additionally suppress any error recognized by `isUniqueConstraintError`. Thus both SQL conflict handling and the Go error policy permit false success. No `RowsAffected` check distinguishes an inserted row from an ignored row. This is not an upsert: the colliding issue row is not overwritten.

`CreateIssue` then records a creation event at `queries.go:202`, marks the existing ID dirty at line 210, commits at line 215, and returns nil. `internal/storage/sqlite/events_helpers.go:13` serializes the attempted issue, not a database readback, into the event's `new_value`. A created event therefore is not proof that its advertised title was stored.

`cmd/bd/create.go:307` correctly treats a non-nil storage error as fatal. It receives nil here. The parent-child insert at lines 311-320 and explicit dependency inserts at lines 330-370 run afterward; errors are warnings. A bare `--deps` ID means `blocks`, at lines 350-353. Existing parent-child and blocks edges are different unique keys, explaining the different warning in each command. Labels and hooks can also run against the old ID, at lines 323-327 and 407-409.

`--silent` only selects `fmt.Println(issue.ID)` at `create.go:414-415`. It does not swallow an error. Normal and JSON output are also built from the attempted issue, so they can advertise a new title and open status that the database does not contain.

The RPC handler uses the same allocator and storage insertion (`internal/rpc/server_issues_epics.go:143`, `:220`). Unlike the direct CLI, its duplicate parent-child edge returns a failed response at lines 234-238, after the earlier storage call has committed. The exact warnings in this incident match the direct CLI path. The scratch runs explicitly disabled the daemon.

## Which paths maintain the counter

Only `getNextChildNumber` changes `last_child` in production Go code. A repository-wide search for `child_counters` and `last_child` finds no counter maintenance in issue insertion, import, or rename.

`create --parent`, in direct and RPC mode, advances the counter before creating the issue through the calls above. It advances even if a later step fails or the issue insert is ignored. Its first allocation for an absent counter row is 1.

Explicit `create --id`, including a hierarchical ID, does not call the allocator. `queries.go:175-193` checks the parent chain but does not advance the parent's counter. Adding a `parent-child` dependency later does not advance it either. The scratch reproduction demonstrates both facts without directly modifying a counter.

Single creates inside a storage transaction also use the same insertion helper (`internal/storage/sqlite/transaction.go:179`) and have no counter reconciliation. Batch create uses `CreateIssuesWithFullOptions` at `internal/storage/sqlite/batch_ops.go:224`; it generates/validates IDs at line 260 and inserts at line 270, without maintaining child counters. Batch create does separately reject existing and repeated IDs, using `checkForExistingIDs` at lines 105-153 and calling it at line 265. That precheck explains why single and batch creation have different collision behavior despite sharing permissive insert helpers.

JSONL import batches new issues by hierarchy depth and invokes that batch path with actor `import` at `internal/importer/importer.go:771-801`. Line 804 explicitly says counter synchronization was removed because hash IDs supposedly no longer need it. That reasoning overlooked numeric child IDs. Auto-import calls `importIssuesCore` at `cmd/bd/autoimport.go:256`; explicit import calls it at `cmd/bd/import.go:289`; sync delegates to a `--no-daemon import` subprocess at `cmd/bd/sync.go:1615-1647`. Loading a worktree database from JSONL has the same gap. The supplementary scratch import below confirms that issue IDs and edges are imported while counters remain absent.

Rename does not repair it. `cmd/bd/rename_prefix.go:427` calls `UpdateIssueID`, and line 436 calls `RenameCounterPrefix`. `internal/storage/sqlite/queries.go:867` changes IDs with foreign keys disabled, but does not rename child-counter keys. `RenameCounterPrefix` at lines 992-996 is a no-op with the incorrect comment that hash IDs do not use counters. Import rename creates via `CreateIssue` (`internal/importer/importer.go:423`, `:470`) and likewise does not advance them. This is another potential source of stale or missing counters, not the demonstrated incident trigger.

The schema at `internal/storage/sqlite/schema.go:161` creates an empty `child_counters` table with `ON DELETE CASCADE` for its parent reference. `internal/storage/sqlite/migrations/014_child_counters_table.go:8-33` only creates that table if absent; it never seeds it from existing children. Neither migration nor subsequent database opening repairs lagging counters. Deleting a parent can also remove its counter through the foreign key.

## Farmplanner: original creation path is proved

No `bd` command was run against farmplanner. Initial SQLite opens with `mode=ro` returned `unable to open database file`. The directory contained `beads.db` and no `beads.db-wal` or `beads.db-shm` at inspection. Read-only queries succeeded with this URI:

```text
file:/home/ben/projects/farmplanner/.beads/beads.db?mode=ro&immutable=1
```

`immutable=1` avoids SQLite attempting journal/shared-memory setup. These observations concern the on-disk database inspected during this run; immutable mode is not a guarantee of a coherent snapshot if another process writes concurrently. The independent transcript and current JSONL corroborate the rows below.

There is no `issues.created_by` column. `SELECT name FROM pragma_table_info('issues') WHERE name LIKE '%creat%';` returns only `created_at`. Creation actors are available in `events.actor`; dependency creators are in `dependencies.created_by`.

These queries read the relevant database evidence:

```bash
sqlite3 'file:/home/ben/projects/farmplanner/.beads/beads.db?mode=ro&immutable=1' "
SELECT id,title,status,created_at FROM issues
WHERE id IN ('farmplanner-r3yq.8.32','farmplanner-r3yq.8.33','farmplanner-r3yq.8.34');
SELECT * FROM child_counters WHERE parent_id='farmplanner-r3yq.8';
SELECT id,issue_id,event_type,actor,created_at,json_extract(new_value,'$.title')
FROM events WHERE event_type='created'
AND issue_id IN ('farmplanner-r3yq.8.32','farmplanner-r3yq.8.33','farmplanner-r3yq.8.34')
ORDER BY id;"
```

```text
farmplanner-r3yq.8.32|Correct test-gate invocation and remove obsolete evidence ignore exceptions|closed|2026-09-09T07:13:58.070225561+02:00
farmplanner-r3yq.8.33|Honor physical PDF page markers in citation mapping|closed|2026-09-09T08:35:33.349057234+02:00
farmplanner-r3yq.8.34|Enforce reviewed citation maps and recoverable source installation before live writes|closed|2026-09-09T14:23:45.98977019+02:00
farmplanner-r3yq.8|33
14782|farmplanner-r3yq.8.32|created|ben|2026-09-09 05:13:58|Correct test-gate invocation and remove obsolete evidence ignore exceptions
14805|farmplanner-r3yq.8.33|created|ben|2026-09-09 06:35:33|Honor physical PDF page markers in citation mapping
14872|farmplanner-r3yq.8.34|created|ben|2026-09-09 12:23:45|Enforce reviewed citation maps and recoverable source installation before live writes
14893|farmplanner-r3yq.8.32|created|ben|2026-09-09 16:19:40|Validate rate-dependency crops against the canonical crop vocabulary, not labeled_crops
14895|farmplanner-r3yq.8.33|created|ben|2026-09-09 16:19:40|Promote VISOR 89167-40 as correction batch 1b
```

Event timestamps are UTC; issue timestamps explicitly carry CEST. The original dependency events show `.32 parent-child .8` at 05:14:09 UTC, eleven seconds after creation, and `.33 blocks .8` and `.34 blocks .8` at their original creation times. At 16:19:40 UTC, events 14894 and 14896-14898 show newly added `.32 blocks .8`, `.33 parent-child .8`, `.33 blocks .32`, and `.33 blocks .20`. Thus the incident changed graph data in addition to the manager's later, reportedly removed `.27 -> .33` edge. This investigation made no repairs.

The decisive command evidence is in `/home/ben/.codex/sessions/2026/09/08/rollout-2026-09-08T23-00-59-01a082d2-f8a7-7c70-ac0e-0331066e6281.jsonl`. Line 2767, timestamp `2026-09-09T05:13:55.958Z`, contains the explicit create for `.32`; line 3308, `06:35:31.974Z`, contains the explicit create for `.33`; line 4907, `12:23:35.897Z`, contains the explicit create for `.34`. All three target `/home/ben/projects/farmplanner/.beads/beads.db`, with `--no-auto-import`. The relevant exact command lines extracted from those historical tool calls are below; they were read, not executed:

```bash
bd --db /home/ben/projects/farmplanner/.beads/beads.db --no-auto-import create --id farmplanner-r3yq.8.32 --title 'Correct test-gate invocation and remove obsolete evidence ignore exceptions' --type bug --priority 0 --description-file work/w893_edition-dedup-copy/beads_v46/farmplanner-r3yq.8.32.md
bd --db /home/ben/projects/farmplanner/.beads/beads.db --no-auto-import create --id farmplanner-r3yq.8.33 --type bug --priority 1 --title 'Honor physical PDF page markers in citation mapping' --body-file work/w893_edition-dedup-copy/beads_v46/farmplanner-r3yq.8.33.md
bd --db /home/ben/projects/farmplanner/.beads/beads.db --no-auto-import create --id farmplanner-r3yq.8.34 --title 'Enforce reviewed citation maps and recoverable source installation before live writes' --type task --priority 1 --body-file work/w893_edition-dedup-copy/artifacts/source_install_contract_v49.md --deps farmplanner-r3yq.8,farmplanner-r3yq.8.31
```

Transcript line 2774 records the separate parent-child addition for `.32`. Line 3308 adds `.33 -> .8` with the default blocks type. These commands explain the precise pre-existing dependency types that produced the two different warnings. Explicit IDs, rather than an import hypothesis, are the proven source of these three children bypassing allocation. The pre-incident counter value 31 is inferred from the reported allocations and the increment-only implementation; no before-incident counter snapshot was found.

The current `/home/ben/projects/farmplanner/.beads/issues.jsonl:1686`, `:1687`, and `:1688` contain `.32`, `.33`, and `.34` respectively, with their original titles and closed status. Git history does not contain their creation: the following all-ref searches returned the last JSONL commit shown and no pickaxe match:

```bash
git -C /home/ben/projects/farmplanner log --all -1 --format='%H %aI %s' -- .beads/issues.jsonl
git -C /home/ben/projects/farmplanner log --all --format='%h %aI %s' -S '"id":"farmplanner-r3yq.8.32"' -- .beads/issues.jsonl
```

```text
e3342944465df9e878f48d36cd7a040cb6bb8f71 2026-09-08T18:51:33+02:00 lane sync (wt-merge): .beads/issues.jsonl
```

An all-ref JSONL log restricted to September 9 also returned no entries. Available Git history predates the three creations; it cannot establish their commands or suggest that they arrived through a committed JSONL import.

## Why the flags conflict

The restriction is a CLI policy, not a SQLite requirement. `--parent` currently means both “allocate the next hierarchical ID” and “add a parent-child edge”; the generated ID would otherwise replace `--id` at `create.go:173`. Rejecting both flags prevents that ambiguity under the current implementation. It is not fundamentally necessary: a supplied hierarchical ID can be checked against the specified parent and inserted with its parent edge atomically. The existing explicit-ID-plus-`dep add` workaround proves the data model supports the result. Permit both only when the ID's immediate hierarchical parent matches `--parent`; reject a mismatch before any write.

# Reproduction

All scratch data is under `work/w6_bd-parent-echo/scratch/astra-repro/`. No direct counter manipulation is needed. This smaller example uses counter 1 and existing children 2-4 instead of counter 31 and children 32-34. Hooks and merge-driver installation are skipped, and every executed bd invocation uses `--no-daemon`. Auto-import and auto-flush are disabled to isolate the insertion behavior.

Exact setup and failing commands:

```bash
mkdir -p /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/scratch/astra-repro
cd /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/scratch/astra-repro
git init -q
/home/ben/.local/libexec/bd-real --no-daemon --db "$PWD/.beads/beads.db" init --quiet --prefix repro --skip-hooks --skip-merge-driver
bdx() { /home/ben/.local/libexec/bd-real --no-daemon --no-auto-import --no-auto-flush --db "$PWD/.beads/beads.db" "$@"; }
bdx version
bdx create --silent --id repro-abc --type epic --title 'Parent epic' --description 'Scratch reproduction'
bdx create --silent --parent repro-abc --title 'Old child 1' --description 'Scratch reproduction'
bdx create --silent --id repro-abc.2 --title 'Old child 2' --description 'Scratch reproduction'
bdx dep add repro-abc.2 repro-abc --type parent-child
bdx close repro-abc.2
bdx create --silent --id repro-abc.3 --title 'Old child 3' --deps repro-abc --description 'Scratch reproduction'
bdx close repro-abc.3
bdx create --silent --id repro-abc.4 --title 'Old child 4' --description 'Scratch reproduction'
bdx close repro-abc.4
sqlite3 .beads/beads.db 'SELECT * FROM child_counters; SELECT id,title,status FROM issues ORDER BY id;'
B2=$(bdx create --silent --type bug --priority 1 --parent repro-abc --deps repro-abc --title 'New bug' --description 'Scratch reproduction'); code=$?
printf 'bug=%s exit=%s\n' "$B2" "$code"
B3=$(bdx create --silent --type task --priority 1 --parent repro-abc --deps "repro-abc,$B2,repro-abc.1" --title 'New task' --description 'Scratch reproduction'); code=$?
printf 'task=%s exit=%s\n' "$B3" "$code"
```

Actual combined output, including warnings on stderr:

```text
bd version 0.34.0 (02e0ab0fe)
repro-abc
repro-abc.1
repro-abc.2
✓ Added dependency: repro-abc.2 depends on repro-abc (parent-child)
✓ Closed repro-abc.2: Closed
repro-abc.3
✓ Closed repro-abc.3: Closed
repro-abc.4
✓ Closed repro-abc.4: Closed
repro-abc|1
repro-abc|Parent epic|open
repro-abc.1|Old child 1|open
repro-abc.2|Old child 2|closed
repro-abc.3|Old child 3|closed
repro-abc.4|Old child 4|closed
Warning: failed to add parent-child dependency repro-abc.2 -> repro-abc: failed to add dependency: sqlite3: constraint failed: UNIQUE constraint failed: dependencies.issue_id, dependencies.depends_on_id, dependencies.type
bug=repro-abc.2 exit=0
Warning: failed to add dependency repro-abc.3 -> repro-abc: failed to add dependency: sqlite3: constraint failed: UNIQUE constraint failed: dependencies.issue_id, dependencies.depends_on_id, dependencies.type
task=repro-abc.3 exit=0
```

Readback and flag-conflict check:

```bash
sqlite3 .beads/beads.db "SELECT * FROM child_counters; SELECT id,title,status FROM issues ORDER BY id; SELECT issue_id,event_type,json_extract(new_value,'\$.title') FROM events WHERE event_type='created' ORDER BY id; SELECT issue_id,depends_on_id,type FROM dependencies WHERE issue_id IN ('repro-abc.2','repro-abc.3') ORDER BY issue_id,depends_on_id,type;"
bdx create --silent --id repro-abc.5 --parent repro-abc --title 'Explicit child'
printf 'exit=%s\n' "$?"
```

```text
repro-abc|3
repro-abc|Parent epic|open
repro-abc.1|Old child 1|open
repro-abc.2|Old child 2|closed
repro-abc.3|Old child 3|closed
repro-abc.4|Old child 4|closed
repro-abc|created|Parent epic
repro-abc.1|created|Old child 1
repro-abc.2|created|Old child 2
repro-abc.3|created|Old child 3
repro-abc.4|created|Old child 4
repro-abc.2|created|New bug
repro-abc.3|created|New task
repro-abc.2|repro-abc|blocks
repro-abc.2|repro-abc|parent-child
repro-abc.3|repro-abc|blocks
repro-abc.3|repro-abc|parent-child
repro-abc.3|repro-abc.1|blocks
repro-abc.3|repro-abc.2|blocks
Error: cannot specify both --id and --parent flags
exit=1
```

Five issue rows remain: neither attempted new issue exists. The old titles and closed statuses survive, while two additional created events and new edges refer to those IDs.

A second experiment exported these five issues and imported them into a fresh scratch database:

```bash
bdx export -o exported.jsonl
mkdir imported
cd imported
git init -q
bdx init --quiet --prefix repro --skip-hooks --skip-merge-driver
bdx import -i ../exported.jsonl
sqlite3 .beads/beads.db 'SELECT count(*) AS counter_rows FROM child_counters; SELECT count(*) AS issue_rows FROM issues;'
bdx create --silent --parent repro-abc --title 'After import' --description 'Scratch reproduction'
printf 'exit=%s\n' "$?"
sqlite3 .beads/beads.db "SELECT * FROM child_counters; SELECT id,title,status FROM issues WHERE id='repro-abc.1';"
```

```text
Import complete: 5 created, 0 updated
0
5
Warning: failed to add parent-child dependency repro-abc.1 -> repro-abc: failed to add dependency: sqlite3: constraint failed: UNIQUE constraint failed: dependencies.issue_id, dependencies.depends_on_id, dependencies.type
repro-abc.1
exit=0
repro-abc|1
repro-abc.1|Old child 1|open
```

Import is therefore an independently reproduced route to the same defect, although the transcript proves explicit creation caused the farmplanner gap.

# Root cause

The storage boundary has no enforced contract that a successful create inserts a new identity. Hierarchical allocation treats a partially maintained counter as authoritative, while general insertion inherits a duplicate-tolerant policy intended for JSONL. Explicit IDs establish real children without reserving their numbers; `--parent` later reuses those numbers; permissive insertion converts the collision into success; subsequent operations mutate the old issue's graph. A counter-only repair would leave explicit-ID collisions falsely successful, and changing warning severity would detect the failure only after side effects.

# Recommended fix

Make creation a single atomic storage operation with strict insertion and allocation derived from the actual occupied namespace. Change `getNextChildNumber` and `GetNextChildID` in `internal/storage/sqlite/hash_ids.go` so allocation occurs on the same connection and immediate transaction as `CreateIssue`, rather than committing a standalone counter increment. Compute `next = max(recorded_counter, largest_existing_immediate_numeric_child_suffix) + 1`, then reserve that value and insert the child in that transaction. Include open, closed, and tombstoned rows. Match the literal parent prefix and parse exactly one numeric suffix; exclude descendants and similar prefixes, and reject integer overflow. An indexed parent-prefix range can bound the rows examined.

Move the direct and RPC callers (`cmd/bd/create.go:159`, `internal/rpc/server_issues_epics.go:140`) onto that shared operation, carrying the requested parent into storage instead of preallocating an explicit ID. Keep parent validation, the new row, requested dependencies/labels, creation event, dirty mark, and counter reservation within its transaction. Emit success and run hooks only after commit. Reuse the existing transaction boundary rather than maintaining separate direct/RPC creation algorithms; migrate the storage interface and implementations together.

In `internal/storage/sqlite/issues.go`, replace `INSERT OR IGNORE` with plain `INSERT` for both helpers and remove UNIQUE-error suppression. Check insertion success before recording events. `CreateIssue` and `sqliteTransaction.CreateIssue` must hard-fail on any final ID collision, regardless of counter state, output format, or caller. Do not overwrite the old issue or report its ID as a successful new create. Generated candidates can be selected to avoid occupied IDs before insertion; a uniqueness violation at insertion is still a transaction failure. Batch and importer code must keep intentional update/deduplication decisions in their explicit reconciliation flow, not hide arbitrary constraints in low-level insertion.

No counter-resync migration is required for this recommendation: every allocation reconciles the stored counter with existing IDs inside the write transaction, including absent counters and databases created before the fix. A one-time backfill alone would not protect the next import or explicit create. Counters may lag between allocations by design; they become a retained allocation floor, not evidence that no higher ID exists. Preserve existing higher counter values, and make `UpdateIssueID`/`RenameCounterPrefix` transfer parent counter keys when a parent is renamed so already allocated numbers are not forgotten. The migration that originally creates the table need not be rewritten.

Cover this with end-to-end direct CLI and RPC tests that prove stale and absent counters allocate above existing children after explicit create and after JSONL import. Assert old rows remain unchanged, each successful ID resolves to the requested issue, and exactly one creation event and the intended relationships exist. Test normal, `--silent`, and JSON output; a supplied duplicate ID must return failure with no success output, extra event, dirty mark, label, dependency, or hook. Test both single and batch/transaction APIs, including duplicates within a batch. Test concurrent automatic creates and explicit/import writers using real database connections, with unique committed IDs and atomic rollback on failure. Test closed/tombstoned children, sparse numbering, nested IDs, dotted prefixes, similarly named parents, counters ahead of rows, numeric overflow, and rename followed by allocation. Test import idempotence and legitimate updates after making insert strict. Finally, test matching `--id` plus `--parent` succeeds atomically and a mismatch fails before mutation.

# Open questions

The original explicit-ID commands are proved; their exact executable build at the morning invocation times was not independently recovered. The installed build reproduces their counter behavior, so version skew is unnecessary to explain this incident.

There is no recorded before-incident counter snapshot. The value 31 follows from the reported `.32` first allocation and current code; unrelated historical resets are not ruled out, but are not needed for the demonstrated cause.

Farmplanner still showed new incident-time edges on `.32` and `.33` during inspection. The manager's removal of `.27 -> .33` does not remove those other edges or the false creation events. Their repair requires a separate authorized change; this report changed no farmplanner data.
