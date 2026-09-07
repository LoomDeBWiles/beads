# Work Report: w4_id-ownership

## Summary
bd now asks one question about which project an issue id belongs to, on create and on import alike: does the id start with the configured prefix and a hyphen. Shipped from plan_v2.md at tip a74b47d6a; the installed `bd` (0.34.0, 02e0ab0fe) is the final tree.

## Dispatch Ledger

| Backend | Dispatches | Where |
|---------|-----------|-------|
| Claude (Opus, build lane) | 5 | builder r1 (plan), r2 (test isolation + flush race), r3 (F1, F2), r4 (G1 revert, G2), r5 (H1-H3) |
| Claude (Opus, review lane) | 3 | final review v1, scoped re-reviews v2 and v3 |

## Acceptance

All 5 ship-gate requirements proved by `ship-gate.py run` at tip a74b47d6a (`ship_gate_result.json`): R1 `demo-w1-p0a` round-trips (`Import complete`); R2 `other-bad1` still refused (`prefix mismatch`); R3 farmplanner, duke, investing, teaching import clean from copies with 0 created and 0 updated (duke and teaching were refused files before); R4 the generated post-merge hook prints bd's own `prefix mismatch` line and still exits 0; R5 the package suites all `ok`. `go test ./...` is green under a plain env and under `BD_JSON=true BD_ACTOR=x BEADS_FLUSH_DEBOUNCE=99s BD_NO_DAEMON=true` (r5-step1.log, r5-step2.log). No PENDING rows.

## Residual Risk

| Item | Kind | What is left open, and why shipping is still right |
|------|------|----------------------------------------------------|
| F3 | residual finding | The beads repo's own `.beads/issues.jsonl` still fails import, now on `invalid issue type: event` for 22 plain `bd-*` rows from upstream Gas Town data. That is a type, not a name; the prefix refusal this item removes no longer fires on the file, and the 88 hyphenated ids it admits carry no `event` row. Out of the plan's scope. |

No gate residues, no gate revisions.

## Follow-Up Required
- The beads repo's export holds 22 `event`-type rows bd cannot load (F3 above). Root cause: rows from upstream data with an issue type this fork does not know. Approach: decide whether `event` becomes a type or those rows are converted, then import.
- 105 Go files under `cmd/bd`, `internal/config`, `internal/importer` were already unformatted at HEAD before this item (r5-step3.log). A repo-wide `gofmt -w` in its own commit.
- The post-merge hooks on disk in the 17 repos other than fleet still discard the import error; each picks up the fixed template the next time its hooks are written by `bd init`.

## Deviations
- Builder r2 was briefed for test isolation only and also fixed a real data-loss race in `cmd/bd/flush_manager.go`: a mutation immediately followed by `Shutdown` could take the shutdown branch first and skip the final flush. Kept: the reviewer reproduced the race by reverting the hunk and proved the fix under `-race`; it is the flake behind `TestAutoFlushOnExit` and the standing rule is root cause over symptom.
- Phase 3's `cp` over the running binary failed with "Text file busy"; the builder copied beside it and used an atomic `mv`. Same outcome, no truncation window. Repeated at r3, r4, r5 so the installed binary tracks the final tree.
- Test-only changes beyond the plan: `cmd/bd/autostart_test.go`, `internal/config/config_test.go` and `cmd/bd/claim_test.go` no longer read the host's `.beads/config.yaml`, `~/.config/bd/config.yaml` or `BD_*`/`BEADS_*` env. The suite was red on main because w28 committed `no-daemon: true` and `auto-start-daemon: false`; the tests asserted built-in defaults while reading real config.
- One stderr line added in the importer's cross-prefix skip branch naming both ids and the prefix scope, so a skipped same-content row is no longer silent (H3).

## Key Decisions
- F2 accepted in review v1 (add a multi-repo short-circuit to the rename test), then reverted after review v2 showed it steered a foreign id into `handleRename`, whose `CreateIssue` validates the configured prefix in every mode, so the batch aborted instead of one row being skipped. The configured-prefix test is exactly the question that create will ask, so it is the right predicate whenever a prefix is configured. The importer differs from the plan commit only in comments and the H3 message.
- Review cap of 3 reached at v3; its three low findings were fixed by builder r5 and verified by the manager (tests, gofmt, diff) rather than a fourth review.
- No upstream pull request, local fork commit only, per Ben's lock-in.

## Notes
- Rollback: `git revert` the item's commits on the fork, then `cp ~/.local/libexec/bd-real.bak-w4 ~/.local/libexec/bd-real` (the pre-item binary, 01:30 Sep 7). Fleet's hook: `~/projects/fleet/.git/hooks/post-merge.bak-w4`.
- `ExtractIssuePrefix` still guesses where no configured prefix exists (`bd init`, autoimport inference, `detectPrefixes`) and still labels refused prefixes in messages. It no longer decides membership anywhere a prefix is configured.
