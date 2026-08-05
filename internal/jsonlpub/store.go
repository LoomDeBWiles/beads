package jsonlpub

import (
	"context"
	"time"
)

// MetadataStore is the database surface the file-content contract needs: the
// hashes and the import timestamp. The full storage.Storage interface satisfies
// it, so callers pass their store unchanged.
type MetadataStore interface {
	GetMetadata(ctx context.Context, key string) (string, error)
	SetMetadata(ctx context.Context, key, value string) error
	SetJSONLFileHash(ctx context.Context, fileHash string) error
}

// DirtySnapshot is one row of the dirty-issue table as it looked when a
// publication started: the issue and the exact mark that made it dirty.
type DirtySnapshot struct {
	ID       string
	MarkedAt time.Time
}

// DirtySnapshotStore is the narrow storage capability a publication needs to
// retire the dirty markers it exported. It is deliberately not part of the
// shared storage.Storage interface: only the SQLite backend records a
// per-issue marked_at, and the memory backend's boolean dirty map cannot
// satisfy it. Publish type-asserts for it and skips dirty-clearing when the
// store does not implement it.
type DirtySnapshotStore interface {
	// GetDirtyIssueSnapshots returns every dirty issue with its current mark.
	GetDirtyIssueSnapshots(ctx context.Context) ([]DirtySnapshot, error)

	// ClearDirtyIssuesIfUnchanged deletes exactly the rows still carrying the
	// snapshotted mark. A row re-marked since the snapshot (an edit made while
	// the export was running) keeps a newer mark, does not match, and stays
	// dirty for the next publication.
	ClearDirtyIssuesIfUnchanged(ctx context.Context, snapshots []DirtySnapshot) error
}
