# RCA: w2_stale-race Phase 3 E2E failure (`e2e_scratch.sh` against `/tmp/bd.new`)

Investigator run: 2026-08-05, worktree `/home/ben/worktrees/beads/w2_stale-race`, scratch work under `/tmp`.
Binaries compared: `/tmp/bd.new` = `bd version 0.34.0 (f31496d65)` (the fix), `~/.local/bin/bd.cd33f0f3.bak` = `bd version 0.34.0 (cd33f0f3: main@cd33f0f306ef)` (currently installed, pre-fix). No tracked file was modified; no daemon outside my own scratch directories was touched.

---

## 1. VERDICT

**SCRIPT-DEFECT-ONLY.** The E2E script asserts a symptom that only exists on the pre-fix binary — a dirty row that survives daemon startup — so it cannot pass against the fixed binary; with three mechanical corrections and no code change, the same script prints `E2E_PASS` on `/tmp/bd.new` and still fails on the old binary.

---

## 2. Mechanisms

### 2.1 Mechanism 1 as posed (empty `LOG`) — **REFUTED**

The claim was that `LOG=$(ls -1 .beads/daemon-*.log 2>/dev/null | tail -1) || LOG=.beads/daemon.log` leaves `LOG` empty because `tail` exits 0.

That is true only without `pipefail`. The script's first line is `set -euo pipefail`, and with `pipefail` the pipeline takes `ls`'s failure status (2), so the `||` fallback *does* fire.

```
$ mkdir -p /tmp/rca_mech1/.beads
$ printf '[x] Flushing 1 dirty issues before import...\n' > /tmp/rca_mech1/.beads/daemon.log
$ cd /tmp/rca_mech1
$ bash -c 'set -euo pipefail; LOG=$(ls -1 .beads/daemon-*.log 2>/dev/null | tail -1) || LOG=.beads/daemon.log; echo "LOG=[$LOG]"'
LOG=[.beads/daemon.log]
$ bash -c 'set -eu;           LOG=$(ls -1 .beads/daemon-*.log 2>/dev/null | tail -1) || LOG=.beads/daemon.log; echo "LOG=[$LOG]"'
LOG=[]
```

And with the script's own settings the counter reads the real file:

```
count_flushes_with_script_LOG=1
real_count_in_file=1
```

So `LOG` resolved correctly in the failed run and `count_flushes` was grepping the right file. Confirmed independently by the failed run's own scratch repo: it contains `.beads/daemon.log` with zero `Flushing` lines, i.e. the counter reported 0 because there was nothing to count, not because it was blind.

**Classification: not a defect at all** — but a fragile coupling worth removing (see §5, item d): the line only works because of `pipefail`, which is invisible at the point of use.

### 2.2 Mechanism 2 (startup sync retires the dirty row) — **CONFIRMED, and it is the sufficient cause**

Causal chain, each link with the output that establishes it.

**Link 1 — the daemon runs a full sync cycle at startup, before any watcher exists.** `cmd/bd/daemon.go:546` calls `doSync()` unconditionally, before the mode switch at :551 that starts event-driven mode. The failed run's own log shows it:

```
[2026-08-05 16:50:47] RPC server ready (socket listening)      <- the script's readiness probe fires here
[2026-08-05 16:50:47] Starting sync cycle...
[2026-08-05 16:50:47] Exported to JSONL
[2026-08-05 16:50:47] Imported from JSONL
[2026-08-05 16:50:47] Sync cycle complete
[2026-08-05 16:50:47] Using event-driven mode                  <- watcher only exists from here on
```

**Link 2 — under the fix, that startup export retires the dirty marker; under the old binary it does not.** Same setup script (`/tmp/rca_setup.sh`: create issue, `--no-daemon --no-auto-flush update`, start daemon, wait for the event loop):

```
$ bash /tmp/rca_setup.sh /tmp/bd.new /tmp/rca_new1
dirty_before_daemon=1
Daemon started (PID 1103408)
dirty_after_daemon_start=0        <-- fixed binary: the startup publish cleared it

$ bash /tmp/rca_setup.sh ~/.local/bin/bd.cd33f0f3.bak /tmp/rca_old1
dirty_before_daemon=1
Daemon started (PID 1135328)
dirty_after_daemon_start=1        <-- old binary: the marker is stranded
```

