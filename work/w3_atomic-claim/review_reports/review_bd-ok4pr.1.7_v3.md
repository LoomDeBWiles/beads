VERDICT: FIX findings=1
| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|
| 1 | low | internal/storage/memory/memory_test.go:1455 | The doc comment opens with `TestUpdateIssueRejectedLeaveIssueUntouched` while the function six lines below is `TestUpdateIssueRejectedLeavesIssueUntouched`, so the comment does not attach to the identifier `go doc` and lint resolve and a reader grepping the name in the comment finds no such test. | Change the first word of the comment to `TestUpdateIssueRejectedLeavesIssueUntouched`. |
