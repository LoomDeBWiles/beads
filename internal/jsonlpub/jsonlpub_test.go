package jsonlpub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/lockfile"
	"github.com/steveyegge/beads/internal/types"
)

// fakeStore is the metadata surface the contract needs, plus the dirty-marker
// surface, with hooks that let a test act at an exact moment inside an
// operation. Hooks run before the mutex is taken, so a hook may re-enter the
// store.
type fakeStore struct {
	mu       sync.Mutex
	meta     map[string]string
	fileHash string
	dirty    map[string]time.Time

	onGetMetadata func(key string)
	onSetMetadata func(key, value string)
	setErr        map[string]error
}

func newFakeStore() *fakeStore {
	return &fakeStore{meta: map[string]string{}, dirty: map[string]time.Time{}}
}

func (f *fakeStore) GetMetadata(_ context.Context, key string) (string, error) {
	if f.onGetMetadata != nil {
		f.onGetMetadata(key)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.meta[key], nil
}

func (f *fakeStore) SetMetadata(_ context.Context, key, value string) error {
	if f.onSetMetadata != nil {
		f.onSetMetadata(key, value)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.setErr[key]; err != nil {
		return err
	}
	f.meta[key] = value
	return nil
}

func (f *fakeStore) SetJSONLFileHash(_ context.Context, fileHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fileHash = fileHash
	return nil
}

func (f *fakeStore) GetDirtyIssueSnapshots(_ context.Context) ([]DirtySnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshots := make([]DirtySnapshot, 0, len(f.dirty))
	for id, markedAt := range f.dirty {
		snapshots = append(snapshots, DirtySnapshot{ID: id, MarkedAt: markedAt})
	}
	return snapshots, nil
}

func (f *fakeStore) ClearDirtyIssuesIfUnchanged(_ context.Context, snapshots []DirtySnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, snapshot := range snapshots {
		if markedAt, ok := f.dirty[snapshot.ID]; ok && markedAt.Equal(snapshot.MarkedAt) {
			delete(f.dirty, snapshot.ID)
		}
	}
	return nil
}

func (f *fakeStore) markDirty(id string, markedAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirty[id] = markedAt
}

func (f *fakeStore) dirtyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dirty)
}

func (f *fakeStore) get(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.meta[key]
}

func issues(ids ...string) []*types.Issue {
	list := make([]*types.Issue, 0, len(ids))
	for _, id := range ids {
		list = append(list, &types.Issue{ID: id, Title: "issue " + id})
	}
	return list
}

func buildIssues(ids ...string) BuildIssuesFunc {
	return func(context.Context) ([]*types.Issue, error) { return issues(ids...), nil }
}

// newRepo returns a scratch directory and the JSONL path inside it.
func newRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	return dir, filepath.Join(dir, "issues.jsonl")
}

