# Codemap: beads (bd)

> Dependency-aware issue tracker for AI-supervised coding workflows. CLI tool with SQLite storage, JSONL git sync, and background daemon.

## Architecture

```
CLI (cmd/bd/)
  │
  ├─ direct mode ──► SQLite storage (internal/storage/sqlite/)
  │
  └─ daemon mode ──► RPC client ──► daemon (Unix socket) ──► SQLite storage
                     (internal/rpc/)    │
                                        ├─ event loop (debounced export)
                                        ├─ hook runner (internal/hooks/)
                                        └─ file watcher (JSONL auto-import)

Storage layer:
  SQLite DB (.beads/beads.db) ◄──export/import──► JSONL (.beads/issues.jsonl)
                                                    │
                                                    └── git sync (commit/push/pull)
```

## Key Files

| File | Responsibility |
|------|----------------|
| `beads.go` | Public Go API — re-exports Storage, types, constants |
| `cmd/bd/main.go` | CLI root, daemon/direct routing, store initialization |
| `cmd/bd/daemon.go` | Daemon start/stop, event-driven mode |
| `cmd/bd/daemon_event_loop.go` | Mutation → debounce → export → hook pipeline |
| `cmd/bd/daemon_sync.go` | Git sync logic (export → commit → push → pull → import) |
| `cmd/bd/sync.go` | `bd sync` command — full sync cycle |
| `cmd/bd/init.go` | `bd init` — database creation, git hooks, merge driver |
| `cmd/bd/create.go` | `bd create` — issue creation with deps/labels |
| `cmd/bd/show.go` | `bd show` — issue display (text + JSON) |
| `cmd/bd/list.go` | `bd list` — filtered issue listing |
| `cmd/bd/ready.go` | `bd ready` — unblocked work queue |
| `cmd/bd/dep.go` | `bd dep` — dependency add/remove/tree |
| `cmd/bd/export.go` | DB → JSONL export |
| `cmd/bd/import.go` | JSONL → DB import |
| `cmd/bd/doctor.go` | `bd doctor` — diagnostic checks and auto-fixes |
| `cmd/bd/template.go` | Template molecule management |
| `cmd/bd/mol.go` | `bd mol` — molecule subcommands |
| `cmd/bd/pour.go` | `bd pour` — instantiate template → persistent work |
| `cmd/bd/wisp.go` | `bd wisp` — instantiate template → ephemeral work |
| `cmd/bd/prime.go` | `bd prime` — inject compact context into agent sessions |
| `cmd/bd/hooks.go` | `bd hooks install` — git hook management |
| `internal/storage/storage.go` | `Storage` interface — all backend operations |
| `internal/storage/sqlite/sqlite.go` | SQLite backend entry point |
| `internal/storage/sqlite/store.go` | SQLiteStorage struct, constructor |
| `internal/storage/sqlite/migrations.go` | Schema migration runner (26 migrations) |
| `internal/types/types.go` | Core types: Issue, Dependency, Status, IssueType |
| `internal/rpc/protocol.go` | RPC request/response format (JSON over Unix socket) |
| `internal/rpc/client.go` | RPC client used by CLI commands |
| `internal/rpc/server.go` | RPC server running inside daemon |
| `internal/hooks/hooks.go` | Hook runner — executes `.beads/hooks/on_{create,update,close}` |
| `internal/importer/importer.go` | JSONL import with orphan handling strategies |
| `internal/export/executor.go` | Export with retry/policy (strict, lenient) |
| `internal/molecules/molecules.go` | Hierarchical template molecule loading |
| `internal/compact/compactor.go` | AI-powered issue summarization (Haiku) |
| `internal/syncbranch/syncbranch.go` | Sync-branch config for protected-branch workflows |
| `internal/routing/routing.go` | Maintainer vs contributor role detection |
| `internal/beads/beads.go` | Database/dir discovery, redirect support |
| `internal/config/config.go` | Config loading (YAML, env, git config) |
| `internal/lockfile/lock.go` | Cross-platform file locking |

## Data Flow

```
bd create → [validate] → [RPC or direct] → SQLite insert
  → dirty tracking → [daemon debounce] → JSONL export
  → [hook: on_create]

bd sync → export dirty → git add/commit → git push
  → git pull → JSONL import → reconcile DB

bd ready → query open issues → filter out blocked → sort by policy → display
```

## Configuration

| Setting | Default | Purpose |
|---------|---------|---------|
| `BEADS_DB` | `.beads/beads.db` | Database path override |
| `BEADS_DAEMON_MODE` | `events` | `events` (default) or `polling` |
| `BEADS_SYNC_BRANCH` | current branch | Branch for JSONL sync |
| `import.orphan_handling` | `allow` | `allow`/`resurrect`/`skip`/`strict` |
| `status.custom` | (none) | Comma-separated custom statuses |
| `ready.sort` | `hybrid` | `hybrid`/`priority`/`oldest` |

## Dependencies

| Package | Purpose |
|---------|---------|
| `spf13/cobra` | CLI framework |
| `spf13/viper` | Configuration |
| `ncruces/go-sqlite3` | SQLite via Wasm (no CGo) |
| `charmbracelet/huh` | Interactive forms |
| `charmbracelet/lipgloss` | Terminal styling |
| `fsnotify/fsnotify` | File watching (daemon) |
| `anthropics/anthropic-sdk-go` | AI compaction (Haiku) |
| `natefinch/lumberjack` | Log rotation |

## Testing

| Type | Location | Command |
|------|----------|---------|
| Unit + integration | `cmd/bd/*_test.go`, `internal/**/*_test.go` | `go test ./...` |
| Script tests | `cmd/bd/scripttest_test.go` | `go test ./cmd/bd/ -run TestScript` |
| Benchmarks | `internal/storage/sqlite/*_bench_test.go` | `make bench` |
| Integration | `tests/integration/` | `go test ./tests/integration/` |
| Skip list | `.test-skip` | Tests excluded by `scripts/test.sh` |

## Common Tasks

| Task | Solution |
|------|----------|
| Add a CLI command | Create `cmd/bd/<name>.go`, register in `main.go` via `rootCmd.AddCommand()` |
| Add an RPC operation | Add const in `internal/rpc/protocol.go`, handler in `server_*.go`, caller in `cmd/bd/<name>.go` |
| Add a storage method | Add to `internal/storage/storage.go` interface, implement in `sqlite/*.go` |
| Add a schema migration | Create `internal/storage/sqlite/migrations/0NN_<name>.go`, register in `migrations.go` |
| Add a hook event | Add constant in `internal/hooks/hooks.go`, fire from daemon event loop |
