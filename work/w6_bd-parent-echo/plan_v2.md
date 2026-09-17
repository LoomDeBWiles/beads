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

The same silent insert costs data elsewhere. `internal/importer/importer.go:463-481` renames an issue by deleting the old ID and creating the new one in two separate transactions, then inspects the create's error to recognise a clone that already did the rename. Because `CreateIssue` never returns that error today, the branch is dead: when the target ID already exists, the delete commits, the create writes nothing, and the import reports success having removed the issue. Strict inserts wake that branch up, which is what it was written for.

## Key Insight

**The counter answers "what numbers have ever been used", which is not the same question as "what numbers are taken", and only the second one can be answered from the issues table.** Tombstones expire after 30 days (`internal/types/types.go:152`) and `bd delete` hard-deletes rows (`queries.go:1172`), so a number whose issue is gone from this database may still exist in git history or a peer's copy; reusing it would collide on the next sync. So the counter stays, demoted to a monotonic floor that only ever rises, and the issues table becomes the authority on what is taken. The floor earns its keep only if every path that creates a hierarchical ID raises it, so the raise belongs at the one choke point every create passes through, the insert itself; a floor that only `--parent` maintains protects exactly the numbers the scan would have found anyway. Forget this and you get one of two wrong designs: dropping the counter entirely (which reuses dead children's numbers), or keeping it authoritative and adding a repair migration (which fixes today's drift and lets tomorrow's import re-create it).

The second half is that no allocator can be trusted alone. Whatever number the allocator picks, the insert is the only place that knows whether the row was written, so the insert must be strict and the create must fail on collision from any door: `--parent`, `--id`, import, or a future one.

## Design

Allocation, insertion and the parent edge become one storage operation inside one `BEGIN IMMEDIATE` transaction:

```
CreateChildIssue(parentID, issue, actor)
  │
  ├─ RunInTransaction  (BEGIN IMMEDIATE — serializes writers across processes)
  │    ├─ parent exists?  ── no ──► TryResurrectParent from JSONL ── no ──► error
  │    ├─ depth(parent) < 3?  ── no ──► error
  │    ├─ issue.ID == ""  ──► allocate:  next = max(floor, highest existing child) + 1
  │    │                                 UPSERT child_counters.last_child = next
  │    │   issue.ID != ""  ──► validate: parent of issue.ID == parentID, else error
  │    ├─ INSERT INTO issues            (plain INSERT — UNIQUE violation aborts here)
  │    ├─ record created event, mark dirty
  │    └─ INSERT parent-child dependency
  └─ COMMIT
```

Allocation reads the floor and the existing children on the transaction's own connection:

```sql
SELECT last_child FROM child_counters WHERE parent_id = ?;         -- absent → 0
SELECT id FROM issues WHERE id LIKE ? ESCAPE '\';                  -- pattern: <escaped parent>.%
```

