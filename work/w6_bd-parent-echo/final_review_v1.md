# Final Review v1: /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo

VERDICT: FINDINGS n=2

| ID | class | sev | file:line | trigger | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|---|---|
| F1 | neither | low | internal/storage/memory/memory.go:1609 | `bd --no-db create --parent patrol-x7k --id patrol-x7k.arm-ace` | The memory backend rejects an explicit child ID whose suffix is non-numeric with `explicit ID ... is not a child of parent ...`, while the SQLite backend accepts it (it validates with `IsHierarchicalID`, not the numeric suffix policy), so the same command succeeds on a database and fails under `--no-db`. | `child_issues.go:322-327` uses `IsHierarchicalID(issue.ID)` and only calls `parseChildSuffix` to decide whether to raise a floor; `memory.go:1609-1612` gates acceptance on `parseChildSuffixStrict`, which rejects any non-all-digit suffix (`memory.go:1533-1550`). Molecule-style children (`parent.childref`, `cmd/bd/template.go:476-505`) are exactly such IDs. | In `MemoryStorage.CreateChildIssue`, validate the explicit ID by parent match (prefix `parentID + "."` with no further dot) and keep `parseChildSuffixStrict` only for the counter raise. |
| F2 | neither | low | internal/storage/sqlite/issues.go:64-66, internal/storage/sqlite/child_issues.go:186 | any `bd create` (non-child creates included) on a large database; any batch insert or multi-repo hydration | Floor seeding is broader than the plan specified: `insertIssue` runs a `seedChildFloor` LIKE scan on every single-row insert (the plan put seeding on the batch path only, "one extra query per batch, not one per row"), and `maintainFloorsForIDs` seeds by scanning `SELECT id FROM issues` (all rows) rather than the children of the IDs just written, so each create and each batch pays a full issues-table read it did not before. | `issues.go:64-66` calls `seedChildFloor` unconditionally after every insert; `seedChildFloor` (`child_issues.go:133-166`) issues `SELECT id FROM issues WHERE id LIKE ?`, which the plan itself notes does not use the primary-key index (plan_v6.md:198); `maintainFloorsForIDs` (`child_issues.go:186`) selects every issue ID. Behaviour is correct — floors are only raised, never lowered — the cost is the only change. | Scope both seeds to `id LIKE <written id> . %` (and drop the per-row seed from `insertIssue`, keeping it on the batch path) so no path reads the whole issues table. |

## Verification Output

### 1. Plan User Intent — delivered

`bd create --parent` now allocates through `CreateChildIssue` in one `BEGIN IMMEDIATE` transaction (`internal/storage/sqlite/child_issues.go:222-236`, `transaction.go:41-55`), inserts strictly, and attaches the parent edge inside that transaction; both callers moved (`cmd/bd/create.go:285-292`, `internal/rpc/server_issues_epics.go:205-217`) and `GetNextChildID` is gone from the interface and from the whole tree (`grep -rn GetNextChildID --include=*.go` → no hits). The `--id` + `--parent` workaround is no longer needed: the flag pair is accepted when the ID names a child of that parent (gate5) and rejected before any write when it does not (gate6). The fleet binary at `~/.local/libexec/bd-real` reports the shipped commit:

```
$ ~/.local/bin/bd version --json
{ "branch": "w6_bd-parent-echo", "build": "dev",
  "commit": "571434a8f7f9f2edb6c7bdf56bb397b3d34bce8c", "version": "0.34.0" }
$ git rev-parse HEAD
571434a8f7f9f2edb6c7bdf56bb397b3d34bce8c
```

(This is gate 10's own command, read-only; I did not re-run the gate scenario. After `wt-merge.sh` the binary must be rebuilt at the merge commit per the plan's Phase 3 deploy line, and gate 10 re-checked then.)

### 2. Existing E2E evidence (not re-run)

The plan's E2E scenario *is* the ship gate (plan_v6.md:220-241). `build.md` records all 13 rows as Y "proved by fresh evidence at revision 41c089c68 (gates 1-9, 11-13 run against that exact tree; gate 10 re-run after the Phase 3 deploy of that commit). No reused results. No PENDING rows."

