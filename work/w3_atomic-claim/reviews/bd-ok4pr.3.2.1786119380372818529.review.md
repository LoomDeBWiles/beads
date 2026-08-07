VERDICT: FIX findings=1

| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|
| R1 | med | internal/merge/merge.go:40-63 | This bead's own contract is fully met, but the same struct-rebuild mechanism it fixes still drops five keys that exist in the live store today (`close_reason` 600 rows, `wisp` 107, `labels` 65, `external_ref` 16, `comments` 13): `Merge3Way` re-marshals every row it emits from `merge.Issue`, so any merge that rewrites a row silently deletes those fields from issues.jsonl. | Open a follow-up bead to model the remaining exported keys in `merge.Issue` (or carry unknown keys through a `map[string]json.RawMessage` catch-all re-emitted on marshal) — no rework of this commit. |
