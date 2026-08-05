# w2_stale-race: Kill the Beads false-staleness race

> Make Beads staleness mean "JSONL content differs from what the database recorded", record export metadata before revealing the file, and stop the daemon's self-triggering rewrite loop — so no healthy system can ever again fail with "Database out of sync with JSONL".

## What Changed from v4

| ID | v4 said | v5 says | Why |
|----|---------|---------|-----|
| R4-1 | Divergence guard only at publish entry | Guard hash re-checked immediately before rename; changed → typed divergence abort | ACCEPT — a lock-ignoring writer (git checkout) between guard and rename was silently overwritten |
| R4-2 | Pre-import flush aborts on divergence | Typed divergence branch: skip flush → import → RecordImport → publish still-dirty snapshot; starvation regression test | ACCEPT — every watcher retry died at the same flush; the healing import was starved |
| R4-3 | Guard aborts on diverged only | Also aborts on no-metadata when the canonical file exists | ACCEPT — an unrecorded existing file could be overwritten though hasJSONLChanged demands import-first |
| R4-4 | Guard called public ContentState under the lock | Lock-held sampler (never reacquires) + context-aware lockfile API, both listed as Phase 1 changes with tests | ACCEPT — self-deadlock; the ctx-aware API did not exist in the change list |
| R4-5 | RecordImport replacing import.go:388-396 | RecordImport placed after import, before the flush at :374-379; old block deleted | ACCEPT — the listed position ran Publish before RecordImport, tripping the new guard |
| R4-6 | Parsed-bytes hash assumed available everywhere | CLI and daemon parsers hash during read; importToJSONLWithStore returns the hash; concurrent-rewrite tests | ACCEPT — only autoimport had the hash; the other callers had no source for it |
| R4-7 | `grep -c … \|\| echo 0` in the E2E | `count_flushes` helper: grep output kept, exit status neutralized, single line | ACCEPT — empty-log case emitted two zero lines and broke arithmetic |
| R4-8 | E2E after install of /tmp/bd.new | E2E is Phase 3 step 4, before stop/install | ACCEPT — the script's BD path pointed at a file already moved away |
| R4-9 | Teardown as a final script line | EXIT trap stops the recorded scratch daemon on success and failure | ACCEPT — set -e skipped teardown on any assertion failure |
| R4-10 | `pgrep -af "bd daemon"` | Self-excluding `pgrep -af '[b]d daemon'` for inventory and post-stop assertion | ACCEPT — the command matched its own invocation |
| R4-11x | Risk table without the ceiling row | Ceiling row added: R4 fixes un-reviewed by Codex; Phase 1 tests + gate as backstop | Consequential — honest gate disclosure |

## What Changed from v3

| ID | v3 said | v4 says | Why |
|----|---------|---------|-----|
| R3-1 | RecordImport commits the current file hash | RecordImport commits the sha256 of the bytes the import parsed (autoimport already computes it) | ACCEPT (smaller fix than proposed) — re-hashing blesses a file rewritten after the import's read; recording the parsed hash makes that state read diverged, no refuse-verify machinery needed |
| R3-2 | External file changes guarded only by callers' validatePreExport | Publish step 2: locked ContentState guard — diverged → abort, import first | ACCEPT (smaller fix) — the guard becomes atomic with the rename it protects; no locking of git operations |
| R3-3 | buildSnapshot reads issues + dirty rows, order unspecified | Dirty snapshot read first, then issue data; during-builder mutation test | ACCEPT — issues-then-markers pairs old bytes with a new marked_at and clears it |
| R3-4 | Crashed pending healed by next publish/import only | Locked readers observing file==pending promote it; remaining transient documented in Risks | ACCEPT (smaller fix) — closes the window at the first locked event; persistent temp-pathname evidence rejected as over-armor for a doubly-rare transient |
| R3-5 | RecordImport after every import | Only for the canonical default JSONL; custom-file imports never touch canonical metadata | ACCEPT — `bd import -i backup.jsonl` must not record the backup hash as canonical |
| R3-6 | No RecordImport failure policy | Record failure warns; onChanged runs with original needsFullExport | ACCEPT — a metadata error must not skip the full-export callback after ID remapping |
| R3-7 | "Resolves to the canonical default JSONL" (unspecified) | Precise rule: resolve parent directories, never the final component; relative-path and symlink-alias tests | ACCEPT — following a final-component symlink misclassifies an alias |
| R3-8 | Blocking lock acquisition | Context-aware acquisition returning the context error | ACCEPT — a wedged publisher must not hang RPC goroutines past their deadlines |
| R3-9 | Phase 1 step 0 / Phase 2 gate still carried the v2 grep baseline | Both phases cite the -json/status/normalizer workflow with artifact paths | ACCEPT — the ordered phases contradicted Verification |
| R3-10 | `pkg` signature from any package-level fail event | `pkg::<build>` only when no failed test event or stderr shows build evidence; fixture self-test | ACCEPT — Go emits package fail events after ordinary test failures too |
| R3-11 | E2E spec with `<id>`/`<scratch db>` placeholders | The literal script inline; builder copies byte-for-byte | ACCEPT — twice-flagged executability closed for good |
| R3-12 | All mapping tests in internal/jsonlpub | Protocol tests in jsonlpub; CheckStaleness mapping in internal/autoimport; hasJSONLChanged mapping in cmd/bd | ACCEPT — jsonlpub cannot call unexported cmd/bd functions |
| R3-13x | Risk table without the R3-4 residual | Residual row added with revisit-if-observed rule | Consequential to R3-4 |

