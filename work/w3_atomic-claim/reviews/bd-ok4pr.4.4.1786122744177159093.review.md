VERDICT: FIX findings=2

| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|
| F1 | low | CONTEXT.md:41 | The cited range `internal/storage/sqlite/claim.go:130-136` points at the SQL text and error return of the UPDATE, while the code that actually produces the documented behavior — `claimed.ClaimExpiresAt = nil` followed by the `if lease != nil` override — is at claim.go:121-125, so a reader following the pointer lands on the bind site rather than the clearing decision the sentence describes. | Change the citation to `internal/storage/sqlite/claim.go:121-125`. |
| F2 | low | CONTEXT.md:41 | The new clause begins mid-note with a lowercase sentence, "removed. the caller decides \"free\" ...", breaking the capitalized-sentence style every other note in CONTEXT.md follows. | Capitalize to "The caller decides ...". |
