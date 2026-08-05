package autoimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/jsonlpub"
	"github.com/steveyegge/beads/internal/storage/memory"
)

// symlinkedRepo builds the NixOS-style layout: .beads/issues.jsonl is a symlink
// to a file living somewhere else. It returns the database path CheckStaleness
// is called with and the content behind the link.
func symlinkedRepo(t *testing.T, content string) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()

	targetDir := filepath.Join(tmpDir, "target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(targetDir, "issues.jsonl")
	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// An old target mtime, so any check that reads clocks reads this one.
	oldTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(targetPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, filepath.Join(beadsDir, "issues.jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	return filepath.Join(beadsDir, "beads.db"), content
}

// TestCheckStaleness_SymlinkedJSONL_RecordedContentIsFresh is the case that
// made symlinks special: home-manager recreates the link, so the link's mtime
// is always newer than the last import. The content behind it never changed,
// so the database is not stale.
func TestCheckStaleness_SymlinkedJSONL_RecordedContentIsFresh(t *testing.T) {
	dbPath, content := symlinkedRepo(t, `{"id":"test-1"}`)

	store := memory.New("")
	ctx := context.Background()
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte(content))); err != nil {
		t.Fatal(err)
	}
	// An import timestamp far in the past: no clock comparison may resurrect
	// staleness here.
	if err := store.SetMetadata(ctx, "last_import_time", time.Now().Add(-2*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if stale {
		t.Error("stale = true for a recreated symlink to recorded content, want false")
	}
}

// TestCheckStaleness_SymlinkedJSONL_UnrecordedContentIsStale keeps the
// predicate useful through a link: content nobody imported is still stale, even
// though the link's target is older than the last import.
func TestCheckStaleness_SymlinkedJSONL_UnrecordedContentIsStale(t *testing.T) {
	dbPath, _ := symlinkedRepo(t, `{"id":"test-1"}`)

	store := memory.New("")
	ctx := context.Background()
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte(`{"id":"something-else"}`))); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadata(ctx, "last_import_time", time.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if !stale {
		t.Error("stale = false for unrecorded content behind a symlink, want true")
	}
}
