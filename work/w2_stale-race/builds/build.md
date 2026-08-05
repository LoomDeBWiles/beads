# Build: work/w2_stale-race/plan_v5.md (Phases 1-2)

## Acceptance

| Criterion | Command | Log | Pass |
|-----------|---------|-----|------|
| 1. Full protocol state machine (failpoints after every step incl. promote sub-writes) | `go test ./internal/jsonlpub/ -v` | [step1.log](step1.log) | Y |
| 2. Mid-export mutation survives conditional clear | `go test ./internal/storage/sqlite/ -run Dirty -v` | [step2.log](step2.log) | Y |
| 3. Regression pins w25 shape + restored-old-file + import coherence | `go test ./internal/autoimport/ -run 'TestCheckStaleness\|TestRecordImport' -v` | [step3.log](step3.log) | Y |
| 4. Loop dead at unit level | `go test ./cmd/bd/ -run PreImportFlush -v` | [step4.log](step4.log) | Y |
| 5. No new suite failures | `comm -13 work/w2_stale-race/artifacts/baseline_failures.txt work/w2_stale-race/artifacts/post_failures.txt` | [step5.log](step5.log) | Y |
| 6. Deployed binary identity | `~/.local/bin/bd version` | — | PENDING (dispatcher, Phase 3) |
| 7. w25 killer gone live | `touch ~/projects/fleet/.beads/issues.jsonl && ~/.local/bin/bd --no-daemon --db ~/projects/fleet/.beads/beads.db --no-auto-import info --json >/dev/null; echo $?` | — | PENDING (dispatcher, Phase 3) |
| 8. Daemon quiet on fleet | After one real bead mutation on the new binary: count `JSONL file created` lines in fleet's daemon log over 60 s | — | PENDING (dispatcher, Phase 3) |
| 9. True divergence still caught live | E2E script step 5 exit code + message | — | PENDING (dispatcher, Phase 3) |

Phase gates (both green, run in order):

| Gate | Command | Log |
|------|---------|-----|
| Phase 1 | `go test ./internal/jsonlpub/ ./internal/storage/sqlite/ -v` | [phase1_gate.log](phase1_gate.log) (status=0) |
| Phase 2 | `go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ -count=1` | [phase2_gate.log](phase2_gate.log) (status=0) |

## Pending User Verification

Checks 6-9 are Phase 3 (deploy). This build did not build to `/tmp/bd.new`, did not stop any
daemon, and did not touch `~/.local/bin`. The dispatcher runs, in order:

1. Build the binary and run the E2E: `work/w2_stale-race/e2e_scratch.sh` is copied byte-for-byte
   from the plan's Verification section and is executable. It expects `BD=/tmp/bd.new` and must run
   **before** the binary is installed. Expected final line: `E2E_PASS`. It also covers check 9
   (`divergence_caught=yes` plus an out-of-sync message).
2. Check 6: `~/.local/bin/bd version` — must report the new short HEAD, not `cd33f0f3`.
3. Check 7: `touch ~/projects/fleet/.beads/issues.jsonl && ~/.local/bin/bd --no-daemon --db ~/projects/fleet/.beads/beads.db --no-auto-import info --json >/dev/null; echo $?` — expect `0`.
4. Check 8: after one real bead mutation on the new binary, count `JSONL file created` lines in
   fleet's daemon log over 60 s — expect ≤1.

The plan requires the builder to verify `init --prefix t --quiet` and `create --json` against
`$BD --help` before the E2E run; that verification needs the Phase 3 binary, so it moves to the
dispatcher along with the run.

## Tests

Package results (`go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ -count=1`, status 0):

```
ok  github.com/steveyegge/beads/cmd/bd              24.728s
ok  github.com/steveyegge/beads/internal/autoimport  0.005s
ok  github.com/steveyegge/beads/internal/rpc         5.509s
```

Full-suite comparison (normalized `go test ./... -count=1 -json` signatures):

- `baseline_status.txt` = 1, `post_status.txt` = 1 (both runs fail only on the pre-existing failure below).
- `comm -13 baseline_failures.txt post_failures.txt` printed **nothing** — no new failures.
- Pre-existing baseline failures, unchanged before and after (environmental: the test makes a git
  config read-only and expects the next write to fail — `e2e_test.go:778: expected error when git
  config is read-only` — which it does not in this environment):
  - `github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E`
  - `github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file`

New tests added:

- `internal/jsonlpub/jsonlpub_test.go` — publisher/reader protocol: reader verdicts at every
  failpoint (temp written, pending recorded, renamed, promoted, both keys, crashed pending), the
  lock-free fast path vs the under-lock recheck against a concurrent publish, `RecordImport`
  clearing a crashed pending key, legacy `last_import_hash` fallback, and `IsCanonicalTarget`.
