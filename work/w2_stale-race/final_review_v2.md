# Final Review v2: work/w2_stale-race

VERDICT: FIX findings=1

| ID | sev | file:line | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|
| F1 | med | internal/autoimport/autoimport.go:105 | The `StatusFresh` branch records `currentHash` — the hash of the bytes `os.ReadFile` returned at line 76 — even though `ContentState` reached its Fresh verdict by independently re-reading and re-hashing the file, so a writer that replaces the file between those two reads makes the database commit a hash for content it neither holds nor has on disk. | `jsonlpub.sampleState` calls `HashFile(jsonlPath)`, a second read of the file, so `StatusFresh` can be true because the *new* bytes match committed-or-pending while `currentHash` matches nothing; `recordImport` → `jsonlpub.RecordImport` then sets `jsonl_content_hash` to that orphan hash and calls `clearPending`, destroying the pending record that described the real content, so every later `ContentState` returns `StatusDiverged` — which is exactly what makes `cmd/bd/staleness.go:48` print "Database out of sync with JSONL. Run 'bd sync --import-only' to fix." on a healthy repo and makes `jsonlpub.Publish` refuse every export with `ErrDiverged` until an import runs. This is new in `beee58774`: the pre-fix branch condition was `currentHash == lastHash`, so the hash compared and the hash recorded always described the same bytes and the record was a harmless re-write of a value already in the database. The sibling CLI path is not affected — `cmd/bd/autoflush.go`'s Fresh branch returns without recording. build.md Round 4 observation 2's "self-heals at the next read" is true only for the auto-import reader, and only after the user has already seen the message this work item exists to eliminate and after exports have already been blocked; `CheckStaleness`/`ensureDatabaseFresh` and `Publish` both read the poisoned key before any healing import happens. The rule the fix's own comment states ("the hash it records must cover the bytes it parsed") is violated here precisely because this branch parses nothing. | Delete the `recordImport` call at line 105 so the Fresh branch only logs and returns: nothing in non-test code reads `last_import_time` (its only writers are `internal/jsonlpub/jsonlpub.go:45`'s constant and `cmd/bd/daemon_sync.go:282`), staleness is content-based now so the "mtime-only change stops looking new" rationale in the comment is vestigial, and `contentStateLocked` already promotes a lingering pending hash at the next locked event. |

Everything else in the four scoped items and five checks passed. Notes below record what was verified, and where an anomaly was chased, why it is not a finding.

**Item 2 — round 1's findings landed.** F1: `cmd/bd/autoflush.go` no longer names `jsonl_content_hash` outside a comment (line 108), reads freshness through `jsonlpub.ContentState`, and its import tail at :263-272 is a single `jsonlpub.RecordImport(ctx, store, jsonlPath, currentHash, jsonlpub.Options{})` under the publish lock. F2: `work/w2_stale-race/builds/build.md:68` now says `GetDirtyIssueSnapshots`; `grep -rn SnapshotDirtyIssues` over the worktree returns zero hits.

**Item 3 — class closure, re-derived independently.** My own `grep -rn 'jsonl_content_hash\|last_import_hash' --include=*.go .`, minus `_test.go` and `internal/jsonlpub/`, returns exactly the six sites the report lists, and each verdict is correct: `internal/importer/importer.go:69`, `cmd/bd/import_shared.go:196`, `cmd/bd/autoflush.go:108` and `internal/autoimport/autoimport.go:90,92` are comments naming the key, not reads of it; `cmd/bd/daemon_event_loop.go:242-243` discards both return values and branches only on `err`, logging and continuing, so it decides nothing about freshness; `cmd/bd/daemon_sync.go:247-300` (`updateExportMetadata`) is a pure `SetMetadata` writer reached only from `daemon_sync.go:443` and `:751`, both iterating `getMultiRepoJSONLPaths()`, which `cmd/bd/deletion_tracking.go:171-175` returns `nil` for whenever `config.GetMultiRepoConfig()` is `nil` — unreachable in single-repo mode, as the table says. No wrong "not a freshness decision" verdict.

