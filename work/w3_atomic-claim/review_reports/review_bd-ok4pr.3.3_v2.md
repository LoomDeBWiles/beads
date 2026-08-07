VERDICT: FIX findings=1

| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|
| 1 | low | cmd/bd/export_test.go:533-535 | The status/assignee check in the rename subtest can never fail, because `ComputeContentHash` (internal/types/types.go:86,92) folds Status and Assignee into the hash, so the twin must already carry `in_progress`/`agent-a` to match the incoming row and route through `handleRename` at all — the assertion is seeded true and advertises coverage of the map's status/assignee entries that the subtest does not have. | Drop the copied assertion, or replace it with a check that bites here, such as asserting the squatter row at `held.ID` is untouched and that the incoming ID did not gain the lease. |
