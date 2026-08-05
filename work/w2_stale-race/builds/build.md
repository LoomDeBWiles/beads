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
- `internal/storage/sqlite/dirty_snapshot_test.go` — `SnapshotDirtyIssues` /
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
