package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// queryExecer is the read/write surface child-floor maintenance needs.
// Both *sql.Conn (single-issue and batch paths) and *sql.Tx (hydration)
// satisfy it, so the floor rules live in one place.
type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// parseChildSuffix reports the numeric child number taken by id under parentID.
//
// A suffix counts only if it is all digits with no further dot and parses as a
// non-negative int64. Anything wider than int64 (e.g. p.9223372036854775808)
// is not a number this allocator could produce, so the scan skips it and the
// insert raises no floor for it; the strict insert stays the backstop.
func parseChildSuffix(parentID, id string) (int64, bool) {
	prefix := parentID + "."
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	suffix := id[len(prefix):]
	if suffix == "" || strings.Contains(suffix, ".") {
		return 0, false
	}
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// escapeLikeLiteral escapes LIKE metacharacters so a parent ID matches literally.
func escapeLikeLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// allocateChildNumber returns the next free child number for parentID without
// writing anything: next = max(floor, highest existing child) + 1.
//
// The floor (child_counters) is a monotonic record of numbers ever issued;
// the issues table is the authority on numbers currently taken, so the scan
// repairs a floor that lags behind (stale counters, fresh imports). Closed
// issues and tombstones count: their numbers stay taken. Allocation refuses
// rather than overflows when the number space is exhausted.
func allocateChildNumber(ctx context.Context, q queryExecer, parentID string) (int, error) {
	var floor int64
	err := q.QueryRowContext(ctx, `SELECT last_child FROM child_counters WHERE parent_id = ?`, parentID).Scan(&floor)
	if err == sql.ErrNoRows {
		floor = 0
	} else if err != nil {
		return 0, fmt.Errorf("failed to read child floor for parent %s: %w", parentID, err)
	}

	pattern := escapeLikeLiteral(parentID) + ".%"
	rows, err := q.QueryContext(ctx, `SELECT id FROM issues WHERE id LIKE ? ESCAPE '\'`, pattern)
	if err != nil {
		return 0, fmt.Errorf("failed to scan children of parent %s: %w", parentID, err)
	}
	defer func() { _ = rows.Close() }()

	var maxExisting int64
	found := false
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("failed to scan child id: %w", err)
		}
		n, ok := parseChildSuffix(parentID, id)
		if !ok {
			continue
		}
		if !found || n > maxExisting {
			maxExisting, found = n, true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("failed to iterate children of parent %s: %w", parentID, err)
	}

	ceiling := floor
	if found && maxExisting > ceiling {
		ceiling = maxExisting
	}
	if ceiling == math.MaxInt64 {
		return 0, fmt.Errorf("child number space exhausted for parent %s", parentID)
	}
	return int(ceiling + 1), nil
}