This is the fix working exactly as `plan_v5.md` describes it ("The publisher retires the dirty markers it flushed, so a repository with no new mutations flushes once and stops"). No data is lost: the published JSONL carries the flushed content — the failed run's `/tmp/tmp.Cwf80OF3ar/.beads/issues.jsonl` holds `"status":"in_progress"`, and the DB metadata records its hash (`jsonl_content_hash = 557a87a0…` = the file's sha256).

**Link 3 — the flush the script counts is gated on `dirtyCount > 0`.** `cmd/bd/daemon_sync.go:562-564`:

```go
} else if dirtyCount > 0 {
    log.log("Flushing %d dirty issues before import...", dirtyCount)
    if err := exportToJSONLWithStore(importCtx, store, jsonlPath); err != nil {
```

With the marker already retired, the watcher pass runs the import branch and logs no `Flushing` line at all. Reproduced directly, with the watcher provably live:

```
$ bash /tmp/rca_rewrite.sh /tmp/rca_new1     # cp aside + mv back, byte-identical
rewrite: inode 36365187 -> 36365188 ; hash_same=yes
flush_lines=0
dirty=0
[16:55:35] JSONL file created: /tmp/rca_new1/.beads/issues.jsonl
[16:55:35] File change detected: /tmp/rca_new1/.beads/issues.jsonl
[16:55:36] Import triggered by file change
[16:55:36] Starting auto-import...
[16:55:36] Skipping auto-import: JSONL content unchanged after pull
```

**Link 4 — put a dirty row in front of a live watcher and the flush the plan wanted happens, exactly once.** Same repo, same daemon:

```
$ /tmp/bd.new --no-daemon --no-auto-flush update t-nws --priority 1
dirty_now=1
$ bash /tmp/rca_rewrite.sh /tmp/rca_new1
flush_delta_after_first_rewrite=1
dirty_after=0
[16:55:53] Flushing 1 dirty issues before import...
$ bash /tmp/rca_rewrite.sh /tmp/rca_new1 ; sleep 10
flush_delta_after_second_touch=0
dirty=0
```

**Classification: script defect.** The script creates its dirty row *before* `bd daemon --start` (line 12 vs line 14) and measures a flush that the fix has already performed one step earlier.

### 2.3 Mechanism 3 (readiness probe races the watcher) — **CONFIRMED, latent, not the cause of this failure**

The script's readiness probe is `[ -S .beads/bd.sock ]`. The socket is announced at "RPC server ready" — *before* the startup sync and before the watcher is created. Everything the script does after that probe can land in a window where no watcher exists.

The failed run took that window: its daemon log has **no** `JSONL file created` / `File change detected` line at all, so the `mv` was never observed. My verbatim replay of the same script took the other side of the race — the watcher fired — **and failed identically**:

```
$ bash work/w2_stale-race/e2e_scratch.sh
dirty_after_update=1
Daemon started (PID 1153765)
flushes=0 dirty_after_flush=0
SCRATCH_DIR=/tmp/tmp.QLdjwRl2dX
exit=1

$ cat /tmp/tmp.QLdjwRl2dX/.beads/daemon.log | tail -6
[16:57:46] JSONL file created: /tmp/tmp.QLdjwRl2dX/.beads/issues.jsonl
[16:57:47] Import triggered by file change
[16:57:47] Starting auto-import...
[16:57:47] Skipping auto-import: JSONL content unchanged after pull
```

Same failure with and without the watcher event. Mechanism 2 alone determines the outcome; this one only decides whether the log looks empty or busy.

Note this also refutes a tempting third theory: the watcher does *not* miss a rename-over. It watches the parent directory (`cmd/bd/daemon_watcher.go:91`) and reacts to the CREATE event.

**Classification: script defect** (flaky readiness probe).

### 2.4 Mechanism 4 (the dirty count is read before the flush completes) — **CONFIRMED, would have failed the script even with §2.2 fixed**

`daemon_sync.go:563` logs `Flushing …` **before** calling `exportToJSONLWithStore` on :564. The script breaks its poll loop the instant the log line appears and reads the dirty count in the next statement, so it samples the state mid-flush. My first corrected variant (which fixed only the readiness probe and the dirty-row ordering) hit it:

```
$ bash /tmp/rca_e2e_fixed.sh
dirty_after_update=1
flushes=1 dirty_after_flush=1     <-- assertion test "$D" = 0 fails
exit=1
```

The instant-sample is the whole difference: adding a bounded wait for the count to reach 0 (and nothing else) turns it into a pass — see §4.3.

There is a second-order effect worth recording, because it looks alarming and is not: that failed assertion triggered the script's EXIT trap, which stopped the daemon *during* the publish. The state left behind was a written-and-renamed file whose hash equals `jsonl_pending_hash`, an un-promoted `jsonl_content_hash`, and a surviving dirty row:

