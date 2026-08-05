# w2_stale-race: Kill the Beads false-staleness race

> Make Beads staleness mean "JSONL content differs from what the database recorded", record export metadata before revealing the file, and stop the daemon's self-triggering rewrite loop — so no healthy system can ever again fail with "Database out of sync with JSONL".

## What Changed from v1

| ID | v1 said | v2 says | Why |
|----|---------|---------|-----|
| R1 | Hash only when mtime is newer | Always hash; freshness = file sha256 ∈ {committed, pending}; mtime gate removed | ACCEPT — bd-v0y already proved the mtime shortcut unsafe (`integrity.go:118-121`); older-mtime divergence (restored old file) slipped through v1 |
| R2 | Single `jsonl_content_hash` written before rename | Two-phase protocol: `jsonl_pending_hash` before rename, promoted to `jsonl_content_hash` after; readers accept either | ACCEPT — v1's crash-between state made `validatePreExport` (`integrity.go:146-153`) refuse all future exports: a deadlock |
| R3 | Metadata failure → warn and rename anyway | Pending-hash write failure aborts publication (temp removed, no rename); only post-rename promotion is best-effort | ACCEPT — publishing bytes with no recorded hash recreates the false-stale state |
| R4a | Change C covered only `exportToJSONLWithStore`; autoflush/sync/RPC "not affected" | One shared publisher (`internal/jsonlpub`) used by all five JSONL writers; only store-less `nodb.go` exempt | ACCEPT — `autoflush.go` and `sync.go` reveal before recording; RPC writers (`server_export_import_auto.go:189,560`) never record at all |
| R4b | "2 source files + 2 test files" | ~6 source files + 3 test files; still one direct builder — the protocol lands atomically | ACCEPT — handoff claim updated to the real scope |
| R4c | 3 validated assumptions | + writer inventory, validatePreExport refusal, dirty_issues marked_at upsert — all probed | ACCEPT — new premises verified before acceptance |
| R4d | Risk table carried the autoflush-window residual | Residual eliminated by the unified publisher; new residuals: marked_at fidelity, lock contention, RPC store access | ACCEPT — risks realigned to the v2 design |
| R5 | No cross-process exclusion | Publish serialized by an `internal/lockfile` lock held from pending-write through promotion | ACCEPT — `operationMu` is process-local; daemon + direct writer can interleave rename and metadata from different snapshots |
| R6 | Clear dirty rows by pre-read ID set | Conditional clear: `DELETE WHERE issue_id=? AND marked_at=?` from an (id, marked_at) snapshot | ACCEPT — plain delete-by-ID silently strands a mutation made during export (`dirty.go:100-121`, upsert refreshes `marked_at`) |
| R7 | Tests check final hash + dirty count | Publisher state-machine tests with injected pending-write failure, injected rename failure, and mid-export mutation | ACCEPT — v1's tests passed even with the ordering broken |
| R8 | Probes 5/7 without `--no-daemon` | Probes run the exact binary by path with `--no-daemon` | ACCEPT — daemon-routed `info` skips the direct freshness gate; v1's check 5 passes on the broken binary |
| R9 | E2E "mutate an issue's status directly" | E2E strands dirty rows via `--no-daemon --no-auto-flush`, triggers watcher by same-bytes atomic replace, asserts one flush + zero dirty, re-triggers, asserts none | ACCEPT — v1's E2E could take the ordinary export path and prove nothing about the pre-import flush |
| R10 | Baseline tee'd into a nonexistent dir, no comparable lists | `mkdir -p` first; normalized failure lists (grep FAIL lines, sorted unique) for baseline and post-change | ACCEPT — no reproducible no-new-failures decision otherwise |
| R11 | Rollback/deploy `cp` over the installed binary, then stop daemons | Stop daemons first; install by write-to-temp + `mv` (rename); verify `bd version` | ACCEPT — `cp` onto a running executable fails ETXTBSY |

## User Intent

"Fix this" — the bug where the fleet w25 supervisor exited fatal on `bd info failed: Error: Database out of sync with JSONL` although nothing was actually out of sync. Fix our local beads fork only (no upstream PR); deploy is in scope: rebuild, install to `~/.local/bin/bd`, restart the running daemons. Locked in 2026-08-04 ("ok let's just lock in the plan to fix our version").

## Problem

