# w4_id-ownership: One rule for which project an issue id belongs to

> The importer asks the same question the creator asks, so a name bd accepts is a name bd can load back.

Scale: standard

## User Intent

"we need to fix the root cause". Ben's fleet lost bead syncing for months
because `bd import` refused fleet's own export file. He rejected a create-time
check as the fix and locked in the direction: where the database already knows
its prefix, the loader stops guessing and asks what the creator asks. He also
locked in a local commit on his fork (no upstream pull request), a rebuild at
the fork tip, and the minimum change that makes the post-merge hook print the
real error instead of discarding it.

## Problem

bd holds two different rules for "which project does this issue id belong to".
On create, `internal/storage/sqlite/ids.go:68 ValidateIssueIDPrefix` asks
whether the id starts with the configured prefix plus a hyphen. On import,
`internal/importer/importer.go:234` calls
`internal/utils/issue_id.go:21 ExtractIssuePrefix`, which guesses where the
project name ends by inspecting the text after the last hyphen: 3 to 8
characters of `[0-9a-z]`, with a digit required at 4 or more. The two rules
disagree on any id whose last hyphen segment looks like a hash.

`bd create --id ben-w31-p0a` therefore succeeds and the next
`bd export` plus `bd import` of that same database fails with "prefix mismatch
detected: database uses 'ben-'". The refusal covers the whole file, so 550
healthy issues fail to load because of 31. Fleet carried that state from w31
until 2026-09-06, and 267 ids across five more repos carry it now:
farmplanner 38, duke 22, investing 22, teaching 20, beads 165.

The failure is silent because the post-merge hook bd installs
(`cmd/bd/init.go:880` and `:934`) runs the import with `>/dev/null 2>&1`,
prints two generic warning lines, and exits 0 so the merge stays green.

## Key Insight

**bd already intends hyphenated ids to be legal; the heuristic implements that
intent wrongly.** `cmd/bd/import_multipart_id_test.go:14` asserts that
`vc-baseline-test` and `vc-92cl-gate-test` import into a `vc` database with no
prefix mismatch. Those pass today only because their last segments hold no
digit and fall through to the fallback branch. Change one to `vc-92cl-gate-p0a`
and the same test fails. The rule is not protecting an invariant, it is
producing a different answer than create for a subset of names nobody chose
deliberately. Forget this and the fix looks like a loosening of validation
rather than the removal of a contradiction.

## Design

An id belongs to this database when it starts with the database's configured
prefix and a hyphen. That is one question, asked in one place, by both paths.

| Path | Question today | Question after |
|------|----------------|----------------|
| `bd create --id X` | does X start with `ben-`? | unchanged |
| `bd import` membership | guess X's prefix, compare to `ben` | does X start with `ben-`? |
| `bd import` rename vs foreign duplicate | guess both prefixes, compare | do both start with `ben-`? |
| `bd init` on a file with no configured prefix | guess | unchanged, guessing is the only option |

Trace on the id that broke fleet, database prefix `ben`:

    ben-w31-p0a
      today:  ExtractIssuePrefix -> last hyphen -> "p0a" -> 3 chars, base36
              -> treated as the local id -> prefix "ben-w31" -> not "ben"
              -> whole file refused
      after:  HasPrefix("ben-w31-p0a", "ben-") -> true -> local issue
              -> imported

Trace on a genuinely foreign id, same database:

    other-bad1
      after:  HasPrefix("other-bad1", "ben-") -> false -> foreign
              -> refused, as today