```
jsonl_pending_hash|ec1dcede8a5f7181     sha256(.beads/issues.jsonl) = ec1dcede8a5f7181
jsonl_content_hash|00bdd3afd2308fed
dirty=1
```

That is precisely the mid-publication state the protocol is built for, and it reads correctly:

```
$ /tmp/bd.new  --no-daemon --db /tmp/rca_e2e_fixed_repo/.beads/beads.db --no-auto-import info ; echo $?
0      (no out-of-sync message — the reader accepts the pending hash)
$ ~/.local/bin/bd.cd33f0f3.bak --no-daemon --db /tmp/rca_e2e_fixed_repo/.beads/beads.db --no-auto-import info ; echo $?
1      Error: Database out of sync with JSONL. Run 'bd sync --import-only' to fix.
```

and it self-heals on the next daemon start (pending promoted, marker cleared, file bytes untouched):

```
before: pending=ec1dcede… content=00bdd3af… dirty=1   file=ec1dcede…
after:  pending=(empty)   content=ec1dcede… dirty=0   file=ec1dcede…
```

**Classification: script defect** (measurement race). The mid-publish state is code behaving as designed.

---

## 3. Five-why chain (deepest cause)

1. **Why did the gate fail?** `test "$((F-B0))" = 1` saw zero `Flushing` lines in the daemon log.
2. **Why zero?** The watcher's pre-import flush is gated on `dirtyCount > 0` (`daemon_sync.go:562`), and the dirty count was 0 when the script's same-bytes rewrite arrived.
3. **Why 0?** The daemon runs a full sync cycle at startup (`daemon.go:546`), and under the new publish protocol that export publishes the dirty issue and conditionally retires its marker — the script created the dirty row *before* starting the daemon, so the startup cycle consumed it.
4. **Why does the script assume the marker outlives daemon startup?** It was written against the pre-fix binary, where export left markers stranded (`dirty_after_daemon_start=1`, §2.2 link 2). The stranded marker *is* the bug: it is what drove the 1 Hz export loop.
5. **Why was a pre-fix precondition frozen into the acceptance gate?** The E2E was derived from the symptom reproduction and pasted into `plan_v5.md` as a byte-for-byte, "substitute nothing" artifact (R3-11). No pass re-derived it against the post-fix state machine — even though the plan's own prose one section earlier states that the publisher now retires the markers it flushes. The contradiction sat inside a single document and the "do not modify" instruction removed the builder's licence to notice it.

**Deepest cause:** the acceptance test encodes the *old* system's intermediate state as its precondition, and the plan's immutability rule prevented that from being reconciled with the design it was meant to verify. A test that observes a symptom must be re-derived from the fixed state machine, not frozen from the reproduction.

---

## 4. Independent behavior evidence (old vs new)

Script used: `/tmp/rca_info_probe.sh <binary> <dir>` — `git init`; `bd init --prefix t --quiet`; `bd --no-daemon create`; then `bd --no-daemon --db <db> --no-auto-import info` at three states. No daemon involved anywhere (verified: `pgrep -af '[b]d daemon'` showed no scratch daemon).

### 4.1 Same-bytes rewrite (the item's User Intent) and genuine divergence

```
$ bash /tmp/rca_info_probe.sh ~/.local/bin/bd.cd33f0f3.bak /tmp/rca_info_old
### binary: bd version 0.34.0 (cd33f0f3: main@cd33f0f306ef)
--- baseline: no rewrite yet ---
baseline_info_exit=0
baseline: no out-of-sync message
--- after same-bytes rewrite (bytes identical: yes, new inode+mtime) ---
same_bytes_info_exit=1
Error: Database out of sync with JSONL. Run 'bd sync --import-only' to fix.
--- after genuine divergence (extra line appended) ---
diverged_info_exit=1
Error: Database out of sync with JSONL. Run 'bd sync --import-only' to fix.

$ bash /tmp/rca_info_probe.sh /tmp/bd.new /tmp/rca_info_new
### binary: bd version 0.34.0 (f31496d65)
--- baseline: no rewrite yet ---
baseline_info_exit=0
baseline: no out-of-sync message
--- after same-bytes rewrite (bytes identical: yes, new inode+mtime) ---
same_bytes_info_exit=0
same-bytes: no out-of-sync message
--- after genuine divergence (extra line appended) ---
diverged_info_exit=1
Error: Database out of sync with JSONL. Run 'bd sync --import-only' to fix.
```

