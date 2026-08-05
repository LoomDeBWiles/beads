# plan_delta_v6: correct the E2E gate's frozen pre-fix precondition

Revision of `plan_v5.md`. **Scope unchanged** — no design change, no code change, no
new files, no change to what ships. This delta corrects the plan's own acceptance
script, which could never pass against the fixed binary. It therefore does not
return to the human gate.

Basis: `rca_e2e_v1.md` (verdict SCRIPT-DEFECT-ONLY), which reproduced every claim it
makes and confirmed the corrected script still fails on the pre-fix binary.

## Why v5's E2E could not pass

v5's Verification froze the symptom reproduction into the plan as a byte-for-byte,
"substitute nothing" artifact (R3-11). That reproduction's precondition is a dirty
marker that survives daemon startup — which is the bug. The fixed publisher retires
the markers it flushes, and the daemon publishes once at startup
(`cmd/bd/daemon.go:546`, before the mode switch at `:551`), so the marker the script
creates on its line 12 is already gone before its measurement window opens. The
watcher's pre-import flush is gated on `dirtyCount > 0`
(`cmd/bd/daemon_sync.go:562`), so it logs nothing and the count stays at zero.

The plan's own Design section one page earlier states that the publisher now retires
those markers. The contradiction sat inside a single document, and the
"substitute nothing" instruction removed the builder's licence to notice it.

| ID | v5 said | v6 says | Why |
|----|---------|---------|-----|
| E1 | Readiness probe waits for `[ -S .beads/bd.sock ]` | Wait for `Using event-driven mode` in the daemon log, then settle | The socket is bound before the startup sync cycle and before the file watcher exists, so every later step races both. Confirmed: the original failure's daemon log records no file-change event at all, while a verbatim replay took the other side of the race and failed identically. |
| E2 | The stranded dirty row is created before `bd daemon --start` | It is created after the readiness wait | **The correction without which the script can never pass.** A marker created before startup is consumed by the startup publish. Measured directly: `dirty_after_daemon_start=0` on the fixed binary, `=1` on the pre-fix binary. |
| E3 | Dirty count sampled the instant a `Flushing` line appears | A bounded poll waits for the count to reach 0 first; the `test "$D" = 0` assertion is unchanged | `Flushing …` is logged *before* `exportToJSONLWithStore` runs (`daemon_sync.go:563-564`), so the old loop sampled mid-publish. Only the reading instant moves; the assertion stays. |
| E4 | `LOG=$(ls -1 .beads/daemon-*.log …\| tail -1) \|\| LOG=.beads/daemon.log`; `echo "same_bytes_info_exit=$?"` | Literal `LOG=.beads/daemon.log`; the misleading `$?` echo dropped | Robustness only. The `\|\|` fallback works, but only because `pipefail` is set thirteen lines earlier — an invisible dependency. The `$?` echo always prints 0 under `set -e`; the real assertion is `set -e` itself. My own first reading of this line was wrong (I tested it without `pipefail` and wrongly called it the cause); the RCA refuted that. |

Nothing else in the script changes. The E2E still asserts, in order: one flush, dirty
count 0, no second flush, same-bytes freshness, and genuine divergence still caught.

## The corrected gate is not a rubber stamp

Proven both directions before adoption (`rca_e2e_v1.md` §4.4):

- Against `/tmp/bd.new` (the fix): prints `E2E_PASS`, exit 0.
- Against `~/.local/bin/bd.cd33f0f3.bak` (pre-fix): fails at `test "$D" = 0` with
  `flushes=1 dirty_after_flush=1` after the same bounded wait.

## Independent confirmation the shipped code delivers User Intent

Measured old binary vs new on identical scenarios, no daemon involved
(`rca_e2e_v1.md` §4.1-§4.3):

| Scenario | pre-fix `cd33f0f3` | fixed `f31496d65` |
|---|---|---|
| Same-bytes rewrite, then direct `info` | exit 1, "Database out of sync with JSONL" | exit 0, no message |
| Genuine divergence (line appended) | exit 1, caught | exit 1, caught |
| Restored older file, different bytes | exit 0 — **missed** | exit 1, caught |
| Daemon with a stranded dirty row, 10 s | 10 flushes, file rewritten each second | 1 flush, marker retired, file untouched for all 10 samples |

The false positive is gone, the true positive is intact, and the content predicate is
strictly more accurate than the mtime one in both directions.

## Deploy

`plan_v5.md`'s Phase 3 is unchanged. Step 4 runs the corrected script; steps 5-7
follow as written. Deploy resumes at step 4.
