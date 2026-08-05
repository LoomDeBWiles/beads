# Final Review v3: work/w2_stale-race

VERDICT: FIX findings=1

| ID | sev | file:line | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|
| F1 | low | internal/autoimport/autoimport_test.go:766 (`TestAutoImportIfNewer_FreshPathRecordsNothing`) | The new regression test's fixture never puts the database in the state the round-2 finding described: pre-fix, `recordImport` in this fixture wrote the hash of the bytes that are actually on disk and cleared pending, which is byte-for-byte the healthy promoted steady state, so the test pins the implementation rule "the Fresh branch writes nothing" instead of the harm rule "never commit a hash for content the database did not parse." | The fixture writes one `published` line to the file, sets `jsonl_pending_hash` to `HashBytes(published)` and `jsonl_content_hash` to `HashBytes("previous content\n")`; I hashed those myself: the file hashes to `4dd9b95375ba29104a4aceb1f24c0f9df9fffe9626f067ff0b319fba221a458e` and the stale committed value is `9c2afbc4…7326aa75`. The builder's own pre-fix run (`builds/build.md` Round 5, "pre-fix failure log") records the deleted line producing `jsonl_content_hash = 4dd9b953…221a458e` and `jsonl_pending_hash = ""` — identical to what `promote()` writes at `internal/jsonlpub/jsonlpub.go:315-342`, so a `ContentState` call after the pre-fix code returns `StatusFresh`, not `StatusDiverged`: no message at `cmd/bd/staleness.go:48`, no `ErrDiverged` from `Publish`, no user-visible harm. The test therefore proves only that the branch is silent, and it would equally fail a legitimate future implementation that promotes a lingering pending hash on the fresh path. The build report's divergence note ("no seam exists" to inject the file-replacement window) is also wrong: `jsonlpub.sampleState` calls `HashFile(jsonlPath)` **before** its two `GetMetadata` reads, so a `storage.Storage` wrapper whose `GetMetadata` swaps the file fires strictly inside the window. | Reach Fresh through that seam instead — wrap the store so its first `GetMetadata` call replaces the file's bytes (pending = hash of the bytes `AutoImportIfNewer` read, committed = hash of the bytes swapped in), so the pre-fix code commits a hash matching neither the file nor any recorded content, and assert the post-condition `jsonlpub.ContentState(...) == StatusFresh` (which the deleted line turns into `StatusDiverged`). |

Everything else in the four scoped items and three checks passed. Notes below record what was verified independently, and where an anomaly was chased, why it is not a finding.

**Item 1 — the commit.** `1e2128ae6` touches exactly two files. In `internal/autoimport/autoimport.go` it deletes the `recordImport(ctx, store, jsonlPath, currentHash, notify)` call from the `StatusFresh` branch and replaces the old comment with one stating the parsed-bytes rule; nothing else in the function changed. `currentHash` is still computed at :85 and still consumed by the surviving post-import `recordImport` at :141, which is placed before `onChanged` as the plan requires. In `internal/autoimport/autoimport_test.go` it adds 55 lines: the new test and nothing else. No production behavior outside the Fresh branch moved.

**Item 2 — round 2's F1 landed.** `internal/autoimport/autoimport.go:101-107` is now `case jsonlpub.StatusFresh:` → `notify.Debugf(...)` → `return nil`. That is exactly the recommended fix, and it makes the daemon/library path identical to the sibling CLI path at `cmd/bd/autoflush.go:119-121`, which has always returned without recording. The two auto-import paths now agree on all four `Status` values.

**Item 3 — the deletion breaks nothing, re-derived from scratch.** I ran my own `grep -rn 'last_import_time\|LastImportTime\|lastImportTime' --include=*.go .`: 18 hits. Excluding `_test.go`, the survivors are `internal/jsonlpub/jsonlpub.go:45` (the key constant), `:201,210,333,334,556,557` (writes inside `Publish`, `promote`, and `RecordImport`), `cmd/bd/daemon_sync.go:252,253` (comment) and `:282` (a `SetMetadata` write in the multi-repo-only `updateExportMetadata`), and `cmd/bd/autoflush.go:266` (comment). **No non-test code reads the value** — nothing compares it, branches on it, or renders it — so the report's claim holds and refreshing the timestamp on the unchanged-content path had no consumer.

