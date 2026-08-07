package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

// claimDeniedExit is the exit code for "not claimable now" (bd-ok4pr). It is
// distinct from 1 so a shell caller can tell a losing race from a broken command;
// the outcome's deny_reason then says whether to retry (held) or skip (status).
const claimDeniedExit = 3

var claimCmd = &cobra.Command{
	Use:     "claim <id>",
	GroupID: "issues",
	Short:   "Atomically take ownership of an issue",
	Long: `Atomically take ownership of an issue.

The read, the decision and the write happen in one transaction, so exactly one of
two concurrent claimants wins. Claiming an issue you already hold renews it;
claiming one whose owner's lease has expired steals it.

Exit codes:
  0  claimed, renewed or stolen
  3  denied - not claimable now. deny_reason=held means a live rival claim (retry
     later); deny_reason=status means the status is not claimable (skip it)
  1  error - issue not found, closed or tombstoned, invalid arguments, DB failure`,
	Example: `  bd claim bd-123 --assignee alice
  bd claim bd-123 --assignee alice --lease 45m
  bd claim bd-123 --assignee alice --json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		CheckReadonly("claim")

		assignee, lease, err := claimFlags(cmd)
		if err != nil {
			FatalError("%v", err)
		}
		id := resolveClaimID(args[0])

		outcome := runClaim(id, assignee, lease)
		reportClaim(outcome)
	},
}

// claimFlags reads and validates --assignee and --lease.
//
// An absent or blank --assignee is a usage error rather than a defaulted actor:
// two defaulted claimants would resolve to the same assignee, land in the renew
// case and both exit 0, and a blank one would match the legacy unowned rung with
// the same two-winners result. Cobra's required-flag check only proves the flag
// was typed, so --assignee "" has to be rejected here.
func claimFlags(cmd *cobra.Command) (string, *time.Duration, error) {
	rawAssignee, _ := cmd.Flags().GetString("assignee")
	assignee := strings.TrimSpace(rawAssignee)
	if assignee == "" {
		return "", nil, fmt.Errorf("--assignee is required and must not be empty")
	}

	if !cmd.Flags().Changed("lease") {
		return assignee, nil, nil
	}

	lease, _ := cmd.Flags().GetDuration("lease")
	// The lease crosses the daemon wire in whole seconds, so anything under a
	// second would arrive as an already-expired claim that any rival could steal.
	if lease < time.Second {
		return "", nil, fmt.Errorf("--lease must be at least 1s")
	}
	// Truncate here rather than at the wire, so the direct path and the RPC path
	// derive the same expiry: --lease 90s500ms must not mean 90.5s locally and 90s
	// whenever a daemon happens to be running.
	lease = lease.Truncate(time.Second)
	return assignee, &lease, nil
}

// resolveClaimID expands a partial ID through whichever route is active.
func resolveClaimID(id string) string {
	if daemonClient == nil {
		resolved, err := utils.ResolvePartialIDs(rootCtx, store, []string{id})
		if err != nil {
			FatalError("%v", err)
		}
		return resolved[0]
	}

	resp, err := daemonClient.ResolveID(&rpc.ResolveIDArgs{ID: id})
	if err != nil {
		FatalError("resolving ID %s: %v", id, err)
	}
	var resolved string
	if err := json.Unmarshal(resp.Data, &resolved); err != nil {
		FatalError("unmarshaling resolved ID: %v", err)
	}
	return resolved
}

// runClaim performs the claim through the daemon when one holds the workspace and
// against the store otherwise. Both routes produce the same outcome; any error
// from either is fatal with exit 1.
func runClaim(id, assignee string, lease *time.Duration) *types.ClaimOutcome {
	if daemonClient == nil {
		outcome, err := store.ClaimIssue(rootCtx, id, assignee, lease, actor)
		if err != nil {
			FatalError("%v", err)
		}
		if outcome.Outcome != types.ClaimDenied {
			markDirtyAndScheduleFlush()
		}
		return outcome
	}

	resp, err := daemonClient.Claim(claimRPCArgs(id, assignee, lease))
	if err != nil {
		FatalError("%v", err)
	}
	var outcome types.ClaimOutcome
	if err := json.Unmarshal(resp.Data, &outcome); err != nil {
		FatalError("unmarshaling claim outcome: %v", err)
	}
	return &outcome
}

// claimRPCArgs renders the claim for the wire, where the lease travels as whole
// seconds. A nil lease stays nil: no expiry.
func claimRPCArgs(id, assignee string, lease *time.Duration) *rpc.ClaimArgs {
	args := &rpc.ClaimArgs{ID: id, Assignee: assignee}
	if lease != nil {
		seconds := int64(lease.Seconds())
		args.LeaseSeconds = &seconds
	}
	return args
}

// reportClaim prints the outcome and exits with the code it maps to.
func reportClaim(outcome *types.ClaimOutcome) {
	if outcome.Outcome == types.ClaimDenied {
		if jsonOutput {
			outputJSON(outcome)
		}
		fmt.Fprintf(os.Stderr, "%s\n", claimDenialMessage(outcome))
		os.Exit(claimDeniedExit)
	}

	if outcome.Issue != nil && hookRunner != nil {
		hookRunner.Run(hooks.EventUpdate, outcome.Issue)
	}

	if jsonOutput {
		outputJSON(outcome)
		return
	}
	fmt.Printf("%s %s issue: %s\n", ui.RenderPass("✓"), claimVerb(outcome.Outcome), claimIssueID(outcome))
}

// claimDenialMessage explains why the issue is not claimable now. A held denial is
// about the rival owner, so it names the holder and its expiry; a status denial is
// about the status, which it names, adding the holder only when one is recorded.
func claimDenialMessage(outcome *types.ClaimOutcome) string {
	var reason string
	if outcome.DenyReason == types.DenyStatus || outcome.Holder == "" {
		reason = fmt.Sprintf("status %s", claimStatus(outcome))
		if outcome.Holder != "" {
			reason += ", " + claimHolderText(outcome)
		}
	} else {
		reason = claimHolderText(outcome)
	}
	return fmt.Sprintf("Denied: %s is not claimable: %s (deny_reason=%s)",
		claimIssueID(outcome), reason, outcome.DenyReason)
}

// claimHolderText names the holder and its lease expiry, which is absent for a
// claim taken without --lease.
func claimHolderText(outcome *types.ClaimOutcome) string {
	expiry := "no expiry"
	if outcome.HolderExpiry != nil {
		expiry = "expires " + outcome.HolderExpiry.Format(time.RFC3339)
	}
	return fmt.Sprintf("held by %s (%s)", outcome.Holder, expiry)
}

func claimStatus(outcome *types.ClaimOutcome) types.Status {
	if outcome.Issue == nil {
		return types.Status("unknown")
	}
	return outcome.Issue.Status
}

func claimIssueID(outcome *types.ClaimOutcome) string {
	if outcome.Issue == nil {
		return "issue"
	}
	return outcome.Issue.ID
}

// claimVerb renders the outcome as the past-tense verb for the success line.
func claimVerb(result types.ClaimResult) string {
	switch result {
	case types.ClaimRenewed:
		return "Renewed"
	case types.ClaimStolen:
		return "Stole"
	default:
		return "Claimed"
	}
}

// registerClaimCmd wires the claim verb into the root command. Called from main's
// init so command registration stays in one place per command.
func registerClaimCmd() {
	claimCmd.Flags().String("assignee", "", "Who is claiming the issue (required)")
	claimCmd.Flags().Duration("lease", 0, "Lease duration, e.g. 45m; absent means the claim never expires")
	rootCmd.AddCommand(claimCmd)
}
