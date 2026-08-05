// Package jsonlpub is the single authority for the consistency contract
// between the SQLite database and its JSONL mirror.
//
// Freshness is a statement about content, never about clocks: a repository is
// fresh when the JSONL file's sha256 equals a hash the database recorded. A
// SQLite write and a filesystem rename cannot commit together, so the record
// spans the reveal with two keys - jsonl_pending_hash written before the
// rename, jsonl_content_hash promoted after it - and a reader accepts either.
// Every reachable state, steady or mid-publication or crashed on either side,
// therefore hashes to one of the two. Bytes matching neither were written by
// somebody else, which is the only condition that genuinely means stale.
//
// The package owns three operations, all serialized by one cross-process lock
// on .publish.lock beside the JSONL file: ContentState decides freshness,
// Publish writes database to file, and RecordImport records file into database.
package jsonlpub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/lockfile"
	"github.com/steveyegge/beads/internal/types"
)

// Metadata key bases. A multi-repo suffix is appended after a ':' separator.
const (
	committedKeyBase = "jsonl_content_hash"
	// legacyCommittedKeyBase is the pre-bd-39o name for the committed hash,
	// still read so databases written by older versions stay fresh.
	legacyCommittedKeyBase = "last_import_hash"
	pendingKeyBase         = "jsonl_pending_hash"
	importTimeKeyBase      = "last_import_time"

	lockFileName = ".publish.lock"

	// jsonlFileMode keeps the mirror readable by git and other tools.
	jsonlFileMode fs.FileMode = 0o644
)

// ErrDiverged reports that the JSONL file holds bytes the database never
// recorded. Publication aborts on it rather than overwriting them; the repair
// is an import, not a retry. Test with errors.Is.
var ErrDiverged = errors.New("JSONL content diverged from the recorded state (import first)")

// Status is the verdict of comparing the JSONL file against the recorded hashes.
type Status int

const (
	// StatusFresh means the file hashes to the committed or the pending hash.
	StatusFresh Status = iota
	// StatusDiverged means the file matches neither recorded hash.
	StatusDiverged
	// StatusNoMetadata means the file exists but no hash was ever recorded.
	StatusNoMetadata
	// StatusNoFile means the JSONL file does not exist.
	StatusNoFile
)

func (s Status) String() string {
	switch s {
	case StatusFresh:
		return "fresh"
	case StatusDiverged:
		return "diverged"
	case StatusNoMetadata:
		return "no-metadata"
	case StatusNoFile:
		return "no-file"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

// Options configure one publication or import record.
type Options struct {
	// KeySuffix scopes the metadata keys to a single repository in multi-repo
	// mode; empty in single-repo mode. Colons are replaced with underscores so
	// Windows paths cannot break the key separator.
	KeySuffix string

	// Warnf, when set, receives the best-effort failures that must not fail the
	// operation: promotion writes after the file is already published, and the
	// dirty-marker clear.
	Warnf func(format string, args ...any)
}

func (o Options) warnf(format string, args ...any) {
	if o.Warnf != nil {
		o.Warnf(format, args...)
	}
}

// BuildIssuesFunc returns the issues to write. Publish invokes it inside the
// publish lock and after the dirty snapshot has been taken, so a mutation
// racing the export either lands in these issues or keeps its dirty marker.
type BuildIssuesFunc func(ctx context.Context) ([]*types.Issue, error)

// Result describes a completed publication.
type Result struct {
	// IssueCount is the number of issues written to the file.
	IssueCount int
	// Hash is the sha256 of the published file content.
	Hash string
}

// NewHasher returns the hash function the JSONL content contract uses. Callers
// that read the file as a stream (importers) hash through it while parsing, so
// the recorded hash covers exactly the bytes they parsed.
func NewHasher() hash.Hash {
	return sha256.New()
}

// HashSum renders a hasher's digest the way the metadata keys store it.
func HashSum(h hash.Hash) string {
	return hex.EncodeToString(h.Sum(nil))
}

// HashBytes is HashSum for callers that already hold the whole content.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashFile returns the sha256 of a file's content. A missing file yields an
// error wrapping fs.ErrNotExist.
func HashFile(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 - controlled path from config
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hasher := NewHasher()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return HashSum(hasher), nil
}

// ResolveCanonical returns a comparable form of a JSONL path: parent
// directories are resolved through symlinks, the final component never is. A
// rename replaces the final directory entry, so an alias symlinked to the
// default file is a different publication target even though reads follow it
// to the same bytes.
func ResolveCanonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.Base(abs)), nil
}