- `internal/storage/sqlite/dirty_snapshot_test.go` — `GetDirtyIssueSnapshots` /
  `ClearDirtyIssuesIfUnchanged` round-trip, and the mid-export re-mark case: a row dirtied again
  while the export ran is still dirty after the conditional clear.
- `internal/autoimport/autoimport_test.go` — `TestAutoImportIfNewer_RewriteDuringImport`: a rewrite
  landing mid-import records the hash of the bytes that were **parsed**, so the repo reads stale
  afterwards rather than silently accepting the newer file.
- `cmd/bd/daemon_sync_test.go` — `TestPreImportFlushRetiresDirtyMarkers` (second watcher pass
  exports nothing: identical bytes *and* identical ModTime, one `Flushing` line, dirty count 0),
  `TestPreImportFlushDivergedImportsThenPublishes` (diverged file: import first, then republish
  carrying both the external and the local issue), `TestImportRecordsParsedBytesNotRereadFile`.
- `cmd/bd/import_record_test.go` (build tag `integration`) — `bd import -i <canonical>` records the
  canonical content; `bd import -i backup.jsonl` leaves canonical metadata alone.

## Lock Order

Two locks are in play. There is exactly one order and no path inverts it.

1. **Daemon operation mutex** — `operationMu` (`cmd/bd/daemon_sync.go:27`), a process-local
   `sync.Mutex` taken by the three daemon closures that mutate the JSONL: `performExport`
   (`:391`), `performAutoImport` (`:519`), `performSync` (`:685`). Those are its only three
   acquisition sites in the whole tree (`grep -rn operationMu cmd/bd/*.go`).
2. **Publish lock** — the per-file flock acquired by `acquirePublishLock`
   (`internal/jsonlpub/jsonlpub.go:356`), taken by exactly three functions in the package:
   `ContentState`'s recheck (`:232`), `Publish` (`:372`), and `RecordImport` (`:544`).

**Order: `operationMu` → publish lock, always.** Evidence it is never inverted:

- Every daemon path that reaches the publisher does so *inside* a closure that already holds
  `operationMu`: `performExport`/`performSync` → `exportToJSONLWithStore` → `jsonlpub.Publish`
  (`cmd/bd/daemon_sync.go:50`); `performAutoImport` → `jsonlpub.RecordImport`
  (`cmd/bd/daemon_sync.go:197`) and, on the diverged branch, the post-import
  `exportToJSONLWithStore`.
- Nothing inside `internal/jsonlpub` can take `operationMu`: it is an unexported package-level
  variable in `package main` (`cmd/bd`), unreachable from an imported library.
- The `buildIssues` callbacks that run *under* the publish lock are
  `collectIssuesForExport` (`cmd/bd/daemon_sync.go:59`), `gatherExportIssues`
  (`cmd/bd/export.go:402`), and the closures at `cmd/bd/autoflush.go:579`, `cmd/bd/sync.go:1329`,
  `internal/rpc/server_export_import_auto.go:68` and `:453`. None of them locks `operationMu`
  (it does not appear in any of those files), and none re-enters `jsonlpub`, so the publish lock is
  always the innermost lock held.
- The non-daemon callers (CLI export, autoflush, RPC server, `bd sync`) take the publish lock
  without holding `operationMu` at all. That is the same order with the outer lock absent, not an
  inversion.

The publish flock is acquired context-aware (`lockfile.AcquireContext`), so a stuck holder surfaces
as a deadline error on the caller's timeout rather than an unbounded block under `operationMu`.

## Divergences from the plan

All are same-scope and carry no data risk; each is recorded here per the dispatch's divergence rule.

1. **No new `internal/lockfile` platform files.** The plan sketched adding context-aware acquisition
   alongside the existing platform-split implementation. The existing `lock.go` already funnels every
   platform through one acquisition loop, so `AcquireContext` was added there and `Acquire` now calls
   it with a background context. No per-OS file was touched.
2. **`MetadataStore` narrowed.** `jsonlpub` takes a two-method interface (`GetMetadata`/`SetMetadata`)
   rather than the full `storage.Storage`, so the package cannot reach into issue state and the tests
   can drive it with a fake.
3. **File mode unified to 0644.** The pre-existing callers disagreed (0600 in one path). The publisher
   chmods the renamed file to 0644 for everyone: the JSONL is a committed, git-shared artifact.
4. **Sorting centralized in `writeTemp`.** Each caller previously sorted its own slice before writing.
   Sorting inside the publisher is what makes "same content → same hash" a property of the protocol
   rather than a convention every caller must remember.
5. **Mtime-contract tests rewritten**, not deleted: `internal/autoimport/autoimport_test.go` and
   `internal/autoimport/symlink_test.go` asserted the old "newer mtime ⇒ stale" rule that this work
   deliberately removes. They now assert the content rule (same bytes ⇒ fresh regardless of mtime;
   different bytes ⇒ stale regardless of mtime).