Reviewed revision check: `git diff --stat 41c089c68 571434a8f -- . ':!work'` is empty, and `git status --porcelain` shows no tracked modifications, so the evidence revision and the shipped code are identical. The second commit adds only `work/` logs.

Recorded results, read from the logs:

| Gate | Expected | Log content | Result |
|---|---|---|---|
| 1 | `repro-abc.5` | `gate1.log`: `floor=4` then `repro-abc.5` | pass (floor also shows explicit `--id` creates now raising it) |
| 2 | `New bug` | `gate2.log`: `New bug` | pass |
| 3 | `already exists` | `gate3.log`: `Error: insert issue: issue repro-abc.2 already exists: sqlite3: constraint failed: UNIQUE constraint failed: issues.id` | pass; driver text preserved |
| 3a | `rc=1` | `gate3a.log`: `rc=1` | pass |
| 4 | `Old child 2 events=1` | `gate4.log`: exact match | pass |
| 5 | `repro-abc.9` | `gate5.log`: `repro-abc.9` | pass |
| 6 | `is not a child of` | `gate6.log`: `Error: explicit ID repro-abc.10 is not a child of parent repro-abc.1` | pass |
| 7 | `repro-imp.3` | `gate7.log`: `Import complete: 3 created`, `counter_rows=1`, `repro-imp.3` | pass |
| 8 | `unique=yes` | `gate8.log`: `created=8 distinct=8`, `unique=yes` | pass |
| 9 | `failures=0` | `gate9.log`: package tail + `failures=0` | pass |
| 10 | `commit-match` | `gate10.log`: `commit-match` | pass at the time; re-verify after merge |
| 11 | `reused=no` | `gate11.log`: `highest=17 deleted=repro-abc.18 next=repro-abc.19`, `reused=no` | pass |
| 12 | `epic-rejected` | `gate12.log`: `rc=1 before=16 after=16`, `epic-rejected` | pass |
| 13 | `repaired=yes` | `gate13.log`: `highest=19 forced_floor=1 next=repro-abc.20`, `repaired=yes` | pass |

Evidence gaps, none blocking:

- `ship_gate.json` carries `"residues": []` and `"revisions": []` — no gate revisions were recorded, and the file stores only the requirement/command table, no results. The pass record lives solely in `build.md` plus the 14 `gate*.log` files, which are internally consistent with the expectations in `ship_gate.json` and `plan_v6.md`.
- Every gate ran with `--no-daemon`, so the daemon/RPC child path has no E2E evidence. It is covered by unit tests instead: `internal/rpc/rpc_test.go:630-690` now drives a real daemon client through `Create` with matching ID+parent (succeeds, returns `parent.ID + ".7"`) and mismatched parent (fails).
- No gate or test exercises `RenameCounterPrefix` end to end (`bd rename-prefix` with a live floor). The implementation matches its sibling `RenameDependencyPrefix` and the ordering is safe — `UpdateIssueID` renames issue rows with `PRAGMA foreign_keys = OFF` (`queries.go:875-891`), so floor rows survive un-cascaded and the later `UPDATE` re-points them at IDs that exist — but this is inference from the code, not measured.

### 3. Verification commands + full suite (run now, worktree root)

