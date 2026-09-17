You are Astra, investigating a bd (beads) bug for a root-cause report. This checkout is a git worktree of the beads fork (LoomDeBWiles/beads) at /home/ben/worktrees/beads/w6_bd-parent-echo. The installed binary `bd` is `/home/ben/.local/libexec/bd-real`, built from commit 02e0ab0fe on main (bd version 0.34.0). The Go source is here.

# Deliverable
Write ONE file: /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/astra_report_v1.md
Do not edit any Go source, docs, or any file outside that work/ directory. Do not touch /home/ben/projects/farmplanner/.beads/ (read it only with `sqlite3 "file:...?mode=ro"` if useful). Never run `bd` against that farmplanner DB in write mode. Use `bd --no-daemon` for anything you run. Build scratch DBs only under /home/ben/worktrees/beads/w6_bd-parent-echo/work/w6_bd-parent-echo/scratch/.

# What happened (observed by a farmplanner manager agent on 2026-09-09 ~18:19 CEST)
DB: /home/ben/projects/farmplanner/.beads/beads.db, prefix farmplanner-r3yq. Parent epic farmplanner-r3yq.8 already had children up to farmplanner-r3yq.8.34 (8.32 created 07:13, 8.33 08:35, 8.34 14:23 the same day; all three closed).

Command 1 (the flags that matter; body-file omitted):
  B35=$(bd --db $DB --no-auto-import create --silent --type bug --priority 1 --parent farmplanner-r3yq.8 --deps farmplanner-r3yq.8 --title "..." --body-file body.md)
  B36=$(bd --db $DB --no-auto-import create --silent --type task --priority 1 --parent farmplanner-r3yq.8 --deps farmplanner-r3yq.8,$B35,farmplanner-r3yq.8.20 --title "..." --body-file body2.md)
Output:
  Warning: failed to add parent-child dependency farmplanner-r3yq.8.32 -> farmplanner-r3yq.8: failed to add dependency: sqlite3: constraint failed: UNIQUE constraint failed: dependencies.issue_id, dependencies.depends_on_id, dependencies.type
  bug=farmplanner-r3yq.8.32
  Warning: failed to add dependency farmplanner-r3yq.8.33 -> farmplanner-r3yq.8: failed to add dependency: sqlite3: constraint failed: UNIQUE constraint failed: ...
  task=farmplanner-r3yq.8.33
So `create` printed the IDs of two EXISTING closed issues (8.32, 8.33) and exit status was success. `bd show` afterwards showed 8.32 and 8.33 unchanged (old titles, still closed). A follow-up `bd dep add farmplanner-r3yq.8.27 $B36` then wired 8.27 onto the wrong issue 8.33 (the agent removed it by hand).

Command 2: `bd create --id farmplanner-r3yq.8.35 --parent farmplanner-r3yq.8 ...` -> "Error: cannot specify both --id and --parent flags". The agent then created with --id and no --parent, and added the parent link via `bd dep add <child> <parent> --type parent-child`.

Current DB state (read-only sqlite): child_counters row is (farmplanner-r3yq.8, 33). So the counter was apparently 31 before command 1 and was bumped twice, to 32 then 33, even though issues 8.32, 8.33, 8.34 already existed. Hypothesis to test: those three were created by some path that does not advance child_counters (explicit --id, JSONL import/auto-import, worktree DB import, `bd` version skew, or something else). Check the `events` table and issues.created_at/created_by for 8.32-8.34, and the farmplanner repo's .beads/issues.jsonl git history, to determine how they were created.

# Questions the report must answer, with file:line evidence
1. Exactly how `bd create --parent` generates the child ID (child_counters, hash_ids.go, queries.go). Why did it hand out 8.32 when 8.34 existed?
2. Why did `create` not fail when the generated ID collided with an existing issue? Trace the INSERT path (INSERT OR IGNORE / upsert / --silent swallowing an error / ignored error return). Which of the two is it: the issue row was silently not written, or overwritten? Prove with a scratch reproduction.
3. Which write paths advance child_counters and which do not (explicit --id, import, sync, rename, migration). Which one left the farmplanner counter behind, and can you prove it from the farmplanner evidence?
4. Why `--id` and `--parent` are mutually exclusive, and whether that restriction is justified.
5. Reproduce end to end in a scratch repo: init a DB, create an epic, create children so that child_counters lags behind existing IDs (via whichever path you found), then run `bd create --parent` and show the same symptom. Include the exact commands and output in the report.
6. Recommend one fix at the deepest cause (not a symptom patch), with the specific functions to change, and list the tests that should cover it. State explicitly whether the fix needs a migration that resyncs child_counters from existing IDs. Give little weight to development cost; prefer correctness and simplicity. Also say whether the create path should hard-fail on any ID collision regardless of counter state.

Format: plain prose plus fenced code blocks for commands and output; a short "Summary" section first (five sentences max), then "Evidence", "Reproduction", "Root cause", "Recommended fix", "Open questions". No em dashes.