6. **Extra `RecordImport` in `cmd/bd/repo.go:232`.** The multi-repo import path parsed a canonical
   JSONL without recording it, which would have left that repo reading diverged on the next check.
   The plan's Changes table did not list this caller; it is the same one-line treatment as the others.
7. **`dropUnencodableIssues` for `SkipEncodingErrors`.** The old export wrote issues one at a time and
   could skip an unencodable row mid-stream. The publisher serializes the whole set, so the skip now
   happens by filtering the slice before the write, preserving the flag's behavior.
8. **Dirty-flag clearing removed from non-canonical RPC export targets.** `bd export -o other.jsonl`
   through the RPC server used to clear dirty markers, which would drop the record of work the
   canonical JSONL had not yet received. Only a canonical publish clears them now.
9. **`gofmt -w cmd/bd/export.go`** — that one file only. The repo is not gofmt-clean at baseline
   (~190 files), so no repo-wide sweep was run.
10. **The two CLI import tests live behind `-tags integration`.** `setupCLITestDB`/`runBDInProcess`
    are themselves in a file with that tag, so `cmd/bd/import_record_test.go` must carry it too.
    Consequence: those two tests are not in the Phase 2 gate; they were run separately and pass
    (`go test -tags integration ./cmd/bd/`).
11. **Three test expectations updated** where the new protocol makes the old assertion false:
    - `cmd/bd/daemon_sync_test.go` `TestUpdateExportMetadataMultiRepo` and
      `TestExportWithMultiRepoConfigUpdatesAllMetadata` asserted the global `jsonl_content_hash` was
      unset. Both tests *simulate* multi-repo by calling the single-repo `exportToJSONLWithStore`
      directly (their stores have no multi-repo config) and then hand-calling `updateExportMetadata`
      with suffixes. The single-repo publisher records the global key by design, and genuine
      multi-repo mode never reaches it — `exportToJSONLWithStore` returns early once
      `ExportToMultiRepo` yields results. The assertion now captures the global value before the
      suffixed writes and requires it unchanged after, which still catches key confusion without
      asserting something the setup itself contradicts.
    - `cmd/bd/export_test.go` "prevent exporting empty database over non-empty JSONL": the new
      no-metadata-plus-existing-file guard refuses before the callback runs, so the empty-database
      message was unreachable. The test now asserts the divergence refusal first (data safety
      preserved: error returned, file untouched), then `RecordImport`s the file so the guard passes
      and the original empty-database message assertion still runs.

## Round 3 (final-review fixes)

Two accepted findings from `final_review_v1.md`, nothing else. Both gates green.

### F1 (med) — `cmd/bd/autoflush.go` auto-import ran on the pre-protocol contract

**What it did before.** `autoImportIfNewer` decided "is the file newer than what I hold?"
by reading `jsonl_content_hash` itself (old :107, with the `last_import_hash` migration
fallback) and comparing it to the hash of the bytes it had just read. It knew nothing about
`jsonl_pending_hash`, the key a publication writes *before* it renames the temp file into
place. So in the window between a publish's rename and its promote — or after a crash there —
the file on disk matches only pending, this comparison called it new, and the CLI re-imported
the database's own just-exported content. It then wrote the committed key back with a bare
`SetMetadata` (old :258) outside the publish lock, which can interleave with a daemon promote
and leave committed describing the pre-import file while the file holds newer bytes: the
`StatusDiverged` that prints "Database out of sync with JSONL" on a healthy repo.
This path runs in `PersistentPreRun` for nearly every command (`cmd/bd/main.go:790,793`) and
in direct mode (`cmd/bd/direct_mode.go:105`), so it is the most-executed reader in the tree.

**What changed.**

- `cmd/bd/autoflush.go:103` — the hand-rolled `sha256`/`hex` hash of the read bytes is now
  `jsonlpub.HashBytes(jsonlData)`. Same value, one hashing authority. This is the hash the
  path carries to the record at the end: the bytes it actually parsed, never a re-hash of the
  file (R3-1).
- `cmd/bd/autoflush.go:105-131` — the single-key comparison is replaced by
  `jsonlpub.ContentState(ctx, store, jsonlPath, "")`. Mapping, per the plan's caller-specific
  rules: `StatusFresh` (file hashes to committed **or** pending) → skip, which is the defect
  fix; `StatusDiverged` (bytes nobody recorded) → import; `StatusNoMetadata` (nothing ever
  recorded) → import, the first-import case; `StatusNoFile` → skip, the file was removed
  between this function's `os.ReadFile` above and the state check, and recording content for
  an absent file would be a lie. The bd-663 recovery is preserved and mapped onto the
  tri-state: a `ContentState` error (metadata or file-hash read failure) logs and falls
  through as `StatusNoMetadata`, i.e. "treat as first import", exactly the old behavior and
  the same direction of error (import rather than silently skip).
