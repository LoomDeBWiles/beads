VERDICT: FIX findings=1
| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|
| F1 | low | cmd/bd/export_test.go:410-418 | The `TestClaimRoundTrip` doc comment says "Three import paths" while the function now has four subtests, and it names `insertIssue` as the fresh-store path when the fresh store actually writes through the batch `insertIssues` (`internal/storage/sqlite/issues.go:106-114`) — nulling the lease argument in `insertIssue` leaves the test green, nulling it in `insertIssues` fails it — so a reader trusting the comment edits the wrong function. | Say "Four import paths", name `insertIssues`, and add the sentence describing the rename-collision path (merge lands on the twin, squatter at the colliding ID untouched). |
