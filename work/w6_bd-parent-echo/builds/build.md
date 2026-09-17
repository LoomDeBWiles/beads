# Build: work/w6_bd-parent-echo/plan_v6.md
## Acceptance
| Criterion | Command | Log | Pass |
|-----------|---------|-----|------|
| 1 child gets next free number (`repro-abc.5`) | gate1 block: build, init prefix repro, parent + children 1-4, `create --parent` | [gate1.log](gate1.log) | Y |
| 2 create actually wrote the reported issue (`New bug`) | gate2 block: `SELECT title ... WHERE id='repro-abc.5'` | [gate2.log](gate2.log) | Y |
| 3 duplicate `--id` fails (`already exists`) | gate3 block: `create --id repro-abc.2`, tail message | [gate3.log](gate3.log) | Y |
| 3a duplicate `--id` exits nonzero (`rc=1`) | gate3a block: read saved rc | [gate3a.log](gate3a.log) | Y |
| 4 failed duplicate left old row untouched (`Old child 2 events=1`) | gate4 block: title + created-event count | [gate4.log](gate4.log) | Y |
| 5 `--id` + `--parent` together succeed (`repro-abc.9`) | gate5 block: `create --id repro-abc.9 --parent repro-abc` | [gate5.log](gate5.log) | Y |
| 6 mismatched parent fails (`is not a child of`) | gate6 block: `create --id repro-abc.10 --parent repro-abc.1` | [gate6.log](gate6.log) | Y |
| 7 JSONL round-trip allocates above imports (`repro-imp.3`) | gate7 block: export, fresh repo, import, `create --parent` | [gate7.log](gate7.log) | Y |
| 8 concurrent creates never share an ID (`unique=yes`) | gate8 block: 8 parallel `create --parent` | [gate8.log](gate8.log) | Y |
| 9 whole suite passes (`failures=0`) | gate9 block: `go test ./... -short`, exit status | [gate9.log](gate9.log) | Y |
| 10 fleet binary runs this code (`commit-match`) | gate10 block: `bd version --json` commit vs HEAD | [gate10.log](gate10.log) | Y |
| 11 number never reissued after row gone (`reused=no`) | gate11 block: explicit create, outright DELETE, `create --parent` | [gate11.log](gate11.log) | Y |
| 12 rejected parent edge writes nothing (`epic-rejected`) | gate12 block: `create --type epic --parent <task>`, count compare | [gate12.log](gate12.log) | Y |
| 13 stale floor repaired (`repaired=yes`) | gate13 block: force floor to 1, `create --parent` | [gate13.log](gate13.log) | Y |
Pass: Y = proved by fresh evidence at revision 41c089c68 (gates 1-9, 11-13 run against that exact tree; gate 10 re-run after the Phase 3 deploy of that commit). No reused results. No PENDING rows.

## Divergences (same scope, no data risk)
- `RenameCounterPrefix`: the plan describes the call site (`cmd/bd/rename_prefix.go:436`) as a transaction, but it issues plain sequential store calls. Implemented as a direct `UPDATE child_counters ... WHERE parent_id LIKE ...` (no transaction parameter needed). Same behavior the plan specifies.
- Gate 3 message reads `Error: insert issue: issue repro-abc.2 already exists: sqlite3: constraint failed: ...` — the `insert issue:` prefix comes from the shared `wrapDBError` context all creates use. Satisfies `already exists` and keeps the driver text the importer matches on.
- Importer rename-race test: "both rows survive" is read as the rename completing with no silent loss — the import returns success, the target row carries the renamed content, the old ID is gone by rename design. A reading where the deleted old row also survives contradicts rename semantics.

## Tests
`go test ./... -short` passes whole-repo, `failures=0` (exit 0), `go vet ./...` clean. New coverage: `child_issues_test.go` (11 cases: stale/absent floor, closed+tombstone occupancy, loud duplicate, parent mismatch, row+edge atomicity, epic rejection, no-reissue, 8-way concurrency, LIKE escaping), `batch_floor_test.go` (batch-max floor, orphan-first seeding, hydration floor), `memory/child_issues_test.go` (3 cases), importer rename-race + orphan-no-counter tests. Rewrote `TestGetNextChildID*`/`TestGetNextChildNumber*`/migration/`ready_blocked_nodb`/RPC both-flags cases against the new API. During development, fixed two wrong expectations in my own rewritten allocate tests (nested parents are themselves children, so the scan correctly allocates above them) — no production-code change.
