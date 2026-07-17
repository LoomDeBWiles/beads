package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/git"
)

func TestGetEmbeddedHooks(t *testing.T) {
	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("getEmbeddedHooks() failed: %v", err)
	}

	expectedHooks := []string{"pre-commit", "post-merge", "pre-push", "post-checkout"}
	for _, hookName := range expectedHooks {
		content, ok := hooks[hookName]
		if !ok {
			t.Errorf("Missing hook: %s", hookName)
			continue
		}
		if len(content) == 0 {
			t.Errorf("Hook %s has empty content", hookName)
		}
		// Verify it's a shell script
		if content[:2] != "#!" {
			t.Errorf("Hook %s doesn't start with shebang: %s", hookName, content[:50])
		}
	}
}

func TestInstallHooks(t *testing.T) {
	// Create temp directory and init git repo
	tmpDir := t.TempDir()

	// Change to temp directory
	t.Chdir(tmpDir)

	// Initialize a real git repo (required for git rev-parse)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Skipf("Skipping test: git init failed: %v", err)
	}

	gitDirPath, err := git.GetGitDir()
	if err != nil {
		t.Fatalf("git.GetGitDir() failed: %v", err)
	}
	gitDir := filepath.Join(gitDirPath, "hooks")

	// Get embedded hooks
	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("getEmbeddedHooks() failed: %v", err)
	}

	// Install hooks
	if err := installHooks(hooks, false, false); err != nil {
		t.Fatalf("installHooks() failed: %v", err)
	}

	// Verify hooks were installed
	for hookName := range hooks {
		hookPath := filepath.Join(gitDir, hookName)
		if _, err := os.Stat(hookPath); os.IsNotExist(err) {
			t.Errorf("Hook %s was not installed", hookName)
		}
		// Windows does not support POSIX executable bits, so skip the check there.
		if runtime.GOOS == "windows" {
			continue
		}

		info, err := os.Stat(hookPath)
		if err != nil {
			t.Errorf("Failed to stat %s: %v", hookName, err)
			continue
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("Hook %s is not executable", hookName)
		}
	}
}

func TestInstallHooksBackup(t *testing.T) {
	// Create temp directory and init git repo
	tmpDir := t.TempDir()

	// Change to temp directory
	t.Chdir(tmpDir)

	// Initialize a real git repo (required for git rev-parse)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Skipf("Skipping test: git init failed: %v", err)
	}

	gitDirPath, err := git.GetGitDir()
	if err != nil {
		t.Fatalf("git.GetGitDir() failed: %v", err)
	}
	gitDir := filepath.Join(gitDirPath, "hooks")

	// Ensure hooks directory exists
	if err := os.MkdirAll(gitDir, 0750); err != nil {
		t.Fatalf("Failed to create hooks directory: %v", err)
	}

	// Create an existing hook
	existingHook := filepath.Join(gitDir, "pre-commit")
	existingContent := "#!/bin/sh\necho old hook\n"
	if err := os.WriteFile(existingHook, []byte(existingContent), 0755); err != nil {
		t.Fatalf("Failed to create existing hook: %v", err)
	}

	// Get embedded hooks
	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("getEmbeddedHooks() failed: %v", err)
	}

	// Install hooks (should backup existing)
	if err := installHooks(hooks, false, false); err != nil {
		t.Fatalf("installHooks() failed: %v", err)
	}

	// Verify backup was created
	backupPath := existingHook + ".backup"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Errorf("Backup was not created")
	}

	// Verify backup has original content
	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("Failed to read backup: %v", err)
	}
	if string(backupContent) != existingContent {
		t.Errorf("Backup content mismatch: got %q, want %q", string(backupContent), existingContent)
	}
}

func TestInstallHooksForce(t *testing.T) {
	// Create temp directory and init git repo
	tmpDir := t.TempDir()

	// Change to temp directory first, then init
	t.Chdir(tmpDir)

	// Initialize a real git repo (required for git rev-parse)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Skipf("Skipping test: git init failed: %v", err)
	}

	gitDirPath, err := git.GetGitDir()
	if err != nil {
		t.Fatalf("git.GetGitDir() failed: %v", err)
	}
	gitDir := filepath.Join(gitDirPath, "hooks")

	// Ensure hooks directory exists
	if err := os.MkdirAll(gitDir, 0750); err != nil {
		t.Fatalf("Failed to create hooks directory: %v", err)
	}

	// Create an existing hook
	existingHook := filepath.Join(gitDir, "pre-commit")
	if err := os.WriteFile(existingHook, []byte("old"), 0755); err != nil {
		t.Fatalf("Failed to create existing hook: %v", err)
	}

	// Get embedded hooks
	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("getEmbeddedHooks() failed: %v", err)
	}

	// Install hooks with force (should not create backup)
	if err := installHooks(hooks, true, false); err != nil {
		t.Fatalf("installHooks() failed: %v", err)
	}

	// Verify no backup was created
	backupPath := existingHook + ".backup"
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Errorf("Backup should not have been created with --force")
	}
}