// publishedRepo returns a repository in steady state: one publication done.
func publishedRepo(t *testing.T) (string, string, *fakeStore, string) {
	t.Helper()
	dir, jsonlPath := newRepo(t)
	store := newFakeStore()
	result, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1"), Options{})
	if err != nil {
		t.Fatalf("initial publish: %v", err)
	}
	return dir, jsonlPath, store, result.Hash
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustState(t *testing.T, store MetadataStore, jsonlPath string) Status {
	t.Helper()
	status, err := ContentState(context.Background(), store, jsonlPath, "")
	if err != nil {
		t.Fatalf("ContentState: %v", err)
	}
	return status
}

// setCrashAt installs a failpoint that fires once, at the named step.
func setCrashAt(t *testing.T, point failpoint) {
	t.Helper()
	crashAt = func(actual failpoint) error {
		if actual != point {
			return nil
		}
		return errors.New("simulated crash at " + string(point))
	}
	t.Cleanup(func() { crashAt = nil })
}

// TestPublishFailpointStates stops a second publication at every step of the
// protocol and asserts what the file, the recorded hashes, and a reader see in
// that state. No reachable state may read stale.
func TestPublishFailpointStates(t *testing.T) {
	keys := newKeys("")

	cases := []struct {
		point         failpoint
		wantFileIsNew bool
		wantCommitted string // "old", "new"
		wantPending   string // "new", "none"
		wantDirty     int
	}{
		{crashAfterPending, false, "old", "new", 1},
		{crashAfterRename, true, "old", "new", 1},
		{crashAfterCommitted, true, "new", "new", 1},
		{crashAfterFileHash, true, "new", "new", 1},
		{crashAfterImportTime, true, "new", "new", 1},
		{crashAfterPendingDelete, true, "new", "none", 1},
		{crashAfterClear, true, "new", "none", 0},
	}

	for _, testCase := range cases {
		t.Run(string(testCase.point), func(t *testing.T) {
			_, jsonlPath, store, oldHash := publishedRepo(t)
			oldContent := readFile(t, jsonlPath)
			store.markDirty("a-2", time.Now())

			setCrashAt(t, testCase.point)
			_, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{})
			if err == nil {
				t.Fatal("expected the simulated crash to abort the publication")
			}

			newContent := readFile(t, jsonlPath)
			if isNew := newContent != oldContent; isNew != testCase.wantFileIsNew {
				t.Errorf("file replaced = %v, want %v", isNew, testCase.wantFileIsNew)
			}
			newHash := HashBytes([]byte(newContent))

			wantCommitted := oldHash
			if testCase.wantCommitted == "new" {
				wantCommitted = HashBytes([]byte(newContent))
			}
			if got := store.get(keys.committed); got != wantCommitted {
				t.Errorf("committed hash = %s, want %s", got, wantCommitted)
			}

			pending := store.get(keys.pending)
			switch testCase.wantPending {
			case "none":
				if pending != "" {
					t.Errorf("pending hash = %s, want empty", pending)
				}
			case "new":
				if pending == "" || pending == oldHash {
					t.Errorf("pending hash = %q, want the new content hash", pending)
				}
				if testCase.wantFileIsNew && pending != newHash {
					t.Errorf("pending hash = %s, want the published content %s", pending, newHash)
				}
			}

			if got := store.dirtyCount(); got != testCase.wantDirty {
				t.Errorf("dirty count = %d, want %d", got, testCase.wantDirty)
			}

			// The invariant every crash state must hold.
			if status := mustState(t, store, jsonlPath); status != StatusFresh {
				t.Errorf("ContentState = %s, want fresh", status)
			}
		})
	}
}

// TestLockedReaderPromotesCrashedPending pins R3-4: a reader that has taken the
// publish lock and finds the file matching the pending hash finishes the
// interrupted promotion.
func TestLockedReaderPromotesCrashedPending(t *testing.T) {
	keys := newKeys("")
	_, jsonlPath, store, _ := publishedRepo(t)

	setCrashAt(t, crashAfterRename)
	if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{}); err == nil {
		t.Fatal("expected the simulated crash to abort the publication")
	}
	crashAt = nil

	pending := store.get(keys.pending)
	if pending == "" {
		t.Fatal("setup: expected a dangling pending hash")
	}

	lock, err := acquirePublishLock(context.Background(), jsonlPath)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	status, _, err := contentStateLocked(context.Background(), store, jsonlPath, keys, Options{})
	if err != nil {
		t.Fatalf("contentStateLocked: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	if status != StatusFresh {
		t.Errorf("status = %s, want fresh", status)
	}
	if got := store.get(keys.committed); got != pending {
		t.Errorf("committed hash = %s, want the promoted %s", got, pending)
	}
	if got := store.get(keys.pending); got != "" {
		t.Errorf("pending hash = %s, want empty after promotion", got)
	}
}

// TestContentStateRechecksUnderLock pins V2-1: a reader whose file sample and
// key sample straddle a publication must not call a healthy repository
// diverged.
func TestContentStateRechecksUnderLock(t *testing.T) {
	_, jsonlPath, store, _ := publishedRepo(t)

	store.onGetMetadata = func(string) {
		// Fires between the reader's file hash and its key reads: the whole
		// A-to-B publication lands in that gap. Detaching the hook first keeps
		// the publication's own reads from re-entering it.
		store.onGetMetadata = nil
		if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{}); err != nil {
			t.Errorf("interleaved publish: %v", err)
		}
	}

	if status := mustState(t, store, jsonlPath); status != StatusFresh {
		t.Errorf("ContentState = %s, want fresh (the lock recheck must confirm)", status)
	}
}