Beads keeps two stores: SQLite (live) and `.beads/issues.jsonl` (git-synced mirror). Before answering, direct commands run a freshness gate that compares the JSONL **mtime** against a `last_import_time` timestamp stored in SQLite; file-newer means fail-stop. Three defects in the fork (tip `cd33f0f3`, the installed binary) turn that gate into a standing false alarm:

1. **Self-triggering rewrite loop.** The daemon's pre-import flush (`cmd/bd/daemon_sync.go:569-583`) exports dirty issues but never calls `ClearDirtyIssuesByID` — unlike the two other export paths (`cmd/bd/autoflush.go:707-720`, `cmd/bd/sync.go:1420`). The same dirty rows re-export on every watcher cycle; the export itself wakes the watcher. The w25 daemon log records `Flushing 2 dirty issues before import...` + `JSONL file created` once per second, indefinitely (`fleet/.beads/daemon-2026-07-24T20-42-34.692.log.gz` lines 666821-669254).
2. **File revealed before it is recorded.** `exportToJSONLWithStore` (`daemon_sync.go:31-147`) renames the new JSONL into place first; only afterwards does `updateExportMetadata` (`daemon_sync.go:280`) write `jsonl_content_hash` and `last_import_time`. Between rename and metadata write, the file is newer than the record.
3. **The gate measures clock order, not content.** `CheckStaleness` (`internal/autoimport/autoimport.go:264-306`) returns stale purely on `mtime > last_import_time`. Any reader landing in defect 2's window — reopened every second by defect 1 — fail-stops. The w25 supervisor's `bd info` hit it twice in one second (`enumerate_manifest` retries once, `process_development/conveyor.py:2413-2429`) and exited code 1 at `1784925592257778910`.

Full RCA with daemon-log line numbers: `~/projects/fleet/work/w25_inbox-queue/rca_stale_v1.md`.

## Key Insight