// IsCanonicalTarget reports whether writing to path publishes the repository's
// default JSONL file. Paths that cannot be resolved (a missing parent
// directory) are not canonical.
func IsCanonicalTarget(path, defaultPath string) bool {
	if path == "" || defaultPath == "" {
		return false
	}
	resolved, err := ResolveCanonical(path)
	if err != nil {
		return false
	}
	resolvedDefault, err := ResolveCanonical(defaultPath)
	if err != nil {
		return false
	}
	return resolved == resolvedDefault
}

// metadataKeys are the four keys one repository's contract lives in.
type metadataKeys struct {
	committed       string
	legacyCommitted string
	pending         string
	importTime      string
}

func newKeys(suffix string) metadataKeys {
	keys := metadataKeys{
		committed:       committedKeyBase,
		legacyCommitted: legacyCommittedKeyBase,
		pending:         pendingKeyBase,
		importTime:      importTimeKeyBase,
	}
	if suffix == "" {
		return keys
	}
	suffix = ":" + strings.ReplaceAll(suffix, ":", "_")
	keys.committed += suffix
	keys.legacyCommitted += suffix
	keys.pending += suffix
	keys.importTime += suffix
	return keys
}

// ContentState reports whether the JSONL file agrees with what the database
// recorded.
//
// The lock-free sample is authoritative only for StatusFresh. Any other verdict
// is provisional and re-sampled under the publish lock: a reader that reads the
// file before an A-to-B publication and the keys after it would otherwise call
// a healthy repository diverged. Steady state never takes the lock.
func ContentState(ctx context.Context, store MetadataStore, jsonlPath, keySuffix string) (Status, error) {
	keys := newKeys(keySuffix)

	status, _, err := sampleState(ctx, store, jsonlPath, keys)
	if err != nil {
		return status, err
	}
	if status == StatusFresh {
		return status, nil
	}

	lock, err := acquirePublishLock(ctx, jsonlPath)
	if err != nil {
		return StatusFresh, err
	}
	defer func() { _ = lock.Release() }()

	status, _, err = contentStateLocked(ctx, store, jsonlPath, keys, Options{})
	return status, err
}

// sampleState hashes the file and compares it against both recorded hashes. It
// takes no lock and performs no repair; both callers wrap it.
func sampleState(ctx context.Context, store MetadataStore, jsonlPath string, keys metadataKeys) (Status, string, error) {
	fileHash, err := HashFile(jsonlPath)
	if errors.Is(err, fs.ErrNotExist) {
		return StatusNoFile, "", nil
	}
	if err != nil {
		return StatusDiverged, "", fmt.Errorf("failed to hash %s: %w", jsonlPath, err)
	}

	committed, err := readCommitted(ctx, store, keys)
	if err != nil {
		return StatusDiverged, fileHash, err
	}
	pending, err := store.GetMetadata(ctx, keys.pending)
	if err != nil {
		return StatusDiverged, fileHash, fmt.Errorf("failed to read %s: %w", keys.pending, err)
	}

	switch {
	case committed == "" && pending == "":
		return StatusNoMetadata, fileHash, nil
	case fileHash == committed || fileHash == pending:
		return StatusFresh, fileHash, nil
	default:
		return StatusDiverged, fileHash, nil
	}
}

// contentStateLocked is the sampler used while the publish lock is already
// held. It never acquires the lock, so Publish's guard cannot deadlock against
// itself, and it repairs the one state a crash can leave behind: a file
// matching the pending hash is promoted to committed, closing the two-key
// window at the first locked event after the crash.
func contentStateLocked(ctx context.Context, store MetadataStore, jsonlPath string, keys metadataKeys, opts Options) (Status, string, error) {
	status, fileHash, err := sampleState(ctx, store, jsonlPath, keys)
	if err != nil || status != StatusFresh {
		return status, fileHash, err
	}

	committed, err := readCommitted(ctx, store, keys)
	if err != nil {
		return status, fileHash, err
	}
	if fileHash != committed {
		_ = promote(ctx, store, keys, fileHash, opts)
	}
	return StatusFresh, fileHash, nil
}

func readCommitted(ctx context.Context, store MetadataStore, keys metadataKeys) (string, error) {
	committed, err := store.GetMetadata(ctx, keys.committed)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", keys.committed, err)
	}
	if committed != "" {
		return committed, nil
	}
	legacy, err := store.GetMetadata(ctx, keys.legacyCommitted)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", keys.legacyCommitted, err)
	}
	return legacy, nil
}

