//go:build (unix || darwin) && !linux

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func markFDsCloexecFromThree() error {
	entries, directory, err := readOpenFDs()
	if err != nil {
		return err
	}

	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < 3 {
			continue
		}

		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			if errors.Is(err, unix.EBADF) {
				continue
			}
			return fmt.Errorf("get flags for file descriptor %d from %s: %w", fd, directory, err)
		}
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags|unix.FD_CLOEXEC); err != nil {
			if errors.Is(err, unix.EBADF) {
				continue
			}
			return fmt.Errorf("mark file descriptor %d from %s close-on-exec: %w", fd, directory, err)
		}
	}

	return nil
}

func readOpenFDs() ([]os.DirEntry, string, error) {
	entries, err := os.ReadDir("/dev/fd")
	if err == nil {
		return entries, "/dev/fd", nil
	}

	entries, procErr := os.ReadDir("/proc/self/fd")
	if procErr != nil {
		return nil, "", fmt.Errorf("list open file descriptors from /dev/fd or /proc/self/fd: %w", procErr)
	}
	return entries, "/proc/self/fd", nil
}
