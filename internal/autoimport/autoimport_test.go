package autoimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/jsonlpub"
	"github.com/steveyegge/beads/internal/storage/memory"
	"github.com/steveyegge/beads/internal/types"
)

// testNotifier captures notifications for assertions
type testNotifier struct {
	debugs []string
	infos  []string
	warns  []string
	errors []string
}

func (n *testNotifier) Debugf(format string, args ...interface{}) {
	n.debugs = append(n.debugs, format)
}

func (n *testNotifier) Infof(format string, args ...interface{}) {
	n.infos = append(n.infos, format)
}

func (n *testNotifier) Warnf(format string, args ...interface{}) {
	n.warns = append(n.warns, format)
}

func (n *testNotifier) Errorf(format string, args ...interface{}) {
	n.errors = append(n.errors, format)
}

func TestAutoImportIfNewer_NoJSONL(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bd-autoimport-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bd.db")
	store := memory.New("")
	notify := &testNotifier{}

	importCalled := false
	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		importCalled = true
		return 0, 0, nil, nil
	}

	err = AutoImportIfNewer(context.Background(), store, dbPath, notify, importFunc, nil)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if importCalled {
		t.Error("Import should not be called when JSONL doesn't exist")
	}
}

func TestAutoImportIfNewer_UnchangedHash(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bd-autoimport-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bd.db")
	jsonlPath := filepath.Join(tmpDir, "issues.jsonl")

	// Create test JSONL
	issue := &types.Issue{
		ID:        "test-1",
		Title:     "Test Issue",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(f).Encode(issue)
	f.Close()

	// Compute hash
	data, _ := os.ReadFile(jsonlPath)
	hasher := sha256.New()
	hasher.Write(data)
	hash := hex.EncodeToString(hasher.Sum(nil))

	// Store hash in metadata
	store := memory.New("")
	ctx := context.Background()
	store.SetMetadata(ctx, "last_import_hash", hash)

	notify := &testNotifier{}
	importCalled := false
	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		importCalled = true
		return 0, 0, nil, nil
	}

	err = AutoImportIfNewer(ctx, store, dbPath, notify, importFunc, nil)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if importCalled {
		t.Error("Import should not be called when hash is unchanged")
	}
}

func TestAutoImportIfNewer_ChangedHash(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bd-autoimport-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bd.db")
	jsonlPath := filepath.Join(tmpDir, "issues.jsonl")

	// Create test JSONL
	issue := &types.Issue{
		ID:        "test-1",
		Title:     "Test Issue",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(f).Encode(issue)
	f.Close()

	// Store different hash in metadata
	store := memory.New("")
	ctx := context.Background()
	store.SetMetadata(ctx, "last_import_hash", "different-hash")

	notify := &testNotifier{}
	importCalled := false
	var receivedIssues []*types.Issue
	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		importCalled = true
		receivedIssues = issues
		return 1, 0, nil, nil
	}

	err = AutoImportIfNewer(ctx, store, dbPath, notify, importFunc, nil)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !importCalled {
		t.Error("Import should be called when hash changed")
	}

	if len(receivedIssues) != 1 {
		t.Errorf("Expected 1 issue, got %d", len(receivedIssues))
	}

	if receivedIssues[0].ID != "test-1" {
		t.Errorf("Expected issue ID 'test-1', got '%s'", receivedIssues[0].ID)
	}
}

func TestAutoImportIfNewer_MergeConflict(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bd-autoimport-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bd.db")
	jsonlPath := filepath.Join(tmpDir, "issues.jsonl")

	// Create JSONL with merge conflict markers
	conflictData := `{"id":"test-1","title":"Issue 1"}
<<<<<<< HEAD
{"id":"test-2","title":"Local version"}
=======
{"id":"test-2","title":"Remote version"}
>>>>>>> main
{"id":"test-3","title":"Issue 3"}
`
	os.WriteFile(jsonlPath, []byte(conflictData), 0644)

	store := memory.New("")
	ctx := context.Background()
	notify := &testNotifier{}

	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		t.Error("Import should not be called with merge conflict")
		return 0, 0, nil, nil
	}

	err = AutoImportIfNewer(ctx, store, dbPath, notify, importFunc, nil)
	if err == nil {
		t.Error("Expected error for merge conflict")
	}

	if len(notify.errors) == 0 {
		t.Error("Expected error notification")
	}
}

