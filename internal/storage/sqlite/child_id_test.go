package sqlite

import (
	"context"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func newChildIssue(title string) *types.Issue {
	return &types.Issue{
		Title:       title,
		Description: "child issue",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}
}

func TestCreateChildIssue(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// Create a parent issue with hash ID
	parent := &types.Issue{
		ID:          "bd-a3f8e9",
		Title:       "Parent Epic",
		Description: "Parent issue",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := store.CreateIssue(ctx, parent, "test"); err != nil {
		t.Fatalf("failed to create parent: %v", err)
	}

	// First child allocates .1 and writes the row
	child1 := newChildIssue("Child Task 1")
	if err := store.CreateChildIssue(ctx, parent.ID, child1, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if child1.ID != "bd-a3f8e9.1" {
		t.Errorf("expected bd-a3f8e9.1, got %s", child1.ID)
	}
	if got, err := store.GetIssue(ctx, child1.ID); err != nil || got == nil {
		t.Fatalf("child row missing after CreateChildIssue: %v", err)
	}

	// Second child allocates sequentially
	child2 := newChildIssue("Child Task 2")
	if err := store.CreateChildIssue(ctx, parent.ID, child2, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if child2.ID != "bd-a3f8e9.2" {
		t.Errorf("expected bd-a3f8e9.2, got %s", child2.ID)
	}

	// Nested child (depth 2)
	nested1 := newChildIssue("Nested Task")
	if err := store.CreateChildIssue(ctx, child1.ID, nested1, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed for nested: %v", err)
	}
	if nested1.ID != "bd-a3f8e9.1.1" {
		t.Errorf("expected bd-a3f8e9.1.1, got %s", nested1.ID)
	}

	// Third level (depth 3, maximum)
	deep1 := newChildIssue("Deep Task")
	if err := store.CreateChildIssue(ctx, nested1.ID, deep1, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed for depth 3: %v", err)
	}
	if deep1.ID != "bd-a3f8e9.1.1.1" {
		t.Errorf("expected bd-a3f8e9.1.1.1, got %s", deep1.ID)
	}

	// Fourth level must fail and write nothing
	before, err := store.GetIssue(ctx, "bd-a3f8e9.1.1.1.1")
	if err != nil {
		t.Fatalf("failed to check for deep child: %v", err)
	}
	if before != nil {
		t.Fatalf("deep child already exists before the failing create")
	}
	tooDeep := newChildIssue("Too Deep")
	err = store.CreateChildIssue(ctx, deep1.ID, tooDeep, "test")
	if err == nil {
		t.Errorf("expected error for depth 4, got nil")
	}
	if err != nil && err.Error() != "maximum hierarchy depth (3) exceeded for parent bd-a3f8e9.1.1.1" {
		t.Errorf("unexpected error message: %v", err)
	}
	if after, _ := store.GetIssue(ctx, "bd-a3f8e9.1.1.1.1"); after != nil {
		t.Errorf("failed create left a row behind")
	}
}

func TestCreateChildIssue_ParentNotExists(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	child := newChildIssue("Orphan")
	err := store.CreateChildIssue(ctx, "bd-nonexistent", child, "test")
	if err == nil {
		t.Errorf("expected error for non-existent parent, got nil")
	}
	// With resurrection feature (bd-dvd fix), error message includes JSONL history check
	expectedErr := "parent issue bd-nonexistent does not exist and could not be resurrected from JSONL history"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("unexpected error message: got %q, want %q", err.Error(), expectedErr)
	}
}

func TestCreateIssue_HierarchicalID(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// Create parent
	parent := &types.Issue{
		ID:          "bd-parent1",
		Title:       "Parent",
		Description: "Parent issue",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := store.CreateIssue(ctx, parent, "test"); err != nil {
		t.Fatalf("failed to create parent: %v", err)
	}

	// Test: Create child with explicit hierarchical ID
	child := &types.Issue{
		ID:          "bd-parent1.1",
		Title:       "Child",
		Description: "Child issue",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}
	if err := store.CreateIssue(ctx, child, "test"); err != nil {
		t.Fatalf("failed to create child: %v", err)
	}

	// Verify child was created
	retrieved, err := store.GetIssue(ctx, child.ID)
	if err != nil {
		t.Fatalf("failed to retrieve child: %v", err)
	}
	if retrieved.ID != child.ID {
		t.Errorf("expected ID %s, got %s", child.ID, retrieved.ID)
	}
}

func TestCreateIssue_HierarchicalID_ParentNotExists(t *testing.T) {
	tmpFile := t.TempDir() + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// Test: Attempt to create child without parent
	child := &types.Issue{
		ID:          "bd-nonexistent.1",
		Title:       "Child",
		Description: "Child issue",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}
	err := store.CreateIssue(ctx, child, "test")
	if err == nil {
		t.Errorf("expected error for child without parent, got nil")
	}
	// With resurrection feature, error message includes JSONL history check
	expectedErr := "parent issue bd-nonexistent does not exist and could not be resurrected from JSONL history"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("unexpected error message: got %q, want %q", err.Error(), expectedErr)
	}
}

func TestCreateChildIssue_ResurrectParent(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// Create parent issue
	parent := &types.Issue{
		ID:          "bd-test123",
		ContentHash: "abc123",
		Title:       "Parent Issue",
		Description: "Parent to be resurrected",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := store.CreateIssue(ctx, parent, "test"); err != nil {
		t.Fatalf("failed to create parent: %v", err)
	}

	// Delete the parent from database (simulating deletion)
	if err := store.DeleteIssue(ctx, parent.ID); err != nil {
		t.Fatalf("failed to delete parent: %v", err)
	}

	// Create JSONL file with the deleted parent (simulating JSONL history)
	// Note: This requires the JSONL to be in .beads/issues.jsonl relative to dbPath
	// The resurrection logic looks for issues.jsonl in the same directory as the database
	beadsDir := tmpDir
	jsonlPath := beadsDir + "/issues.jsonl"

	// Write parent to JSONL
	jsonlFile, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatalf("failed to create JSONL file: %v", err)
	}
	parentJSON := `{"id":"bd-test123","content_hash":"abc123","title":"Parent Issue","description":"Parent to be resurrected","status":"open","priority":1,"type":"epic","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`
	if _, err := jsonlFile.WriteString(parentJSON + "\n"); err != nil {
		jsonlFile.Close()
		t.Fatalf("failed to write to JSONL: %v", err)
	}
	jsonlFile.Close()

	// Creating a child now resurrects the parent inside the same transaction
	child := newChildIssue("Resurrected Child")
	if err := store.CreateChildIssue(ctx, parent.ID, child, "test"); err != nil {
		t.Fatalf("CreateChildIssue should have resurrected parent, but got error: %v", err)
	}

	if child.ID != "bd-test123.1" {
		t.Errorf("expected child ID bd-test123.1, got %s", child.ID)
	}

	// Verify parent was resurrected as tombstone
	resurrectedParent, err := store.GetIssue(ctx, parent.ID)
	if err != nil {
		t.Fatalf("failed to get resurrected parent: %v", err)
	}
	if resurrectedParent.Status != types.StatusClosed {
		t.Errorf("expected resurrected parent to be closed, got %s", resurrectedParent.Status)
	}
	if resurrectedParent.Title != "Parent Issue" {
		t.Errorf("expected resurrected parent title to be preserved, got %s", resurrectedParent.Title)
	}
}

// TestCreateChildIssue_ResurrectParent_NotInJSONL tests resurrection when parent doesn't exist in JSONL (bd-ar2.7)
func TestCreateChildIssue_ResurrectParent_NotInJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// Create empty JSONL file (parent not in history)
	jsonlPath := tmpDir + "/issues.jsonl"
	if err := os.WriteFile(jsonlPath, []byte(""), 0600); err != nil {
		t.Fatalf("failed to create JSONL file: %v", err)
	}

	child := newChildIssue("Orphan")
	err := store.CreateChildIssue(ctx, "bd-notfound", child, "test")
	if err == nil {
		t.Errorf("expected error for parent not in JSONL, got nil")
	}
	expectedErr := "parent issue bd-notfound does not exist and could not be resurrected from JSONL history"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("unexpected error: got %q, want %q", err.Error(), expectedErr)
	}
}

// TestCreateChildIssue_ResurrectParent_NoJSONL tests resurrection when JSONL file doesn't exist (bd-ar2.7)
func TestCreateChildIssue_ResurrectParent_NoJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// No JSONL file created
	child := newChildIssue("Orphan")
	err := store.CreateChildIssue(ctx, "bd-missing", child, "test")
	if err == nil {
		t.Errorf("expected error for parent with no JSONL, got nil")
	}
	expectedErr := "parent issue bd-missing does not exist and could not be resurrected from JSONL history"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("unexpected error: got %q, want %q", err.Error(), expectedErr)
	}
}

