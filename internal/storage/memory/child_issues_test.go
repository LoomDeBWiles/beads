package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// bd --no-db reaches CreateChildIssue and no other test covers it: allocation
// runs above children loaded from JSONL, and a duplicate ID fails.
func TestCreateChildIssue_AllocatesAboveLoadedChildren(t *testing.T) {
	store := New("")
	ctx := context.Background()

	issues := []*types.Issue{
		{ID: "bd-parent", Title: "Parent", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeEpic},
		{ID: "bd-parent.1", Title: "Child 1", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask},
		{ID: "bd-parent.3", Title: "Child 3", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask},
	}
	if err := store.LoadFromIssues(issues); err != nil {
		t.Fatalf("LoadFromIssues failed: %v", err)
	}

	child := &types.Issue{Title: "Child 4", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	if err := store.CreateChildIssue(ctx, "bd-parent", child, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}
	if child.ID != "bd-parent.4" {
		t.Fatalf("CreateChildIssue ID = %q, want %q", child.ID, "bd-parent.4")
	}
	if got, err := store.GetIssue(ctx, "bd-parent.4"); err != nil || got == nil {
		t.Fatalf("child row missing after CreateChildIssue: %v", err)
	}
}

func TestCreateChildIssue_RejectsDuplicateID(t *testing.T) {
	store := New("")
	ctx := context.Background()

	parent := &types.Issue{ID: "bd-parent", Title: "Parent", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeEpic}
	if err := store.CreateIssue(ctx, parent, "test"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	first := &types.Issue{ID: "bd-parent.1", Title: "First", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	if err := store.CreateChildIssue(ctx, "bd-parent", first, "test"); err != nil {
		t.Fatalf("CreateChildIssue failed: %v", err)
	}

	dup := &types.Issue{ID: "bd-parent.1", Title: "Duplicate", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	err := store.CreateChildIssue(ctx, "bd-parent", dup, "test")
	if err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
	if !strings.Contains(err.Error(), "bd-parent.1") || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error must name the ID and say it exists, got: %v", err)
	}

	kept, getErr := store.GetIssue(ctx, "bd-parent.1")
	if getErr != nil || kept == nil || kept.Title != "First" {
		t.Fatalf("duplicate create disturbed the old row: %+v, err=%v", kept, getErr)
	}
}

func TestCreateChildIssue_ExplicitIDParentMismatch(t *testing.T) {
	store := New("")
	ctx := context.Background()

	for _, id := range []string{"bd-a", "bd-a.1"} {
		issueType := types.TypeTask
		if id == "bd-a" {
			issueType = types.TypeEpic
		}
		if err := store.CreateIssue(ctx, &types.Issue{
			ID: id, Title: id, Status: types.StatusOpen, Priority: 1, IssueType: issueType,
		}, "test"); err != nil {
			t.Fatalf("CreateIssue(%s) failed: %v", id, err)
		}
	}

	wrong := &types.Issue{ID: "bd-a.10", Title: "Wrong", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	if err := store.CreateChildIssue(ctx, "bd-a.1", wrong, "test"); err == nil {
		t.Fatal("expected error for mismatched parent, got nil")
	} else if !strings.Contains(err.Error(), "is not a child of") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := store.GetIssue(ctx, "bd-a.10"); got != nil {
		t.Errorf("mismatched create left a row behind")
	}
}
