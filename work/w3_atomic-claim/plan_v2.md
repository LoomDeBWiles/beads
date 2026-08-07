# w3_atomic-claim: Atomic claim verb with owner lease for bd

> Make claiming a bead one preconditioned write instead of a read-then-unconditional-write race, and give claims an optional lease so stale-claim recovery becomes a preconditioned steal instead of a blind overwrite.

## What Changed from v1

| Finding | v1 said | v2 says | Why |
|---------|---------|---------|-----|
| F1a | Design: `--assignee` defaults to the actor helper | `--assignee` required; absent → usage error exit 1 | Two defaulted claimants resolve to the same actor, land in RENEW, both exit 0 — the race reborn |
| F1b | claim.go row carried the actor default | Row states required flag, no default | Same defect, Changes-table copy |
| F2a | Phase 3 had no importer row; round-trip = fresh store only | importer.go row (map `claim_expires_at` in both explicit update maps); round-trip also imports into an existing store | Import-update path drops unmapped fields silently; fresh-store test cannot see it |
| F2b | importer.go listed in Files NOT Affected | Row deleted | File is now affected (Phase 3) |
| F2c | E2E item 6 imported into a fresh store only | Fresh store AND existing store, lease identical in both | Exercise the update maps where the drop happens |
| F2d | allowedUpdateFields untouched | queries.go row admits `claim_expires_at` through `allowedUpdateFields` (:545, import only) | Import maps route through UpdateIssue validation |
| F3 | No memory-backend row | Phase 1 row: `internal/storage/memory/memory.go` implements `ClaimIssue` | Interface method without it breaks compilation of the `--no-db` backend |
| F4 | queries.go row claimed the INSERT column lists | Phase 1 row for `internal/storage/sqlite/issues.go` (insertIssue :44 / insertIssues :74); change rows are the authority, not the pinned grep | The INSERT lists live in issues.go; a bead scoped from the table would miss the file |
| F5a | Decision matrix had no empty-assignee cell | `in_progress ∧ assignee==""` → CLAIM | Legacy `update --status in_progress` rows have no owner; otherwise unclaimable during migration |
| F5b | claim_test matrix lacked the case | Empty-assignee-in_progress → claimed added | Test locks the cell |
| F6a | Merge driver drops fields "on true conflicts" | Drops on every rewritten both-live row (`Merge3Way` re-marshals from the modeled struct, merge.go:133-141) | Correct mechanism statement |
| F6b | Merge tests were conflict-only | Added no-conflict pass-through case retaining `assignee` + `claim_expires_at` | Conflict-only tests would miss a forgotten copy into the rebuilt result |