// TestCreateChildIssue_ResurrectParent_MalformedJSONL tests resurrection with invalid JSON lines (bd-ar2.7)
func TestCreateChildIssue_ResurrectParent_MalformedJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// Create JSONL with malformed lines and one valid parent
	jsonlPath := tmpDir + "/issues.jsonl"
	jsonlContent := `{invalid json
{"id":"bd-test456","content_hash":"def456","title":"Valid Parent","description":"Should be found","status":"open","priority":1,"type":"epic","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}
this is not json either
`
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0600); err != nil {
		t.Fatalf("failed to create JSONL file: %v", err)
	}

	// Should successfully resurrect despite malformed lines
	child := newChildIssue("Resurrected Child")
	if err := store.CreateChildIssue(ctx, "bd-test456", child, "test"); err != nil {
		t.Fatalf("CreateChildIssue should skip malformed lines and resurrect valid parent, got error: %v", err)
	}

	if child.ID != "bd-test456.1" {
		t.Errorf("expected child ID bd-test456.1, got %s", child.ID)
	}
}

// TestCreateChildIssue_ResurrectParentChain tests resurrection of deeply nested missing parents (bd-ar2.7)
func TestCreateChildIssue_ResurrectParentChain(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.db"
	defer os.Remove(tmpFile)
	store := newTestStore(t, tmpFile)
	defer store.Close()
	ctx := context.Background()

	// Create root parent only
	root := &types.Issue{
		ID:          "bd-root",
		ContentHash: "root123",
		Title:       "Root Issue",
		Description: "Root",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	if err := store.CreateIssue(ctx, root, "test"); err != nil {
		t.Fatalf("failed to create root: %v", err)
	}

	// Create JSONL with intermediate parents that are deleted
	jsonlPath := tmpDir + "/issues.jsonl"
	jsonlContent := `{"id":"bd-root","content_hash":"root123","title":"Root Issue","description":"Root","status":"open","priority":1,"type":"epic","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}
{"id":"bd-root.1","content_hash":"l1abc","title":"Level 1","description":"First level","status":"open","priority":1,"type":"task","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}
{"id":"bd-root.1.2","content_hash":"l2abc","title":"Level 2","description":"Second level","status":"open","priority":1,"type":"task","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}
`
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0600); err != nil {
		t.Fatalf("failed to create JSONL file: %v", err)
	}

	// Create a child of bd-root.1.2 (which doesn't exist in DB, but its parent bd-root.1 also doesn't exist)
	// With TryResurrectParentChain (bd-ar2.4), this should work
	child := newChildIssue("Deep Child")
	if err := store.CreateChildIssue(ctx, "bd-root.1.2", child, "test"); err != nil {
		t.Fatalf("CreateChildIssue should resurrect entire parent chain, got error: %v", err)
	}

	if child.ID != "bd-root.1.2.1" {
		t.Errorf("expected child ID bd-root.1.2.1, got %s", child.ID)
	}

	// Verify both intermediate parents were resurrected
	parent1, err := store.GetIssue(ctx, "bd-root.1")
	if err != nil {
		t.Fatalf("bd-root.1 should have been resurrected: %v", err)
	}
	if parent1.Status != types.StatusClosed {
		t.Errorf("expected resurrected parent to be closed, got %s", parent1.Status)
	}

	parent2, err := store.GetIssue(ctx, "bd-root.1.2")
	if err != nil {
		t.Fatalf("bd-root.1.2 should have been resurrected: %v", err)
	}
	if parent2.Status != types.StatusClosed {
		t.Errorf("expected resurrected parent to be closed, got %s", parent2.Status)
	}
}
