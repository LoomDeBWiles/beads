# w6_bd-parent-echo: Child creation that cannot collide or silently succeed

> `bd create --parent` hands out child IDs that already exist and reports success without writing anything. Make child creation one transaction that allocates from the issues table, inserts strictly, and fails loudly on any collision.

Scale: standard

## User Intent

A farmplanner manager reported: "the bead tool, given a parent flag, echoed existing IDs without creating anything. I removed the bad dependency it caused and created the beads with explicit IDs." The user asked for the source of that bug, then for the fix to be designed as if collision-freedom had been a foundational requirement rather than patched in place.

Done means: `bd create --parent` always creates a new issue or fails with a nonzero exit; no create anywhere in bd can report success without inserting a row; the workaround the farmplanner agent had to use (`--id` plus a separate `bd dep add`) is no longer necessary; and the fleet binary at `~/.local/libexec/bd-real` runs the fix.

## Problem

Child IDs are allocated from the `child_counters` table alone. `getNextChildNumber` (`internal/storage/sqlite/hash_ids.go:11-23`) increments `last_child` for the parent and returns it; `GetNextChildID` (`hash_ids.go:29-65`) formats `parent.N` from that number and never reads the issues table. Only that one path advances the counter. `bd create --id 8.32` does not (`queries.go:175-193` validates the prefix and parent chain, nothing more), and JSONL import does not (`internal/importer/importer.go:804` removed counter sync on the reasoning that hash IDs no longer need counters, which overlooked numeric child IDs).

