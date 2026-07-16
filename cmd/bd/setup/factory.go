package setup

import (
	"fmt"
	"os"
	"strings"
)

// Factory/Droid integration markers for AGENTS.md
const (
	factoryBeginMarker = "<!-- BEGIN BEADS INTEGRATION -->"
	factoryEndMarker   = "<!-- END BEADS INTEGRATION -->"
)

const factoryBeadsSection = `<!-- BEGIN BEADS INTEGRATION -->
## Issue Tracking with bd (beads)

**IMPORTANT**: This project uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs, task lists, or other tracking methods.

### Why bd?

- Dependency-aware: Track blockers and relationships between issues
- Git-friendly: Auto-syncs to JSONL for version control
- Agent-optimized: JSON output, ready work detection, discovered-from links
- Prevents duplicate tracking systems and confusion

### Quick Start

**Check for ready work:**

` + "```bash" + `
bd ready --json
` + "```" + `

**Create new issues:**

` + "```bash" + `
bd create "Issue title" --description="Detailed context" -t bug|feature|task -p 0-4 --json
bd create "Issue title" --description="What this issue is about" -p 1 --deps discovered-from:bd-123 --json
` + "```" + `

**Claim and update:**

` + "```bash" + `
bd update bd-42 --status in_progress --json
bd update bd-42 --priority 1 --json
` + "```" + `

**Complete work:**

` + "```bash" + `
bd close bd-42 --reason "Completed" --json
` + "```" + `

### Issue Types

- ` + "`bug`" + ` - Something broken
- ` + "`feature`" + ` - New functionality
- ` + "`task`" + ` - Work item (tests, docs, refactoring)
- ` + "`epic`" + ` - Large feature with subtasks
- ` + "`chore`" + ` - Maintenance (dependencies, tooling)

### Priorities

- ` + "`0`" + ` - Critical (security, data loss, broken builds)
- ` + "`1`" + ` - High (major features, important bugs)
- ` + "`2`" + ` - Medium (default, nice-to-have)
- ` + "`3`" + ` - Low (polish, optimization)
- ` + "`4`" + ` - Backlog (future ideas)

### Workflow for AI Agents

1. **Check ready work**: ` + "`bd ready`" + ` shows unblocked issues
2. **Claim your task**: ` + "`bd update <id> --status in_progress`" + `
3. **Work on it**: Implement, test, document
4. **Discover new work?** Create linked issue:
   - ` + "`bd create \"Found bug\" --description=\"Details about what was found\" -p 1 --deps discovered-from:<parent-id>`" + `
5. **Complete**: ` + "`bd close <id> --reason \"Done\"`" + `

### Auto-Sync

bd automatically syncs with git:

- Exports to ` + "`.beads/issues.jsonl`" + ` after changes (5s debounce)
- Imports from JSONL when newer (e.g., after ` + "`git pull`" + `)
- No manual export/import needed!

### Important Rules

- ✅ Use bd for ALL task tracking
- ✅ Always use ` + "`--json`" + ` flag for programmatic use
- ✅ Link discovered work with ` + "`discovered-from`" + ` dependencies
- ✅ Check ` + "`bd ready`" + ` before asking "what should I work on?"
- ❌ Do NOT create markdown TODO lists
- ❌ Do NOT use external issue trackers
- ❌ Do NOT duplicate tracking systems

For more details, see README.md and docs/QUICKSTART.md.

<!-- END BEADS INTEGRATION -->
`

// InstallFactory prints Factory.ai/Droid integration instructions.
func InstallFactory() {
	fmt.Println("Factory.ai (Droid) integration instructions")
	fmt.Println("bd never writes AGENTS.md. Apply this block manually:")
	fmt.Println()
	fmt.Print(factoryBeadsSection)
	fmt.Println("Add this block to AGENTS.md yourself. If AGENTS.md already contains the")
	fmt.Printf("%s and %s markers, replace only the marked section. Otherwise append the block.\n", factoryBeginMarker, factoryEndMarker)
}

// CheckFactory checks if Factory.ai integration is installed
func CheckFactory() {
	agentsPath := "AGENTS.md"

	// Check if AGENTS.md exists
	data, err := os.ReadFile(agentsPath)
	if os.IsNotExist(err) {
		fmt.Println("✗ AGENTS.md not found")
		fmt.Println("  Run: bd setup factory")
		os.Exit(1)
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to read AGENTS.md: %v\n", err)
		os.Exit(1)
	}

	// Check if it contains beads section
	content := string(data)
	if strings.Contains(content, factoryBeginMarker) {
		fmt.Println("✓ Factory.ai integration installed:", agentsPath)
		fmt.Println("  Beads section found in AGENTS.md")
	} else {
		fmt.Println("⚠ AGENTS.md exists but no beads section found")
		fmt.Println("  Run: bd setup factory (to add beads section)")
		os.Exit(1)
	}
}

// RemoveFactory prints Factory.ai integration removal instructions.
func RemoveFactory() {
	fmt.Println("Factory.ai (Droid) integration removal instructions")
	fmt.Println("bd never writes AGENTS.md. Edit AGENTS.md yourself and delete every line")
	fmt.Printf("from %s through %s, inclusive.\n", factoryBeginMarker, factoryEndMarker)
}
