//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// daemonize re-launches the current binary as a detached background process.
// See daemon_unix.go for the full description.
func daemonize(filteredArgs []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate own executable: %w", err)
	}

	childArgs := append(filteredArgs, "--daemon-child")

	cmd := exec.Command(exe, childArgs...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// DETACHED_PROCESS (0x8) | CREATE_NEW_PROCESS_GROUP (0x200)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start background process: %w", err)
	}

	fmt.Printf("Traffic Orchestrator daemon started (PID %d)\n", cmd.Process.Pid)
	os.Exit(0)
	return nil // unreachable
}
