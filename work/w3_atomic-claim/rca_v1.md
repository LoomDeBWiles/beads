# RCA: `claim_expires_at` is NULL on the next process open

**Verdict: the builder's hypothesis is CORRECT.** Confirmed against source and reproduced
live. One correction to the framing: the defect is not specific to `claim_expires_at`, and
it does not originate with this work item. It has been in every released `bd` since
**v0.30.7** and it silently wipes `pinned` and `is_template` too, on every store on this
host, on every single `bd` invocation.

---

## 1. The mechanism

### The one-sentence version

`bd` has no record of which migrations it has already run, so it runs all 27 of them every
time it opens a database. Two of those migrations undo each other: migration 019 re-adds
four columns that migration 022 exists to delete, so 022 fires on every open and rebuilds
the `issues` table from a hard-coded list of 28 column names. Any column added after 022 in
the list is not on that list, so it is silently re-created empty.

### The chain, with line numbers

**Why 1 — Why is `claim_expires_at` NULL in the next process?**
Because the `issues` table was dropped and recreated between the two processes, and the new
table's `claim_expires_at` column was created fresh with no data. `updated_at` is unchanged
because the rebuild copies `updated_at` verbatim (`022_drop_edge_columns.go:140`) — the row
is *copied*, so it looks untouched, while the un-copied columns are silently blank.

**Why 2 — Why was the table rebuilt?**
`internal/storage/sqlite/migrations/022_drop_edge_columns.go` — `MigrateDropEdgeColumns()`.
It creates `issues_new` (line 91), `INSERT INTO issues_new ... SELECT` a **hard-coded list of
28 columns** (lines 129-145), `DROP TABLE issues` (line 151), `ALTER TABLE issues_new RENAME
TO issues` (line 157). Columns not in that 28-name list are not carried across. This is the
only migration in the tree that rebuilds `issues`:

```
$ grep -n "DROP TABLE \|RENAME TO " internal/storage/sqlite/migrations/*.go
022_drop_edge_columns.go:151:  DROP TABLE issues
022_drop_edge_columns.go:157:  ALTER TABLE issues_new RENAME TO issues
025_remove_depends_on_fk.go:65:  DROP TABLE dependencies      <- different table
025_remove_depends_on_fk.go:70:  ALTER TABLE dependencies_new RENAME TO dependencies
```

**Why 3 — Why did 022 run at all? Its job was done long ago.**
022 is guarded: it returns early (line 56-58) if none of `replies_to`, `relates_to`,
`duplicate_of`, `superseded_by` exist. The guard does not hold, because
`internal/storage/sqlite/migrations/019_messaging_fields.go` — `MigrateMessagingFields()`,
lines 29-48 — re-adds all four with `ALTER TABLE issues ADD COLUMN` whenever they are
missing. 019 runs before 022 in the same pass. So every pass: 019 adds the four columns →
022 sees them → 022 rebuilds. 019 is the only migration that adds these columns; 021 only
reads them and 022 only drops them.

**Why 4 — Why do 019 and 022 both run every time, given each is "already applied"?**
Because there is no migration ledger. `internal/storage/sqlite/migrations.go:141-145`:

```go
for _, migration := range migrationsList {
    if err := migration.Func(db); err != nil { ... }
}
```

`RunMigrations()` iterates the entire `migrationsList` (lines 19-47) unconditionally on
every open. There is no `schema_migrations` table, no `PRAGMA user_version`, no applied-set
of any kind anywhere in the repository:

```
$ grep -rn "schema_migrations\|migration_version\|user_version\|applied_migrations" --include=*.go --include=*.sql .
(no matches)
```

The comment at `migrations.go:56` states the design assumption out loud: *"This returns ALL
registered migrations, not just pending ones (all are idempotent)."* The assumption is
false. 019 and 022 are each idempotent in isolation but not as a pair — their composition
is a cycle.

**Why 5 — Why did nothing catch it?**
Two reasons.

- The post-migration invariant checker (`internal/storage/sqlite/migration_invariants.go`)
  validates only *row counts* and foreign-key integrity: `checkRequiredConfig`,
  `checkForeignKeys`, `checkIssueCount` (lines 26-41, 108-190). The `issues` row count is
  preserved perfectly by the rebuild, so every invariant passes while column values are
  destroyed.
- No test ever runs migrations twice against a database that holds data.
  `RunMigrations` appears in exactly one test file (`schema_probe_test.go`, three call sites)
  and never in a write-reopen-read sequence.