// promote records hash as the committed content hash and drops the pending
// hash. Every write is best-effort: the bytes are already published, and a
// surviving pending hash keeps readers correct until the next locked event, so
// a failed write warns and the sequence continues.
//
// The returned error is never a write failure: it is non-nil only when a test
// failpoint interrupts the sequence, standing in for the process dying here.
func promote(ctx context.Context, store MetadataStore, keys metadataKeys, contentHash string, opts Options) error {
	if err := store.SetMetadata(ctx, keys.committed, contentHash); err != nil {
		opts.warnf("failed to update %s: %v", keys.committed, err)
		return nil
	}
	if err := simulateCrash(crashAfterCommitted); err != nil {
		return err
	}

	if err := store.SetJSONLFileHash(ctx, contentHash); err != nil {
		opts.warnf("failed to update JSONL file hash: %v", err)
	}
	if err := simulateCrash(crashAfterFileHash); err != nil {
		return err
	}

	// RFC3339Nano keeps nanosecond precision so the timestamp cannot tie with a
	// file mtime.
	if err := store.SetMetadata(ctx, keys.importTime, time.Now().Format(time.RFC3339Nano)); err != nil {
		opts.warnf("failed to update %s: %v", keys.importTime, err)
	}
	if err := simulateCrash(crashAfterImportTime); err != nil {
		return err
	}

	clearPending(ctx, store, keys, opts)
	return nil
}

// clearPending deletes the pending hash. The metadata store has no delete, and
// an empty value is what every reader treats as absent.
func clearPending(ctx context.Context, store MetadataStore, keys metadataKeys, opts Options) {
	if err := store.SetMetadata(ctx, keys.pending, ""); err != nil {
		opts.warnf("failed to clear %s: %v", keys.pending, err)
	}
}

func publishLockPath(jsonlPath string) string {
	return filepath.Join(filepath.Dir(jsonlPath), lockFileName)
}

func acquirePublishLock(ctx context.Context, jsonlPath string) (*lockfile.Lock, error) {
	lockPath := publishLockPath(jsonlPath)
	lock, err := lockfile.AcquireContext(ctx, lockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire publish lock %s: %w", lockPath, err)
	}
	return lock, nil
}

