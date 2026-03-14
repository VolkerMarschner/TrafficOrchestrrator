//go:build !windows

package update

import (
	"fmt"
	"os"
	"syscall"
)

// applyUpdate atomically replaces currentBinary with tmpFile and exec's the new binary.
// syscall.Exec replaces the current process image — the function never returns on success.
func applyUpdate(tmpFile, currentBinary string, restartArgs []string) error {
	if err := os.Rename(tmpFile, currentBinary); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("update: rename %q → %q: %w", tmpFile, currentBinary, err)
	}

	// Replace the current process with the new binary (same PID group, same environment).
	argv := append([]string{currentBinary}, restartArgs...)
	if err := syscall.Exec(currentBinary, argv, os.Environ()); err != nil {
		return fmt.Errorf("update: exec new binary: %w", err)
	}
	return nil // unreachable
}