- `cmd/bd/autoflush.go:263-272` — the old `:258-268` tail (bare `SetMetadata` of
  `jsonl_content_hash` + `last_import_time`) is replaced by
  `jsonlpub.RecordImport(ctx, store, jsonlPath, currentHash, jsonlpub.Options{})`. That takes
  the publish lock, commits the parsed-bytes hash, retires any pending hash, and stamps
  `last_import_time` in RFC3339Nano — so the racing-promote half of the defect is gone too.

**Behaviors deliberately preserved** (not part of the defect, verified still in place):
the merge-conflict-marker check (:137-155, still runs before parsing, unchanged);
the ID-remapping decision (:255-263 — `markDirtyAndScheduleFullExport` when
`result.IDMapping` is non-empty, `markDirtyAndScheduleFlush` otherwise); the
`ClearAllExportHashes` pre-import call; every stderr message on the parse/import failure
paths. Ordering is unchanged apart from the freshness check now consulting the protocol.

**Regression test** — `cmd/bd/autoimport_test.go:120`
`TestAutoImportIfNewer_PendingHashIsFresh`: writes a JSONL holding one issue, records that
file's hash under `jsonl_pending_hash` and a *different* hash under `jsonl_content_hash`
(the exact mid-publication state), then calls `autoImportIfNewer()` and asserts the issue was
not imported. It fails on the pre-fix tree with the intended message
(`work/w2_stale-race/builds/r3_prefix_test_fails.log`) and passes after
(`work/w2_stale-race/builds/r3_regression_test.log`).

**Gate 1** — `go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ ./internal/jsonlpub/
./internal/storage/sqlite/ -count=1`, log `work/w2_stale-race/builds/r3_gate1.log`, status 0:

```
ok  github.com/steveyegge/beads/cmd/bd                  48.837s
ok  github.com/steveyegge/beads/internal/autoimport      0.006s
ok  github.com/steveyegge/beads/internal/rpc            10.466s
ok  github.com/steveyegge/beads/internal/jsonlpub        0.121s
ok  github.com/steveyegge/beads/internal/storage/sqlite 59.456s
```

**Gate 2** — full-suite comparison, run exactly as the plan's Verification specifies:
`go test ./... -count=1 -json` → `artifacts/post2.json` (stderr `post2_stderr.txt`, status
`post2_status.txt` = 1), `artifacts/normalize_failures.py` → `artifacts/post2_failures.txt`,
then `comm -13 artifacts/baseline_failures.txt artifacts/post2_failures.txt`. Log
`work/w2_stale-race/builds/r3_gate2.log`:

```
=== post2_failures.txt ===
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file
=== comm -13 baseline post2 ===
=== end (empty above = no new failures) ===
```

`comm -13` printed nothing: the only failures are the two pre-existing environmental ones
already in the baseline.

**Divergences from the dispatch: none.** Nothing outside `cmd/bd/autoflush.go` and its test
needed to change; `go build ./...` and `go vet ./cmd/bd/` are clean.

Two consequences of the prescribed fix worth recording, neither a change of scope:

1. `RecordImport` also calls `SetJSONLFileHash`, which this path never did. That is a
   correction, not drift: `autoImportIfNewer` clears `export_hashes` before importing but
   used to leave `jsonl_file_hash` describing the pre-import file, which
   `validateJSONLIntegrity` (`autoflush.go:332`) would later read as a mismatch and warn
   about. It now matches the other import paths (`internal/autoimport` records through the
   same function).
2. `jsonlpub.Options{}` is passed with no `Warnf`, as the dispatch specifies, so the
   publisher's best-effort promote/pending warnings are silent on this path. The one failure
   that matters — `RecordImport` returning an error — still prints the original two-line
   stderr warning.

**One observation, outside this dispatch's scope, not acted on:**
`internal/autoimport.AutoImportIfNewer` (`internal/autoimport/autoimport.go:84-97`) still
makes its *freshness* decision with the same direct `jsonl_content_hash` read (its metadata
*tails* were converted to `RecordImport` in the last commit, which is what the plan's line 177
required of it). It is therefore blind to pending in the same way, though its blast radius is
smaller: it re-imports content the database already holds rather than corrupting the record,
because its write side is now `RecordImport`. Flagging it, changing nothing.

### F2 (low) — nonexistent method name in this report

`work/w2_stale-race/builds/build.md:68` named the storage method `SnapshotDirtyIssues`. The
shipped method is `GetDirtyIssueSnapshots` (`internal/jsonlpub/store.go`,
`internal/storage/sqlite/dirty.go`), which is also the name the plan uses at lines 179 and
192. Renamed. Note: the review said "two occurrences"; the file contained one, at line 68.
`grep -rn "SnapshotDirtyIssues" work/w2_stale-race/builds/build.md` now returns nothing, and
line 68 reads `GetDirtyIssueSnapshots`. No other content in this report was touched.

## Round 4 (freshness-authority sweep)

### What changed and where

One source change: `internal/autoimport/autoimport.go`, inside `AutoImportIfNewer`
(the function `internal/rpc/server_export_import_auto.go:358` calls, i.e. the RPC/daemon
auto-import path).

