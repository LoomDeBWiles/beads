# Manager Log: w4_id-ownership

## 2026-09-06 — Item Start / Pre-Plan

Origin: fleet's post-merge `bd import` failed for months. Traced to bd holding
two different rules for "which project does this issue id belong to".

Facts established before any plan:
- Create path: `internal/storage/sqlite/ids.go:68 ValidateIssueIDPrefix` asks
  `strings.HasPrefix(id, prefix+"-")`. `ben-w31-p0a` passes.
- Import path: `internal/importer/importer.go:234` calls
  `internal/utils/issue_id.go:21 ExtractIssuePrefix`, which GUESSES the
  boundary from the tail (3-8 base36 chars, digit required at 4+) and returns
  `ben-w31`. `buildAllowedPrefixSet` allows only the configured prefix, so the
  whole file is refused.
- Repro from empty repo: `bd init --prefix demo` then
  `bd create "x" --id demo-w1-p0a` succeeds, and the immediate
  `bd export` + `bd import` fails.
- Blast radius: 267 ids across 5 repos (farmplanner 38, duke 22, investing 22,
  teaching 20, beads 165) plus fleet's 37, already renamed under w45.
- No configured prefix in any of Ben's 18 repos is a hyphen-prefix of another,
  so HasPrefix cannot misclassify a foreign id as local in this fleet today.
- The refusal itself is wanted: GH#686 (multi-repo mode turns it off) and
  bd-6pni (contributor PR tombstones from foreign prefixes) show the intent.
- beads is a fork of steveyegge/beads with an `upstream` remote, 3824 commits.
- Installed binary is `main@00d653c0f475`, an ancestor of local HEAD
  (a5ce30c1b), so the deployed bd is older than this fork's tip.

Direction proposed: where the database has a configured prefix, the importer
asks the same question the creator asks. Guessing survives only where there is
no configured prefix (`bd init` inferring from a file).

Rejected: adding a create-time check. Create already validates with the
correct rule; a second gate would keep two rules and fix none of the 267
existing ids.

- Next: user lock-in on direction, fork policy and rebuild scope.

## 2026-09-06 — Lock-in and Review R1

Ben locked the direction and the three open decisions:
- Local commit on the fork; no upstream pull request.
- Rebuild at the fork tip. The two commits ahead of the running binary touch
  only a config file and a work log, no program code, so the question I raised
  about shipping them was empty.
- Include the minimum hook change: print the real import error.

Wrote plan_v1.md (standard scale). `check_plan.py --lint` exits 0.

Scale: standard. About 30 lines across `internal/importer/importer.go` and
`cmd/bd/init.go` plus two tests, then a rebuild. Direct execution, no beads:
the bead threshold (5+ independently testable changes with ordering
dependencies and multiple builders) does not hold.

Dispatched: review lane, R1 (modules: Core, Plan Quality, Scenario Trace).
- launch=work/w4_id-ownership/review_codex_v1.md.launch
- done=work/w4_id-ownership/review_codex_v1.md.done
- Next: watch-done.sh on the done= path wakes me; disposition every finding,
  then either plan_v2.md or render the human gate.

## 2026-09-06 — Review R1 dispositions

- Output: review_codex_v1.md, VERDICT FIX findings=7
- Findings: 7 → ACCEPT 7, REJECT 0. Each verified against the code first.
  - ACCEPT F1 (high): `importer.go:278` hands `RenameImportedIssuePrefixes` the
    whole slice, so under `--rename-on-import` it would rewrite local
    `bd-w31-p0a` to `bd-p0a` and collapse it with `bd-w45-p0a`. Verified.
    New Changes row in `internal/importer/utils.go` plus a test.
  - ACCEPT F2 (med): `ValidateIssueIDPrefix(id, "")` is false for every id, so a
    prefix-less database would skip every rename it accepts today. v2 reads the
    prefix once and keeps the old comparison when it is empty.
  - ACCEPT F3 (med): gate 4 inlined the intended hook body, so it would pass
    green without the template edit. v2 runs the hook bd generates.
  - ACCEPT F4 (med): `repairPrefixes` (`cmd/bd/rename_prefix.go:236`) decides
    membership by guessing and mints a new hash id for ids create and import
    both call local. Verified. Brought into scope: a third contradicting rule
    defeats the item's purpose. `detectPrefixes` stays, it counts prefixes.
  - ACCEPT F5 (low): the tombstone label needs `ExtractIssuePrefix` too; row
    reworded.
  - ACCEPT F6 (low): `--quiet` exists (`init.go:478`); gates use it.
  - ACCEPT F7 (low): gates call the built binary directly, not the wrapper that
    sets the daemon off switch; gates now export it.
- Wrote plan_v2.md, `check_plan.py --lint` exits 0.
- No R2: every accepted fix applies the plan's existing single rule to two more
  call sites the reviewer itself specified, plus one branch that preserves
  today's behaviour. No new mechanism, state or ordering.
- Next: human gate.

## 2026-09-06 — Human Gate v2

- Rendered: http://localhost:8095/beads/w4_id-ownership/gate_summary.html
- gate armed: auto-dispatch on approve
- Posted to the inbox as beads-w4-gate-v2
- Next: Ben's verdict. On approve the exec-ledger watcher dispatches the /work
  session; on send_back his next message wakes this session.

## 2026-09-07 01:28 — Work Start / Builder r1

- Approved plan: plan_v2.md. Ledger gate w4-v2 consumed (rc=0).
- ship_gate.json compiled from the plan's 5-row table (rc=0).
- Budget: lane=claude used=13%, safe to dispatch.
- Route: bead-free (plan says Direct). Model Assignment: builder on Opus, so
  `dispatch-builder.sh --claude`, effort default medium.
- Dispatched builder r1 with the bead-free template plus item notes (all 5
  gates as acceptance criteria; fleet hook edit and binary swap approved).
  - log=work/w4_id-ownership/builds/build.r1.codex.log
  - done=work/w4_id-ownership/builds/build.r1.done
  - launch=work/w4_id-ownership/builds/build.r1.launch
- Watch: claude-watch.sh (silence 900s) armed in background; timeouts=0.
- Next: builder done → read build.md, then Final Review.