**Root cause:** `RunMigrations` re-executes every migration on every database open with no
applied-migration ledger, and the migration set contains a pair (019 adds four columns / 022
deletes them by whole-table rebuild) whose composition is a cycle. The rebuild copies a
hard-coded column list frozen at the time 022 was written, so every column added by a later
migration is destroyed on each cycle.

### The "pinned column added by ALTER, then dropped, then re-added" fingerprint

`internal/storage/sqlite/schema.go` declares `pinned` at line 39, `is_template` at 41 and
`claim_expires_at` at 43 — i.e. *before* `close_reason` in the CREATE TABLE. But a freshly
initialised store reports them at the very end, positions 29/30/31, after `close_reason`:

```
id content_hash title ... sender ephemeral close_reason | pinned is_template claim_expires_at
                                                          ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
```

That is the 28-column table from `022_drop_edge_columns.go:92-122` with 023/024/027's
`ALTER TABLE ADD COLUMN` tacked on. The rebuild has already happened before the store has
ever been used once.

---

## 2. Reproduction (this worktree's binary, temp store, `/tmp/rca-probe`)

The reported symptom, exactly:

```
$ bd --no-daemon claim probe-nne --assignee holder-A --lease 600s
✓ Claimed issue: probe-nne
$ sqlite3 db "SELECT id,assignee,claim_expires_at FROM issues WHERE id='probe-nne'"
probe-nne|holder-A|2026-08-07T20:15:50.459606717+02:00
$ bd --no-daemon list > /dev/null          # a pure read
$ sqlite3 db "SELECT id,assignee,COALESCE(claim_expires_at,'NULL') ..."
probe-nne|holder-A|NULL
```

`assignee` is a pre-022 column and survives. `claim_expires_at` is post-022 and does not.

All three post-022 columns together, `updated_at` untouched:

```
--- BEFORE reopen ---   probe-lpd|pinned=1|is_template=1|2030-01-01T00:00:00Z|2026-08-07T20:01:27.665
--- AFTER `bd list` --- probe-lpd|pinned=0|is_template=0|NULL                |2026-08-07T20:01:27.665
```

**Direct proof it is a table rebuild, not an UPDATE.** I added a sentinel column and two
triggers, then ran one read command:

| artifact | before | after `bd list` |
|---|---|---|
| `zzz_probe` column on `issues` | present | **gone** |
| trigger `trg_probe` on `issues` | present | **gone** |
| trigger `trg_events` on `events` | present | present |

Only the `issues` table is destroyed and recreated. This independently confirms the
builder's trigger observation and rules out any UPDATE/DEFAULT/ORM-writeback explanation.

---

## 3. Blast radius

**Columns that lose data:** exactly those added by a migration positioned after
`drop_edge_columns` in `migrationsList` (migrations.go:41):

| column | migration | added in list at | feature it breaks |
|---|---|---|---|
| `pinned` | 023 | line 42 | `bd pin` / molecules / "yellow sticky" handoffs |
| `is_template` | 024 | line 43 | template molecules (`bd mol`, `bd pour`, `bd wisp`) |
| `claim_expires_at` | 027 | line 46 | the owner lease this work item adds |

025 and 026 add no `issues` columns, so the list is complete. **Any future migration that
adds a column to `issues` joins this list automatically.**

**Under what conditions:** unconditionally. Every `bd` process, every store, including
read-only commands (`bd list`, `bd show`) and the daemon. There is no trigger condition to
avoid — the loss is not a race, not a concurrency artefact, and not daemon-related. (Note
the E2E artifact's step 2, the concurrency check, passed: the atomic-claim SQL itself is
sound. Only the persistence is broken.)

**Collateral damage beyond columns:**
- Any trigger on `issues` is destroyed on the next `bd` command. Verified.
- `idx_issues_ephemeral` and `idx_issues_sender` are created by 019 and then dropped by
  022's `DROP TABLE` within the same pass, and nothing recreates them afterwards. They are
  permanently absent at rest — verified on the probe store. `gt mail inbox` queries filter
  on `sender`, so mail listing runs unindexed.
- Every `bd` invocation rewrites the entire `issues` table. On a large store this is a real
  and entirely pointless cost on every command.

**From which version:** first release shipping both 022 and a later column-adding migration
is **v0.30.7** (2025-12-19). 022 landed in `7c8b69f5b`, first tagged `v0.30.6`; `pinned`/023
landed in `b1ba1c531`, first tagged `v0.30.7`.

