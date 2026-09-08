# bd CLI development

Read `../../docs/CLAUDE.md` before changing the CLI, storage, or RPC flow. Read `../../docs/BD_AGENT_REFERENCE.md` before running bd commands.

```bash
bd ready --json
bd show <id> --json
bd claim <id> --assignee <name> --lease 45m --json
bd close <id> --reason "Completed" --json
bd sync --flush-only
```

Repeat `--lease` when renewing a claim. `--flush-only` exports pending issue changes without Git operations; full `bd sync` is not a worker finishing step.
