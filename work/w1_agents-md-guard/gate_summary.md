# w1_agents-md-guard: Stop bd from writing AGENTS.md — Decision Summary

## What this does

`bd init` will never again create or modify an AGENTS.md file — the code that appends the "Landing the Plane" boilerplate is deleted from your bd fork at the source, so every future `bd init` from any agent in any repo is covered. A second, less-known writer gets closed too: `bd setup factory` also creates and rewrites AGENTS.md, and it will print its text to the terminal instead of touching the file. The 14 repos that already have the boilerplate committed get cleaned: 13 files that are 100% bd-generated are deleted, and moltbot's hand-written AGENTS.md has just the appended section stripped.

## Why bother

On 2026-07-15 a `bd init` run silently appended 26 lines of workflow boilerplate to your curated shared-docs AGENTS.md — instructions every agent then loads in every session. It had already done the same, unnoticed, in 16 places across your projects. Without a source fix it happens again on the next `bd init` anywhere.

## How it works

1. One Codex builder edits the bd fork (`~/projects/tools/beads`, worktree already created) — deletes the AGENTS.md append/create code from `cmd/bd/init.go`, rewires `cmd/bd/setup/factory.go` (`InstallFactory`/`RemoveFactory`) to print instead of write, and fixes the `setup.go` help text that claims the command edits AGENTS.md.
2. The same builder adds regression tests — `TestInitDoesNotTouchAgentsFile` (4 subtests: file absent/present × default/quiet output) and `TestFactoryDoesNotTouchAgentsFile` (5 subtests covering every former write path). If a future sync with upstream (which still ships this feature) reintroduces the behavior, the tests fail. Gate: `go build` clean, all tests pass.
3. I merge the worktree to the fork's main, then reinstall BOTH stale binary copies — your PATH runs `~/.local/bin/bd` (built Jan 28) while `make install` only writes `~/go/bin/bd` (built Mar 16); missing either would leave the polluting binary live. Originals are backed up write-once with hash verification before overwrite.
4. E2E proof against the INSTALLED binary: in one scratch repo `bd init` must leave a sentinel AGENTS.md byte-identical; in another it must not create the file; `bd setup factory` must print and not write. Gate: 5 exact commands with expected outputs, pasted.
5. Fleet cleanup, one repo at a time through the normal worktree flow: the 13 wholly bd-generated AGENTS.md files (verified: nothing references them) are moved to the agent trash (14-day undo) and committed as deletions; moltbot keeps its curated content, losing only the appended section. Gate: a fleet-wide re-sweep finds the boilerplate only in the bd repo's own upstream docs.

## What you're approving

- **Delete the feature from your bd fork rather than wrap or configure around it — bd is built locally from `~/projects/tools/beads`, so this covers every caller forever.**
  Detail: remove `addLandingThePlaneInstructions`, `updateAgentFile`, the template const, and the call at `cmd/bd/init.go:466`; no opt-out flag kept (upstream's only skip, `--stealth`, also flips global git settings).
- **Also close the second writer, `bd setup factory`, so the guarantee is "bd never touches AGENTS.md", not "bd init doesn't".**
  Detail: `cmd/bd/setup/factory.go:104+` `InstallFactory` prints the integration block to stdout; `RemoveFactory` prints removal instructions; read-only `CheckFactory` unchanged; `setup.go:75-78,122` help text corrected.
- **Reinstall both binary copies and prove the fix on the installed binary, not the source tree.**
  Detail: `make install` → `~/go/bin/bd`, then `install -m755` to `~/.local/bin/bd`; proof check asserts the two copies are hash-identical and the embedded commit matches merged main.
- **Delete 13 wholly bd-generated AGENTS.md files; strip only the appended section from moltbot's curated one.**
  Detail: repos acid, decisions, fleet, investing, lit-search, mine, patent-forge, patent-search, spec-code, tts-reader, mini-warriors-reborn, game/game-generator, teaching (file `548-2026/website/AGENTS.md`). Each deletion is gated on an exact SHA-256 match against the two measured template hashes — a concurrently edited file stops the deletion. Via trash.sh + git, so undo exists twice over (trash 14 days + git history).
- **I execute the cleanup phase myself instead of dispatching builders.**
  Detail: each repo change is one file deletion or one section strip inside a manager-created worktree; the wt-new/wt-merge lifecycle is owner-only tooling workers may not run.

## Ideas reviewers raised that I rejected

None — every finding from both review rounds (10 in round 1, 5 in round 2) was accepted and folded in.

## What could go wrong

| Risk | What we'd do |
|------|--------------|
| A future upstream sync brings the feature back | The two regression tests fail at the next test run; fork docs note the invariant |
| Something regenerates the old `~/.local/bin/bd` binary | No auto-update mechanism found; proof check verifies the PATH binary's commit hash after install |
| A template file was edited between planning and cleanup | Exact-hash pre-deletion gate refuses to delete anything that isn't byte-identical to the measured templates |
| moltbot's section isn't actually the last thing in the file | Builder verifies nothing follows it before cutting; otherwise strips only the section's own lines |
| A polluted AGENTS.md the inventory missed | The phase gate is a fresh fleet-wide sweep (two search patterns), not the cached list |

## Links

- Full technical plan: `~/worktrees/beads/w1_agents-md-guard/work/w1_agents-md-guard/plan_v3.md`
- Decision trail: `~/worktrees/beads/w1_agents-md-guard/work/w1_agents-md-guard/manager_log.md`
