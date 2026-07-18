//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func markFDsCloexecFromThree() error {
	err := unix.CloseRange(3, ^uint(0), unix.CLOSE_RANGE_CLOEXEC)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("mark file descriptors close-on-exec: %w", err)
	}

	return markFDsCloexecFromProc()
}

func markFDsCloexecFromProc() error {
	directory, err := os.Open("/proc/self/fd")
	if err != nil {
		return fmt.Errorf("open file descriptor directory: %w", err)
	}
	defer func() { _ = directory.Close() }()

	entries, err := directory.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("list open file descriptors: %w", err)
	}

	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < 3 {
			continue
		}

		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			return fmt.Errorf("get flags for file descriptor %d: %w", fd, err)
		}
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags|unix.FD_CLOEXEC); err != nil {
			return fmt.Errorf("mark file descriptor %d close-on-exec: %w", fd, err)
		}
	}

	return nil
}
