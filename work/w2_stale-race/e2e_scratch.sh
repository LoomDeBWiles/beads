#!/usr/bin/env bash
set -euo pipefail
BD=/tmp/bd.new
S=$(mktemp -d)
count_flushes() { local n; n=$(grep -c "Flushing" "$LOG" 2>/dev/null) || n=0; printf '%s\n' "$n"; }   # R4-7: no double-zero
cleanup() { (cd "$S" && "$BD" daemon --stop >/dev/null 2>&1) || true; echo "SCRATCH_DIR=$S (leave for trash.sh)"; }
trap cleanup EXIT   # R4-9: scratch daemon stopped on success AND failure
cd "$S"
git init -q .
"$BD" init --prefix t --quiet
ID=$("$BD" --no-daemon create "e2e probe" --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
"$BD" --no-daemon --no-auto-flush update "$ID" --status in_progress
D=$(sqlite3 .beads/beads.db "SELECT COUNT(*) FROM dirty_issues"); echo "dirty_after_update=$D"; test "$D" = 1
"$BD" daemon --start --interval 1s
for i in $(seq 1 20); do [ -S .beads/bd.sock ] && break; sleep 0.5; done
LOG=$(ls -1 .beads/daemon-*.log 2>/dev/null | tail -1) || LOG=.beads/daemon.log
B0=$(count_flushes)
cp .beads/issues.jsonl /tmp/e2e_same.$$ && mv /tmp/e2e_same.$$ .beads/issues.jsonl
F=$B0
for i in $(seq 1 20); do F=$(count_flushes); [ "$F" -gt "$B0" ] && break; sleep 0.5; done
D=$(sqlite3 .beads/beads.db "SELECT COUNT(*) FROM dirty_issues"); echo "flushes=$((F-B0)) dirty_after_flush=$D"
test "$((F-B0))" = 1; test "$D" = 0
cp .beads/issues.jsonl /tmp/e2e_same.$$ && mv /tmp/e2e_same.$$ .beads/issues.jsonl
sleep 10
F2=$(count_flushes); echo "flushes_after_second_touch=$((F2-B0))"
test "$((F2-B0))" = 1   # old binary loops once per second here
"$BD" --no-daemon --db "$S/.beads/beads.db" --no-auto-import info --json >/dev/null
echo "same_bytes_info_exit=$?"
printf '%s\n' '{"id":"t-zzz9","title":"divergence probe","status":"open","issue_type":"task"}' >> .beads/issues.jsonl
if "$BD" --no-daemon --db "$S/.beads/beads.db" --no-auto-import info --json >/dev/null 2>/tmp/e2e_err.$$; then
  echo "DIVERGENCE NOT CAUGHT"; exit 1
else
  echo "divergence_caught=yes"; grep -i "out of sync" /tmp/e2e_err.$$ || true
fi
echo E2E_PASS
