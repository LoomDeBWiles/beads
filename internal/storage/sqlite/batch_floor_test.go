package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/types"
)

// A batch holding p.5 and p.3 leaves the floor at the maximum, 5: passing any
// other member would under-raise the floor and reopen the hole for the
// higher-numbered child. Deleting .5 outright must still not reissue it.
func TestBatchFloor_RaisesToMaximum(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createParent(t, store, ctx, "bd-batch")
	batch := []*types.Issue{
		{ID: "bd-batch.5", Title: "Child 5", Description: "x", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "bd-batch.3", Title: "Child 3", Description: "x", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	}
	if err := store.CreateIssues(ctx, batch, "test"); err != nil {
		t.Fatalf("CreateIssues failed: %v", err)
	}

	floor, ok := floorOf(t, store, ctx, "bd-batch")
	if !ok {
		t.Fatal("batch insert wrote no floor")
	}
	if floor != 5 {
		t.Fatalf("expected floor 5 (the batch maximum), got %d", floor)
	}

	if _, err := store.db.ExecContext(ctx, `DELETE FROM issues WHERE id = 'bd-batch.5'`); err != nil {
		t.Fatalf("failed to delete row outright: %v", err)
	}

	next := &types.Issue{
		Title: "After delete", Description: "x",
		Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
	}
	if err := store.CreateChildIssue(ctx, "bd-batch", next, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if next.ID != "bd-batch.6" {
		t.Fatalf("expected bd-batch.6, got %s", next.ID)
	}
}

// Inserting a parent that already has children seeds its own floor from them:
// the orphan-first ordering, where the child landed before the parent and
// raised no floor because the guarded statement skips an absent parent.
func TestBatchFloor_SeedsParentFromExistingChildren(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	orphan := &types.Issue{
		ID: "bd-seedparent.4", Title: "Early child", Description: "x",
		Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
	}
	if err := store.CreateIssuesWithOptions(ctx, []*types.Issue{orphan}, "test", OrphanAllow); err != nil {
		t.Fatalf("failed to import orphan child: %v", err)
	}
	if _, ok := floorOf(t, store, ctx, "bd-seedparent"); ok {
		t.Fatal("orphan child must not write a floor for its absent parent")
	}

	createParent(t, store, ctx, "bd-seedparent")
	floor, ok := floorOf(t, store, ctx, "bd-seedparent")
	if !ok {
		t.Fatal("parent insert seeded no floor from its existing child")
	}
	if floor != 4 {
		t.Fatalf("expected seeded floor 4, got %d", floor)
	}
}

// Hydration writes issue rows outside both insert helpers, so it maintains
// the floor itself: hydrate a repo, delete the highest child's row outright,
// and its number must not be reissued.
func TestBatchFloor_HydrationMaintainsFloor(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	if err := config.Initialize(); err != nil {
		t.Fatalf("failed to initialize config: %v", err)
	}

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("failed to create .beads dir: %v", err)
	}

	writeIssue := func(id, title string) types.Issue {
		return types.Issue{
			ID: id, Title: title, Description: "hydrated",
			Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	issues := []types.Issue{
		func() types.Issue {
			i := writeIssue("bd-hyd", "Hydrated epic")
			i.IssueType = types.TypeEpic
			i.Priority = 1
			return i
		}(),
		writeIssue("bd-hyd.1", "Hydrated child 1"),
		writeIssue("bd-hyd.2", "Hydrated child 2"),
		writeIssue("bd-hyd.3", "Hydrated child 3"),
	}
	f, err := os.Create(filepath.Join(beadsDir, "issues.jsonl"))
	if err != nil {
		t.Fatalf("failed to create JSONL file: %v", err)
	}
	enc := json.NewEncoder(f)
	for i := range issues {
		issues[i].ContentHash = issues[i].ComputeContentHash()
		if err := enc.Encode(issues[i]); err != nil {
			f.Close()
			t.Fatalf("failed to write issue: %v", err)
		}
	}
	f.Close()

	config.Set("repos.primary", tmpDir)
	defer config.Set("repos.primary", "")

	ctx := context.Background()
	if _, err := store.HydrateFromMultiRepo(ctx); err != nil {
		t.Fatalf("HydrateFromMultiRepo failed: %v", err)
	}

	floor, ok := floorOf(t, store, ctx, "bd-hyd")
	if !ok {
		t.Fatal("hydration wrote no floor")
	}
	if floor != 3 {
		t.Fatalf("expected floor 3 after hydration, got %d", floor)
	}

	if _, err := store.db.ExecContext(ctx, `DELETE FROM issues WHERE id = 'bd-hyd.3'`); err != nil {
		t.Fatalf("failed to delete row outright: %v", err)
	}

	next := &types.Issue{
		Title: "After hydration", Description: "x",
		Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
	}
	if err := store.CreateChildIssue(ctx, "bd-hyd", next, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if next.ID == "bd-hyd.3" {
		t.Fatal("reissued the hydrated number bd-hyd.3")
	}
	if next.ID != "bd-hyd.4" {
		t.Fatalf("expected bd-hyd.4, got %s", next.ID)
	}
}
