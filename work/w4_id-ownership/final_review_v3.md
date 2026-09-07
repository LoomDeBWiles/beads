# Final Review v3: /home/ben/worktrees/beads/w4_id-ownership/work/w4_id-ownership

Round 3, scoped to 529b6dc9a (fix) and f73377305 (report).

VERDICT: FINDINGS n=3

| ID | class | sev | file:line | trigger | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|---|---|
| H1 | neither | low | internal/config/config_test.go:11-23 | `go doc -all ./internal/config` on the test file, or anyone reading the top of `config_test.go` | Splitting the helper in two left `isolateConfig`'s nine-line doc comment glued to the front of `clearBeadsEnv`'s new comment as one contiguous block, so the paragraph about pointing config discovery at an empty temp project now documents the env-clearing function, and `isolateConfig` at line 40 has no doc comment at all | Lines 11-23 are one unbroken `//` run ending at line 23, immediately followed by `func clearBeadsEnv` at line 24; line 39 is blank and line 40 is a bare `func isolateConfig(t *testing.T) {`. The stranded text still says "Restores the working directory and the config singleton on cleanup", which `clearBeadsEnv` does not do. | Move lines 11-18 down to sit directly above `func isolateConfig` at line 40, leaving lines 19-23 as `clearBeadsEnv`'s own comment. |
| H2 | neither | low | cmd/bd/claim_test.go:361, cmd/bd/claim_test.go:414 | `BD_JSON=true go test ./cmd/bd/` from the worktree | `runBDBinary` builds the child environment as `append(os.Environ(), env...)`, so an exported `BD_JSON=true` reaches the spawned `bd` and flips it to JSON output; both claim tests then look for the literal string "Claimed issue" in stdout and fail. The item already diagnosed this exact passthrough at d1a58c92c and neutralized only `BD_NO_DAEMON` for one of the two tests, so the round-2/round-3 env-proofing pass stopped at `internal/config` and left the sibling package exposed to the same class of host env. | `BD_JSON=true go test ./cmd/bd/` → `--- FAIL: TestClaimExitCodesInDirectMode (1.09s) claim_test.go:374: the winner should say it claimed the issue:` and `--- FAIL: TestClaimExitCodesInDaemonMode (0.50s) claim_test.go:447`. `BD_ACTOR=hostactor` and `BEADS_FLUSH_DEBOUNCE=99s` are both green on the same package. Pre-existing at 836ed2c07^ (line 361 was never touched by the item; line 414's edit added `BD_NO_DAEMON=false` but not `BD_JSON=false`), so this is not a regression from 529b6dc9a. | Add `"BD_JSON=false"` to both env slices, or better, have `runBDBinary` strip every inherited `BD_*`/`BEADS_*` from `os.Environ()` before appending the per-test overrides. |
| H3 | neither | low | internal/importer/importer.go:644 | Multi-repo config (`repos.primary` set), db `issue_prefix` = `bd`, DB legitimately holds `other-1` (multi-repo import allows foreign prefixes by design, `handlePrefixMismatch` returns early), and the JSONL renames it to `other-2` | The reverted condition makes the foreign-prefix content match take the skip branch, which does `result.Skipped++` with no message naming the issue or the reason, so a genuine rename inside an additional repo is dropped and every subsequent `bd sync` drops it again, silently and permanently | Probe under the exact round-2 setup: `multiRepo=true GetMultiRepoConfig()=&{...}` then `created=2 updated=0 unchanged=0 skipped=1`, `other-2 exists=false`, `other-1 exists=true`, and nothing on stderr between those two lines. Round 2's accepted G1 fix said "the pre-fix silent skip, **with a warning line so it is not silent**"; the skip was restored, the warning was not. | Emit one `fmt.Fprintf(os.Stderr, ...)` in the `!sameProject` branch naming `existing.ID`, `incoming.ID` and the configured prefix, the way `handlePrefixMismatch` already does for tombstones at importer.go:262. |

**G1 and G2 are both closed.** F3 from round 1 (`invalid issue type: event` on the beads repo's own export) remains residual and untouched; not re-raised.

## Verification Output

### Check 1: G1 closure

**Comment-only against the item's first commit.** `git diff 836ed2c07 HEAD -- internal/importer/importer.go` is four added lines, all `//`, and no code line changed:

```
@@ -624,6 +624,10 @@ func upsertIssues(...)
 				// Same content, different ID - check if this is a rename or cross-prefix duplicate
+				// The rename path below calls CreateIssue, which validates the incoming
+				// ID against the configured prefix in every mode, so membership must be
+				// tested the same way here. Steering a foreign ID into a rename would
+				// reach a create that cannot succeed and abort the whole batch.
 				var sameProject bool
 				if configuredPrefix != "" {
```

The `config` import is still used elsewhere in the file, so no unused-import fallout.

**Round 2's probe, re-run.** Scratch test in `internal/importer`: stage a viper config with `repos.primary` set, create `other-1` under prefix `other`, flip the db's `issue_prefix` to `bd`, then `ImportIssues` a three-row batch `bd-100` / `other-2` (same content as `other-1`) / `bd-200`.

```
multiRepo=true GetMultiRepoConfig()=&{/tmp/TestProbeG1MultiRepo780013730/001 []}
multiRepo=true created=2 updated=0 unchanged=0 skipped=1
   bd-100 exists=true
   bd-200 exists=true
   other-1 exists=true
   other-2 exists=false
```

Exactly the expected outcome: the import completes, `other-2` is skipped, `bd-100` and `bd-200` are created. Round 2's abort (`failed to handle rename other-1 -> other-2: ... does not match configured prefix 'bd'`, with neither `bd-100` nor `bd-200` landing) is gone. G1 closed.

Side observation, not a finding: the single-repo arm of the same probe now stops earlier, at the prefix-mismatch gate (`prefix mismatch detected: database uses 'bd-' but found issues with prefixes: [other- (1 issues)]`, importer.go:275), because that gate only waives foreign prefixes in multi-repo mode. That gate sits upstream of the changed line and is unchanged by the item; round 2's single-repo `created=2 skipped=1` came from a probe that got past it.

The probe file was removed with `trash.sh` (batch `20260907T001208Z-621044`); `git status --porcelain` shows only `work/` files.

### Check 2: G2 closure

**With the four host variables exported:**

```
$ export BD_ACTOR=hostactor BD_JSON=true BEADS_FLUSH_DEBOUNCE=99s BD_NO_DAEMON=true
$ go test ./internal/config/...
ok  	github.com/steveyegge/beads/internal/config	0.015s   EXIT=0

$ go test ./internal/config/... -shuffle=on -count=3 -race
ok  	github.com/steveyegge/beads/internal/config	1.100s   EXIT=0
```

**Per-variable sweep** (round 2 had three reds here; all now green, plus `BD_JSON=false`, which catches a leak in the opposite direction that `BD_JSON=true` would mask):

```
BD_ACTOR=hostactor               ok
BD_JSON=true                     ok
BD_JSON=false                    ok
BEADS_FLUSH_DEBOUNCE=99s         ok
BD_NO_DAEMON=true                ok
BEADS_NO_DAEMON=1                ok
BD_DB=/tmp/x.db                  ok
BEADS_IDENTITY=hostid            ok
BEADS_AUTO_START_DAEMON=false    ok
```

**`TestConfigPrecedence`'s own `BD_JSON=true` versus `clearBeadsEnv`'s cleanup: correct, and for a stronger reason than LIFO.** The two mechanisms are not both `t.Cleanup`, so LIFO ordering between them never comes up. `clearBeadsEnv(t)` at config_test.go:225 registers a `t.Cleanup` per host `BD_*`/`BEADS_*` variable; the test's own `_ = os.Setenv("BD_JSON", "true")` is undone by a plain `defer` in the function body. Go runs all body `defer`s at function return and only then drains `t.Cleanup`, so the sequence is fixed: unset the test's `BD_JSON`, then restore the host's value (or leave it unset if the host never had one, since `clearBeadsEnv` only registers restores for variables it actually found). No window in which the test's `true` survives, and no risk of the cleanup writing `true` back. The `BD_JSON=false` sweep row above is the empirical confirmation: if the restore lost the host value, `TestConfigFile` running after `TestConfigPrecedence` under shuffle would see `BD_JSON` unset instead of `false`, and it asserts `json` is `true` from its own file either way, so the shuffled `-count=3` run is what actually exercises both orders. Green.

`t.Chdir(tmpDir)` is called before `clearBeadsEnv(t)` in both tests, so its directory restore is registered first and runs last, after the env is back. Correct order.

No `t.Parallel` anywhere in `internal/config/*_test.go`, so the process-global `os.Unsetenv`/`os.Chdir` in these helpers are safe as written.

### Check 3: Regressions

```
$ go test ./...
EXIT=0
```

37 lines, every one `ok` or `no test files`, zero `FAIL`. Notable rows: `cmd/bd 19.886s ok`, `internal/config 0.029s ok`, `internal/importer ok`, `internal/storage/sqlite ok`. No regression.

### Check 4: Installed binary

```
$ ~/.local/bin/bd version
bd version 0.34.0 (529b6dc9a)          # = the reviewed commit

$ ls -la ~/.local/libexec/
-rwxrwxr-x 33848209 Sep  7 02:09 bd-real
-rwxrwxr-x 33864282 Sep  7 01:30 bd-real.bak-w4    # pre-item rollback binary intact
```

PASS on both.
