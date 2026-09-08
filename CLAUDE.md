# Beads

@context/INDEX.md

## Repo rules

- Use `bd --no-daemon` in worktrees; see `docs/WORKTREES.md`.
- Keep the host-managed Git hooks; `bd hooks install` would overwrite their validation.
- Exclude `.beads/issues.jsonl` from PRs. Preserve issue data when preparing the branch.
- Claim work with `bd claim <id> --assignee <name> --lease 45m --json`; repeat `--lease` on renewal.
- Use `--json` for programmatic output and `discovered-from` for issues found during work.

Read the matching index entry before running bd, changing code, or creating issues. Detailed commands, architecture, and development procedures stay in those documents.
