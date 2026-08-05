# Final Review v1: work/w2_stale-race

VERDICT: FIX findings=2

| ID | sev | file:line | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|
| F1 | med | cmd/bd/autoflush.go:107,258 | The CLI's own auto-import (`autoImportIfNewer`, run in `PersistentPreRun` for nearly every command) still reads `jsonl_content_hash` directly and writes it back with a bare `SetMetadata` outside the publish lock, so it is blind to `jsonl_pending_hash` and can commit a hash for bytes that are no longer in the file. | `grep '"jsonl_content_hash"' --include=*.go` outside `internal/jsonlpub` returns only `autoflush.go:107` (read), `autoflush.go:258` (write) and the multi-repo `updateExportMetadata`; `git show HEAD^:cmd/bd/autoflush.go` shows the same two calls at old lines 257/265, i.e. this commit replaced the export tail of this file but left its import tail on the pre-protocol contract. Live via `cmd/bd/main.go:790,793` and `cmd/bd/direct_mode.go:105`. Plan line 177 routes `internal/autoimport`, `cmd/bd/import.go` and `daemon_sync.go:633,854` through `RecordImport` but never names this fourth import path, and it is absent from both the eleven divergences and "Files NOT Affected". Two reachable consequences: (a) between a publish's rename and its promote (or after a crash there) the file matches only `pending`, so this reader calls it newer and re-imports the database's own just-exported bytes; (b) an unlocked write of the committed key racing a daemon promote leaves committed = hash of the pre-import file while the file holds newer bytes, which is exactly the `StatusDiverged` that makes `cmd/bd/staleness.go:48` print "Database out of sync with JSONL" on a healthy repo. | Give `autoImportIfNewer` the same one-line treatment as `internal/autoimport.AutoImportIfNewer`: read through `jsonlpub.ContentState` and replace the `:258-268` metadata tail with `jsonlpub.RecordImport(ctx, store, jsonlPath, currentHash, jsonlpub.Options{})`. |
| F2 | low | work/w2_stale-race/builds/build.md:68 | The build report's Tests section names a storage method `SnapshotDirtyIssues` that does not exist in the tree. | The shipped method is `GetDirtyIssueSnapshots` (`internal/jsonlpub/store.go`, `internal/storage/sqlite/dirty.go`), which is also the name the plan uses at line 179 and line 192. | Rename the two occurrences in build.md to `GetDirtyIssueSnapshots`. |

Everything else in the five checks passed; the notes below record what was verified and, where an anomaly was chased, why it is not a finding.

**Check 1 (User Intent).** The w25 message has exactly one source: `cmd/bd/staleness.go:48` inside `ensureDatabaseFresh`, fed by `autoimport.CheckStaleness`, which now ends `return status == jsonlpub.StatusDiverged, nil` (`internal/autoimport/autoimport.go:285`). Mtime plays no part in the verdict; `touch` on recorded content cannot trip it. A healthy repo can still read the message only through F1's race.

**Check 4 (integration).** All six canonical writers reach `jsonlpub.Publish`; `hasJSONLChanged` (`cmd/bd/integrity.go:107-126`) and `CheckStaleness` map the quad-state as the plan specifies; `RecordImport` is called at every canonical import site including the plan-unlisted `cmd/bd/repo.go:232` (divergence 6, correct: guarded by `importedHash != ""`, which `importToJSONLWithStore` returns only in multi-repo mode). `DirtySnapshotStore` is type-asserted with a no-clear fallback. `ResolveCanonical` resolves parent directories but not the final component, per R3-7.

*Lock order, verified independently of the build report.* `grep -rn operationMu cmd/bd/*.go` gives exactly three acquisition sites (`daemon_sync.go:391`, `:519`, `:685`); the publish flock has exactly three (`jsonlpub.go:232`, `:372`, `:544`). `internal/jsonlpub` cannot reach `operationMu` — it is an unexported variable in `package main`. No `buildIssues` callback re-enters `jsonlpub`. Order is `operationMu` → publish flock, never inverted. The report's claim holds.

*Divergence 8 (dirty clearing dropped from non-canonical RPC targets)* is a correct consequence of the design: clearing dirty markers for a write to `other.jsonl` would discard the record of work the canonical JSONL has not yet received, and multi-repo export never cleared them before or after this commit.

