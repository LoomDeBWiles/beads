# Final Review v2: /home/ben/worktrees/beads/w4_id-ownership/work/w4_id-ownership

Round 2, scoped to 3ce83d0c6 (fix) and cd69c6dda (report).

VERDICT: FINDINGS n=2

| ID | class | sev | file:line | trigger | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|---|---|
| G1 | contract | med | internal/importer/importer.go:632 | Multi-repo config (`repos.primary` set), db `issue_prefix` = `bd`, DB holds `other-1`, import a batch whose same-content row carries id `other-2` | The F2 fix sends multi-repo foreign renames down the `handleRename` path that the comment three lines below it already warns cannot work ("Calling handleRename would fail because CreateIssue validates prefix"), so instead of skipping one row the import now aborts with an error and the whole batch is lost | Scratch probe importing `[bd-100, other-2, bd-200]`: multi-repo → `IMPORT ERROR: failed to handle rename other-1 -> other-2: failed to create renamed issue other-2: validate issue ID prefix: issue ID 'other-2' does not match configured prefix 'bd'`, and neither `bd-100` nor `bd-200` reached the DB; single-repo on the identical input → `created=2 updated=0 unchanged=0 skipped=1`. At the parent commit 836ed2c07 the same multi-repo input gave `0 created, 0 updated, 1 skipped` (final_review_v1 F2 evidence), i.e. the import completed. | Either revert to the plain `configuredPrefix != ""` test (the pre-fix silent skip, with a warning line so it is not silent), or make `handleRename`'s `CreateIssue` call prefix-tolerant in multi-repo mode so the branch it now takes can actually succeed. |
| G2 | neither | low | internal/config/config_test.go:142, internal/config/config_test.go:198 | `BD_ACTOR=x go test ./internal/config/` or `BD_JSON=true go test ./internal/config/` or `BEADS_FLUSH_DEBOUNCE=99s go test ./internal/config/` from the worktree | `TestConfigFile` and `TestConfigPrecedence` write their own config file and `t.Chdir` into it, but viper ranks env above the config file, so a host `BD_*`/`BEADS_*` export still masks the values they wrote; the run report's sweep claim ("the remaining tests write their own config file and `t.Chdir` into it, so they were already isolated") holds for config discovery but not for the env half that this commit added | `BD_ACTOR=hostactor` → `config_test.go:190: GetString(actor) = "hostactor", want "configuser"`; `BD_JSON=true` → `config_test.go:225: GetBool(json) from config file = true, want false`; `BEADS_FLUSH_DEBOUNCE=99s` → `config_test.go:194: GetDuration(flush-debounce) = 1m39s, want 15s`. Not triggered by the worker contract's own `BD_NO_DAEMON`/`BEADS_NO_DAEMON`, both of which are green. | Split the env-clearing loop out of `isolateConfig` into its own helper and call it from `TestConfigFile` and `TestConfigPrecedence` too (they cannot call `isolateConfig` whole, since it chdirs elsewhere). |