Before, the function decided freshness itself: it hashed the bytes it had just read, read
`jsonl_content_hash` with a hand-rolled fallback to `last_import_hash` (bd-39o), and
skipped the import only on an exact match with the committed key. That comparison is blind
to `jsonl_pending_hash` — the key a publication writes *before* its rename and promotes to
the committed key *after*. Between those two steps, and after a crash between them, the
file on disk holds bytes the database itself just exported but only `pending` records
them, so this reader called its own export "new" and re-imported it.

After, the decision goes through the publish protocol, mapped exactly as
`cmd/bd/autoflush.go` maps it (Round 3):

```go
status, err := jsonlpub.ContentState(ctx, store, jsonlPath, "")
if err != nil {
        notify.Debugf("content state read failed (%v), treating as first import", err)
        status = jsonlpub.StatusNoMetadata
}
switch status {
case jsonlpub.StatusFresh:
        notify.Debugf("auto-import skipped, JSONL content already recorded")
        recordImport(ctx, store, jsonlPath, currentHash, notify)
        return nil
case jsonlpub.StatusNoFile:
        notify.Debugf("auto-import skipped, JSONL disappeared during check")
        return nil
}
notify.Debugf("auto-import triggered (content %s)", status)
```

Preserved deliberately:

- **The `recordImport` on the unchanged-content path.** `StatusFresh` is the tri-state's
  name for the old `currentHash == lastHash` branch, and it still calls
  `recordImport(..., currentHash, ...)` with the same comment: re-recording refreshes
  `last_import_time`, so a change that only moved the mtime (a `git pull` that rewrote
  identical bytes, a `touch`) stops looking new.
- **The bd-39o migration fallback.** It was not deleted, it moved to where it belongs:
  `jsonlpub.readCommitted` reads `jsonl_content_hash` and falls back to `last_import_hash`.
  A database that only ever wrote the legacy key still reads Fresh.
- **The parsed-bytes hash rule (R3-1).** `currentHash := jsonlpub.HashBytes(jsonlData)` is
  still computed from the bytes this call read and will parse, and is what both
  `recordImport` calls record. Nothing re-hashes the file.
- **Everything below the decision**: merge-conflict check, parse, `importFunc`,
  `showRemapping`, the record-before-callback ordering, the `changed`/`onChanged` block,
  the `recordImport` helper (already on `jsonlpub.RecordImport`), and `CheckStaleness`.

Error direction matches `autoflush.go`: a `ContentState` error maps to `StatusNoMetadata`
("treat as first import"), never to a silent skip, so unreadable metadata recovers by
importing rather than by going quiet.

Second change: one regression test appended to `internal/autoimport/autoimport_test.go`
(`TestAutoImportIfNewer_PendingHashIsFresh`). It writes a JSONL file, sets
`jsonl_pending_hash` to that file's hash and `jsonl_content_hash` to the hash of different
content, calls `AutoImportIfNewer`, and fails if `importFunc` ran.

### Regression test: fails pre-fix, passes post-fix

Pre-fix proof was taken by temporarily restoring the old single-key comparison with the
Edit tool (no `git checkout`, no shell overwrite of a tracked file), running the test, then
restoring the fixed block verbatim. Full output in
`work/w2_stale-race/builds/r4_prefix_test_fails.log` (exit 1):

```
=== RUN   TestAutoImportIfNewer_PendingHashIsFresh
    autoimport_test.go:765: AutoImportIfNewer re-imported content the database had already published (file matched jsonl_pending_hash)
--- FAIL: TestAutoImportIfNewer_PendingHashIsFresh (0.00s)
```

Post-fix, `work/w2_stale-race/builds/r4_regression_test.log`:

```
--- PASS: TestAutoImportIfNewer_PendingHashIsFresh (0.00s)
ok  	github.com/steveyegge/beads/internal/autoimport	0.002s
```

After restoring the fix, `go build ./...` and `go vet ./internal/autoimport/` were clean.

### Gate 1 — targeted packages

`go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ ./internal/jsonlpub/ ./internal/storage/sqlite/ -count=1`,
exit 0. Full output in `work/w2_stale-race/builds/r4_gate1.log`:

```
ok  	github.com/steveyegge/beads/cmd/bd	23.075s
ok  	github.com/steveyegge/beads/internal/autoimport	0.006s
ok  	github.com/steveyegge/beads/internal/rpc	4.755s
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.068s
ok  	github.com/steveyegge/beads/internal/storage/sqlite	27.439s
```

### Gate 2 — full suite vs baseline

`go test ./... -count=1 -json` into `work/w2_stale-race/artifacts/post3.json` (stderr in
`post3_stderr.txt`, exit status in `post3_status.txt`), normalized by the existing
`artifacts/normalize_failures.py` into `artifacts/post3_failures.txt`, then compared.
Full output in `work/w2_stale-race/builds/r4_gate2.log`:

