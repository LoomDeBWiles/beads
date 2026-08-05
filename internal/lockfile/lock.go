package lockfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Polling bounds for AcquireContext. The non-blocking lock primitive is the only
// one available on every supported platform, so waiting means retrying; the
// interval grows from responsive to cheap while contention lasts.
const (
	acquireMinInterval = 1 * time.Millisecond
	acquireMaxInterval = 50 * time.Millisecond
)

// Lock is an acquired exclusive advisory lock on a file. Release it exactly once.
type Lock struct {
	file *os.File
}

// AcquireContext acquires an exclusive advisory lock on lockPath, creating the
// file (and not its parent directories) if it does not exist. It waits for a
// holder to release, returning ctx.Err() if the context is cancelled or its
// deadline passes first, so a wedged holder cannot hang a caller past its own
// deadline.
func AcquireContext(ctx context.Context, lockPath string) (*Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Read-write access is required by LockFileEx on Windows.
	// #nosec G304 - controlled path from config
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", lockPath, err)
	}

	interval := acquireMinInterval
	for {
		err := flockExclusive(file)
		if err == nil {
			return &Lock{file: file}, nil
		}
		if !errors.Is(err, errDaemonLocked) {
			_ = file.Close()
			return nil, fmt.Errorf("failed to lock %s: %w", lockPath, err)
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}

		if interval < acquireMaxInterval {
			interval *= 2
			if interval > acquireMaxInterval {
				interval = acquireMaxInterval
			}
		}
	}
}

// Release unlocks and closes the lock file. Closing the descriptor releases the
// lock on every supported platform; the explicit unlock keeps the intent visible
// and surfaces unlock errors.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := FlockUnlock(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// LockInfo represents the metadata stored in the daemon.lock file
type LockInfo struct {
	PID       int       `json:"pid"`
	ParentPID int       `json:"parent_pid,omitempty"`
	Database  string    `json:"database"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
}

// TryDaemonLock attempts to acquire and immediately release the daemon lock
// to check if a daemon is running. Returns true if daemon is running.
// Falls back to PID file check for backward compatibility with pre-lock daemons.
//
// This is a cheap probe operation that should be called before attempting
// RPC connections to avoid unnecessary connection timeouts.
func TryDaemonLock(beadsDir string) (running bool, pid int) {
	lockPath := filepath.Join(beadsDir, "daemon.lock")

	// Open lock file with read-write access (required for LockFileEx on Windows)
	// #nosec G304 - controlled path from config
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		// No lock file - could be old daemon without lock support
		// Fall back to PID file check for backward compatibility
		return checkPIDFile(beadsDir)
	}
	defer func() { _ = f.Close() }()

	// Try to acquire lock non-blocking
	if err := flockExclusive(f); err != nil {
		if err == errDaemonLocked {
			// Lock is held - daemon is running
			// Try to read PID from JSON format (best effort)
			_, _ = f.Seek(0, 0)
			var lockInfo LockInfo
			if err := json.NewDecoder(f).Decode(&lockInfo); err == nil {
				pid = lockInfo.PID
			} else {
				// Fallback: try reading as plain integer (old format)
				_, _ = f.Seek(0, 0)
				data := make([]byte, 32)
				n, _ := f.Read(data)
				if n > 0 {
					_, _ = fmt.Sscanf(string(data[:n]), "%d", &pid)
				}
				// Fallback to PID file if we couldn't read PID from lock file
				if pid == 0 {
					_, pid = checkPIDFile(beadsDir)
				}
			}
			return true, pid
		}
		// Other errors mean we can't determine status
		return false, 0
	}

	// We got the lock - no daemon running
	// Release immediately (file close will do this)
	return false, 0
}

// checkPIDFile checks if a daemon is running by reading the PID file.
// This is used for backward compatibility with pre-lock daemons.
func checkPIDFile(beadsDir string) (running bool, pid int) {
	pidFile := filepath.Join(beadsDir, "daemon.pid")
	// #nosec G304 - controlled path from config
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false, 0
	}

	pidVal, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}

	if !isProcessRunning(pidVal) {
		return false, 0
	}

	return true, pidVal
}

// ReadLockInfo reads and parses the daemon lock file
// Returns lock info if available, or error if file doesn't exist or can't be parsed
func ReadLockInfo(beadsDir string) (*LockInfo, error) {
	lockPath := filepath.Join(beadsDir, "daemon.lock")
	
	// #nosec G304 - controlled path from config
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, err
	}
	
	var lockInfo LockInfo
	if err := json.Unmarshal(data, &lockInfo); err != nil {
		// Try parsing as old format (plain PID)
		var pid int
		if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil {
			return &LockInfo{PID: pid}, nil
		}
		return nil, fmt.Errorf("cannot parse lock file: %w", err)
	}
	
	return &lockInfo, nil
}