*Divergence 11 and divergence 5 (rewritten test expectations)* are correct consequences, not silenced regressions. The two multi-repo tests simulate multi-repo by calling the single-repo `exportToJSONLWithStore` on stores with no multi-repo config; the publisher records the unsuffixed key by design and genuine multi-repo returns early at `daemon_sync.go:41` before reaching `Publish`, so the old `globalHash == ""` assertion contradicted its own setup — the replacement captures the value before the suffixed writes and requires it unchanged, still catching key confusion. `cmd/bd/export_test.go` was strengthened, not weakened: it now asserts the divergence refusal *and*, after a `RecordImport`, the original empty-database message. The autoimport and symlink test rewrites deleted assertions that encoded the very "newer mtime ⇒ stale" rule this work abolishes, and replaced them with content assertions in both directions.

*Chased and dismissed:* `Publish` skips `clearPending` on rename failure where the guard path clears it (`jsonlpub.go:430-437`) — benign in every reachable state, because a pending hash always describes database-derived content, so a file matching a stale pending yields the same verdict either way. `cmd/bd/export.go`'s stdout path clearing dirty markers and its `output == findJSONLPath()` string comparison are both pre-existing (`git show HEAD^:cmd/bd/export.go`, old lines 434/440/504). A fresh repo cannot fall into the RPC non-canonical branch: `internal/utils/path.go:19-49` never returns `""`. Single-repo mode writes no metadata outside the publish lock, because `performExport` only calls `updateExportMetadata` for `getMultiRepoJSONLPaths()`, which is `nil` without a multi-repo config (`cmd/bd/deletion_tracking.go:171-175`).

Phase 3 (build to `/tmp/bd.new`, E2E run, daemon stop, install, live probes 6-9) was not run and is not counted as missing; nothing outside this worktree was touched.

## Verification Output

### Check 2 — plan Verification commands 1-5

```
$ go test ./internal/jsonlpub/ -count=1
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.064s

$ go test ./internal/storage/sqlite/ -run Dirty -count=1
ok  	github.com/steveyegge/beads/internal/storage/sqlite	0.982s

$ go test ./internal/autoimport/ -run "TestCheckStaleness|TestRecordImport" -count=1
ok  	github.com/steveyegge/beads/internal/autoimport	0.003s

$ go test ./cmd/bd/ -run PreImportFlush -count=1
ok  	github.com/steveyegge/beads/cmd/bd	0.136s
```

`go test ./internal/jsonlpub/ -v -count=1` (run earlier, same result) shows the full protocol suite present and green: `TestPublishFailpointStates`, `TestLockedReaderPromotesCrashedPending`, `TestContentStateRechecksUnderLock`, `TestPublishAbortsOnDivergence`, `TestPublishAbortsOnUnrecordedFile`, `TestPublishOntoAbsentFileSucceeds`, `TestPublishRechecksGuardHashBeforeRename`, `TestPublishKeepsIssueDirtyWhenRemarkedDuringExport`, `TestPublishClearsDirtyMarkers`, `TestPublishWithoutDirtySnapshotStore`, `TestPublishAbortsWhenPendingWriteFails`, `TestAcquireWaitRespectsContext`, `TestRecordImportUsesParsedHash`, `TestRecordImportClearsCrashedPending`, `TestRecordImportThenPublish`, `TestContentStateMigrationKey`, `TestContentStateNoFile`, `TestContentStateNoMetadata`. `TestPreImportFlushDivergedImportsThenPublishes` logs the starvation branch end to end: "Pre-import flush skipped: JSONL diverged, importing first" → "Imported from JSONL" → "Flushed dirty issues after import".

### Check 3 — E2E script matches the plan byte-for-byte

```
$ cd work/w2_stale-race && sed -n '277,311p' plan_v5.md > /tmp/plan_e2e_final.sh && diff /tmp/plan_e2e_final.sh e2e_scratch.sh
diff_exit=0
   1969 /tmp/plan_e2e_final.sh
   1969 e2e_scratch.sh
   3938 total
-rwxrwxr-x 1 ben ben 1969 Aug  5 15:37 e2e_scratch.sh
```

Identical, and executable. No difference to report.

### Check 5 — full suite vs baseline

```
$ go test ./... -count=1 -json > /tmp/review_post.json 2> /tmp/review_post_stderr.txt; echo $?
1
$ python3 work/w2_stale-race/artifacts/normalize_failures.py /tmp/review_post.json /tmp/review_post_stderr.txt > /tmp/review_post_failures.txt
$ comm -13 work/w2_stale-race/artifacts/baseline_failures.txt /tmp/review_post_failures.txt
(no output)

$ cat /tmp/review_post_failures.txt
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file

$ cat work/w2_stale-race/artifacts/baseline_failures.txt
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file
```

My own post-run reproduces the builder's result exactly: the same two pre-existing environmental failures (the test makes a git config read-only and expects the next write to fail; on this host it does not), and no new ones. The builder's own `comm -13 baseline_failures.txt post_failures.txt` also printed nothing.
