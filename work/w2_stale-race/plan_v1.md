# w2_stale-race: Kill the Beads false-staleness race

> Make Beads staleness mean "JSONL content differs from what the database recorded", record export metadata before revealing the file, and stop the daemon's self-triggering rewrite loop — so no healthy system can ever again fail with "Database out of sync with JSONL".

## User Intent

"Fix this" — the bug where the fleet w25 supervisor exited fatal on `bd info failed: Error: Database out of sync with JSONL` although nothing was actually out of sync. Fix our local beads fork only (no upstream PR); deploy is in scope: rebuild, install to `~/.local/bin/bd`, restart the running daemons. Locked in 2026-08-04 ("ok let's just lock in the plan to fix our version").

## Problem

Beads keeps two stores: SQLite (live) and `.beads/issues.jsonl` (git-synced mirror). Before answering, direct commands run a freshness gate that compares the JSONL **mtime** against a `last_import_time` timestamp stored in SQLite; file-newer means fail-stop. Three defects in the fork (tip `cd33f0f3`, the installed binary) turn that gate into a standing false alarm:

1. **Self-triggering rewrite loop.** The daemon's pre-import flush (`cmd/bd/daemon_sync.go:569-583`) exports dirty issues but never calls `ClearDirtyIssuesByID` — unlike the two other export paths (`cmd/bd/autoflush.go:707-720`, `cmd/bd/sync.go:1420`). The same dirty rows re-export on every watcher cycle; the export itself wakes the watcher. The w25 daemon log records `Flushing 2 dirty issues before import...` + `JSONL file created` once per second, indefinitely (`fleet/.beads/daemon-2026-07-24T20-42-34.692.log.gz` lines 666821-669254).
2. **File revealed before it is recorded.** `exportToJSONLWithStore` (`daemon_sync.go:31-147`) renames the new JSONL into place first; only afterwards does `updateExportMetadata` (`daemon_sync.go:280`) write `jsonl_content_hash` and `last_import_time`. Between rename and metadata write, the file is newer than the record.
3. **The gate measures clock order, not content.** `CheckStaleness` (`internal/autoimport/autoimport.go:264-306`) returns stale purely on `mtime > last_import_time`. Any reader landing in defect 2's window — reopened every second by defect 1 — fail-stops. The w25 supervisor's `bd info` hit it twice in one second (`enumerate_manifest` retries once, `process_development/conveyor.py:2413-2429`) and exited code 1 at `1784925592257778910`.

Full RCA with daemon-log line numbers: `~/projects/fleet/work/w25_inbox-queue/rca_stale_v1.md`.

## Key Insight

**A freshness predicate over two separately-published artifacts must compare content, and the publisher must record before it reveals.** `mtime > last_import_time` is a proxy for "the bytes differ" that false-fires whenever the file is rewritten with equal content or rewritten a moment before the bookkeeping lands — both of which Beads itself does. Forget this and any fix is armor: retries, allow-stale flags, and env quartets all treat the alarm, not the lie. After this change, "stale" is true iff the JSONL bytes differ from the content hash the database already maintains — a predicate that cannot false-fire regardless of timing, touch, or git checkout.

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
| Hash-based compare already exists as a pattern in the codebase | `hasJSONLChanged` (`cmd/bd/integrity.go:102+`) does mtime-fast-path + sha256 compare for auto-import | Same design, different consumer — precedent, not new invention |

Unvalidated: current full-suite baseline (`go test ./...`) — builder captures it before any change (Phase 1 step 0); the gate is "no new failures vs baseline", not "all green", in case the fork carries pre-existing reds.

## Design

Before → after of one daemon cycle:

```
BEFORE                                        AFTER
watcher event                                 watcher event
  dirty rows found (never cleared)              dirty rows found
  write temp → RENAME (file now newer)          write temp, hashing while writing
  ...ms window: reader bd info → FATAL          SetMetadata(hash, now)   ← record
  SetMetadata(hash, now)                        RENAME                   ← reveal
  watcher sees rename → next cycle (1/sec)      ClearDirtyIssuesByID     ← loop dies
                                              reader bd info at ANY instant:
                                                mtime older → fresh (fast path)
                                                mtime newer → sha256 == stored hash → fresh
                                                hash differs → stale (real divergence only)
```

Crash trace for the reordered publish (record-then-reveal): metadata written, process dies before rename → disk keeps the OLD file; `last_import_time` = now > old mtime → fresh fast-path (correct: SQLite holds the newest data, JSONL is behind — the normal direction; the next export heals it). The reverse order's crash case (today's) leaves the file newer than the record → permanent false-stale until an import. The new order fails safe; the old order fails lying.