**Does it predate this work item: YES.** Verified live against the installed baseline
`bd version 0.34.0 (f31496d65)` in an isolated temp store: `pinned=1, is_template=1` set by
hand, then a plain `bd --no-daemon list`, then both read back as `0`. The baseline has no
`claim_expires_at` column at all (027 is new here), so this work item did not introduce the
bug. It added a third victim to an eight-month-old defect, and is the first feature whose
*core semantics* depend on the wiped column, which is why the defect surfaced now.
`bd pin` has been silently non-functional across processes on this host since v0.30.7.

**Is data unrecoverable?** Partially recoverable, with a narrow window:
- The values do reach `.beads/issues.jsonl` before the wipe. Verified: after `bd pin`, the
  JSONL contains `"pinned":true`; after `bd claim`, it contains the `claim_expires_at`
  timestamp. So a value that was committed to git is recoverable from the JSONL history.
- An explicit `bd import -i .beads/issues.jsonl` **does** restore the value into the DB
  (verified: `1 updated`, `pinned` back to `1`) — but the very next `bd` command wipes it
  again. Recovery is a treadmill until the code is fixed.
- The JSONL is clobbered by the next export. Verified: after the wipe, one further
  write + `bd sync` rewrote `issues.jsonl` and the `"pinned":true` occurrence count went to
  **0**. So on the 17 live stores, any `pinned`/`is_template` value that was never committed
  to git before a subsequent export is **gone permanently**.
- Practically: assume `pinned` and `is_template` are `0` on every live store right now, and
  that recovery means grepping `git log -p .beads/issues.jsonl` for `"pinned":true`.

---

## 4. Candidate fixes

An important constraint on the fix space, found while evaluating them:
`021_migrate_edge_fields.go` queries `replies_to`, `relates_to`, `duplicate_of` and
`superseded_by` directly (e.g. line 27-30) with **no column-existence guard** —
`grep -n "pragma_table_info\|columnExists" 021_migrate_edge_fields.go` returns nothing.
Today 019's re-add is what keeps 021 from failing with `no such column`. Any fix that stops
019 from re-adding the columns must also guard 021, or every `bd` command will error out.

### A. Stop 019 re-adding the dropped columns (+ guard 021)
Remove `replies_to`, `relates_to`, `duplicate_of`, `superseded_by` from the `columns` slice
in `019_messaging_fields.go:23-26`, guard the `idx_issues_replies_to` creation (line 63),
and add column-existence guards to 021's four queries.

- **Fixes:** the cycle stops; 022's early-return at line 56 finally holds; no more rebuilds.
- **Risk to live data:** low but not zero — 021 must be guarded correctly or every command
  breaks loudly (loud failure, not silent corruption, which is the good kind).
- **Does not fix the class.** 022's hard-coded 28-column copy stays. Any future migration
  that drops a column, or any store that still legitimately has the edge columns, walks
  right back into the same trap.

### B. Make 022 preserve columns it does not know about
Replace the hard-coded column list at `022_drop_edge_columns.go:91-145` with one built at
runtime from `pragma_table_info('issues')` minus the four columns being dropped, and
recreate the table from the live schema rather than a frozen literal.

- **Fixes:** the data loss, immediately and for every column, present and future — even
  while the cycle keeps spinning.
- **Risk to live data:** low. It strictly widens what is copied; the failure mode of a bug
  here is a migration error inside the EXCLUSIVE transaction, which rolls back.
- **Does not fix the cycle.** The pointless per-command table rebuild, the trigger
  destruction and the two missing indexes all remain.

### C. Add a migration ledger (the actual root cause)
Create `schema_migrations(name TEXT PRIMARY KEY, applied_at DATETIME)`, and in
`RunMigrations` (migrations.go:141) skip any migration already recorded, recording each on
success inside the same EXCLUSIVE transaction. Backfill needs no heuristics: on a store with
no ledger, run all migrations exactly as today (which is what happens now anyway) and then
record all of them. From the second open onward, nothing re-runs.

- **Fixes:** the whole class. 019/021/022 each run once, ever. Removes the per-command
  rebuild cost, the trigger destruction and the missing indexes.
- **Risk to live data:** moderate, and it is the real question. The "run once more, then
  record everything" backfill is the safe form — it is behaviour-identical to today for
  exactly one more open, so it cannot introduce a state today's code would not already
  produce. The unsafe form is any attempt to *detect* which migrations already ran and skip
  some; that could leave a store half-migrated. Do not do that.

### Recommendation: **B + C, in that order, in one change.**

B alone stops the bleeding with the lowest possible risk, and it is what makes C's one final
backfill pass harmless — with B in place, that last rebuild loses nothing. C then removes
the cause rather than the symptom, so the next engineer who writes a table-rebuilding
migration does not rediscover this. Ship them together; B without C leaves `bd` rewriting
the whole issues table on every command, and C without B means the backfill pass costs one
last wipe of `pinned`/`is_template`/`claim_expires_at` on all 17 stores.

