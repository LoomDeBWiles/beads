# Detailed Agent Instructions for Beads Development

**For the repo rules and entry points, see [CLAUDE.md](CLAUDE.md)**

## Development Guidelines

### Code Standards

- **Go version**: 1.24+
- **Linting**: `golangci-lint run ./...` (baseline warnings documented in [docs/LINTING.md](docs/LINTING.md))
- **Testing**: All new features need tests (`go test -short ./...` for local, full tests run in CI)
- **Documentation**: Update relevant .md files

### File Organization

```
beads/
├── cmd/bd/              # CLI commands
├── internal/
│   ├── types/           # Core data types
│   └── storage/         # Storage layer
│       └── sqlite/      # SQLite implementation
├── examples/            # Integration examples
└── *.md                 # Documentation
```

### Testing Workflow

Use a scratch database for test issues.

**For manual testing**, use the `BEADS_DB` environment variable to point to a temporary database:

```bash
# Create test issues in isolated database
BEADS_DB=/tmp/test.db ./bd init --quiet --prefix test
BEADS_DB=/tmp/test.db ./bd create "Test issue" -p 1

# Or for quick testing
BEADS_DB=/tmp/test.db ./bd create "Test feature" -p 1
```

**For automated tests**, use `t.TempDir()` in Go tests:

```go
func TestMyFeature(t *testing.T) {
    tmpDir := t.TempDir()
    testDB := filepath.Join(tmpDir, ".beads", "beads.db")
    s := newTestStore(t, testDB)
    // ... test code
}
```

**Warning:** bd will warn you when creating issues with "Test" prefix in the production database. Always use `BEADS_DB` for manual testing.

### Before Committing

1. **Run tests**: `go test -short ./...` (full tests run in CI)
2. **Run linter**: `golangci-lint run ./...` (ignore baseline warnings)
3. **Update docs**: If you changed behavior, update README.md or other docs
4. **Commit**: Issues auto-sync to `.beads/issues.jsonl` and import after pull

### Commit Message Convention

When committing work for an issue, include the issue ID in parentheses at the end:

```bash
git commit -m "Fix auth validation bug (bd-abc)"
git commit -m "Add retry logic for database locks (bd-xyz)"
```

This enables `bd doctor` to detect **orphaned issues** - work that was committed but the issue wasn't closed. The doctor check cross-references open issues against git history to find these orphans.

### Git Workflow

bd batches issue changes:

- **Exports** to JSONL after CRUD operations (30-second debounce for batching)
- **Imports** from JSONL when it's newer than DB (e.g., after `git pull`)
- **Daemon commits/pushes** every 5 seconds (if `--auto-commit` / `--auto-push` enabled)

The 30-second debounce provides a **transaction window** for batch operations - multiple issue changes within 30 seconds get flushed together, avoiding commit spam.

### Git Integration

**Auto-sync**: bd automatically exports to JSONL (30s debounce), imports after `git pull`, and optionally commits/pushes.

**Protected branches**: Use `bd init --branch beads-metadata` to commit to separate branch. See [docs/PROTECTED_BRANCHES.md](docs/PROTECTED_BRANCHES.md).

**Git worktrees**: Use `bd --no-daemon`. See [docs/GIT_INTEGRATION.md](docs/GIT_INTEGRATION.md).

**Merge conflicts**: Reconcile both sides of the issue data before importing it; do not discard one side wholesale. See [docs/GIT_INTEGRATION.md](docs/GIT_INTEGRATION.md).

## Finishing a Session

1. **File beads issues for any remaining work** that needs follow-up.
2. **Ensure quality gates pass** if you changed code: `go test -short ./...` and `golangci-lint run ./...` (file a P0 issue for anything broken).
3. **Close finished issues** and update status.
4. **Flush pending issue changes** so the JSONL matches the database: `bd sync --flush-only` (exports without touching git).
5. **Hand off**: name the follow-up issue for the next session.

Committing, pulling, pushing, and merging follow the host's global rules, which
give the dispatcher the merge. Do not push from a work session.

If `.beads/issues.jsonl` conflicts during a merge, preserve both sides, reconcile the records, then run `bd import -i .beads/issues.jsonl`.

## Agent Session Workflow

Export pending issue changes before handing off:

```bash
bd sync --flush-only
```

This immediately exports pending changes to JSONL without the 30-second wait and
without git operations, so the tracked JSONL matches the database before anyone
commits.

**Example agent session:**

```bash
# Make multiple changes (batched in 30-second window)
bd create "Fix bug" -p 1
bd create "Add tests" -p 1
bd claim bd-42 --assignee <name> --lease 45m --json
bd close bd-40 --reason "Completed"

# Force immediate export at end of session
bd sync --flush-only
```

**Why this matters:**

- Without a flush, changes sit in the 30-second debounce window
- The JSONL can be stale when someone commits, which diverges workspaces