F3 from round 1 (`invalid issue type: event` on the beads repo's own export) remains residual and untouched by this commit; not re-raised.

## Verification Output

### Check 1: F2 closure — does `upsertIssues` now branch correctly?

Code trace. `internal/importer/importer.go:632`:

```go
var sameProject bool
if configuredPrefix != "" && config.GetMultiRepoConfig() == nil {
    sameProject = sqlite.ValidateIssueIDPrefix(existing.ID, configuredPrefix) == nil &&
        sqlite.ValidateIssueIDPrefix(incoming.ID, configuredPrefix) == nil
} else {
    sameProject = utils.ExtractIssuePrefix(existing.ID) == utils.ExtractIssuePrefix(incoming.ID)
}
```

`config.GetMultiRepoConfig()` (`internal/config/config.go:255`) returns nil exactly when
`repos.primary` is empty, which is the same predicate `buildAllowedPrefixSet` uses, so the
two short-circuits now agree. The branch selection asked for in round 1 is present: multi-repo
takes the `ExtractIssuePrefix` comparison, single-repo takes the configured-prefix test.

**Probe.** A scratch test staged a multi-repo viper config around `ImportIssues` (create
`other-1` under prefix `other`, flip `issue_prefix` to `bd`, import a three-row batch
`bd-100` / `other-2` (same content as `other-1`) / `bd-200`):

```
multiRepo=true  IMPORT ERROR: failed to handle rename other-1 -> other-2: failed to
                create renamed issue other-2: validate issue ID prefix: issue ID
                'other-2' does not match configured prefix 'bd'
                || partial state: bd-100(before)=false bd-200(after)=false
multiRepo=false created=2 updated=0 unchanged=0 skipped=1 other-2-exists=false
                bd-100=true bd-200=true
```

Branch selection: correct. Outcome: G1. The multi-repo branch reaches `handleRename` and
`handleRename`'s `CreateIssue` still validates against the single configured prefix, so the
branch it was steered into is a dead end. `ImportIssues` has no enclosing transaction
(`importer.go:150` calls `upsertIssues` directly), but the abort happens before the batch
create, so nothing landed — a clean failure, not partial state.

The probe file was removed with `trash.sh`; `git status --porcelain` shows only `work/` files.

### Check 2: F1 closure and the env-clearing addition to `isolateConfig`

**`t.Parallel`: no interaction, because there is none.** `grep -n "t.Parallel" internal/config/*_test.go`
returns nothing. `isolateConfig` calls `os.Chdir` and `os.Unsetenv`, both process-global, so a
future `t.Parallel` in this package would break it — but that is hypothetical, not a finding.

**Ordering across tests: no interaction.** Each call snapshots `os.Environ()` and registers one
`t.Cleanup` per variable, and the loop variables `name` and `value` are declared inside the loop
body, so each closure restores its own pair. Names are unique within a snapshot, so no
double-restore. Verified under shuffle, repetition and the race detector:

```
$ go test -race ./internal/config/ -count=3 -shuffle=on   (x3)
ok  github.com/steveyegge/beads/internal/config  1.099s
ok  github.com/steveyegge/beads/internal/config  1.101s
ok  github.com/steveyegge/beads/internal/config  1.099s
```

**`Initialize()` on cleanup runs before the env restores** — `t.Cleanup` is LIFO and the env
cleanups are registered first — but this is harmless: viper's `AutomaticEnv` and `BindEnv` read
the environment lazily at `Get` time, and `internal/config/config.go` contains no `os.Getenv` or
`os.Environ` call at all, so the rebuilt singleton picks up the restored values on the next read.
The shuffle run above exercises this ordering with `BD_NO_DAEMON=true` exported and
`TestEnvironmentBinding`/`TestGetIdentity` (which assert env-derived values) running both before
and after `TestDefaults`; all green.

**Set-but-empty variables are preserved**, since `os.Getenv` returns `""` for them and the
cleanup calls `os.Setenv(name, "")` rather than leaving them unset.

**Side benefit not claimed in the report:** `TestGetMultiRepoConfig` and `TestGetExternalProjects`
both `Set()` keys on the singleton and never cleared them; `isolateConfig`'s cleanup `Initialize()`
now rebuilds `v`, so those writes no longer leak forward.

**Still reading host config or env.** Config discovery: clean. Every remaining test either calls
`isolateConfig`, or `t.Chdir`s into a temp dir holding its own `.beads/config.yaml`
(`TestConfigFile`, `TestConfigPrecedence`, `TestGetStringSliceFromConfig`,
`TestGetMultiRepoConfigFromFile`, `TestGetExternalProjectsFromConfig`,
`TestResolveExternalProjectPath`, `TestGetIdentityFromConfig`), or asserts only values it `Set()`
itself (`TestSetAndGet`, `TestAllSettings`, `TestGetStringSlice`, `TestNilViperBehavior`) or nothing
at all (`TestInitialize`). Host env: two are still exposed — G2. Per-variable sweep:

```
--- BD_NO_DAEMON=true             ok
--- BEADS_NO_DAEMON=1             ok
--- BD_ACTOR=hostactor            FAIL TestConfigFile      config_test.go:190
--- BD_DB=/tmp/x.db               ok
--- BD_JSON=true                  FAIL TestConfigPrecedence config_test.go:225
--- BEADS_FLUSH_DEBOUNCE=99s      FAIL TestConfigFile      config_test.go:194
--- BEADS_IDENTITY=hostid         ok
--- BEADS_AUTO_START_DAEMON=false ok
```

### Check 3: Regressions — `go test ./...` from the worktree root

Plain environment:

```
$ go test ./...
EXIT=0
```
all ok (37 lines, every one `ok` or `no test files`).

With the worker contract's environment exported:

```
$ BD_NO_DAEMON=true BEADS_NO_DAEMON=1 go test ./...
EXIT=0
```
all ok.

F1 is closed under both: `internal/config` is green where round 1 had it red.

### Check 4: Installed binary

```
$ ~/.local/bin/bd version
bd version 0.34.0 (3ce83d0c6)          # = HEAD

$ ls -la ~/.local/libexec/
-rwxrwxr-x 33848113 Sep  7 01:57 bd-real
-rwxrwxr-x 33864282 Sep  7 01:30 bd-real.bak-w4    # pre-item rollback binary intact
```

PASS. The stale-binary note from round 1 is resolved.
