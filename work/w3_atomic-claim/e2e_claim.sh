#!/usr/bin/env bash
#
# End-to-end proof for the atomic claim verb (bd-ok4pr.4.2).
#
# Runs the built bd binary against scratch stores under a temp directory this
# script creates and removes itself. It never touches the fleet store, the
# installed binary or any live daemon: every bd invocation carries --no-daemon and
# an explicit --db under the temp directory.
#
# The binary under test defaults to /tmp/bd-w3, which the acceptance step builds
# with `go build -o /tmp/bd-w3 ./cmd/bd/`. Override with BD_BIN=<path>.
#
# Prints E2E_PASS as the last line only when every step passed; otherwise exits
# non-zero with the failures named above.

set -uo pipefail

BD_BIN=${BD_BIN:-/tmp/bd-w3}

failures=0

fail() {
	printf 'FAIL: %s\n' "$*"
	failures=$((failures + 1))
}

check() {
	# check <description> <expected> <actual>
	if [ "$2" = "$3" ]; then
		printf '  ok: %s = %s\n' "$1" "$3"
	else
		fail "$1: expected [$2], got [$3]"
	fi
}

for tool in jq sqlite3; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		fail "required tool not found: $tool"
	fi
done
if [ ! -x "$BD_BIN" ]; then
	fail "binary under test not executable: $BD_BIN (build it with: go build -o /tmp/bd-w3 ./cmd/bd/)"
fi
if [ "$failures" -ne 0 ]; then
	exit 1
fi

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/bd_e2e_claim.XXXXXXXX") || exit 1

cleanup() {
	# Only ever removes the directory this script created, on every exit path.
	case "$SCRATCH" in
	*/bd_e2e_claim.*) rm -rf -- "$SCRATCH" ;;
	esac
}
trap cleanup EXIT INT TERM

# bd_at <store-dir> <args...> runs the binary against that store, direct mode only.
bd_at() {
	local store=$1
	shift
	"$BD_BIN" --no-daemon --no-auto-import --db "$SCRATCH/$store/.beads/beads.db" "$@"
}

# init_store <store-dir> <prefix> creates an empty scratch store.
init_store() {
	mkdir -p "$SCRATCH/$1" || return 1
	(cd "$SCRATCH/$1" && "$BD_BIN" init --quiet --prefix "$2" --skip-hooks --skip-merge-driver --no-daemon) >/dev/null 2>&1
}

# new_issue <store-dir> <title> prints the created issue's ID.
new_issue() {
	bd_at "$1" create "$2" -t task -p 1 --json 2>/dev/null | jq -r '.id'
}

# show_json <store-dir> <id> prints the issue object. `bd show --json` emits a
# one-element array, which every caller here unwraps.
show_json() {
	bd_at "$1" show "$2" --json 2>/dev/null | jq -c '.[0]'
}

# field <json> <jq-filter> reads one value out of an issue object.
field() {
	printf '%s' "$1" | jq -r "$2"
}

# epoch_nanos <rfc3339> normalizes a timestamp so two stores' renderings compare.
# An absent lease normalizes to the literal "none" rather than to an empty string,
# so a missing lease can never compare equal to another missing lease.
epoch_nanos() {
	case "$1" in
	"" | null) printf 'none' ;;
	*) date -u -d "$1" +%s%N 2>/dev/null || printf 'unparseable(%s)' "$1" ;;
	esac
}

echo "== step 1: init scratch store, create issue"
if ! init_store a e2e; then
	fail "could not init scratch store"
	exit 1
fi
race_id=$(new_issue a "concurrent claim target")
if [ -z "$race_id" ] || [ "$race_id" = "null" ]; then
	fail "could not create the step 2 issue"
	exit 1
fi
echo "  ok: store initialized, issue $race_id created open"

echo "== step 2: two concurrent claims on one open issue"
# The probe's failing scenario: before the atomic verb both claimants exited 0 and
# one acknowledged claim was silently lost.
bd_at a claim "$race_id" --assignee agent-A >"$SCRATCH/a.out" 2>&1 &
pid_a=$!
bd_at a claim "$race_id" --assignee agent-B >"$SCRATCH/b.out" 2>&1 &
pid_b=$!
wait "$pid_a"
exit_a=$?
wait "$pid_b"
exit_b=$?
echo "  agent-A exit=$exit_a, agent-B exit=$exit_b"