The second half of the question — does anything depend on the Fresh branch clearing a pending hash — is also no, and for a stronger reason than "nobody reads it." A lingering `jsonl_pending_hash` only *widens* the set of file contents that read Fresh (`sampleState` accepts committed **or** pending), so leaving one in place cannot manufacture a `StatusDiverged` verdict and cannot reach `cmd/bd/staleness.go:48`. It is cleared on the next locked event either way: `contentStateLocked` promotes whenever `fileHash != committed`, and `RecordImport` calls `clearPending` unconditionally. The bd-39o legacy-key migration is not lost either, because `readCommitted` falls back to `last_import_hash` on every read rather than depending on a one-time rewrite.

The one real side effect of the deletion is that `jsonl_file_hash` (`SetJSONLFileHash`) is no longer refreshed on the fresh path. Its only non-test reader is `validateJSONLIntegrity` (`cmd/bd/autoflush.go:334-387`, reached only from `flushToJSONLWithState:499`), which on mismatch prints the pre-existing bd-160 warning and forces a full export — a warning plus extra work, never a fail-stop, and the CLI auto-import path has behaved this way for its entire life. Not a finding.

**Item 4 — the substituted test.** Judged weaker than the finding it documents; that is F1. To be explicit about what it still buys: it does catch a straight reintroduction of the deleted line, and it is a valid characterization of the branch as written. What it does not do is demonstrate the failure — the fixture's pre-fix end state is the healthy state — so a future reader cannot learn from it why the line was dangerous, and it will also reject a correct promote-on-fresh implementation. The claimed absence of a seam is refuted above by the call order inside `sampleState`; the substitution was unnecessary.

**Check 1 (User Intent) — every route to the message.** `grep -rn "out of sync with JSONL" --include=*.go .` returns `cmd/bd/staleness.go:48` and one changelog string in `cmd/bd/info.go:414`. The live one is reached only from `ensureDatabaseFresh` (`cmd/bd/staleness.go:20`), whose callers are `search.go:179`, `info.go:96`, `show.go:36`, `stale.go:71`, `ready.go:153`, `count.go:110`, `duplicates.go:44`, `list.go:326`, `status.go:79`. Every one of them funnels through `autoimport.CheckStaleness`, which ends `return status == jsonlpub.StatusDiverged, nil` (`internal/autoimport/autoimport.go:299`). The two RPC callers of `CheckStaleness` (`internal/rpc/server_export_import_auto.go:252,293`) never emit the message. No mtime comparison survives on the route: `isJSONLNewer`/`isJSONLNewerWithStore` (`cmd/bd/integrity.go:29-70`) are dead — their only callers are each other. So the message now requires a file whose bytes hash to neither the committed nor the pending key, and every `RecordImport` call site feeds it a parsed-bytes hash under a canonical-target guard (`internal/autoimport/autoimport.go:141`, `cmd/bd/autoflush.go:267`, `cmd/bd/repo.go:232`, `cmd/bd/import.go:394`, `cmd/bd/daemon_sync.go:197`). With `1e2128ae6` the last remaining way for a healthy repo to reach it — round 2's F1 — is closed. F1 of this round is about test strength, not about a live route.

**gofmt.** `gofmt -l internal/autoimport/autoimport.go` flags the file, but its trailing-whitespace lines are byte-identical to `main`'s and many untouched `cmd/bd` files are flagged the same way; pre-existing repo state, not introduced here.

Phase 3 (build to `/tmp/bd.new`, E2E run, daemon stop, install to `~/.local/bin/bd`, live probes 6-9) was not run and is not counted as missing. Nothing outside this worktree was touched; no fixes were applied.

