# Beads architecture

Beads stores issues in SQLite and exchanges them through JSONL. Root repo rules apply in this directory.

## Architecture Overview

### Three-Layer Design

1. **Storage Layer** (`internal/storage/`)
   - Interface-based design in `storage.go`
   - SQLite implementation in `storage/sqlite/`
   - Memory backend in `storage/memory/` for testing
   - Extensions can add custom tables via `UnderlyingDB()` (see EXTENDING.md)

2. **RPC Layer** (`internal/rpc/`)
   - Client/server architecture using Unix domain sockets (Windows named pipes)
   - Protocol defined in `protocol.go`
   - Server split into focused files: `server_core.go`, `server_issues_epics.go`, `server_labels_deps_comments.go`, etc.
   - Per-workspace daemons communicate via `.beads/bd.sock`

3. **CLI Layer** (`cmd/bd/`)
   - Cobra-based commands (one file per command: `create.go`, `list.go`, etc.)
   - Commands try daemon RPC first, fall back to direct database access
   - All commands support `--json` for programmatic use
   - Main entry point in `main.go`

### Distributed Database Pattern

SQLite and JSONL synchronize through these paths:

```
SQLite DB (.beads/beads.db, gitignored)
    ↕ auto-sync (5s debounce)
JSONL (.beads/issues.jsonl, git-tracked)
    ↕ git push/pull
Remote JSONL (shared across machines)
```

- **Write path**: CLI → SQLite → JSONL export → git commit
- **Read path**: git pull → JSONL import → SQLite → CLI
- **Hash-based IDs**: Automatic collision prevention (v0.20+)

Core implementation:
- Export: `cmd/bd/export.go`, `cmd/bd/autoflush.go`
- Import: `cmd/bd/import.go`, `cmd/bd/autoimport.go`
- Collision detection: `internal/importer/importer.go`

### Key Data Types

See `internal/types/types.go`:
- `Issue`: Core work item (title, description, status, priority, etc.)
- `Dependency`: Four types (blocks, related, parent-child, discovered-from)
- `Label`: Flexible tagging system
- `Comment`: Threaded discussions
- `Event`: Full audit trail

### Daemon Architecture

Each workspace gets its own daemon process:
- Auto-starts on first command (unless disabled)
- Handles auto-sync, batching, and background operations
- Socket at `.beads/bd.sock` (or `.beads/bd.pipe` on Windows)
- Version checking prevents mismatches after upgrades
- Manage with `bd daemons` command (see BD_AGENT_REFERENCE.md)

## Common Development Commands

```bash
# Build and test
go build -o bd ./cmd/bd
go test ./...
go test -coverprofile=coverage.out ./...

# Run linter (baseline warnings documented in docs/LINTING.md)
golangci-lint run ./...

# Version management
./scripts/bump-version.sh 0.9.3 --commit

# Local testing
./bd init --prefix test
./bd create "Test issue" -p 1
./bd ready
```

## Testing Philosophy

- Unit tests live next to implementation (`*_test.go`)
- Integration tests use real SQLite databases (`:memory:` or temp files)
- Script-based tests in `cmd/bd/testdata/*.txt` (see `scripttest_test.go`)
- RPC layer has extensive isolation and edge case coverage

## Important Notes

- Use `bd --no-daemon` in git worktrees (see WORKTREES.md for why)
- Preview duplicates with `bd duplicates --dry-run`; read `BD_ISSUE_GUIDE.md` before merging them.

## Key Files

- **BD_AGENT_REFERENCE.md** - Running bd: commands, MCP, configuration, daemons
- **BD_ISSUE_GUIDE.md** - Writing issues: descriptions, types, priorities, dependencies
- **../AGENT_INSTRUCTIONS.md** - Development and release procedures
- **README.md** - User-facing documentation
- **ADVANCED.md** - Advanced features (rename, merge, compaction)
- **EXTENDING.md** - How to add custom tables to the database
- **LABELS.md** - Complete label system guide
- **CONFIG.md** - Configuration system

## When Adding Features

See ../AGENT_INSTRUCTIONS.md "Adding a New Command" and "Adding Storage Features" sections for step-by-step guidance.
