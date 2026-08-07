# Work Index

> Keyed lookup — read when you hold the key: resuming or reviewing a wN item, tracing a
> decision month, or checking a discovery corpus. Durable topic-triggered knowledge lives
> in the root CONTEXT.md and CODEMAP.md, never here.

| File | What | When to read |
|------|------|--------------|
| work/w1_agents-md-guard/ | w1 SHIPPED: bd never writes AGENTS.md; init.go writer deleted, guard tests added | Reviewing the AGENTS.md deletion or an upstream sync that reintroduces it |
| work/w2_stale-race/work_report.md | w2 SHIPPED + DEPLOYED + LIVE: false "Database out of sync with JSONL" killed; freshness is content-based via internal/jsonlpub | Reviewing the JSONL publish protocol, freshness rules, or the w2 build |
| work/w2_stale-race/rca_e2e_v1.md | Why the w2 acceptance script failed on the fix: it froze a pre-fix precondition (a dirty marker surviving daemon startup) | Writing an E2E that observes a symptom the fix removes |
| work/w3_atomic-claim/work_report.md | w3 SHIPPED + DEPLOYED + LIVE: `bd claim` is one preconditioned write with an owner lease; two concurrent claimants no longer both win | Reviewing the claim verb, its exit codes and lease rules, or the w3 build |
| work/w3_atomic-claim/rca_v1.md | Why every column added after migration 022 was wiped on each database open since v0.30.7 (no ledger; 019/022 cycle) | Touching migrations, adding a column to `issues`, or investigating a value that silently reverts |
