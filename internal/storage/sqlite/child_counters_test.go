package sqlite

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestChildCountersTableExists(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Verify table exists by querying it
	var count int
	err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='child_counters'`).Scan(&count)
	if err != nil {
		t.Fatalf("failed to check for child_counters table: %v", err)
	}

	if count != 1 {
		t.Errorf("child_counters table not found, got count %d", count)
	}
}

func allocateOnConn(t *testing.T, store *SQLiteStorage, ctx context.Context, parentID string) int {
	t.Helper()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to acquire connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	n, err := allocateChildNumber(ctx, conn, parentID)
	if err != nil {
		t.Fatalf("allocateChildNumber failed: %v", err)
	}
	return n
}

func childFloor(t *testing.T, store *SQLiteStorage, ctx context.Context, parentID string) (int64, bool) {
	t.Helper()
	var floor int64
	err := store.db.QueryRowContext(ctx,
		`SELECT last_child FROM child_counters WHERE parent_id = ?`, parentID).Scan(&floor)
	if err == sql.ErrNoRows {
		return 0, false
	}
	if err != nil {
		t.Fatalf("failed to read child floor: %v", err)
	}
	return floor, true
}

func TestAllocateChildNumber(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	parentID := "bd-af78e9a2"

	// Create parent issue first (required by foreign key)
	parent := &types.Issue{
		ID:          parentID,
		Title:       "Parent epic",
		Description: "Test parent",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := store.CreateIssue(ctx, parent, "test-user"); err != nil {
		t.Fatalf("failed to create parent issue: %v", err)
	}

	// Allocation is a pure read: ten reads with no insert between them return
	// the same number and leave the floor unchanged.
	for i := 0; i < 10; i++ {
		if n := allocateOnConn(t, store, ctx, parentID); n != 1 {
			t.Fatalf("expected allocation 1 with no children, got %d", n)
		}
	}
	if _, present := childFloor(t, store, ctx, parentID); present {
		t.Errorf("repeated reads must not write a floor row")
	}

	// After a child is written, allocation moves above it.
	child := &types.Issue{
		ID:          parentID + ".1",
		Title:       "Child",
		Description: "Test child",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}
	if err := store.CreateIssue(ctx, child, "test-user"); err != nil {
		t.Fatalf("failed to create child issue: %v", err)
	}
	if n := allocateOnConn(t, store, ctx, parentID); n != 2 {
		t.Errorf("expected allocation 2 after one child, got %d", n)
	}
}

func TestAllocateChildNumber_DifferentParents(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	parent1 := "bd-af78e9a2"
	parent2 := "bd-bf78e9a3"

	// Create two independent parent issues
	for _, id := range []string{parent1, parent2} {
		parent := &types.Issue{
			ID:          id,
			Title:       "Parent " + id,
			Description: "Test parent",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeEpic,
		}
		if err := store.CreateIssue(ctx, parent, "test-user"); err != nil {
			t.Fatalf("failed to create parent issue %s: %v", id, err)
		}
	}

	// Each parent allocates independently, starting at 1 with no children.
	if n := allocateOnConn(t, store, ctx, parent1); n != 1 {
		t.Errorf("expected parent1 allocation 1, got %d", n)
	}
	if n := allocateOnConn(t, store, ctx, parent2); n != 1 {
		t.Errorf("expected parent2 allocation 1, got %d", n)
	}
	// Reads leave both floors unchanged.
	if _, present := childFloor(t, store, ctx, parent1); present {
		t.Errorf("reads must not write a floor for parent1")
	}
	if _, present := childFloor(t, store, ctx, parent2); present {
		t.Errorf("reads must not write a floor for parent2")
	}
}

func TestAllocateChildNumber_ConcurrentReads(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	parentID := "bd-af78e9a2"
	numWorkers := 10

	// Create parent issue first
	parent := &types.Issue{
		ID:          parentID,
		Title:       "Parent epic",
		Description: "Test parent",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := store.CreateIssue(ctx, parent, "test-user"); err != nil {
		t.Fatalf("failed to create parent issue: %v", err)
	}

	// Concurrent reads with no insert between them all agree; uniqueness of
	// issued numbers is covered by the CreateChildIssue concurrency test.
	results := make([]int, numWorkers)
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			conn, err := store.db.Conn(ctx)
			if err != nil {
				t.Errorf("failed to acquire connection: %v", err)
				return
			}
			defer func() { _ = conn.Close() }()
			n, err := allocateChildNumber(ctx, conn, parentID)
			if err != nil {
				t.Errorf("concurrent allocateChildNumber failed: %v", err)
				return
			}
			results[idx] = n
		}(i)
	}
	wg.Wait()

	for _, n := range results {
		if n != 1 {
			t.Errorf("expected every concurrent read to return 1, got %d", n)
		}
	}
	if _, present := childFloor(t, store, ctx, parentID); present {
		t.Errorf("concurrent reads must not write a floor row")
	}
}

func TestAllocateChildNumber_NestedHierarchy(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create parent issues for nested hierarchy
	parents := []string{"bd-af78e9a2", "bd-af78e9a2.1", "bd-af78e9a2.1.2"}
	for _, id := range parents {
		parent := &types.Issue{
			ID:          id,
			Title:       "Parent " + id,
			Description: "Test parent",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeEpic,
		}
		if err := store.CreateIssue(ctx, parent, "test-user"); err != nil {
			t.Fatalf("failed to create parent issue %s: %v", id, err)
		}
	}

	// Each level allocates above the children already present: the nesting
	// itself occupies numbers (.1 exists under the root, .2 under .1).
	for _, tc := range []struct {
		parent   string
		expected int
	}{
		{"bd-af78e9a2", 2},
		{"bd-af78e9a2.1", 3},
		{"bd-af78e9a2.1.2", 1},
	} {
		if n := allocateOnConn(t, store, ctx, tc.parent); n != tc.expected {
			t.Errorf("parent %s: expected allocation %d, got %d", tc.parent, tc.expected, n)
		}
	}
}