func TestAutoImportIfNewer_WithRemapping(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bd-autoimport-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bd.db")
	jsonlPath := filepath.Join(tmpDir, "issues.jsonl")

	// Create test JSONL
	issue := &types.Issue{
		ID:        "test-1",
		Title:     "Test Issue",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(f).Encode(issue)
	f.Close()

	store := memory.New("")
	ctx := context.Background()
	notify := &testNotifier{}

	idMapping := map[string]string{"test-1": "test-2"}
	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		return 1, 0, idMapping, nil
	}

	onChangedCalled := false
	var needsFullExport bool
	onChanged := func(fullExport bool) {
		onChangedCalled = true
		needsFullExport = fullExport
	}

	err = AutoImportIfNewer(ctx, store, dbPath, notify, importFunc, onChanged)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if !onChangedCalled {
		t.Error("onChanged should be called when issues are remapped")
	}

	if !needsFullExport {
		t.Error("needsFullExport should be true when issues are remapped")
	}

	// Verify remapping was logged
	foundRemapping := false
	for _, info := range notify.infos {
		if strings.Contains(info, "remapped") {
			foundRemapping = true
			break
		}
	}
	if !foundRemapping {
		t.Error("Expected remapping notification")
	}
}

func TestCheckStaleness_NoMetadata(t *testing.T) {
	store := memory.New("")
	ctx := context.Background()

	tmpDir, err := os.MkdirTemp("", "bd-stale-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bd.db")

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if stale {
		t.Error("Should not be stale with no metadata")
	}
}

// stalenessRepo lays out a database directory next to a JSONL file holding the
// given content and returns the two paths.
func stalenessRepo(t *testing.T) (dbPath, jsonlPath string) {
	t.Helper()
	tmpDir := t.TempDir()
	return filepath.Join(tmpDir, "bd.db"), filepath.Join(tmpDir, "issues.jsonl")
}

func writeJSONL(t *testing.T, path, content string, modTime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !modTime.IsZero() {
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCheckStaleness_UnrecordedContent is the case staleness exists for: the
// file holds issues this database never imported.
func TestCheckStaleness_UnrecordedContent(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	writeJSONL(t, jsonlPath, `{"id":"test-1"}`, time.Time{})

	store := memory.New("")
	ctx := context.Background()
	// Some other content was the last thing imported.
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte(`{"id":"test-0"}`))); err != nil {
		t.Fatal(err)
	}

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if !stale {
		t.Error("stale = false for content the database never imported, want true")
	}
}

// TestCheckStaleness_RepublishedSameBytes is the false-staleness bug: an export
// rewrites the file with the bytes already recorded, so the mtime jumps forward
// while the content does not change. Readers must not fail-stop on that.
func TestCheckStaleness_RepublishedSameBytes(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	content := `{"id":"test-1"}`

	store := memory.New("")
	ctx := context.Background()
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte(content))); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadata(ctx, "last_import_time", time.Now().Add(-time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	// The rewrite: same bytes, mtime now.
	writeJSONL(t, jsonlPath, content, time.Now())

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if stale {
		t.Error("stale = true after a same-bytes rewrite, want false")
	}
}

// TestCheckStaleness_RestoredOlderFile guards the other direction: a file
// restored from a backup or an older commit carries an old mtime but content
// nobody imported, and that is stale.
func TestCheckStaleness_RestoredOlderFile(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)

	store := memory.New("")
	ctx := context.Background()
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte(`{"id":"test-current"}`))); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadata(ctx, "last_import_time", time.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	writeJSONL(t, jsonlPath, `{"id":"test-restored"}`, time.Now().Add(-24*time.Hour))

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if !stale {
		t.Error("stale = false for a restored older file with different content, want true")
	}
}

