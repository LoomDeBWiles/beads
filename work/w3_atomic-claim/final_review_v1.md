# Final Review v1: work/w3_atomic-claim

VERDICT: FINDINGS n=2

| ID | class | sev | file:line | trigger | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|---|---|
| F1 | corruption | high | work/w3_atomic-claim/plan_v4.md:209 (rollback), :220 (risk row) | Post-deploy rollback as written: `go install` a pre-merge `bd`, then run any command (even a read) against a store the new binary already opened. | The documented rollback claims "old binaries ignore the extra column", but the old binary re-runs migration 022, which rebuilds `issues` from its frozen 28-column list: one old-binary read drops `claim_expires_at` and blanks `pinned` and `is_template` to 0 on every row, and the new binary's schema self-repair then silently restores the columns as NULL/0 so the loss leaves no trace. | Mixed-binary probe below: `NEW-BINARY STATE : mx-cw1\|holder-A\|1\|1\|2026-08-07T21:26:28...` → one `/tmp/bd-main list` → column `claim_expires_at` gone, `pinned is_template` remain but → after the next new-binary read `mx-cw1\|holder-A\|0\|0\|NULL`. | Correct the rollback line to state that reverting the binary destroys `pinned`, `is_template` and all leases, so a rollback must restore the store from `.beads/issues.jsonl` (or git) after reinstalling the old binary. |
| F2 | neither | med | internal/merge/merge.go:138 | A `git merge` on `.beads/issues.jsonl` where the same issue is live on both sides, so the row is rewritten by the merge driver. | Every merged row is re-marshaled from `merge.Issue`, which models only a subset of the schema, so fields it does not carry are dropped from the rewritten row — including `pinned` and `is_template`, the exact columns this item just stopped migration 022 from blanking. | `internal/merge/merge.go:138` `line, err := json.Marshal(issue)`; `merge.Issue` (merge.go:30-60) has no `design`, `acceptance_criteria`, `labels`, `pinned`, `is_template`, `external_ref`, `source_repo`, `close_reason`, `sender`, `ephemeral`. | Pre-existing and explicitly scoped out by the plan's risk row ("Only `assignee` ... and the new column are added; existing field rules untouched") — record as residual, no code change inside this item. |

Scope notes for the manager: F1 is not the absence of a deploy step; the deploy sequence (build → E2E → `go install` → `bd daemon --stop-all` → live verify) is sound as written and out of builder scope. F1 is that the *rollback* half of that procedure rests on a premise the probe disproves. Deploy forward is safe: the ledger plus the runtime-schema rewrite of 022 hold on a pre-ledger store (probe below), and the new `store.go` self-repair heals a store an old binary damaged, at the cost of the data in the dropped columns.

Checks 1 and 4 found nothing to raise. On Check 1 (User Intent), the shipped `bd claim` delivers CAS-on-status with exit 0/3/1, the full decision ladder (claim / legacy-row claim / renew / steal / deny-held / deny-status / error), the owner lease with expiry in the same verb, lease release on any transition out of `in_progress`, and identical behavior on the daemon and direct paths — all exercised end to end below. On Check 4 (integration seams), every explicit column list in `ready.go`, `dependencies.go`, `labels.go`, `issues.go`, `multirepo.go` and `transaction.go` carries `claim_expires_at`; the two `INSERT INTO issues` sites (`multirepo.go:300`, `022_drop_edge_columns.go:278`) both carry it; the signature change to `migrations.DB` is total (`go build ./...` and `go vet ./...` clean, and only `store.go` calls `RunMigrations`; `cmd/bd/migrate.go` only inspects); the tx `UpdateIssue` path reaches the new column through the generic SET builder plus `allowedUpdateFields`, and its omission from `applyUpdatesToIssue` is harmless because the lease is not in the content hash; `RunMigrations`' deferred cleanup unwinds in the correct LIFO order (ROLLBACK → `PRAGMA foreign_keys=ON` → `conn.Close`). `gofmt -l` flags 18 changed files, but 189 files are unformatted repo-wide and the same files are unformatted on `main`, so that is the repo's baseline, not this item's.

## Verification Output

### Check 2 — the plan's Verification commands

