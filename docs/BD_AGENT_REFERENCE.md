# bd command reference

Use `bd --no-daemon` in worktrees. Examples below describe bd commands; the global rules govern Git operations and process ownership. Use `bd sync --flush-only` to export changes; full `bd sync` is not a worker finishing step.

## Setup and upgrades

Run `bd info --whats-new` after an upgrade; add `--json` for programmatic output. For a new scratch database, `bd init --quiet` avoids interactive prompts. Keep this repository's host-managed Git hooks. Installation and initialization details are in `INSTALLING.md` and `CLI_REFERENCE.md` in this directory.

### CLI Quick Reference

Repeat `--lease` when renewing a claim.

```bash
# Create and manage issues
bd create "Issue title" --description="Detailed context about the issue" -t bug|feature|task -p 0-4 --json
bd create "Found bug" --description="What the bug is and how it was discovered" -p 1 --deps discovered-from:<parent-id> --json
bd claim <id> --assignee <name> --lease 45m --json
bd close <id> --reason "Done" --json

# Search and filter
bd list --status open --priority 1 --json
bd list --label-any urgent,critical --json
bd show <id> --json

# Export the database to JSONL without touching git
bd sync --flush-only
```

**For comprehensive CLI documentation**, see [docs/CLI_REFERENCE.md](CLI_REFERENCE.md).

### MCP Server (Alternative)

For Claude Desktop, Sourcegraph Amp, or other MCP-only environments where CLI access is limited, use the MCP server:

```bash
pip install beads-mcp
```

Add to MCP config:
```json
{
  "beads": {
    "command": "beads-mcp",
    "args": []
  }
}
```

Use MCP when the client cannot run CLI commands. Use the CLI when shell access is available.

See `../integrations/beads-mcp/README.md` for MCP documentation. For multi-repo MCP patterns, see [docs/MULTI_REPO_AGENTS.md](MULTI_REPO_AGENTS.md).

### Import Configuration

bd provides configuration for handling edge cases during import, especially when dealing with hierarchical issues and deleted parents:

```bash
# Configure orphan handling for imports
bd config set import.orphan_handling "allow"      # Default: import orphans without validation
bd config set import.orphan_handling "resurrect"  # Auto-resurrect deleted parents as tombstones
bd config set import.orphan_handling "skip"       # Skip orphaned children with warning
bd config set import.orphan_handling "strict"     # Fail if parent is missing
```

**Modes explained:**

- **`allow` (default)** - Import orphaned children without parent validation. Most permissive, ensures no data loss even if hierarchy is temporarily broken.
- **`resurrect`** - Search JSONL history for deleted parents and recreate them as tombstones (Status=Closed, Priority=4). Preserves hierarchy with minimal data.
- **`skip`** - Skip orphaned children with a warning. Partial import succeeds but some issues are excluded.
- **`strict`** - Fail import immediately if a child's parent is missing. Use when database integrity is critical.

**When to use each mode:**

- Use `allow` (default) for daily imports and auto-sync - ensures no data loss
- Use `resurrect` when importing from another database that had parent deletions
- Use `strict` only for controlled imports where you need to guarantee parent existence
- Use `skip` rarely - only when you want to selectively import a subset

**Override per command:**
```bash
bd import -i issues.jsonl --orphan-handling resurrect  # One-time override
bd sync  # Uses import.orphan_handling config setting
```

See [docs/CONFIG.md](CONFIG.md) for complete configuration documentation.

### Managing Daemons

bd runs a background daemon per workspace for auto-sync and RPC operations:

```bash
bd daemons list --json          # List all running daemons
bd daemons health --json        # Check for version mismatches
bd daemons logs . -n 100        # View daemon logs
```

After an upgrade, inspect daemon versions. Stop only a process this session created and recorded; otherwise obtain approval for the exact command. Do not restart all system-wide daemons.

### Event-Driven Daemon Mode (Experimental)

Event-driven mode processes mutations without polling; see `DAEMON.md` for configuration and version support.

**Enable globally:**
```bash
export BEADS_DAEMON_MODE=events
# Apply only to a daemon this session owns; otherwise request approval.
```

**For configuration, troubleshooting, and complete daemon management**, see [docs/DAEMON.md](DAEMON.md).

### Web Interface (Monitor)

bd includes a built-in web interface for human visualization:

```bash
bd monitor                  # Start on localhost:8080
bd monitor --port 3000      # Custom port
```

**AI agents**: Continue using CLI with `--json` flags. The monitor is for human supervision only.

### Chemistry Commands (Templates & Workflows)

bd uses a molecular chemistry metaphor for template instantiation:

| Phase | Storage | Synced | Use Case |
|-------|---------|--------|----------|
| **Proto** (solid) | Built-in | N/A | Reusable templates |
| **Mol** (liquid) | `.beads/` | Yes | Persistent work |
| **Wisp** (vapor) | `.beads-wisp/` | No | Ephemeral operations |

**Instantiation commands:**

```bash
# Pour: proto → persistent mol (liquid phase)
bd pour <proto> --var key=value      # Create in .beads/

# Wisp: proto → ephemeral wisp (vapor phase)
bd wisp create <proto> --var key=value  # Create in .beads-wisp/

# List available templates
bd mol list --json
```

**Work assignment:**

```bash
# Pin work to an agent's hook
bd pin <id> --for <agent> --start    # Assign and start work

# Inspect what's on an agent's hook
bd hook --agent <agent>              # Show pinned work
bd hook --json                       # JSON output
```

**Phase control with bond:**

```bash
# Attach proto to existing mol/wisp
bd mol bond <proto> <target> --pour  # Force liquid (persistent)
bd mol bond <proto> <target> --wisp  # Force vapor (ephemeral)
```

Commands like `bd pour` require `--no-daemon` flag when daemon is running.
