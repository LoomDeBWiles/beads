package migrations

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// edgeColumns are the deprecated columns this migration removes from issues.
var edgeColumns = []string{"replies_to", "relates_to", "duplicate_of", "superseded_by"}

// canonicalIssuesColumns lists the columns the rebuilt issues table declares
// directly, with the constraints they carry. Columns added to issues by any
// later migration are discovered at runtime and appended, so this list governs
// only which constraints survive the rebuild - never which data is copied.
const canonicalIssuesColumns = `
	id TEXT PRIMARY KEY,
	content_hash TEXT,
	title TEXT NOT NULL CHECK(length(title) <= 500),
	description TEXT NOT NULL DEFAULT '',
	design TEXT NOT NULL DEFAULT '',
	acceptance_criteria TEXT NOT NULL DEFAULT '',
	notes TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'open',
	priority INTEGER NOT NULL DEFAULT 2 CHECK(priority >= 0 AND priority <= 4),
	issue_type TEXT NOT NULL DEFAULT 'task',
	assignee TEXT,
	estimated_minutes INTEGER,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	closed_at DATETIME,
	external_ref TEXT,
	source_repo TEXT DEFAULT '',
	compaction_level INTEGER DEFAULT 0,
	compacted_at DATETIME,
	compacted_at_commit TEXT,
	original_size INTEGER,
	deleted_at DATETIME,
	deleted_by TEXT DEFAULT '',
	delete_reason TEXT DEFAULT '',
	original_type TEXT DEFAULT '',
	sender TEXT DEFAULT '',
	ephemeral INTEGER DEFAULT 0,
	close_reason TEXT DEFAULT '',
	CHECK ((status = 'closed') = (closed_at IS NOT NULL))
`

// canonicalIssuesColumnNames is the set of column names declared by
// canonicalIssuesColumns, used to tell known columns from ones a later
// migration added.
var canonicalIssuesColumnNames = map[string]bool{
	"id": true, "content_hash": true, "title": true, "description": true,
	"design": true, "acceptance_criteria": true, "notes": true, "status": true,
	"priority": true, "issue_type": true, "assignee": true, "estimated_minutes": true,
	"created_at": true, "updated_at": true, "closed_at": true, "external_ref": true,
	"source_repo": true, "compaction_level": true, "compacted_at": true,
	"compacted_at_commit": true, "original_size": true, "deleted_at": true,
	"deleted_by": true, "delete_reason": true, "original_type": true,
	"sender": true, "ephemeral": true, "close_reason": true,
}

// issuesColumn describes one column of the live issues table.
type issuesColumn struct {
	name       string
	declType   string
	notNull    bool
	defaultSQL sql.NullString
}