The false positive is gone and the true positive is intact. This is the item's whole purpose, and it is delivered.

### 4.2 Bonus: the old binary *misses* a divergence the new one catches

Replacing the JSONL with different bytes and an **older** mtime (`touch -d '2020-01-01'`) — the "someone restored an old file" shape:

```
$ bash /tmp/rca_restored_old.sh /tmp/bd.new /tmp/rca_restored_new
### bd version 0.34.0 (f31496d65)
restored_old_info_exit=1
Error: Database out of sync with JSONL. Run 'bd sync --import-only' to fix.

$ bash /tmp/rca_restored_old.sh ~/.local/bin/bd.cd33f0f3.bak /tmp/rca_restored_old
### bd version 0.34.0 (cd33f0f3: main@cd33f0f306ef)
restored_old_info_exit=0
restored-old: NO out-of-sync message (would be a miss)
```

Content-based freshness is strictly more accurate in both directions than the mtime-based check it replaced.

### 4.3 The 1 Hz rewrite loop

Old binary, daemon at `--interval 1s`, one stranded dirty row, one same-bytes rewrite:

```
rewrite: inode 36365325 -> 36365326 ; hash_same=yes
t+1s  inode=36365326 mtime=16:57:05 dirty=1 flushes=1
t+2s  inode=36365325 mtime=16:57:06 dirty=1 flushes=2
t+3s  inode=36365326 mtime=16:57:07 dirty=1 flushes=3
t+4s  inode=36365320 mtime=16:57:08 dirty=1 flushes=4
t+5s  inode=36365325 mtime=16:57:09 dirty=1 flushes=5
t+6s  inode=36365325 mtime=16:57:11 dirty=1 flushes=6
t+7s  inode=36365325 mtime=16:57:11 dirty=1 flushes=7
t+8s  inode=36365320 mtime=16:57:12 dirty=1 flushes=8
t+9s  inode=36365325 mtime=16:57:13 dirty=1 flushes=9
t+10s inode=36365320 mtime=16:57:14 dirty=1 flushes=10
```

New binary, same scenario after its single flush:

```
t+1s … t+10s : inode=36365189 mtime=16:56:04.623590765 dirty=0 flushes=1   (unchanged for all ten samples)
```

The loop is gone: one flush, marker retired, file never rewritten again.

### 4.4 The plan's own gate, corrected, run end to end

`/tmp/rca_e2e_fixed2.sh` is `e2e_scratch.sh` with exactly the three corrections in §5 (a)(b)(c) and nothing else:

```
$ bash /tmp/rca_e2e_fixed2.sh
dirty_after_update=1
flushes=1 dirty_after_flush=0
flushes_after_second_touch=1
same_bytes_info_exit=0
divergence_caught=yes
Error: Database out of sync with JSONL. Run 'bd sync --import-only' to fix.
E2E_PASS
exit=0
```

The same corrected script still fails on the pre-fix binary, so it is a real gate and not a rubber stamp:

```
$ bash /tmp/rca_e2e_old.sh        # identical, BD=~/.local/bin/bd.cd33f0f3.bak
dirty_after_update=1
flushes=1 dirty_after_flush=1     <-- after a 10s bounded wait; assertion test "$D" = 0 fails
```

---

## 5. What the E2E script would have to change (not applied)

Against `work/w2_stale-race/e2e_scratch.sh`:

**(a) Wait for the watcher, not for the socket.** Replace the readiness loop on line 15 (`[ -S .beads/bd.sock ]`) with a wait for the event loop to announce itself, e.g. poll `grep -q "Using event-driven mode" .beads/daemon.log`, then a short settle. The socket is bound before the startup sync cycle and before the file watcher is created; every step after the current probe can race both.

**(b) Create the stranded dirty row after the daemon's startup cycle, not before it.** Move line 12 (`"$BD" --no-daemon --no-auto-flush update "$ID" --status in_progress`) and its `dirty_after_update` assertion (line 13) to *after* the readiness wait of (a). The fixed publisher retires dirty markers when it publishes, and the daemon publishes once at startup, so a marker created beforehand is gone before the measurement window opens. This is the change without which the script can never pass.

**(c) Let the flush finish before sampling the dirty count.** The `Flushing …` line is written before `exportToJSONLWithStore` runs (`daemon_sync.go:563-564`), so the current `for … [ "$F" -gt "$B0" ] && break` loop samples mid-publish. Add a second bounded poll — `for i in $(seq 1 20); do D=$(sqlite3 …); [ "$D" = 0 ] && break; sleep 0.5; done` — before the `test "$D" = 0` on line 22. Keep the assertion; only stop reading it at the wrong instant.