func TestUninstallHooks(t *testing.T) {
	// Create temp directory and init git repo
	tmpDir := t.TempDir()

	// Change to temp directory first, then init
	t.Chdir(tmpDir)

	// Initialize a real git repo (required for git rev-parse)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Skipf("Skipping test: git init failed: %v", err)
	}

	gitDirPath, err := git.GetGitDir()
	if err != nil {
		t.Fatalf("git.GetGitDir() failed: %v", err)
	}
	gitDir := filepath.Join(gitDirPath, "hooks")

	// Get embedded hooks and install them
	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("getEmbeddedHooks() failed: %v", err)
	}
	if err := installHooks(hooks, false, false); err != nil {
		t.Fatalf("installHooks() failed: %v", err)
	}

	// Uninstall hooks
	if err := uninstallHooks(); err != nil {
		t.Fatalf("uninstallHooks() failed: %v", err)
	}

	// Verify hooks were removed
	hookNames := []string{"pre-commit", "post-merge", "pre-push", "post-checkout"}
	for _, hookName := range hookNames {
		hookPath := filepath.Join(gitDir, hookName)
		if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
			t.Errorf("Hook %s was not removed", hookName)
		}
	}
}

func TestHooksCheckGitHooks(t *testing.T) {
	// Create temp directory and init git repo
	tmpDir := t.TempDir()

	// Change to temp directory first, then init
	t.Chdir(tmpDir)

	// Initialize a real git repo (required for git rev-parse)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Skipf("Skipping test: git init failed: %v", err)
	}

	// Initially no hooks installed
	statuses := CheckGitHooks()

	for _, status := range statuses {
		if status.Installed {
			t.Errorf("Hook %s should not be installed initially", status.Name)
		}
	}

	// Install hooks
	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("getEmbeddedHooks() failed: %v", err)
	}
	if err := installHooks(hooks, false, false); err != nil {
		t.Fatalf("installHooks() failed: %v", err)
	}

	// Check again
	statuses = CheckGitHooks()

	for _, status := range statuses {
		if !status.Installed {
			t.Errorf("Hook %s should be installed", status.Name)
		}
		// Thin shims use version format "v1" (shim format version, not bd version)
		if !status.IsShim {
			t.Errorf("Hook %s should be a thin shim", status.Name)
		}
		if status.Version != "v1" {
			t.Errorf("Hook %s shim version mismatch: got %s, want v1", status.Name, status.Version)
		}
		if status.Outdated {
			t.Errorf("Hook %s should not be outdated", status.Name)
		}
	}
}

func TestInstallHooksShared(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Change to temp directory
	t.Chdir(tmpDir)

	// Initialize a real git repo (needed for git config command)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Skipf("Skipping test: git init failed (git may not be available): %v", err)
	}

	// Get embedded hooks
	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("getEmbeddedHooks() failed: %v", err)
	}

	// Install hooks in shared mode
	if err := installHooks(hooks, false, true); err != nil {
		t.Fatalf("installHooks() with shared=true failed: %v", err)
	}

	// Verify hooks were installed to .beads-hooks/
	sharedHooksDir := ".beads-hooks"
	for hookName := range hooks {
		hookPath := filepath.Join(sharedHooksDir, hookName)
		if _, err := os.Stat(hookPath); os.IsNotExist(err) {
			t.Errorf("Hook %s was not installed to .beads-hooks/", hookName)
		}
		// Windows does not support POSIX executable bits, so skip the check there.
		if runtime.GOOS == "windows" {
			continue
		}

		info, err := os.Stat(hookPath)
		if err != nil {
			t.Errorf("Failed to stat %s: %v", hookName, err)
			continue
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("Hook %s is not executable", hookName)
		}
	}

	// Verify hooks were NOT installed to .git/hooks/
	gitDirPath, err := git.GetGitDir()
	if err != nil {
		t.Fatalf("git.GetGitDir() failed: %v", err)
	}
	standardHooksDir := filepath.Join(gitDirPath, "hooks")
	for hookName := range hooks {
		hookPath := filepath.Join(standardHooksDir, hookName)
		if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
			t.Errorf("Hook %s should not be in .git/hooks/ when using --shared", hookName)
		}
	}
}

