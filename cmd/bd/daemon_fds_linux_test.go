//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDaemonDoesNotInheritExtraFileDescriptor(t *testing.T) {
	repo := t.TempDir()
	bdBinary := filepath.Join(t.TempDir(), "bd")

	sourceDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get source directory: %v", err)
	}
	build := exec.Command("go", "build", "-o", bdBinary, ".")
	build.Dir = sourceDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build bd test binary: %v\n%s", err, output)
	}

	gitInit := exec.Command("git", "init", "-q", "-b", "main")
	gitInit.Dir = repo
	if output, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("initialize test repository: %v\n%s", err, output)
	}
	gitName := exec.Command("git", "config", "user.name", "FD Test")
	gitName.Dir = repo
	if output, err := gitName.CombinedOutput(); err != nil {
		t.Fatalf("configure test repository name: %v\n%s", err, output)
	}
	gitEmail := exec.Command("git", "config", "user.email", "fd@example.invalid")
	gitEmail.Dir = repo
	if output, err := gitEmail.CombinedOutput(); err != nil {
		t.Fatalf("configure test repository email: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fd test\n"), 0644); err != nil {
		t.Fatalf("write test repository file: %v", err)
	}
	commit := exec.Command("git", "add", "README.md")
	commit.Dir = repo
	if output, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("stage test repository file: %v\n%s", err, output)
	}
	commit = exec.Command("git", "commit", "-m", "initial")
	commit.Dir = repo
	if output, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("commit test repository file: %v\n%s", err, output)
	}

	init := exec.Command(bdBinary, "init", "--quiet", "--prefix", "fdtest")
	init.Dir = repo
	init.Env = append(os.Environ(), "BEADS_NO_DAEMON=1")
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("initialize bd test repository: %v\n%s", err, output)
	}

	extraPath := filepath.Join(repo, "inherited-fd")
	extraFile, err := os.OpenFile(extraPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("open inherited descriptor: %v", err)
	}
	defer func() { _ = extraFile.Close() }()

	t.Cleanup(func() {
		stop := exec.Command(bdBinary, "daemon", "--stop")
		stop.Dir = repo
		if output, err := stop.CombinedOutput(); err != nil {
			t.Errorf("stop test daemon: %v\n%s", err, output)
		}
	})

	start := exec.Command(bdBinary, "daemon", "--start")
	start.Dir = repo
	start.ExtraFiles = []*os.File{extraFile}
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start daemon with inherited descriptor: %v\n%s", err, output)
	}

	pidFile := filepath.Join(repo, ".beads", "daemon.pid")
	pid := waitForDaemonPID(t, pidFile)
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		t.Fatalf("list daemon file descriptors: %v", err)
	}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "fd", fd.Name()))
		if err != nil {
			t.Fatalf("read daemon file descriptor %s: %v", fd.Name(), err)
		}
		if target == extraPath {
			t.Fatalf("daemon inherited extra file descriptor %s", fd.Name())
		}
	}
}

func waitForDaemonPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon pid file %s was not ready", pidFile)
	return 0
}