```
=== post3_status.txt === 1
=== post3_failures.txt ===
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file
=== comm -13 baseline post3 ===
=== end (empty above = no new failures) ===
```

`comm -13` printed nothing: no new failures. The two failures present are the same
pre-existing environmental ones in `artifacts/baseline_failures.txt` (the test makes a git
config read-only and expects the next write to fail; on this host, running as a user who
can still write it, it does not).

### Class closure

`grep -rn 'jsonl_content_hash\|last_import_hash' --include=*.go .`, run from the repo root,
excluding `_test.go` files and `internal/jsonlpub/`, leaves six sites. None is a freshness
decision:

| site | what it is | why it is not a freshness decision |
|---|---|---|
| `internal/importer/importer.go:69` | doc comment ("The caller is responsible for ... setting metadata (e.g., last_import_hash)") | prose, no code |
| `cmd/bd/import_shared.go:196` | doc comment, same sentence | prose, no code |
| `cmd/bd/autoflush.go:108` | comment in the Round 3 fix explaining why the key is not read directly | prose, no code |
| `internal/autoimport/autoimport.go:90,92` | comment in this round's fix, same explanation | prose, no code |
| `cmd/bd/daemon_event_loop.go:242-243` | daemon health check: `if _, err := store.GetMetadata(ctx, "jsonl_content_hash"); err != nil { if _, err := store.GetMetadata(ctx, "last_import_hash"); err != nil { log... } }` | it discards both values and only inspects `err`. Nothing is compared to a file hash and no import/export/skip follows; the failure branch only logs "metadata read failed" and continues. It asks "is metadata readable", not "is the file fresh". Untouched. |
| `cmd/bd/daemon_sync.go:247,252,253,257,280,281` | `updateExportMetadata`, a metadata **writer** for multi-repo suffixed keys (`hashKey := "jsonl_content_hash"` then `hashKey += ":" + keySuffix`) plus its doc comments | it only ever calls `SetMetadata`; it reads no recorded hash and decides nothing. It is reached solely from `performExport` for `getMultiRepoJSONLPaths()`, which is `nil` without a multi-repo config, so single-repo publishing never touches it. Plan v5 preserves multi-repo metadata byte-identical, so it stays exactly as it is. Untouched. |

No other freshness decision on these keys remains outside tests and `internal/jsonlpub`.

### Divergences and observations

No divergence from the dispatch: one source file changed, one test added, nothing else.
Two things noticed and deliberately not changed:

1. **Pre-existing gofmt violation in the touched file.** `gofmt -l` flags
   `internal/autoimport/autoimport.go`, but the entire diff is a trailing-whitespace line
   inside `showRemapping` (line 185), which predates this work and is nowhere near the
   change. Out of scope; left alone rather than mixed into this commit.
2. **A narrow race that the preserved `StatusFresh` `recordImport` keeps.** Between the
   `os.ReadFile` and the `ContentState` call, another writer could replace the file. On the
   Fresh path the code then records `currentHash`, the hash of the bytes it read, which by
   then describes content no longer on disk. This is the behavior the dispatch asked to
   preserve, and it self-heals: the next reader sees the new file hashing to neither
   recorded key, reads `StatusDiverged`, and imports. Recorded as an observation, not fixed.

## Round 5 (Fresh-path record removal)

Applies accepted finding F1 from `final_review_v2.md`. Round 4's dispatch told me to keep
the `recordImport` call on the `StatusFresh` path; that instruction was wrong and this
round removes it. Round 4's observation 2 above ("self-heals") is retracted: the healing
only happens on the *next* `AutoImportIfNewer`, while `CheckStaleness`/`ensureDatabaseFresh`
and `jsonlpub.Publish` read the poisoned committed key first and fail loudly in between.

### What changed

**`internal/autoimport/autoimport.go`, the `StatusFresh` branch (was line 101-106).** Deleted
the `recordImport(ctx, store, jsonlPath, currentHash, notify)` call and the comment above it
that claimed the record "refreshes the import timestamp so a mtime-only change stops looking
new". The branch now logs and returns, matching `cmd/bd/autoflush.go`'s Fresh branch. The
replacement comment states the rule: this path parses nothing, `currentHash` covers the bytes
`os.ReadFile` returned while the Fresh verdict came from the protocol's own re-read
(`jsonlpub.sampleState` → `HashFile`), so recording one as the other commits a hash for
content the database never took in and `clearPending` destroys the record describing what is
actually on disk.

Nothing else changed: `StatusNoFile`, `StatusDiverged`, `StatusNoMetadata`, the error-direction
mapping, and the post-import `recordImport` (now line 141) are untouched.

### `last_import_time` grep

`grep -rn "last_import_time\|LastImportTime\|lastImportTime" --include=*.go .` returns 19 hits.
Non-test hits are exactly four, and none is a read:

