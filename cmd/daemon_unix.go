//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// daemonize re-launches the current binary as a detached background process.
//
// filteredArgs are the arguments to pass to the child process; the -d/--daemon
// flag must already be removed by the caller.  The internal sentinel flag
// --daemon-child is appended automatically so the child knows it is the daemon.
//
// On success the parent prints the child PID and exits with code 0.
// The child continues with the normal startup flow and writes a PID file.
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
	// Create a new session so the child is fully detached from the terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start background process: %w", err)
	}

	fmt.Printf("Traffic Orchestrator daemon started (PID %d)\n", cmd.Process.Pid)
	os.Exit(0)
	return nil // unreachable
}
