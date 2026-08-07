package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/types"
)

// newClaimFlagSet builds a command carrying the claim flags, so the flag rules can
// be exercised without going through the command's fatal exit path.
func newClaimFlagSet(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "claim"}
	cmd.Flags().String("assignee", "", "")
	cmd.Flags().Duration("lease", 0, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parsing flags %v: %v", args, err)
	}
	return cmd
}

func TestClaimFlagsRejectsMissingOrEmptyAssignee(t *testing.T) {
	// A defaulted or blank assignee would let two claimants resolve to the same
	// owner and both exit 0, which is the race the verb exists to close.
	tests := []struct {
		name string
		args []string
	}{
		{"absent", nil},
		{"empty", []string{"--assignee", ""}},
		{"whitespace", []string{"--assignee", "   "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := claimFlags(newClaimFlagSet(t, tt.args...))
			if err == nil {
				t.Fatal("expected a usage error, got none")
			}
			if !strings.Contains(err.Error(), "--assignee") {
				t.Errorf("error should name the flag, got %q", err)
			}
		})
	}
}

func TestClaimFlagsAcceptsAssigneeAndLease(t *testing.T) {
	assignee, lease, err := claimFlags(newClaimFlagSet(t, "--assignee", " alice ", "--lease", "45m"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assignee != "alice" {
		t.Errorf("assignee = %q, want %q (trimmed)", assignee, "alice")
	}
	if lease == nil || *lease != 45*time.Minute {
		t.Errorf("lease = %v, want 45m", lease)
	}
}

func TestClaimFlagsWithoutLeaseMeansNoExpiry(t *testing.T) {
	_, lease, err := claimFlags(newClaimFlagSet(t, "--assignee", "alice"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lease != nil {
		t.Errorf("lease = %v, want nil (never expires)", *lease)
	}
}

func TestClaimFlagsRejectsSubSecondLease(t *testing.T) {
	// The wire carries whole seconds, so a sub-second lease would arrive expired.
	_, _, err := claimFlags(newClaimFlagSet(t, "--assignee", "alice", "--lease", "500ms"))
	if err == nil {
		t.Fatal("expected an error for a sub-second lease, got none")
	}
}

func TestClaimRPCArgsCarryLeaseSeconds(t *testing.T) {
	lease := 90 * time.Second
	args := claimRPCArgs("bd-1", "alice", &lease)
	if args.LeaseSeconds == nil || *args.LeaseSeconds != 90 {
		t.Errorf("LeaseSeconds = %v, want 90", args.LeaseSeconds)
	}

	noLease := claimRPCArgs("bd-1", "alice", nil)
	if noLease.LeaseSeconds != nil {
		t.Errorf("LeaseSeconds = %v, want nil", *noLease.LeaseSeconds)
	}
	if noLease.Assignee != "alice" {
		t.Errorf("Assignee = %q, want alice", noLease.Assignee)
	}
}

func TestClaimDenialMessage(t *testing.T) {
	expiry := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		outcome *types.ClaimOutcome
		want    []string
	}{
		{
			name: "held with a lease names the holder and the expiry",
			outcome: &types.ClaimOutcome{
				Outcome:      types.ClaimDenied,
				DenyReason:   types.DenyHeld,
				Holder:       "alice",
				HolderExpiry: &expiry,
				Issue:        &types.Issue{ID: "bd-1", Status: types.StatusInProgress},
			},
			want: []string{"bd-1", "held by alice", "expires 2026-08-07T12:00:00Z", "deny_reason=held"},
		},
		{
			name: "held without a lease says so rather than inventing an expiry",
			outcome: &types.ClaimOutcome{
				Outcome:    types.ClaimDenied,
				DenyReason: types.DenyHeld,
				Holder:     "alice",
				Issue:      &types.Issue{ID: "bd-1", Status: types.StatusInProgress},
			},
			want: []string{"held by alice", "no expiry", "deny_reason=held"},
		},
		{
			name: "a status denial names the status, which is what makes it unclaimable",
			outcome: &types.ClaimOutcome{
				Outcome:    types.ClaimDenied,
				DenyReason: types.DenyStatus,
				Issue:      &types.Issue{ID: "bd-1", Status: types.StatusBlocked},
			},
			want: []string{"bd-1", "status blocked", "deny_reason=status"},
		},
		{
			name: "a status denial still names a holder when one is recorded",
			outcome: &types.ClaimOutcome{
				Outcome:    types.ClaimDenied,
				DenyReason: types.DenyStatus,
				Holder:     "alice",
				Issue:      &types.Issue{ID: "bd-1", Status: types.StatusDeferred},
			},
			want: []string{"status deferred", "held by alice", "deny_reason=status"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := claimDenialMessage(tt.outcome)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("message %q is missing %q", got, want)
				}
			}
		})
	}
}

func TestClaimVerb(t *testing.T) {
	tests := map[types.ClaimResult]string{
		types.ClaimClaimed: "Claimed",
		types.ClaimRenewed: "Renewed",
		types.ClaimStolen:  "Stole",
	}
	for result, want := range tests {
		if got := claimVerb(result); got != want {
			t.Errorf("claimVerb(%s) = %q, want %q", result, got, want)
		}
	}
}

func TestClaimCommandIsRegistered(t *testing.T) {
	var found *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "claim" {
			found = cmd
			break
		}
	}
	if found == nil {
		t.Fatal("claim command is not registered on rootCmd")
	}
	for _, flag := range []string{"assignee", "lease"} {
		if found.Flags().Lookup(flag) == nil {
			t.Errorf("claim command has no --%s flag", flag)
		}
	}
}
