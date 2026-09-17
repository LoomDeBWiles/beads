package sqlite

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func createParent(t *testing.T, store *SQLiteStorage, ctx context.Context, id string) {
	t.Helper()
	parent := &types.Issue{
		ID:          id,
		Title:       "Parent " + id,
		Description: "parent issue",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := store.CreateIssue(ctx, parent, "test"); err != nil {
		t.Fatalf("failed to create parent %s: %v", id, err)
	}
}

func createTask(t *testing.T, store *SQLiteStorage, ctx context.Context, id, title string) {
	t.Helper()
	issue := &types.Issue{
		ID:          id,
		Title:       title,
		Description: "task issue",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("failed to create issue %s: %v", id, err)
	}
}

func floorOf(t *testing.T, store *SQLiteStorage, ctx context.Context, parentID string) (int64, bool) {
	t.Helper()
	var floor int64
	err := store.db.QueryRowContext(ctx,
		`SELECT last_child FROM child_counters WHERE parent_id = ?`, parentID).Scan(&floor)
	if err != nil {
		return 0, false
	}
	return floor, true
}

func createdEventCount(t *testing.T, store *SQLiteStorage, ctx context.Context, id string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRowContext(ctx,
		`SELECT count(*) FROM events WHERE issue_id = ? AND event_type = 'created'`, id).Scan(&n); err != nil {
		t.Fatalf("failed to count created events: %v", err)
	}
	return n
}

// Stale floor allocates above existing children: the farmplanner shape, floor
// 33 with children past it, must yield the next free number, not an existing
// closed bug.
func TestCreateChildIssue_StaleFloorAllocatesAboveChildren(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-farm")
	for n := 30; n <= 43; n++ {
		createTask(t, store, ctx, fmt.Sprintf("bd-farm.%d", n), fmt.Sprintf("Old child %d", n))
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE child_counters SET last_child = 33 WHERE parent_id = 'bd-farm'`); err != nil {
		t.Fatalf("failed to force a stale floor: %v", err)
	}

	child := &types.Issue{
		Title: "New bug", Description: "scratch",
		Status: types.StatusOpen, Priority: 1, IssueType: types.TypeBug,
	}
	if err := store.CreateChildIssue(ctx, "bd-farm", child, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if child.ID != "bd-farm.44" {
		t.Fatalf("expected bd-farm.44 above 43 children, got %s", child.ID)
	}
	if got, err := store.GetIssue(ctx, "bd-farm.44"); err != nil || got == nil || got.Title != "New bug" {
		t.Fatalf("new row missing or wrong: %+v, err=%v", got, err)
	}
}

// An absent counter row after import allocates above imported children: the
// scan, not the floor, is what finds them.
func TestCreateChildIssue_AbsentCounterAllocatesAboveImported(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-imp")
	createTask(t, store, ctx, "bd-imp.1", "Imported child 1")
	createTask(t, store, ctx, "bd-imp.2", "Imported child 2")
	if _, err := store.db.ExecContext(ctx, `DELETE FROM child_counters WHERE parent_id = 'bd-imp'`); err != nil {
		t.Fatalf("failed to drop the counter row: %v", err)
	}

	child := &types.Issue{
		Title: "After import", Description: "scratch",
		Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
	}
	if err := store.CreateChildIssue(ctx, "bd-imp", child, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if child.ID != "bd-imp.3" {
		t.Fatalf("expected bd-imp.3, got %s", child.ID)
	}
}

// Closed and tombstoned children still occupy their numbers.
func TestCreateChildIssue_ClosedAndTombstonedOccupyNumbers(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-occ")
	createTask(t, store, ctx, "bd-occ.1", "Child 1")
	createTask(t, store, ctx, "bd-occ.2", "Child 2")
	if err := store.CloseIssue(ctx, "bd-occ.1", "done", "test"); err != nil {
		t.Fatalf("failed to close child: %v", err)
	}
	if err := store.CreateTombstone(ctx, "bd-occ.2", "test", "deleted"); err != nil {
		t.Fatalf("failed to tombstone child: %v", err)
	}

	child := &types.Issue{
		Title: "Next", Description: "scratch",
		Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
	}
	if err := store.CreateChildIssue(ctx, "bd-occ", child, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if child.ID != "bd-occ.3" {
		t.Fatalf("expected bd-occ.3 past closed and tombstoned children, got %s", child.ID)
	}
}

// An explicit duplicate ID fails naming the ID, leaving the old row, its
// title and its created-event count untouched.
func TestCreateChildIssue_ExplicitDuplicateFailsLoudly(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-dup")
	createTask(t, store, ctx, "bd-dup.2", "Old child 2")

	dup := &types.Issue{
		ID:          "bd-dup.2",
		Title:       "Should fail",
		Description: "scratch",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeBug,
	}
	err := store.CreateChildIssue(ctx, "bd-dup", dup, "test")
	if err == nil {
		t.Fatal("expected error for duplicate explicit ID, got nil")
	}
	if !strings.Contains(err.Error(), "bd-dup.2") || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error must name the ID and say it exists, got: %v", err)
	}

	old, getErr := store.GetIssue(ctx, "bd-dup.2")
	if getErr != nil || old == nil {
		t.Fatalf("old row missing after failed create: %v", getErr)
	}
	if old.Title != "Old child 2" {
		t.Errorf("old title changed to %q", old.Title)
	}
	if n := createdEventCount(t, store, ctx, "bd-dup.2"); n != 1 {
		t.Errorf("expected 1 created event on the old row, got %d", n)
	}
}

// An explicit ID whose parent mismatches errors before any write.
func TestCreateChildIssue_ExplicitIDParentMismatch(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-m1")
	createTask(t, store, ctx, "bd-m1.1", "Child of m1")

	wrong := &types.Issue{
		ID:          "bd-m1.10",
		Title:       "Wrong parent",
		Description: "scratch",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
	}
	err := store.CreateChildIssue(ctx, "bd-m1.1", wrong, "test")
	if err == nil {
		t.Fatal("expected error for mismatched parent, got nil")
	}
	if !strings.Contains(err.Error(), "explicit ID bd-m1.10 is not a child of parent bd-m1.1") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := store.GetIssue(ctx, "bd-m1.10"); got != nil {
		t.Errorf("mismatched create left a row behind")
	}
}

// The issue row and the parent edge commit together: a create rejected by the
// parent-child direction check leaves neither.
func TestCreateChildIssue_RowAndEdgeCommitTogether(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-ep")
	createTask(t, store, ctx, "bd-ep.1", "A task")

	beforeIssues := countIssues(t, store, ctx)
	beforeEdges := countParentEdges(t, store, ctx, "bd-ep.1")

	// An epic under a non-epic trips the direction check inside the transaction.
	epic := &types.Issue{
		ID:          "bd-ep.99",
		Title:       "Epic under a task",
		Description: "scratch",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	err := store.CreateChildIssue(ctx, "bd-ep.1", epic, "test")
	if err == nil {
		t.Fatal("expected direction-check error, got nil")
	}
	if got, _ := store.GetIssue(ctx, "bd-ep.99"); got != nil {
		t.Errorf("rejected create left a row behind")
	}
	if n := countIssues(t, store, ctx); n != beforeIssues {
		t.Errorf("issue count changed %d -> %d on rejected create", beforeIssues, n)
	}
	if n := countParentEdges(t, store, ctx, "bd-ep.1"); n != beforeEdges {
		t.Errorf("parent edges changed %d -> %d on rejected create", beforeEdges, n)
	}
}

// A create whose parent edge is rejected writes nothing (allocated-ID path:
// the would-be ID stays absent and counts stay put).
func TestCreateChildIssue_EpicUnderTaskWritesNothing(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-rej")
	createTask(t, store, ctx, "bd-rej.1", "A task")

	beforeIssues := countIssues(t, store, ctx)
	epic := &types.Issue{
		Title: "Epic under a task", Description: "scratch",
		Status: types.StatusOpen, Priority: 1, IssueType: types.TypeEpic,
	}
	err := store.CreateChildIssue(ctx, "bd-rej.1", epic, "test")
	if err == nil {
		t.Fatal("expected direction-check error, got nil")
	}
	if n := countIssues(t, store, ctx); n != beforeIssues {
		t.Errorf("issue count changed %d -> %d on rejected create", beforeIssues, n)
	}
	// The allocated ID was rolled back with the row: no row carries the title.
	rows, qerr := store.db.QueryContext(ctx, `SELECT id FROM issues WHERE title = 'Epic under a task'`)
	if qerr != nil {
		t.Fatalf("failed to query for stray rows: %v", qerr)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Errorf("rejected create left a row behind")
	}
}

// A number is never reissued after its issue's row is gone outright: the
// insert raised the floor when the child was written.
func TestCreateChildIssue_NumberNotReissuedAfterHardDelete(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-del")
	createTask(t, store, ctx, "bd-del.1", "Child 1")
	createTask(t, store, ctx, "bd-del.2", "Child 2")

	doomed := &types.Issue{
		ID:          "bd-del.3",
		Title:       "Doomed child",
		Description: "scratch",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
	}
	if err := store.CreateChildIssue(ctx, "bd-del", doomed, "test"); err != nil {
		t.Fatalf("failed to create explicit child: %v", err)
	}
	// bd delete --hard keeps a tombstone row by design, so delete outright to
	// reach the state the floor is the only defence against.
	if _, err := store.db.ExecContext(ctx, `DELETE FROM issues WHERE id = 'bd-del.3'`); err != nil {
		t.Fatalf("failed to delete row outright: %v", err)
	}

	next := &types.Issue{
		Title: "After hard delete", Description: "scratch",
		Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
	}
	if err := store.CreateChildIssue(ctx, "bd-del", next, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if next.ID == "bd-del.3" {
		t.Fatalf("reissued the deleted number bd-del.3")
	}
	if next.ID != "bd-del.4" {
		t.Fatalf("expected bd-del.4, got %s", next.ID)
	}
}

// Concurrent creates under one parent yield distinct IDs.
func TestCreateChildIssue_Concurrent(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-conc")

	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			child := &types.Issue{
				Title: fmt.Sprintf("Concurrent %d", idx), Description: "scratch",
				Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
			}
			errs[idx] = store.CreateChildIssue(ctx, "bd-conc", child, "test")
			ids[idx] = child.ID
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d failed: %v", i, err)
		}
	}
	seen := make(map[string]bool, n)
	for _, id := range ids {
		if id == "" {
			t.Fatal("concurrent create left an empty ID")
		}
		if seen[id] {
			t.Fatalf("duplicate child ID %s from concurrent creates", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct IDs, got %d", n, len(seen))
	}
}

// A parent ID containing LIKE metacharacters matches literally: the decoy
// child of a lookalike parent must not move the allocation.
func TestCreateChildIssue_UnderscoreParentMatchedLiterally(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-under_score")
	createTask(t, store, ctx, "bd-under_score.1", "Child 1")
	createTask(t, store, ctx, "bd-under_score.2", "Child 2")

	// Lookalike parent whose child number would win if `_` acted as a wildcard.
	createParent(t, store, ctx, "bd-underXscore")
	createTask(t, store, ctx, "bd-underXscore.9", "Decoy")

	child := &types.Issue{
		Title: "Next", Description: "scratch",
		Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
	}
	if err := store.CreateChildIssue(ctx, "bd-under_score", child, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if child.ID != "bd-under_score.3" {
		t.Fatalf("expected bd-under_score.3, got %s", child.ID)
	}
}

func countIssues(t *testing.T, store *SQLiteStorage, ctx context.Context) int {
	t.Helper()
	var n int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM issues`).Scan(&n); err != nil {
		t.Fatalf("failed to count issues: %v", err)
	}
	return n
}

func countParentEdges(t *testing.T, store *SQLiteStorage, ctx context.Context, parentID string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRowContext(ctx,
		`SELECT count(*) FROM dependencies WHERE depends_on_id = ? AND type = 'parent-child'`,
		parentID).Scan(&n); err != nil {
		t.Fatalf("failed to count parent edges: %v", err)
	}
	return n
}