## Verification Output

### Check 2 — plan Verification commands 1-4

```
$ go test ./internal/jsonlpub/ -count=1
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.063s

$ go test ./internal/storage/sqlite/ -run Dirty -count=1
ok  	github.com/steveyegge/beads/internal/storage/sqlite	1.000s

$ go test ./internal/autoimport/ -run "TestCheckStaleness|TestRecordImport" -count=1 -v
=== RUN   TestCheckStaleness_NoJSONLFile
--- PASS: TestCheckStaleness_NoJSONLFile (0.00s)
=== RUN   TestCheckStaleness_NoMetadata
--- PASS: TestCheckStaleness_NoMetadata (0.00s)
=== RUN   TestCheckStaleness_ContentRecorded
--- PASS: TestCheckStaleness_ContentRecorded (0.00s)
=== RUN   TestCheckStaleness_ContentDiverged
--- PASS: TestCheckStaleness_ContentDiverged (0.00s)
=== RUN   TestCheckStaleness_PendingHashMatches
--- PASS: TestCheckStaleness_PendingHashMatches (0.00s)
=== RUN   TestCheckStaleness_LegacyHashKey
--- PASS: TestCheckStaleness_LegacyHashKey (0.00s)
=== RUN   TestCheckStaleness_MtimeOnlyChange
--- PASS: TestCheckStaleness_MtimeOnlyChange (0.00s)
=== RUN   TestCheckStaleness_TouchedAfterExport
--- PASS: TestCheckStaleness_TouchedAfterExport (0.00s)
=== RUN   TestCheckStaleness_MetadataError
--- PASS: TestCheckStaleness_MetadataError (0.00s)
=== RUN   TestCheckStaleness_EmptyFile
--- PASS: TestCheckStaleness_EmptyFile (0.00s)
PASS
ok  	github.com/steveyegge/beads/internal/autoimport	0.003s

$ go test ./cmd/bd/ -run PreImportFlush -count=1
ok  	github.com/steveyegge/beads/cmd/bd	0.191s
```

The commit's own tests plus their neighbours, run explicitly:

```
$ go test ./internal/autoimport/ -run "TestAutoImportIfNewer_RewriteDuringImport|TestAutoImportIfNewer_PendingHashIsFresh|TestAutoImportIfNewer_FreshPathRecordsNothing" -count=1 -v
=== RUN   TestAutoImportIfNewer_RewriteDuringImport
--- PASS: TestAutoImportIfNewer_RewriteDuringImport (0.00s)
=== RUN   TestAutoImportIfNewer_PendingHashIsFresh
--- PASS: TestAutoImportIfNewer_PendingHashIsFresh (0.00s)
=== RUN   TestAutoImportIfNewer_FreshPathRecordsNothing
--- PASS: TestAutoImportIfNewer_FreshPathRecordsNothing (0.00s)
PASS
ok  	github.com/steveyegge/beads/internal/autoimport	0.004s
```

### Check 3 — plan Verification command 5: full suite vs baseline

```
$ go test ./... -count=1 -json > /tmp/rv3_post.json 2> /tmp/rv3_post_stderr.txt; echo $?
1

$ wc -c /tmp/rv3_post_stderr.txt
0 /tmp/rv3_post_stderr.txt

$ python3 work/w2_stale-race/artifacts/normalize_failures.py /tmp/rv3_post.json /tmp/rv3_post_stderr.txt > /tmp/rv3_failures.txt

$ cat /tmp/rv3_failures.txt
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file

$ comm -13 work/w2_stale-race/artifacts/baseline_failures.txt /tmp/rv3_failures.txt
(no output)
```

37 packages seen, 2 failed tests, stderr empty. Identical to `artifacts/baseline_failures.txt`: the same two pre-existing environmental failures (the test makes a git config read-only and expects the next write to fail; on this host, running as a user who can write it anyway, it does not), and no new ones. The gate passes: no regression.