```
$ go build -o /tmp/bd-w3 ./cmd/bd/
BUILD_OK

$ go test ./internal/storage/sqlite/ -run TestClaimRace -count=5 -v
=== RUN   TestClaimRace
--- PASS: TestClaimRace (0.08s)
=== RUN   TestClaimRace
--- PASS: TestClaimRace (0.06s)
=== RUN   TestClaimRace
--- PASS: TestClaimRace (0.06s)
=== RUN   TestClaimRace
--- PASS: TestClaimRace (0.06s)
=== RUN   TestClaimRace
--- PASS: TestClaimRace (0.07s)
PASS
ok  	github.com/steveyegge/beads/internal/storage/sqlite	0.334s

$ go test ./internal/storage/sqlite/ -run TestClaimDeniedNoWrite -count=1 -v
--- PASS: TestClaimDeniedNoWrite (0.09s)
ok  	github.com/steveyegge/beads/internal/storage/sqlite	0.089s

$ go test ./internal/storage/sqlite/ -run 'TestClaimMigrationExistingStore|TestIssueColumnsSurviveReopen|TestMigrationLedger|TestDropEdgeColumns|TestOpenHealsDroppedColumn' -count=1 -v
--- PASS: TestClaimMigrationExistingStore (0.10s)
--- PASS: TestIssueColumnsSurviveReopen (0.01s)
--- PASS: TestMigrationLedgerRecordsEveryMigration (0.01s)
--- PASS: TestDropEdgeColumnsRebuildKeepsLaterColumns (0.02s)
--- PASS: TestDropEdgeColumnsQuotesIdentifiers (0.02s)
--- PASS: TestOpenHealsDroppedColumn (0.10s)
ok  	github.com/steveyegge/beads/internal/storage/sqlite	0.265s

$ go vet ./...
VET_EXIT=0
```

### Check 3 — E2E, against a binary rebuilt from this worktree

```
$ go build -o /tmp/bd-w3 ./cmd/bd/ && bash work/w3_atomic-claim/e2e_claim.sh
BUILD_OK
== step 1: init scratch store, create issue
  ok: store initialized, issue e2e-k1b created open
== step 2: two concurrent claims on one open issue
  agent-A exit=0, agent-B exit=3
one exit=0, one exit=3
  ok: stored assignee equals the winner = agent-A
== step 3: expired lease is stolen, event names the prior holder
  ok: agent-C claim exit = 0
  ok: agent-D claim exit = 0
  ok: outcome = stolen
  ok: displaced holder = agent-C
  ok: event previous_holder = agent-C
== step 4: the holder renews
  ok: agent-D renewal exit = 0
  ok: outcome = renewed
== step 5: leaving in_progress clears the lease
  ok: update exit = 0
  ok: status = open
  ok: claim_expires_at present = false
== step 6: lease round-trips into a fresh store and an existing one
  ok: pre-claim export exit = 0
  ok: agent-E claim exit = 0
  ok: post-claim export exit = 0
  ok: fresh-store import exit = 0
  ok: pre-claim import exit = 0
  ok: existing store starts pre-claim = false
  ok: existing-store import exit = 0
  ok: fresh store lease = 1786132932953959587
  ok: existing store lease = 1786132932953959587
E2E_PASS
E2E_EXIT=0
```

The winner alternates between runs (an earlier run had `agent-A exit=3, agent-B exit=0`), which is the point: the race is genuinely raced and exactly one side wins each time.

### Check 5 — full suite

```
$ go test -count=1 ./...
ok  	github.com/steveyegge/beads	0.159s
ok  	github.com/steveyegge/beads/cmd/bd	24.790s
ok  	github.com/steveyegge/beads/cmd/bd/doctor	1.975s
ok  	github.com/steveyegge/beads/cmd/bd/doctor/fix	0.924s
ok  	github.com/steveyegge/beads/cmd/bd/setup	0.022s
ok  	github.com/steveyegge/beads/internal/audit	0.007s
ok  	github.com/steveyegge/beads/internal/autoimport	0.037s
ok  	github.com/steveyegge/beads/internal/beads	0.234s
ok  	github.com/steveyegge/beads/internal/compact	0.005s
ok  	github.com/steveyegge/beads/internal/config	0.037s
ok  	github.com/steveyegge/beads/internal/configfile	0.005s
ok  	github.com/steveyegge/beads/internal/daemon	0.112s
ok  	github.com/steveyegge/beads/internal/debug	0.003s
ok  	github.com/steveyegge/beads/internal/export	0.127s
ok  	github.com/steveyegge/beads/internal/git	0.742s
ok  	github.com/steveyegge/beads/internal/hooks	1.225s
ok  	github.com/steveyegge/beads/internal/idgen	0.003s
ok  	github.com/steveyegge/beads/internal/importer	1.574s
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.082s
ok  	github.com/steveyegge/beads/internal/linear	0.003s
ok  	github.com/steveyegge/beads/internal/lockfile	0.016s
ok  	github.com/steveyegge/beads/internal/merge	0.007s
ok  	github.com/steveyegge/beads/internal/molecules	0.218s
ok  	github.com/steveyegge/beads/internal/routing	0.002s
ok  	github.com/steveyegge/beads/internal/rpc	4.268s
?   	github.com/steveyegge/beads/internal/storage	[no test files]
ok  	github.com/steveyegge/beads/internal/storage/memory	0.003s
ok  	github.com/steveyegge/beads/internal/storage/sqlite	28.309s
?   	github.com/steveyegge/beads/internal/storage/sqlite/migrations	[no test files]
ok  	github.com/steveyegge/beads/internal/syncbranch	0.448s
ok  	github.com/steveyegge/beads/internal/testutil	0.002s
?   	github.com/steveyegge/beads/internal/testutil/fixtures	[no test files]
ok  	github.com/steveyegge/beads/internal/types	0.005s
?   	github.com/steveyegge/beads/internal/ui	[no test files]
ok  	github.com/steveyegge/beads/internal/util	0.002s
ok  	github.com/steveyegge/beads/internal/utils	0.008s
ok  	github.com/steveyegge/beads/internal/validation	0.002s
```

