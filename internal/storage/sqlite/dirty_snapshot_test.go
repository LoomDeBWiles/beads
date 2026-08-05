package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/jsonlpub"
	"github.com/steveyegge/beads/internal/types"
)

func createDirtyIssue(t *testing.T, store *SQLiteStorage, title string) string {
	t.Helper()
	issue := &types.Issue{
		Title:     title,
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	if err := store.CreateIssue(context.Background(), issue, "test-user"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	return issue.ID
}

func snapshotByID(snapshots []jsonlpub.DirtySnapshot, id string) (jsonlpub.DirtySnapshot, bool) {
	for _, snapshot := range snapshots {
		if snapshot.ID == id {
			return snapshot, true
		}
	}
	return jsonlpub.DirtySnapshot{}, false
}

// TestGetDirtyIssueSnapshotsRoundTrip covers the ordinary export cycle: what the
// snapshot reports is exactly what the conditional clear retires.
func TestGetDirtyIssueSnapshotsRoundTrip(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	first := createDirtyIssue(t, store, "first issue")
	second := createDirtyIssue(t, store, "second issue")

	snapshots, err := store.GetDirtyIssueSnapshots(ctx)
	if err != nil {
		t.Fatalf("GetDirtyIssueSnapshots failed: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(snapshots))
	}
	for _, id := range []string{first, second} {
		snapshot, ok := snapshotByID(snapshots, id)
		if !ok {
			t.Fatalf("issue %s missing from the snapshot", id)
		}
		if snapshot.MarkedAt.IsZero() {
			t.Errorf("issue %s has a zero mark", id)
		}
	}

	if err := store.ClearDirtyIssuesIfUnchanged(ctx, snapshots); err != nil {
		t.Fatalf("ClearDirtyIssuesIfUnchanged failed: %v", err)
	}

	remaining, err := store.GetDirtyIssues(ctx)
	if err != nil {
		t.Fatalf("GetDirtyIssues failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("dirty issues after clear = %v, want none", remaining)
	}
}

// TestClearDirtyIssuesIfUnchangedKeepsRemarkedIssue is the reason the clear is
// conditional: an issue edited while the export was running keeps a newer mark,
// so its row must survive for the next export.
func TestClearDirtyIssuesIfUnchangedKeepsRemarkedIssue(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	exported := createDirtyIssue(t, store, "exported issue")
	remarked := createDirtyIssue(t, store, "edited mid-export issue")

	snapshots, err := store.GetDirtyIssueSnapshots(ctx)
	if err != nil {
		t.Fatalf("GetDirtyIssueSnapshots failed: %v", err)
	}

	// The mutation the export is racing.
	time.Sleep(2 * time.Millisecond)
	if err := store.MarkIssueDirty(ctx, remarked); err != nil {
		t.Fatalf("MarkIssueDirty failed: %v", err)
	}

	if err := store.ClearDirtyIssuesIfUnchanged(ctx, snapshots); err != nil {
		t.Fatalf("ClearDirtyIssuesIfUnchanged failed: %v", err)
	}

	remaining, err := store.GetDirtyIssues(ctx)
	if err != nil {
		t.Fatalf("GetDirtyIssues failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0] != remarked {
		t.Fatalf("remaining dirty issues = %v, want only %s", remaining, remarked)
	}
	if _, ok := snapshotByID(snapshots, exported); !ok {
		t.Fatalf("setup: %s should have been in the snapshot", exported)
	}
}

// TestClearDirtyIssuesIfUnchangedIgnoresStaleSnapshot covers the crashed-export
// case: a snapshot from a run that never finished must not retire markers taken
// since.
func TestClearDirtyIssuesIfUnchangedIgnoresStaleSnapshot(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issueID := createDirtyIssue(t, store, "issue")
	stale := []jsonlpub.DirtySnapshot{{ID: issueID, MarkedAt: time.Now().Add(-time.Hour)}}

	if err := store.ClearDirtyIssuesIfUnchanged(ctx, stale); err != nil {
		t.Fatalf("ClearDirtyIssuesIfUnchanged failed: %v", err)
	}

	remaining, err := store.GetDirtyIssues(ctx)
	if err != nil {
		t.Fatalf("GetDirtyIssues failed: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("dirty issues = %v, want the issue still dirty", remaining)
	}
}

// TestClearDirtyIssuesIfUnchangedEmpty keeps the no-dirty-issues export from
// opening a transaction for nothing.
func TestClearDirtyIssuesIfUnchangedEmpty(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	if err := store.ClearDirtyIssuesIfUnchanged(context.Background(), nil); err != nil {
		t.Fatalf("ClearDirtyIssuesIfUnchanged failed: %v", err)
	}
}

// TestPublishClearsDirtyIssuesEndToEnd runs the real publisher against the real
// store: the loop that re-exported the same issues every second ends with the
// markers retired.
func TestPublishClearsDirtyIssuesEndToEnd(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	createDirtyIssue(t, store, "first issue")
	createDirtyIssue(t, store, "second issue")

	jsonlPath := t.TempDir() + "/issues.jsonl"
	build := func(ctx context.Context) ([]*types.Issue, error) {
		return store.SearchIssues(ctx, "", types.IssueFilter{IncludeTombstones: true})
	}

	result, err := jsonlpub.Publish(ctx, store, jsonlPath, build, jsonlpub.Options{})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if result.IssueCount != 2 {
		t.Errorf("published issue count = %d, want 2", result.IssueCount)
	}

	remaining, err := store.GetDirtyIssues(ctx)
	if err != nil {
		t.Fatalf("GetDirtyIssues failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("dirty issues after publish = %v, want none", remaining)
	}

	status, err := jsonlpub.ContentState(ctx, store, jsonlPath, "")
	if err != nil {
		t.Fatalf("ContentState failed: %v", err)
	}
	if status != jsonlpub.StatusFresh {
		t.Errorf("ContentState = %s, want fresh", status)
	}
}