## What Changed from v2

| ID | v2 said | v3 says | Why |
|----|---------|---------|-----|
| V2-1 | ContentState: hash file, read keys, verdict | Diverged verdict only after acquiring the publish lock and re-sampling; lock-free fast path when hashes match | ACCEPT — a reader sampling the file before an A→B publish and the keys after it called a healthy repo diverged |
| V2-2 | Publish accepts caller-built issues + dirty snapshot | Publish takes a snapshot-builder callback invoked inside the lock | ACCEPT — pre-lock snapshots let an older snapshot publish last while a newer publisher's clear ran |
| V2-3 | hasJSONLChanged "changed = diverged" | Caller-specific tri-state mapping: CheckStaleness no-metadata→fresh; hasJSONLChanged no-metadata (file exists)→changed | ACCEPT — v2 silently inverted `integrity.go:134-136` first-run behavior, skipping a needed import |
| V2-4 | Import paths "unchanged" | New `jsonlpub.RecordImport` (committed=file hash, delete pending, timestamps, under the lock) after every successful import, incl. zero-row | ACCEPT — import metadata writers are scattered (`autoimport.go:103,137,144`, `import.go:388,396`) and daemon import sites record inconsistently; without an import-side twin the pulled file fails the gate |
| V2-5 | Pending deleted only by Publish promote | RecordImport is the second deleter, ordered before post-import export callbacks | ACCEPT — a crashed publish's pending B otherwise survives a later import X indefinitely |
| V2-6 | Five JSONL writers | Six: manual `bd export` default-path writer (`export.go:463-476`) added; stdout/custom-path exports stay plain | ACCEPT — missed writer renames onto the canonical file outside any protocol |
| V2-7 | RPC handleExport wholesale → Publish | Publish only when the target resolves to the canonical default JSONL; custom paths keep a plain atomic writer | ACCEPT — a custom-path export must not overwrite the default repo's committed hash or dirty state |
| V2-8 | Snapshot methods added to storage.Storage | Narrow `DirtySnapshotStore` interface, SQLite-only; shared interface and memory backend untouched | ACCEPT — memory backend's `map[string]bool` dirty state (`memory.go:37`) cannot satisfy marked_at methods; build would break |
| V2-9 | Tests stop at rename failure | Failpoints after every protocol step incl. each promote sub-write, asserting reader verdicts at each state | ACCEPT — promote-phase failures were untested |
| V2-10 | Baseline = grep FAIL lines | `go test -json` capturing test + package build failures + command status; `comm -13` comparison | ACCEPT — a compile failure produced an empty "passing" subset |
| V2-11 | E2E prose steps | One checked scratch-repo script with explicit paths, SQL assertions, log baselining, teardown by recorded PID | ACCEPT — v2's steps were not executable as written |
| V2-12 | (relaunch-on-demand after --stop-all) | Unchanged; deploy additionally records the pre-stop daemon inventory as evidence | REJECT — relaunch-on-demand is the deploy scope the user approved at lock-in (manager_log); inventory recording added as evidence only |
| V2-13x | Files NOT Affected listed import.go implicitly, omitted export.go/memory | import.go and export.go move to affected; memory backend added as verified-untouched | Consequential to V2-4/V2-6/V2-8 |
| V2-14x | ~6 source files + 3 test files | ~9 source files + tests; single direct builder rationale restated | Consequential to V2-4/V2-6/V2-7 |
| V2-15x | Risk table carried an RPC-store-access residual | Replaced by lock-recheck cost and import-lock interaction rows | Consequential to V2-1/V2-4/V2-7 |

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
| Exactly six writers rename onto the canonical issues.jsonl | `exportToJSONLWithStore` (daemon export :444, pre-import flush :578, daemon sync :731 + `repo.go:229`), `writeJSONLAtomic` (autoflush :707, nodb :218), `sync.go` export :1380-1444, RPC export sites `server_export_import_auto.go:189,560`, manual `bd export` default-path branch `export.go:463-476` | Inventoried by grep over `os.Rename`; RPC sites and manual export rename with **no** metadata record |
| Import-side metadata writers are scattered, not centralized | `autoimport.go:103` (touch path), `:137,144` (post-import), `import.go:388,396`; daemon import sites `daemon_sync.go:633,854` call `importToJSONLWithStore` | Confirmed — no single import-record authority exists today; RecordImport creates it |
| Memory backend cannot carry marked_at snapshot methods | `internal/storage/memory/memory.go:37`: `dirty map[string]bool` | Confirmed — narrow SQLite-only interface required (V2-8) |
| `validatePreExport` refuses export when content hash mismatches | `cmd/bd/integrity.go:146-153` returns "refusing to export: JSONL content has changed" | Confirmed — this is why a crashed record-before-reveal without a pending key would deadlock the daemon |
| `dirty_issues` carries `marked_at`, refreshed by upsert on re-mark | `internal/storage/sqlite/schema.go:136`, `dirty_helpers.go:12-45` (`ON CONFLICT … DO UPDATE SET marked_at`) | Confirmed — enables the conditional clear |