**(d) Two robustness nits, optional.** Line 16's `LOG=$(ls -1 .beads/daemon-*.log …| tail -1) || LOG=…` works only because `pipefail` is set thirteen lines earlier; the daemon's log is always `.beads/daemon.log`, so a literal assignment removes an invisible dependency. And line 28's `echo "same_bytes_info_exit=$?"` always prints 0 under `set -e` — it reports nothing; the real assertion is `set -e` itself.

With (a)(b)(c) the script measures what the plan intended: one flush, marker retired, no second flush, same-bytes freshness, divergence still caught.

---

## 6. Cross-checked and ruled out

- **`LOG` resolution (posed mechanism 1)** — refuted under `pipefail`; the counter was reading the correct, genuinely empty-of-`Flushing` log. §2.1.
- **Watcher blind to rename-over** — refuted; the parent-directory watch produces `JSONL file created` and the replay run took that path and still failed. §2.3.
- **Data loss from the startup flush clearing markers** — ruled out: the published JSONL carries the flushed value (`"status":"in_progress"` in the failed run's own scratch file) and the recorded hash equals the file's sha256. Marker clearing is conditional on `marked_at` (`TestPublishKeepsIssueDirtyWhenRemarkedDuringExport`, `TestPublishClearsDirtyMarkers` in `internal/jsonlpub/jsonlpub_test.go`).
- **Mid-publish interruption leaving a permanently wrong verdict** — ruled out: the pending state reads fresh on the new binary, stale on the old one, and the next daemon start promotes it and clears the marker. §2.4.
- **The corrected script being a rubber stamp** — ruled out: it fails on `~/.local/bin/bd.cd33f0f3.bak`. §4.4.
- **Divergence detection weakened by the fix** — ruled out twice: appended line caught (§4.1), and the restored-older-file case that the old binary *misses* is now caught (§4.2).

**What would have changed my verdict, and whether I looked for it.** I would have written CODE-DEFECT if any of these had held; I ran each one:
1. `/tmp/bd.new` reporting "out of sync" after a same-bytes rewrite → it exits 0 (§4.1).
2. `/tmp/bd.new` failing to catch an appended line, or the restored-old-file shape → both caught (§4.1, §4.2).
3. The new daemon still rewriting the JSONL once per second → ten identical inode/mtime samples over ten seconds (§4.3).
4. The startup flush clearing a dirty marker whose content never reached the JSONL → content present, hash recorded (§6 bullet 3).
5. The plan's gate still failing after only mechanical script corrections → it prints `E2E_PASS` (§4.4).
6. A stranded pending/dirty state surviving a daemon restart → promoted and cleared on next start (§2.4).

---

## 7. Housekeeping

Scratch daemons I started and stopped, by recorded PID: 1103408 (`/tmp/rca_new1`), 1135328 (`/tmp/rca_old1`), 1171069 (`/tmp/rca_e2e_fixed_repo`), 1220868 (`/tmp/rca_e2e_fixed2_repo`), 1236978 and 1267897 (`/tmp/rca_e2e_old*_repo`), 1276803 (restart probe), plus 1153765 from the verbatim replay (stopped by the script's own trap). All verified `stopped`; `bd daemons list` shows none under `/tmp`. The seven pre-existing `~/projects` daemons were counted before and after (7 → 7) and never touched.

Scratch repos left in place for inspection: `/tmp/tmp.Cwf80OF3ar` (original failure), `/tmp/tmp.QLdjwRl2dX` (replay), `/tmp/rca_new1`, `/tmp/rca_old1`, `/tmp/rca_info_new`, `/tmp/rca_info_old`, `/tmp/rca_restored_new`, `/tmp/rca_restored_old`, `/tmp/rca_e2e_fixed_repo`, `/tmp/rca_e2e_fixed2_repo`, `/tmp/rca_e2e_old2_repo`, `/tmp/rca_mech1`. Diagnostic scripts: `/tmp/rca_setup.sh`, `/tmp/rca_rewrite.sh`, `/tmp/rca_info_probe.sh`, `/tmp/rca_restored_old.sh`, `/tmp/rca_e2e_fixed.sh`, `/tmp/rca_e2e_fixed2.sh`, `/tmp/rca_e2e_old.sh`.

No tracked file was modified. Nothing was fixed.