On farmplanner the counter for epic `farmplanner-r3yq.8` sat at 31 while children `.32`, `.33` and `.34` existed, all three created with explicit `--id` earlier the same day (proved from that session's codex transcript, quoted in `astra_report_v1.md`). The next two `--parent` creates were handed 32 and 33.

A collision then passes silently. `insertIssue` (`internal/storage/sqlite/issues.go:43-69`) uses `INSERT OR IGNORE`, discards the result, and additionally swallows any UNIQUE error through `isUniqueConstraintError`. `CreateIssue` therefore commits with no row written, records a `created` event carrying the new title against the old issue (`queries.go:202`), marks the old row dirty, and returns nil. The CLI then attaches the parent edge, every `--deps` entry, labels and hooks to the old issue (`cmd/bd/create.go:311-370`), and prints the old ID as if it were new. In the farmplanner database this left four dependency edges and two false `created` events on two closed bugs, and wired a live epic's blocker onto the wrong issue.

The current database still shows the counter at 33 with children up to `.43` under that one epic, so the drift is ten numbers wide and still growing.

The same silent insert costs data in a narrower case. `internal/importer/importer.go:465-470` renames an issue by deleting the old ID and creating the new one in two separate transactions, and the branch at `:472` exists to recognise a clone that created the target in between. The plain case where the target already exists is handled earlier and correctly (`:348-364` returns as soon as it sees matching content), so the loss is confined to that race window: if another clone writes the target between the lookup at `:350` and the insert at `:470`, today's silent insert leaves the old row deleted, nothing written, and the import reporting success, because the recovery branch cannot fire on an error the create never returns. Strict inserts make it fire.

## Key Insight

**The counter answers "what numbers have ever been used", which is not the same question as "what numbers are taken", and only the second one can be answered from the issues table.** Tombstones expire after 30 days (`internal/types/types.go:152`) and the importer's rename path removes rows outright through `DeleteIssue` (`queries.go:1172`), so a number whose issue is gone from this database may still exist in git history or a peer's copy; reusing it would collide on the next sync. So the counter stays, demoted to a monotonic floor that only ever rises, and the issues table becomes the authority on what is taken. The floor earns its keep only if every path that creates a hierarchical ID raises it, so the raise belongs at the one choke point every create passes through, the insert itself; a floor that only `--parent` maintains protects exactly the numbers the scan would have found anyway. Forget this and you get one of two wrong designs: dropping the counter entirely (which reuses dead children's numbers), or keeping it authoritative and adding a repair migration (which fixes today's drift and lets tomorrow's import re-create it).

The second half is that no allocator can be trusted alone. Whatever number the allocator picks, the insert is the only place that knows whether the row was written, so the insert must be strict and the create must fail on collision from any door: `--parent`, `--id`, import, or a future one.

## Design

Allocation, insertion and the parent edge become one storage operation inside one `BEGIN IMMEDIATE` transaction:

```
CreateChildIssue(parentID, issue, actor)
  │
  ├─ RunInTransaction  (BEGIN IMMEDIATE — serializes writers across processes)
  │    ├─ parent exists?  ── no ──► tryResurrectParentWithConn(conn) ── no ──► error
  │    ├─ depth(parent) < 3?  ── no ──► error
  │    ├─ issue.ID == ""  ──► allocate (read only): next = max(floor, highest existing child) + 1
  │    │   issue.ID != ""  ──► validate: parent of issue.ID == parentID, else error
  │    ├─ INSERT INTO issues            (plain INSERT — UNIQUE violation aborts here)
  │    ├─ raise the parent's floor to the inserted number
  │    ├─ record created event, mark dirty
  │    └─ INSERT parent-child dependency
  └─ COMMIT
```

Allocation is a pure read. It reads the floor and the existing children on the transaction's own connection, and writes nothing:

```sql
SELECT last_child FROM child_counters WHERE parent_id = ?;         -- absent → 0
SELECT id FROM issues WHERE id LIKE ? ESCAPE '\';                  -- pattern: <escaped parent>.%
```

`%`, `_` and `\` in the parent ID are escaped into the pattern. Each returned ID is parsed in Go: strip `parentID + "."`, accept only an all-digit remainder with no further dot, and take the maximum. Closed issues and tombstones count. One numeric policy governs both the scan and the floor writes: a suffix is used only if it parses as a non-negative int64. A suffix that does not parse, including one wider than int64 such as `p.9223372036854775808`, is skipped by the scan and raises no floor, because it is not a number this allocator could produce and the strict insert is the backstop. Allocation refuses rather than overflows: if the maximum is `math.MaxInt64`, `allocateChildNumber` returns an error naming the parent instead of incrementing into a negative suffix that `IsHierarchicalID` would not even recognise as hierarchical. Then `next = max(floor, maxExisting) + 1`. Nothing is written here: the insert that follows raises the floor in the same transaction, so a second write at allocation time would guarantee nothing the first does not, and a create that fails rolls both back together.

Worked trace on the farmplanner data. Floor 31; children present `.3`, `.30`, `.31`, `.32`, `.33`, `.34`. Scan yields max 34. `next = max(31, 34) + 1 = 35`. Insert of `farmplanner-r3yq.8.35` succeeds, and the insert raises the floor to 35. Before this change the same input produced 32, an existing closed bug, and a silent no-op.

Before and after, at the two doors that were broken:

| Case | Before | After |
|------|--------|-------|
| `--parent` with stale counter | prints existing ID, exit 0, nothing written, edges land on the old issue | allocates above the highest existing child, creates the row |
| `--id` naming an existing issue | prints that ID, exit 0, false `created` event, old row marked dirty | `Error: issue ... already exists`, exit 1, nothing written |
| `--id` plus `--parent` | `Error: cannot specify both --id and --parent flags` | succeeds when the ID's parent is that parent; errors before any write when it is not |
| `--parent` after JSONL import into a fresh database | counters empty, allocates 1, collides with imported `.1` | scans imported children, allocates above them |
| `--parent` after an explicit-ID child was created and then deleted | floor never rose for that child, so its number is handed out again and collides with the copy in git history | the insert raised the floor when the child was written, so the number is never reissued |
| `bd create --type epic --parent <non-epic>` | issue created, then `Warning: failed to add parent-child dependency ... parent cannot depend on child` | the direction check runs inside the transaction, so the create fails and writes nothing |
| Import renaming an issue whose target ID already exists | old row deleted, new row silently not written, import reports success | the create returns the collision, the importer's existing branch compares content hashes and completes the rename |

The floor is raised where the row is written, and three functions write issue rows: `insertIssue`, `insertIssues`, and `upsertIssueInTx` (`multirepo.go:253`), which multi-repo hydration uses on every store open when a multi-repo config exists, including for the primary repository (`multirepo.go:34`, `store.go:206`). All three raise the floor; naming only the first two would leave hydration able to write a child whose number is later reissued.

Two rules cover the orderings that exist:

1. Writing a child raises its parent's floor to that child's number. `insertIssue` does it per row; `insertIssues` and the hydration pass group by parent and pass each group's **maximum** parsed suffix, because passing any other member under-raises the floor and reopens the hole for the higher-numbered child.
2. Writing an issue that already has children seeds its own floor from them. This is the orphan-first ordering: a child imported before its parent gets no floor, because the guarded statement skips an absent parent, and if that child's row later disappears before any allocation, nothing remembers its number. One extra query per batch closes it, not one per row: after the rows land, select the children of the IDs just written and upsert a floor for each parent found. The allocator's scan is what repairs databases written before this change, where floors already sit below their children; farmplanner's floor of 33 against 43 children is that case.

Resurrecting a missing parent happens once, through `tryResurrectParentWithConn(ctx, conn, parentID)`, never the exported `TryResurrectParent`. The transaction's `CreateIssue` (`transaction.go:166-176`) already resurrects the parent chain for a hierarchical ID, so the child-create helper checks the parent once before allocation and does not run a second resurrection around the insert. The exported one takes a fresh pooled connection (`resurrection.go:28-36`), which would deadlock against the write lock this transaction already holds and surface as a busy timeout; `queries.go:180` and `transaction.go:168` use the conn-scoped variant for that reason.

One statement does the raise, and its shape is dictated by two facts measured on this database rather than assumed:

```sql
INSERT INTO child_counters (parent_id, last_child)
  SELECT ?, ? WHERE EXISTS (SELECT 1 FROM issues WHERE id = ?)
  ON CONFLICT(parent_id) DO UPDATE SET last_child = max(last_child, excluded.last_child);
```

The `WHERE EXISTS` guard is there because `child_counters.parent_id` carries `REFERENCES issues(id)` (`migrations/014_child_counters_table.go:17-21`) and every connection runs with `foreign_keys(ON)` (`store.go:99-113`). Orphan children are legal on import: `OrphanAllow` is the default (`internal/storage/sqlite/config.go:60,68`) and `EnsureIDs` admits a hierarchical child whose parent is absent (`ids.go:210-236`). Without the guard, importing one such child raises a floor for a parent that does not exist, the foreign key aborts the write, and the whole depth batch fails. The guard makes that a no-op instead. `max(last_child, excluded.last_child)` is what makes the floor monotonic: a lower number leaves it untouched.

Two error strings are fixed by this plan, because the ship gate, the importer and the agents reading them all depend on the wording:

- A collision on insert: `issue <id> already exists: UNIQUE constraint failed: issues.id`, built by wrapping the driver error with `%w`. The ID is named for the human, and the driver's text survives in the message because `internal/importer/importer.go:472` recognises a concurrent rename by substring, through `sqlite.IsUniqueConstraintError` (`util.go:48`). Replacing the text instead of wrapping it would leave that branch dead and turn a concurrent clone's rename into an aborted import with the old row already deleted.
- An explicit ID that is not a child of the named parent: `explicit ID <id> is not a child of parent <parent>`.

The repo carries two functions for that test: exported `IsUniqueConstraintError` (`util.go:48`, matching `UNIQUE constraint failed`) and unexported `isUniqueConstraintError` (`issues.go:14`, matching that plus `constraint failed: UNIQUE`). The unexported one goes and both of its uses move to the exported one, which is unchanged: the driver's message is `sqlite3: constraint failed: UNIQUE constraint failed: ...`, so the second substring can never be the only match.

`GetNextChildID` leaves the storage interface. Nothing can allocate a child number without inserting the row, which is what made the two-call sequence unsafe.

## Validated Assumptions

Measured against `bd 0.34.0` built from this worktree, on a scratch database, before writing this version. Commands and outputs are in `manager_log.md`.

| Assumption | Probe | Result |
|------------|-------|--------|
| An unguarded floor upsert breaks orphan imports | `INSERT INTO child_counters` for a parent with no issue row, `foreign_keys(ON)` | `Error: stepping, FOREIGN KEY constraint failed (19)` |
| The `WHERE EXISTS` guard turns that into a no-op and still works for a real parent | The statement above, once with a missing parent and once with a present one | `rows=0` for the missing parent; `floor=5` for the present one |
| The floor never moves backwards | Raise to 5, then ask for 3, then ask for 9 | `after_lower=5`, `after_higher=9` |
| Orphan children are reachable by default, so the guard is not theoretical | `bd import` of a lone `probe-ghost.7` into a fresh database | `Import complete: 1 created`, `orphan_row=1` |
| A plain `go build -o` cannot satisfy ship gate 10 | Build without ldflags, read `bd version --json` | Keys `branch`, `build`, `version`; no `commit` |
| The ldflags build does satisfy it | Build with `-X main.Commit=$(git rev-parse HEAD)` | `commit_key= True ad55e1298e9d`, matching HEAD |

## Changes

### Phase 1: The code change — allocation, floor maintenance, strict insert, one child-create operation, both callers — Gate: `go build ./... && go vet ./...` clean; `go test ./... -short` passes

Removing `GetNextChildID` from the interface breaks every caller at once, so storage, both callers, and the tests that name the removed symbols move together. Splitting them produces a phase whose build gate cannot pass.

| File | Change | Why |
|------|--------|-----|
| `internal/storage/sqlite/child_issues.go` (new) | `allocateChildNumber(ctx, conn, parentID) (int, error)` reading floor-plus-scan and writing nothing; `raiseChildFloor(ctx, conn, parentID, n)` running the guarded monotonic statement from the Design section, the only writer of the floor; `(s *SQLiteStorage) CreateChildIssue(ctx, parentID, issue, actor) error` wrapping `RunInTransaction`; `createChildOnConn(...)` doing the work on a transaction-owned connection and resurrecting a missing parent through `tryResurrectParentWithConn`, never the exported `TryResurrectParent` | One file owns hierarchical child creation; the conn-level helper is what keeps allocation and insertion in one transaction |
| `internal/storage/sqlite/hash_ids.go` | Delete the file: `getNextChildNumber` and `GetNextChildID` move into `child_issues.go` in their new form, and the only other content is a comment pointing at `ids.go` | Leaving an empty file named for hash IDs that holds child-counter code is how the misleading name survived this long |
| `internal/storage/sqlite/issues.go` | `insertIssue` and `insertIssues`: `INSERT OR IGNORE` becomes `INSERT`; on a UNIQUE violation return `fmt.Errorf("issue %s already exists: %w", id, err)`; after a successful insert call `raiseChildFloor` for a hierarchical ID, `insertIssues` grouping by parent and passing that group's **maximum** parsed suffix, so a batch holding `p.5` and `p.3` leaves the floor at 5 rather than 3; `insertIssues` additionally runs one query for children of the IDs it just wrote and seeds a floor for each parent found, which is the orphan-first ordering from the Design section; both honour the non-negative int64 parsing policy; delete the unexported `isUniqueConstraintError` and point its former uses at the exported `IsUniqueConstraintError` in `util.go` | A create that wrote no row must not return nil; the floor is only trustworthy if the insert maintains it; two helpers for one test is one too many |
| `internal/storage/sqlite/multirepo.go` | `upsertIssueInTx` maintains the floor the same way: after the hydration pass has written its rows, raise each parent's floor to the maximum child written, and seed a floor for any written ID that already has children, inside the transaction the pass already holds | Hydration runs on every store open under a multi-repo config (`:34`, `store.go:206`) and writes issue rows without touching either insert helper, so a floor rule stated only for `issues.go` would leave this path able to write a child whose number is later reissued |
| `internal/storage/sqlite/queries.go` | Implement `RenameCounterPrefix` (`:992-996`), today a no-op whose comment claims hash IDs do not use counters: rewrite each `child_counters.parent_id` that carries the old prefix, in the transaction `rename_prefix.go:436` already calls it from | A rename otherwise strands every floor under the old parent ID, and the scan cannot rebuild one whose highest child was purged, which is exactly the number the floor exists to remember |
| `internal/storage/sqlite/batch_ops.go` | Delete `checkForExistingIDs` (`:105-153`) and its call at `:265` | `EnsureIDs` already rejects both in-batch duplicates and IDs present in the database, before any write (`ids.go:186-199`), and it runs immediately before this call on the same slice; the strict insert closes the remaining gap with an error that names the ID, so the third mechanism has no case left of its own |
| `internal/storage/storage.go` | Remove `GetNextChildID` from the `Storage` interface; add `CreateChildIssue(ctx, parentID string, issue *types.Issue, actor string) error` | Allocation without insertion stops being reachable |
| `internal/storage/memory/memory.go` | Replace `GetNextChildID` with `CreateChildIssue`: under one lock, compute `max(counter, highest existing child)+1`, insert (its duplicate check already errors), record the event, add the parent-child edge, raise the counter | `bd --no-db` runs on this implementation (`cmd/bd/nodb.go:44`), so it is a production create path, not a test double |
| `cmd/bd/create.go` | Delete the both-flags fatal at `:151-154` and the pre-allocation block at `:159-173`; when `parentID != ""` call `store.CreateChildIssue(ctx, parentID, issue, actor)` instead of `store.CreateIssue` at `:307`; delete the post-create parent-edge block at `:311-321` | The parent edge commits with the row instead of after it |
| `internal/rpc/server_issues_epics.go` | Delete the both-flags rejection at `:118-123` and the `GetNextChildID` call at `:140-152`; call `store.CreateChildIssue` when `createArgs.Parent != ""`; delete the post-create parent-edge block at `:226-238` | The daemon path and the direct path stop diverging |
| `internal/storage/sqlite/child_id_test.go` | Rewrite the eight `TestGetNextChildID*` cases against `CreateChildIssue`, keeping parent-resurrection and depth-limit coverage | The method they exercise is gone; the behaviors it guarded are not |
| `internal/storage/sqlite/child_counters_test.go` | Rewrite `TestGetNextChildNumber*` against `allocateChildNumber`, and drop its uniqueness and progression assertions (`:168-182`): ten reads with no insert between them correctly return the same number, so assert instead that repeated reads leave the floor unchanged. Uniqueness coverage moves to the `CreateChildIssue` concurrency test | `allocateChildNumber` writes nothing, so the old test's guarantee is wrong to state |
| `internal/storage/sqlite/child_counters_migration_test.go` | Rewrite the `GetNextChildID` call at `:126` against `CreateChildIssue` | Same package, will not compile otherwise |
| `internal/storage/memory/ready_blocked_nodb_test.go` | Rewrite the `GetNextChildID` calls at `:26-39` against `CreateChildIssue` | Same |
| `internal/rpc/rpc_test.go` | Flip the both-flags case at `:633-648` to assert success when the explicit ID's parent matches, and an error when it does not | The rejection it asserts is the behavior being removed |

### Phase 2: Tests for the new behaviors — Gate: `go test ./... -short` passes

| File | Change | Why |
|------|--------|-----|
| `internal/storage/sqlite/child_issues_test.go` (new) | Stale floor allocates above existing children (the farmplanner shape: floor 33, children to 43); absent counter row after import allocates above imported children; closed and tombstoned children still occupy their numbers; explicit duplicate ID returns an error naming the ID and leaves the old row, its title and its created-event count unchanged; an explicit ID whose parent mismatches errors before any write; issue row and parent edge commit together and neither survives a failed create; a create rejected by the parent-child direction check leaves no row; an explicit-ID child whose row is deleted outright does not have its number reissued (`bd delete --hard` keeps a tombstone row by design, `cmd/bd/delete.go:513-523`, so the test deletes the row directly to reach the state the floor is the only defence against); concurrent `CreateChildIssue` from N goroutines yields N distinct IDs; a parent ID containing `_` is matched literally, not as a LIKE wildcard | These are the behaviors the bug violated or that the fix newly guarantees |
| `internal/storage/sqlite/batch_floor_test.go` (new) | Insert children `.5` then `.3` in one batch, assert the floor is 5; delete `.5` outright; assert the next child is `.6`. Insert a parent that already has children and assert its floor is seeded. Hydrate a repo through `HydrateFromMultiRepo`, delete the highest child's row, and assert its number is not reissued | Nothing else in the plan proves the batch and hydration paths raise the **maximum** floor rather than the last one, or any floor at all: allocation after an import can pass entirely through the scan, so omitting batch floor maintenance would leave every other test green |
| `internal/storage/memory/child_issues_test.go` (new) | `CreateChildIssue` on memory storage allocates above children loaded from JSONL, and rejects a duplicate ID | `bd --no-db` reaches this path and no test covers it today |
| `internal/importer/importer_test.go` | Two cases. First, the rename race: the target must be absent at the lookup (`:350`) and present at the insert (`:470`), which a test-only SQLite trigger on the delete of the old row creates deterministically; assert the import completes and both rows survive. Second, an import of a child whose parent is absent still succeeds under the default orphan mode and writes no counter row | The first is the only path that reaches the newly live branch at `:472`; a target that already exists returns earlier at `:348-364` and never gets there. The second is the foreign-key regression the `WHERE EXISTS` guard prevents, and it would break every orphan-carrying import |

### Phase 3: Docs and deploy — Gate: ship gate table passes; `bd version --json` from `~/.local/bin/bd` reports the merged commit

| File | Change | Why |
|------|--------|-----|
| `CODEMAP.md` | Add the `internal/storage/sqlite/child_issues.go` row; drop `hash_ids.go` if listed | Same commit as the file move, per repo rules |
| `CONTEXT.md` | Add the gotcha: the counter is a floor maintained by `insertIssue` and guarded by `WHERE EXISTS` because of the foreign key; child creation goes through `CreateChildIssue`; inserts are strict, so nothing may reintroduce `INSERT OR IGNORE` on `issues`; the collision error must keep the driver text because the importer matches on it | These are the traps: the one that produced the bug, and the two the fix introduces |
| deploy | `go build -ldflags="-X main.Commit=$(git rev-parse HEAD) -X main.Branch=$(git rev-parse --abbrev-ref HEAD)" -o "$HOME/.local/libexec/bd-real.new" ./cmd/bd/ && mv "$HOME/.local/libexec/bd-real.new" "$HOME/.local/libexec/bd-real"` | `~/.local/bin/bd` is a shell wrapper around `~/.local/libexec/bd-real`; the ldflags are what put a `commit` key in `bd version --json` at all, matching `Makefile:41`; writing the file in place fails with `text file busy` while any agent's `bd` is running, and a rename leaves running processes on the old inode |

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `internal/importer/importer.go` | Yes | No source change, and orphan-carrying imports keep working because the floor statement skips a parent with no row (measured; see Validated Assumptions). Two behaviors do shift underneath it: the batch path still pre-checks IDs, while the rename recovery branch at `:472` stops being dead code because `CreateIssue` now returns the collision it tests for, which is why the error keeps the driver text it matches on. Phase 2 tests that case. The removed counter sync at `:804` stays removed: the insert maintains the floor now |
| `internal/storage/sqlite/resurrection.go` | Yes | `tryResurrectParentWithConn` returns early when the parent exists (`:41-48`), so its tombstone `insertIssue` never hits an occupied ID |
| `internal/storage/sqlite/migrations/014_child_counters_table.go` | Yes | No schema change; the table's shape and cascade are unchanged, only what is written into it and how often |
| `internal/storage/sqlite/transaction.go` | Yes | Its `CreateIssues` has no non-test callers (`batch_ops.go:188` is a doc comment) and inherits strict insert with no live behavior to preserve; its `CreateIssue` and `AddDependency` are reused unchanged by the new child-create path |
| `cmd/bd/doctor/database.go` | Yes | Reads `child_counters` only to probe schema shape (`:178`) |
| `cmd/bd/doctor/integrity.go` | Yes | Uses the table's existence as a hash-ID indicator (`:508-515`); shape and existence are unchanged |

## Not in Scope

- **A migration that resyncs every stale counter.** Allocation repairs each parent on first use and the insert keeps it repaired; a backfill would fix today's rows and add a migration to maintain forever.
- **Making `--deps` and label failures fatal** (`cmd/bd/create.go:323-370` warns). Real, but a separate change to a different contract. The parent edge moves inside the transaction because it defines the issue's place in the tree; a blocked-by edge does not.
- **Repairing the farmplanner database** (four stray edges and two false `created` events on `farmplanner-r3yq.8.32` and `.33`). Handed to that item's manager on 2026-09-09; both issues are closed, so nothing is blocked.

## Model Assignment

| Work | Lane | Tier | Effort | Why |
|------|------|------|--------|-----|
| Manager | auto | Opus | medium | Ben's assignment on 2026-09-17; the roster's default work manager |
| Builder (all three phases) | opencode | Spark | xhigh | Ben directed the build to Muse Spark 1.3 on the OpenCode Go route, `opencode-go/muse-spark-1.3-contributor`, not the Zen free tier. Dispatch: `OPENCODE_BUILD_MODELS=opencode-go/muse-spark-1.3-contributor OPENCODE_BUILD_EFFORT=xhigh skills/manager/codex-build.sh --opencode <worktree>`. xhigh is the top of the model's effort options |
| Build review | review | Opus | medium | Roster assigns per-bead and plan review to Opus |
| Final review | review | Opus | medium | Roster default; Fable only on request |

## Execution Handoff

Direct: a single builder works from this plan. The interface change breaks compilation until every caller moves, so the three phases are one atomic change that cannot be split into independently testable beads. The phase gates are the builder's checkpoints, not bead boundaries.

## Rollback

No schema migration, so no data rollback. To undo: `git revert` the merge commit on `main`, then rebuild the fleet binary with the Phase 3 deploy command and re-verify the reported commit. Old and new binaries interoperate on the same database, because the counter table's shape is unchanged and a floor written higher by the new binary is read normally by the old one (it would simply resume incrementing from there).

Partial rollback within Phase 1 is not possible: reverting the callers without the storage change leaves the build broken.

## Risks

| Risk | Mitigation |
|------|------------|
| Strict inserts surface a caller that relied on silent duplicate tolerance | The three `insertIssue` callers (`queries.go:197`, `resurrection.go:95`, `transaction.go:180`) and the two `insertIssues` callers (`transaction.go:283`, `batch_ops.go:92`) are known: resurrection inserts only when the parent row is absent, and the batch paths reject existing IDs before the insert. The full suite runs at every phase gate, and gate 9 is `go test ./... -short` |
| The LIKE scan misreads IDs containing `%` or `_` | The pattern escapes `%`, `_` and `\`, and the suffix is parsed in Go rather than trusted from the pattern; a test covers a parent whose prefix contains an underscore |
| Two agents creating under one parent at the same moment | Allocation and insert share one `BEGIN IMMEDIATE` transaction, which SQLite serializes across processes; the concurrency test asserts N distinct consecutive IDs from N goroutines |
| The scan cost grows with children per parent | The default case-insensitive LIKE does not use the primary-key index, so the scan reads the issues table once per child create; at fleet sizes (farmplanner has a few hundred issues, its largest epic 43 children) that is well under a millisecond, and the Go parse bounds the work after the scan to one parent's children |
| The floor write breaks orphan-carrying imports through the foreign key | Measured, not assumed: the unguarded statement returns `FOREIGN KEY constraint failed (19)` and the `WHERE EXISTS` form is a no-op for a missing parent. Phase 2 tests an orphan import end to end |
| Strict inserts turn the importer's dead rename branch live, changing import behavior | That branch is the intended recovery and it replaces silent deletion of the renamed issue; Phase 2 tests the case, and the error keeps the driver text the branch matches on |
| The epic direction check becomes fatal for `create --type epic --parent <non-epic>` | Stated in the before-and-after table and covered by a Phase 2 test; failing loudly is the point of the change, and the previous behavior left a warning nobody read |
| A future path writes issue rows without maintaining the floor | The three writers are named in the Design section and each has a test; the rule to check when adding a fourth is recorded in CONTEXT.md. This round found hydration, which three earlier rounds missed, so the CONTEXT.md entry names the symptom to grep for rather than the functions |
| Replacing the fleet binary while agents are running | The deploy renames a freshly built file into place, so running processes keep the old inode and no agent sees a partially written binary |

## Reach

No graphical surface: the only affordance is a command-line flag pair, so the modality columns describe how each is reached in a terminal.

| Affordance | Desktop mouse | Keyboard | Touch | Assistive tech |
|------------|---------------|----------|-------|----------------|
| `bd create --id X --parent P` (previously refused, now accepted when X is a child of P) | Not applicable: no pointer surface | Typed like any flag; `bd create --help` lists both, and neither is positional | Not applicable: no touch surface | Errors and results go to stdout and stderr as plain text a screen reader reads in order; no colour-only or symbol-only signal |

## Verification

**Tests:**
- `go test ./... -short` passes
- `go vet ./...` is clean
- The pre-change proof is the ship gate run against the installed `bd 0.34.0 (02e0ab0fe)` binary, which reproduces the collision; the new tests cannot run against pre-change source because the functions they call do not exist there

**E2E verification:**
Build the binary, create a scratch repo whose counter lags behind its children exactly as farmplanner's did, and run the two commands from the incident. The old binary printed existing closed IDs with exit 0; the new one must create new issues with the next free numbers, and a duplicate explicit ID must exit nonzero having written nothing. The ship gate below is that scenario, executed.

**Ship Gate** (proof of behavior; the /work gate compiles and executes this table and its command blocks):

| # | Requirement | Check | Expected |
|---|-------------|-------|----------|
| 1 | Creating a child under a parent gives the next free number, not an existing one | Build a repo with children 1-4, then `create --parent` | `repro-abc.5` |
| 2 | That create actually wrote the issue it reported | Read the title back for the ID the create printed | `New bug` |
| 3 | Creating with an ID that already exists fails | `create --id` naming an existing child, capturing its message | `already exists` |
| 3a | Creating with an ID that already exists fails | The same create's exit status | `rc=1` |
| 4 | The failed duplicate create left the old issue untouched | Read the old child's title and created-event count after the failed create | `Old child 2 events=1` |
| 5 | Naming an ID and its parent together works | `create --id repro-abc.9 --parent repro-abc` | `repro-abc.9` |
| 6 | Naming an ID whose parent is a different issue fails | `create --id repro-abc.10 --parent repro-abc.1` | `is not a child of` |
| 7 | A database rebuilt from JSONL allocates above its imported children | Export a repo with children 1-2 into a fresh repo, import, then `create --parent` | `repro-imp.3` |
| 8 | Concurrent creates under one parent never share an ID | 8 parallel `create --parent` runs, compare printed IDs against distinct printed IDs | `unique=yes` |
| 9 | The whole suite passes | `go test ./... -short` from the module root, reading its exit status rather than its output text | `failures=0` |
| 10 | The fleet binary runs this code | `bd version --json` commit is a prefix of the built HEAD | `commit-match` |
| 11 | A number is never reissued after its issue's row is gone | Create the next child by explicit ID, delete its row outright, then `create --parent` | `reused=no` |
| 12 | A create whose parent edge is rejected writes nothing | `create --type epic --parent <a task>`, comparing issue counts before and after | `epic-rejected` |
| 13 | A database written before this change, whose floor sits below its children, still allocates above them | Force the floor back to 1 with `sqlite3`, then `create --parent` | `repaired=yes` |

```bash gate=1
set -e
cd "$(git rev-parse --show-toplevel)"
go build -o /tmp/bd-w6 ./cmd/bd/
D=$(mktemp -d /tmp/bd-w6-gate.XXXXXX); cd "$D"
git init -q .
/tmp/bd-w6 --no-daemon --db "$D/.beads/beads.db" init --quiet --prefix repro --skip-hooks --skip-merge-driver
bdx() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$D/.beads/beads.db" "$@"; }
bdx create --silent --id repro-abc --type epic --title 'Parent epic' --description scratch
bdx create --silent --parent repro-abc --title 'Old child 1' --description scratch
for n in 2 3 4; do bdx create --silent --id repro-abc.$n --title "Old child $n" --description scratch; bdx close repro-abc.$n; done
sqlite3 "$D/.beads/beads.db" "SELECT 'floor=' || last_child FROM child_counters;"
bdx create --silent --type bug --priority 1 --parent repro-abc --deps repro-abc --title 'New bug' --description scratch
```

```bash gate=2
D=$(ls -dt /tmp/bd-w6-gate.* | head -1)
sqlite3 "$D/.beads/beads.db" "SELECT title FROM issues WHERE id='repro-abc.5';"
```

```bash gate=3
D=$(ls -dt /tmp/bd-w6-gate.* | head -1)
bdx() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$D/.beads/beads.db" "$@"; }
out=$(bdx create --silent --id repro-abc.2 --title 'Should fail' --description scratch 2>&1); rc=$?
printf 'rc=%s\n' "$rc" > "$D/dup-rc"
echo "$out" | tail -1
```

```bash gate=3a
D=$(ls -dt /tmp/bd-w6-gate.* | head -1)
cat "$D/dup-rc"
```

```bash gate=4
D=$(ls -dt /tmp/bd-w6-gate.* | head -1)
sqlite3 "$D/.beads/beads.db" "SELECT title || ' events=' || (SELECT count(*) FROM events WHERE issue_id='repro-abc.2' AND event_type='created') FROM issues WHERE id='repro-abc.2';"
```

```bash gate=5
D=$(ls -dt /tmp/bd-w6-gate.* | head -1)
bdx() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$D/.beads/beads.db" "$@"; }
bdx create --silent --id repro-abc.9 --parent repro-abc --title 'Explicit child' --description scratch
```

```bash gate=6
D=$(ls -dt /tmp/bd-w6-gate.* | head -1)
bdx() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$D/.beads/beads.db" "$@"; }
bdx create --silent --id repro-abc.10 --parent repro-abc.1 --title 'Wrong parent' --description scratch 2>&1 | tail -1
```

```bash gate=7
set -e
S=$(mktemp -d /tmp/bd-w6-src.XXXXXX); I=$(mktemp -d /tmp/bd-w6-imp.XXXXXX); X=$(mktemp /tmp/bd-w6-export.XXXXXX)
cd "$S" && git init -q .
/tmp/bd-w6 --no-daemon --db "$S/.beads/beads.db" init --quiet --prefix repro --skip-hooks --skip-merge-driver
bds() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$S/.beads/beads.db" "$@"; }
bds create --silent --id repro-imp --type epic --title 'Import epic' --description scratch
bds create --silent --id repro-imp.1 --title 'Imported child 1' --description scratch
bds create --silent --id repro-imp.2 --title 'Imported child 2' --description scratch
bds export -o "$X"
cd "$I" && git init -q .
/tmp/bd-w6 --no-daemon --db "$I/.beads/beads.db" init --quiet --prefix repro --skip-hooks --skip-merge-driver
bdi() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$I/.beads/beads.db" "$@"; }
bdi import -i "$X"
sqlite3 "$I/.beads/beads.db" "SELECT 'counter_rows=' || count(*) FROM child_counters;"
bdi create --silent --parent repro-imp --title 'After import' --description scratch
```

```bash gate=8
D=$(ls -dt /tmp/bd-w6-gate.* | head -1)
DB="$D/.beads/beads.db"; C=$(mktemp /tmp/bd-w6-conc.XXXXXX)
for i in 1 2 3 4 5 6 7 8; do
  ( /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$DB" create --silent --parent repro-abc --title "Concurrent $i" --description scratch >> "$C" 2>/dev/null ) &
done
wait
total=$(grep -c . "$C" || true)
uniq=$(sort -u "$C" | grep -c . || true)
echo "created=$total distinct=$uniq"
if [ "$total" = 8 ] && [ "$total" = "$uniq" ]; then echo "unique=yes"; else echo "unique=no"; fi
```

```bash gate=9
cd "$(git rev-parse --show-toplevel)"
test -f go.mod || { echo "failures=no-module-root"; exit 0; }
L=$(mktemp /tmp/bd-w6-test.XXXXXX)
go test ./... -short > "$L" 2>&1; rc=$?
tail -5 "$L"
echo "failures=$rc"
```

```bash gate=10
cd "$(git rev-parse --show-toplevel)"
BD_COMMIT=$(~/.local/bin/bd version --json | python3 -c 'import json,sys; print(json.load(sys.stdin).get("commit",""))')
test -n "$BD_COMMIT" && git rev-parse HEAD | grep -q "^$BD_COMMIT" && echo commit-match
```

```bash gate=11
D=$(ls -dt /tmp/bd-w6-gate.* | head -1); DB="$D/.beads/beads.db"
bdx() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$DB" "$@"; }
H=$(sqlite3 "$DB" "SELECT max(CAST(substr(id, length('repro-abc.')+1) AS INTEGER)) FROM issues WHERE id LIKE 'repro-abc.%' AND id NOT LIKE 'repro-abc.%.%';")
N=$((H+1))
bdx create --silent --id "repro-abc.$N" --title 'Doomed child' --description scratch
sqlite3 "$DB" "DELETE FROM issues WHERE id='repro-abc.$N';"
NEW=$(bdx create --silent --parent repro-abc --title 'After hard delete' --description scratch)
echo "highest=$H deleted=repro-abc.$N next=$NEW"
if [ "$NEW" = "repro-abc.$N" ]; then echo "reused=yes"; else echo "reused=no"; fi
```

```bash gate=12
D=$(ls -dt /tmp/bd-w6-gate.* | head -1); DB="$D/.beads/beads.db"
bdx() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$DB" "$@"; }
before=$(sqlite3 "$DB" "SELECT count(*) FROM issues;")
bdx create --silent --type epic --parent repro-abc.1 --title 'Epic under a task' --description scratch > /dev/null 2>&1; rc=$?
after=$(sqlite3 "$DB" "SELECT count(*) FROM issues;")
echo "rc=$rc before=$before after=$after"
if [ "$rc" -ne 0 ] && [ "$before" = "$after" ]; then echo "epic-rejected"; else echo "epic-created"; fi
```

```bash gate=13
D=$(ls -dt /tmp/bd-w6-gate.* | head -1); DB="$D/.beads/beads.db"
bdx() { /tmp/bd-w6 --no-daemon --no-auto-import --no-auto-flush --db "$DB" "$@"; }
H=$(sqlite3 "$DB" "SELECT max(CAST(substr(id, length('repro-abc.')+1) AS INTEGER)) FROM issues WHERE id LIKE 'repro-abc.%' AND id NOT LIKE 'repro-abc.%.%';")
sqlite3 "$DB" "UPDATE child_counters SET last_child=1 WHERE parent_id='repro-abc';"
NEW=$(bdx create --silent --parent repro-abc --title 'After forced stale floor' --description scratch)
echo "highest=$H forced_floor=1 next=$NEW"
if [ "$NEW" = "repro-abc.$((H+1))" ]; then echo "repaired=yes"; else echo "repaired=no"; fi
```