**Item 4 — the rest of the two new fixes.** The tri-state mappings are right in both callers: `StatusDiverged` and `StatusNoMetadata` both import, and `StatusNoFile` skips, which is correct because importing bytes that are no longer on disk would record content for a missing file. The error direction preserves bd-663 in both callers — a `ContentState` error becomes `StatusNoMetadata` and therefore an import, never a silent skip. The bd-39o `last_import_hash` fallback is correctly centralized in `jsonlpub.readCommitted`: `sampleState`'s `committed == "" && pending == ""` test uses the post-fallback value, so a legacy-only database reads Fresh rather than NoMetadata, and a metadata *error* propagates instead of silently falling through to the legacy key. `%s` on `jsonlpub.Status` is fine (value-receiver `String()`). No lock inversion is introduced: `ContentState` releases the flock before returning and `RecordImport` re-acquires it, and `internal/jsonlpub` cannot reach `operationMu` (unexported, `package main`). The new `SetJSONLFileHash` side effect on the CLI path corrects a pre-existing `validateJSONLIntegrity` (`cmd/bd/autoflush.go:334-386`) mismatch warning rather than introducing drift.

**Check 1 (User Intent).** `cmd/bd/staleness.go:48` remains the single source of the message in non-test Go code, fed by `autoimport.CheckStaleness`, which ends `return status == jsonlpub.StatusDiverged, nil` (`internal/autoimport/autoimport.go:298`). Mtime plays no part. A healthy repo can still reach the message only through F1's window.

**Check 3 (integration).** Every non-test `ContentState` caller: `internal/autoimport/autoimport.go:93` and `:290`, `cmd/bd/autoflush.go`, `cmd/bd/integrity.go`'s `hasJSONLChanged`. Every non-test `RecordImport` caller: `internal/autoimport/autoimport.go:105,140` (via `recordImport`), `cmd/bd/autoflush.go:263`, `cmd/bd/import.go`, `cmd/bd/repo.go:232`, `cmd/bd/daemon_sync.go`. CLI and daemon auto-import now share one contract and one lock, so the CLI can no longer commit a key the daemon is mid-promote on; the residual interaction is F1's, and it is intra-path, not cross-process ordering.

**Check 5 (build.md Round 4 observation 2).** Not true in every reachable state — see F1's evidence. The healing import happens only on the *next* `AutoImportIfNewer`; `ensureDatabaseFresh` and `Publish` read the poisoned committed key first and both fail loudly in the meantime.

Phase 3 (build to `/tmp/bd.new`, E2E run, daemon stop, install to `~/.local/bin/bd`, live probes 6-9) was not run and is not counted as missing. Nothing outside this worktree was touched; no fixes were applied.

## Verification Output

### Check 2 — plan Verification commands 1-5

```
$ go test ./internal/jsonlpub/ -count=1
ok  	github.com/steveyegge/beads/internal/jsonlpub	0.080s

$ go test ./internal/storage/sqlite/ -run Dirty -count=1
ok  	github.com/steveyegge/beads/internal/storage/sqlite	0.937s

$ go test ./internal/autoimport/ -run "TestCheckStaleness|TestRecordImport" -count=1
ok  	github.com/steveyegge/beads/internal/autoimport	0.004s

$ go test ./cmd/bd/ -run PreImportFlush -count=1
ok  	github.com/steveyegge/beads/cmd/bd	0.168s
```

### Check 4 — full suite vs baseline

```
$ go test ./... -count=1 -json > /tmp/rv2_post.json 2> /tmp/rv2_post_stderr.txt; echo $?
1

$ python3 work/w2_stale-race/artifacts/normalize_failures.py /tmp/rv2_post.json /tmp/rv2_post_stderr.txt > /tmp/rv2_failures.txt

$ cat /tmp/rv2_failures.txt
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E
github.com/steveyegge/beads/cmd/bd/doctor/fix::TestMergeDriverWithLockedConfig_E2E/handles_read-only_git_config_file

$ comm -13 work/w2_stale-race/artifacts/baseline_failures.txt /tmp/rv2_failures.txt
(no output)
```

37 packages seen, 2 failed tests, stderr empty. Identical to `artifacts/baseline_failures.txt`: the same two pre-existing environmental failures (the test makes a git config read-only and expects the next write to fail; on this host it does not), and no new ones. F1 is a concurrency window no test in the tree exercises — `TestAutoImportIfNewer_RewriteDuringImport` (`internal/autoimport/autoimport_test.go:691-730`) pins the parsed-bytes rule on the *import* path only, and no test asserts anything about a Fresh-path record.
