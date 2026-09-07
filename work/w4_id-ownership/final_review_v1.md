# Final Review v1: /home/ben/worktrees/beads/w4_id-ownership/work/w4_id-ownership

VERDICT: FINDINGS n=3
| ID | class | sev | file:line | trigger | defect (one sentence) | evidence | fix (one sentence) |
|---|---|---|---|---|---|---|---|
| F1 | neither | med | internal/config/config_test.go:22 | `go test ./...` from the worktree (any cwd under this repo, any HOME carrying `~/.config/bd/config.yaml`) | `TestDefaults` reads the repo's own `.beads/config.yaml` instead of bd's built-in defaults, so `no-daemon` and `auto-start-daemon` fail and the full suite is red; this is the identical host-config leak the follow-up commit fixed for `TestDaemonAutoStart` in `cmd/bd`, left unfixed one package over | `--- FAIL: TestDefaults/no-daemon: GetXXX("no-daemon") = true, want false`; the same compiled test binary passes when run from a temp cwd holding an empty `.beads/config.yaml` with `HOME` redirected (see appendix) | Call the same isolation the follow-up added: chdir `TestDefaults` into a temp dir holding an empty `.beads/config.yaml`, re-`Initialize()`, restore both on cleanup. |
| F2 | neither | low | internal/importer/importer.go:626 | Multi-repo mode (`repos.primary` set), db prefix `bd`, DB holds `other-1`, import a JSONL where the same content carries id `other-2` | In multi-repo mode `handlePrefixMismatch` short-circuits membership (as the plan's Design says) but `upsertIssues` does not, so a foreign-repo rename now fails the `sameProject` test and is counted as `Skipped` with no message, leaving the stale id in the DB | New binary: `Import complete: 0 created, 0 updated, 1 skipped`, `ids: ['other-1']`. Old binary at the same input: `Import failed: failed to handle rename other-1 -> other-2` (hard error, so this is not a regression from a working state, but the outcome is now silent) | Give `upsertIssues` the same multi-repo short-circuit: when `config.GetMultiRepoConfig() != nil`, keep the `ExtractIssuePrefix` comparison. |
| F3 | neither | low | .beads/issues.jsonl (22 rows, e.g. `bd-00pel`) | `bd import -i .beads/issues.jsonl` in the beads repo itself | The beads repo still cannot load back its own export: the prefix refusal is gone, but the import now stops on `invalid issue type: event` for 22 plain `bd-*` rows, so the User Intent sentence ("a name bd accepts is a name bd can load back") does not yet hold in the repo the item ships from | New binary: `Import failed: error creating depth-0 issues: validation failed for issue 1: invalid issue type: event`. Old binary on the same copy failed earlier, at `prefix mismatch ... [bd-eph- (1 issues) bd-wisp- (87 issues)]`. The 22 `event` rows are plain `bd-*` ids, none of the 88 newly-admitted `bd-wisp-`/`bd-eph-` rows carries the `event` type, so the change neither causes nor worsens this | Out of this item's scope (`event` is not one of bd's issue types and the rows come from upstream Gas Town data); record it as residual rather than patching it here. |

## Verification Output

### Scrutiny of the follow-up's production change (cmd/bd/flush_manager.go)

The item notes asked whether the `drainMarkDirty` change in d1a58c92c is right. It is.

**The race is real.** `MarkDirty` only hands its event to a buffered channel and
returns; `PersistentPostRun` in `cmd/bd/main.go:852` then calls `Shutdown`, which
sends to `shutdownCh`. Both channels are ready and Go's `select` picks uniformly at
random, so roughly half the time the run loop takes shutdown first, sees `isDirty ==
false`, skips the final flush, and the mutation is never exported. Reproduced by
reverting only that hunk (`git show d1a58c92c -- cmd/bd/flush_manager.go | git apply -R`)
and running the test:

```
REVERTED (drain removed)
--- FAIL: TestAutoFlushOnExit (0.07s)
    main_test.go:90: Expected JSONL file to be created on exit
    main_test.go:96: Failed to open JSONL file: open /tmp/bd-test-exit-716293243/issues.jsonl: no such file or directory
FAIL	github.com/steveyegge/beads/cmd/bd	2.268s
```

The file was restored immediately afterwards; `git status --porcelain cmd/bd/flush_manager.go`
is empty.

**The fix is correct and complete.** With the drain in place:

```
$ go test -count=30 -run TestAutoFlushOnExit ./cmd/bd/
ok  	github.com/steveyegge/beads/cmd/bd	2.336s

$ go test -race -count=10 -run 'TestAutoFlush|TestFlush' ./cmd/bd/
ok  	github.com/steveyegge/beads/cmd/bd	8.519s
```

- **No lost flush.** The drain consumes queued events and folds each one into
  `isDirty` / `needsFullExport` before the branch decides, so nothing that was
  queued is discarded.
- **No duplicate flush.** Drained events are removed from the channel, so the main
  loop cannot see them again; each branch still calls `performFlush` at most once
  and clears the flags after.