// TestCheckStaleness_PendingHashAccepted covers a reader arriving between the
// rename and the promotion: the published content is recorded as pending, which
// is a promise the writer already kept on disk.
func TestCheckStaleness_PendingHashAccepted(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	content := `{"id":"test-1"}`
	writeJSONL(t, jsonlPath, content, time.Time{})

	store := memory.New("")
	ctx := context.Background()
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte(`{"id":"test-0"}`))); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadata(ctx, "jsonl_pending_hash", jsonlpub.HashBytes([]byte(content))); err != nil {
		t.Fatal(err)
	}

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if stale {
		t.Error("stale = true for content recorded as pending, want false")
	}
}

// TestCheckStaleness_LegacyImportHash keeps databases written before the
// two-key protocol readable: their only record is last_import_hash.
func TestCheckStaleness_LegacyImportHash(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	content := `{"id":"test-1"}`
	writeJSONL(t, jsonlPath, content, time.Now())

	store := memory.New("")
	ctx := context.Background()
	if err := store.SetMetadata(ctx, "last_import_hash", jsonlpub.HashBytes([]byte(content))); err != nil {
		t.Fatal(err)
	}

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if stale {
		t.Error("stale = true for content recorded under the legacy key, want false")
	}
}

// TestCheckStaleness_UnparseableImportTime pins the contract change: the
// staleness answer no longer depends on the import timestamp, so a timestamp
// nothing can parse is no longer an error path.
func TestCheckStaleness_UnparseableImportTime(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	content := `{"id":"test-1"}`
	writeJSONL(t, jsonlPath, content, time.Time{})

	store := memory.New("")
	ctx := context.Background()
	if err := store.SetMetadata(ctx, "last_import_time", "not-a-valid-timestamp"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte(content))); err != nil {
		t.Fatal(err)
	}

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if stale {
		t.Error("stale = true despite a recorded content hash, want false")
	}
}

// TestCheckStaleness_PullThenImport walks the sequence a git pull produces:
// incoming content reads stale, the import records it, and the same check then
// passes.
func TestCheckStaleness_PullThenImport(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	store := memory.New("")
	ctx := context.Background()

	// What the pull dropped in.
	pulled := `{"id":"test-1","title":"pulled"}` + "\n"
	writeJSONL(t, jsonlPath, pulled, time.Time{})

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if stale {
		t.Error("stale = true before anything was ever imported, want false (first-run)")
	}

	// Record something else, so the pulled content is genuinely unimported.
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte("{}\n"))); err != nil {
		t.Fatal(err)
	}
	stale, err = CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if !stale {
		t.Fatal("stale = false for pulled content, want true")
	}

	// The import.
	if err := jsonlpub.RecordImport(ctx, store, jsonlPath, jsonlpub.HashBytes([]byte(pulled)), jsonlpub.Options{}); err != nil {
		t.Fatalf("RecordImport failed: %v", err)
	}

	stale, err = CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if stale {
		t.Error("stale = true after the import recorded the content, want false")
	}
}

