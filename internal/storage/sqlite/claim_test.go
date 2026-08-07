package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// These tests lock the claim decision ladder from work/w3_atomic-claim/plan_v4.md.
// They assert the specified semantics, not the current implementation's behaviour.

const (
	holderA = "agent-a"
	holderB = "agent-b"
)

func leaseOf(d time.Duration) *time.Duration { return &d }

// newClaimIssue inserts an issue in the given starting state and returns it.
func newClaimIssue(t *testing.T, store *SQLiteStorage, status types.Status, assignee string) *types.Issue {
	t.Helper()
	issue := &types.Issue{
		Title:     "claim fixture",
		Status:    status,
		Assignee:  assignee,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	if err := store.CreateIssue(context.Background(), issue, "test"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	return issue
}

// mustGet reloads an issue, failing the test if it has vanished.
func mustGet(t *testing.T, store *SQLiteStorage, id string) *types.Issue {
	t.Helper()
	issue, err := store.GetIssue(context.Background(), id)
	if err != nil {
		t.Fatalf("GetIssue(%s) failed: %v", id, err)
	}
	if issue == nil {
		t.Fatalf("issue %s not found", id)
	}
	return issue
}

// mustClaim claims and fails the test on error, for the setup half of a case.
func mustClaim(t *testing.T, store *SQLiteStorage, id, assignee string, lease *time.Duration) *types.ClaimOutcome {
	t.Helper()
	outcome, err := store.ClaimIssue(context.Background(), id, assignee, lease, assignee)
	if err != nil {
		t.Fatalf("ClaimIssue(%s, %s) failed: %v", id, assignee, err)
	}
	return outcome
}

// latestClaimEvent decodes the new_value payload of the most recent event row.
func latestClaimEvent(t *testing.T, store *SQLiteStorage, id string) map[string]any {
	t.Helper()
	var raw string
	err := store.db.QueryRowContext(context.Background(),
		`SELECT new_value FROM events WHERE issue_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&raw)
	if err != nil {
		t.Fatalf("failed to read latest event for %s: %v", id, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("event new_value is not JSON (%q): %v", raw, err)
	}
	return payload
}

// Ladder rung 1: an open issue is claimable and the claim takes effect.
func TestClaimOpenIssueClaimed(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	const lease = 30 * time.Minute
	issue := newClaimIssue(t, store, types.StatusOpen, "")
	before := time.Now()
	outcome := mustClaim(t, store, issue.ID, holderA, leaseOf(lease))
	after := time.Now()

	if outcome.Outcome != types.ClaimClaimed {
		t.Errorf("outcome = %q, want %q", outcome.Outcome, types.ClaimClaimed)
	}
	if outcome.DenyReason != "" {
		t.Errorf("DenyReason = %q, want empty on a win", outcome.DenyReason)
	}
	if outcome.Holder != "" {
		t.Errorf("Holder = %q, want empty: an open issue had no prior holder", outcome.Holder)
	}

	stored := mustGet(t, store, issue.ID)
	if stored.Assignee != holderA {
		t.Errorf("stored assignee = %q, want %q", stored.Assignee, holderA)
	}
	if stored.Status != types.StatusInProgress {
		t.Errorf("stored status = %q, want %q", stored.Status, types.StatusInProgress)
	}
	if stored.ClaimExpiresAt == nil {
		t.Fatal("claim_expires_at is NULL, want the lease expiry")
	}
	// The expiry must be the REQUESTED lease from the moment of the claim, not some
	// multiple of it: an over-long lease keeps a dead builder's bead unstealable.
	// The claim happened between before and after, so the expiry must lie in the
	// same window shifted by exactly one lease.
	earliest, latest := before.Add(lease), after.Add(lease)
	if stored.ClaimExpiresAt.Before(earliest) || stored.ClaimExpiresAt.After(latest) {
		t.Errorf("claim_expires_at = %v, want within [%v, %v] (now + the requested %v)",
			stored.ClaimExpiresAt, earliest, latest, lease)
	}
}

// Ladder rung: in_progress, held by someone else, lease still live -> denied,
// deny_reason=held, and the holder is reported so a caller can name it.
func TestClaimHeldUnexpiredDenied(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusOpen, "")
	mustClaim(t, store, issue.ID, holderA, leaseOf(time.Hour))

	outcome, err := store.ClaimIssue(context.Background(), issue.ID, holderB, leaseOf(time.Hour), holderB)
	if err != nil {
		t.Fatalf("a denial must not be an error: %v", err)
	}
	if outcome.Outcome != types.ClaimDenied {
		t.Errorf("outcome = %q, want %q", outcome.Outcome, types.ClaimDenied)
	}
	if outcome.DenyReason != types.DenyHeld {
		t.Errorf("DenyReason = %q, want %q (contention is retryable)", outcome.DenyReason, types.DenyHeld)
	}
	if outcome.Holder != holderA {
		t.Errorf("Holder = %q, want %q", outcome.Holder, holderA)
	}
	if outcome.HolderExpiry == nil {
		t.Error("HolderExpiry is nil, want the live holder's lease expiry")
	}

	stored := mustGet(t, store, issue.ID)
	if stored.Assignee != holderA {
		t.Errorf("stored assignee = %q, want the denying holder %q", stored.Assignee, holderA)
	}
}

// Ladder rung: the current holder re-claiming renews and refreshes the lease.
func TestClaimSelfRenews(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusOpen, "")
	mustClaim(t, store, issue.ID, holderA, leaseOf(time.Minute))
	firstExpiry := mustGet(t, store, issue.ID).ClaimExpiresAt

	outcome := mustClaim(t, store, issue.ID, holderA, leaseOf(time.Hour))
	if outcome.Outcome != types.ClaimRenewed {
		t.Errorf("outcome = %q, want %q", outcome.Outcome, types.ClaimRenewed)
	}

	stored := mustGet(t, store, issue.ID)
	if stored.ClaimExpiresAt == nil {
		t.Fatal("claim_expires_at is NULL after renewal, want the refreshed expiry")
	}
	if !stored.ClaimExpiresAt.After(*firstExpiry) {
		t.Errorf("renewed expiry %v did not extend the original %v", stored.ClaimExpiresAt, firstExpiry)
	}
}

// The self-match rung sits above the expiry rung: an expired holder renews its
// own claim rather than stealing from itself.
func TestClaimSelfRenewsExpiredLease(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusOpen, "")
	mustClaim(t, store, issue.ID, holderA, leaseOf(-time.Second))

	outcome := mustClaim(t, store, issue.ID, holderA, leaseOf(time.Hour))
	if outcome.Outcome != types.ClaimRenewed {
		t.Errorf("outcome = %q, want %q: self-match precedes the expiry check", outcome.Outcome, types.ClaimRenewed)
	}
}

// Ladder rung: in_progress with an expired lease -> stolen, and the event names
// the prior holder so the steal is reconstructable after the fact.
func TestClaimExpiredLeaseStolen(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusOpen, "")
	mustClaim(t, store, issue.ID, holderA, leaseOf(-time.Second))

	outcome := mustClaim(t, store, issue.ID, holderB, leaseOf(time.Hour))
	if outcome.Outcome != types.ClaimStolen {
		t.Fatalf("outcome = %q, want %q", outcome.Outcome, types.ClaimStolen)
	}
	if outcome.Holder != holderA {
		t.Errorf("Holder = %q, want the stolen-from holder %q", outcome.Holder, holderA)
	}

	if stored := mustGet(t, store, issue.ID); stored.Assignee != holderB {
		t.Errorf("stored assignee = %q, want the thief %q", stored.Assignee, holderB)
	}

	event := latestClaimEvent(t, store, issue.ID)
	if got := event["previous_holder"]; got != holderA {
		t.Errorf("event previous_holder = %v, want %q", got, holderA)
	}
	if got := event["outcome"]; got != string(types.ClaimStolen) {
		t.Errorf("event outcome = %v, want %q", got, types.ClaimStolen)
	}
}

// Ladder rung: a legacy `update --status in_progress` row has no owner to
// protect, so it is claimable rather than permanently denied.
func TestClaimInProgressEmptyAssigneeClaimed(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusInProgress, "")
	outcome := mustClaim(t, store, issue.ID, holderA, leaseOf(time.Hour))

	if outcome.Outcome != types.ClaimClaimed {
		t.Errorf("outcome = %q, want %q for an unowned in_progress row", outcome.Outcome, types.ClaimClaimed)
	}
	if stored := mustGet(t, store, issue.ID); stored.Assignee != holderA {
		t.Errorf("stored assignee = %q, want %q", stored.Assignee, holderA)
	}
}

// Ladder rung: a non-claimable status denies with deny_reason=status, and the
// outcome carries the status so the caller can name it. This must not be an
// error: exit 3 (skip), not exit 1.
func TestClaimBlockedStatusDenied(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusBlocked, "")
	outcome, err := store.ClaimIssue(context.Background(), issue.ID, holderA, leaseOf(time.Hour), holderA)
	if err != nil {
		t.Fatalf("a status denial must not be an error: %v", err)
	}
	if outcome.Outcome != types.ClaimDenied {
		t.Errorf("outcome = %q, want %q", outcome.Outcome, types.ClaimDenied)
	}
	if outcome.DenyReason != types.DenyStatus {
		t.Errorf("DenyReason = %q, want %q (a blocked issue must be skipped, not retried)",
			outcome.DenyReason, types.DenyStatus)
	}
	if outcome.Issue == nil || outcome.Issue.Status != types.StatusBlocked {
		t.Errorf("outcome does not carry the blocking status, so no caller can name it: %+v", outcome.Issue)
	}
	if stored := mustGet(t, store, issue.ID); stored.Status != types.StatusBlocked {
		t.Errorf("stored status = %q, want %q untouched", stored.Status, types.StatusBlocked)
	}
}

// An empty or whitespace-only assignee is rejected before the row is read: it
// would otherwise match the legacy unowned rung and let two claimants both win.
func TestClaimEmptyAssigneeRejected(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusOpen, "")

	for _, assignee := range []string{"", "   ", "\t\n"} {
		outcome, err := store.ClaimIssue(context.Background(), issue.ID, assignee, leaseOf(time.Hour), "test")
		if err == nil {
			t.Errorf("ClaimIssue(assignee=%q) succeeded with outcome %+v, want an error", assignee, outcome)
		}
		if stored := mustGet(t, store, issue.ID); stored.Status != types.StatusOpen || stored.Assignee != "" {
			t.Fatalf("rejected claim wrote to the issue: status=%q assignee=%q", stored.Status, stored.Assignee)
		}
	}
}

// Ladder rung: closed and tombstoned issues are errors, not denials.
func TestClaimClosedIssueErrors(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	issue := newClaimIssue(t, store, types.StatusOpen, "")
	if err := store.CloseIssue(ctx, issue.ID, "done", "test"); err != nil {
		t.Fatalf("CloseIssue failed: %v", err)
	}

	outcome, err := store.ClaimIssue(ctx, issue.ID, holderA, leaseOf(time.Hour), holderA)
	if err == nil {
		t.Fatalf("claiming a closed issue succeeded with outcome %+v, want an error", outcome)
	}
	if stored := mustGet(t, store, issue.ID); stored.Status != types.StatusClosed {
		t.Errorf("stored status = %q, want %q untouched", stored.Status, types.StatusClosed)
	}
}

// A soft-deleted issue is an error too. Without this the tombstone rung is dead
// code and a deleted issue could be claimed back to life as in_progress.
func TestClaimTombstonedIssueErrors(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	issue := newClaimIssue(t, store, types.StatusOpen, "")
	if err := store.CreateTombstone(ctx, issue.ID, "test", "deleted"); err != nil {
		t.Fatalf("CreateTombstone failed: %v", err)
	}

	outcome, err := store.ClaimIssue(ctx, issue.ID, holderA, leaseOf(time.Hour), holderA)
	if err == nil {
		t.Fatalf("claiming a tombstoned issue succeeded with outcome %+v, want an error", outcome)
	}

	stored := mustGet(t, store, issue.ID)
	if stored.Status != types.StatusTombstone {
		t.Errorf("stored status = %q, want %q untouched", stored.Status, types.StatusTombstone)
	}
	if stored.Assignee != "" {
		t.Errorf("stored assignee = %q, want empty: the rejected claim wrote to the row", stored.Assignee)
	}
	if stored.ClaimExpiresAt != nil {
		t.Errorf("claim_expires_at = %v, want NULL: the rejected claim wrote a lease", stored.ClaimExpiresAt)
	}
}

// An unknown ID is an error (exit 1), not a denial: there is nothing to retry
// or skip, the caller named an issue that does not exist.
func TestClaimUnknownIssueErrors(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	outcome, err := store.ClaimIssue(context.Background(), "bd-nope", holderA, leaseOf(time.Hour), holderA)
	if err == nil {
		t.Fatalf("claiming a nonexistent issue succeeded with outcome %+v, want an error", outcome)
	}
}

// A claim without a lease never expires: claim_expires_at stays NULL and the
// row is not stealable however long it is held.
func TestClaimWithoutLeaseNeverExpires(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	issue := newClaimIssue(t, store, types.StatusOpen, "")
	mustClaim(t, store, issue.ID, holderA, nil)

	stored := mustGet(t, store, issue.ID)
	if stored.ClaimExpiresAt != nil {
		t.Fatalf("claim_expires_at = %v, want NULL for a lease-less claim", stored.ClaimExpiresAt)
	}
	if stored.ClaimExpired(time.Now().Add(100 * 365 * 24 * time.Hour)) {
		t.Error("a lease-less claim reports as expired")
	}

	outcome, err := store.ClaimIssue(context.Background(), issue.ID, holderB, leaseOf(time.Hour), holderB)
	if err != nil {
		t.Fatalf("rival claim errored: %v", err)
	}
	if outcome.Outcome != types.ClaimDenied || outcome.DenyReason != types.DenyHeld {
		t.Errorf("rival got outcome=%q reason=%q, want denied/held: a lease-less claim is never stealable",
			outcome.Outcome, outcome.DenyReason)
	}
}

// TestClaimDeniedNoWrite proves a denial is a pure read: no updated_at bump, no
// event row, no dirty mark. Any of those would churn export and audit for a
// claimant that got nothing.
func TestClaimDeniedNoWrite(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	issue := newClaimIssue(t, store, types.StatusOpen, "")
	mustClaim(t, store, issue.ID, holderA, leaseOf(time.Hour))

	type snapshot struct {
		updatedAt  time.Time
		eventCount int
		dirtyMark  string
	}
	take := func() snapshot {
		t.Helper()
		var s snapshot
		s.updatedAt = mustGet(t, store, issue.ID).UpdatedAt
		if err := store.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE issue_id = ?`, issue.ID).Scan(&s.eventCount); err != nil {
			t.Fatalf("failed to count events: %v", err)
		}
		if err := store.db.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(marked_at), '') FROM dirty_issues WHERE issue_id = ?`, issue.ID).Scan(&s.dirtyMark); err != nil {
			t.Fatalf("failed to read dirty mark: %v", err)
		}
		return s
	}

	before := take()
	// Ensure a write would be observable in updated_at and marked_at.
	time.Sleep(10 * time.Millisecond)

	outcome, err := store.ClaimIssue(ctx, issue.ID, holderB, leaseOf(time.Hour), holderB)
	if err != nil {
		t.Fatalf("denial errored: %v", err)
	}
	if outcome.Outcome != types.ClaimDenied {
		t.Fatalf("outcome = %q, want %q", outcome.Outcome, types.ClaimDenied)
	}

	after := take()
	if !after.updatedAt.Equal(before.updatedAt) {
		t.Errorf("updated_at moved %v -> %v on a denial", before.updatedAt, after.updatedAt)
	}
	if after.eventCount != before.eventCount {
		t.Errorf("events grew %d -> %d on a denial", before.eventCount, after.eventCount)
	}
	if after.dirtyMark != before.dirtyMark {
		t.Errorf("dirty mark changed %q -> %q on a denial", before.dirtyMark, after.dirtyMark)
	}
}

// warmPool opens n concurrent reads so the connection pool is populated before
// a race starts. Without it the first claimant pays the connection-open cost
// and can finish before the others have begun, which hides non-atomicity.
func warmPool(t *testing.T, store *SQLiteStorage, id string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.GetIssue(context.Background(), id)
		}()
	}
	wg.Wait()
}

// TestClaimRace is the compare-and-swap proof: N claimants, one open issue,
// exactly one winner. A read-then-unconditional-write implementation produces
// several winners here and fails.
func TestClaimRace(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	const n = 8
	ctx := context.Background()
	issue := newClaimIssue(t, store, types.StatusOpen, "")

	type attempt struct {
		assignee string
		outcome  *types.ClaimOutcome
		err      error
	}
	results := make([]attempt, n)

	warmPool(t, store, issue.ID, n)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			assignee := "racer-" + string(rune('a'+i))
			start.Wait()
			outcome, err := store.ClaimIssue(ctx, issue.ID, assignee, leaseOf(time.Hour), assignee)
			results[i] = attempt{assignee: assignee, outcome: outcome, err: err}
		}(i)
	}
	start.Done()
	done.Wait()

	var winner string
	claimed, denied := 0, 0
	for _, r := range results {
		if r.err != nil {
			t.Fatalf("claimant %s errored: %v", r.assignee, r.err)
		}
		switch r.outcome.Outcome {
		case types.ClaimClaimed:
			claimed++
			winner = r.assignee
		case types.ClaimDenied:
			denied++
			if r.outcome.DenyReason != types.DenyHeld {
				t.Errorf("claimant %s denied with reason %q, want %q", r.assignee, r.outcome.DenyReason, types.DenyHeld)
			}
		default:
			t.Errorf("claimant %s got outcome %q; only claimed or denied are possible from an open issue",
				r.assignee, r.outcome.Outcome)
		}
	}

	if claimed != 1 {
		t.Fatalf("%d claimants won the race, want exactly 1", claimed)
	}
	if denied != n-1 {
		t.Fatalf("%d claimants were denied, want %d", denied, n-1)
	}

	stored := mustGet(t, store, issue.ID)
	if stored.Assignee != winner {
		t.Errorf("stored assignee = %q, want the single winner %q", stored.Assignee, winner)
	}
	if stored.Status != types.StatusInProgress {
		t.Errorf("stored status = %q, want %q", stored.Status, types.StatusInProgress)
	}
}

// TestClaimMigrationExistingStore opens a store whose issues table predates
// migration 027, and asserts the column is added and the existing rows survive.
// The pre-027 schema is built here rather than committed as a fixture database.
func TestClaimMigrationExistingStore(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pre027.db")

	store, err := New(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("failed to set issue_prefix: %v", err)
	}
	open := newClaimIssue(t, store, types.StatusOpen, "")
	held := newClaimIssue(t, store, types.StatusInProgress, holderA)

	// Roll the schema back to its pre-027 shape: drop the column and forget that
	// 027 ever ran, which is what a genuine pre-027 store looks like. A migration
	// runs once per database and is then recorded in the ledger (bd-ok4pr.1.8),
	// so removing the record is what lets the reopen replay 027.
	if _, err := store.db.ExecContext(ctx, `ALTER TABLE issues DROP COLUMN claim_expires_at`); err != nil {
		t.Fatalf("failed to build the pre-027 schema: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE name = 'claim_expires_at_column'`); err != nil {
		t.Fatalf("failed to build the pre-027 ledger: %v", err)
	}
	if hasClaimExpiresAtColumn(t, store) {
		t.Fatal("claim_expires_at still present; the pre-027 fixture was not built")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close pre-027 store: %v", err)
	}

	migrated, err := New(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to reopen pre-027 store: %v", err)
	}
	defer func() { _ = migrated.Close() }()

	if !hasClaimExpiresAtColumn(t, migrated) {
		t.Fatal("claim_expires_at missing after migration 027")
	}

	for _, want := range []*types.Issue{open, held} {
		got := mustGet(t, migrated, want.ID)
		if got.Title != want.Title || got.Status != want.Status || got.Assignee != want.Assignee {
			t.Errorf("row %s changed across migration: got status=%q assignee=%q title=%q, want status=%q assignee=%q title=%q",
				want.ID, got.Status, got.Assignee, got.Title, want.Status, want.Assignee, want.Title)
		}
		if got.ClaimExpiresAt != nil {
			t.Errorf("row %s has claim_expires_at = %v, want NULL for a pre-lease row", want.ID, got.ClaimExpiresAt)
		}
	}

	// The migrated store is usable: the lease column takes writes.
	outcome := mustClaim(t, migrated, open.ID, holderB, leaseOf(time.Hour))
	if outcome.Outcome != types.ClaimClaimed {
		t.Fatalf("outcome = %q, want %q on the migrated store", outcome.Outcome, types.ClaimClaimed)
	}
	if mustGet(t, migrated, open.ID).ClaimExpiresAt == nil {
		t.Error("claim_expires_at is NULL after claiming on the migrated store")
	}
}

// hasClaimExpiresAtColumn reports the column's presence via PRAGMA table_info.
func hasClaimExpiresAtColumn(t *testing.T, store *SQLiteStorage) bool {
	t.Helper()
	var present bool
	err := store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) > 0 FROM pragma_table_info('issues') WHERE name = 'claim_expires_at'`).Scan(&present)
	if err != nil {
		t.Fatalf("PRAGMA table_info(issues) failed: %v", err)
	}
	return present
}
