package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// claimEventValue is the audit payload recorded for a winning claim.
// PreviousHolder is what makes a steal reconstructable after the fact.
type claimEventValue struct {
	Outcome        types.ClaimResult `json:"outcome"`
	Assignee       string            `json:"assignee"`
	Status         types.Status      `json:"status"`
	PreviousHolder string            `json:"previous_holder,omitempty"`
	ClaimExpiresAt *time.Time        `json:"claim_expires_at,omitempty"`
}

// ClaimIssue atomically takes ownership of an issue. See storage.Storage.ClaimIssue
// for the decision ladder.
//
// The read, the decision and the write all happen inside one BEGIN IMMEDIATE
// transaction, which SQLite serializes against every other write transaction. That
// is the whole point of the method: a pre-read outside the transaction, as the
// generic UpdateIssue path does, lets two concurrent claimants both succeed.
func (s *SQLiteStorage) ClaimIssue(ctx context.Context, id, assignee string, lease *time.Duration, actor string) (*types.ClaimOutcome, error) {
	var outcome *types.ClaimOutcome
	err := s.RunInTransaction(ctx, func(tx storage.Transaction) error {
		var claimErr error
		outcome, claimErr = tx.ClaimIssue(ctx, id, assignee, lease, actor)
		return claimErr
	})
	if err != nil {
		return nil, err
	}
	return outcome, nil
}

// claimIssue decides and applies a claim on the transaction's connection.
// It is the single authority for claim semantics; both the direct and the
// transactional entry points route through it.
func claimIssue(ctx context.Context, t *sqliteTxStorage, id, assignee string, lease *time.Duration, actor string) (*types.ClaimOutcome, error) {
	// Validate the caller's value before reading the row. This is about the argument,
	// not the stored assignee: an empty value would otherwise match the legacy
	// "in_progress with no owner" rung and let two claimants both win.
	if strings.TrimSpace(assignee) == "" {
		return nil, fmt.Errorf("claim requires a non-empty assignee")
	}

	issue, err := t.GetIssue(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to read issue for claim: %w", err)
	}
	if issue == nil {
		return nil, fmt.Errorf("issue %s not found", id)
	}

	now := time.Now()
	holder, holderExpiry := issue.Assignee, issue.ClaimExpiresAt

	var result types.ClaimResult
	switch {
	case issue.Status == types.StatusClosed || issue.IsTombstone():
		return nil, fmt.Errorf("issue %s is %s and cannot be claimed", id, issue.Status)

	case issue.Status == types.StatusOpen:
		result = types.ClaimClaimed

	case issue.Status != types.StatusInProgress:
		// Blocked, deferred, pinned and custom statuses: nothing to write, but the
		// denial is permanent rather than contention, so callers must not retry it.
		return &types.ClaimOutcome{
			Outcome:      types.ClaimDenied,
			DenyReason:   types.DenyStatus,
			Holder:       holder,
			HolderExpiry: holderExpiry,
			Issue:        issue,
		}, nil

	// From here down the issue is in_progress.
	case issue.Assignee == "":
		// Legacy row from `bd update --status in_progress`: no owner to protect.
		result = types.ClaimClaimed

	case issue.Assignee == assignee:
		// Self-match precedes the expiry check so an expired holder renews its own
		// lease instead of "stealing" from itself.
		result = types.ClaimRenewed

	case issue.ClaimExpired(now):
		result = types.ClaimStolen

	default:
		return &types.ClaimOutcome{
			Outcome:      types.ClaimDenied,
			DenyReason:   types.DenyHeld,
			Holder:       holder,
			HolderExpiry: holderExpiry,
			Issue:        issue,
		}, nil
	}

	claimed, err := writeClaim(ctx, t, issue, assignee, lease, actor, result, now)
	if err != nil {
		return nil, err
	}

	return &types.ClaimOutcome{
		Outcome:      result,
		Holder:       holder,
		HolderExpiry: holderExpiry,
		Issue:        claimed,
	}, nil
}

// writeClaim applies a winning claim and every secondary effect that keeps the
// rest of the system honest: the content hash (which covers assignee and status),
// the audit event, the dirty mark that drives incremental export, and the blocked
// cache that feeds `bd ready`.
func writeClaim(ctx context.Context, t *sqliteTxStorage, issue *types.Issue, assignee string, lease *time.Duration, actor string, result types.ClaimResult, now time.Time) (*types.Issue, error) {
	previousStatus := issue.Status

	claimed := *issue
	claimed.Assignee = assignee
	claimed.Status = types.StatusInProgress
	claimed.UpdatedAt = now
	claimed.ClaimExpiresAt = nil
	if lease != nil {
		expiry := now.Add(*lease)
		claimed.ClaimExpiresAt = &expiry
	}
	claimed.ContentHash = claimed.ComputeContentHash()

	_, err := t.conn.ExecContext(ctx, `
		UPDATE issues
		SET assignee = ?, status = ?, claim_expires_at = ?, updated_at = ?, content_hash = ?
		WHERE id = ?
	`, claimed.Assignee, claimed.Status, claimed.ClaimExpiresAt, claimed.UpdatedAt, claimed.ContentHash, claimed.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to write claim: %w", err)
	}

	if err := recordClaimEvent(ctx, t, issue, &claimed, actor, result, previousStatus); err != nil {
		return nil, err
	}

	if err := markDirty(ctx, t.conn, claimed.ID); err != nil {
		return nil, fmt.Errorf("failed to mark issue dirty: %w", err)
	}

	if previousStatus != claimed.Status {
		if err := t.parent.invalidateBlockedCache(ctx, t.conn); err != nil {
			return nil, fmt.Errorf("failed to invalidate blocked cache: %w", err)
		}
	}

	return &claimed, nil
}

// recordClaimEvent writes the audit row for a winning claim.
func recordClaimEvent(ctx context.Context, t *sqliteTxStorage, before, after *types.Issue, actor string, result types.ClaimResult, previousStatus types.Status) error {
	newValue := claimEventValue{
		Outcome:        result,
		Assignee:       after.Assignee,
		Status:         after.Status,
		ClaimExpiresAt: after.ClaimExpiresAt,
	}
	if result == types.ClaimStolen {
		newValue.PreviousHolder = before.Assignee
	}

	oldData, err := json.Marshal(before)
	if err != nil {
		oldData = []byte(fmt.Sprintf(`{"id":%q}`, before.ID))
	}
	newData, err := json.Marshal(newValue)
	if err != nil {
		newData = []byte(`{}`)
	}

	eventType := types.EventUpdated
	if previousStatus != after.Status {
		eventType = types.EventStatusChanged
	}

	_, err = t.conn.ExecContext(ctx, `
		INSERT INTO events (issue_id, event_type, actor, old_value, new_value)
		VALUES (?, ?, ?, ?, ?)
	`, after.ID, eventType, actor, string(oldData), string(newData))
	if err != nil {
		return fmt.Errorf("failed to record claim event: %w", err)
	}
	return nil
}