A is worth doing as cleanup afterwards (019 should not be re-adding columns the schema
deliberately removed), but it is not the fix — it patches one instance of the cycle, and
touching 021 is the riskiest edit of the three for the smallest gain.

**Before deploying anything:** the wiped values are recoverable only from
`git log -p .beads/issues.jsonl`. Harvest `"pinned":true` / `"is_template":true` from the
17 stores' JSONL history *first*, then fix, then restore by `bd import`. After the fix the
restored values will actually stick.

### The regression test that would have caught this

None of the existing tests could have: `RunMigrations` is called in only one test file and
never twice on a populated database. The missing test is a *reopen-survival* test, and it
must be data-driven so it covers columns added in future:

```go
// TestPostRebuildColumnsSurviveReopen: every column in the issues table must retain
// its value across a second RunMigrations pass. This is the guard against a migration
// that rebuilds `issues` from a frozen column list (bd-ok4pr / migration 022).
func TestPostRebuildColumnsSurviveReopen(t *testing.T) {
    db := freshDB(t)
    RunMigrations(db)                       // first open
    insertIssue(db, "t-1")
    // write a distinguishable non-default value into EVERY column of issues,
    // enumerated from pragma_table_info - not a hand-written list.
    want := writeSentinelIntoEveryColumn(t, db, "t-1")

    RunMigrations(db)                       // second open - simulates the next process

    got := readAllColumns(t, db, "t-1")
    if diff := cmp.Diff(want, got); diff != "" {
        t.Fatalf("values lost across reopen (-want +got):\n%s", diff)
    }
}
```

Enumerating columns from `pragma_table_info` rather than listing them by hand is the whole
point: a hand-written list would have been written in the same breath as 022's hand-written
list and would have had the same three columns missing from it. A second, cheap assertion in
the same test — that the `issues` table's `rootpage`/schema is byte-identical before and
after the second pass — would catch the pointless rebuild itself, not just its data loss.

---

## 5. Cross-check: what would have falsified this, and did I look for it

I set out four ways this conclusion could have been wrong and tested each.

1. **"Some other migration rebuilds `issues`."** Falsifier: a second `DROP TABLE issues` or
   `RENAME TO issues` anywhere in the migration set, or in the storage layer outside
   migrations. Looked: `grep -n "DROP TABLE \|RENAME TO " migrations/*.go` returns only 022
   (issues) and 025 (dependencies). Not falsified.

2. **"It isn't a rebuild at all — something UPDATEs the columns to NULL, or a struct
   round-trip writes back zero values."** This is the most plausible competing explanation
   and it predicts that a sentinel *column* and a *trigger* on `issues` would survive. Looked:
   both were destroyed by one `bd list`, while a trigger on `events` survived. A write-back
   cannot drop a column or a trigger. Firmly falsified; only a rebuild explains it.

3. **"There is a migration ledger somewhere and 022 does not actually re-run."** Falsifier:
   any `schema_migrations` / `user_version` / applied-set. Looked repo-wide by grep — none.
   And the empirical evidence is decisive independently of the grep: the trigger dies on
   *every* invocation, so 022 demonstrably re-runs.

4. **"This is new with the claim-lease work item."** Falsifier: the installed 0.34.0 baseline
   behaving correctly. Looked: tested the baseline binary in an isolated temp store — it
   wipes `pinned` and `is_template` identically. Not this work item's doing.

**One thing I could not verify and am flagging rather than asserting.** The E2E artifact
cited in the brief, `work/w3_atomic-claim/artifacts/e2e_claim_output.txt`, does not exist —
there is no `artifacts/` directory under `work/w3_atomic-claim/`. My conclusions rest
entirely on my own reproduction plus the source, not on that file, so nothing here depends
on it; but if that artifact contains a step whose failure this mechanism does *not* explain,
that is the loose end to pull.

**Scope of what I proved directly vs. inferred.** Directly observed: the table is dropped and
recreated on every open; the three columns are blanked; `updated_at` survives; the baseline
binary does the same; values reach the JSONL and are clobbered by a later export. Inferred
from source rather than observed at runtime: that 019 specifically is the migration that
re-adds the four columns each pass. The inference is tight — 019 is the only code in the
repository that adds those columns, the columns are absent at rest, and 022 provably fires
every pass, which requires them to be present when 022 checks. All migrations run inside one
EXCLUSIVE transaction, so the intermediate state is not observable from outside the process
without patching the source.
