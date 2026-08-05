package autoimport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/beads/internal/debug"
	"github.com/steveyegge/beads/internal/jsonlpub"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
)

// Notifier handles user notifications during import
type Notifier interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// stderrNotifier implements Notifier using stderr
type stderrNotifier struct {
	debug bool
}

func (n *stderrNotifier) Debugf(format string, args ...interface{}) {
	if n.debug {
		fmt.Fprintf(os.Stderr, "Debug: "+format+"\n", args...)
	}
}

func (n *stderrNotifier) Infof(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func (n *stderrNotifier) Warnf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Warning: "+format+"\n", args...)
}

func (n *stderrNotifier) Errorf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}

// NewStderrNotifier creates a notifier that writes to stderr
func NewStderrNotifier(debug bool) Notifier {
	return &stderrNotifier{debug: debug}
}

// ImportFunc is called to perform the actual import after detecting staleness
// It receives the parsed issues and should return created/updated counts and ID mappings
// The ID mapping maps old IDs -> new IDs for collision resolution
type ImportFunc func(ctx context.Context, issues []*types.Issue) (created, updated int, idMapping map[string]string, err error)

// AutoImportIfNewer checks if JSONL is newer than last import and imports if needed
// dbPath is the full path to the database file (e.g., /path/to/.beads/bd.db)
func AutoImportIfNewer(ctx context.Context, store storage.Storage, dbPath string, notify Notifier, importFunc ImportFunc, onChanged func(needsFullExport bool)) error {
	if notify == nil {
		notify = NewStderrNotifier(debug.Enabled())
	}

	// Find JSONL using database directory
	dbDir := filepath.Dir(dbPath)
	jsonlPath := utils.FindJSONLInDir(dbDir)
	if jsonlPath == "" {
		notify.Debugf("auto-import skipped, JSONL not found")
		return nil
	}

	jsonlData, err := os.ReadFile(jsonlPath) // #nosec G304 - controlled path from config
	if err != nil {
		notify.Debugf("auto-import skipped, JSONL not readable: %v", err)
		return nil
	}

	currentHash := jsonlpub.HashBytes(jsonlData)

	// Try new key first, fall back to old key for migration (bd-39o)
	lastHash, err := store.GetMetadata(ctx, "jsonl_content_hash")
	if err != nil || lastHash == "" {
		lastHash, err = store.GetMetadata(ctx, "last_import_hash")
		if err != nil {
			notify.Debugf("metadata read failed (%v), treating as first import", err)
			lastHash = ""
		}
	}

	if currentHash == lastHash {
		notify.Debugf("auto-import skipped, JSONL unchanged (hash match)")
		// Content already imported, but recording it again refreshes the import
		// timestamp so a mtime-only change (git pull, touch) stops looking new.
		recordImport(ctx, store, jsonlPath, currentHash, notify)
		return nil
	}

	notify.Debugf("auto-import triggered (hash changed)")

	if err := checkForMergeConflicts(jsonlData, jsonlPath); err != nil {
		notify.Errorf("%v", err)
		return err
	}

	allIssues, err := parseJSONL(jsonlData, notify)
	if err != nil {
		notify.Errorf("Auto-import skipped: %v", err)
		return err
	}

	created, updated, idMapping, err := importFunc(ctx, allIssues)
	if err != nil {
		notify.Errorf("Auto-import failed: %v", err)
		return err
	}

	// Show detailed remapping if any
	showRemapping(allIssues, idMapping, notify)

	// Record before the callback: onChanged usually exports, and its publication
	// must find the imported content already recorded or it reads diverged.
	recordImport(ctx, store, jsonlPath, currentHash, notify)

	changed := (created + updated + len(idMapping)) > 0
	if changed && onChanged != nil {
		needsFullExport := len(idMapping) > 0
		onChanged(needsFullExport)
	}

	return nil
}

// recordImport records the content this import parsed. currentHash covers the
// bytes actually read: hashing the file again here would bless a rewrite that
// landed during the import and hide it from the next one.
//
// A failure warns rather than propagating: the issues are in the database, and
// the worst outcome is that the next operation imports the same content again.
func recordImport(ctx context.Context, store storage.Storage, jsonlPath, currentHash string, notify Notifier) {
	if err := jsonlpub.RecordImport(ctx, store, jsonlPath, currentHash, jsonlpub.Options{Warnf: notify.Warnf}); err != nil {
		notify.Warnf("failed to record imported JSONL content: %v", err)
		notify.Warnf("This may cause auto-import to retry the same import on next operation.")
	}
}