`CheckStaleness` reads the **unsuffixed** `jsonl_content_hash` — exact parity with the unsuffixed `last_import_time` it reads today (verified against fleet's live metadata). Multi-repo suffixed keys (`:repoKey`) keep their existing behavior in `updateExportMetadata`; no repo here uses them.

## Changes

### Phase 1: Content-based staleness  —  Gate: new + existing `internal/autoimport` tests pass; baseline captured first

| File | Change | Why |
|------|--------|-----|
| `internal/autoimport/autoimport.go` | `CheckStaleness`: keep mtime comparison as fast path; when mtime is newer, sha256 the JSONL and compare to metadata `jsonl_content_hash` — equal → `(false, nil)`; missing/empty stored hash → stale (today's behavior); read error → `(false, err)`. Add a small file-sha256 helper in this package (`computeJSONLHash` lives in package `main`, not importable) | Defect 3: the predicate itself; heals every reader (`cmd/bd/staleness.go:32`, `internal/rpc/server_export_import_auto.go:270,311`) through the one choke point |
| `internal/autoimport/autoimport_test.go` | New cases: (a) mtime newer + identical content → fresh; (b) mtime newer + different content → stale; (c) mtime newer + no stored hash → stale; (d) w25 shape: rewrite same bytes via temp+rename → fresh | Regression pins the exact false-fire that killed w25 |

### Phase 2: Record-then-reveal + loop kill  —  Gate: `cmd/bd` daemon tests pass; `go test ./...` shows no new failures vs Phase 1 step 0 baseline

| File | Change | Why |
|------|--------|-----|
| `cmd/bd/daemon_sync.go` | `exportToJSONLWithStore`: hash content while writing the temp file (`io.MultiWriter` into sha256), then `SetMetadata("jsonl_content_hash", …)` + `SetMetadata("last_import_time", RFC3339Nano now)` **before** `os.Rename`. Metadata write failure → log warning and still rename (today's failure tolerance, `daemon_sync.go:302-307`) | Defect 2: no instant at which the file is ahead of the record |
| `cmd/bd/daemon_sync.go` | Callers at :444 and :731: drop the now-redundant single-repo `updateExportMetadata(…, "")` branch; keep the multi-repo loop byte-identical. Pre-import flush (:569-583): capture `GetDirtyIssues` IDs before export; after successful export, `ClearDirtyIssuesByID(ids)` (mirror of `autoflush.go:716`); keep its suffixed `updateExportMetadata` call only for `repoKey != ""` | Defect 1: the flush stops re-exporting the same rows; single-repo metadata is now recorded inside the export itself |
| `cmd/bd/daemon_sync_test.go` | New cases: (a) after export, stored `jsonl_content_hash` == sha256 of the renamed file; (b) pre-import flush with N dirty rows exports once and leaves dirty count 0; second invocation exports nothing | Pins both fixes at the unit level |

### Phase 3: Deploy + live verify  —  Gate: `bd version` shows the new commit; live probes 4-6 below pass

| Step | Action |
|------|--------|
| 1 | `cp ~/.local/bin/bd ~/.local/bin/bd.cd33f0f3.bak` (rollback artifact), then `cd <worktree> && go build -ldflags="-X main.Build=$(git rev-parse --short HEAD)" -o ~/.local/bin/bd ./cmd/bd` |
| 2 | `bd daemon --stop-all` (bd's own sanctioned stop; daemons relaunch on demand), then confirm `pgrep -af "bd daemon"` is empty |
| 3 | Run live probes (Verification 4-6) against fleet |

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `cmd/bd/autoflush.go` | Yes | Direct-flush path already clears dirty IDs (:716); its rename-then-metadata order stays — Phase 1's content check makes its ms-window benign for every reader, and restructuring the incremental-merge path is risk without a proven failing scenario (accepted residual, see Risks) |
| `cmd/bd/staleness.go`, `internal/rpc/server_export_import_auto.go` | Yes | Callers of `CheckStaleness`; interface unchanged |
| `cmd/bd/integrity.go` | Yes | Its auto-import trigger is already hash-based; unchanged semantics |
| `cmd/bd/sync.go`, `cmd/bd/repo.go`, `cmd/bd/nodb.go` | Yes | Other `exportToJSONLWithStore`/`writeJSONLAtomic` consumers gain the safe publish order for free via the shared function or don't record metadata today (repo.go multi-repo sync — pre-existing, out of scope) |
| supervise-launch.sh (shared-docs skills/manager) and the orchestrator conveyor (external repos) | Yes | Root cause fixed in beads; no armor added, no env quartet baked in; the conveyor's existing one-retry stays as-is |

## Not in Scope

- Upstream PR to steveyegge/beads (user 2026-08-04: "we're not trying to upstream pr anything, just fix our local variant").
- Migration to upstream beads v1.x/Dolt — the architecture that deletes this bug class entirely; recorded as a future research-scale candidate, decoupled per user decision.
- `BD_ALLOW_STALE` environment plumbing — irrelevant once staleness is content-based.
- Multi-repo (`:repoKey`-suffixed) metadata semantics — behavior preserved byte-identical; no repo on this host uses them.

## Execution Handoff

Direct: a single builder works from this plan (2 source files + 2 test files, three ordered phases — under the bead threshold). Person steps: none; deploy is agent-executable (`bd daemon --stop-all` is bd's own command, not a process kill).

## Rollback

- Binary: `cp ~/.local/bin/bd.cd33f0f3.bak ~/.local/bin/bd && bd daemon --stop-all` — daemons relaunch on demand on the old binary. Full undo, seconds.
- Source: revert the work branch commits (worktree-local, pre-merge) or `git revert` post-merge.
- Data: none — no schema, key, or file-format change; `jsonl_content_hash`/`last_import_time` are the keys every current binary already writes.

## Risks

| Risk | Mitigation |
|------|------------|
| Hash cost on every mtime-newer check | Only paid when mtime is newer (rare once the loop is dead); fleet's issues.jsonl is ~1 MB → sub-ms sha256; same cost profile `hasJSONLChanged` already pays |
| Direct autoflush keeps rename-then-metadata order — a reader in its ms window sees hash mismatch → stale | Accepted residual: window is per-real-mutation (not per-second), `conveyor.py` retries once, and the false verdict requires landing inside single milliseconds twice. If it ever fires, the fix is extending record-then-reveal to `writeJSONLAtomic` |
| Fork's `go test ./...` has pre-existing failures masking regressions | Phase 1 step 0 captures the baseline; gates compare against it, and any new failure blocks |
| A daemon relaunching mid-deploy on the old binary | Deploy order: install binary first, then `--stop-all`; anything that respawns after the stop execs the new file |

## Verification

**Tests:**
- Step 0 (before any edit): `go test ./... 2>&1 | tee work/w2_stale-race/artifacts/baseline.txt` — record failures.
- After Phase 1: `go test ./internal/autoimport/ -v` — all pass.
- After Phase 2: `go test ./cmd/bd/ -run 'TestExport|TestDaemon|TestStale' -v` pass; `go test ./...` — no failure absent from baseline.

**E2E verification:** scratch repo E2E reproducing the w25 mechanism against the built binary (not the installed one) — init a throwaway beads repo, start its daemon, mutate an issue's status directly, and observe the daemon log: exactly one `Flushing … dirty issues` flush, then quiet (the loop used to repeat per second). Then rewrite `issues.jsonl` with identical bytes via temp+rename and run `bd info` — must exit 0. Then append a real change to JSONL and run `bd info` — must fail stale (the gate still catches true divergence).

**Proof of behavior** (builder executes and pastes output; artifacts under `work/w2_stale-race/artifacts/`):

| # | Check | Command | Expected |
|---|------|---------|----------|
| 1 | Regression pins w25 shape | `go test ./internal/autoimport/ -run TestCheckStaleness -v` | PASS incl. same-content-rewrite → fresh |
| 2 | Loop dead at unit level | `go test ./cmd/bd/ -run TestPreImportFlush -v` | PASS: second flush exports nothing, dirty count 0 |
| 3 | No new suite failures | `diff` of failure lists: baseline vs post-change `go test ./...` | Empty or subset |
| 4 | Deployed binary identity | `bd version` | Reports the new short HEAD, not `cd33f0f3` |
| 5 | w25 killer gone live | `touch ~/projects/fleet/.beads/issues.jsonl && bd --db ~/projects/fleet/.beads/beads.db --no-auto-import info --json >/dev/null; echo $?` | `0` (today this can exit 1 stale) |
| 6 | Daemon quiet on fleet | Start fleet daemon on new binary, make one bead mutation, then `grep -c "JSONL file created" <daemon log tail over 60s>` | ≤1 — no once-per-second rewrites |
| 7 | True divergence still caught | In scratch repo: append a valid issue line to issues.jsonl, `bd --no-auto-import info` | Fails with out-of-sync error (gate still works) |