**A freshness predicate over two separately-published artifacts must compare content — and because a SQLite write and a filesystem rename cannot commit atomically, the record must span the reveal: a pending hash before the rename, a committed hash after it, and readers that accept either.** `mtime > last_import_time` is a proxy for "the bytes differ" that false-fires on same-content rewrites and misses divergence with an older mtime. Any single-key hash design lies in some window or crash state: recorded-after leaves the file ahead of the record (today's fatal); recorded-before leaves the record ahead of the file, and a crash there makes `validatePreExport` refuse every future export. With two keys, every reachable state — steady, mid-publish, crash on either side — hashes to committed or pending and reads fresh; only bytes written by someone else can diverge from both, which is the one condition that genuinely is stale. Forget this and any fix is armor: retries, allow-stale flags, and env quartets treat the alarm, not the lie.

## Verified State

Observed 2026-08-04 (read-only):

| Fact | Value |
|------|-------|
| Fork | `~/projects/tools/beads`, branch main at `cd33f0f3`, v0.34.0+109 commits, remotes origin=LoomDeBWiles/beads upstream=steveyegge/beads |
| Installed binary | `~/.local/bin/bd` (2026-07-18), `bd version` reports `cd33f0f3` — built from fork tip |
| Live daemons | 7 × `bd daemon --start --interval 5s` (PIDs 608600, 1216628, 1871189, 2053925, 3042331, 3447676, 3964600); `bd daemon --stop-all` exists |
| Fleet metadata | `jsonl_content_hash` = `d1df2880af33…24ca6949` = sha256 of live `fleet/.beads/issues.jsonl` (exact match, probed) — keys are **unsuffixed** (single-repo mode) |
| Toolchain | go1.24.11 linux/amd64; `Makefile` has `build` (ldflags Build=short-HEAD) and `install` targets |

## Validated Assumptions

| Assumption | Probe | Result |
|------------|-------|--------|
| Content-hash predicate returns "fresh" on real data where mtime predicate false-fired | `sha256sum fleet/.beads/issues.jsonl` vs `SELECT value FROM metadata WHERE key='jsonl_content_hash'` | Byte-identical (`d1df2880…`) |
| The three defects exist at the installed commit | Read `daemon_sync.go:569-583` (no clear call), `daemon_sync.go:90-147` (rename before metadata), `autoimport.go:264-306` (mtime-only) at `cd33f0f3` | Confirmed, matches RCA |
| Hash-based compare already exists as a pattern in the codebase | `hasJSONLChanged` (`cmd/bd/integrity.go:102-143`) always hashes — bd-v0y removed its mtime fast-path as unsafe | Precedent for always-hash, and for the migration fallback key `last_import_hash` |
| Exactly five JSONL writers exist (all `package main`/`internal/rpc`) | `exportToJSONLWithStore` (daemon export :444, pre-import flush :578, daemon sync :731 + `repo.go:229`), `writeJSONLAtomic` (autoflush :707, nodb :218), `sync.go` export :1380-1444, RPC export sites `server_export_import_auto.go:189,560` | Inventoried by grep; RPC sites rename with **no** metadata record |
| `validatePreExport` refuses export when content hash mismatches | `cmd/bd/integrity.go:146-153` returns "refusing to export: JSONL content has changed" | Confirmed — this is why a crashed record-before-reveal without a pending key would deadlock the daemon |
| `dirty_issues` carries `marked_at`, refreshed by upsert on re-mark | `internal/storage/sqlite/schema.go:136`, `dirty_helpers.go:12-45` (`ON CONFLICT … DO UPDATE SET marked_at`) | Confirmed — enables the conditional clear |

Unvalidated: current full-suite baseline (`go test ./...`) — builder captures it before any change (Phase 1 step 0); the gate is "no new failures vs baseline", not "all green", in case the fork carries pre-existing reds.

## Design

One new internal package, `internal/jsonlpub`, becomes the sole authority for two things: **deciding freshness** and **publishing JSONL**. Both `cmd/bd` (package main) and `internal/rpc` import it.

**The reader** — `ContentState(ctx, store, jsonlPath, keySuffix)` computes the file's sha256 and compares it against two metadata keys: `jsonl_content_hash` (committed; migration fallback `last_import_hash`, same as `hasJSONLChanged` today) and `jsonl_pending_hash` (pending). Result: `fresh` if the file hash equals either key; `diverged` if it matches neither; `no-metadata` if both keys are absent (first run — callers keep their current behavior). There is **no mtime logic**: bd-v0y already established the hash is cheap (~10-50 ms worst case, `integrity.go:120-121`) and the mtime shortcut unsafe. `CheckStaleness` and `hasJSONLChanged` both delegate to it, so the staleness gate, auto-import trigger, and `validatePreExport` all share one definition of "changed".

**The writer** — `Publish(ctx, store, jsonlPath, issues, opts{keySuffix, dirtySnapshot})` executes one serialized publication:

```
1. acquire publish lock (internal/lockfile, .beads/.publish.lock)   ── serializes all writers (R5)
2. write temp file, hashing while writing → H_new
3. SetMetadata(jsonl_pending_hash, H_new)                           ── record (pending)
   └ failure → remove temp, ABORT: no rename happens (R3)
4. os.Rename(temp, issues.jsonl) + chmod                            ── reveal
5. SetMetadata(jsonl_content_hash, H_new) + SetJSONLFileHash
   + last_import_time(RFC3339Nano) + delete pending                 ── promote (best-effort)
6. conditional dirty clear: DELETE WHERE issue_id=? AND marked_at=? ── loop dies (R6)
7. release lock

reader at ANY instant (no lock needed):
  file hash == committed → fresh        (steady state)
  file hash == pending   → fresh        (between 4 and 5, or crash there)
  file is old, pending set              (between 3 and 4, or crash there)
                         → old hash == committed → fresh
  file hash matches neither → STALE     (someone else wrote the file: git pull,
                                         manual edit — the one true divergence)
```

State walk (every reachable state reads correctly): crash after 3 → file old = committed, fresh; next `Publish` overwrites pending — and `validatePreExport` does not refuse, because `hasJSONLChanged` now accepts committed-or-pending (this is what makes record-before-reveal safe at all; v1's single-key version deadlocked here). Crash after 4 → file new = pending, fresh; next successful publish promotes. Promotion failure at 5 → warn, state stays valid via pending. Mid-export mutation: the upsert refreshes `marked_at` (`dirty_helpers.go:14-18`), so step 6's conditional delete leaves that row dirty and the next flush exports it — nothing is silently lost.

**Callers routed through `Publish`** (the five writers): daemon export (`daemon_sync.go:444`), daemon pre-import flush (`:578` — which finally clears its dirty rows, killing the once-per-second loop), daemon sync (`:731`), direct autoflush (`autoflush.go:707` block, replacing its hand-rolled write+clear+metadata tail), CLI sync export (`sync.go:1380-1444`), and both RPC export sites (`server_export_import_auto.go:189,560` — which today record nothing). `nodb.go:218` keeps plain `writeJSONLAtomic`: with no database there is no metadata and no staleness predicate to protect. New storage methods: `GetDirtyIssueSnapshots` (id + marked_at) and `ClearDirtyIssuesIfUnchanged` (conditional delete); existing `ClearDirtyIssuesByID` callers migrate.

Multi-repo suffixed keys (`:repoKey`): `Publish` takes the suffix; the extra-path metadata loops in `daemon_sync.go` stay byte-identical. No repo on this host uses them (fleet probe: unsuffixed keys only).

## Changes

### Phase 1: The publisher package  —  Gate: `mkdir -p work/w2_stale-race/artifacts` done, baseline failure list recorded, then `go test ./internal/jsonlpub/ ./internal/storage/sqlite/ -v` all pass

Step 0 (before any edit): `mkdir -p work/w2_stale-race/artifacts && go test ./... -count=1 2>&1 | tee work/w2_stale-race/artifacts/baseline.txt; grep '^--- FAIL:' work/w2_stale-race/artifacts/baseline.txt | sort -u > work/w2_stale-race/artifacts/baseline_failures.txt`

| File | Change | Why |
|------|--------|-----|
| `internal/jsonlpub/jsonlpub.go` (new) | `ContentState` (sha256 vs committed `jsonl_content_hash` / fallback `last_import_hash` / pending `jsonl_pending_hash`) and `Publish` (lock → temp+hash → pending → rename → promote → conditional clear), per Design. Rename/metadata failure semantics exactly as the Design state walk | The single authority both `cmd/bd` and `internal/rpc` can import |
| `internal/storage/sqlite/dirty.go` + interface in `internal/storage` | Add `GetDirtyIssueSnapshots(ctx) ([]DirtySnapshot{ID, MarkedAt})` and `ClearDirtyIssuesIfUnchanged(ctx, []DirtySnapshot)` (`DELETE … WHERE issue_id=? AND marked_at=?`) | R6: conditional clear needs the snapshot; a round-trip equality test proves the driver preserves `marked_at` fidelity |
| `internal/jsonlpub/jsonlpub_test.go` (new) | State-machine tests: steady fresh; same-bytes rewrite fresh; diverged (external edit, any mtime) stale; injected `SetMetadata` failure → temp removed, no rename, file unchanged; injected rename failure → pending retained, old file fresh, `validatePreExport`-equivalent not refused; concurrent-publisher serialization via the lock; mid-export re-mark survives conditional clear | R1, R2, R3, R5, R6, R7 pinned before any caller migrates |

### Phase 2: Route every reader and writer  —  Gate: `go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ -count=1` pass; `grep '^--- FAIL:'` of a full `go test ./... -count=1` run is a subset of `baseline_failures.txt`

| File | Change | Why |
|------|--------|-----|
| `internal/autoimport/autoimport.go` | `CheckStaleness` delegates to `jsonlpub.ContentState`: diverged → stale; fresh/no-metadata → not stale; interface unchanged for `staleness.go:32` and `server_export_import_auto.go:270,311` | Defect 3 via the one choke point |
| `cmd/bd/integrity.go` | `hasJSONLChanged` delegates to `ContentState` (changed = diverged) — gains pending-acceptance; `computeJSONLHash` stays for other callers | R2: `validatePreExport` must accept mid-publish states or record-before-reveal deadlocks |
| `cmd/bd/daemon_sync.go` | `exportToJSONLWithStore` body becomes collect-issues + `jsonlpub.Publish`; pre-import flush (:569-583) passes its dirty snapshot so step 6 clears it (loop dies); callers at :444/:731 drop single-repo `updateExportMetadata` branches; multi-repo loops byte-identical | Defects 1 + 2 |
| `cmd/bd/autoflush.go` | Flush path (:707-749): incremental merge map stays; `writeJSONLAtomic`+manual clear+metadata tail replaced by `Publish` with the dirty snapshot | R4: same protocol on the direct path |
| `cmd/bd/sync.go` | Export block (:1380-1444) replaced by `Publish` | R4 |
| `internal/rpc/server_export_import_auto.go` | Both export sites (:189, :560) replaced by `Publish` | R4: these record no metadata today |
| `internal/autoimport/autoimport_test.go`, `cmd/bd/daemon_sync_test.go` | Regressions: w25 shape (same-bytes rewrite via temp+rename → `CheckStaleness` false); restored-old-file (older mtime, different bytes) → stale; pre-import flush with N dirty rows flushes once, dirty count 0, second watcher pass exports nothing | Pins the killer and the R1 gap at the integration level |

### Phase 3: Deploy + live verify  —  Gate: `~/.local/bin/bd version` shows the new commit; live probes 5-8 below pass

| Step | Action |
|------|--------|
| 1 | `cp ~/.local/bin/bd ~/.local/bin/bd.cd33f0f3.bak` (rollback artifact; plain read-copy) |
| 2 | `cd <worktree> && go build -ldflags="-X main.Build=$(git rev-parse --short HEAD)" -o /tmp/bd.new ./cmd/bd` |
| 3 | `bd daemon --stop-all` (bd's own sanctioned stop), confirm `pgrep -af "bd daemon"` empty — **before** install, so nothing executes the file being replaced |
| 4 | `mv /tmp/bd.new ~/.local/bin/bd` (rename, immune to ETXTBSY), then `~/.local/bin/bd version` |
| 5 | Run live probes (Verification 5-8); daemons relaunch on demand on the new binary |

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `cmd/bd/nodb.go` | Yes | Store-less mode: no database → no metadata, no staleness predicate to protect; keeps plain `writeJSONLAtomic` |
| `cmd/bd/staleness.go` | Yes | Caller of `CheckStaleness`; interface unchanged |
| `cmd/bd/repo.go` | Yes | Its `exportToJSONLWithStore` call (:229) gains the protocol through the shared function body; no site-local edit |
| `internal/storage/sqlite/dirty_helpers.go` | Yes | `markDirty`/`markDirtyBatch` upsert semantics are exactly what the conditional clear relies on; unchanged |
| supervise-launch.sh (shared-docs skills/manager) and the orchestrator conveyor (external repos) | Yes | Root cause fixed in beads; no armor added, no env quartet baked in; the conveyor's existing one-retry stays as-is |

## Not in Scope

- Upstream PR to steveyegge/beads (user 2026-08-04: "we're not trying to upstream pr anything, just fix our local variant").
- Migration to upstream beads v1.x/Dolt — the architecture that deletes this bug class entirely; recorded as a future research-scale candidate, decoupled per user decision.
- `BD_ALLOW_STALE` environment plumbing — irrelevant once staleness is content-based.
- Multi-repo (`:repoKey`-suffixed) metadata semantics — behavior preserved byte-identical; no repo on this host uses them.

## Execution Handoff

Direct: a single builder works from this plan (~6 source files + 3 test files across three ordered phases). The changes form one atomic protocol — publisher, readers, and writers must land together or the repo is in a mixed single-key/two-key state, so splitting across builders adds integration risk without independent testability; the phase gates give the ordering a bead tree would. Person steps: none; deploy is agent-executable (`bd daemon --stop-all` is bd's own command, not a process kill).

## Rollback

- Binary: `bd daemon --stop-all` first (any bd binary can issue it), then `cp ~/.local/bin/bd.cd33f0f3.bak /tmp/bd.rollback && mv /tmp/bd.rollback ~/.local/bin/bd` — install by rename so a straggler daemon can't ETXTBSY the copy — then `~/.local/bin/bd version` must report `cd33f0f3`. Daemons relaunch on demand on the old binary. Full undo, seconds.
- Source: revert the work branch commits (worktree-local, pre-merge) or `git revert` post-merge.
- Data: forward-compatible — `jsonl_content_hash`/`last_import_time` keep today's meaning; the only new key, `jsonl_pending_hash`, is unknown to the old binary and at most one stale row of metadata (old binary ignores it; steady state has it deleted). A `.beads/.publish.lock` file may remain; it is inert without the new binary. No schema or file-format change.

## Risks

| Risk | Mitigation |
|------|------------|
| Hash on every freshness check (mtime gate removed) | bd-v0y already accepted this cost for `hasJSONLChanged` (~10-50 ms worst case per its comment); fleet's 811 KB file hashes sub-ms |
| `marked_at` round-trip fidelity: the conditional `DELETE … AND marked_at=?` silently matches nothing if the driver's time encoding differs between insert and bind | Phase 1 storage test proves insert→snapshot→conditional-delete round-trips on the real SQLite driver before any caller depends on it |
| Publish-lock contention: a slow publisher blocks others | Lock held only for temp-write + 2 metadata ops + rename (ms scale); `internal/lockfile` already carries stale-holder recovery used by the daemon |
| RPC export sites may lack direct store access for metadata writes | Builder verifies at migration time; if a site truly has no store handle, it keeps its current behavior and the plan's claim is corrected in the work report — readers still treat its output as diverged, exactly as today |
| Fork's `go test ./...` has pre-existing failures masking regressions | Phase 1 step 0 records `baseline_failures.txt`; Phase 2's gate requires the post-change failure list to be a subset |
| A daemon relaunching mid-deploy on the old binary | `--stop-all` before install; install by rename; any respawn after the `mv` execs the new file |

## Verification

**Tests:**
- Step 0 (before any edit, after `mkdir -p work/w2_stale-race/artifacts`): `go test ./... -count=1 2>&1 | tee work/w2_stale-race/artifacts/baseline.txt; grep '^--- FAIL:' work/w2_stale-race/artifacts/baseline.txt | sort -u > work/w2_stale-race/artifacts/baseline_failures.txt`
- After Phase 1: `go test ./internal/jsonlpub/ ./internal/storage/sqlite/ -v` — all pass (publisher state machine + dirty-snapshot round-trip).
- After Phase 2: `go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ -count=1` pass; full run's `grep '^--- FAIL:' | sort -u` is a subset of `baseline_failures.txt`.

**E2E verification** (scratch repo, exact built binary `BD=/tmp/bd.new` before install — R9 shape):
1. `$BD init` a throwaway repo; create an issue; then `$BD --no-daemon --no-auto-flush update <id> --status in_progress` — strands the row dirty (proven via `SELECT COUNT(*) FROM dirty_issues` = 1).
2. Start the repo's daemon with the built binary; trigger the watcher by replacing `issues.jsonl` with identical bytes via temp+rename.
3. Assert the daemon log records exactly one `Flushing 1 dirty issues before import` and dirty count is 0; trigger the watcher again the same way; assert **no** second flush line (the old binary loops once per second here).
4. `$BD --no-daemon --db <scratch db> --no-auto-import info --json` → exit 0 (same-bytes rewrite is fresh).
5. Append a valid issue JSON line to `issues.jsonl`; repeat the command → must fail with the out-of-sync error (true divergence still caught).

**Proof of behavior** (builder executes and pastes output; artifacts under `work/w2_stale-race/artifacts/`):

| # | Check | Command | Expected |
|---|------|---------|----------|
| 1 | Publisher state machine incl. injected pending-write failure and injected rename failure | `go test ./internal/jsonlpub/ -v` | PASS: no rename after metadata failure; pending retained + readers fresh after rename failure |
| 2 | Mid-export mutation survives conditional clear | `go test ./internal/storage/sqlite/ -run Dirty -v` | PASS: re-marked row still dirty after clear; round-trip delete matches |
| 3 | Regression pins w25 shape + restored-old-file | `go test ./internal/autoimport/ -run TestCheckStaleness -v` | PASS: same-bytes rewrite fresh; older-mtime different-bytes stale |
| 4 | Loop dead at unit level | `go test ./cmd/bd/ -run PreImportFlush -v` | PASS: second flush exports nothing, dirty count 0 |
| 5 | No new suite failures | `go test ./... -count=1 2>&1 | grep '^--- FAIL:' | sort -u` vs `baseline_failures.txt` | Subset (ideally equal) |
| 6 | Deployed binary identity | `~/.local/bin/bd version` | Reports the new short HEAD, not `cd33f0f3` |
| 7 | w25 killer gone live | `touch ~/projects/fleet/.beads/issues.jsonl && ~/.local/bin/bd --no-daemon --db ~/projects/fleet/.beads/beads.db --no-auto-import info --json >/dev/null; echo $?` | `0` (the current binary daemon-routes without `--no-daemon`, masking the gate — R8) |
| 8 | Daemon quiet on fleet | After one real bead mutation on the new binary: count `JSONL file created` lines in fleet's daemon log over 60 s | ≤1 — no once-per-second rewrites |
| 9 | True divergence still caught live | Scratch repo from E2E step 5 | Out-of-sync error with `--no-daemon` |