Unvalidated: current full-suite baseline (`go test ./...`) — builder captures it before any change (Phase 1 step 0); the gate is "no new failures vs baseline", not "all green", in case the fork carries pre-existing reds.

## Design

One new internal package, `internal/jsonlpub`, becomes the sole authority for the file↔database consistency contract. It owns three operations — **deciding freshness** (`ContentState`), **publishing DB→file** (`Publish`), and **recording file→DB** (`RecordImport`) — all serialized by one cross-process lock. Both `cmd/bd` (package main) and `internal/rpc` import it.

**The reader** — `ContentState(ctx, store, jsonlPath, keySuffix)` computes the file's sha256 and compares it against two metadata keys: `jsonl_content_hash` (committed; migration fallback `last_import_hash`, same as `hasJSONLChanged` today) and `jsonl_pending_hash` (pending). Tri-state result: `fresh` (file hash equals either key), `diverged` (matches neither), `no-metadata` (both keys absent). **Lock-recheck rule (V2-1):** the lock-free sample is authoritative only for `fresh`; a provisional `diverged` or `no-metadata` verdict is confirmed by acquiring the publish lock and re-sampling file hash plus both keys — a reader that catches an A→B publish between its file read and its key read otherwise calls a healthy repo diverged. There is **no mtime logic** (bd-v0y: hash is ~10-50 ms worst case, mtime unsafe). Caller mapping is caller-specific (V2-3): `CheckStaleness` maps no-metadata→fresh (first-run tolerance, today's behavior); `hasJSONLChanged` maps no-metadata with an existing file→changed (preserves `integrity.go:134-136`: an unrecorded JSONL must import before export is allowed).

**The writer** — `Publish(ctx, store, jsonlPath, buildSnapshot, opts{keySuffix})` executes one serialized publication. `buildSnapshot` is a callback invoked **inside the lock** (V2-2) that reads the **dirty snapshot first, then** the database issues (and, for incremental paths, the existing JSONL merge map) and returns `(issues, dirtySnapshot)` — dirty-first ordering (R3-3) guarantees a mutation landing between the two reads leaves either a newer `marked_at` (the conditional clear skips it) or newer bytes (they get exported); pre-lock or issues-first snapshots let a mutation be exported stale yet cleared.

```
1. acquire publish lock (internal/lockfile, .beads/.publish.lock;
   context-aware acquisition — R3-8/R4-4)
2. guard: lock-held ContentState sample (never reacquires the lock — R4-4)
   └ diverged → typed divergence ABORT (import first) — validatePreExport's
     semantic, atomic with the rename it protects (R3-2)
   └ no-metadata AND the canonical file exists → same ABORT (R4-3):
     an unrecorded file must import first; no-metadata publishes only
     onto an absent file
   └ record guardHash = the sampled file hash
3. dirtySnap := read dirty snapshot; issues := read issue data             ── dirty-first (R3-3)
4. write temp file, hashing while writing → H_new
5. SetMetadata(jsonl_pending_hash, H_new)                                  ── record (pending)
   └ failure → remove temp, ABORT: no rename happens (R3)
6. re-hash the canonical file; != guardHash → typed divergence ABORT       ── R4-1: a lock-
   (remove temp, delete pending)                                              ignoring writer
                                                                              landed since step 2
7. os.Rename(temp, issues.jsonl) + chmod
8. SetMetadata(jsonl_content_hash, H_new) + SetJSONLFileHash
   + last_import_time(RFC3339Nano) + delete pending                        ── promote (best-effort)
9. conditional dirty clear: DELETE WHERE issue_id=? AND marked_at=?        ── loop dies (R6)
10. release lock
```