**Git hooks:** bd ships pre-commit, post-merge, pre-push, and post-checkout hooks
that keep the database and JSONL consistent, and `bd hooks install` plants them.
Do not run it in this checkout: `.git/hooks/pre-commit` is a bd shim wrapped by
the host's `wt-ensure` validator, and reinstalling overwrites the validator.

## Common Development Tasks

### Adding a New Command

1. Create file in `cmd/bd/`
2. Add to root command in `cmd/bd/main.go`
3. Implement with Cobra framework
4. Add `--json` flag for agent use
5. Add tests in `cmd/bd/*_test.go`
6. Document in README.md

### Adding Storage Features

1. Update schema in `internal/storage/sqlite/schema.go`
2. Add migration if needed
3. Update `internal/types/types.go` if new types
4. Implement in `internal/storage/sqlite/sqlite.go`
5. Add tests
6. Update export/import in `cmd/bd/export.go` and `cmd/bd/import.go`

### Adding Examples

1. Create directory in `examples/`
2. Add README.md explaining the example
3. Include working code
4. Link from `examples/README.md`
5. Mention in main README.md

## Building and Testing

```bash
# Build
go build -o bd ./cmd/bd

# Test (short - for local development)
go test -short ./...

# Test with coverage (full tests - for CI)
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run locally
./bd init --prefix test
./bd create "Test issue" -p 1
./bd ready
```

## Version Management

When the user asks to "bump the version" or mentions a new version number (e.g., "bump to 0.9.3"), use the version bump script:

```bash
# Preview changes (shows diff, doesn't commit)
./scripts/bump-version.sh 0.9.3

# Auto-commit the version bump
./scripts/bump-version.sh 0.9.3 --commit
```

**What it does:**

- Updates ALL version files (CLI, plugin, MCP server, docs) in one command
- Validates semantic versioning format
- Shows diff preview
- Verifies all versions match after update
- Creates standardized commit message

**User will typically say:**

- "Bump to 0.9.3"
- "Update version to 1.0.0"
- "Rev the project to 0.9.4"
- "Increment the version"

**You should:**

1. Run `./scripts/bump-version.sh <version> --commit`
2. Confirm all versions updated correctly
3. Land the commit on `main` through the host's normal branch and merge flow

**Files updated automatically:**

- `cmd/bd/version.go` - CLI version
- `.claude-plugin/plugin.json` - Plugin version
- `.claude-plugin/marketplace.json` - Marketplace version
- `integrations/beads-mcp/pyproject.toml` - MCP server version
- `README.md` - Documentation version
- `PLUGIN.md` - Version requirements

**Why this matters:** We had version mismatches (bd-66) when only `version.go` was updated. This script prevents that by updating all components atomically.

See `scripts/README.md` for more details.

## Release Process (Maintainers)

Releases run only when the user asks for one. The script below pushes commits and tags.

**Automated (Recommended):**

```bash
# One command to do everything (version bump, tests, tag, Homebrew update, local install)
./scripts/release.sh 0.9.3
```

This handles the entire release workflow automatically, including waiting ~5 minutes for GitHub Actions to build release artifacts. See [scripts/README.md](scripts/README.md) for details.

**Manual (Step-by-Step):**

1. Bump version: `./scripts/bump-version.sh <version> --commit`
2. Update CHANGELOG.md with release notes
3. Run tests: `go test -short ./...` (CI runs full suite)
4. Land the version bump on `main` through the host's normal branch and merge flow
5. Tag release: `git tag v<version> && git push origin v<version>`
6. Update Homebrew: `./scripts/update-homebrew.sh <version>` (waits for GitHub Actions)
7. Verify: `brew update && brew upgrade bd && bd version`

See [docs/RELEASING.md](docs/RELEASING.md) for complete manual instructions.

## Checking GitHub Issues and PRs

When asked to check GitHub issues or PRs, use command-line tools like `gh` instead of browser/playwright tools.

**Preferred approach:**

```bash
# List open issues with details
gh issue list --limit 30

# List open PRs
gh pr list --limit 30

# View specific issue
gh issue view 201
```

**Then provide an in-conversation summary** highlighting:

- Urgent/critical issues (regressions, bugs, broken builds)
- Common themes or patterns
- Feature requests with high engagement
- Items that need immediate attention

**Why this matters:**

- Browser tools consume more tokens and are slower
- CLI summaries are easier to scan and discuss
- Keeps the conversation focused and efficient
- Better for quick triage and prioritization

**Do NOT use:** `browser_navigate`, `browser_snapshot`, or other playwright tools for GitHub PR/issue reviews unless specifically requested by the user.

## Further guidance

- Check existing issues: `bd list`
- Look at recent commits: `git log --oneline -20`
- Read the docs: README.md, ADVANCED.md, EXTENDING.md
- Create an issue if unsure: `bd create "Question: ..." -t task -p 2`

## Important Files

- **README.md** - Main documentation (keep this updated)
- **EXTENDING.md** - Database extension guide
- **ADVANCED.md** - JSONL format analysis
- **CONTRIBUTING.md** - Contribution guidelines
- **SECURITY.md** - Security policy
