// Package update implements binary self-update logic for Traffic Orchestrator agents.
//
// Flow:
//  1. Master detects that an agent's version is lower than its own.
//  2. Master sends UPDATE_AVAILABLE over the control channel (port 9000).
//  3. Agent calls Apply(), which downloads the binary from the master's HTTP
//     distribution server (port 9001), verifies the SHA-256 checksum, and
//     replaces the running binary.
//  4. On Linux/macOS the new binary is exec'd in-place (same PID group).
//     On Windows a helper batch script swaps the files and restarts after exit.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"
)

// BinaryChecksum returns the hex-encoded SHA-256 digest of the file at path.
func BinaryChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("checksum: open %q: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("checksum: read %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Apply downloads a new binary from downloadURL, verifies its SHA-256 against
// sha256Expected, and replaces currentBinary.
//
// restartArgs are the command-line arguments passed to the new process on restart
// (os.Args[1:] filtered to remove internal flags like --daemon-child).
//
// On success this function does not return — it either exec's the new binary
// (Linux/macOS) or exits after spawning a helper script (Windows).
// On failure an error is returned and the current binary is left unchanged.
func Apply(downloadURL, sha256Expected, currentBinary string, restartArgs []string) error {
	tmpFile := currentBinary + ".new"
	if runtime.GOOS == "windows" {
		tmpFile = currentBinary + "_new.exe"
	}

	// ── Download ──────────────────────────────────────────────────────────────
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("update: download %q: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: server returned HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("update: create temp file %q: %w", tmpFile, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("update: write temp file: %w", err)
	}
	f.Close()

	// ── Verify SHA-256 ────────────────────────────────────────────────────────
	actual, err := BinaryChecksum(tmpFile)
	if err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("update: checksum verification failed: %w", err)
	}
	if actual != sha256Expected {
		os.Remove(tmpFile)
		return fmt.Errorf("update: checksum mismatch (got %s, want %s)", actual, sha256Expected)
	}

	// ── Platform-specific apply & restart ─────────────────────────────────────
	return applyUpdate(tmpFile, currentBinary, restartArgs)
}
