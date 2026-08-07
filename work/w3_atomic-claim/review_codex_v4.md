VERDICT: CLEAN findings=0
| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|

Landing verification (round 4 of 4, scope: did the ten round-3 accepts land, and is the reworded exit contract internally consistent).

LANDED — all ten bindings present and complete:

| Binding | Landed at | Evidence |
|---|---|---|
| H1 Design exit paragraph | plan_v4.md:98 | "Exit 3 means \"not claimable now\"; the outcome's `DenyReason` (`held` \| `status`) tells a shell caller whether to retry ... or skip"; the v3 "branch on lost the race without parsing JSON" claim is gone |
| H1 supervisor before/after cell | plan_v4.md:110 | "exit 3 + `deny_reason=held` = retry, `deny_reason=status` = skip" (v3: "exit 3 = someone else has it") |
| H1b types.go row | plan_v4.md:140 | `ClaimOutcome` enum plus `DenyReason` held/status; Why column states it separates retry from skip |
| H2 Design empty-assignee rule | plan_v4.md:98 | "required and must be non-empty after trimming; absent or empty is a usage error (exit 1)", with the empty-matches-legacy-CLAIM-cell rationale |
| H2b claim.go row + claim_test | plan_v4.md:159, :150, :161 | claim.go "non-empty after trimming (absent or empty → usage error, exit 1; no default)"; claim_test "empty `--assignee` value→rejected"; CLI test "empty `--assignee` → exit 1" |
| H3 status-DENY tests | plan_v4.md:150, :161 | claim_test "`blocked` status→denied ... `deny_reason=status` naming the status"; scripttest "the `blocked`-status → exit 3 `deny_reason=status` case" |
| H4 utils.go comparator | plan_v4.md:168 | "calling a new nil-aware `equalPtrTime` helper built on `time.Time.Equal`", with both failure modes (string helpers → unconditional change; missing case → default false) |
| H5 TestClaimRoundTrip | plan_v4.md:169 | "plus a lease-only-renewal case on an external_ref-bearing issue (same status/assignee, new expiry → propagates)"; Why names the external_ref path as the only load-bearing one (guard is `updated_at`, not the hash) |
| H5r Risks renewal-lag row | plan_v4.md:218 | Scoped: "for rows without an `external_ref`"; body adds "external_ref-matched rows propagate via their `updated_at` guard (importer.go:548-598)" |
| H6 multirepo row | plan_v4.md:148 | "INSERT copy path (:302-305) AND the full-column UPDATE branch (:330-347)", with the stale-lease/never-stealable rationale |
| H7 claim.go DENY qualifier | plan_v4.md:142 | "DENY writes nothing and returns `DenyReason` plus holder+expiry only when present, otherwise the status" |

CONSISTENCY — no contradictions in the changed text:

- `DenyReason` vocabulary is `held` | `status` at every appearance: Design :98, types.go row :140, supervisor cell :110, claim_test row :150, scripttest row :161. Go field `DenyReason` vs JSON/CLI `deny_reason` is the ordinary struct-tag convention, not a divergence.
- The two DENY cells in the Design ladder (:87 held-unexpired, :89-91 any other status) map one-to-one onto the two reasons; the vocabulary is complete, no third DENY path is left unnamed.
- Empty-assignee rules agree: Design :98 and claim.go row :159 both reject an empty flag value after trimming, while the stored-row cell `in_progress ∧ assignee==""` → CLAIM (:82, :98) governs legacy rows only. Rejecting the flag value is what keeps new claims from writing the empty owner that cell forgives, so the two rules reinforce rather than conflict.
- H4's two stated failure modes (missing case → `default:` false → renewal never propagates; wrong comparator → unconditional change → import churn) are complementary, not contradictory.
- H5 and H5r agree on mechanism: hash-matched rows short-circuit (importer.go:602-606) and lag; external_ref-matched rows propagate via `updated_at` (importer.go:548-598). No unchanged sentence asserts the older unconditional lag.
- Unchanged sections that reference the changed contract (E2E step 2 at :228 "one exit 0 and one exit 3", Proof row 1 at :238, deploy line :181) remain true under the reworded exit contract.