// raiseChildFloor records that child number n under parentID is taken. It is
// the only writer of child_counters, and it only ever moves the floor up.
//
// The WHERE EXISTS guard skips a parent with no issue row: orphan children
// are legal on import and child_counters.parent_id carries a foreign key, so
// an unguarded write would abort the whole batch. max() keeps the floor
// monotonic: a lower number leaves it untouched.
func raiseChildFloor(ctx context.Context, q queryExecer, parentID string, n int64) error {
	if n < 0 {
		return nil
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO child_counters (parent_id, last_child)
		SELECT ?, ? WHERE EXISTS (SELECT 1 FROM issues WHERE id = ?)
		ON CONFLICT(parent_id) DO UPDATE SET last_child = max(last_child, excluded.last_child)
	`, parentID, n, parentID)
	if err != nil {
		return fmt.Errorf("failed to raise child floor for parent %s: %w", parentID, err)
	}
	return nil
}

// seedChildFloor raises id's own floor from children already present. This is
// the orphan-first ordering: a child written before its parent gets no floor
// (the guarded statement skips an absent parent), and if that child's row
// later disappears nothing else remembers its number.
func seedChildFloor(ctx context.Context, q queryExecer, id string) error {
	pattern := escapeLikeLiteral(id) + ".%"
	rows, err := q.QueryContext(ctx, `SELECT id FROM issues WHERE id LIKE ? ESCAPE '\'`, pattern)
	if err != nil {
		return fmt.Errorf("failed to scan children of %s: %w", id, err)
	}
	defer func() { _ = rows.Close() }()

	var maxN int64
	found := false
	for rows.Next() {
		var childID string
		if err := rows.Scan(&childID); err != nil {
			return fmt.Errorf("failed to scan child id: %w", err)
		}
		n, ok := parseChildSuffix(id, childID)
		if !ok {
			continue
		}
		if !found || n > maxN {
			maxN, found = n, true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate children of %s: %w", id, err)
	}
	if !found {
		return nil
	}
	return raiseChildFloor(ctx, q, id, maxN)
}

// maintainFloorsForIDs raises floors for a batch of freshly written issue IDs:
// each parent to the maximum child suffix in the batch, and each written ID
// to the maximum child already present (orphan-first seeding). The seed is one
// query per batch, not one per row: allocation after an import can pass
// entirely through the scan, so without this the batch paths would leave no
// floor at all.
func maintainFloorsForIDs(ctx context.Context, q queryExecer, ids []string) error {
	byParent := make(map[string]int64)
	written := make(map[string]bool, len(ids))
	for _, id := range ids {
		written[id] = true
		if isHierarchical, parent := IsHierarchicalID(id); isHierarchical {
			if n, ok := parseChildSuffix(parent, id); ok && n > byParent[parent] {
				byParent[parent] = n
			}
		}
	}
	for parent, n := range byParent {
		if err := raiseChildFloor(ctx, q, parent, n); err != nil {
			return err
		}
	}
	if len(written) == 0 {
		return nil
	}

	rows, err := q.QueryContext(ctx, `SELECT id FROM issues`)
	if err != nil {
		return fmt.Errorf("failed to scan issues for floor seeding: %w", err)
	}
	defer func() { _ = rows.Close() }()

	seedMax := make(map[string]int64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("failed to scan issue id: %w", err)
		}
		isHierarchical, parent := IsHierarchicalID(id)
		if !isHierarchical || !written[parent] {
			continue
		}
		n, ok := parseChildSuffix(parent, id)
		if !ok {
			continue
		}
		if n > seedMax[parent] {
			seedMax[parent] = n
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate issues for floor seeding: %w", err)
	}
	for parent, n := range seedMax {
		if err := raiseChildFloor(ctx, q, parent, n); err != nil {
			return err
		}
	}
	return nil
}

// CreateChildIssue creates issue as a child of parentID in one transaction:
// allocation, insert, floor raise, created event, dirty mark and the
// parent-child edge commit together or not at all.
//
// With an empty issue ID the next free number is allocated above the highest
// existing child. With an explicit ID it must already name a child of
// parentID. A collision on insert fails loudly instead of reporting success
// without writing a row.
func (s *SQLiteStorage) CreateChildIssue(ctx context.Context, parentID string, issue *types.Issue, actor string) error {
	return s.RunInTransaction(ctx, func(tx storage.Transaction) error {
		txs, ok := tx.(*sqliteTxStorage)
		if !ok {
			return fmt.Errorf("CreateChildIssue requires a SQLite transaction")
		}
		return s.createChildOnConn(ctx, txs.conn, txs, parentID, issue, actor)
	})
}

// createChildOnConn does the CreateChildIssue work on a transaction-owned
// connection. The parent is resurrected through tryResurrectParentWithConn,
// never the exported TryResurrectParent, which would take a fresh pooled
// connection and deadlock against the write lock this transaction holds.
func (s *SQLiteStorage) createChildOnConn(ctx context.Context, conn *sql.Conn, tx *sqliteTxStorage, parentID string, issue *types.Issue, actor string) error {
	// Mirror CreateIssue's validation so the child path accepts the same rows.
	customStatuses, err := tx.GetCustomStatuses(ctx)
	if err != nil {
		return fmt.Errorf("failed to get custom statuses: %w", err)
	}

	now := time.Now()
	if issue.CreatedAt.IsZero() {
		issue.CreatedAt = now
	}
	if issue.UpdatedAt.IsZero() {
		issue.UpdatedAt = now
	}

	// Defensive fix for closed_at invariant (GH#523).
	if issue.Status == types.StatusClosed && issue.ClosedAt == nil {
		maxTime := issue.CreatedAt
		if issue.UpdatedAt.After(maxTime) {
			maxTime = issue.UpdatedAt
		}
		closedAt := maxTime.Add(time.Second)
		issue.ClosedAt = &closedAt
	}

	// Defensive fix for deleted_at invariant: tombstones must have deleted_at.
	if issue.Status == types.StatusTombstone && issue.DeletedAt == nil {
		maxTime := issue.CreatedAt
		if issue.UpdatedAt.After(maxTime) {
			maxTime = issue.UpdatedAt
		}
		deletedAt := maxTime.Add(time.Second)
		issue.DeletedAt = &deletedAt
	}

	if err := issue.ValidateWithCustomStatuses(customStatuses); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	if issue.ContentHash == "" {
		issue.ContentHash = issue.ComputeContentHash()
	}

	var prefix string
	err = conn.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, "issue_prefix").Scan(&prefix)
	if err == sql.ErrNoRows || prefix == "" {
		return fmt.Errorf("database not initialized: issue_prefix config is missing (run 'bd init --prefix <prefix>' first)")
	} else if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE id = ?`, parentID).Scan(&count); err != nil {
		return fmt.Errorf("failed to check parent existence: %w", err)
	}
	if count == 0 {
		resurrected, err := s.tryResurrectParentWithConn(ctx, conn, parentID)
		if err != nil {
			return fmt.Errorf("failed to resurrect parent %s: %w", parentID, err)
		}
		if !resurrected {
			return fmt.Errorf("parent issue %s does not exist and could not be resurrected from JSONL history", parentID)
		}
	}

	if strings.Count(parentID, ".") >= 3 {
		return fmt.Errorf("maximum hierarchy depth (3) exceeded for parent %s", parentID)
	}

	var childNum int64
	haveNum := false
	if issue.ID == "" {
		n, err := allocateChildNumber(ctx, conn, parentID)
		if err != nil {
			return err
		}
		issue.ID = fmt.Sprintf("%s.%d", parentID, n)
		childNum, haveNum = int64(n), true
	} else {
		if err := ValidateIssueIDPrefix(issue.ID, prefix); err != nil {
			return wrapDBError("validate issue ID prefix", err)
		}
		isHierarchical, actualParent := IsHierarchicalID(issue.ID)
		if !isHierarchical || actualParent != parentID {
			return fmt.Errorf("explicit ID %s is not a child of parent %s", issue.ID, parentID)
		}
		if n, ok := parseChildSuffix(parentID, issue.ID); ok {
			childNum, haveNum = n, true
		}
	}

	// Plain INSERT: a UNIQUE violation aborts here and the create fails.
	// No second parent resurrection runs around the insert; the check above
	// already covered it within this transaction.
	if err := insertIssue(ctx, conn, issue); err != nil {
		return wrapDBError("insert issue", err)
	}
	if haveNum {
		if err := raiseChildFloor(ctx, conn, parentID, childNum); err != nil {
			return err
		}
	}
	if err := recordCreatedEvent(ctx, conn, issue, actor); err != nil {
		return wrapDBError("record creation event", err)
	}
	if err := markDirty(ctx, conn, issue.ID); err != nil {
		return wrapDBError("mark issue dirty", err)
	}

	// The parent edge commits with the row. The direction check runs here,
	// inside the transaction, so a rejected edge fails the create and writes
	// nothing instead of warning after the fact.
	dep := &types.Dependency{
		IssueID:     issue.ID,
		DependsOnID: parentID,
		Type:        types.DepParentChild,
	}
	if err := tx.AddDependency(ctx, dep, actor); err != nil {
		return err
	}
	return nil
}