Exit 0, no failures, no skips outside the four packages that have no tests.

### Supplementary — mixed-binary probe (the evidence behind F1)

`main`'s `bd` was built into `/tmp/bd-main` from `git archive main | tar -x` (no checkout), then pointed at a scratch store the new binary had already claimed and flagged.

```
NEW-BINARY STATE : mx-cw1|holder-A|1|1|2026-08-07T21:26:28.345649919+02:00
--- one read with the OLD (main) binary ---
AFTER OLD READ   : Error: in prepare, no such column: claim_expires_at
cols now         : pinned is_template
ledger rows      : 27
--- back to the NEW binary ---
AFTER NEW READ   : mx-cw1|holder-A|0|0|NULL
```

Columns are `id|assignee|pinned|is_template|claim_expires_at`. The old binary dropped `claim_expires_at` outright; the new binary's self-repair rebuilt it as NULL and `pinned`/`is_template` came back 0, so the flags and the lease are gone with no error surfaced.

### Supplementary — pre-ledger (live fleet) store survival, the RCA fix

A store was claimed with a 600s lease and flagged `pinned=1, is_template=1`, then `schema_migrations` was dropped to simulate a store that predates the ledger.

```
BEFORE: lg-0bs|holder-A|1|1|2026-08-07T21:18:20.459799847+02:00|2026-08-07T21:08:20.459799847+02:00
AFTER1: lg-0bs|holder-A|1|1|2026-08-07T21:18:20.459799847+02:00|2026-08-07T21:08:20.459799847+02:00
AFTER2: lg-0bs|holder-A|1|1|2026-08-07T21:18:20.459799847+02:00|2026-08-07T21:08:20.459799847+02:00
LEDGER ROWS: 27
INDEXES: idx_issues_assignee idx_issues_created_at idx_issues_deleted_at idx_issues_ephemeral idx_issues_external_ref idx_issues_external_ref_unique idx_issues_priority idx_issues_sender idx_issues_status idx_issues_status_priority idx_issues_updated_at sqlite_autoindex_issues_1
sentinel col after read: 1
```

Byte-identical across two reads, the ledger repopulates to 27, `idx_issues_ephemeral` and `idx_issues_sender` now persist at rest, and a hand-added sentinel column survives a read — the per-command table rebuild is gone.

### Supplementary — CLI exit-code matrix on a scratch store

- `blocked` → exit 3, `deny_reason=status`
- `deferred` → exit 3, `deny_reason=status`
- held by another owner → exit 3, `Denied: eg-snv is not claimable: held by owner1 (expires 2026-08-07T22:10:49+02:00) (deny_reason=held)`
- closed → exit 1, `issue eg-29o is closed and cannot be claimed`
- tombstone → exit 1
- unknown ID → exit 1
- `--assignee ""` and absent `--assignee` → exit 1, `--assignee is required and must not be empty`
- `bd close` sets `claim_expires_at` to NULL
- `bd list --json` and `bd ready --json` both return `claim_expires_at`

`go test ./cmd/bd/ -run Claim -v -count=1` passes throughout, including `TestClaimExitCodesInDirectMode` (1.23s), `TestClaimExitCodesInDaemonMode` (0.49s), `TestClaimRPCRejectsNonPositiveLease` (0.52s), and `TestClaimRoundTrip`'s four subtests (fresh-store import, import into a store already holding the pre-claim issue, lease-only renewal on an `external_ref` issue, rename collision keeping the lease on the surviving ID).

All stores created for this review lived in self-made `mktemp -d` scratch directories, since removed; no `.beads/` outside them and no installed binary was touched.