**Divergence branch in the daemon watcher (R4-2):** the pre-import flush path treats a typed divergence abort as routing, not failure — dirty + diverged → skip the flush, run the import, `RecordImport` the canonical bytes, then publish the still-dirty snapshot (the conditional clear kept those rows dirty; the importer's existing merge semantics decide row contents, unchanged by this plan). Without this branch every watcher retry aborts at the same flush and the healing import is starved.

**The import twin** — `RecordImport(ctx, store, jsonlPath, importedHash, keySuffix)` runs after every successful import of the **canonical** file (including zero-row imports), under the same lock. It commits `importedHash` — the sha256 of the bytes the import actually parsed, which autoimport already computes (R3-1) — never a re-hash of the file: if the file changed between the import's read and the record, the gate correctly reads diverged and the next import picks it up. It deletes pending and sets `last_import_time` (V2-4/V2-5), ordered **before** any post-import export callback; a record failure is a warning and the callback still runs with its original `needsFullExport` value (R3-6). Custom-file imports (`bd import -i backup.jsonl`) never call it — canonical metadata belongs to the canonical file only (R3-5).

**Canonical-path rule (R3-7):** a path is canonical iff, after resolving parent directories but **not** the final component, it equals the repo's default JSONL path the same way — rename replaces the final directory entry, so following a final-component symlink would misclassify an alias whose rename leaves the real default file untouched.

Reader verdicts at every instant (fast path lock-free, mismatch confirmed under lock):

```
file hash == committed → fresh          (steady state)
file hash == pending   → fresh          (between 7 and 8, or crash there); a LOCKED reader
                                        that observes this promotes: committed=pending,
                                        delete pending (R3-4) — the two-key window closes
                                        at the first locked event after a crash
file old + pending set → fresh          (between 5 and 7, or crash/abort there:
                                         old hash == committed)
matches neither (under lock) → STALE    (bytes written by someone else: git pull before its
                                         import lands, manual edit — true divergence only)
```

State walk: crash after 5, or a step-6 divergence abort → file = committed (or the diverging external bytes), pending dangling until the first locked event clears or promotes past it; `validatePreExport` does not refuse (hasJSONLChanged accepts committed-or-pending — v1's single-key version deadlocked exactly here); next publish overwrites pending. Crash after 7 → file = pending, fresh; the first locked event (reader promotion, publish, or RecordImport) promotes/clears. Promotion failure at 8 → warn; pending keeps readers correct. Mid-export mutation → upsert refreshes `marked_at` (`dirty_helpers.go:14-18`), step 9's conditional delete leaves the row dirty, next flush exports it. Accepted residual (R3-4, see Risks): between a crashed post-rename publish and the first locked event, a manual restore of the exact old committed bytes reads fresh; it self-corrects at the next publish or import.

**Callers routed through `Publish`** — the six canonical-file writers (V2-6): daemon export (`daemon_sync.go:444`), daemon pre-import flush (`:578` — finally clearing its dirty rows: the once-per-second loop dies), daemon sync (`:731`), direct autoflush (`autoflush.go:707` block, replacing its hand-rolled write+clear+metadata tail), CLI sync export (`sync.go:1380-1444`), manual `bd export` **default-path branch only** (`export.go:463-476`), and RPC export (`server_export_import_auto.go:189,560`) **only when the requested path resolves to the canonical default JSONL** (V2-7) — stdout, custom `-o` paths, and custom RPC paths keep a plain atomic writer with no metadata or dirty effects. `nodb.go:218` keeps plain `writeJSONLAtomic` (no database → no contract to protect). **Callers routed through `RecordImport`**: `internal/autoimport` (both the hash-unchanged touch path :103 and the post-import path :137-144), CLI import (`import.go:388-396`), and the daemon import sites (`daemon_sync.go:633,854`).

**Storage surface (V2-8):** the new methods `GetDirtyIssueSnapshots` (id + marked_at) and `ClearDirtyIssuesIfUnchanged` (conditional delete) live on a narrow `DirtySnapshotStore` interface implemented by SQLite only; publish paths type-assert it and fall back to no dirty-clearing when absent. The shared `storage.Storage` interface and the memory backend (`memory.go:37`, boolean dirty map) are untouched.

Multi-repo suffixed keys (`:repoKey`): `Publish`/`RecordImport` take the suffix; the extra-path metadata loops in `daemon_sync.go` stay byte-identical. No repo on this host uses them (fleet probe: unsuffixed keys only).

## Changes

### Phase 1: The publisher package  —  Gate: `mkdir -p work/w2_stale-race/artifacts` done, baseline failure list recorded, then `go test ./internal/jsonlpub/ ./internal/storage/sqlite/ -v` all pass

Step 0 (before any edit, R3-9 — this IS the baseline protocol, identical to Verification's): `mkdir -p work/w2_stale-race/artifacts`, then `go test ./... -count=1 -json > work/w2_stale-race/artifacts/baseline.json 2> work/w2_stale-race/artifacts/baseline_stderr.txt; echo $? > work/w2_stale-race/artifacts/baseline_status.txt`, then write `work/w2_stale-race/artifacts/normalize_failures.py` (spec in Verification) and run it → `work/w2_stale-race/artifacts/baseline_failures.txt`.

| File | Change | Why |
|------|--------|-----|
| `internal/jsonlpub/jsonlpub.go` (new) | Per Design: `ContentState` (tri-state, lock-recheck on provisional mismatch), `Publish` (lock → snapshot callback → temp+hash → pending → rename → promote → conditional clear; pending-write failure aborts), `RecordImport` (lock → committed=file hash, delete pending, timestamps) | The single authority both `cmd/bd` and `internal/rpc` can import; import twin per V2-4/V2-5 |
| `internal/jsonlpub/store.go` (new) | `DirtySnapshotStore` narrow interface: `GetDirtyIssueSnapshots(ctx) ([]DirtySnapshot{ID, MarkedAt})`, `ClearDirtyIssuesIfUnchanged(ctx, []DirtySnapshot)`; publish paths type-assert and skip dirty-clearing when the store doesn't implement it | V2-8: shared `storage.Storage` and the memory backend stay untouched |
| `internal/storage/sqlite/dirty.go` | Implement the two snapshot methods (`DELETE … WHERE issue_id=? AND marked_at=?`) | R6: conditional clear; a round-trip equality test proves the driver preserves `marked_at` fidelity |
| `internal/lockfile/lock.go` (+ platform files) | Context-aware acquisition API (`AcquireContext(ctx, …)`): lock waits return the context error on deadline/cancellation; existing blocking API kept for current callers | R4-4: the divergence path needs interruptible waits; the current API is uninterruptible |
| `internal/jsonlpub/jsonlpub.go` (new; same file as the first row) | Lock-held `contentStateLocked` sampler used by `Publish`'s guard and re-check — never reacquires the lock; the public `ContentState` wraps it with the lock-recheck rule | R4-4: the guard calling the public reader would self-deadlock on the provisional-mismatch path |
| `internal/jsonlpub/jsonlpub_test.go` (new) | Failpoint state-machine tests (V2-9): after pending write, after rename, after committed write, after file-hash write, after timestamp write, after pending delete, after conditional clear — at each state assert `ContentState` (caller-mapping tests live with their callers, R3-12); plus reader lock-recheck under a concurrent publish (V2-1), locked-reader promotion of a crashed pending (R3-4), the diverged-abort guard (R3-2), dirty-first snapshot with a during-builder mutation (R3-3), canonical-path rule incl. relative path and final-component symlink (R3-7), context-cancelled lock wait (R3-8), RecordImport with a parsed-bytes hash differing from the current file (R3-1), RecordImport clearing a crashed publish's pending (V2-5) | The full protocol pinned before any caller migrates |

### Phase 2: Route every reader and writer  —  Gate: `go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ -count=1` pass; the full `-json` run + normalizer → `work/w2_stale-race/artifacts/post_failures.txt`, and `comm -13 work/w2_stale-race/artifacts/baseline_failures.txt work/w2_stale-race/artifacts/post_failures.txt` prints nothing (R3-9)

| File | Change | Why |
|------|--------|-----|
| `internal/autoimport/autoimport.go` | `CheckStaleness` delegates to `ContentState` (diverged→stale; fresh/no-metadata→not stale; interface unchanged for `staleness.go:32`, `server_export_import_auto.go:270,311`). `AutoImportIfNewer`: both metadata tails (:103 touch path, :137-144 post-import) replaced by `RecordImport` carrying the already-computed `currentHash` of the parsed bytes (R3-1), ordered **before** `onChanged`; record failure warns and `onChanged` still runs with its original `needsFullExport` (R3-6) | Defect 3 + V2-4/V2-5/R3-1/R3-6 |
| `cmd/bd/import.go` | Canonical `RecordImport` (parsed-bytes hash) placed immediately after the successful import and **before** the canonical post-import flush at :374-379 (R4-5) — record-before-callback order; the old metadata block at :388-396 is deleted. Only when the import source is the canonical default JSONL (R3-5); `-i backup.jsonl` imports leave canonical metadata untouched. The CLI parser hashes bytes during its streaming read and carries the hash to `RecordImport` (R4-6) | V2-4 + R3-5 + R4-5/R4-6 |
| `cmd/bd/integrity.go` | `hasJSONLChanged` delegates to `ContentState`: diverged→changed, fresh→unchanged, no-metadata with existing file→changed (preserves :134-136); `computeJSONLHash` stays for other callers | R2 + V2-3 |
| `cmd/bd/daemon_sync.go` | `exportToJSONLWithStore` body becomes `Publish` with a snapshot callback; pre-import flush (:569-583) supplies its dirty snapshot (loop dies) and implements the R4-2 divergence branch (dirty + diverged → skip flush → import → `RecordImport` → publish still-dirty snapshot); `importToJSONLWithStore` hashes bytes during its read and returns the hash (R4-6) so the import sites (:633, :854) call `RecordImport` with it; callers at :444/:731 drop single-repo `updateExportMetadata` branches; multi-repo loops byte-identical | Defects 1 + 2 + V2-4 + R4-2/R4-6 |
| `cmd/bd/autoflush.go` | Flush path (:707-749): incremental merge map moves inside the snapshot callback; `writeJSONLAtomic`+manual clear+metadata tail replaced by `Publish` | R4 + V2-2 |
| `cmd/bd/sync.go` | Export block (:1380-1444) replaced by `Publish` | R4 |
| `cmd/bd/export.go` | Default-path branch (:463-476) routed through `Publish`; stdout and custom `-o` paths keep the plain atomic writer | V2-6/V2-7 |
| `internal/rpc/server_export_import_auto.go` | Export sites (:189, :560): `Publish` when the requested path resolves to the canonical default JSONL, plain atomic writer otherwise; `RecordImport` after its import handlers | R4 + V2-7 + V2-4 |
| `internal/autoimport/autoimport_test.go`, `cmd/bd/daemon_sync_test.go` | Regressions: w25 shape (same-bytes rewrite → `CheckStaleness` false); restored-old-file (older mtime, different bytes) → stale; pull-then-import → gate passes (RecordImport); pre-import flush flushes once, dirty count 0, second watcher pass exports nothing; dirty + diverged watcher cycle → import runs, dirty rows publish after (R4-2 starvation regression); concurrent-rewrite-during-import at each caller (R4-6) | Pins the killer, the R1 gap, import coherence, and the starvation branch |

### Phase 3: Deploy + live verify  —  Gate: E2E prints `E2E_PASS` pre-install; `~/.local/bin/bd version` shows the new commit; live probes 6-8 below pass

| Step | Action |
|------|--------|
| 1 | `cp ~/.local/bin/bd ~/.local/bin/bd.cd33f0f3.bak` (rollback artifact; plain read-copy) |
| 2 | `pgrep -af '[b]d daemon' > work/w2_stale-race/artifacts/daemons_before_deploy.txt` — pre-stop inventory as deploy evidence; self-excluding pattern so the recording command never matches itself (R4-10) |
| 3 | `cd <worktree> && go build -ldflags="-X main.Build=$(git rev-parse --short HEAD)" -o /tmp/bd.new ./cmd/bd` |
| 4 | Run the E2E script (Verification) against `/tmp/bd.new` **now, before install** (R4-8) — it must print `E2E_PASS` |
| 5 | `bd daemon --stop-all` (bd's own sanctioned stop), confirm `pgrep -af '[b]d daemon'` prints nothing — **before** install, so nothing executes the file being replaced |
| 6 | `mv /tmp/bd.new ~/.local/bin/bd` (rename, immune to ETXTBSY), then `~/.local/bin/bd version` |
| 7 | Run live probes (Verification 6-8); daemons relaunch on demand on the new binary (settled lock-in scope) |

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `cmd/bd/nodb.go` | Yes | Store-less mode: no database → no metadata, no staleness predicate to protect; keeps plain `writeJSONLAtomic` |
| `cmd/bd/staleness.go` | Yes | Caller of `CheckStaleness`; interface unchanged |
| `cmd/bd/repo.go` | Yes | Its `exportToJSONLWithStore` call (:229) gains the protocol through the shared function body; no site-local edit |
| `internal/storage/sqlite/dirty_helpers.go` | Yes | `markDirty`/`markDirtyBatch` upsert semantics are exactly what the conditional clear relies on; unchanged |
| `internal/storage/memory/memory.go` | Yes | Boolean dirty map stays; `DirtySnapshotStore` is a narrow optional interface it never implements — publish paths skip dirty-clearing for it (V2-8) |
| `internal/storage/storage.go` (shared interface) | Yes | No new methods added to the shared interface; the narrow interface lives in `internal/jsonlpub` |
| supervise-launch.sh (shared-docs skills/manager) and the orchestrator conveyor (external repos) | Yes | Root cause fixed in beads; no armor added, no env quartet baked in; the conveyor's existing one-retry stays as-is |

## Not in Scope

- Upstream PR to steveyegge/beads (user 2026-08-04: "we're not trying to upstream pr anything, just fix our local variant").
- Migration to upstream beads v1.x/Dolt — the architecture that deletes this bug class entirely; recorded as a future research-scale candidate, decoupled per user decision.
- `BD_ALLOW_STALE` environment plumbing — irrelevant once staleness is content-based.
- Multi-repo (`:repoKey`-suffixed) metadata semantics — behavior preserved byte-identical; no repo on this host uses them.

## Execution Handoff

Direct: a single builder works from this plan (~9 source files + 3 test files across three ordered phases). The changes form one atomic protocol — publisher, import twin, readers, and all six writers must land together or the repo is in a mixed single-key/two-key state, so splitting across builders adds integration risk without independent testability; the phase gates give the ordering a bead tree would. Person steps: none; deploy is agent-executable (`bd daemon --stop-all` is bd's own command, not a process kill).

## Rollback

- Binary: `bd daemon --stop-all` first (any bd binary can issue it), then `cp ~/.local/bin/bd.cd33f0f3.bak /tmp/bd.rollback && mv /tmp/bd.rollback ~/.local/bin/bd` — install by rename so a straggler daemon can't ETXTBSY the copy — then `~/.local/bin/bd version` must report `cd33f0f3`. Daemons relaunch on demand on the old binary. Full undo, seconds.
- Source: revert the work branch commits (worktree-local, pre-merge) or `git revert` post-merge.
- Data: forward-compatible — `jsonl_content_hash`/`last_import_time` keep today's meaning; the only new key, `jsonl_pending_hash`, is unknown to the old binary and at most one stale row of metadata (old binary ignores it; steady state has it deleted). A `.beads/.publish.lock` file may remain; it is inert without the new binary. No schema or file-format change.

## Risks

| Risk | Mitigation |
|------|------------|
| Hash on every freshness check (mtime gate removed) | bd-v0y already accepted this cost for `hasJSONLChanged` (~10-50 ms worst case per its comment); fleet's 811 KB file hashes sub-ms |
| Reader lock-recheck: a provisional mismatch now takes the publish lock, so a wedged publisher could block readers | Lock hold time is ms-scale (temp write + 2 metadata ops + rename); `internal/lockfile` carries stale-holder recovery; the recheck path only runs when a mismatch was observed — steady state never locks |
| `marked_at` round-trip fidelity: the conditional `DELETE … AND marked_at=?` silently matches nothing if the driver's time encoding differs between insert and bind | Phase 1 storage test proves insert→snapshot→conditional-delete round-trips on the real SQLite driver before any caller depends on it |
| Publish/import lock interaction: `RecordImport` runs inside import flows that may already hold daemon import locks | Builder maps the existing lock order (daemon import lock → publish lock, never the reverse) and the Phase 1 tests exercise RecordImport under a concurrently-held publish lock; any inversion found is a Phase 2 blocker, not a silent reorder |
| Fork's `go test ./...` has pre-existing failures masking regressions | Phase 1 step 0 records normalized `go test -json` failure signatures (tests + package build failures + command status); Phase 2's gate is an explicit `comm -13` producing zero new signatures |
| A daemon relaunching mid-deploy on the old binary | `--stop-all` before install; install by rename; any respawn after the `mv` execs the new file |
| Accepted residual (R3-4): after a publish crashes between rename and promote, a manual restore of the exact old committed bytes reads fresh until the first locked event (reader promotion, publish, or import) | Requires two rare events in sequence (mid-promote crash, then a byte-exact restore before any locked event); self-corrects at the next publish/import; the persistent-evidence design that would close it costs more state than the transient justifies — revisit only if observed |
| Review ceiling: v5's ten R4 fixes (guard re-check, starvation branch, no-metadata abort, lock-held sampler, import-hash plumbing, script corrections) had no further Codex round — round 4 was the hard cap | Disclosed at the human gate; the Phase 1 protocol test suite exercises every one of these mechanisms (failpoints, locked-divergence, cancellation, starvation regression) before any caller migrates, and the builder surfaces any test-revealed design flaw as a defect rather than patching around it |

## Verification

**Tests (V2-10/R3-10 baseline protocol):**
- Step 0 (before any edit): `mkdir -p work/w2_stale-race/artifacts`, then `go test ./... -count=1 -json > work/w2_stale-race/artifacts/baseline.json 2> work/w2_stale-race/artifacts/baseline_stderr.txt; echo $? > work/w2_stale-race/artifacts/baseline_status.txt`. Builder writes `work/w2_stale-race/artifacts/normalize_failures.py`: reads a go-test-json stream plus the stderr file and emits sorted unique failure signatures — `pkg::Test` for each `"Action":"fail"` event that names a test; `pkg::<build>` **only** when a failing package has no failed test event, or the stderr file carries build/setup evidence for it (R3-10: Go emits a package-level fail event after ordinary test failures too, so a bare package fail event is not build evidence). The normalizer ships with two fixtures — a test-failure stream and a compile-failure stream — and a self-test asserting the distinct signatures. Output → `baseline_failures.txt`.
- After Phase 1: `go test ./internal/jsonlpub/ ./internal/storage/sqlite/ -v` — all pass (publisher + RecordImport state machine, dirty-snapshot round-trip).
- After Phase 2: `go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ -count=1` pass; then the same `-json` run + normalizer → `post_failures.txt`; gate: `comm -13 work/w2_stale-race/artifacts/baseline_failures.txt work/w2_stale-race/artifacts/post_failures.txt` prints nothing AND both status files are recorded.
- Caller-mapping tests placed per package visibility (R3-12): `ContentState` protocol tests in `internal/jsonlpub`; `CheckStaleness` mapping in `internal/autoimport`; `hasJSONLChanged` mapping (unexported) in `cmd/bd`.

**E2E verification (V2-11/R3-11):** the literal script below is the E2E — builder copies it byte-for-byte to `work/w2_stale-race/e2e_scratch.sh`, substitutes nothing, and pastes its full output. Any flag it uses that the built binary rejects is a plan defect to surface, not to silently fix.

```bash
#!/usr/bin/env bash
set -euo pipefail
BD=/tmp/bd.new
S=$(mktemp -d)
count_flushes() { local n; n=$(grep -c "Flushing" "$LOG" 2>/dev/null) || n=0; printf '%s\n' "$n"; }   # R4-7: no double-zero
cleanup() { (cd "$S" && "$BD" daemon --stop >/dev/null 2>&1) || true; echo "SCRATCH_DIR=$S (leave for trash.sh)"; }
trap cleanup EXIT   # R4-9: scratch daemon stopped on success AND failure
cd "$S"
git init -q .
"$BD" init --prefix t --quiet
ID=$("$BD" --no-daemon create "e2e probe" --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
"$BD" --no-daemon --no-auto-flush update "$ID" --status in_progress
D=$(sqlite3 .beads/beads.db "SELECT COUNT(*) FROM dirty_issues"); echo "dirty_after_update=$D"; test "$D" = 1
"$BD" daemon --start --interval 1s
for i in $(seq 1 20); do [ -S .beads/bd.sock ] && break; sleep 0.5; done
LOG=$(ls -1 .beads/daemon-*.log 2>/dev/null | tail -1) || LOG=.beads/daemon.log
B0=$(count_flushes)
cp .beads/issues.jsonl /tmp/e2e_same.$$ && mv /tmp/e2e_same.$$ .beads/issues.jsonl
F=$B0
for i in $(seq 1 20); do F=$(count_flushes); [ "$F" -gt "$B0" ] && break; sleep 0.5; done
D=$(sqlite3 .beads/beads.db "SELECT COUNT(*) FROM dirty_issues"); echo "flushes=$((F-B0)) dirty_after_flush=$D"
test "$((F-B0))" = 1; test "$D" = 0
cp .beads/issues.jsonl /tmp/e2e_same.$$ && mv /tmp/e2e_same.$$ .beads/issues.jsonl
sleep 10
F2=$(count_flushes); echo "flushes_after_second_touch=$((F2-B0))"
test "$((F2-B0))" = 1   # old binary loops once per second here
"$BD" --no-daemon --db "$S/.beads/beads.db" --no-auto-import info --json >/dev/null
echo "same_bytes_info_exit=$?"
printf '%s\n' '{"id":"t-zzz9","title":"divergence probe","status":"open","issue_type":"task"}' >> .beads/issues.jsonl
if "$BD" --no-daemon --db "$S/.beads/beads.db" --no-auto-import info --json >/dev/null 2>/tmp/e2e_err.$$; then
  echo "DIVERGENCE NOT CAUGHT"; exit 1
else
  echo "divergence_caught=yes"; grep -i "out of sync" /tmp/e2e_err.$$ || true
fi
echo E2E_PASS
```

The script runs in Phase 3 step 4, against the built `/tmp/bd.new` **before** it is installed (R4-8). Bounded polling replaces every fixed wait except the 10-second negative check (which must observe absence, not presence). The builder verifies `init --prefix t --quiet` and `create --json` against `$BD --help` before the run and corrects the plan via the normal defect route if a flag differs.

**Proof of behavior** (builder executes and pastes output; artifacts under `work/w2_stale-race/artifacts/`):

| # | Check | Command | Expected |
|---|------|---------|----------|
| 1 | Full protocol state machine (failpoints after every step incl. promote sub-writes) | `go test ./internal/jsonlpub/ -v` | PASS: reader verdicts correct at all seven failpoint states; lock-recheck survives concurrent publish; RecordImport clears crashed pending |
| 2 | Mid-export mutation survives conditional clear | `go test ./internal/storage/sqlite/ -run Dirty -v` | PASS: re-marked row still dirty after clear; round-trip delete matches |
| 3 | Regression pins w25 shape + restored-old-file + import coherence | `go test ./internal/autoimport/ -run 'TestCheckStaleness|TestRecordImport' -v` | PASS: same-bytes rewrite fresh; older-mtime different-bytes stale; pull-then-import passes the gate |
| 4 | Loop dead at unit level | `go test ./cmd/bd/ -run PreImportFlush -v` | PASS: second flush exports nothing, dirty count 0 |
| 5 | No new suite failures | `comm -13 baseline_failures.txt post_failures.txt` (normalized `-json` signatures incl. package build failures) | Empty output; both run statuses recorded |
| 6 | Deployed binary identity | `~/.local/bin/bd version` | Reports the new short HEAD, not `cd33f0f3` |
| 7 | w25 killer gone live | `touch ~/projects/fleet/.beads/issues.jsonl && ~/.local/bin/bd --no-daemon --db ~/projects/fleet/.beads/beads.db --no-auto-import info --json >/dev/null; echo $?` | `0` (R8: `--no-daemon` forces the direct gate; today's binary can exit 1 here) |
| 8 | Daemon quiet on fleet | After one real bead mutation on the new binary: count `JSONL file created` lines in fleet's daemon log over 60 s | ≤1 — no once-per-second rewrites |
| 9 | True divergence still caught live | E2E script step 5 exit code + message | Nonzero with out-of-sync error |
