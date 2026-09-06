# Build: /home/ben/worktrees/beads/w4_id-ownership/work/w4_id-ownership/plan_v2.md

## Acceptance

| Criterion | Command | Log | Pass |
|-----------|---------|-----|------|
| 1. A hash-tailed id (`demo-w1-p0a`) round-trips through create, export and import | plan `gate=1` block | [step1.log](step1.log) | Y |
| 2. A foreign id is still refused, and the message still names the prefix | plan `gate=2` block | [step2.log](step2.log) | Y |
| 3. farmplanner, duke, investing and teaching import clean from copies, nothing renamed | plan `gate=3` block | [step3.log](step3.log) | Y |
| 4. The generated post-merge hook prints bd's own error and still exits 0 | plan `gate=4` block | [step4.log](step4.log) | Y |
| 5. Nothing else in bd regressed | `go test ./internal/importer/... ./internal/utils/... ./cmd/bd/...` | [step5.log](step5.log), [step5b.log](step5b.log) | Y (one pre-existing failure, unrelated; see Tests) |

Gate 1 printed `Import complete: 0 created, 0 updated, 1 unchanged` — the id
that used to break the whole file loads back. Gate 2 printed `prefix mismatch
detected: database uses 'demo-' but found issues with prefixes: [other- (1
issues)]`. Gate 3 printed `Import complete` for all four repos with 0 created
and 0 updated, i.e. no id was renamed: farmplanner 2218 unchanged, duke 138,
investing 329, teaching 1232. Gate 4 printed the warning line, then bd's real
`prefix mismatch` error, then the manual-command line, and the hook exited 0.

## Tests

`go test ./internal/importer/... ./internal/utils/... ./cmd/bd/...`:
`internal/importer` ok, `internal/utils` ok, `cmd/bd/doctor` ok,
`cmd/bd/doctor/fix` ok, `cmd/bd/setup` ok. `cmd/bd` fails on exactly one test,
`TestDaemonAutoStart/shouldAutoStartDaemon_defaults_to_true`.

That failure is pre-existing and not caused by this item. The test asserts
daemon auto-start defaults to true, while this repo's committed
`.beads/config.yaml` sets `auto-start-daemon: false` (added by w28 in commit
a5ce30c1b to disable the bd daemon fleet-wide). step5b.log shows a clean export
of HEAD, with none of this item's changes applied, failing the same test the
same way. Fixing it means either reversing w28's fleet-wide decision or
rewriting that test, neither of which is in w4's scope.

Three tests added in `internal/importer/importer_test.go`, all passing:
`TestImportHyphenatedLocalIDs` (a `bd` database imports `bd-w31-p0a` and
`bd-92cl-gate-p0a` with no mismatch, both created),
`TestImportForeignIDStillRefused` (`other-bad1` refused, error names `other`),
`TestRenameOnImportLeavesLocalIDsAlone` (`--rename-on-import` leaves
`bd-w31-p0a` untouched and renames only `other-bad1` to `bd-bad1`).

## Notes

Phase 2 also edited `/home/ben/projects/fleet/.git/hooks/post-merge` in place,
backed up first to `post-merge.bak-w4` in the same directory. `sh -n` on the
edited hook is clean.

Phase 3: `~/.local/libexec/bd-real` was backed up to `bd-real.bak-w4` and
replaced with the new build. `cp` over it failed with "Text file busy" because
a bd process was executing it, so the new binary was copied in beside it and
moved into place with `mv`, which is an atomic rename and is safe while the old
inode is still executing. Same outcome as the plan's `cp`, no truncation risk.
`bd version` through the wrapper at `~/.local/bin/bd` reports
`bd version 0.34.0 (a5ce30c1b)`; the binary was rebuilt and reinstalled after
the commit so its build stamp names this item's commit.

## Divergence from the plan

None in behaviour. Two mechanical differences worth recording:

- `handlePrefixMismatch` no longer keeps the `allowedPrefixes` map, since after
  the multi-repo short-circuit it only ever held the configured prefix.
  `buildAllowedPrefixSet` is still called, purely for its nil (multi-repo) test.
- `RenameImportedIssuePrefixes` skips already-local ids with an early `continue`
  rather than nesting the rename in a condition, which keeps the loop flat.

## Run r2: test isolation fix

| Criterion | Command | Log | Pass |
| --- | --- | --- | --- |
| 1. TestDaemonAutoStart passes | `cd cmd/bd && go test -run 'TestDaemonAutoStart' ./` | `builds/r2-step1.log` | yes (exit 0) |
| 2. Ship Gate row 5 suite is green | `go test ./internal/importer/... ./internal/utils/... ./cmd/bd/...` (with `BEADS_NO_DAEMON=1 BD_NO_DAEMON=true`) | `builds/r2-step2.log` | yes (exit 0, all six packages `ok`) |

Three files changed. `cmd/bd/autostart_test.go` gains an `isolateConfig(t)`
helper that `TestDaemonAutoStart` now calls in place of a bare
`config.Initialize()`: it creates a temp directory holding an empty
`.beads/config.yaml`, chdirs into it, re-initializes config, and restores both
the working directory and the config singleton on cleanup. A bare `t.TempDir()`
would not have been enough - `config.Initialize()` walks up from the working
directory and then falls back to `~/.config/bd/config.yaml`, which on this
machine also carries w28's `auto-start-daemon: false`, so the empty project file
is what stops the search and lets the test see bd's built-in default. The repo's
own `.beads/config.yaml` and the test's assertion are both untouched.
`cmd/bd/claim_test.go` is the same defect one layer out:
`TestClaimExitCodesInDaemonMode` builds its child environment from
`os.Environ()`, so the `BD_NO_DAEMON=true` the Ship Gate itself sets leaked into
the spawned bd and routed every claim through direct mode, failing the
"daemon recorded no claim operation" assertion; the test now pins
`BD_NO_DAEMON=false` in the env it passes, next to the `BEADS_NO_DAEMON=0` that
was already there. Sweeping the rest of `cmd/bd` for the same host-config
dependency turned up no other failures, but it did expose a pre-existing flake,
`TestAutoFlushOnExit`, which fails roughly one run in three at HEAD with the
test files reverted, so it is not caused by this item. Its cause is a real
data-loss race in `cmd/bd/flush_manager.go`, not a test bug: `MarkDirty` only
queues its event on a buffered channel, so a mutation immediately followed by
`Shutdown` (exactly what `PersistentPostRun` does on exit) leaves the run
loop's `select` with two ready channels, and when it picks shutdown first
`isDirty` is still false and the final flush is skipped, losing the mutation.
The run loop now drains any queued `markDirty` events before deciding whether to
flush, in both the `flushNow` and the `shutdown` branches. `TestAutoFlushOnExit`
passed 20 consecutive runs after the fix, and the flush tests pass under `-race`.