| site | what it is |
|---|---|
| `internal/jsonlpub/jsonlpub.go:45` | the key constant `importTimeKeyBase` |
| `cmd/bd/daemon_sync.go:282` | `timeKey := "last_import_time"` in `updateExportMetadata`, a multi-repo `SetMetadata` writer |
| `cmd/bd/daemon_sync.go:252,253` | comments listing the keys |
| `cmd/bd/autoflush.go:266` | comment ("...retires a pending hash and stamps last_import_time") |

The remaining 15 are `_test.go` files that *set* the key as fixture state (`autoimport/symlink_test.go`,
`autoimport/autoimport_test.go`, `cmd/bd/git_sync_test.go`) or assert the multi-repo writer wrote it
(`cmd/bd/daemon_sync_test.go:358-363,606,627`). No non-test code reads the timestamp, so removing the
refresh changes no decision anywhere.

No existing test asserted the deleted recording behavior; nothing was rewritten to make the gates pass.

### Regression test

Added `TestAutoImportIfNewer_FreshPathRecordsNothing` (`internal/autoimport/autoimport_test.go`).
It sets up a publication caught between its rename and its promote: the file holds the published
bytes, `jsonl_pending_hash` records them, and `jsonl_content_hash` still names the previous
content. `ContentState` returns Fresh from the pending key, and the test asserts both metadata
keys are byte-identical afterwards (and that no import ran).

Pre-fix (old branch restored with the Edit tool, never `git checkout`) — `work/w2_stale-race/builds/r5.prefix_test.log`:

```
=== RUN   TestAutoImportIfNewer_FreshPathRecordsNothing
    autoimport_test.go:812: jsonl_content_hash = "4dd9b953...221a458e", want it untouched at "9c2afbc4...7326aa75"
    autoimport_test.go:820: jsonl_pending_hash = "", want it untouched at "4dd9b953...221a458e"
--- FAIL: TestAutoImportIfNewer_FreshPathRecordsNothing (0.00s)
FAIL	github.com/steveyegge/beads/internal/autoimport	0.001s
```

Both halves of the defect show: the committed key is overwritten with the hash of bytes this
call never parsed, and the pending record that described the real on-disk content is destroyed.
Post-fix: `--- PASS`.

### Gates

Gate 1 — `go test ./cmd/bd/ ./internal/autoimport/ ./internal/rpc/ ./internal/jsonlpub/ ./internal/storage/sqlite/ -count=1`
(`builds/r5.gate1.log`), exit 0:

```
ok  	github.com/steveyegge/beads/cmd/bd	21.945s
ok  	github.com/steveyegge/beads/internal/autoimport	0.005s
ok  	github.com/steveyegge/beads/internal/rpc	4.099s
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.063s
ok  	github.com/steveyegge/beads/internal/storage/sqlite	23.268s
```

Gate 2 — full suite into `artifacts/post4.json` (stderr `post4_stderr.txt`, 0 bytes; status
`post4_status.txt` = 1), normalized to `artifacts/post4_failures.txt` (`builds/r5.gate2.log`):

```
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file

$ comm -13 work/w2_stale-race/artifacts/baseline_failures.txt work/w2_stale-race/artifacts/post4_failures.txt
(no output)
```

Same two pre-existing environmental failures as the baseline, no new ones.

### Divergence

**One, in how the regression test reaches the Fresh state.** The dispatch asked for a test where
the file's bytes differ from the bytes the caller read. There is no seam to inject that
deterministically: between `os.ReadFile` (line 76) and `HashFile` (the first statement of
`jsonlpub.sampleState`) the only code is the pure `HashBytes` call, so no store wrapper,
notifier, or callback can rewrite the file in that window, and a goroutine racing it would be
flaky. The test instead reaches Fresh through the pending key, which produces the identical
observable defect (a committed key overwritten with a hash this call did not parse, plus a
destroyed pending record) deterministically, and asserts exactly what the dispatch specified:
`jsonl_content_hash` and `jsonl_pending_hash` both untouched.

Round 4's observation 1 still stands: `gofmt -l` flags `internal/autoimport/autoimport.go` for a
pre-existing trailing-whitespace line in `showRemapping`, unrelated to this change and left alone.

## Round 6 (regression test strengthened)

Applies accepted finding F1 from `final_review_v3.md`. Only
`TestAutoImportIfNewer_FreshPathRecordsNothing` in
`internal/autoimport/autoimport_test.go` changed. No production code, no other test:
`git diff --stat` is `internal/autoimport/autoimport_test.go | 58 +++++, 28 ---`, and
`git diff internal/autoimport/autoimport.go` is empty.

### Retraction of Round 5's "no seam exists" claim