// Publish writes the database's issues to the JSONL file as one serialized
// publication: guard, snapshot, temp file, record pending, re-check, rename,
// promote, retire dirty markers. It returns ErrDiverged without touching the
// file when the current content was written by somebody else.
func Publish(ctx context.Context, store MetadataStore, jsonlPath string, buildIssues BuildIssuesFunc, opts Options) (*Result, error) {
	keys := newKeys(opts.KeySuffix)

	lock, err := acquirePublishLock(ctx, jsonlPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()

	guardStatus, guardHash, err := contentStateLocked(ctx, store, jsonlPath, keys, opts)
	if err != nil {
		return nil, err
	}
	switch guardStatus {
	case StatusDiverged:
		return nil, fmt.Errorf("%w: %s", ErrDiverged, jsonlPath)
	case StatusNoMetadata:
		// An existing file no hash was ever recorded for must be imported
		// before it can be replaced; publication onto no metadata is only legal
		// when there is no file yet.
		return nil, fmt.Errorf("%w: %s exists but was never recorded", ErrDiverged, jsonlPath)
	}

	// Dirty markers are read before the issues (never after): a mutation
	// landing between the two reads then either refreshes a marker this
	// snapshot no longer matches, or is included in the issues below.
	dirtySnapshots, dirtyStore, err := snapshotDirty(ctx, store)
	if err != nil {
		return nil, err
	}

	issues, err := buildIssues(ctx)
	if err != nil {
		return nil, err
	}

	tempPath, contentHash, err := writeTemp(jsonlPath, issues)
	if err != nil {
		return nil, err
	}
	renamed, crashed := false, false
	defer func() {
		// A simulated crash stands in for process death, which cleans up
		// nothing; every real abort removes the temp file it created.
		if !renamed && !crashed {
			_ = os.Remove(tempPath)
		}
	}()

	if err := store.SetMetadata(ctx, keys.pending, contentHash); err != nil {
		// Revealing bytes no hash records recreates the false-stale state, so
		// a failure here aborts the publication instead of renaming anyway.
		return nil, fmt.Errorf("failed to record %s: %w", keys.pending, err)
	}
	if err := simulateCrash(crashAfterPending); err != nil {
		crashed = true
		return nil, err
	}

	// A writer that ignores this lock (git checkout, a manual edit) may have
	// landed since the guard. Re-check the bytes we are about to replace.
	if err := verifyGuardHash(jsonlPath, guardHash); err != nil {
		clearPending(ctx, store, keys, opts)
		return nil, err
	}

	if err := os.Rename(tempPath, jsonlPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}
	renamed = true
	if err := os.Chmod(jsonlPath, jsonlFileMode); err != nil {
		opts.warnf("failed to set permissions on %s: %v", jsonlPath, err)
	}
	if err := simulateCrash(crashAfterRename); err != nil {
		return nil, err
	}

	if err := promote(ctx, store, keys, contentHash, opts); err != nil {
		return nil, err
	}
	if err := simulateCrash(crashAfterPendingDelete); err != nil {
		return nil, err
	}

	// Retiring the markers is what stops the exported-forever loop. A failure
	// here costs one redundant export next cycle, so it warns rather than
	// failing a publication that has already succeeded.
	if dirtyStore != nil && len(dirtySnapshots) > 0 {
		if err := dirtyStore.ClearDirtyIssuesIfUnchanged(ctx, dirtySnapshots); err != nil {
			opts.warnf("failed to clear dirty issues after export: %v", err)
		}
	}
	if err := simulateCrash(crashAfterClear); err != nil {
		return nil, err
	}

	return &Result{IssueCount: len(issues), Hash: contentHash}, nil
}

// snapshotDirty reads the dirty markers when the store can report them. A store
// without per-issue marks (the memory backend) publishes without clearing.
func snapshotDirty(ctx context.Context, store MetadataStore) ([]DirtySnapshot, DirtySnapshotStore, error) {
	dirtyStore, ok := store.(DirtySnapshotStore)
	if !ok {
		return nil, nil, nil
	}
	snapshots, err := dirtyStore.GetDirtyIssueSnapshots(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to snapshot dirty issues: %w", err)
	}
	return snapshots, dirtyStore, nil
}

// verifyGuardHash re-reads the file we are about to replace and compares it
// against the hash the guard saw.
func verifyGuardHash(jsonlPath, guardHash string) error {
	currentHash, err := HashFile(jsonlPath)
	if errors.Is(err, fs.ErrNotExist) {
		currentHash = ""
	} else if err != nil {
		return fmt.Errorf("failed to re-hash %s: %w", jsonlPath, err)
	}
	if currentHash != guardHash {
		return fmt.Errorf("%w: %s changed during publication", ErrDiverged, jsonlPath)
	}
	return nil
}

// writeTemp writes the issues to a temp file beside the target, hashing as it
// writes, and returns the temp path and the content hash. The caller renames or
// removes it.
func writeTemp(jsonlPath string, issues []*types.Issue) (string, string, error) {
	dir := filepath.Dir(jsonlPath)
	base := filepath.Base(jsonlPath)
	tempFile, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Sorting here rather than in each caller is what makes the content hash a
	// function of the issues alone: two exports of the same data produce the
	// same bytes regardless of the order the caller assembled them in.
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })

	hasher := NewHasher()
	encoder := json.NewEncoder(io.MultiWriter(tempFile, hasher))
	for _, issue := range issues {
		if err := encoder.Encode(issue); err != nil {
			_ = tempFile.Close()
			_ = os.Remove(tempPath)
			return "", "", fmt.Errorf("failed to encode issue %s: %w", issue.ID, err)
		}
	}

	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", "", fmt.Errorf("failed to close temp file: %w", err)
	}
	return tempPath, HashSum(hasher), nil
}

// RecordImport records that the database now holds the content an import just
// parsed. importedHash must be the sha256 of the bytes that import actually
// read, never a fresh hash of the file: if the file changed after the read, the
// repository correctly reads diverged and the next import picks the new bytes
// up.
//
// It runs for the canonical default JSONL only - a `bd import -i backup.jsonl`
// must not claim the backup's content as the repository's - and before any
// post-import export callback, so the callback's own publication finds a
// recorded state.
func RecordImport(ctx context.Context, store MetadataStore, jsonlPath, importedHash string, opts Options) error {
	keys := newKeys(opts.KeySuffix)

	lock, err := acquirePublishLock(ctx, jsonlPath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	if err := store.SetMetadata(ctx, keys.committed, importedHash); err != nil {
		return fmt.Errorf("failed to record %s: %w", keys.committed, err)
	}
	if err := store.SetJSONLFileHash(ctx, importedHash); err != nil {
		opts.warnf("failed to update JSONL file hash: %v", err)
	}
	if err := store.SetMetadata(ctx, keys.importTime, time.Now().Format(time.RFC3339Nano)); err != nil {
		opts.warnf("failed to update %s: %v", keys.importTime, err)
	}
	// A pending hash left by a crashed publication is stale the moment an
	// import commits a different content hash.
	clearPending(ctx, store, keys, opts)
	return nil
}