codes=$(printf '%s\n%s\n' "$exit_a" "$exit_b" | sort | tr '\n' ' ')
if [ "$codes" = "0 3 " ]; then
	echo "one exit=0, one exit=3"
else
	fail "expected exactly one exit 0 and one exit 3, got exits [$exit_a] and [$exit_b]"
fi

if [ "$exit_a" -eq 0 ]; then
	winner=agent-A
else
	winner=agent-B
fi
stored_assignee=$(field "$(show_json a "$race_id")" '.assignee')
check "stored assignee equals the winner" "$winner" "$stored_assignee"

echo "== step 3: expired lease is stolen, event names the prior holder"
lease_id=$(new_issue a "lease target")
bd_at a claim "$lease_id" --assignee agent-C --lease 1s >/dev/null 2>&1
check "agent-C claim exit" "0" "$?"
sleep 2
steal_json=$(bd_at a claim "$lease_id" --assignee agent-D --json 2>/dev/null)
check "agent-D claim exit" "0" "$?"
check "outcome" "stolen" "$(printf '%s' "$steal_json" | jq -r '.outcome')"
check "displaced holder" "agent-C" "$(printf '%s' "$steal_json" | jq -r '.holder')"

event_value=$(sqlite3 "$SCRATCH/a/.beads/beads.db" \
	"SELECT new_value FROM events WHERE issue_id = '$lease_id' ORDER BY id DESC LIMIT 1")
check "event previous_holder" "agent-C" "$(printf '%s' "$event_value" | jq -r '.previous_holder')"

echo "== step 4: the holder renews"
renew_json=$(bd_at a claim "$lease_id" --assignee agent-D --json 2>/dev/null)
check "agent-D renewal exit" "0" "$?"
check "outcome" "renewed" "$(printf '%s' "$renew_json" | jq -r '.outcome')"

echo "== step 5: leaving in_progress clears the lease"
bd_at a update "$lease_id" --status open >/dev/null 2>&1
check "update exit" "0" "$?"
released=$(show_json a "$lease_id")
check "status" "open" "$(field "$released" '.status')"
check "claim_expires_at present" "false" "$(field "$released" 'has("claim_expires_at")')"

echo "== step 6: lease round-trips into a fresh store and an existing one"
trip_id=$(new_issue a "round-trip target")
bd_at a export -o "$SCRATCH/pre.jsonl" >/dev/null 2>&1
check "pre-claim export exit" "0" "$?"
bd_at a claim "$trip_id" --assignee agent-E --lease 45m >/dev/null 2>&1
check "agent-E claim exit" "0" "$?"
bd_at a export -o "$SCRATCH/post.jsonl" >/dev/null 2>&1
check "post-claim export exit" "0" "$?"

source_lease=$(field "$(show_json a "$trip_id")" '.claim_expires_at')
expected_lease=$(epoch_nanos "$source_lease")
if [ "$expected_lease" = "none" ]; then
	# Without a lease in the source store the two comparisons below would both read
	# "none" and pass vacuously, so this is a failure in its own right and the
	# expectation becomes a value no store can produce.
	fail "source store has no claim_expires_at to round-trip"
	expected_lease="<a lease the source store never had>"
fi

init_store fresh e2e || fail "could not init the fresh store"
bd_at fresh import -i "$SCRATCH/post.jsonl" >/dev/null 2>&1
check "fresh-store import exit" "0" "$?"
fresh_lease=$(field "$(show_json fresh "$trip_id")" '.claim_expires_at')

# The existing store already holds the issue in its pre-claim state, so the import
# takes the update path rather than the insert path.
init_store existing e2e || fail "could not init the existing store"
bd_at existing import -i "$SCRATCH/pre.jsonl" >/dev/null 2>&1
check "pre-claim import exit" "0" "$?"
pre_lease=$(field "$(show_json existing "$trip_id")" 'has("claim_expires_at")')
check "existing store starts pre-claim" "false" "$pre_lease"
bd_at existing import -i "$SCRATCH/post.jsonl" >/dev/null 2>&1
check "existing-store import exit" "0" "$?"
existing_lease=$(field "$(show_json existing "$trip_id")" '.claim_expires_at')

check "fresh store lease" "$expected_lease" "$(epoch_nanos "$fresh_lease")"
check "existing store lease" "$expected_lease" "$(epoch_nanos "$existing_lease")"

if [ "$failures" -ne 0 ]; then
	printf '%d check(s) failed\n' "$failures"
	exit 1
fi
echo "E2E_PASS"