Round 5's Divergence section said there is no deterministic seam to make the file's bytes
differ from the bytes the caller read, because "between `os.ReadFile` (line 76) and
`HashFile` (the first statement of `jsonlpub.sampleState`) the only code is the pure
`HashBytes` call". That is wrong and is withdrawn. The window that matters is not before
`HashFile` but after it: `sampleState`
(`internal/jsonlpub/jsonlpub.go:244-260`) hashes the file at :245 and only then reads
metadata — `readCommitted` at :253, the pending `GetMetadata` at :257. A store wrapper
whose `GetMetadata` rewrites the file therefore fires strictly inside the check, with no
goroutine and no timing dependency. Round 5's substitution was unnecessary, and the test it
produced pinned the implementation rule ("the Fresh branch writes nothing") instead of the
harm rule ("never commit a hash for content the database did not parse").

### The new fixture's mechanism

`swapOnFirstRead` embeds `storage.Storage` and overrides `GetMetadata` so the very first
metadata read — whoever makes it — replaces the JSONL file's bytes exactly once, then
delegates. `AutoImportIfNewer` makes no metadata call before `jsonlpub.ContentState`, so
that first read is `readCommitted`'s, inside the first `sampleState`.

Fixture state: the file holds bytes `read`; the wrapper swaps in bytes `published`;
`jsonl_pending_hash` = hash(`published`); `jsonl_content_hash` = hash of an older
`"previous content\n"`. hash(`read`) matches neither key.

The resulting call ordering, all on one goroutine:

1. `AutoImportIfNewer` reads the file (`read`) and computes `currentHash` = hash(`read`).
2. `ContentState` → `sampleState`: `HashFile` still sees `read`. `readCommitted` fires the
   swap; the file on disk is now `published`. hash(`read`) matches neither committed nor
   pending, so this sample is Diverged — provisional, exactly as the protocol intends.
3. `ContentState` takes the publish lock and re-samples. `HashFile` now sees `published`,
   which matches the pending key: **Fresh**. Because `published` differs from the committed
   key, `contentStateLocked` promotes it — committed becomes hash(`published`), pending is
   cleared. The database's record and the file now agree.
4. `AutoImportIfNewer` receives `StatusFresh` and takes the Fresh branch holding
   `currentHash` = hash(`read`) — a hash of content that is neither on disk nor recorded
   anywhere.

The test asserts the swap actually fired (`store.swapped`) so the fixture cannot silently
stop exercising the window, and then asserts the post-condition the finding names:
`jsonlpub.ContentState(...)` still returns `StatusFresh`.

### Pre-fix failure

The deleted `recordImport(ctx, store, jsonlPath, currentHash, notify)` call was restored on
the Fresh branch with the Edit tool (never `git checkout`), the test run, then the fixed code
restored verbatim — `git diff internal/autoimport/autoimport.go` confirms it is byte-identical
to `1e2128ae6`. Full output, `builds/r6.prefix_test.log`:

```
=== RUN   TestAutoImportIfNewer_FreshPathRecordsNothing
    autoimport_test.go:849: ContentState = diverged after a Fresh auto-import, want fresh: the Fresh branch recorded a hash for bytes it never parsed, so a healthy repository now reports being out of sync with its JSONL
--- FAIL: TestAutoImportIfNewer_FreshPathRecordsNothing (0.01s)
FAIL	github.com/steveyegge/beads/internal/autoimport	0.010s
FAIL
```

`diverged`, not merely a changed metadata value. That is the state behind
`cmd/bd/staleness.go:48`'s "database is out of sync with JSONL" and behind `Publish`'s
`ErrDiverged`: the pre-fix line overwrote the just-promoted committed hash with hash(`read`)
and cleared pending, leaving the on-disk `published` bytes matching no recorded key. Post-fix
the branch records nothing, the promote from step 3 stands, and the same assertion passes:

```
=== RUN   TestAutoImportIfNewer_FreshPathRecordsNothing
--- PASS: TestAutoImportIfNewer_FreshPathRecordsNothing (0.00s)
PASS
ok  	github.com/steveyegge/beads/internal/autoimport	0.002s
```

The test now also permits a legitimate future implementation that promotes a lingering
pending hash on the fresh path, since it constrains the resulting content state rather than
which keys were touched.

### Gates

Gate 1 — `go test ./internal/autoimport/ ./internal/jsonlpub/ ./cmd/bd/ -count=1`
(`builds/r6.gate1.log`), exit 0:

```
ok  	github.com/steveyegge/beads/internal/autoimport	0.006s
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.065s
ok  	github.com/steveyegge/beads/cmd/bd	17.344s
```

Gate 2 — full suite into `artifacts/post5.json` (stderr `post5_stderr.txt`, 0 bytes; status
`post5_status.txt` = 1), normalized by `artifacts/normalize_failures.py` into
`artifacts/post5_failures.txt` (`builds/r6.gate2.log`):

```
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file

$ comm -13 work/w2_stale-race/artifacts/baseline_failures.txt work/w2_stale-race/artifacts/post5_failures.txt
(no output)
```

The same two pre-existing environmental failures as the baseline, no new ones.

`gofmt -l internal/autoimport/autoimport_test.go` is clean.