```
$ go build ./... && go vet ./... && go test ./... -short
ok  	github.com/steveyegge/beads	0.142s
ok  	github.com/steveyegge/beads/cmd/bd	22.124s
ok  	github.com/steveyegge/beads/cmd/bd/doctor	1.580s
ok  	github.com/steveyegge/beads/cmd/bd/doctor/fix	0.799s
ok  	github.com/steveyegge/beads/cmd/bd/setup	0.013s
ok  	github.com/steveyegge/beads/internal/audit	0.004s
ok  	github.com/steveyegge/beads/internal/autoimport	0.011s
ok  	github.com/steveyegge/beads/internal/beads	0.153s
ok  	github.com/steveyegge/beads/internal/compact	0.004s
ok  	github.com/steveyegge/beads/internal/config	0.016s
ok  	github.com/steveyegge/beads/internal/configfile	0.003s
ok  	github.com/steveyegge/beads/internal/daemon	0.120s
ok  	github.com/steveyegge/beads/internal/debug	0.002s
ok  	github.com/steveyegge/beads/internal/export	0.114s
ok  	github.com/steveyegge/beads/internal/git	0.648s
ok  	github.com/steveyegge/beads/internal/hooks	0.109s
ok  	github.com/steveyegge/beads/internal/idgen	0.002s
ok  	github.com/steveyegge/beads/internal/importer	1.920s
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.071s
ok  	github.com/steveyegge/beads/internal/linear	0.005s
ok  	github.com/steveyegge/beads/internal/lockfile	0.004s
ok  	github.com/steveyegge/beads/internal/merge	0.004s
ok  	github.com/steveyegge/beads/internal/molecules	0.200s
ok  	github.com/steveyegge/beads/internal/routing	0.002s
ok  	github.com/steveyegge/beads/internal/rpc	4.211s
?   	github.com/steveyegge/beads/internal/storage	[no test files]
ok  	github.com/steveyegge/beads/internal/storage/memory	0.003s
ok  	github.com/steveyegge/beads/internal/storage/sqlite	25.100s
?   	github.com/steveyegge/beads/internal/storage/sqlite/migrations	[no test files]
ok  	github.com/steveyegge/beads/internal/syncbranch	0.133s
ok  	github.com/steveyegge/beads/internal/testutil	0.002s
?   	github.com/steveyegge/beads/internal/testutil/fixtures	[no test files]
ok  	github.com/steveyegge/beads/internal/types	0.004s
?   	github.com/steveyegge/beads/internal/ui	[no test files]
ok  	github.com/steveyegge/beads/internal/util	0.002s
ok  	github.com/steveyegge/beads/internal/utils	0.004s
ok  	github.com/steveyegge/beads/internal/validation	0.002s
EXIT=0
```

`go build` and `go vet` produced no output (clean); 33 packages pass, 0 failures.

### 4. Integration and named invariants