`ExtractIssuePrefix` keeps its two legitimate jobs: naming the offending
prefix in the error message, and inferring a prefix at `bd init` when the
database has none yet. Multi-repo mode still short-circuits membership
entirely (GH#686).

## Changes

### Phase 1: One membership rule in the importer  —  Gate: `go test ./internal/... ./cmd/...` passes, and a `demo-w1-p0a` id round-trips through export and import

| File | Change | Why |
|------|--------|-----|
| `internal/importer/importer.go` | In `handlePrefixMismatch` (~line 234), decide membership with `sqlite.ValidateIssueIDPrefix(issue.ID, configuredPrefix)` instead of `allowedPrefixes[utils.ExtractIssuePrefix(issue.ID)]`. Keep `ExtractIssuePrefix` for the `result.MismatchPrefixes` label only, so the error still names what it refused. Keep the multi-repo short-circuit. | The one place the two rules disagreed |
| `internal/importer/importer.go` | In `upsertIssues` (~line 616), same-content different-id: read `issue_prefix` via `sqliteStore.GetConfig` and treat it as a rename when both ids belong to that prefix, instead of comparing two guessed prefixes | Same defect: a rename from `ben-w31-p0a` to `ben-42` reads as a foreign duplicate and is silently skipped |
| `internal/importer/importer_test.go` | Add a test: database prefix `bd`, import `bd-w31-p0a` and `bd-92cl-gate-p0a`, expect no mismatch and both created. Add a test that `other-bad1` is still refused. | The two tests that would have caught this |

### Phase 2: The hook prints its error  —  Gate: a forced import failure prints bd's own message, and the merge still exits 0

| File | Change | Why |
|------|--------|-----|
| `cmd/bd/init.go` | In both post-merge hook templates (~lines 880 and 934), capture the import output and print it on failure: `if ! import_error=$(bd import -i "$BEADS_DIR/issues.jsonl" 2>&1); then` then echo `$import_error` between the two existing warning lines. Keep `exit 0`. | A hook that discards its own error turned a small bug into months of silence |

Fleet runs a copy of the old template at `~/projects/fleet/.git/hooks/post-merge`.
It is untracked and outside this repo, so it is not a table row: apply the
identical edit there, because the template alone only reaches repos whose hooks
are written after the rebuild.

### Phase 3: Build and roll out  —  Gate: `bd version` runs from the new binary and the five affected repos import clean from copies

| File | Change | Why |
|------|--------|-----|
| `Makefile` | Run its `build` target, then back up `~/.local/libexec/bd-real` to `bd-real.bak-w4` and copy `./bd` over it | The wrapper at `~/.local/bin/bd` execs that path; the running copy is from commit `00d653c` |

## Files NOT Affected (verified)

| File | Checked | Why no change |
|------|---------|---------------|
| `internal/utils/issue_id.go` | Yes | `ExtractIssuePrefix` keeps its guessing job for `bd init` and for error labels; the defect is where it was called, not what it does |
| `internal/importer/utils.go` | Yes | `RenameImportedIssuePrefixes` runs only for genuinely foreign ids under `--rename-on-import`, where the prefix is unknown and guessing is the only available answer |
| `internal/storage/sqlite/ids.go` | Yes | `ValidateIssueIDPrefix` is already the rule being adopted; it becomes the shared one |
| `cmd/bd/doctor/prefix_test.go` | Yes | Tests prefix detection on files with no configured prefix, which still guesses |

## Not in Scope

- Renaming the 267 ids in duke, farmplanner, investing, teaching and beads. The whole point is that they need no rename.
- Undoing fleet's w45 rename. Those 37 ids import clean either way, and renaming them back would be churn for no gain.
- The post-merge hooks on disk in the other 17 repos. Fleet's is fixed because that is where the silence was observed; the rest pick up the fixed template when their hooks are next written.
- An upstream pull request to steveyegge/beads. Ben locked in a local commit on his fork.
- Escalating a failed post-merge import beyond stderr (inbox row, exit code). The locked scope is that the real error prints.

## Model Assignment

| Work | Lane | Tier | Effort | Why |
|------|------|------|--------|-----|
| Manager | auto | Fable | medium | Non-rote: a semantic change to a shared tool with a data-loss-adjacent blast radius |
| Importer, hook template and tests | build | Opus | medium | Builder work against a precise spec in an unfamiliar Go codebase |

## Execution Handoff

Direct: a single builder works from this plan. The bead threshold does not
hold. The three phases are one coherent change of about 30 lines across two
files plus tests, with a strict order rather than independent units.

Person steps: none. Ben has already made the three decisions this item needed
(local commit, rebuild at tip, hook error printing).

## Rollback

Full: `git revert` the commit on the fork, then restore the binary from the
backup taken in Phase 3 (`cp ~/.local/libexec/bd-real.bak-w4 ~/.local/libexec/bd-real`).
The backup is the exact binary running today, so rollback needs no rebuild.

Partial: the hook edit is independent. Restoring fleet's `.git/hooks/post-merge`
from its backup undoes Phase 2 on disk without touching the importer.

No data migration happens in this item, so nothing in any `.beads/beads.db`
needs undoing.

## Risks

| Risk | Mitigation |
|------|------------|
| A database whose prefix is a hyphen-extension of another repo's would now claim that repo's ids (`beads` claiming `beads-vscode-1`) | Verified: none of the 18 configured prefixes in `~/projects/*/.beads/beads.db` is a hyphen-prefix of another. Create already behaves exactly this way, so import stops being stricter than create rather than becoming loose |
| Swapping the binary under agents running bd right now | bd invocations are short and single-shot; the copy is over a path no running process holds open for the length of a command; the previous binary is kept for instant rollback |
| Fleet's hook edit is untracked and could be overwritten by a future `bd init` | The source template carries the same change, so a rewrite writes the fixed version |
| The fix hides a genuinely foreign id that happens to share the prefix | The error message still names the offending prefix via `ExtractIssuePrefix`, so a real cross-repo leak is still reported by name |

## Reach

The only new surface is text on stderr from a git hook.

| Affordance | Desktop mouse | Keyboard | Touch | Assistive tech |
|------------|---------------|----------|-------|----------------|
| Import error text after a merge | Visible in terminal output in DOM-free stream order | Not interactive; scrollback reaches it | Same terminal output | Plain text on stderr, read in order by any screen reader attached to the terminal |

## Verification

**Tests:**
- `go test ./internal/importer/... ./internal/utils/... ./cmd/bd/...` — all pass, including the two added cases

**E2E verification:**
Build the binary, create a throwaway repo with `bd init --prefix demo`, create
an issue with `--id demo-w1-p0a`, export and import it back, and confirm the
import completes. Then copy each affected repo's `.beads` directory to a
scratch path and run a real import there with the new binary, confirming
farmplanner, duke, investing and teaching load with no prefix mismatch and no
id renamed. The originals are never touched.

**Ship Gate** (proof of behavior; the /work gate compiles and executes this table and its command blocks):

| # | Requirement | Check | Expected |
|---|-------------|-------|----------|
| 1 | A bead named the way w31 named its beads can be created and loaded back | Round-trip an id with a hash-looking tail through a fresh database | `Import complete` |
| 2 | Issues from another project are still refused | Import a foreign id into a `demo` database | `prefix mismatch` |
| 3 | The four repos still carrying these ids load without renaming anything | Real import against copies of their own databases and export files | `Import complete` |
| 4 | A failed import after a merge prints what actually went wrong | Run the new hook body against a database that refuses the file | `prefix mismatch` |
| 5 | Nothing else in bd regressed | The package test suites | `ok` |

```bash gate=1
set -e
cd "$(git rev-parse --show-toplevel)" && make build
d=$(mktemp -d) && cd "$d" && git init -q .
"$OLDPWD/bd" init --prefix demo >/dev/null 2>&1 || true
"$OLDPWD/bd" create "phase 0 rev" --id demo-w1-p0a -t task >/dev/null
"$OLDPWD/bd" export -o .beads/issues.jsonl
"$OLDPWD/bd" import -i .beads/issues.jsonl
```

```bash gate=2
set -e
cd "$(git rev-parse --show-toplevel)"
d=$(mktemp -d) && cd "$d" && git init -q .
"$OLDPWD/bd" init --prefix demo >/dev/null 2>&1 || true
printf '%s\n' '{"id":"other-bad1","title":"foreign","status":"open","priority":2,"issue_type":"task"}' > foreign.jsonl
"$OLDPWD/bd" import -i foreign.jsonl 2>&1 || true
```

```bash gate=3
set -e
bd_bin="$(git rev-parse --show-toplevel)/bd"
for r in farmplanner duke investing teaching; do
  d=$(mktemp -d)
  cp -r "$HOME/projects/$r/.beads" "$d/.beads"
  ( cd "$d" && git init -q . && "$bd_bin" import -i .beads/issues.jsonl )
done
```

```bash gate=4
set -e
bd_bin="$(git rev-parse --show-toplevel)/bd"
d=$(mktemp -d) && cd "$d" && git init -q .
"$bd_bin" init --prefix demo >/dev/null 2>&1 || true
printf '%s\n' '{"id":"other-bad1","title":"foreign","status":"open","priority":2,"issue_type":"task"}' > .beads/issues.jsonl
if ! import_error=$("$bd_bin" import -i .beads/issues.jsonl 2>&1); then
  echo "Warning: Failed to import bd changes after merge" >&2
  echo "$import_error" >&2
fi
```

```bash gate=5
cd "$(git rev-parse --show-toplevel)" && go test ./internal/importer/... ./internal/utils/... ./cmd/bd/...
```
