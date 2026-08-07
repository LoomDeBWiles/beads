package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/rpc"
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

func TestClaimFlagsTruncatesSubSecondRemainder(t *testing.T) {
	// The wire carries whole seconds. Truncating at the wire instead of here would
	// give --lease 90s500ms a 90.5s expiry in direct mode and a 90s expiry whenever
	// a daemon happens to be running.
	_, lease, err := claimFlags(newClaimFlagSet(t, "--assignee", "alice", "--lease", "90s500ms"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lease == nil || *lease != 90*time.Second {
		t.Fatalf("lease = %v, want 90s (the value both routes must send)", lease)
	}
	args := claimRPCArgs("bd-1", "alice", lease)
	if args.LeaseSeconds == nil || time.Duration(*args.LeaseSeconds)*time.Second != *lease {
		t.Errorf("the RPC route sends %v seconds, the direct route sends %v", args.LeaseSeconds, *lease)
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

// --- End-to-end exit codes (bd-ok4pr.2.2) ---
//
// cmd/bd/testdata/claim.txt asserts the same contract, but its runner lives behind
// the `scripttests` build tag, so no gate command executes it. The tests below are
// tag-free: they build the binary and assert exit 0, 3 and 1 through the direct
// path and through the daemon's RPC handler.

var (
	claimBinaryOnce sync.Once
	claimBinaryPath string
	claimBinaryErr  error
)

// claimTestBinary builds bd once per test run. It deliberately does not reuse a
// prebuilt binary from the repo root: a stale one would assert the contract of code
// that is no longer here, which is the illusory coverage these tests exist to end.
func claimTestBinary(t *testing.T) string {
	t.Helper()
	claimBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "bd-claim-e2e-*")
		if err != nil {
			claimBinaryErr = err
			return
		}
		claimBinaryPath = filepath.Join(dir, "bd")
		if out, err := exec.Command("go", "build", "-o", claimBinaryPath, ".").CombinedOutput(); err != nil {
			claimBinaryErr = fmt.Errorf("%v\n%s", err, out)
		}
	})
	if claimBinaryErr != nil {
		t.Fatalf("building the bd binary: %v", claimBinaryErr)
	}
	return claimBinaryPath
}

// bdResult is one binary invocation: the streams stay separate because the claim
// contract puts the denial on stderr and the exit code carries the outcome.
type bdResult struct {
	stdout string
	stderr string
	exit   int
}

func (r bdResult) String() string {
	return fmt.Sprintf("exit=%d\nstdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
}

func runBDBinary(t *testing.T, bin, dir string, env []string, args ...string) bdResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := bdResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		result.exit = exitErr.ExitCode()
	default:
		t.Fatalf("running bd %v: %v", args, err)
	}
	return result
}

// newClaimTestRepo builds an initialized bd workspace in its own git repository.
// Initialization always runs daemonless so the workspace starts without one; the
// daemon-mode test starts its daemon explicitly.
func newClaimTestRepo(t *testing.T, bin string) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "claim-test@example.invalid"},
		{"config", "user.name", "Claim Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if res := runBDBinary(t, bin, repo, []string{"BEADS_NO_DAEMON=1"}, "init", "--quiet", "--prefix", "test"); res.exit != 0 {
		t.Fatalf("bd init failed:\n%s", res)
	}
	return repo
}

func newClaimTestIssue(t *testing.T, bin, repo string, env []string, title string) string {
	t.Helper()
	res := runBDBinary(t, bin, repo, env, "create", title, "--json")
	if res.exit != 0 {
		t.Fatalf("bd create failed:\n%s", res)
	}
	start := strings.Index(res.stdout, "{")
	if start < 0 {
		t.Fatalf("bd create printed no JSON object:\n%s", res)
	}
	var issue struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(strings.NewReader(res.stdout[start:])).Decode(&issue); err != nil {
		t.Fatalf("decoding the created issue: %v\n%s", err, res)
	}
	if issue.ID == "" {
		t.Fatalf("the created issue has no ID:\n%s", res)
	}
	return issue.ID
}

func TestClaimExitCodesInDirectMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the claim end-to-end tests drive a Unix-socket daemon and POSIX exit codes")
	}
	bin := claimTestBinary(t)
	env := []string{"BEADS_NO_DAEMON=1"}
	repo := newClaimTestRepo(t, bin)
	id := newClaimTestIssue(t, bin, repo, env, "Issue to claim")

	claim := func(args ...string) bdResult {
		return runBDBinary(t, bin, repo, env, append([]string{"--no-daemon", "claim"}, args...)...)
	}

	won := claim(id, "--assignee", "alice")
	if won.exit != 0 {
		t.Fatalf("claiming an open issue should exit 0:\n%s", won)
	}
	if !strings.Contains(won.stdout, "Claimed issue") {
		t.Errorf("the winner should say it claimed the issue:\n%s", won)
	}

	denied := claim(id, "--assignee", "bob")
	if denied.exit != claimDeniedExit {
		t.Fatalf("a rival of a live holder should exit %d:\n%s", claimDeniedExit, denied)
	}
	for _, want := range []string{"deny_reason=held", "held by alice"} {
		if !strings.Contains(denied.stderr, want) {
			t.Errorf("the denial should say %q so a caller knows to retry:\n%s", want, denied)
		}
	}

	if res := runBDBinary(t, bin, repo, env, "--no-daemon", "close", id, "--reason", "done"); res.exit != 0 {
		t.Fatalf("bd close failed:\n%s", res)
	}
	failed := claim(id, "--assignee", "bob")
	if failed.exit != 1 {
		t.Fatalf("claiming a closed issue is an error, not a denial, so it should exit 1:\n%s", failed)
	}
	if !strings.Contains(failed.stderr, "closed") {
		t.Errorf("the error should name the closed status:\n%s", failed)
	}
}

func TestClaimExitCodesInDaemonMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the claim end-to-end tests drive a Unix-socket daemon and POSIX exit codes")
	}
	bin := claimTestBinary(t)
	env := []string{"BEADS_NO_DAEMON=0"}
	repo := newClaimTestRepo(t, bin)

	if res := runBDBinary(t, bin, repo, env, "daemon", "--start", "--local"); res.exit != 0 {
		t.Fatalf("starting the local daemon failed:\n%s", res)
	}
	t.Cleanup(func() {
		if res := runBDBinary(t, bin, repo, env, "daemon", "--stop"); res.exit != 0 {
			t.Errorf("stopping the test daemon failed:\n%s", res)
		}
	})

	id := newClaimTestIssue(t, bin, repo, env, "Issue to claim via the daemon")
	claim := func(args ...string) bdResult {
		res := runBDBinary(t, bin, repo, env, append([]string{"claim"}, args...)...)
		// bd falls back to the direct path with a "Running in direct mode" warning
		// whenever the daemon is unreachable, so without this check a broken
		// handleClaim would pass every assertion below by re-running direct mode.
		if strings.Contains(res.stderr, "direct mode") {
			t.Fatalf("the claim fell back to direct mode, so it proves nothing about the RPC handler:\n%s", res)
		}
		return res
	}

	won := claim(id, "--assignee", "alice")
	if won.exit != 0 {
		t.Fatalf("claiming an open issue should exit 0:\n%s", won)
	}
	if !strings.Contains(won.stdout, "Claimed issue") {
		t.Errorf("the winner should say it claimed the issue:\n%s", won)
	}

	denied := claim(id, "--assignee", "bob")
	if denied.exit != claimDeniedExit {
		t.Fatalf("a rival of a live holder should exit %d:\n%s", claimDeniedExit, denied)
	}
	if !strings.Contains(denied.stderr, "deny_reason=held") {
		t.Errorf("the denial should say deny_reason=held:\n%s", denied)
	}

	if res := runBDBinary(t, bin, repo, env, "close", id, "--reason", "done"); res.exit != 0 {
		t.Fatalf("bd close failed:\n%s", res)
	}
	failed := claim(id, "--assignee", "bob")
	if failed.exit != 1 {
		t.Fatalf("claiming a closed issue is an error, not a denial, so it should exit 1:\n%s", failed)
	}
	if !strings.Contains(failed.stderr, "closed") {
		t.Errorf("the error should name the closed status:\n%s", failed)
	}

	assertDaemonServedClaims(t, repo)
}

