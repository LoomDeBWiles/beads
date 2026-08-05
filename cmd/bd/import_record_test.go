//go:build integration
// +build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/jsonlpub"
	"github.com/steveyegge/beads/internal/storage/sqlite"
)

// openWorkspaceStore opens the database the CLI just wrote, so a test can read
// the metadata the command recorded.
func openWorkspaceStore(t *testing.T, dir string) *sqlite.SQLiteStorage {
	t.Helper()
	store, err := sqlite.New(context.Background(), filepath.Join(dir, ".beads", "beads.db"))
	if err != nil {
		t.Fatalf("failed to open workspace store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestImportCanonicalRecordsContent covers the CLI import caller: importing the
// repository's own JSONL records what was imported, so the post-import flush -
// which publishes the database straight back to that same file - does not read
// the file as somebody else's writing and abort. The repository must be fresh
// afterwards, never stale.
func TestImportCanonicalRecordsContent(t *testing.T) {
	dir := setupCLITestDB(t)
	canonical := filepath.Join(dir, ".beads", "issues.jsonl")

	content := `{"id":"test-imported","title":"From canonical JSONL","status":"open","priority":1,"issue_type":"task","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(canonical, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write canonical JSONL: %v", err)
	}

	runBDInProcess(t, dir, "import", "-i", canonical)

	store := openWorkspaceStore(t, dir)
	ctx := context.Background()

	if _, err := store.GetIssue(ctx, "test-imported"); err != nil {
		t.Fatalf("expected the canonical import to load the issue: %v", err)
	}

	state, err := jsonlpub.ContentState(ctx, store, canonical, "")
	if err != nil {
		t.Fatalf("failed to read content state: %v", err)
	}
	if state != jsonlpub.StatusFresh {
		t.Errorf("content state after a canonical import is %s, want fresh", state)
	}
}

// TestImportBackupLeavesCanonicalMetadata pins the canonical-path rule (R3-5):
// `bd import -i backup.jsonl` loads a different file's content, and claiming
// that content as the repository's would make the real JSONL read diverged for
// every later reader.
func TestImportBackupLeavesCanonicalMetadata(t *testing.T) {
	dir := setupCLITestDB(t)
	canonical := filepath.Join(dir, ".beads", "issues.jsonl")

	// The trailing blank line makes these bytes impossible for an export to
	// produce, so a hash matching them can only have come from recording this
	// file - not from the post-import flush publishing the same issue.
	backupContent := `{"id":"test-backup","title":"From a backup file","status":"open","priority":1,"issue_type":"task","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}` + "\n\n"
	backup := filepath.Join(dir, "backup.jsonl")
	if err := os.WriteFile(backup, []byte(backupContent), 0o644); err != nil {
		t.Fatalf("failed to write backup JSONL: %v", err)
	}

	runBDInProcess(t, dir, "import", "-i", backup)

	store := openWorkspaceStore(t, dir)
	ctx := context.Background()

	recorded, err := store.GetMetadata(ctx, "jsonl_content_hash")
	if err != nil {
		t.Fatalf("failed to read recorded hash: %v", err)
	}
	if recorded == jsonlpub.HashBytes([]byte(backupContent)) {
		t.Error("the backup file's content was recorded as the canonical JSONL's")
	}

	state, err := jsonlpub.ContentState(ctx, store, canonical, "")
	if err != nil {
		t.Fatalf("failed to read content state: %v", err)
	}
	if state != jsonlpub.StatusFresh {
		t.Errorf("canonical JSONL reads %s after a backup import, want fresh", state)
	}
}