// TestPublishAbortsOnDivergence pins R3-2: foreign bytes are never overwritten.
func TestPublishAbortsOnDivergence(t *testing.T) {
	_, jsonlPath, store, _ := publishedRepo(t)

	foreign := `{"id":"a-9","title":"written by someone else"}` + "\n"
	if err := os.WriteFile(jsonlPath, []byte(foreign), 0o644); err != nil {
		t.Fatalf("write foreign content: %v", err)
	}

	_, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{})
	if !errors.Is(err, ErrDiverged) {
		t.Fatalf("error = %v, want ErrDiverged", err)
	}
	if got := readFile(t, jsonlPath); got != foreign {
		t.Errorf("file content = %q, want the foreign bytes preserved", got)
	}
	if status := mustState(t, store, jsonlPath); status != StatusDiverged {
		t.Errorf("ContentState = %s, want diverged", status)
	}
}

// TestPublishAbortsOnUnrecordedFile pins R4-3: a file no hash was recorded for
// must be imported before it can be replaced.
func TestPublishAbortsOnUnrecordedFile(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()
	if err := os.WriteFile(jsonlPath, []byte(`{"id":"a-1"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-2"), Options{})
	if !errors.Is(err, ErrDiverged) {
		t.Fatalf("error = %v, want ErrDiverged", err)
	}
}

// TestPublishOntoAbsentFileSucceeds is the other half of R4-3: no metadata and
// no file is the first export, which is legal.
func TestPublishOntoAbsentFileSucceeds(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()

	result, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1"), Options{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if result.IssueCount != 1 {
		t.Errorf("issue count = %d, want 1", result.IssueCount)
	}
	if status := mustState(t, store, jsonlPath); status != StatusFresh {
		t.Errorf("ContentState = %s, want fresh", status)
	}
}

// TestPublishRechecksGuardHashBeforeRename pins R4-1: a writer that ignores the
// lock between the guard and the rename aborts the publication.
func TestPublishRechecksGuardHashBeforeRename(t *testing.T) {
	keys := newKeys("")
	_, jsonlPath, store, _ := publishedRepo(t)

	foreign := `{"id":"a-9","title":"git checkout landed here"}` + "\n"
	store.onSetMetadata = func(key, _ string) {
		if key != keys.pending {
			return
		}
		store.onSetMetadata = nil
		if err := os.WriteFile(jsonlPath, []byte(foreign), 0o644); err != nil {
			t.Errorf("write foreign content: %v", err)
		}
	}

	_, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{})
	if !errors.Is(err, ErrDiverged) {
		t.Fatalf("error = %v, want ErrDiverged", err)
	}
	if got := readFile(t, jsonlPath); got != foreign {
		t.Errorf("file content = %q, want the foreign bytes preserved", got)
	}
	if got := store.get(keys.pending); got != "" {
		t.Errorf("pending hash = %s, want cleared after the abort", got)
	}
	if leftovers := tempFiles(t, jsonlPath); len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func tempFiles(t *testing.T, jsonlPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(jsonlPath + ".tmp.*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return matches
}

// TestPublishKeepsIssueDirtyWhenRemarkedDuringExport pins R3-3: the dirty
// snapshot is taken before the issues, and a mutation landing while the issues
// are being read keeps its marker.
func TestPublishKeepsIssueDirtyWhenRemarkedDuringExport(t *testing.T) {
	_, jsonlPath, store, _ := publishedRepo(t)
	store.markDirty("a-2", time.Now())

	build := func(context.Context) ([]*types.Issue, error) {
		// The mutation the export is racing: re-marked while issues are read.
		store.markDirty("a-2", time.Now().Add(time.Second))
		return issues("a-1", "a-2"), nil
	}

	if _, err := Publish(context.Background(), store, jsonlPath, build, Options{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := store.dirtyCount(); got != 1 {
		t.Errorf("dirty count = %d, want 1 (the re-marked issue must survive)", got)
	}
}

// TestPublishClearsDirtyMarkers is the ordinary case the once-per-second export
// loop failed at: markers retired, so the next pass has nothing to flush.
func TestPublishClearsDirtyMarkers(t *testing.T) {
	_, jsonlPath, store, _ := publishedRepo(t)
	store.markDirty("a-1", time.Now())
	store.markDirty("a-2", time.Now())

	if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := store.dirtyCount(); got != 0 {
		t.Errorf("dirty count = %d, want 0", got)
	}
}

// metadataOnly hides the dirty-marker methods, standing in for the memory
// backend's boolean dirty map.
type metadataOnly struct {
	inner *fakeStore
}

func (m metadataOnly) GetMetadata(ctx context.Context, key string) (string, error) {
	return m.inner.GetMetadata(ctx, key)
}
func (m metadataOnly) SetMetadata(ctx context.Context, key, value string) error {
	return m.inner.SetMetadata(ctx, key, value)
}
func (m metadataOnly) SetJSONLFileHash(ctx context.Context, fileHash string) error {
	return m.inner.SetJSONLFileHash(ctx, fileHash)
}

// TestPublishWithoutDirtySnapshotStore pins V2-8: a store with no per-issue
// marks publishes normally and simply clears nothing.
func TestPublishWithoutDirtySnapshotStore(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := metadataOnly{inner: newFakeStore()}

	if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1"), Options{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if status := mustState(t, store, jsonlPath); status != StatusFresh {
		t.Errorf("ContentState = %s, want fresh", status)
	}
}

// TestPublishAbortsWhenPendingWriteFails pins R3: bytes are never revealed
// without a hash recording them.
func TestPublishAbortsWhenPendingWriteFails(t *testing.T) {
	keys := newKeys("")
	_, jsonlPath, store, oldHash := publishedRepo(t)
	oldContent := readFile(t, jsonlPath)
	store.setErr = map[string]error{keys.pending: errors.New("metadata write failed")}

	if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{}); err == nil {
		t.Fatal("expected the publication to abort")
	}
	if got := readFile(t, jsonlPath); got != oldContent {
		t.Error("file was replaced despite the failed pending write")
	}
	if got := store.get(keys.committed); got != oldHash {
		t.Errorf("committed hash = %s, want %s", got, oldHash)
	}
	if leftovers := tempFiles(t, jsonlPath); len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

// TestPublishSortsIssues keeps the published bytes independent of the order a
// caller happens to assemble issues in, so equal content hashes equal.
func TestPublishSortsIssues(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()

	if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-3", "a-1", "a-2"), Options{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, jsonlPath)), "\n")
	if len(lines) != 3 {
		t.Fatalf("line count = %d, want 3", len(lines))
	}
	for index, wantID := range []string{"a-1", "a-2", "a-3"} {
		if !strings.Contains(lines[index], `"id":"`+wantID+`"`) {
			t.Errorf("line %d = %s, want issue %s", index, lines[index], wantID)
		}
	}
}

// TestAcquireWaitRespectsContext pins R3-8: a wedged holder cannot hang a
// caller past its own deadline.
func TestAcquireWaitRespectsContext(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()

	holder, err := acquirePublishLock(context.Background(), jsonlPath)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = holder.Release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = Publish(ctx, store, jsonlPath, buildIssues("a-1"), Options{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %v, want the deadline to cut it short", elapsed)
	}
}

// TestAcquireContextCancelledBeforeWait covers the already-cancelled caller.
func TestAcquireContextCancelledBeforeWait(t *testing.T) {
	dir, _ := newRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := lockfile.AcquireContext(ctx, filepath.Join(dir, ".publish.lock")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// TestRecordImportUsesParsedHash pins R3-1: the recorded hash covers the bytes
// the import parsed, so a file rewritten after the read reads diverged instead
// of being blessed.
func TestRecordImportUsesParsedHash(t *testing.T) {
	keys := newKeys("")
	_, jsonlPath := newRepo(t)
	store := newFakeStore()

	parsed := []byte(`{"id":"a-1","title":"the bytes the import read"}` + "\n")
	parsedHash := HashBytes(parsed)
	rewritten := []byte(`{"id":"a-2","title":"rewritten after the read"}` + "\n")
	if err := os.WriteFile(jsonlPath, rewritten, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := RecordImport(context.Background(), store, jsonlPath, parsedHash, Options{}); err != nil {
		t.Fatalf("record import: %v", err)
	}
	if got := store.get(keys.committed); got != parsedHash {
		t.Errorf("committed hash = %s, want the parsed-bytes hash %s", got, parsedHash)
	}
	if status := mustState(t, store, jsonlPath); status != StatusDiverged {
		t.Errorf("ContentState = %s, want diverged (the file moved on)", status)
	}
}

// TestRecordImportClearsCrashedPending pins V2-5: the import is the second
// deleter of a pending hash a crashed publication left behind.
func TestRecordImportClearsCrashedPending(t *testing.T) {
	keys := newKeys("")
	_, jsonlPath, store, _ := publishedRepo(t)

	setCrashAt(t, crashAfterPending)
	if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{}); err == nil {
		t.Fatal("expected the simulated crash to abort the publication")
	}
	crashAt = nil
	if store.get(keys.pending) == "" {
		t.Fatal("setup: expected a dangling pending hash")
	}

	pulled := []byte(`{"id":"a-7","title":"pulled from git"}` + "\n")
	if err := os.WriteFile(jsonlPath, pulled, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RecordImport(context.Background(), store, jsonlPath, HashBytes(pulled), Options{}); err != nil {
		t.Fatalf("record import: %v", err)
	}

	if got := store.get(keys.pending); got != "" {
		t.Errorf("pending hash = %s, want cleared", got)
	}
	if status := mustState(t, store, jsonlPath); status != StatusFresh {
		t.Errorf("ContentState = %s, want fresh", status)
	}
}

// TestRecordImportThenPublish covers the pull-then-export sequence: recording
// the import is what lets the next publication past the divergence guard.
func TestRecordImportThenPublish(t *testing.T) {
	_, jsonlPath, store, _ := publishedRepo(t)

	pulled := []byte(`{"id":"a-7","title":"pulled from git"}` + "\n")
	if err := os.WriteFile(jsonlPath, pulled, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if status := mustState(t, store, jsonlPath); status != StatusDiverged {
		t.Fatalf("ContentState = %s, want diverged before the import is recorded", status)
	}

	if err := RecordImport(context.Background(), store, jsonlPath, HashBytes(pulled), Options{}); err != nil {
		t.Fatalf("record import: %v", err)
	}
	if _, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-7", "a-8"), Options{}); err != nil {
		t.Fatalf("publish after recorded import: %v", err)
	}
	if status := mustState(t, store, jsonlPath); status != StatusFresh {
		t.Errorf("ContentState = %s, want fresh", status)
	}
}

// TestContentStateMigrationKey keeps databases written before the key rename
// reading fresh.
func TestContentStateMigrationKey(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()
	content := []byte(`{"id":"a-1"}` + "\n")
	if err := os.WriteFile(jsonlPath, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := store.SetMetadata(context.Background(), legacyCommittedKeyBase, HashBytes(content)); err != nil {
		t.Fatalf("set legacy key: %v", err)
	}

	if status := mustState(t, store, jsonlPath); status != StatusFresh {
		t.Errorf("ContentState = %s, want fresh via the legacy key", status)
	}
}

// TestContentStateNoFile keeps the first-run repository out of the diverged
// branch.
func TestContentStateNoFile(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()

	if status := mustState(t, store, jsonlPath); status != StatusNoFile {
		t.Errorf("ContentState = %s, want no-file", status)
	}
}

// TestContentStateNoMetadata is the unrecorded-file verdict the callers map
// differently: not stale for a staleness check, changed for an export gate.
func TestContentStateNoMetadata(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()
	if err := os.WriteFile(jsonlPath, []byte(`{"id":"a-1"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if status := mustState(t, store, jsonlPath); status != StatusNoMetadata {
		t.Errorf("ContentState = %s, want no-metadata", status)
	}
}

// TestKeySuffix pins the multi-repo key names, including the Windows-path
// colon replacement.
func TestKeySuffix(t *testing.T) {
	keys := newKeys("../frontend")
	if keys.committed != "jsonl_content_hash:../frontend" {
		t.Errorf("committed key = %s", keys.committed)
	}
	if keys.pending != "jsonl_pending_hash:../frontend" {
		t.Errorf("pending key = %s", keys.pending)
	}
	if keys.importTime != "last_import_time:../frontend" {
		t.Errorf("import time key = %s", keys.importTime)
	}

	windows := newKeys(`C:\repos\frontend`)
	if windows.committed != `jsonl_content_hash:C_\repos\frontend` {
		t.Errorf("windows committed key = %s", windows.committed)
	}
}

// TestSuffixedPublishIsolated keeps one repository's publication out of
// another's keys.
func TestSuffixedPublishIsolated(t *testing.T) {
	_, jsonlPath := newRepo(t)
	store := newFakeStore()

	result, err := Publish(context.Background(), store, jsonlPath, buildIssues("a-1"), Options{KeySuffix: "../frontend"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := store.get("jsonl_content_hash:../frontend"); got != result.Hash {
		t.Errorf("suffixed committed hash = %s, want %s", got, result.Hash)
	}
	if got := store.get(committedKeyBase); got != "" {
		t.Errorf("unsuffixed committed hash = %s, want empty", got)
	}
}

// TestResolveCanonicalRelativePath pins R3-7 for a relative path.
func TestResolveCanonicalRelativePath(t *testing.T) {
	dir, jsonlPath := newRepo(t)
	if err := os.WriteFile(jsonlPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(original) }()

	if !IsCanonicalTarget("issues.jsonl", jsonlPath) {
		t.Error("a relative path to the default file must be canonical")
	}
	if !IsCanonicalTarget("./issues.jsonl", jsonlPath) {
		t.Error("a dot-relative path to the default file must be canonical")
	}
}

// TestResolveCanonicalFinalComponentSymlink pins the other half of R3-7: an
// alias symlinked to the default file is a different rename target.
func TestResolveCanonicalFinalComponentSymlink(t *testing.T) {
	dir, jsonlPath := newRepo(t)
	if err := os.WriteFile(jsonlPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	alias := filepath.Join(dir, "alias.jsonl")
	if err := os.Symlink(jsonlPath, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if IsCanonicalTarget(alias, jsonlPath) {
		t.Error("a final-component symlink must not count as the canonical file")
	}
}

// TestResolveCanonicalParentSymlink is the case the rule does resolve: the
// parent directory reached through a link is the same directory.
func TestResolveCanonicalParentSymlink(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if !IsCanonicalTarget(filepath.Join(linkDir, "issues.jsonl"), filepath.Join(realDir, "issues.jsonl")) {
		t.Error("a path through a linked parent directory must be canonical")
	}
}

// TestPublishesAreSerialized proves the lock excludes concurrent publications
// of the same file: each sees the previous one's bytes and none aborts.
func TestPublishesAreSerialized(t *testing.T) {
	_, jsonlPath, store, _ := publishedRepo(t)

	const publishers = 4
	var wait sync.WaitGroup
	errs := make([]error, publishers)
	for index := 0; index < publishers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errs[index] = Publish(context.Background(), store, jsonlPath, buildIssues("a-1", "a-2"), Options{})
		}(index)
	}
	wait.Wait()

	for index, err := range errs {
		if err != nil {
			t.Errorf("publisher %d: %v", index, err)
		}
	}
	if status := mustState(t, store, jsonlPath); status != StatusFresh {
		t.Errorf("ContentState = %s, want fresh", status)
	}
}
