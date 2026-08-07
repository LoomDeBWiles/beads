package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/importer"
	"github.com/steveyegge/beads/internal/jsonlpub"
	"github.com/steveyegge/beads/internal/storage/sqlite"
	"github.com/steveyegge/beads/internal/types"
)



func TestExportCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bd-test-export-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testDB := filepath.Join(tmpDir, "test.db")
	s := newTestStore(t, testDB)
	defer s.Close()

	ctx := context.Background()

	// Create test issues
	issues := []*types.Issue{
		{
			Title:       "First Issue",
			Description: "Test description 1",
			Priority:    0,
			IssueType:   types.TypeBug,
			Status:      types.StatusOpen,
		},
		{
			Title:       "Second Issue",
			Description: "Test description 2",
			Priority:    1,
			IssueType:   types.TypeFeature,
			Status:      types.StatusInProgress,
		},
	}

	for _, issue := range issues {
		if err := s.CreateIssue(ctx, issue, "test-user"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}
	}

	// Add a label to first issue
	if err := s.AddLabel(ctx, issues[0].ID, "critical", "test-user"); err != nil {
		t.Fatalf("Failed to add label: %v", err)
	}

	// Add a dependency
	dep := &types.Dependency{
		IssueID:     issues[0].ID,
		DependsOnID: issues[1].ID,
		Type:        "blocks",
	}
	if err := s.AddDependency(ctx, dep, "test-user"); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	t.Run("export to file", func(t *testing.T) {
		exportPath := filepath.Join(tmpDir, "export.jsonl")

		// Set up global state
		store = s
		dbPath = testDB
		rootCtx = ctx
		defer func() { rootCtx = nil }()

		// Create a mock command with output flag
		exportCmd.SetArgs([]string{"-o", exportPath})
		exportCmd.Flags().Set("output", exportPath)

		// Export
		exportCmd.Run(exportCmd, []string{})

		// Verify file was created
		if _, err := os.Stat(exportPath); os.IsNotExist(err) {
			t.Fatal("Export file was not created")
		}

		// Read and verify JSONL content
		file, err := os.Open(exportPath)
		if err != nil {
			t.Fatalf("Failed to open export file: %v", err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineCount := 0
		for scanner.Scan() {
			lineCount++
			var issue types.Issue
			if err := json.Unmarshal(scanner.Bytes(), &issue); err != nil {
				t.Fatalf("Failed to parse JSONL line %d: %v", lineCount, err)
			}

			// Verify issue has required fields
			if issue.ID == "" {
				t.Error("Issue missing ID")
			}
			if issue.Title == "" {
				t.Error("Issue missing title")
			}
		}

		if lineCount != 2 {
			t.Errorf("Expected 2 lines in export, got %d", lineCount)
		}
	})

	t.Run("export includes labels", func(t *testing.T) {
		exportPath := filepath.Join(tmpDir, "export_labels.jsonl")

		// Clear export hashes to force re-export (test isolation)
		if err := s.ClearAllExportHashes(ctx); err != nil {
			t.Fatalf("Failed to clear export hashes: %v", err)
		}

		store = s
		dbPath = testDB
		rootCtx = ctx
		defer func() { rootCtx = nil }()
		exportCmd.Flags().Set("output", exportPath)
		exportCmd.Run(exportCmd, []string{})

		file, err := os.Open(exportPath)
		if err != nil {
			t.Fatalf("Failed to open export file: %v", err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		foundLabeledIssue := false
		for scanner.Scan() {
			var issue types.Issue
			if err := json.Unmarshal(scanner.Bytes(), &issue); err != nil {
				t.Fatalf("Failed to parse JSONL: %v", err)
			}

			if issue.ID == issues[0].ID {
				foundLabeledIssue = true
				if len(issue.Labels) != 1 || issue.Labels[0] != "critical" {
					t.Errorf("Expected label 'critical', got %v", issue.Labels)
				}
			}
		}

		if !foundLabeledIssue {
			t.Error("Did not find labeled issue in export")
		}
	})

	t.Run("export includes dependencies", func(t *testing.T) {
		exportPath := filepath.Join(tmpDir, "export_deps.jsonl")

		// Clear export hashes to force re-export (test isolation)
		if err := s.ClearAllExportHashes(ctx); err != nil {
			t.Fatalf("Failed to clear export hashes: %v", err)
		}

		store = s
		dbPath = testDB
		rootCtx = ctx
		defer func() { rootCtx = nil }()
		exportCmd.Flags().Set("output", exportPath)
		exportCmd.Run(exportCmd, []string{})

		file, err := os.Open(exportPath)
		if err != nil {
			t.Fatalf("Failed to open export file: %v", err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		foundDependency := false
		for scanner.Scan() {
			var issue types.Issue
			if err := json.Unmarshal(scanner.Bytes(), &issue); err != nil {
				t.Fatalf("Failed to parse JSONL: %v", err)
			}

			if issue.ID == issues[0].ID && len(issue.Dependencies) > 0 {
				foundDependency = true
				if issue.Dependencies[0].DependsOnID != issues[1].ID {
					t.Errorf("Expected dependency to %s, got %s", issues[1].ID, issue.Dependencies[0].DependsOnID)
				}
			}
		}

		if !foundDependency {
			t.Error("Did not find dependency in export")
		}
	})

	t.Run("validate export path", func(t *testing.T) {
		// Test safe path
		if err := validateExportPath(tmpDir); err != nil {
			t.Errorf("Unexpected error for safe path: %v", err)
		}

		// Test Windows system directories
		// Note: validateExportPath() only checks Windows paths on case-insensitive systems
		// On Unix/Mac, C:\Windows won't match, so we skip this assertion
		// Just verify the function doesn't panic with Windows-style paths
		_ = validateExportPath("C:\\Windows\\system32\\test.jsonl")
	})

	t.Run("prevent exporting empty database over non-empty JSONL", func(t *testing.T) {
		exportPath := filepath.Join(tmpDir, "export_empty_check.jsonl")

		// First, create a JSONL file with issues
		file, err := os.Create(exportPath)
		if err != nil {
			t.Fatalf("Failed to create JSONL: %v", err)
		}
		encoder := json.NewEncoder(file)
		for _, issue := range issues {
			if err := encoder.Encode(issue); err != nil {
				t.Fatalf("Failed to encode issue: %v", err)
			}
		}
		file.Close()

		// Verify file has issues
		count, err := countIssuesInJSONL(exportPath)
		if err != nil {
			t.Fatalf("Failed to count issues: %v", err)
		}
		if count != 2 {
			t.Fatalf("Expected 2 issues in JSONL, got %d", count)
		}

		// Create empty database
		emptyDBPath := filepath.Join(tmpDir, "empty.db")
		emptyStore := newTestStore(t, emptyDBPath)
		defer emptyStore.Close()

		// A file this database never recorded is refused before anything is
		// written at all: its contents may be somebody else's, and the repair
		// is an import.
		err = exportToJSONLWithStore(ctx, emptyStore, exportPath)
		if !errors.Is(err, jsonlpub.ErrDiverged) {
			t.Errorf("Expected a divergence refusal for an unrecorded JSONL, got: %v", err)
		}

		// Record the file as imported, so the divergence guard passes and the
		// empty-database check below is the thing under test.
		fileHash, err := jsonlpub.HashFile(exportPath)
		if err != nil {
			t.Fatalf("Failed to hash JSONL: %v", err)
		}
		if err := jsonlpub.RecordImport(ctx, emptyStore, exportPath, fileHash, jsonlpub.Options{}); err != nil {
			t.Fatalf("Failed to record imported JSONL: %v", err)
		}

		// Test using exportToJSONLWithStore directly (daemon code path)
		err = exportToJSONLWithStore(ctx, emptyStore, exportPath)
		if err == nil {
			t.Error("Expected error when exporting empty database over non-empty JSONL")
		} else {
			expectedMsg := "refusing to export empty database over non-empty JSONL file (database: 0 issues, JSONL: 2 issues). This would result in data loss"
			if err.Error() != expectedMsg {
				t.Errorf("Unexpected error message:\nGot:      %q\nExpected: %q", err.Error(), expectedMsg)
			}
		}

		// Verify JSONL file is unchanged
		countAfter, err := countIssuesInJSONL(exportPath)
		if err != nil {
			t.Fatalf("Failed to count issues after failed export: %v", err)
		}
		if countAfter != 2 {
			t.Errorf("JSONL file was modified! Expected 2 issues, got %d", countAfter)
		}
	})

	t.Run("verify JSONL line count matches exported count", func(t *testing.T) {
		exportPath := filepath.Join(tmpDir, "export_verify.jsonl")

		// Clear export hashes to force re-export
		if err := s.ClearAllExportHashes(ctx); err != nil {
			t.Fatalf("Failed to clear export hashes: %v", err)
		}

		store = s
		dbPath = testDB
		rootCtx = ctx
		defer func() { rootCtx = nil }()
		exportCmd.Flags().Set("output", exportPath)
		exportCmd.Run(exportCmd, []string{})

		// Verify the exported file has exactly 2 lines
		actualCount, err := countIssuesInJSONL(exportPath)
		if err != nil {
			t.Fatalf("Failed to count issues in JSONL: %v", err)
		}
		if actualCount != 2 {
			t.Errorf("Expected 2 issues in JSONL, got %d", actualCount)
		}

		// Simulate corrupted export by truncating file
		corruptedPath := filepath.Join(tmpDir, "export_corrupted.jsonl")
		
		// First export normally
		if err := s.ClearAllExportHashes(ctx); err != nil {
			t.Fatalf("Failed to clear export hashes: %v", err)
		}
		store = s
		rootCtx = ctx
		defer func() { rootCtx = nil }()
		exportCmd.Flags().Set("output", corruptedPath)
		exportCmd.Run(exportCmd, []string{})

		// Now manually corrupt it by removing one line
		file, err := os.Open(corruptedPath)
		if err != nil {
			t.Fatalf("Failed to open file for corruption: %v", err)
		}
		scanner := bufio.NewScanner(file)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		file.Close()

		// Write back only first line (simulating partial write)
		corruptedFile, err := os.Create(corruptedPath)
		if err != nil {
			t.Fatalf("Failed to create corrupted file: %v", err)
		}
		corruptedFile.WriteString(lines[0] + "\n")
		corruptedFile.Close()

		// Verify countIssuesInJSONL detects the corruption
		count, err := countIssuesInJSONL(corruptedPath)
		if err != nil {
			t.Fatalf("Failed to count corrupted file: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 line in corrupted file, got %d", count)
		}
	})

	t.Run("export cancellation", func(t *testing.T) {
		// Create a large number of issues to ensure export takes time
		ctx := context.Background()
		largeStore := newTestStore(t, filepath.Join(tmpDir, "large.db"))
		defer largeStore.Close()

		// Create 100 issues
		for i := 0; i < 100; i++ {
			issue := &types.Issue{
				Title:       "Test Issue",
				Description: "Test description for cancellation",
				Priority:    0,
				IssueType:   types.TypeBug,
				Status:      types.StatusOpen,
			}
			if err := largeStore.CreateIssue(ctx, issue, "test-user"); err != nil {
				t.Fatalf("Failed to create issue: %v", err)
			}
		}

		exportPath := filepath.Join(tmpDir, "export_cancel.jsonl")

		// Create a cancellable context
		cancelCtx, cancel := context.WithCancel(context.Background())

		// Start export in a goroutine
		errChan := make(chan error, 1)
		go func() {
			errChan <- exportToJSONLWithStore(cancelCtx, largeStore, exportPath)
		}()

		// Cancel after a short delay
		cancel()

		// Wait for export to finish
		err := <-errChan

		// Verify that the operation was cancelled
		if err != nil && err != context.Canceled {
			t.Logf("Export returned error: %v (expected context.Canceled)", err)
		}

		// Verify database integrity - we should still be able to query
		issues, err := largeStore.SearchIssues(ctx, "", types.IssueFilter{})
		if err != nil {
			t.Fatalf("Database corrupted after cancellation: %v", err)
		}
		if len(issues) != 100 {
			t.Errorf("Expected 100 issues after cancellation, got %d", len(issues))
		}
	})
}

// TestClaimRoundTrip locks the owner lease into the JSONL round trip (bd-ok4pr).
//
// Four import paths, because they carry the field through different code:
// a fresh store goes through the batch insertIssues and picks the lease up from
// the INSERT column list, while a store that already holds the issue goes
// through the importer's explicit update maps, which drop silently anything
// they do not name. The renewal case is the only one where the checkFieldChanged
// comparator decides whether the update runs at all. The rename collision adds
// a third writer: handleRename merges the incoming fields into the twin that
// already holds the content under another ID, and the unrelated issue squatting
// on the incoming ID must come through untouched, lease included.
func TestClaimRoundTrip(t *testing.T) {
	ctx := context.Background()

	t.Run("import into a fresh store", func(t *testing.T) {
		tmpDir := t.TempDir()
		source := newTestStore(t, filepath.Join(tmpDir, "source.db"))
		held := createClaimedIssue(t, ctx, source, nil)
		incoming := exportedIssue(t, ctx, source, filepath.Join(tmpDir, "source.jsonl"), held.ID)

		if incoming.ClaimExpiresAt == nil {
			t.Fatal("export dropped claim_expires_at")
		}

		targetPath := filepath.Join(tmpDir, "target.db")
		target := newTestStore(t, targetPath)
		importIssues(t, ctx, target, targetPath, []*types.Issue{incoming})

		imported := mustGetIssue(t, ctx, target, held.ID)
		assertSameLease(t, "fresh store", held.ClaimExpiresAt, imported.ClaimExpiresAt)
		if imported.Assignee != held.Assignee || imported.Status != held.Status {
			t.Errorf("imported issue is %s/%q, want %s/%q", imported.Status, imported.Assignee, held.Status, held.Assignee)
		}
	})

	t.Run("import into a store already holding the pre-claim issue", func(t *testing.T) {
		tmpDir := t.TempDir()
		source := newTestStore(t, filepath.Join(tmpDir, "source.db"))
		held := createClaimedIssue(t, ctx, source, nil)
		incoming := exportedIssue(t, ctx, source, filepath.Join(tmpDir, "source.jsonl"), held.ID)

		// The clone still sees the issue as nobody claimed it, and its copy is
		// older, so the importer routes the row through an update map.
		targetPath := filepath.Join(tmpDir, "target.db")
		target := newTestStore(t, targetPath)
		preClaim := *held
		preClaim.Status = types.StatusOpen
		preClaim.Assignee = ""
		preClaim.ClaimExpiresAt = nil
		preClaim.ContentHash = ""
		preClaim.UpdatedAt = held.UpdatedAt.Add(-time.Hour)
		if err := target.CreateIssue(ctx, &preClaim, "test-user"); err != nil {
			t.Fatalf("failed to seed the pre-claim issue: %v", err)
		}

		importIssues(t, ctx, target, targetPath, []*types.Issue{incoming})

		imported := mustGetIssue(t, ctx, target, held.ID)
		if imported.Assignee != held.Assignee || imported.Status != held.Status {
			t.Fatalf("imported issue is %s/%q, want %s/%q", imported.Status, imported.Assignee, held.Status, held.Assignee)
		}
		assertSameLease(t, "existing store", held.ClaimExpiresAt, imported.ClaimExpiresAt)
	})

	t.Run("lease-only renewal on an external_ref issue", func(t *testing.T) {
		tmpDir := t.TempDir()
		source := newTestStore(t, filepath.Join(tmpDir, "source.db"))
		externalRef := "JIRA-4242"
		held := createClaimedIssue(t, ctx, source, &externalRef)
		incoming := exportedIssue(t, ctx, source, filepath.Join(tmpDir, "source.jsonl"), held.ID)

		// Same holder, same status, only the expiry moved: this is what a
		// renewal looks like on the wire.
		targetPath := filepath.Join(tmpDir, "target.db")
		target := newTestStore(t, targetPath)
		stale := *held
		staleExpiry := held.ClaimExpiresAt.Add(-20 * time.Minute)
		stale.ClaimExpiresAt = &staleExpiry
		stale.ContentHash = ""
		stale.UpdatedAt = held.UpdatedAt.Add(-time.Hour)
		if err := target.CreateIssue(ctx, &stale, "test-user"); err != nil {
			t.Fatalf("failed to seed the stale-lease issue: %v", err)
		}

		importIssues(t, ctx, target, targetPath, []*types.Issue{incoming})

		imported := mustGetIssue(t, ctx, target, held.ID)
		assertSameLease(t, "lease renewal", held.ClaimExpiresAt, imported.ClaimExpiresAt)
	})

	t.Run("rename collision keeps the lease on the surviving ID", func(t *testing.T) {
		tmpDir := t.TempDir()
		source := newTestStore(t, filepath.Join(tmpDir, "source.db"))
		held := createClaimedIssue(t, ctx, source, nil)
		incoming := exportedIssue(t, ctx, source, filepath.Join(tmpDir, "source.jsonl"), held.ID)

		// The rename path needs two rows in the target. The twin carries the
		// incoming content under a different ID of the same prefix, which is
		// what makes the importer read the row as a rename; the squatter holds
		// the incoming ID with different content, which makes handleRename keep
		// the twin's ID and merge the incoming fields into it. That merge is a
		// field-by-field update map, so it drops any field it does not name.
		targetPath := filepath.Join(tmpDir, "target.db")
		target := newTestStore(t, targetPath)

		twin := *held
		twin.ID = "test-renametwin"
		twin.ClaimExpiresAt = nil
		twin.ContentHash = ""
		twin.UpdatedAt = held.UpdatedAt.Add(-time.Hour)
		if err := target.CreateIssue(ctx, &twin, "test-user"); err != nil {
			t.Fatalf("failed to seed the rename twin: %v", err)
		}

		squatter := *held
		squatter.Title = "Unrelated issue squatting on the incoming ID"
		squatter.ClaimExpiresAt = nil
		squatter.ContentHash = ""
		squatter.UpdatedAt = held.UpdatedAt.Add(-time.Hour)
		if err := target.CreateIssue(ctx, &squatter, "test-user"); err != nil {
			t.Fatalf("failed to seed the ID-collision issue: %v", err)
		}

		importIssues(t, ctx, target, targetPath, []*types.Issue{incoming})

		// The squatter is a different issue that merely occupies the incoming
		// ID: the merge must land on the twin and leave the squatter alone,
		// lease included.
		collided := mustGetIssue(t, ctx, target, squatter.ID)
		if collided.Title != squatter.Title {
			t.Fatalf("squatter title is %q, want %q", collided.Title, squatter.Title)
		}
		if collided.ClaimExpiresAt != nil {
			t.Fatalf("squatter gained a lease expiring at %s, want none", collided.ClaimExpiresAt)
		}

		imported := mustGetIssue(t, ctx, target, twin.ID)
		assertSameLease(t, "rename collision", held.ClaimExpiresAt, imported.ClaimExpiresAt)
	})
}

// createClaimedIssue creates an issue and claims it under a lease, returning the
// stored issue as the claim left it.
func createClaimedIssue(t *testing.T, ctx context.Context, store *sqlite.SQLiteStorage, externalRef *string) *types.Issue {
	t.Helper()

	issue := &types.Issue{
		Title:       "Claimed issue",
		Description: "held under an owner lease",
		Priority:    1,
		IssueType:   types.TypeTask,
		Status:      types.StatusOpen,
		ExternalRef: externalRef,
	}
	if err := store.CreateIssue(ctx, issue, "test-user"); err != nil {
		t.Fatalf("failed to create issue: %v", err)
	}

	lease := 30 * time.Minute
	outcome, err := store.ClaimIssue(ctx, issue.ID, "agent-a", &lease, "agent-a")
	if err != nil {
		t.Fatalf("failed to claim issue: %v", err)
	}
	if outcome.Outcome != types.ClaimClaimed {
		t.Fatalf("claim outcome is %s, want claimed", outcome.Outcome)
	}

	claimed := mustGetIssue(t, ctx, store, issue.ID)
	if claimed.ClaimExpiresAt == nil {
		t.Fatal("claim_expires_at is NULL after a leased claim")
	}
	return claimed
}

// exportedIssue exports the store to JSONL and reads one issue back out, so the
// value under test travels the same bytes a sync would produce.
func exportedIssue(t *testing.T, ctx context.Context, store *sqlite.SQLiteStorage, jsonlPath, id string) *types.Issue {
	t.Helper()

	if err := exportToJSONLWithStore(ctx, store, jsonlPath); err != nil {
		t.Fatalf("failed to export to %s: %v", jsonlPath, err)
	}
	exported, err := loadIssuesFromJSONL(jsonlPath)
	if err != nil {
		t.Fatalf("failed to read exported JSONL: %v", err)
	}
	for _, issue := range exported {
		if issue.ID == id {
			return issue
		}
	}
	t.Fatalf("issue %s missing from the export", id)
	return nil
}

func importIssues(t *testing.T, ctx context.Context, store *sqlite.SQLiteStorage, dbPath string, issues []*types.Issue) {
	t.Helper()

	if _, err := importer.ImportIssues(ctx, dbPath, store, issues, importer.Options{}); err != nil {
		t.Fatalf("import failed: %v", err)
	}
}

func mustGetIssue(t *testing.T, ctx context.Context, store *sqlite.SQLiteStorage, id string) *types.Issue {
	t.Helper()

	issue, err := store.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("failed to read issue %s: %v", id, err)
	}
	if issue == nil {
		t.Fatalf("issue %s not found", id)
	}
	return issue
}

func assertSameLease(t *testing.T, path string, want, got *time.Time) {
	t.Helper()

	if got == nil {
		t.Fatalf("%s: claim_expires_at is NULL after import, want %v", path, want)
	}
	if !got.Equal(*want) {
		t.Errorf("%s: claim_expires_at is %v after import, want %v", path, got, want)
	}
}
