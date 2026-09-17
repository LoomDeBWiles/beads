package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/types"
)

// insertIssue inserts a single issue into the database.
//
// The insert is strict: a plain INSERT lets a UNIQUE violation abort here,
// and a collision returns an error naming the ID instead of reporting success
// without writing a row. Callers must not swallow that error: the importer's
// rename recovery recognises it through IsUniqueConstraintError, so the
// driver's text survives via %w. A successful insert of a hierarchical ID
// raises its parent's floor, and any ID seeds its own floor from children
// already present (orphan-first ordering).
func insertIssue(ctx context.Context, conn *sql.Conn, issue *types.Issue) error {
	sourceRepo := issue.SourceRepo
	if sourceRepo == "" {
		sourceRepo = "." // Default to primary repo
	}

	wisp := 0
	if issue.Wisp {
		wisp = 1
	}
	pinned := 0
	if issue.Pinned {
		pinned = 1
	}
	isTemplate := 0
	if issue.IsTemplate {
		isTemplate = 1
	}

	_, err := conn.ExecContext(ctx, `
		INSERT INTO issues (
			id, content_hash, title, description, design, acceptance_criteria, notes,
			status, priority, issue_type, assignee, estimated_minutes,
			created_at, updated_at, closed_at, external_ref, source_repo, close_reason,
			deleted_at, deleted_by, delete_reason, original_type,
			sender, ephemeral, pinned, is_template, claim_expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		issue.ID, issue.ContentHash, issue.Title, issue.Description, issue.Design,
		issue.AcceptanceCriteria, issue.Notes, issue.Status,
		issue.Priority, issue.IssueType, issue.Assignee,
		issue.EstimatedMinutes, issue.CreatedAt, issue.UpdatedAt,
		issue.ClosedAt, issue.ExternalRef, sourceRepo, issue.CloseReason,
		issue.DeletedAt, issue.DeletedBy, issue.DeleteReason, issue.OriginalType,
		issue.Sender, wisp, pinned, isTemplate, issue.ClaimExpiresAt,
	)
	if err != nil {
		if IsUniqueConstraintError(err) {
			return fmt.Errorf("issue %s already exists: %w", issue.ID, err)
		}
		return fmt.Errorf("failed to insert issue: %w", err)
	}
	if isHierarchical, parent := IsHierarchicalID(issue.ID); isHierarchical {
		if n, ok := parseChildSuffix(parent, issue.ID); ok {
			if err := raiseChildFloor(ctx, conn, parent, n); err != nil {
				return err
			}
		}
	}
	if err := seedChildFloor(ctx, conn, issue.ID); err != nil {
		return err
	}
	return nil
}

// insertIssues bulk inserts multiple issues using a prepared statement.
// Like insertIssue it is strict: a collision on any row aborts the batch with
// an error naming the ID. After the rows land, floors are maintained for the
// batch: each parent to its group's maximum suffix, plus orphan-first seeding.
func insertIssues(ctx context.Context, conn *sql.Conn, issues []*types.Issue) error {
	stmt, err := conn.PrepareContext(ctx, `
		INSERT INTO issues (
			id, content_hash, title, description, design, acceptance_criteria, notes,
			status, priority, issue_type, assignee, estimated_minutes,
			created_at, updated_at, closed_at, external_ref, source_repo, close_reason,
			deleted_at, deleted_by, delete_reason, original_type,
			sender, ephemeral, pinned, is_template, claim_expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, issue := range issues {
		sourceRepo := issue.SourceRepo
		if sourceRepo == "" {
			sourceRepo = "." // Default to primary repo
		}

		wisp := 0
		if issue.Wisp {
			wisp = 1
		}
		pinned := 0
		if issue.Pinned {
			pinned = 1
		}
		isTemplate := 0
		if issue.IsTemplate {
			isTemplate = 1
		}

		_, err = stmt.ExecContext(ctx,
			issue.ID, issue.ContentHash, issue.Title, issue.Description, issue.Design,
			issue.AcceptanceCriteria, issue.Notes, issue.Status,
			issue.Priority, issue.IssueType, issue.Assignee,
			issue.EstimatedMinutes, issue.CreatedAt, issue.UpdatedAt,
			issue.ClosedAt, issue.ExternalRef, sourceRepo, issue.CloseReason,
			issue.DeletedAt, issue.DeletedBy, issue.DeleteReason, issue.OriginalType,
			issue.Sender, wisp, pinned, isTemplate, issue.ClaimExpiresAt,
		)
		if err != nil {
			if IsUniqueConstraintError(err) {
				return fmt.Errorf("issue %s already exists: %w", issue.ID, err)
			}
			return fmt.Errorf("failed to insert issue %s: %w", issue.ID, err)
		}
	}
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	if err := maintainFloorsForIDs(ctx, conn, ids); err != nil {
		return err
	}
	return nil
}