func TestRunPreCommitHookDoesNotStage(t *testing.T) {
	repoDir := t.TempDir()
	hookTestGit(t, repoDir, "init")
	hookTestGit(t, repoDir, "config", "user.email", "test@example.com")
	hookTestGit(t, repoDir, "config", "user.name", "Test User")

	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get package directory: %v", err)
	}
	bdBinary := filepath.Join(t.TempDir(), "bd")
	buildCmd := exec.Command("go", "build", "-o", bdBinary, ".")
	buildCmd.Dir = packageDir
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build bd: %v\n%s", err, output)
	}

	runBD := func(args ...string) {
		t.Helper()
		cmd := exec.Command(bdBinary, args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), "BEADS_NO_DAEMON=1", "PATH="+filepath.Dir(bdBinary)+string(os.PathListSeparator)+os.Getenv("PATH"))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd %v: %v\n%s", args, err, output)
		}
	}

	runBD("init", "--quiet", "--skip-hooks", "--prefix", "test")
	runBD("--no-daemon", "create", "Committed issue")
	runBD("--no-daemon", "sync", "--flush-only")
	if err := os.WriteFile(filepath.Join(repoDir, "selected.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatalf("write selected file: %v", err)
	}
	hookTestGit(t, repoDir, "add", "selected.txt", ".beads/issues.jsonl")
	hookTestGit(t, repoDir, "commit", "-m", "initial state")

	runBD("--no-daemon", "create", "Pending export")
	if err := os.WriteFile(filepath.Join(repoDir, "selected.txt"), []byte("staged change\n"), 0644); err != nil {
		t.Fatalf("update selected file: %v", err)
	}
	hookTestGit(t, repoDir, "add", "selected.txt")
	if err := os.WriteFile(filepath.Join(repoDir, "selected.txt"), []byte("unstaged change\n"), 0644); err != nil {
		t.Fatalf("write unstaged selected file change: %v", err)
	}

	runBD("--no-daemon", "hooks", "install", "--force")
	indexBefore := gitIndexState(t, repoDir)

	hookCmd := exec.Command(filepath.Join(repoDir, ".git", "hooks", "pre-commit"))
	hookCmd.Dir = repoDir
	hookCmd.Env = append(os.Environ(), "BEADS_NO_DAEMON=1", "PATH="+filepath.Dir(bdBinary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := hookCmd.CombinedOutput(); err != nil {
		t.Fatalf("installed pre-commit hook: %v\n%s", err, output)
	}

	indexAfter := gitIndexState(t, repoDir)
	if indexAfter != indexBefore {
		t.Fatalf("pre-commit hook changed Git index: before %q, after %q", indexBefore, indexAfter)
	}

	hooks, err := getEmbeddedHooks()
	if err != nil {
		t.Fatalf("get embedded hooks: %v", err)
	}
	if strings.Contains(hooks["pre-commit"], "git add") {
		t.Fatal("installed pre-commit template stages files")
	}
}

func TestRunPreCommitHookFailsWhenFlushFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script to simulate a failing bd command")
	}

	repoDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoDir, ".beads"), 0755); err != nil {
		t.Fatalf("create beads directory: %v", err)
	}

	fakeBinDir := t.TempDir()
	fakeBD := filepath.Join(fakeBinDir, "bd")
	if err := os.WriteFile(fakeBD, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write failing bd command: %v", err)
	}

	packageDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get package directory: %v", err)
	}
	bdBinary := filepath.Join(t.TempDir(), "bd")
	buildCmd := exec.Command("go", "build", "-o", bdBinary, ".")
	buildCmd.Dir = packageDir
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build bd: %v\n%s", err, output)
	}

	hookCmd := exec.Command(bdBinary, "hooks", "run", "pre-commit")
	hookCmd.Dir = repoDir
	hookCmd.Env = append(os.Environ(), "PATH="+fakeBinDir)
	output, err := hookCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pre-commit hook succeeded after flush failure: %s", output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run pre-commit hook: %v\n%s", err, output)
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("pre-commit hook exit code = 0 after flush failure: %s", output)
	}
}

func gitIndexState(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-s")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read Git index: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func hookTestGit(t *testing.T, repoDir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
