# bd Issue Guide

Issue descriptions, priorities, dependencies, and duplicate merging. Command setup is in `BD_AGENT_REFERENCE.md`.

## Working with issues

### Include issue descriptions

**Issues without descriptions lack context for future work.** When creating issues, always include a meaningful description with:

- **Why** the issue exists (problem statement or need)
- **What** needs to be done (scope and approach)
- **How** you discovered it (if applicable during work)

**Good examples:**

```bash
# Bug discovered during work
bd create "Fix auth bug in login handler" \
  --description="Login fails with 500 error when password contains special characters like quotes. Found while testing GH#123 feature. Stack trace shows unescaped SQL in auth/login.go:45." \
  -t bug -p 1 --deps discovered-from:bd-abc --json

# Feature request
bd create "Add password reset flow" \
  --description="Users need ability to reset forgotten passwords via email. Should follow OAuth best practices and include rate limiting to prevent abuse." \
  -t feature -p 2 --json

# Technical debt
bd create "Refactor auth package for testability" \
  --description="Current auth code has tight DB coupling making unit tests difficult. Need to extract interfaces and add dependency injection. Blocks writing tests for bd-xyz." \
  -t task -p 3 --json
```

**Bad examples (missing context):**

```bash
bd create "Fix auth bug" -t bug -p 1 --json  # What bug? Where? Why?
bd create "Add feature" -t feature --json     # What feature? Why needed?
bd create "Refactor code" -t task --json      # What code? Why refactor?
```

### Deletion Tracking

Read `DELETIONS.md` before deleting issues, pruning tombstones, or resolving deletion conflicts.

### Issue Types

- `bug` - Something broken that needs fixing
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature composed of multiple issues (supports hierarchical children)
- `chore` - Maintenance work (dependencies, tooling)

**Hierarchical children:** Epics can have child issues with dotted IDs (e.g., `bd-a3f8e9.1`, `bd-a3f8e9.2`). Children are auto-numbered sequentially. Up to 3 levels of nesting supported. The parent hash ensures unique namespace - no coordination needed between agents working on different epics.

### Priorities

- `0` - Critical (security, data loss, broken builds)
- `1` - High (major features, important bugs)
- `2` - Medium (nice-to-have features, minor bugs)
- `3` - Low (polish, optimization)
- `4` - Backlog (future ideas)

### Dependency Types

**Blocking dependencies:**
- `blocks` - Hard dependency (issue X blocks issue Y)

**Structural relationships:**
- `parent-child` - Epic/subtask relationship
- `discovered-from` - Track issues discovered during work (automatically inherits parent's `source_repo`)
- `related` - Soft relationship (issues are connected)

**Graph links:** (see [docs/graph-links.md](graph-links.md))
- `relates_to` - Bidirectional "see also" links (`bd relate <id1> <id2>`)
- `duplicates` - Mark issue as duplicate (`bd duplicate <id> --of <canonical>`)
- `supersedes` - Version chains (`bd supersede <old> --with <new>`)
- `replies_to` - Message-thread relationship

Only `blocks` dependencies affect the ready work queue.

When creating an issue with a `discovered-from` dependency, the new issue automatically inherits the parent's `source_repo` field. This ensures discovered work stays in the same repository as the parent task.

### Planning Work with Dependencies

Use `bd dep add <dependent> <prerequisite>`. Name tasks by their work, then verify with `bd blocked` that each task waits for its prerequisites.

```bash
bd dep add msg-rendering buffer-layout
```

**Example breakdown** (for a multi-part feature):
```bash
# Create tasks named by what they do, not what order they're in
bd create "Implement conversation region" -t task -p 1
bd create "Add header-line status display" -t task -p 1
bd create "Render tool calls inline" -t task -p 2
bd create "Add streaming content support" -t task -p 2

# Set up dependencies: X depends on Y means "X needs Y first"
bd dep add header-line conversation-region    # header needs region
bd dep add tool-calls conversation-region     # tools need region
bd dep add streaming tool-calls               # streaming needs tools

# Verify with bd blocked - should show sensible blocking
bd blocked
```

### Duplicate Detection & Merging

Search for duplicates before creating an issue. Preview a merge and preserve useful source content before applying it.

**Automated duplicate detection:**

```bash
# Find all content duplicates in the database
bd duplicates

# Automatically merge all duplicates
bd duplicates --auto-merge

# Preview what would be merged
bd duplicates --dry-run

# During import
bd import -i issues.jsonl --dedupe-after
```

**Detection strategies:**

1. **Before creating new issues**: Search for similar existing issues

   ```bash
   bd list --json | grep -i "authentication"
   bd show bd-41 bd-42 --json  # Compare candidates
   ```

2. **Periodic duplicate scans**: Review issues by type or priority

   ```bash
   bd list --status open --priority 1 --json  # High-priority issues
   bd list --issue-type bug --json             # All bugs
   ```

3. **During work discovery**: Check for duplicates when filing discovered-from issues
   ```bash
   # Before: bd create "Fix auth bug" --description="Details..." --deps discovered-from:bd-100
   # First: bd list --json | grep -i "auth bug"
   # Then decide: create new or link to existing
   ```

**Merge workflow:**

```bash
# Step 1: Identify duplicates (bd-42 and bd-43 duplicate bd-41)
bd show bd-41 bd-42 bd-43 --json

# Step 2: Preview merge to verify
bd merge bd-42 bd-43 --into bd-41 --dry-run

# Step 3: Execute merge
bd merge bd-42 bd-43 --into bd-41 --json

# Step 4: Verify result
bd dep tree bd-41  # Check unified dependency tree
bd show bd-41 --json  # Verify merged content
```

**What gets merged:**

- All dependencies from source → target
- Text references updated across ALL issues (descriptions, notes, design, acceptance criteria)
- Source issues closed with "Merged into bd-X" reason
- Source issue content NOT copied (target keeps its original content)

**Important notes:**

- Merge preserves target issue completely; only dependencies/references migrate
- If source issues have valuable content, manually copy it to target BEFORE merging
- Cannot merge in daemon mode yet (bd-190); use `--no-daemon` flag
- Follow the global approval rule for irreversible operations; Git history may retain the original issue data.

**Best practices:**

- Merge early to prevent dependency fragmentation
- Choose the oldest or most complete issue as merge target
- Add labels like `duplicate` to source issues before merging (for tracking)
- File a discovered-from issue if you found duplicates during work:
  ```bash
  bd create "Found duplicates during bd-X" \
    --description="Issues bd-A, bd-B, and bd-C are duplicates and need merging" \
    -p 2 --deps discovered-from:bd-X --json
  ```