(Also under the F2a/F6b Changes binding: Phase 3's gate reworded to drop the `\|` regex characters, which the revision-delta table format cannot address.)

## User Intent

The user wants an atomic claim verb in our bd v0.34 fork (compare-and-swap on status, nonzero exit on a lost race) plus an owner/lease with expiry in the same verb, so that the fleet's one measured tracker defect (w828 decision) is removed at the root and the compensating machinery built around it (w29 liveness sweep mkdir-locks, w702 per-bead mkdir claim lock, w788 blind assignee overwrite on recovery) can be deleted by follow-up caller migrations. Chosen over migrating to beads_rust: br's license rider names Anthropic/OpenAI agents as Restricted Parties and applies to derivatives, and its adapter work-list is strictly larger than this patch.

## Problem

Claiming a bead today is `bd update <id> --status in_progress --assignee X`. `SQLiteStorage.UpdateIssue` (`internal/storage/sqlite/queries.go:637`) reads the issue outside any transaction, then runs an unconditional `UPDATE issues SET ... WHERE id = ?` (`queries.go:757-778`). There is no precondition, so two concurrent claimants both succeed. Reproduced (probe, 2026-08-07, sandbox store): two concurrent claims on open issue `cr-qpg` returned `A exit=0`, `B exit=0`, final assignee `agent-A` — B's acknowledged claim was silently lost. The fleet record shows this class costing real work: w702 review F-9 ("claiming remains a read-then-unconditional-write race"), w695 duplicate-builder dispatch, w813 two builders live on one bead, and three generations of compensating lock/sweep machinery with their own bug history (w29 F-1 false reset; w702 supervisor.16/.18 permanently-blocked bead).

A second defect compounds it: a dead claimant's bead stays `in_progress` forever, so w29 built a liveness sweep that guesses process death and resets beads, and w788's recovery path overwrites `assignee` blindly. Claims have no owner lease and no expiry, so recovery cannot be preconditioned.

## Key Insight

**The precondition must live inside the same SQLite transaction that writes, and the fork already owns the right primitive: `BEGIN IMMEDIATE` (`internal/storage/sqlite/transaction.go:29`).** A claim implemented as read → decide → write inside an IMMEDIATE transaction is serialized against every other write transaction, so exactly one claimant wins and the loser gets a distinct denial, not a false success. If you instead bolt a `--claim` flag onto the existing `UpdateIssue` map path, the pre-read stays outside the transaction and the race survives. Forgetting the secondary writes — `content_hash` refresh (hash covers assignee and status, `internal/types/types.go:67-88`), the `events` insert, the `dirty_issues` mark, and the blocked-cache invalidation on status change — silently breaks export freshness, audit, and `bd ready`.

## Design

One new verb, one new nullable column, one storage method, one RPC op.

```
bd claim <id> --assignee X (required) [--lease 30m] [--json]
  │
  ├─ daemon running ──► RPC OpClaim ──► store.ClaimIssue
  └─ direct mode    ──────────────────► store.ClaimIssue
                                          │ BEGIN IMMEDIATE
                                          │ SELECT status, assignee, claim_expires_at
                                          │ decide:
                                          │   open ─────────────────────────► CLAIM
                                          │   in_progress ∧ assignee==""  ──► CLAIM (legacy row, no owner)
                                          │   in_progress ∧ assignee==X ────► RENEW (refresh lease)
                                          │   in_progress ∧ lease expired ──► STEAL (event records prior holder)
                                          │   otherwise ───────────────────► DENY (no write)
                                          │ on win: UPDATE assignee, status, claim_expires_at,
                                          │         updated_at, content_hash; INSERT event;
                                          │         mark dirty; invalidate blocked cache
                                          └ COMMIT
```

Exit codes: `0` claimed/renewed/stolen, `3` denied (stderr names holder and expiry; `--json` emits the outcome object), `1` error (not found, closed, invalid args, DB failure). Exit 3 lets shell callers branch on "lost the race" without parsing JSON. `--assignee` is required; absent is a usage error (exit 1) — a defaulted actor would make two defaulted claimants resolve to the same assignee, land in the RENEW cell, and both exit 0, which is the race reborn. The `in_progress ∧ assignee==""` cell claims rather than denies: legacy `update --status in_progress` (no assignee) rows have no owner to protect, and denying them would make them permanently unclaimable through the verb during the migration window.

Lease: `--lease` takes a Go duration; sets `claim_expires_at = now + lease`. Absent `--lease` → `claim_expires_at = NULL` (never expires — current behavior preserved for human claims). Renewal is the same `bd claim` by the current holder: refreshes the lease from this call's `--lease` (or NULL if absent). Steal is automatic when a lease exists and has passed — that is the point of a lease; the stolen-from assignee is recorded in the event row.

Lease lifecycle: any status transition out of `in_progress` clears `claim_expires_at` (extend `manageClosedAt`'s pattern at `queries.go:592`; also `CloseIssue` at `queries.go:951` and its tx twin). A lease never blocks anything except a rival claim.

JSONL: `ClaimExpiresAt *time.Time` with `json:"claim_expires_at,omitempty"` on `types.Issue` round-trips through export automatically; the import update path needs explicit field mapping (Phase 3, importer.go). The git merge driver drops harder: `Merge3Way` re-marshals every rewritten both-live row from the modeled `merge.Issue` struct (`merge.go:133-141`), so unmodeled fields are dropped on all rewritten rows, not only true conflicts — `assignee` is already lost this way today (pre-existing defect, same root). Fix both: model `assignee` and `claim_expires_at` in `merge.Issue`/`mergeIssue`, merged as a pair by latest `updated_at` (they change together in a claim), cleared when the merged status is not `in_progress`.

Before/after for the callers this unblocks (migrated in follow-up items, not here):

| Today | After |
|-------|-------|
| supervisor mkdir claim-lock + revalidate + unconditional update (w702) | `bd claim <id> --assignee <run> --lease 45m`; exit 3 = someone else has it |
| w29 sweep: pgid liveness guess → reset to open | builder renews its lease; a dead builder's bead is stolen by the next `bd claim` |
| w788 recovery: overwrite assignee, hope | steal only fires on an expired lease; unexpired holder is never overwritten |

## Validated Assumptions

| Assumption | Evidence |
|------------|----------|
| The race is real and reproducible end to end | Probe 2026-08-07: concurrent `update --status in_progress --assignee {A,B}` both exit 0, one claim silently lost (manager_log.md, `race.sh`) |
| An IMMEDIATE tx serializes read-decide-write | `RunInTransaction` doc + impl, `internal/storage/sqlite/transaction.go:29-41`; busy_timeout set, `store.go:113` |
| A new migration auto-applies to all live stores | `RunMigrations` runs the full idempotent list at every DB open under `BEGIN EXCLUSIVE`, `migrations.go:107-150` |
| RowsAffected is reliable under the ncruces driver | 8 existing non-test call sites, e.g. `queries.go:840`, `dependencies.go:207` |
| A late-added column's full touch-point set is enumerable | `pinned` (migration 023) and `ephemeral` precedents; `grep -rn "pinned" internal/ cmd/` enumerates scan/insert/filter classes |
| Merge driver drops unmodeled fields on conflict | `merge.Issue` struct `merge.go:41-60`; `mergeIssue` rebuild `merge.go:542-608`; no `Assignee` anywhere in the file |

## Verified State

- Installed binary: `~/.local/bin/bd`, 2026-08-05, `bd version 0.34.0`; worktree builds clean (`go build ./cmd/bd/` → `BUILD_OK`).
- 6 live `bd daemon` processes on this host; daemon autostart is on by default (`cmd/bd/main.go:414-424`), so the CLI routes through RPC when a daemon holds the workspace.
- Stores: 17 project `.beads/` stores fleet-wide (w828 field scan, 8222 records).

## Changes

### Phase 1: Storage + migration — Gate: `go test ./internal/storage/... ./internal/types/...` passes; new race test proves exactly one winner

| File | Change | Why |
|------|--------|-----|
| `internal/storage/sqlite/migrations/027_claim_expires_at.go` | New: idempotent `ALTER TABLE issues ADD COLUMN claim_expires_at TIMESTAMP` (nullable), guarded by column-exists check like 023 | Lease storage; auto-applies at next open of each store |
| `internal/storage/sqlite/migrations.go` | Register `{"claim_expires_at_column", ...}` at list end | Runner executes it |
| `internal/types/types.go` | `ClaimExpiresAt *time.Time \`json:"claim_expires_at,omitempty"\`` on `Issue`; new `ClaimOutcome` type (`Outcome` enum claimed/renewed/stolen/denied, `Holder`, `HolderExpiry`, `Issue`) | JSONL round-trip; typed result for CLI/RPC |
| `internal/storage/storage.go` | Interface method `ClaimIssue(ctx, id, assignee string, lease *time.Duration, actor string) (*types.ClaimOutcome, error)` on Storage and Transaction | Single authority for claim semantics |
| `internal/storage/sqlite/claim.go` | New: `ClaimIssue` — IMMEDIATE tx; SELECT row; decide per Design; on win UPDATE (assignee, status, claim_expires_at, updated_at, content_hash) + event insert (steal event carries prior holder) + dirty mark + blocked-cache invalidation (status changes); DENY returns holder+expiry, writes nothing; closed/tombstone/not-found → error | The atomic core |
| `internal/storage/sqlite/queries.go` | `manageClaimExpiry`: status leaving `in_progress` appends `claim_expires_at = NULL` (mirror `manageClosedAt`); wire into `UpdateIssue` and `CloseIssue`; add `claim_expires_at` to `allowedUpdateFields` (:545, import path only — no CLI flag); add column to scanners and explicit SELECT lists in this file. The change-table rows, not the `pinned` grep, are the builder's authority (the grep spans ~30 files, most needing no change) | Lease lifecycle; field visible everywhere rows are read |
| `internal/storage/sqlite/issues.go` | Add `claim_expires_at` to both explicit INSERT column lists (`insertIssue` :44, `insertIssues` :74) | JSONL-import create path writes rows through these lists; omitting the column silently drops imported leases |
| `internal/storage/memory/memory.go` | Implement `ClaimIssue` with the same decision matrix under the existing mutex | Backs `--no-db` mode (`cmd/bd/nodb.go:44`); interface method without it breaks compilation |
| `internal/storage/sqlite/transaction.go` | Same scanner/column additions; `sqliteTxStorage.ClaimIssue` delegating to shared claim core; `CloseIssue` tx twin clears lease | Tx path parity |
| `internal/storage/sqlite/claim_test.go` | New: outcome matrix (open→claimed; held-unexpired→denied+no write; self→renewed; expired→stolen+event has prior holder; `in_progress` with empty assignee→claimed; closed→error; no-lease claim never expires); race test: N=8 goroutines claim one open issue, assert exactly 1 outcome=claimed, 7 denied, final assignee = winner | Proves the CAS |

### Phase 2: RPC + CLI — Gate: `go test ./cmd/bd/ ./internal/rpc/...` passes; scripttest covers exit 0/3/1 in both daemon and direct modes

| File | Change | Why |
|------|--------|-----|
| `internal/rpc/protocol.go` | `OpClaim = "claim"`; `ClaimArgs{ID, Assignee string; LeaseSeconds *int64}` | Wire format |
| `internal/rpc/server_issues_epics.go` | Handler: validate, call `store.ClaimIssue`, push `MutationUpdate` event on win (fires `on_update` hook, marks export dirty per existing pipeline) | Daemon path |
| `internal/rpc/client.go` | `Claim(args *ClaimArgs)` | CLI→daemon call |
| `cmd/bd/claim.go` | New cobra command `claim <id>` (GroupID issues): `--assignee` required (usage error, exit 1, when absent; no default), `--lease` duration, `--json`; `CheckReadonly("claim")`; partial-ID resolve like update; route daemon/direct; exit map per Design | The verb |
| `cmd/bd/main.go` | Register command | Discoverable |
| `cmd/bd/claim_test.go` + scripttest entries | New: exit-code matrix incl. denial output naming holder; `--json` object shape | CLI contract |

### Phase 3: JSONL round-trip + merge driver — Gate: `go test ./internal/merge/...` passes and the export/import round-trip tests in `cmd/bd` pass

| File | Change | Why |
|------|--------|-----|
| `internal/importer/importer.go` | Map `claim_expires_at` in both explicit update maps (:561-587, :656-682) | Import into an existing store applies changes through these maps; unmapped fields are silently dropped, leaving a synced clone `in_progress` with no expiry (never stealable there) |
| `cmd/bd/export_test.go` (or nearest round-trip test) | Round-trip test: claimed issue with lease → export → import into a fresh store AND into a store already holding the issue (pre-claim state) → `claim_expires_at` identical in both | The fresh-store path exercises only `insertIssue`; the existing-store path exercises the update maps where the drop actually happens |
| `internal/merge/merge.go` | Add `Assignee`, `ClaimExpiresAt` to `Issue`; in `mergeIssue` merge the pair by latest `updated_at`; clear both lease (and keep assignee) when merged status ≠ `in_progress` — lease only | Conflict-path merge currently drops assignee (pre-existing) and would drop the lease |
| `internal/merge/merge_test.go` | Conflict cases: both sides claim (latest updated_at wins, one holder survives); one side closes + one claims (closed wins per `mergeStatus`, lease cleared); no-conflict pass-through: issue changed on one side in an unrelated field retains `assignee` and `claim_expires_at` in merged output | Merge semantics locked by test; pass-through case catches a forgotten copy into `mergeIssue`'s rebuilt result |

### Phase 4: Docs + deploy — Gate: E2E script prints `E2E_PASS` against the built binary; installed `bd version` reports new HEAD; live daemons healthy

| File | Change | Why |
|------|--------|-----|
| `CODEMAP.md` | Row: `cmd/bd/claim.go` + `internal/storage/sqlite/claim.go` | Same-commit codemap rule |
| `CONTEXT.md` | Pattern note: "Claiming: always `bd claim`, never `update --status in_progress --assignee` — the update form is the unconditioned race this verb removed" | Stops regression to the racy idiom |
| `work/w3_atomic-claim/e2e_claim.sh` | New: literal E2E script (below) | Executed proof |

Deploy (AGENT, w2 precedent): build → run `e2e_claim.sh` against built binary in a scratch store → `go install ./cmd/bd/` → `bd daemon --stop-all` (documented lifecycle op, CONTEXT.md gotcha; daemons autostart on next invocation) → live verify: `~/.local/bin/bd version` shows new HEAD; one `bd claim` + release cycle on a scratch store; `bd --no-daemon info --json` exit 0 on the fleet store (read-only).

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `internal/jsonlpub/jsonlpub.go` | Yes | Claim writes flow through the existing dirty→Publish pipeline; freshness authority untouched (w2 invariant) |
| `cmd/bd/autoflush.go`, `internal/autoimport/*` | Yes | No new freshness decision; claim is an ordinary mutation to them |
| `cmd/bd/show.go` | Yes | No `--claim` flag added; update stays unconditioned by design — claim is the only preconditioned path |
| `internal/compact/compactor.go`, `internal/molecules/molecules.go`, `internal/syncbranch/syncbranch.go`, `internal/routing/routing.go` | Yes | No interaction with claim fields |

## Not in Scope

- Caller migrations (orchestrator supervisor claim path, w29 liveness sweep, errfix claim, WORK/BEADS docs in shared-docs) — cross-repo; named follow-up items in Execution Handoff.
- A `bd claim --release` verb — release is `bd update --status open` (clears lease via `manageClaimExpiry`); adding a second verb duplicates update.
- Doctor warnings for stale leases, lease metrics, config defaults for lease duration — no consumer yet.
- Cross-machine lease-clock skew handling — single-host fleet today; risk-table entry, not machinery.
- Upstream (steveyegge/beads) sync considerations — fork is frozen except our commits.

## Execution Handoff

Beads: ~6 tasks along phase boundaries — storage+migration core; claim tests (matrix+race); RPC+CLI; JSONL/merge; docs; deploy+E2E. Ordering: storage before RPC/CLI, everything before deploy. Multiple builders viable (Phase 1 vs Phase 3 are disjoint files).

Follow-up items to open after ship (not beads here): shared-docs item migrating w29 sweep + errfix + WORK.md claim idiom to `bd claim --lease`; orchestrator item replacing the supervisor mkdir claim-lock and claim-revalidation with `bd claim` and deleting supervisor.16/.18's lock machinery.

## Rollback

- Pre-deploy: revert the branch; nothing installed.
- Post-deploy: reinstall previous binary (`git checkout <prior HEAD> && go install ./cmd/bd/`; prior HEAD recorded in work_report), `bd daemon --stop-all`. The migration is additive-nullable: old binaries ignore the extra column, and SQLite tolerates unknown columns on scan-by-name; JSONL from new binaries carries an extra key old importers drop silently. No data migration to unwind.

## Risks

| Risk | Mitigation |
|------|------------|
| A caller keeps using the racy `update` idiom | CONTEXT.md pattern note + follow-up migration items; the verb can't force adoption |
| Busy-timeout contention on IMMEDIATE tx under heavy parallel claims | busy_timeout already configured (`store.go:113`); denial ≠ BUSY error; w828 measured 12-writer load with zero timeouts on this same storage engine |
| Merge-driver change alters conflict output for existing fields | Only `assignee` (currently dropped) and the new column are added; existing field rules untouched; tests lock both |
| Lease steal fires on a live-but-slow builder | Lease duration is the caller's contract (choose ≥ heartbeat interval); steal event records prior holder for audit; no-lease claims are never stolen |
| Old binary writes during rollout window | Old `update` path ignores lease (unconditioned, as today); claim vs old-update conflict is exactly today's race, no worse; window ends at daemon restart |

## Verification

**Tests:** `go test ./...` from the worktree — all pass (baseline: pass before Phase 1; `scripts/test.sh` honors `.test-skip`).

**E2E verification** (`work/w3_atomic-claim/e2e_claim.sh`, run against the built binary in a scratch store, prints `E2E_PASS`):
1. Init scratch store, create issue.
2. Two concurrent `bd claim <id> --assignee agent-{A,B}` → assert exactly one exit 0 and one exit 3, and `show --json` assignee equals the winner (the probe's failing scenario, now passing).
3. `bd claim <id> --assignee agent-C --lease 1s`; wait 2s; `bd claim <id> --assignee agent-D` → exit 0, outcome `stolen`, event lists agent-C.
4. Holder renews: `bd claim` again as agent-D → exit 0 outcome `renewed`.
5. `bd update <id> --status open` → `show --json` has no `claim_expires_at`.
6. Round-trip: export → import into a fresh store AND into a store already holding the issue (pre-claim state) → lease field identical in both.

**Proof of behavior** (builder must execute and paste output):

| # | Check | Command | Expected |
|---|------|---------|----------|
| 1 | CAS: one winner | e2e_claim.sh step 2 | `one exit=0, one exit=3` line printed by script |
| 2 | Race test | `go test ./internal/storage/sqlite/ -run TestClaimRace -count=5` | PASS ×5, exactly one winner each run |
| 3 | Denial writes nothing | `go test ./internal/storage/sqlite/ -run TestClaimDeniedNoWrite` | PASS (updated_at, events, dirty_issues unchanged) |
| 4 | Migration on existing store | open a pre-027 fixture DB with new binary; `PRAGMA table_info(issues)` via test | `claim_expires_at` present, data intact |
| 5 | Full suite | `go test ./...` | PASS |
| 6 | E2E | `bash work/w3_atomic-claim/e2e_claim.sh` | `E2E_PASS` |
| 7 | Deployed identity | `~/.local/bin/bd version` | reports new build |
