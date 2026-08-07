VERDICT: FIX findings=2
| ID | sev | file:line | defect (one sentence) | fix (one sentence) |
|---|---|---|---|---|
| F1 | low | cmd/bd/claim_test.go:425 | The comment asserts "Every fallback message says 'direct mode'", but `emitVerboseWarning` (daemon_autostart.go:423-428) returns without printing for `FallbackWorktreeSafety` and `FallbackFlagNoDaemon`, so two of the seven fallback reasons pass the guard silently and the sentence still overstates it. | Reword to "every fallback reason this test can hit prints 'direct mode'", or drop the universal claim and keep only the sentence naming the daemon counters as the proof. |
| F2 | low | /tmp/bd-claim-e2e-* (4 dirs, ~132 MB) | The builder's own pre-fix test runs at 19:25-19:27 left four 33 MB binary directories in /tmp that the new TestMain cleanup does not retroactively remove, so the leak this item closed still occupies disk. | Trash the four directories with `~/projects/shared-docs/scripts/trash.sh /tmp/bd-claim-e2e-*`. |