- **Ordering on the `flushNow` reply path is unchanged in shape.** `responseCh` is
  still written exactly once on both the nothing-to-flush and the flushed branch,
  and the drain runs after the debounce timer is stopped and nil'd, so the drained
  events leave no orphan timer behind. The only semantic shift is that `FlushNow`
  now flushes a mutation that was queued microseconds before the call rather than
  replying `nil` and leaving it for the debounce timer, which is what `FlushNow`
  means.
- **Nothing depends on the old behaviour.** `FlushNow` has no production caller at
  all (`grep -rn FlushNow --include=*.go cmd/ internal/` finds only the definition
  and tests); the daemon path and every other caller reaches the loop through
  `MarkDirty` and `Shutdown`, both of which are strictly better off.
- **Concurrent `MarkDirty` during shutdown** is still unordered, but that was
  always true and no happens-before relationship exists to fix there.

### Check 1: User Intent

Delivered. Import now asks create's question wherever a prefix is configured, and
the four repos named in the plan load without renaming anything (Check 3 below).
The two commits match the plan's Changes table row for row: `handlePrefixMismatch`,
the `upsertIssues` rename test, `RenameImportedIssuePrefixes`, `repairPrefixes`,
both post-merge hook templates, and the three required tests. `ExtractIssuePrefix`
still labels refused prefixes and still guesses in `bd init`, `autoimport` and
`detectPrefixes`, exactly as the Design table says. Fleet's out-of-repo hook carries
the identical edit with a backup beside it:

```
$ grep -n "import_error" ~/projects/fleet/.git/hooks/post-merge
47:if ! import_error=$(bd import -i "$BEADS_DIR/issues.jsonl" 2>&1); then
49:		  echo "$import_error" >&2
$ ls ~/projects/fleet/.git/hooks/ | grep post-merge
post-merge
post-merge.bak-w4
```

`buildAllowedPrefixSet` only ever held the single configured prefix
(`importer.go:1006-1011`), so swapping the set lookup for `ValidateIssueIDPrefix`
narrows nothing. `ValidateIssueIDPrefix` is a bare `strings.HasPrefix(id, prefix+"-")`,
so hierarchical child ids (`bd-a3f8e9.1`) keep passing.

### Check 2: Verification / Ship Gate

**Gate 1 — round-trip a hash-tailed id (expected `Import complete`): PASS**

```
$ make build && bd init --quiet --prefix demo && bd create "phase 0 rev" --id demo-w1-p0a -t task \
    && bd export -o .beads/issues.jsonl && bd import -i .beads/issues.jsonl
Import complete: 0 created, 0 updated, 1 unchanged
```

**Gate 2 — foreign id still refused (expected `prefix mismatch`): PASS**

```
Import failed: prefix mismatch detected: database uses 'demo-' but found issues with prefixes: [other- (1 issues)] (use --rename-on-import to automatically fix)
```

**Gate 4 — the post-merge hook prints the real error (expected `prefix mismatch`): PASS**

```
Warning: Failed to import bd changes after merge
Import failed: prefix mismatch detected: database uses 'demo-' but found issues with prefixes: [other- (1 issues)] (use --rename-on-import to automatically fix)
Run 'bd import -i .beads/issues.jsonl' manually to see the error
```

The hook still exits 0. The `if ! var=$(cmd)` form works under `sh` because a simple
command that is only an assignment takes the exit status of its command
substitution; the run above is under `sh .git/hooks/post-merge`, so this is proven,
not assumed.

**Gate 5 — the package suites (expected `ok`): PASS**

```
$ go test -count=1 ./internal/importer/... ./internal/utils/... ./cmd/bd/...
ok  	github.com/steveyegge/beads/internal/importer	1.407s
ok  	github.com/steveyegge/beads/internal/utils	0.005s
ok  	github.com/steveyegge/beads/cmd/bd	19.282s
ok  	github.com/steveyegge/beads/cmd/bd/doctor	1.484s
ok  	github.com/steveyegge/beads/cmd/bd/doctor/fix	0.631s
ok  	github.com/steveyegge/beads/cmd/bd/setup	0.005s
```

### Check 3: E2E — Gate 3, real repo copies

Originals never touched; each run works on a `cp -r` into a fresh `mktemp -d`.

**New binary (HEAD):**

```
--- farmplanner --- Import complete: 0 created, 0 updated, 2218 unchanged, 110 skipped
--- duke ---        Import complete: 0 created, 0 updated, 138 unchanged, 1 skipped
--- investing ---   Import complete: 0 created, 0 updated, 329 unchanged, 72 skipped
--- teaching ---    Import complete: 0 created, 0 updated, 1297 unchanged, 6 skipped
```

**Pre-item binary (`~/.local/libexec/bd-real.bak-w4`), same copies:**

