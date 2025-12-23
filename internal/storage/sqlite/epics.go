package sqlite

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/types"
)

// GetEpicsEligibleForClosure returns all epics with their completion status
func (s *SQLiteStorage) GetEpicsEligibleForClosure(ctx context.Context) ([]*types.EpicStatus, error) {
	query := `
		WITH epic_children AS (
			SELECT 
				d.depends_on_id AS epic_id,
				i.id AS child_id,
				i.status AS child_status
			FROM dependencies d
			JOIN issues i ON i.id = d.issue_id
			WHERE d.type = 'parent-child'
		),
		epic_stats AS (
			SELECT 
				epic_id,
				COUNT(*) AS total_children,
				SUM(CASE WHEN child_status = 'closed' THEN 1 ELSE 0 END) AS closed_children
			FROM epic_children
			GROUP BY epic_id
		)
		SELECT 
			i.id, i.title, i.description, i.design, i.acceptance_criteria, i.notes,
			i.status, i.priority, i.issue_type, i.assignee, i.estimated_minutes,
			i.created_at, i.updated_at, i.closed_at, i.external_ref,
			COALESCE(es.total_children, 0) AS total_children,
			COALESCE(es.closed_children, 0) AS closed_children
		FROM issues i
		LEFT JOIN epic_stats es ON es.epic_id = i.id
		WHERE i.issue_type = 'epic'
		  AND i.status != 'closed'
		ORDER BY i.priority ASC, i.created_at ASC
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*types.EpicStatus
	for rows.Next() {
		var epic types.Issue
		var totalChildren, closedChildren int
		var assignee sql.NullString

		err := rows.Scan(
			&epic.ID, &epic.Title, &epic.Description, &epic.Design,
			&epic.AcceptanceCriteria, &epic.Notes, &epic.Status,
			&epic.Priority, &epic.IssueType, &assignee,
			&epic.EstimatedMinutes, &epic.CreatedAt, &epic.UpdatedAt,
			&epic.ClosedAt, &epic.ExternalRef,
			&totalChildren, &closedChildren,
		)
		if err != nil {
			return nil, err
		}

		// Convert sql.NullString to string
		if assignee.Valid {
			epic.Assignee = assignee.String
		}

		eligibleForClose := false
		if totalChildren > 0 && closedChildren == totalChildren {
			eligibleForClose = true
		}

		results = append(results, &types.EpicStatus{
			Epic:             &epic,
			TotalChildren:    totalChildren,
			ClosedChildren:   closedChildren,
			EligibleForClose: eligibleForClose,
		})
	}

	return results, rows.Err()
}

// GetParentEpics returns parent epics of an issue (via parent-child dependency).
// Used for auto-closing eligible parent epics when closing a child issue.
func (s *SQLiteStorage) GetParentEpics(ctx context.Context, issueID string) ([]*types.Issue, error) {
	query := `
		SELECT i.id, i.title, i.description, i.design, i.acceptance_criteria, i.notes,
		       i.status, i.priority, i.issue_type, i.assignee, i.estimated_minutes,
		       i.created_at, i.updated_at, i.closed_at, i.external_ref
		FROM issues i
		JOIN dependencies d ON i.id = d.depends_on_id
		WHERE d.issue_id = ?
		  AND d.type = 'parent-child'
		  AND i.issue_type = 'epic'
		ORDER BY i.priority ASC
	`

	rows, err := s.db.QueryContext(ctx, query, issueID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*types.Issue
	for rows.Next() {
		var epic types.Issue
		var assignee sql.NullString

		err := rows.Scan(
			&epic.ID, &epic.Title, &epic.Description, &epic.Design,
			&epic.AcceptanceCriteria, &epic.Notes, &epic.Status,
			&epic.Priority, &epic.IssueType, &assignee,
			&epic.EstimatedMinutes, &epic.CreatedAt, &epic.UpdatedAt,
			&epic.ClosedAt, &epic.ExternalRef,
		)
		if err != nil {
			return nil, err
		}

		if assignee.Valid {
			epic.Assignee = assignee.String
		}
		results = append(results, &epic)
	}

	return results, rows.Err()
}

// IsEpicEligibleForClosure returns true if the epic has at least one child
// and all children are closed.
func (s *SQLiteStorage) IsEpicEligibleForClosure(ctx context.Context, epicID string) (bool, error) {
	query := `
		SELECT
			COUNT(*) AS total_children,
			COALESCE(SUM(CASE WHEN i.status = 'closed' THEN 1 ELSE 0 END), 0) AS closed_children
		FROM dependencies d
		JOIN issues i ON i.id = d.issue_id
		WHERE d.depends_on_id = ?
		  AND d.type = 'parent-child'
	`

	var totalChildren, closedChildren int
	err := s.db.QueryRowContext(ctx, query, epicID).Scan(&totalChildren, &closedChildren)
	if err != nil {
		return false, err
	}

	// Eligible if has at least one child and all children are closed
	return totalChildren > 0 && closedChildren == totalChildren, nil
}