func TestCheckForMergeConflicts(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		wantError bool
	}{
		{
			name:      "no conflict",
			data:      `{"id":"test-1"}`,
			wantError: false,
		},
		{
			name: "conflict with HEAD marker",
			data: `<<<<<<< HEAD
{"id":"test-1"}`,
			wantError: true,
		},
		{
			name: "conflict with separator",
			data: `{"id":"test-1"}
=======
{"id":"test-2"}`,
			wantError: true,
		},
		{
			name: "conflict with end marker",
			data: `{"id":"test-1"}
>>>>>>> main`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkForMergeConflicts([]byte(tt.data), "test.jsonl")
			if tt.wantError && err == nil {
				t.Error("Expected error for merge conflict")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestParseJSONL(t *testing.T) {
	notify := &testNotifier{}

	t.Run("valid jsonl", func(t *testing.T) {
		data := `{"id":"test-1","title":"Issue 1","status":"open","priority":1,"issue_type":"task","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}
{"id":"test-2","title":"Issue 2","status":"open","priority":1,"issue_type":"task","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`

		issues, err := parseJSONL([]byte(data), notify)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(issues) != 2 {
			t.Errorf("Expected 2 issues, got %d", len(issues))
		}
	})

	t.Run("empty lines ignored", func(t *testing.T) {
		data := `{"id":"test-1","title":"Issue 1","status":"open","priority":1,"issue_type":"task","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}

{"id":"test-2","title":"Issue 2","status":"open","priority":1,"issue_type":"task","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`

		issues, err := parseJSONL([]byte(data), notify)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(issues) != 2 {
			t.Errorf("Expected 2 issues, got %d", len(issues))
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		data := `{"id":"test-1","title":"Issue 1"}
not valid json`

		_, err := parseJSONL([]byte(data), notify)
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})

	t.Run("closed without closedAt", func(t *testing.T) {
		data := `{"id":"test-1","title":"Closed Issue","status":"closed","priority":1,"issue_type":"task","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`

		issues, err := parseJSONL([]byte(data), notify)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if issues[0].ClosedAt == nil {
			t.Error("Expected ClosedAt to be set for closed issue")
		}
	})
}

func TestShowRemapping(t *testing.T) {
	notify := &testNotifier{}

	allIssues := []*types.Issue{
		{ID: "test-1", Title: "Issue 1"},
		{ID: "test-2", Title: "Issue 2"},
	}

	idMapping := map[string]string{
		"test-1": "test-3",
		"test-2": "test-4",
	}

	showRemapping(allIssues, idMapping, notify)

	if len(notify.infos) == 0 {
		t.Error("Expected info messages for remapping")
	}

	foundRemappingHeader := false
	for _, info := range notify.infos {
		if strings.Contains(info, "remapped") && strings.Contains(info, "colliding") {
			foundRemappingHeader = true
			break
		}
	}

	if !foundRemappingHeader {
		t.Errorf("Expected remapping summary message, got infos: %v", notify.infos)
	}
}

func TestStderrNotifier(t *testing.T) {
	t.Run("debug enabled", func(t *testing.T) {
		notify := NewStderrNotifier(true)
		// Just verify it doesn't panic
		notify.Debugf("test debug")
		notify.Infof("test info")
		notify.Warnf("test warn")
		notify.Errorf("test error")
	})

	t.Run("debug disabled", func(t *testing.T) {
		notify := NewStderrNotifier(false)
		// Just verify it doesn't panic
		notify.Debugf("test debug")
		notify.Infof("test info")
	})
}

// TestAutoImportIfNewer_RewriteDuringImport pins R4-6 for the auto-import
// caller: the hash it records must describe the bytes it parsed, not whatever
// the file holds when the import finishes. Here a writer replaces the file
// while the import is running (from inside the import callback). Recording a
// re-read of the file would bless content the database never took in, and the
// next reader would call a genuinely diverged repository fresh.
func TestAutoImportIfNewer_RewriteDuringImport(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	parsed := `{"id":"test-1","title":"Parsed","status":"open","priority":1,"issue_type":"task"}` + "\n"
	writeJSONL(t, jsonlPath, parsed, time.Time{})

	store := memory.New("")
	ctx := context.Background()
	notify := &testNotifier{}

	// The concurrent rewrite lands while the import is in flight.
	rewritten := parsed + `{"id":"test-2","title":"Landed mid-import","status":"open","priority":1,"issue_type":"task"}` + "\n"
	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		if len(issues) != 1 {
			t.Errorf("import parsed %d issues, want 1", len(issues))
		}
		writeJSONL(t, jsonlPath, rewritten, time.Time{})
		return len(issues), 0, nil, nil
	}

	if err := AutoImportIfNewer(ctx, store, dbPath, notify, importFunc, nil); err != nil {
		t.Fatalf("AutoImportIfNewer failed: %v", err)
	}

	recorded, err := store.GetMetadata(ctx, "jsonl_content_hash")
	if err != nil {
		t.Fatalf("failed to read recorded hash: %v", err)
	}
	if want := jsonlpub.HashBytes([]byte(parsed)); recorded != want {
		t.Errorf("recorded hash %q, want the parsed bytes' hash %q", recorded, want)
	}

	stale, err := CheckStaleness(ctx, store, dbPath)
	if err != nil {
		t.Fatalf("CheckStaleness failed: %v", err)
	}
	if !stale {
		t.Error("stale = false after a rewrite landed mid-import, want true")
	}
}

// TestAutoImportIfNewer_PendingHashIsFresh pins the state between a
// publication's rename and its promote: the file holds bytes the database
// itself just exported, recorded under jsonl_pending_hash but not yet under
// jsonl_content_hash. A reader that compares against the committed hash alone
// calls that content new and re-imports the database's own export.
func TestAutoImportIfNewer_PendingHashIsFresh(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	published := `{"id":"test-pending-fresh","title":"Published but not yet promoted","status":"open","priority":1,"issue_type":"task"}` + "\n"
	writeJSONL(t, jsonlPath, published, time.Time{})

	store := memory.New("")
	ctx := context.Background()

	// Mid-publication state: pending records the file's bytes, committed still
	// holds the hash of the content the file had before the rename.
	if err := store.SetMetadata(ctx, "jsonl_pending_hash", jsonlpub.HashBytes([]byte(published))); err != nil {
		t.Fatalf("failed to set pending hash: %v", err)
	}
	if err := store.SetMetadata(ctx, "jsonl_content_hash", jsonlpub.HashBytes([]byte("previous content\n"))); err != nil {
		t.Fatalf("failed to set committed hash: %v", err)
	}

	notify := &testNotifier{}
	importCalled := false
	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		importCalled = true
		return len(issues), 0, nil, nil
	}

	if err := AutoImportIfNewer(ctx, store, dbPath, notify, importFunc, nil); err != nil {
		t.Fatalf("AutoImportIfNewer failed: %v", err)
	}

	if importCalled {
		t.Error("AutoImportIfNewer re-imported content the database had already published (file matched jsonl_pending_hash)")
	}
}

// TestAutoImportIfNewer_FreshPathRecordsNothing pins the rule that the Fresh
// branch parses nothing and must therefore record nothing. The hash the branch
// held describes the bytes os.ReadFile returned, while the Fresh verdict comes
// from the publish protocol's own re-read of the file, so the two can describe
// different content and writing the first as if it were the second commits a
// hash for content the database neither holds nor has on disk.
//
// The state here is a publication between its rename and its promote: the file
// holds the published bytes, recorded as pending, and the committed key still
// names the previous content. Recording on this path would overwrite the
// committed key and clear the pending one, destroying the record that describes
// what is actually on disk.
func TestAutoImportIfNewer_FreshPathRecordsNothing(t *testing.T) {
	dbPath, jsonlPath := stalenessRepo(t)
	published := `{"id":"test-fresh-no-record","title":"Published","status":"open","priority":1,"issue_type":"task"}` + "\n"
	writeJSONL(t, jsonlPath, published, time.Time{})

	store := memory.New("")
	ctx := context.Background()

	pendingHash := jsonlpub.HashBytes([]byte(published))
	committedHash := jsonlpub.HashBytes([]byte("previous content\n"))
	if err := store.SetMetadata(ctx, "jsonl_pending_hash", pendingHash); err != nil {
		t.Fatalf("failed to set pending hash: %v", err)
	}
	if err := store.SetMetadata(ctx, "jsonl_content_hash", committedHash); err != nil {
		t.Fatalf("failed to set committed hash: %v", err)
	}

	importFunc := func(ctx context.Context, issues []*types.Issue) (int, int, map[string]string, error) {
		t.Error("AutoImportIfNewer imported content the publish protocol called fresh")
		return 0, 0, nil, nil
	}

	if err := AutoImportIfNewer(ctx, store, dbPath, &testNotifier{}, importFunc, nil); err != nil {
		t.Fatalf("AutoImportIfNewer failed: %v", err)
	}

	gotCommitted, err := store.GetMetadata(ctx, "jsonl_content_hash")
	if err != nil {
		t.Fatalf("failed to read committed hash: %v", err)
	}
	if gotCommitted != committedHash {
		t.Errorf("jsonl_content_hash = %q, want it untouched at %q", gotCommitted, committedHash)
	}

	gotPending, err := store.GetMetadata(ctx, "jsonl_pending_hash")
	if err != nil {
		t.Fatalf("failed to read pending hash: %v", err)
	}
	if gotPending != pendingHash {
		t.Errorf("jsonl_pending_hash = %q, want it untouched at %q", gotPending, pendingHash)
	}
}