```
--- farmplanner --- Ignoring prefix mismatches (all are tombstones): [farmplanner-w485- (2) farmplanner-w496- (9) farmplanner-w496v10- (10) farmplanner-ben- (17)]
                    Import complete: 0 created, 0 updated, 2218 unchanged, 72 skipped
--- duke ---        Import failed: prefix mismatch detected: database uses 'duke-' but found issues with prefixes: [duke-w12- (11 issues) duke-w15- (9 issues) duke-w18- (2 issues)]
--- investing ---   Ignoring prefix mismatches (all are tombstones): [investing-w34- (10) investing-w47- (6) investing-w55- (6)]
                    Import complete: 0 created, 0 updated, 329 unchanged, 50 skipped
--- teaching ---    Import failed: prefix mismatch detected: database uses 'teaching-' but found issues with prefixes: [teaching-t1786687139530096155- (4 issues) ... teaching-t1786702613923527139- (1 issues)]
```

duke and teaching went from a refused file to a clean import. `0 created, 0 updated`
on all four confirms nothing was renamed.

The changed skip counts are accounted for, not a regression: farmplanner 72 → 110 is
exactly the 38 tombstones the old code deleted from the file as foreign-prefixed
(`2+9+10+17 = 38`), which are now recognised as local and land on the pre-existing
"skip tombstones" branch at `importer.go:663`; investing 50 → 72 is the same
arithmetic with its 22 tombstones. Net effect on the database is identical.

### Check 4: Integration seams

- `sqlite.ValidateIssueIDPrefix` is a pure prefix test with no suffix rules, so the
  three new call sites accept hierarchical ids and every id create accepts. No
  signature changed; `internal/importer/utils.go` gained an import of
  `internal/storage/sqlite`, which introduces no cycle (the whole tree builds).
- `upsertIssues` reads `issue_prefix` once before the loop and keeps the
  `ExtractIssuePrefix` comparison when it is empty, so prefix-less databases still
  detect renames. Its new `GetConfig` error path aborts the import, which is the
  same treatment `handlePrefixMismatch` already gives that read.
- `repairPrefixes` still computes `prefix` for the sort key only; `detectPrefixes`
  is untouched, as the plan requires. The consequence is that
  `bd rename-prefix X --repair` on a database of `duke-w12-*` ids still prints
  "Multiple prefixes detected" from `detectPrefixes` and then correctly repairs
  zero of them. Cosmetic and explicitly what the plan chose, so not raised.
- `RenameImportedIssuePrefixes`'s early `continue` is a strict superset of the old
  `oldPrefix != targetPrefix` skip: `ExtractIssuePrefix` always returns a value the
  id starts with, so every id the old code skipped the new code also skips.
  Malformed ids with no hyphen still produce the same "malformed ID" error.
- Post-merge hook: on success the captured output is discarded exactly as
  `>/dev/null 2>&1` did, so quiet merges stay quiet.

### Check 5: Regressions — full suite

```
$ go test ./...
... 35 packages ok ...
--- FAIL: TestDefaults (0.00s)
    --- FAIL: TestDefaults/no-daemon (0.00s)
        config_test.go:48: GetXXX("no-daemon") = true, want false
    --- FAIL: TestDefaults/auto-start-daemon (0.00s)
        config_test.go:48: GetXXX("auto-start-daemon") = false, want true
FAIL	github.com/steveyegge/beads/internal/config	0.033s
```

One failing package, `internal/config` (F1). It is pre-existing rather than caused
by this item: neither commit touches `internal/config`, `internal/config/config_test.go`,
or `.beads/config.yaml`, and the `no-daemon: true` / `auto-start-daemon: false`
values it reads were committed to main by a5ce30c1b (w28). Proof that it is the
host config and not the code:

```
$ go test -c -o /tmp/cfgtest ./internal/config/
$ cd <tempdir with empty .beads/config.yaml> && HOME=<tempdir> /tmp/cfgtest -test.run TestDefaults -test.v
    --- PASS: TestDefaults/no-auto-import (0.00s)
    --- PASS: TestDefaults/db (0.00s)
    --- PASS: TestDefaults/actor (0.00s)
    --- PASS: TestDefaults/flush-debounce (0.00s)
    --- PASS: TestDefaults/auto-start-daemon (0.00s)
PASS
```

Everything else is green, including `internal/storage/sqlite`, `internal/rpc`,
`internal/daemon`, `internal/export` and `cmd/bd`.

### Ship gate integrity and binary state

`ship_gate.json` has `"revisions": []` and `"residues": []`, and its five requirement
rows match the plan's Ship Gate table verbatim; `plan_sha256` matches the plan file
on disk. No check was rewritten and no requirement was dropped mid-item.

The installed binary is stale but not misleading. `~/.local/libexec/bd-real` reports
`0.34.0 (836ed2c07)` while a build at HEAD reports `0.34.0 (d1a58c92c)`, and their
md5sums differ. The follow-up's only production change is `cmd/bd/flush_manager.go`,
which touches no path the Phase 3 gate covers, so every Phase 3 result above holds
for the installed binary too. Raised here as context, not as a finding: the rebuild
at the final tree is still owed, and it now carries a data-loss fix.
