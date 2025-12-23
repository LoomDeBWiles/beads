package sqlite

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// epicTestHelper provides test setup and assertion methods
type epicTestHelper struct {
	t     *testing.T
	ctx   context.Context
	store *SQLiteStorage
}

func newEpicTestHelper(t *testing.T, store *SQLiteStorage) *epicTestHelper {
	return &epicTestHelper{t: t, ctx: context.Background(), store: store}
}

func (h *epicTestHelper) createEpic(title string) *types.Issue {
	epic := &types.Issue{
		Title:       title,
		Description: "Epic for testing",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := h.store.CreateIssue(h.ctx, epic, "test-user"); err != nil {
		h.t.Fatalf("CreateIssue (epic) failed: %v", err)
	}
	return epic
}

func (h *epicTestHelper) createTask(title string) *types.Issue {
	task := &types.Issue{
		Title:     title,
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := h.store.CreateIssue(h.ctx, task, "test-user"); err != nil {
		h.t.Fatalf("CreateIssue (%s) failed: %v", title, err)
	}
	return task
}

func (h *epicTestHelper) addParentChildDependency(childID, parentID string) {
	dep := &types.Dependency{
		IssueID:     childID,
		DependsOnID: parentID,
		Type:        types.DepParentChild,
	}
	if err := h.store.AddDependency(h.ctx, dep, "test-user"); err != nil {
		h.t.Fatalf("AddDependency failed: %v", err)
	}
}

func (h *epicTestHelper) closeIssue(id, reason string) {
	if err := h.store.CloseIssue(h.ctx, id, reason, "test-user"); err != nil {
		h.t.Fatalf("CloseIssue (%s) failed: %v", id, err)
	}
}

func (h *epicTestHelper) getEligibleEpics() []*types.EpicStatus {
	epics, err := h.store.GetEpicsEligibleForClosure(h.ctx)
	if err != nil {
		h.t.Fatalf("GetEpicsEligibleForClosure failed: %v", err)
	}
	return epics
}

func (h *epicTestHelper) findEpic(epics []*types.EpicStatus, epicID string) (*types.EpicStatus, bool) {
	for _, e := range epics {
		if e.Epic.ID == epicID {
			return e, true
		}
	}
	return nil, false
}

func (h *epicTestHelper) assertEpicStats(epic *types.EpicStatus, totalChildren, closedChildren int, eligible bool, desc string) {
	if epic.TotalChildren != totalChildren {
		h.t.Errorf("%s: Expected %d total children, got %d", desc, totalChildren, epic.TotalChildren)
	}
	if epic.ClosedChildren != closedChildren {
		h.t.Errorf("%s: Expected %d closed children, got %d", desc, closedChildren, epic.ClosedChildren)
	}
	if epic.EligibleForClose != eligible {
		h.t.Errorf("%s: Expected eligible=%v, got %v", desc, eligible, epic.EligibleForClose)
	}
}

func (h *epicTestHelper) assertEpicNotFound(epics []*types.EpicStatus, epicID string, desc string) {
	if _, found := h.findEpic(epics, epicID); found {
		h.t.Errorf("%s: Epic %s should not be in results", desc, epicID)
	}
}

func (h *epicTestHelper) assertEpicFound(epics []*types.EpicStatus, epicID string, desc string) *types.EpicStatus {
	epic, found := h.findEpic(epics, epicID)
	if !found {
		h.t.Fatalf("%s: Epic %s not found in results", desc, epicID)
	}
	return epic
}

func TestGetEpicsEligibleForClosure(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	h := newEpicTestHelper(t, store)
	epic := h.createEpic("Test Epic")
	task1 := h.createTask("Task 1")
	task2 := h.createTask("Task 2")
	h.addParentChildDependency(task1.ID, epic.ID)
	h.addParentChildDependency(task2.ID, epic.ID)

	// Test 1: Epic with open children should NOT be eligible
	epics := h.getEligibleEpics()
	if len(epics) == 0 {
		t.Fatal("Expected at least one epic")
	}
	e := h.assertEpicFound(epics, epic.ID, "All children open")
	h.assertEpicStats(e, 2, 0, false, "All children open")

	// Test 2: Close one task
	h.closeIssue(task1.ID, "Done")
	epics = h.getEligibleEpics()
	e = h.assertEpicFound(epics, epic.ID, "One child closed")
	h.assertEpicStats(e, 2, 1, false, "One child closed")

	// Test 3: Close second task - epic should be eligible
	h.closeIssue(task2.ID, "Done")
	epics = h.getEligibleEpics()
	e = h.assertEpicFound(epics, epic.ID, "All children closed")
	h.assertEpicStats(e, 2, 2, true, "All children closed")

	// Test 4: Close the epic - should no longer appear
	h.closeIssue(epic.ID, "All tasks complete")
	epics = h.getEligibleEpics()
	h.assertEpicNotFound(epics, epic.ID, "Closed epic")
}

func TestGetEpicsEligibleForClosureWithNoChildren(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	h := newEpicTestHelper(t, store)
	epic := h.createEpic("Childless Epic")
	epics := h.getEligibleEpics()

	// Should find the epic but it should NOT be eligible
	e := h.assertEpicFound(epics, epic.ID, "No children")
	h.assertEpicStats(e, 0, 0, false, "No children")
}

func TestGetParentEpics(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	h := newEpicTestHelper(t, store)

	// Create an epic and two tasks
	epic := h.createEpic("Parent Epic")
	task1 := h.createTask("Task 1")
	task2 := h.createTask("Task 2")

	// Add parent-child dependencies
	h.addParentChildDependency(task1.ID, epic.ID)
	h.addParentChildDependency(task2.ID, epic.ID)

	// Test 1: task1 should have epic as parent
	parents, err := store.GetParentEpics(ctx, task1.ID)
	if err != nil {
		t.Fatalf("GetParentEpics failed: %v", err)
	}
	if len(parents) != 1 {
		t.Fatalf("Expected 1 parent epic, got %d", len(parents))
	}
	if parents[0].ID != epic.ID {
		t.Errorf("Expected parent ID %s, got %s", epic.ID, parents[0].ID)
	}

	// Test 2: epic should have no parent epics
	parents, err = store.GetParentEpics(ctx, epic.ID)
	if err != nil {
		t.Fatalf("GetParentEpics failed: %v", err)
	}
	if len(parents) != 0 {
		t.Errorf("Expected no parent epics for epic, got %d", len(parents))
	}
}

func TestIsEpicEligibleForClosure(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	h := newEpicTestHelper(t, store)

	// Create an epic and two tasks
	epic := h.createEpic("Test Epic")
	task1 := h.createTask("Task 1")
	task2 := h.createTask("Task 2")
	h.addParentChildDependency(task1.ID, epic.ID)
	h.addParentChildDependency(task2.ID, epic.ID)

	// Test 1: Epic with open children should NOT be eligible
	eligible, err := store.IsEpicEligibleForClosure(ctx, epic.ID)
	if err != nil {
		t.Fatalf("IsEpicEligibleForClosure failed: %v", err)
	}
	if eligible {
		t.Error("Epic with open children should not be eligible")
	}

	// Test 2: Close one task - still not eligible
	h.closeIssue(task1.ID, "Done")
	eligible, err = store.IsEpicEligibleForClosure(ctx, epic.ID)
	if err != nil {
		t.Fatalf("IsEpicEligibleForClosure failed: %v", err)
	}
	if eligible {
		t.Error("Epic with some open children should not be eligible")
	}

	// Test 3: Close second task - now eligible
	h.closeIssue(task2.ID, "Done")
	eligible, err = store.IsEpicEligibleForClosure(ctx, epic.ID)
	if err != nil {
		t.Fatalf("IsEpicEligibleForClosure failed: %v", err)
	}
	if !eligible {
		t.Error("Epic with all children closed should be eligible")
	}
}

func TestIsEpicEligibleForClosureNoChildren(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	h := newEpicTestHelper(t, store)

	// Create an epic with no children
	epic := h.createEpic("Childless Epic")

	// Epic with no children should NOT be eligible
	eligible, err := store.IsEpicEligibleForClosure(ctx, epic.ID)
	if err != nil {
		t.Fatalf("IsEpicEligibleForClosure failed: %v", err)
	}
	if eligible {
		t.Error("Epic with no children should not be eligible")
	}
}