// liveIssuesColumns reads the issues table's columns in declaration order from
// the database itself. Reading the live schema, rather than trusting a list
// written when this migration was, is what keeps the rebuild from dropping
// columns added after it (bd-ok4pr.1.8).
func liveIssuesColumns(db *sql.DB) ([]issuesColumn, error) {
	rows, err := db.Query(`SELECT name, type, "notnull", dflt_value FROM pragma_table_info('issues') ORDER BY cid`)
	if err != nil {
		return nil, fmt.Errorf("failed to read issues columns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var cols []issuesColumn
	for rows.Next() {
		var c issuesColumn
		if err := rows.Scan(&c.name, &c.declType, &c.notNull, &c.defaultSQL); err != nil {
			return nil, fmt.Errorf("failed to scan issues column: %w", err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issues columns: %w", err)
	}
	return cols, nil
}

// addColumnClause renders an ALTER TABLE ADD COLUMN body for a column carried
// over from the live table. A NOT NULL column with no default cannot be added
// to an existing table in SQLite, so the constraint is relaxed rather than
// losing the column; the data itself is copied either way.
func addColumnClause(c issuesColumn) string {
	clause := c.name
	if c.declType != "" {
		clause += " " + c.declType
	}
	if c.defaultSQL.Valid {
		clause += " DEFAULT " + c.defaultSQL.String
		if c.notNull {
			clause += " NOT NULL"
		}
	}
	return clause
}

// copyExpr returns the SELECT expression used to copy one column across.
// source_repo and close_reason are normalised because the rebuilt table
// declares them with a '' default and older rows may hold NULL.
func copyExpr(name string) string {
	switch name {
	case "source_repo", "close_reason":
		return fmt.Sprintf("COALESCE(%s, '')", name)
	default:
		return name
	}
}

// survivingIssuesIndexes returns the CREATE INDEX statements of the live issues
// table that do not mention a column being dropped. DROP TABLE takes the
// table's indexes with it, so they are replayed after the rebuild instead of
// being silently lost.
func survivingIssuesIndexes(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'issues' AND sql IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("failed to read issues indexes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stmts []string
	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			return nil, fmt.Errorf("failed to scan issues index: %w", err)
		}
		if !mentionsEdgeColumn(stmt) {
			stmts = append(stmts, stmt)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issues indexes: %w", err)
	}
	return stmts, nil
}

// edgeColumnPattern matches a reference to any dropped column as a whole word.
var edgeColumnPattern = regexp.MustCompile(`\b(` + strings.Join(edgeColumns, "|") + `)\b`)

// mentionsEdgeColumn reports whether a statement references any column this
// migration drops, in an indexed expression or in a partial index predicate.
func mentionsEdgeColumn(stmt string) bool {
	return edgeColumnPattern.MatchString(stmt)
}

// MigrateDropEdgeColumns removes the deprecated edge fields from the issues table.
// This is Phase 4 of the Edge Schema Consolidation (Decision 004).
//
// Removes columns:
// - replies_to (now: replies-to dependency)
// - relates_to (now: relates-to dependencies)
// - duplicate_of (now: duplicates dependency)
// - superseded_by (now: supersedes dependency)
//
// Prerequisites:
// - Migration 021 (migrate_edge_fields) must have already run to convert data
// - All code must be updated to use the dependencies API
//
// SQLite doesn't support DROP COLUMN directly in older versions, so we
// recreate the table without the deprecated columns.
func MigrateDropEdgeColumns(db *sql.DB) error {
	liveColumns, err := liveIssuesColumns(db)
	if err != nil {
		return err
	}

	dropped := make(map[string]bool, len(edgeColumns))
	for _, col := range edgeColumns {
		dropped[col] = true
	}

	// If none of the deprecated columns exist, migration already ran
	anyToDrop := false
	for _, col := range liveColumns {
		if dropped[col.name] {
			anyToDrop = true
			break
		}
	}
	if !anyToDrop {
		return nil
	}

	// Everything the live table has, minus what we are dropping: this is both
	// what the rebuilt table must hold and what gets copied into it.
	var carried []issuesColumn
	for _, col := range liveColumns {
		if !dropped[col.name] {
			carried = append(carried, col)
		}
	}

	indexStmts, err := survivingIssuesIndexes(db)
	if err != nil {
		return err
	}

	// SQLite 3.35.0+ supports DROP COLUMN, but we use table recreation for compatibility
	// This is idempotent - we recreate the table without the deprecated columns

	// NOTE: Foreign keys are disabled in RunMigrations() before the EXCLUSIVE transaction starts.
	// This prevents ON DELETE CASCADE from deleting dependencies when we drop/recreate the issues table.

	// Drop views that depend on the issues table BEFORE starting savepoint
	// This is necessary because SQLite validates views during table operations
	_, err = db.Exec(`DROP VIEW IF EXISTS ready_issues`)
	if err != nil {
		return fmt.Errorf("failed to drop ready_issues view: %w", err)
	}
	_, err = db.Exec(`DROP VIEW IF EXISTS blocked_issues`)
	if err != nil {
		return fmt.Errorf("failed to drop blocked_issues view: %w", err)
	}

	// Use SAVEPOINT for atomicity (we're already inside an EXCLUSIVE transaction from RunMigrations)
	// SQLite doesn't support nested transactions but SAVEPOINTs work inside transactions
	_, err = db.Exec(`SAVEPOINT drop_edge_columns`)
	if err != nil {
		return fmt.Errorf("failed to create savepoint: %w", err)
	}
	savepointReleased := false
	defer func() {
		if !savepointReleased {
			_, _ = db.Exec(`ROLLBACK TO SAVEPOINT drop_edge_columns`)
		}
	}()

	// Create the new table with the constrained columns this migration knows
	// about, then append every other column the live table carries.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS issues_new (` + canonicalIssuesColumns + `)`)
	if err != nil {
		return fmt.Errorf("failed to create new issues table: %w", err)
	}

	for _, col := range carried {
		if canonicalIssuesColumnNames[col.name] {
			continue
		}
		_, err = db.Exec(`ALTER TABLE issues_new ADD COLUMN ` + addColumnClause(col))
		if err != nil {
			return fmt.Errorf("failed to add %s column to new issues table: %w", col.name, err)
		}
	}

	// Copy every carried column across, so nothing added after this migration
	// was written is left behind.
	names := make([]string, len(carried))
	exprs := make([]string, len(carried))
	for i, col := range carried {
		names[i] = col.name
		exprs[i] = copyExpr(col.name)
	}
	_, err = db.Exec(fmt.Sprintf(
		`INSERT INTO issues_new (%s) SELECT %s FROM issues`,
		strings.Join(names, ", "), strings.Join(exprs, ", "),
	))
	if err != nil {
		return fmt.Errorf("failed to copy issues data: %w", err)
	}

	// Drop old table
	_, err = db.Exec(`DROP TABLE issues`)
	if err != nil {
		return fmt.Errorf("failed to drop old issues table: %w", err)
	}

	// Rename new table to issues
	_, err = db.Exec(`ALTER TABLE issues_new RENAME TO issues`)
	if err != nil {
		return fmt.Errorf("failed to rename new issues table: %w", err)
	}

	// Replay the indexes the live table had, then ensure the baseline set.
	// The replayed statements come first: they carry their original names, and
	// the baseline creations below are IF NOT EXISTS, so the two cannot collide.
	for _, stmt := range indexStmts {
		if _, err = db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to recreate index: %w", err)
		}
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_issues_status ON issues(status)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_priority ON issues(priority)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_assignee ON issues(assignee)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_created_at ON issues(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_external_ref ON issues(external_ref) WHERE external_ref IS NOT NULL`,
	}

	for _, idx := range indexes {
		_, err = db.Exec(idx)
		if err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// Release savepoint (commits the changes within the outer transaction)
	_, err = db.Exec(`RELEASE SAVEPOINT drop_edge_columns`)
	if err != nil {
		return fmt.Errorf("failed to release savepoint: %w", err)
	}
	savepointReleased = true

	// Recreate views that we dropped earlier (after commit, outside transaction)
	// ready_issues view
	_, err = db.Exec(`
		CREATE VIEW IF NOT EXISTS ready_issues AS
		WITH RECURSIVE
		  blocked_directly AS (
		    SELECT DISTINCT d.issue_id
		    FROM dependencies d
		    JOIN issues blocker ON d.depends_on_id = blocker.id
		    WHERE d.type = 'blocks'
		      AND blocker.status IN ('open', 'in_progress', 'blocked')
		  ),
		  blocked_transitively AS (
		    SELECT issue_id, 0 as depth
		    FROM blocked_directly
		    UNION ALL
		    SELECT d.issue_id, bt.depth + 1
		    FROM blocked_transitively bt
		    JOIN dependencies d ON d.depends_on_id = bt.issue_id
		    WHERE d.type = 'parent-child'
		      AND bt.depth < 50
		  )
		SELECT i.*
		FROM issues i
		WHERE i.status = 'open'
		  AND NOT EXISTS (
		    SELECT 1 FROM blocked_transitively WHERE issue_id = i.id
		  )
	`)
	if err != nil {
		return fmt.Errorf("failed to recreate ready_issues view: %w", err)
	}

	// blocked_issues view
	_, err = db.Exec(`
		CREATE VIEW IF NOT EXISTS blocked_issues AS
		SELECT
		    i.*,
		    COUNT(d.depends_on_id) as blocked_by_count
		FROM issues i
		JOIN dependencies d ON i.id = d.issue_id
		JOIN issues blocker ON d.depends_on_id = blocker.id
		WHERE i.status IN ('open', 'in_progress', 'blocked')
		  AND d.type = 'blocks'
		  AND blocker.status IN ('open', 'in_progress', 'blocked')
		GROUP BY i.id
	`)
	if err != nil {
		return fmt.Errorf("failed to recreate blocked_issues view: %w", err)
	}

	return nil
}
