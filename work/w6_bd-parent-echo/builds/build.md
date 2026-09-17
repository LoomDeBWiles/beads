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

## Round 2

**Outcome: no code change. F1's premise is refuted by measured evidence — applying the prescribed fix would create the backend divergence it claims to close.**

The change made: none to production code or tests. `git status --porcelain -- . ':!work'` is empty and `git diff --stat HEAD -- . ':!work'` is empty; the tree is the shipped commit 571434a8f.

What F1 claims: SQLite `CreateChildIssue` accepts an explicit non-numeric child ID (`p.arm-ace` under parent `p`) because it "validates with `IsHierarchicalID`, not the numeric suffix policy", while memory rejects it via `parseChildSuffixStrict`.

What the code and both backends actually do (measured 2026-09-17 on a scratch repo with a binary built from this tree):

- `IsHierarchicalID` (`internal/storage/sqlite/ids.go:44-64`) IS a numeric-suffix policy: ids.go:56-60 requires every character after the last dot to be a digit. `IsHierarchicalID("repro-abc.arm-ace")` returns `(false, "")`, so `createChildOnConn` (`internal/storage/sqlite/child_issues.go:336-339`) rejects it — the review's "not the numeric suffix policy" reading contradicts the function body.
- SQLite, live: `create --silent --id repro-abc.arm-ace --parent repro-abc` → `Error: explicit ID repro-abc.arm-ace is not a child of parent repro-abc`, rc=1.
- Memory, live (temporary probe test, since removed): `CreateChildIssue` returns `explicit ID repro-abc.arm-ace is not a child of parent repro-abc` — the identical message.
- Molecule children (`parent.childref`, `cmd/bd/template.go:482-501`) never traverse `CreateChildIssue`: the bonded clone path calls `tx.CreateIssue` (`template.go:597`), which accepts such IDs on both backends (SQLite measured rc=0 for `--id repro-abc.arm-ace` without `--parent`; memory `CreateIssue` has no hierarchical check at all). So molecule bonding already behaves identically on both backends.

Why the prescribed fix was not applied: making memory accept `parentID + "." + <non-empty dotless non-numeric remainder>` while SQLite (unchanged, per the order) keeps rejecting it would make `bd create --id p.arm-ace --parent p` succeed under `--no-db` and fail against a database — manufacturing the exact divergence F1 complains about, and contradicting the plan's before-and-after row ("succeeds when the ID's parent is that parent": under SQLite's `IsHierarchicalID`, a non-numeric dotted ID has no parent, so "errors before any write" is the conforming behavior on both sides today).

Tests: `go build ./... && go vet ./...` clean; `go test ./... -short` passes whole-repo, exit status 0, 33 packages ok, 0 FAIL — log at `work/w6_bd-parent-echo/builds/r2_test.log`.

Gate blocks: none re-run as scenarios, all 13 rows carried forward at their round-1 revision (`work/w6_bd-parent-echo/builds/gate*.log`, code revision 41c089c68, binary commit 571434a8f). Reason: with zero production change no gate result could be altered; re-running the table would only re-prove an untouched tree. The gate-9 equivalent (full short suite) was run fresh above, and the gate-10 condition was re-verified read-only: `~/.local/bin/bd version --json` reports commit `571434a8f7f9f2edb6c7bdf56bb397b3d34bce8c`, equal to `git rev-parse HEAD` at the time of writing.

Deploy deviation (deliberate): step 4's redeploy was skipped. Its purpose is to keep the fleet binary at HEAD after a code change; there is no code change, the binary already runs the shipped commit, and rebuilding it at a work/-only commit would overwrite `gate10.log`'s valid evidence with a behavior-identical binary. `gate10.log` is therefore left intact. After `wt-merge.sh` the binary still must be rebuilt at the merge commit per the plan.

Exit status: FAILURE per the dispatch contract (the accepted finding could not be fixed because its defect does not exist) — reported, not claimed as success. Recommended manager follow-up: close F1 as contradicted, or re-scope it if backend parity for non-numeric `--id`+`--parent` (i.e. changing SQLite too) is actually desired; that would be a design change, not this bugfix.