`%`, `_` and `\` in the parent ID are escaped into the pattern. Each returned ID is parsed in Go: strip `parentID + "."`, accept only an all-digit remainder with no further dot, and take the maximum. Closed issues and tombstones count; a suffix that does not parse as an int is skipped, because it is not a number this allocator could produce and the strict insert is the backstop either way. Then `next = max(floor, maxExisting) + 1`, written back with `INSERT INTO child_counters ... ON CONFLICT DO UPDATE SET last_child = next`.

Worked trace on the farmplanner data. Floor 31; children present `.3`, `.30`, `.31`, `.32`, `.33`, `.34`. Scan yields max 34. `next = max(31, 34) + 1 = 35`. Counter is written to 35. Insert of `farmplanner-r3yq.8.35` succeeds. Before this change the same input produced 32, an existing closed bug, and a silent no-op.

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

The floor is raised where the row is written. `insertIssue` parses a hierarchical ID and upserts its parent's floor to at least that number in the same transaction as the row; `insertIssues` groups the batch by parent and issues one upsert per parent. Every door then maintains the floor: `--parent`, `--id`, import, `mol bond`, template instantiation. The allocator's scan is what repairs databases written before this change, where floors already sit below their children; farmplanner's floor of 33 against 43 children is that case.

Two error strings are fixed by this plan, because the ship gate, the importer and the agents reading them all depend on the wording:

- A collision on insert: `issue <id> already exists: UNIQUE constraint failed: issues.id`, built by wrapping the driver error with `%w`. The ID is named for the human, and the driver's text survives in the message because `internal/importer/importer.go:472` recognises a concurrent rename by substring, through `sqlite.IsUniqueConstraintError` (`util.go:48`). Replacing the text instead of wrapping it would leave that branch dead and turn a concurrent clone's rename into an aborted import with the old row already deleted.
- An explicit ID that is not a child of the named parent: `explicit ID <id> is not a child of parent <parent>`.

The repo carries two functions for that test: exported `IsUniqueConstraintError` (`util.go:48`, matching `UNIQUE constraint failed`) and unexported `isUniqueConstraintError` (`issues.go:14`, matching that plus `constraint failed: UNIQUE`). The unexported one goes; the exported one takes both substrings and both call sites.

`GetNextChildID` leaves the storage interface. Nothing can allocate a child number without inserting the row, which is what made the two-call sequence unsafe.

## Changes

### Phase 1: Storage — allocation, floor maintenance, strict insert, one child-create operation — Gate: `go build ./... && go vet ./...` clean; `go test ./internal/storage/... -short` passes

| File | Change | Why |
|------|--------|-----|
| `internal/storage/sqlite/child_issues.go` (new) | `allocateChildNumber(ctx, conn, parentID) (int, error)` implementing floor-plus-scan; `raiseChildFloor(ctx, conn, parentID, n)` for the write-back; `(s *SQLiteStorage) CreateChildIssue(ctx, parentID, issue, actor) error` wrapping `RunInTransaction`; `createChildOnConn(...)` doing the work on a transaction-owned connection | One file owns hierarchical child creation; the conn-level helper is what keeps allocation and insertion in one transaction |
| `internal/storage/sqlite/hash_ids.go` | Delete the file: `getNextChildNumber` and `GetNextChildID` move into `child_issues.go` in their new form, and the only other content is a comment pointing at `ids.go` | Leaving an empty file named for hash IDs that holds child-counter code is how the misleading name survived this long |
| `internal/storage/sqlite/issues.go` | `insertIssue` and `insertIssues`: `INSERT OR IGNORE` becomes `INSERT`; on a UNIQUE violation return `fmt.Errorf("issue %s already exists: %w", id, err)`; after a successful insert raise the parent floor for a hierarchical ID (`insertIssues` groups by parent, one upsert each); delete the unexported `isUniqueConstraintError` in favour of the exported `IsUniqueConstraintError` in `util.go`, extended to both substrings | A create that wrote no row must not return nil; the floor is only trustworthy if the insert maintains it; two helpers for one test is one too many |
| `internal/storage/storage.go` | Remove `GetNextChildID` from the `Storage` interface; add `CreateChildIssue(ctx, parentID string, issue *types.Issue, actor string) error` | Allocation without insertion stops being reachable |
| `internal/storage/memory/memory.go` | Replace `GetNextChildID` with `CreateChildIssue`: under one lock, compute `max(counter, highest existing child)+1`, insert (its duplicate check already errors), record the event, add the parent-child edge, raise the counter | `bd --no-db` runs on this implementation (`cmd/bd/nodb.go:44`), so it is a production create path, not a test double |
| `internal/storage/sqlite/child_id_test.go` | Rewrite the eight `TestGetNextChildID*` cases against `CreateChildIssue`, keeping parent-resurrection and depth-limit coverage | The method they exercise is gone; the behaviors it guarded are not |
| `internal/storage/sqlite/child_counters_test.go` | Rewrite `TestGetNextChildNumber*` against `allocateChildNumber`, including the concurrency case | Same |
| `internal/storage/sqlite/child_counters_migration_test.go` | Rewrite the `GetNextChildID` call at `:126` against `CreateChildIssue` | Same package, will not compile otherwise |
| `internal/storage/memory/ready_blocked_nodb_test.go` | Rewrite the `GetNextChildID` calls at `:26-39` against `CreateChildIssue` | Same |

### Phase 2: Callers — CLI and RPC — Gate: `go build ./... && go vet ./...` clean; `go test ./cmd/... ./internal/rpc/... -short` passes

| File | Change | Why |
|------|--------|-----|
| `cmd/bd/create.go` | Delete the both-flags fatal at `:151-154` and the pre-allocation block at `:159-173`; when `parentID != ""` call `store.CreateChildIssue(ctx, parentID, issue, actor)` instead of `store.CreateIssue` at `:307`; delete the post-create parent-edge block at `:311-321` | The parent edge commits with the row instead of after it |
| `internal/rpc/server_issues_epics.go` | Delete the both-flags rejection at `:118-123` and the `GetNextChildID` call at `:140-152`; call `store.CreateChildIssue` when `createArgs.Parent != ""`; delete the post-create parent-edge block at `:226-238` | The daemon path and the direct path stop diverging |
| `internal/rpc/rpc_test.go` | Flip the both-flags case at `:633-648` to assert success when the explicit ID's parent matches, and an error when it does not | The rejection it asserts is the behavior being removed |

### Phase 3: Tests for the new behaviors — Gate: `go test ./... -short` passes

| File | Change | Why |
|------|--------|-----|
| `internal/storage/sqlite/child_issues_test.go` (new) | Stale floor allocates above existing children (the farmplanner shape: floor 33, children to 43); absent counter row after import allocates above imported children; closed and tombstoned children still occupy their numbers; explicit duplicate ID returns an error naming the ID and leaves the old row, its title and its created-event count unchanged; an explicit ID whose parent mismatches errors before any write; issue row and parent edge commit together and neither survives a failed create; a create rejected by the parent-child direction check leaves no row; an explicit-ID child that is created and then deleted does not have its number reissued; concurrent `CreateChildIssue` from N goroutines yields N distinct IDs; a parent ID containing `_` is matched literally, not as a LIKE wildcard | These are the behaviors the bug violated or that the fix newly guarantees |
| `internal/storage/memory/child_issues_test.go` (new) | `CreateChildIssue` on memory storage allocates above children loaded from JSONL, and rejects a duplicate ID | `bd --no-db` reaches this path and no test covers it today |
| `internal/importer/importer_test.go` | Add a rename case whose target ID already exists with identical content: the import completes and the issue survives | Today the old row is deleted and nothing is written; this is the latent loss strict inserts repair |

### Phase 4: Docs and deploy — Gate: ship gate table passes; `bd version --json` from `~/.local/bin/bd` reports the merged commit

| File | Change | Why |
|------|--------|-----|
| `CODEMAP.md` | Add the `internal/storage/sqlite/child_issues.go` row; drop `hash_ids.go` if listed | Same commit as the file move, per repo rules |
| `CONTEXT.md` | Add the gotcha: the counter is a floor maintained by `insertIssue`, never the authority; child creation goes through `CreateChildIssue`; inserts are strict, so nothing may reintroduce `INSERT OR IGNORE` on `issues`; the collision error must keep the driver text because the importer matches on it | This is the trap that produced the bug, plus the one the fix introduces |
| deploy | Build to a temp path beside the target and rename it into place: `go build -o "$HOME/.local/libexec/bd-real.new" ./cmd/bd/ && mv "$HOME/.local/libexec/bd-real.new" "$HOME/.local/libexec/bd-real"` | `~/.local/bin/bd` is a shell wrapper around `~/.local/libexec/bd-real`; writing the file in place fails with `text file busy` while any agent's `bd` is running, and a rename leaves running processes on the old inode |

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `internal/storage/sqlite/batch_ops.go` | Yes | `checkForExistingIDs` (`:105-153`) already rejects existing and in-batch duplicate IDs before insert, so the import path's behavior is unchanged by strict inserts; its error names the offending ID, which a raw UNIQUE violation does not |
| `internal/importer/importer.go` | Yes | No source change, but two behaviors shift underneath it: the batch path still pre-checks IDs, while the rename recovery branch at `:472` stops being dead code because `CreateIssue` now returns the collision it tests for, which is why the error keeps the driver text it matches on. Phase 3 tests that case. The removed counter sync at `:804` stays removed: the insert maintains the floor now |
| `internal/storage/sqlite/resurrection.go` | Yes | `tryResurrectParentWithConn` returns early when the parent exists (`:41-48`), so its tombstone `insertIssue` never hits an occupied ID |
| `internal/storage/sqlite/migrations/014_child_counters_table.go` | Yes | No schema change; the table's shape and cascade are unchanged, only what is written into it and how often |
| `internal/storage/sqlite/transaction.go` | Yes | Its `CreateIssues` has no non-test callers (`batch_ops.go:188` is a doc comment) and inherits strict insert with no live behavior to preserve; its `CreateIssue` and `AddDependency` are reused unchanged by the new child-create path |
| `cmd/bd/doctor/database.go` | Yes | Reads `child_counters` only to probe schema shape (`:178`) |
| `cmd/bd/doctor/integrity.go` | Yes | Uses the table's existence as a hash-ID indicator (`:508-515`); shape and existence are unchanged |

## Not in Scope

- **A migration that resyncs every stale counter.** Allocation repairs each parent on first use and the insert keeps it repaired; a backfill would fix today's rows and add a migration to maintain forever.
- **Renaming counter keys during `rename-prefix`** (`queries.go:992-996` is a no-op today). Out of scope because a floor whose key was left behind is rebuilt by the first allocation under the new name, and the scan covers the children that already exist.
- **Making `--deps` and label failures fatal** (`cmd/bd/create.go:323-370` warns). Real, but a separate change to a different contract. The parent edge moves inside the transaction because it defines the issue's place in the tree; a blocked-by edge does not.
- **Repairing the farmplanner database** (four stray edges and two false `created` events on `farmplanner-r3yq.8.32` and `.33`). Handed to that item's manager on 2026-09-09; both issues are closed, so nothing is blocked.

## Model Assignment

| Work | Lane | Tier | Effort | Why |
|------|------|------|--------|-----|
| Manager | auto | Fable | medium | Non-rote: a storage-interface change with a concurrency contract and a deploy |
| Builder (all four phases) | build | Opus | medium | Go storage-layer work with transaction semantics; roster assigns builders to Opus |
| Build review | review | Opus | medium | Roster assigns per-bead and plan review to Opus |
| Final review | review | Opus | medium | Roster default; Fable only on request |

## Execution Handoff

Direct: a single builder works from this plan. The interface change breaks compilation until every caller moves, so the four phases are one atomic change that cannot be split into independently testable beads. The phase gates are the builder's checkpoints, not bead boundaries.

## Rollback

No schema migration, so no data rollback. To undo: `git revert` the merge commit on `main`, then rebuild the fleet binary with the Phase 4 command and re-verify the reported commit. Old and new binaries interoperate on the same database, because the counter table's shape is unchanged and a floor written higher by the new binary is read normally by the old one (it would simply resume incrementing from there).

Partial rollback of Phase 2 alone is not possible: reverting the callers without the storage change leaves the build broken.

## Risks

| Risk | Mitigation |
|------|------------|
| Strict inserts surface a caller that relied on silent duplicate tolerance | The three `insertIssue` callers and the two `insertIssues` callers are enumerated in "Files NOT Affected"; the full suite runs at every phase gate, and gate 5 is `go test ./...` |
| The LIKE scan misreads IDs containing `%` or `_` | The pattern escapes `%`, `_` and `\`, and the suffix is parsed in Go rather than trusted from the pattern; a test covers a parent whose prefix contains an underscore |
| Two agents creating under one parent at the same moment | Allocation and insert share one `BEGIN IMMEDIATE` transaction, which SQLite serializes across processes; the concurrency test asserts N distinct consecutive IDs from N goroutines |
| The scan cost grows with children per parent | Bounded by children of one parent (farmplanner's largest epic has 43); the query is a prefix match on the primary key |
| Strict inserts turn the importer's dead rename branch live, changing import behavior | That branch is the intended recovery and it replaces silent deletion of the renamed issue; Phase 3 tests the case, and the error keeps the driver text the branch matches on |
| The epic direction check becomes fatal for `create --type epic --parent <non-epic>` | Stated in the before-and-after table and covered by a Phase 3 test; failing loudly is the point of the change, and the previous behavior left a warning nobody read |
| Replacing the fleet binary while agents are running | The deploy renames a freshly built file into place, so running processes keep the old inode and no agent sees a partially written binary |

## Reach

No graphical surface: the only affordance is a command-line flag pair, so the modality columns describe how each is reached in a terminal.

| Affordance | Desktop mouse | Keyboard | Touch | Assistive tech |
|------------|---------------|----------|-------|----------------|
| `bd create --id X --parent P` (previously refused, now accepted when X is a child of P) | Not applicable: no pointer surface | Typed like any flag; `bd create --help` lists both, and neither is positional | Not applicable: no touch surface | Errors and results go to stdout and stderr as plain text a screen reader reads in order; no colour-only or symbol-only signal |

## Verification

**Tests:**
- `go test ./... -short` — all pass
- `go vet ./...` — clean
- The pre-change proof is the ship gate run against the installed `bd 0.34.0 (02e0ab0fe)` binary, which reproduces the collision; the new tests cannot run against pre-change source because the functions they call do not exist there

**E2E verification:**
Build the binary, create a scratch repo whose counter lags behind its children exactly as farmplanner's did, and run the two commands from the incident. The old binary printed existing closed IDs with exit 0; the new one must create new issues with the next free numbers, and a duplicate explicit ID must exit nonzero having written nothing. The ship gate below is that scenario, executed.

**Ship Gate** (proof of behavior; the /work gate compiles and executes this table and its command blocks):

| # | Requirement | Check | Expected |
|---|-------------|-------|----------|
| 1 | Creating a child under a parent whose counter lags gives a new issue, not an existing one | Build a repo with floor 1 and children 1-4, then `create --parent` | `repro-abc.5` |
| 2 | That create actually wrote the issue it reported | Read the title back for the ID the create printed | `New bug` |
| 3 | Creating with an ID that already exists fails | `create --id` naming an existing child, capturing its message | `already exists` |
| 3a | Creating with an ID that already exists fails | The same create's exit status | `rc=1` |
| 4 | The failed duplicate create left the old issue untouched | Read the old child's title and created-event count after the failed create | `Old child 2 events=1` |
| 5 | Naming an ID and its parent together works | `create --id repro-abc.9 --parent repro-abc` | `repro-abc.9` |
| 6 | Naming an ID whose parent is a different issue fails | `create --id repro-abc.10 --parent repro-abc.1` | `is not a child of` |
| 7 | A database rebuilt from JSONL allocates above its imported children | Export a repo with children 1-2 into a fresh repo, import, then `create --parent` | `repro-imp.3` |
| 8 | Concurrent creates under one parent never share an ID | 8 parallel `create --parent` runs, compare printed IDs against distinct printed IDs | `unique=yes` |
| 9 | The whole suite passes | `go test ./... -short`, counting package failures | `failures=0` |
| 10 | The fleet binary runs this code | `bd version --json` commit is a prefix of the built HEAD | `commit-match` |
| 11 | A number is never reissued after its issue is hard-deleted | Create the next child by explicit ID, `delete --hard --force` it, then `create --parent` | `reused=no` |
| 12 | A create whose parent edge is rejected writes nothing | `create --type epic --parent <a task>`, comparing issue counts before and after | `epic-rejected` |

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
if [ "$total" -ge 2 ] && [ "$total" = "$uniq" ]; then echo "unique=yes"; else echo "unique=no"; fi
```

```bash gate=9
cd "$(git rev-parse --show-toplevel)"
L=$(mktemp /tmp/bd-w6-test.XXXXXX)
go test ./... -short > "$L" 2>&1 || true
echo "failures=$(grep -c '^FAIL' "$L")"
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
bdx delete "repro-abc.$N" --hard --force > /dev/null
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
