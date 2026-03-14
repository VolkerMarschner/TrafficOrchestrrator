//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
)

// updateBat is a batch script that waits for the current process to exit,
// swaps the binary files, and restarts the new binary without arguments
// (the agent will pick up its configuration from to.conf on restart).
const updateBat = `@echo off
timeout /t 3 /nobreak > nul
move /y "%s" "%s"
if errorlevel 1 (
    echo Update failed: could not replace binary
    exit /b 1
)
start "" "%s"
del "%%~f0"
`

// applyUpdate writes a helper batch script that swaps the new binary into place
// after this process exits, then exits the current process.
// The batch script deletes itself on completion.
func applyUpdate(tmpFile, currentBinary string, _ []string) error {
	batPath := currentBinary + "_update.bat"
	script := fmt.Sprintf(updateBat, tmpFile, currentBinary, currentBinary)

	if err := os.WriteFile(batPath, []byte(script), 0755); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("update: write helper script: %w", err)
	}

	cmd := exec.Command("cmd", "/c", "start", "", "/b", batPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		os.Remove(tmpFile)
		os.Remove(batPath)
		return fmt.Errorf("update: launch helper script: %w", err)
	}

	fmt.Println("Update downloaded. Restarting via helper script...")
	os.Exit(0)
	return nil // unreachable
}
