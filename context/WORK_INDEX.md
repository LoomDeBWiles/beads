# Work Index

> Keyed lookup — read when you hold the key: resuming or reviewing a wN item, tracing a
> decision month, or checking a discovery corpus. Durable topic-triggered knowledge lives
> in the root CONTEXT.md and CODEMAP.md, never here.

| File | What | When to read |
|------|------|--------------|
| ~/projects/library/sources/work-beads-w1_agents-md-guard/record/ | w1 SHIPPED: bd never writes AGENTS.md; init.go writer deleted, guard tests added | Reviewing the AGENTS.md deletion or an upstream sync that reintroduces it |
| ~/projects/library/sources/work-beads-w2_stale-race/record/work_report.md | w2 SHIPPED + DEPLOYED + LIVE: false "Database out of sync with JSONL" killed; freshness is content-based via internal/jsonlpub | Reviewing the JSONL publish protocol, freshness rules, or the w2 build |
| ~/projects/library/sources/work-beads-w2_stale-race/record/rca_e2e_v1.md | Why the w2 acceptance script failed on the fix: it froze a pre-fix precondition (a dirty marker surviving daemon startup) | Writing an E2E that observes a symptom the fix removes |
| ~/projects/library/sources/work-beads-w3_atomic-claim/record/work_report.md | w3 SHIPPED + DEPLOYED + LIVE: `bd claim` is one preconditioned write with an owner lease; two concurrent claimants no longer both win | Reviewing the claim verb, its exit codes and lease rules, or the w3 build |
| ~/projects/library/sources/work-beads-w3_atomic-claim/record/rca_v1.md | Why every column added after migration 022 was wiped on each database open since v0.30.7 (no ledger; 019/022 cycle) | Touching migrations, adding a column to `issues`, or investigating a value that silently reverts |
| ~/projects/library/sources/work-beads-w4_id-ownership/record/work_report.md | w4 SHIPPED: import judges id membership as create does (configured prefix + hyphen), so `ben-w31-p0a` ids load; hook prints the import error | Reviewing prefix membership, `bd import` refusals, `--rename-on-import`, `rename-prefix --repair`, the hook template, or the w4 build |
| ~/projects/library/sources/work-beads-w6_bd-parent-echo/record/work_report.md | w6 SHIPPED + DEPLOYED: `bd create --parent` allocates from the issues table inside one transaction and every insert is strict, so a create can no longer echo an existing ID or report success without writing a row | Reviewing child ID allocation, the `child_counters` floor, `CreateChildIssue`, strict inserts, or the w6 build |
| ~/projects/library/sources/work-beads-w6_bd-parent-echo/record/astra_report_v1.md | The RCA behind w6: how a counter left behind by explicit-`--id` creates handed out live IDs, and how `INSERT OR IGNORE` turned the collision into a silent no-op that attached edges to the wrong issue | Tracing the farmplanner incident, or why the counter is not the authority on child numbers |
