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

**Freshness is content, never mtime** (w2_stale-race, 2026-08-05): `internal/jsonlpub` is the sole authority for deciding whether `.beads/issues.jsonl` agrees with the database, and for publishing to it. Never add a hand-rolled `jsonl_content_hash` comparison or an mtime check — that is the exact defect this item removed, and it was found in three separate callers. A file is fresh when its sha256 equals `jsonl_content_hash` **or** `jsonl_pending_hash`; publication writes pending before the rename and promotes after, so no window and no crash state reads stale. Any new code that writes the canonical JSONL calls `Publish`; any code that imports it calls `RecordImport` with the hash of the bytes it actually parsed, never a re-hash of the file. `cmd/bd/integrity.go`'s `isJSONLNewer`/`isJSONLNewerWithStore` are dead mtime helpers (only callers are each other) — do not revive them.

`hooks/hooks.go`: Hooks are fire-and-forget (async). Check hook exists and is executable before running. Symlinks work - `os.Stat` follows them.

**bd never writes AGENTS.md** (w1_agents-md-guard, 2026-07-16): the "Landing the Plane" AGENTS.md writer was deleted from `init.go`, and `setup factory` prints its integration block instead of writing the file. Upstream (steveyegge/beads) still ships the feature — a future upstream sync that reintroduces it breaks `TestInitDoesNotTouchAgentsFile` (`cmd/bd/init_test.go`) and `TestFactoryDoesNotTouchAgentsFile` (`cmd/bd/setup/factory_test.go`); keep those tests, resolve merge conflicts by keeping the deletion.

## Patterns

**Mutation → Hook flow**: RPC mutation → `MutationChan()` → event loop receives → `exportDebouncer.Trigger()` → `hookRunner.Run(event, issue)`

**Claiming work: always `bd claim`, never `bd update --status in_progress --assignee <who>`** (w3_atomic-claim, 2026-08-07): the `update` form is the unconditioned race this verb removed. `update` reads the issue, decides in Go, then writes, with the read outside the write transaction, so two agents that read the same unassigned issue both decide "free" and both write; the second silently overwrites the first and two agents work the same issue believing they own it. `bd claim` puts the read, the decision and the write in one `BEGIN IMMEDIATE` transaction (`internal/storage/sqlite/claim.go`), which SQLite serializes against every other write, so exactly one of two concurrent claimants wins. The loser gets exit 3, not 1, with `deny_reason=held` (a live rival, retry later) or `deny_reason=status` (the status is not claimable, skip it); exit 1 stays a real error. Claiming an issue you already hold renews it, and `--lease 45m` bounds the hold so a dead agent's claim can be stolen rather than stranding the issue forever. A no-lease claim never expires, so scripts that can die should always pass `--lease`.

**Hook directory**: `.beads/hooks/` with executables named `on_create`, `on_update`, `on_close`. Can be symlinks to shared script.
