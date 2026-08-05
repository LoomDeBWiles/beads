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
