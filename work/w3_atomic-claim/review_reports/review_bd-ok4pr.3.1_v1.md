VERDICT: FIX findings=1

| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|
| R1 | med | internal/importer/importer.go:381-385 | The `handleRename` collision map is the only one of the three lease mappings that no test reaches: deleting its `"claim_expires_at": incoming.ClaimExpiresAt` entry leaves `go test ./internal/importer/... ./cmd/bd/` fully green, so the rename-collision path can silently regress to the exact drop this bead exists to prevent (renamed row left `in_progress` with no expiry, unstealable on that clone). | Add a fourth `TestClaimRoundTrip` subtest that seeds the target store with a same-content, different-ID, older-`updated_at` copy of the claimed issue so the import routes through `handleRename`, then assert the lease matches. |