// assertDaemonServedClaims reads the daemon's own operation counters, which is the
// positive half of the proof: the absence of a fallback warning says the CLI did not
// give up on the daemon, and these counts say the daemon actually ran the claims.
func assertDaemonServedClaims(t *testing.T, repo string) {
	t.Helper()
	client := connectClaimTestDaemon(t, repo)
	defer func() { _ = client.Close() }()

	metrics, err := client.Metrics()
	if err != nil {
		t.Fatalf("reading daemon metrics: %v", err)
	}
	for _, op := range metrics.Operations {
		if op.Operation != rpc.OpClaim {
			continue
		}
		if op.SuccessCount < 2 {
			t.Errorf("the daemon served %d successful claims, want the claim and the denial", op.SuccessCount)
		}
		if op.ErrorCount < 1 {
			t.Errorf("the daemon served %d claim errors, want the closed-issue error", op.ErrorCount)
		}
		return
	}
	t.Fatalf("the daemon recorded no %q operation, so the claims never reached it", rpc.OpClaim)
}

// connectClaimTestDaemon dials the workspace daemon directly. TryConnect reports an
// unready socket as (nil, nil) rather than an error, so it is retried: the daemon
// answers its own start command before the socket accepts connections.
func connectClaimTestDaemon(t *testing.T, repo string) *rpc.Client {
	t.Helper()
	socket := filepath.Join(repo, ".beads", "bd.sock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		client, err := rpc.TryConnect(socket)
		if err != nil {
			t.Fatalf("connecting to the test daemon: %v", err)
		}
		if client != nil {
			return client
		}
		if time.Now().After(deadline) {
			t.Fatalf("the test daemon never became reachable on %s", socket)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestClaimRPCRejectsNonPositiveLease covers the wire the CLI cannot reach: the CLI
// rejects anything under 1s, so only a direct RPC call can carry a zero or negative
// lease, which the handler must refuse rather than write as an expired claim.
func TestClaimRPCRejectsNonPositiveLease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the claim end-to-end tests drive a Unix-socket daemon and POSIX exit codes")
	}
	bin := claimTestBinary(t)
	env := []string{"BEADS_NO_DAEMON=0"}
	repo := newClaimTestRepo(t, bin)

	if res := runBDBinary(t, bin, repo, env, "daemon", "--start", "--local"); res.exit != 0 {
		t.Fatalf("starting the local daemon failed:\n%s", res)
	}
	t.Cleanup(func() {
		if res := runBDBinary(t, bin, repo, env, "daemon", "--stop"); res.exit != 0 {
			t.Errorf("stopping the test daemon failed:\n%s", res)
		}
	})

	id := newClaimTestIssue(t, bin, repo, env, "Issue a zero lease must not claim")
	client := connectClaimTestDaemon(t, repo)
	defer func() { _ = client.Close() }()

	for _, seconds := range []int64{0, -1} {
		lease := seconds
		if _, err := client.Claim(&rpc.ClaimArgs{ID: id, Assignee: "alice", LeaseSeconds: &lease}); err == nil {
			t.Errorf("a lease of %ds was accepted; it would be claimed already expired and stolen at once", seconds)
		} else if !strings.Contains(err.Error(), "lease") {
			t.Errorf("the rejection of a %ds lease should name the lease, got %q", seconds, err)
		}
	}

	show := runBDBinary(t, bin, repo, env, "show", id, "--json")
	if show.exit != 0 {
		t.Fatalf("bd show failed:\n%s", show)
	}
	if strings.Contains(show.stdout, "alice") {
		t.Errorf("a rejected claim must leave the issue unowned:\n%s", show)
	}
}
