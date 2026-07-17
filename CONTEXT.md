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

`init.go` hook initializer RETIRED (w731, 2026-07-17): the `createHooks()` auto-render helper and the daemon auto-restart block were deleted from `bd init`. `bd init` no longer plants `.beads/hooks/{on_create,on_update,on_close}` (they ran `spec render -o output`, which dirtied tracked `.beads/output/` and blocked wt-merge) and no longer restarts the daemon. The daemon still *runs* whatever hooks exist in `.beads/hooks/`; init just stops creating them. Regression test: `TestInitDoesNotCreateHooks` (`cmd/bd/init_test.go`) asserts `.beads/hooks/` absent after init. A future upstream sync that reintroduces auto-render breaks that test; keep the deletion.

`hooks/hooks.go`: Hooks are fire-and-forget (async). Check hook exists and is executable before running. Symlinks work - `os.Stat` follows them.

**bd never writes AGENTS.md** (w1_agents-md-guard, 2026-07-16): the "Landing the Plane" AGENTS.md writer was deleted from `init.go`, and `setup factory` prints its integration block instead of writing the file. Upstream (steveyegge/beads) still ships the feature — a future upstream sync that reintroduces it breaks `TestInitDoesNotTouchAgentsFile` (`cmd/bd/init_test.go`) and `TestFactoryDoesNotTouchAgentsFile` (`cmd/bd/setup/factory_test.go`); keep those tests, resolve merge conflicts by keeping the deletion.

## Patterns

**Mutation → Hook flow**: RPC mutation → `MutationChan()` → event loop receives → `exportDebouncer.Trigger()` → `hookRunner.Run(event, issue)`

**Hook directory**: `.beads/hooks/` with executables named `on_create`, `on_update`, `on_close`. Can be symlinks to shared script.