// showRemapping displays ID remapping details
func showRemapping(allIssues []*types.Issue, idMapping map[string]string, notify Notifier) {
	if len(idMapping) == 0 {
		return
	}

	// Build title lookup map
	titleByID := make(map[string]string)
	for _, issue := range allIssues {
		titleByID[issue.ID] = issue.Title
	}

	// Sort by old ID for consistent output
	type mapping struct {
		oldID string
		newID string
	}
	mappings := make([]mapping, 0, len(idMapping))
	for oldID, newID := range idMapping {
		mappings = append(mappings, mapping{oldID, newID})
	}
	
	// Sort by old ID
	for i := 0; i < len(mappings); i++ {
		for j := i + 1; j < len(mappings); j++ {
			if mappings[i].oldID > mappings[j].oldID {
				mappings[i], mappings[j] = mappings[j], mappings[i]
			}
		}
	}

	maxShow := 10
	numRemapped := len(mappings)
	if numRemapped < maxShow {
		maxShow = numRemapped
	}

	notify.Infof("\nAuto-import: remapped %d colliding issue(s) to new IDs:", numRemapped)
	for i := 0; i < maxShow; i++ {
		m := mappings[i]
		title := titleByID[m.oldID]
		notify.Infof("  %s → %s (%s)", m.oldID, m.newID, title)
	}
	if numRemapped > maxShow {
		notify.Infof("  ... and %d more", numRemapped-maxShow)
	}
	notify.Infof("")
}

func checkForMergeConflicts(jsonlData []byte, jsonlPath string) error {
	lines := bytes.Split(jsonlData, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("<<<<<<< ")) ||
			bytes.Equal(trimmed, []byte("=======")) ||
			bytes.HasPrefix(trimmed, []byte(">>>>>>> ")) {
			return fmt.Errorf("❌ Git merge conflict detected in %s\n\n"+
				"The JSONL file contains unresolved merge conflict markers.\n"+
				"This prevents auto-import from loading your issues.\n\n"+
				"To resolve:\n"+
				"  1. Resolve the merge conflict in your Git client, OR\n"+
				"  2. Export from database to regenerate clean JSONL:\n"+
				"     bd export -o %s\n\n"+
				"After resolving, commit the fixed JSONL file.\n", jsonlPath, jsonlPath)
		}
	}
	return nil
}

func parseJSONL(jsonlData []byte, _ Notifier) ([]*types.Issue, error) {
	scanner := bufio.NewScanner(bytes.NewReader(jsonlData))
	scanner.Buffer(make([]byte, 0, 1024), 2*1024*1024)
	var allIssues []*types.Issue
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if line == "" {
			continue
		}

		var issue types.Issue
		if err := json.Unmarshal([]byte(line), &issue); err != nil {
			snippet := line
			if len(snippet) > 80 {
				snippet = snippet[:80] + "..."
			}
			return nil, fmt.Errorf("parse error at line %d: %v\nSnippet: %s", lineNo, err, snippet)
		}

		if issue.Status == types.StatusClosed && issue.ClosedAt == nil {
			now := time.Now()
			issue.ClosedAt = &now
		}

		allIssues = append(allIssues, &issue)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	return allIssues, nil
}

// CheckStaleness reports whether the JSONL file holds content the database has
// not imported. dbPath is the full path to the database file.
//
// Staleness is a question about content, not about clocks: a file rewritten
// with the same bytes, or republished by an export, is not stale no matter how
// new its mtime is. Only content nobody recorded makes the database stale.
//
// Returns:
//   - (true, nil) if the file holds content this database never imported
//   - (false, nil) if the content is recorded, or there is no JSONL yet
//   - (false, err) if an abnormal error occurred (file system issues, permissions, etc.)
func CheckStaleness(ctx context.Context, store storage.Storage, dbPath string) (bool, error) {
	// Find JSONL using database directory
	dbDir := filepath.Dir(dbPath)
	jsonlPath := utils.FindJSONLInDir(dbDir)
	if jsonlPath == "" {
		// No JSONL yet - expected for a new repo
		return false, nil
	}

	status, err := jsonlpub.ContentState(ctx, store, jsonlPath, "")
	if err != nil {
		return false, err
	}

	// A file no hash was ever recorded for is the first-run state, which every
	// caller of this predicate has always treated as fresh: reporting it stale
	// would fail-stop a brand new repository.
	return status == jsonlpub.StatusDiverged, nil
}
