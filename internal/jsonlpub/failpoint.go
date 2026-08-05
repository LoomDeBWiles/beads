package jsonlpub

// Failpoints let the protocol tests stop a publication exactly where a process
// death could stop it, and then assert what a reader sees in that state. Every
// step between the first metadata write and the last one is reachable.
//
// crashAt is nil in production, so simulateCrash compiles to a nil check.

type failpoint string

const (
	// crashAfterPending: pending hash recorded, file not yet replaced.
	crashAfterPending failpoint = "after-pending"
	// crashAfterRename: file replaced, nothing promoted yet.
	crashAfterRename failpoint = "after-rename"
	// crashAfterCommitted: committed hash written, file hash not yet.
	crashAfterCommitted failpoint = "after-committed"
	// crashAfterFileHash: JSONL file hash written, timestamp not yet.
	crashAfterFileHash failpoint = "after-file-hash"
	// crashAfterImportTime: timestamp written, pending hash not yet dropped.
	crashAfterImportTime failpoint = "after-import-time"
	// crashAfterPendingDelete: promotion complete, dirty markers still set.
	crashAfterPendingDelete failpoint = "after-pending-delete"
	// crashAfterClear: dirty markers retired; the publication is complete.
	crashAfterClear failpoint = "after-clear"
)

// crashAt, when set by a test, returns a non-nil error at the failpoint the
// test wants to stop at. The caller returns immediately without cleanup,
// exactly as a dying process would.
var crashAt func(failpoint) error

func simulateCrash(point failpoint) error {
	if crashAt == nil {
		return nil
	}
	return crashAt(point)
}