- **Allocation is a pure read.** `allocateChildNumber` (`child_issues.go:68-108`) runs only `SELECT last_child ...` and `SELECT id FROM issues WHERE id LIKE ? ESCAPE '\'`, computes `max(floor, maxExisting) + 1`, and refuses at `math.MaxInt64` instead of overflowing. No write. Grandchildren (`p.1.2`) and non-numeric suffixes are skipped by `parseChildSuffix` (`:32-49`); closed and tombstoned rows count because the scan does not filter status.
- **`raiseChildFloor` is the only floor writer and carries the guard.** `grep -rn child_counters --include=*.go internal cmd` shows exactly one `INSERT INTO child_counters` (`child_issues.go:120-131`, with `WHERE EXISTS (SELECT 1 FROM issues WHERE id = ?)` and `ON CONFLICT ... max(last_child, excluded.last_child)`) and one other write, `RenameCounterPrefix`'s prefix `UPDATE` (`queries.go:995-1002`), which re-points rows rather than setting a floor value. `getNextChildNumber`'s old `last_child = last_child + 1` is gone with `hash_ids.go`.
- **All three row writers maintain the floor.** `insertIssue` raises per row (`issues.go:58-63`); `insertIssues` calls `maintainFloorsForIDs` after the rows land, which groups by parent and passes each group's **maximum** parsed suffix (`child_issues.go:173-184`) — a batch of `p.5`, `p.3` leaves the floor at 5, asserted by `TestBatchFloor_RaisesToMaximum`; `upsertIssueInTx` raises per hydrated row (`multirepo.go:330-338`) and `importJSONLFile` seeds afterwards on the same `*sql.Tx` (`multirepo.go:200-206`), covered by `TestBatchFloor_HydrationMaintainsFloor`. Orphan-first ordering is closed by `seedChildFloor` / the seed pass, covered by `TestBatchFloor_SeedsParentFromExistingChildren` and `TestImportOrphanChildWritesNoCounterRow`.
- **Inserts are strict and the collision text survives.** Both helpers use plain `INSERT` and return `issue %s already exists: %w` (`issues.go:56-60`, `:122-126`); the unexported `isUniqueConstraintError` is deleted and both uses moved to the exported `IsUniqueConstraintError`, which is what `internal/importer/importer.go:471` matches on. gate3.log confirms the driver substring `UNIQUE constraint failed: issues.id` reaches the user-visible message.
- **Child creation resurrects only through the conn-scoped helper.** `createChildOnConn` calls `s.tryResurrectParentWithConn(ctx, conn, parentID)` once, before allocation (`child_issues.go:303-312`), and never `TryResurrectParent`; `insertIssue` has no resurrection of its own. Resurrection behaviour is still covered (`child_id_test.go:190-380`).
- **Call signatures and schema.** `Storage` now declares `CreateChildIssue(ctx, parentID, issue, actor) error` and both implementations satisfy it; `go build ./...` finds no stale caller. No migration touched; `child_counters` keeps `parent_id TEXT PRIMARY KEY … REFERENCES issues(id) ON DELETE CASCADE` and the schema probes in `doctor/database.go:178` and `schema_probe.go:33` still match. `CreateChildIssue` runs under `RunInTransaction` → `beginImmediateWithRetry` (`transaction.go:52`), which is what the concurrency guarantee rests on; gate 8 and `TestCreateChildIssue_Concurrent` both hold. `checkForExistingIDs` removal is safe: `EnsureIDs` still runs immediately before `bulkInsertIssues` in `CreateIssuesWithFullOptions`.
- **No other production path allocates a child number.** `fmt.Sprintf("%s.%d", parent, n)` appears only in `child_issues.go:330`, `memory.go:1608`, the unused `types.GenerateChildID` (test-only callers), and `cmd/bd/migrate_hash_ids.go:263` (one-shot ID remap, not an allocator). Molecule bonding creates named children (`parent.childref`) through `CreateIssue`; those raise no floor by design and are skipped by the scan, consistent with the plan's numeric policy — see F1 for the one place the two backends disagree about accepting them.

### 5. The three recorded divergences

1. **`RenameCounterPrefix` as a direct `UPDATE` rather than a transaction parameter** — correct reading of the call site: `cmd/bd/rename_prefix.go:428-444` issues plain sequential store calls (`UpdateIssueID` per issue, then `RenameDependencyPrefix`, then `RenameCounterPrefix`), and the shipped implementation mirrors `RenameDependencyPrefix` exactly, including `s.db.ExecContext`. Ordering is safe because `UpdateIssueID` renames with foreign keys OFF, so floor rows are neither cascaded away nor blocked, and by the time the floors are re-pointed the new issue rows exist. No promised behaviour changes. Untested end to end (noted above).
2. **`insert issue:` prefix on the gate-3 message** — comes from `wrapDBError("insert issue", err)` in `createChildOnConn`/`CreateIssue`, which is the repo's existing convention. The plan's two contractual requirements both hold: the ID is named for the human and the driver's `UNIQUE constraint failed` text survives for `importer.go:471`. No behaviour the plan promised changes.
3. **Rename-race test asserting the rename completes rather than "both rows survive"** — the builder's reading is right: `handleRename` deletes the old ID by design (`importer.go:462-466`), so a literal "both rows survive" would contradict rename semantics and is unachievable. What the plan actually promised — the create returns the collision, the recovery branch at `:472` fires, the import completes with no silent loss — is what `TestImportRenameRace_ConcurrentCloneCompletesRename` asserts (import succeeds, `Updated == 1`, `test-new` present with the renamed content, `test-old` gone). No promised behaviour changes.

Observation for the manager, not a finding: in the narrower case where the rename target exists with *different* content, the old row is already deleted when the now-strict `CreateIssue` returns, so the import aborts with the old row lost. That data loss predates this change (previously it happened silently and the import reported success); the change makes it loud. Out of this plan's scope, worth a follow-up issue.
