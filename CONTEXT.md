# Context

## Commands
| Task | Command |
|------|---------|
| Build | `go build ./cmd/bd/` |
| Install | `go install ./cmd/bd/` |
| Test | `go test ./...` |
| Test specific | `go test ./cmd/bd/ -run TestName` |

## Architecture

CLI tool (`bd`) for dependency-aware issue tracking. SQLite storage with JSONL git sync.

Key packages:
- `cmd/bd/` - CLI commands and daemon
- `internal/storage/` - SQLite backend
- `internal/rpc/` - Daemon RPC server
- `internal/hooks/` - Hook execution (on_create, on_update, on_close)

Daemon modes:
- **Event-driven** (default): Watches for mutations via RPC, debounces exports
- **Polling**: Periodic sync checks (legacy)

## Gotchas

`daemon_event_loop.go`: Stale daemons can accumulate. `bd daemon --stop` only kills daemon for current workspace. Use `bd daemon --stop-all` to kill all system-wide, then `bd daemon --start` fresh. Symptoms: code changes don't take effect, hooks don't fire.

`init.go:createHooks()`: After creating hooks, `bd init` now auto-restarts the daemon so hooks are picked up immediately. Previously required manual `bd daemon --stop && bd daemon --start`.

`hooks/hooks.go`: Hooks are fire-and-forget (async). Check hook exists and is executable before running. Symlinks work - `os.Stat` follows them.

## Patterns

**Mutation → Hook flow**: RPC mutation → `MutationChan()` → event loop receives → `exportDebouncer.Trigger()` → `hookRunner.Run(event, issue)`

**Hook directory**: `.beads/hooks/` with executables named `on_create`, `on_update`, `on_close`. Can be symlinks to shared script.
