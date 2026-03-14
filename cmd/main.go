// Package main implements the Traffic Orchestrator CLI entry point.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"trafficorch/pkg/config"
	"trafficorch/pkg/logging"
	"trafficorch/pkg/netutils"
	"trafficorch/pkg/registry"
)

const version = "0.4.5"

func main() {
	args := os.Args[1:]

	// ── Detect and strip internal daemon-child sentinel ───────────────────────
	// When the process was launched by daemonize(), --daemon-child is appended
	// to signal that we are already the background process.
	daemonChild := false
	{
		filtered := args[:0]
		for _, a := range args {
			if a == "--daemon-child" {
				daemonChild = true
			} else {
				filtered = append(filtered, a)
			}
		}
		args = filtered
	}
	if daemonChild {
		writePIDFile(pidFile)
	}

	// ── Detect and strip -d / --daemon flag ───────────────────────────────────
	daemon := false
	{
		filtered := args[:0]
		for _, a := range args {
			if a == "-d" || a == "--daemon" {
				daemon = true
			} else {
				filtered = append(filtered, a)
			}
		}
		args = filtered
	}
	if daemon && !daemonChild {
		if err := daemonize(args); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
			os.Exit(1)
		}
		return // unreachable — daemonize calls os.Exit(0)
	}

	config.DebugMode = false

	// ── No arguments: auto-start from to.conf ─────────────────────────────────
	if len(args) == 0 {
		tryAutoStart()
		return
	}

	mode := args[0]

	switch mode {
	case "--master", "-m":
		handleMasterMode(args[1:])
	case "--agent", "-a":
		handleAgentMode(args[1:])
	case "--status", "-s":
		handleStatusMode()
	case "--version", "-v":
		fmt.Printf("Traffic Orchestrator v%s\n", version)
		os.Exit(0)
	case "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", mode)
		printUsage()
		os.Exit(1)
	}
}

// tryAutoStart is invoked when no arguments are supplied.
func tryAutoStart() {
	mode, err := config.DetectToConfMode(config.ToConfFile)

	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Traffic Orchestrator v%s\n\n", version)
			fmt.Printf("%s not found in current directory.\n\n", config.ToConfFile)
			printUsage()
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", config.ToConfFile, err)
		fmt.Fprintf(os.Stderr, "Fix or delete %s and try again.\n", config.ToConfFile)
		os.Exit(1)
	}

	switch mode {
	case config.ToConfModeMaster:
		fmt.Printf("Traffic Orchestrator v%s — master mode from %s\n", version, config.ToConfFile)
		startMasterFromFile(config.ToConfFile)
	case config.ToConfModeAgent:
		fmt.Printf("Traffic Orchestrator v%s — agent mode from %s\n", version, config.ToConfFile)
		cfg, err := config.LoadAgentConf(config.ToConfFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", config.ToConfFile, err)
			os.Exit(1)
		}
		startAgent(cfg)
	}
}

// handleStatusMode prints the agent registry table to stdout.
func handleStatusMode() {
	reg, err := registry.New(registryFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot load agent registry (%s): %v\n", registryFile, err)
		os.Exit(1)
	}
	fmt.Printf("Traffic Orchestrator v%s — Agent Registry\n\n", version)
	reg.PrintTable(os.Stdout)
}

func printUsage() {
	fmt.Printf(`Traffic Orchestrator — Network Traffic Generator

Version: %s

Usage: trafficorch [options] <mode> [mode-options]

Global options:
  -d, --daemon    Run as a background daemon (detached from terminal).
                  A PID file is written to trafficorch.pid in the working directory.

Modes:
  --master, -m    Run as Master (coordinates agents)
    Options:
      --config <FILE>   Path to traffic config file (required)
      --port   <PORT>   Override listen port from config
      --psk    <KEY>    Override pre-shared key from config

  --agent, -a     Run as Agent (generates / receives traffic on command)
    Options:
      --master <HOST>   Master host or IP (required on first run)
      --port   <PORT>   Master port (required on first run)
      --psk    <KEY>    Pre-shared key (required on first run)
      --id     <ID>     Agent identifier (optional)

    First run: supply all flags. They are saved to to.conf for subsequent
    starts, and rules received from the master are saved to instructions.conf.

    Subsequent runs: just run  trafficorch  with no arguments.

  --status, -s    Print a table of all known agents and their status.
                  Reads agents.json written by the master.

  --version, -v   Show version information
  --help, -h      Show this help message

Deployment (v0.4.5+):
  Bootstrap new agent:
    curl -O http://<master-ip>:9001/binary && chmod +x binary
    ./binary --agent --master <master-ip> --port 9000 --psk <key>

  Auto-update:
    The master serves its own binary on port 9001. When an agent connects
    with an older version, the master sends an UPDATE_AVAILABLE notification.
    The agent downloads, verifies (SHA-256) and restarts itself automatically.

  HTTP endpoints on port 9001:
    GET /binary   — Download the master binary
    GET /sha256   — SHA-256 checksum of the binary
    GET /version  — Current master version
    GET /agents   — Agent registry as JSON

Auto-start:
  No arguments:  trafficorch looks for to.conf in the current directory.
  Found:     loads values and starts as agent or master.
  Not found: prints this help message.

Environment variables:
  TRAFFICORCH_PSK        Pre-shared key (alternative to --psk)
  TRAFFICORCH_LOG_DIR    Directory for log files (default: current directory)
`, version)
}

// resolveLogPath returns an absolute, safe log file path.
func resolveLogPath(filename string) (string, error) {
	logDir := os.Getenv("TRAFFICORCH_LOG_DIR")
	if logDir == "" {
		logDir = "."
	}

	absDir, err := filepath.Abs(logDir)
	if err != nil {
		return "", fmt.Errorf("invalid log directory %q: %w", logDir, err)
	}

	if filepath.Base(filename) != filename {
		return "", fmt.Errorf("log filename must not contain path separators: %q", filename)
	}

	return filepath.Join(absDir, filename), nil
}

// writePIDFile writes the current process PID to path (best-effort, errors are ignored).
func writePIDFile(path string) {
	_ = os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
}

func handleMasterMode(args []string) {
	cfg, err := config.ParseMasterArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'trafficorch --help' for usage.")
		os.Exit(1)
	}

	masterCfg, err := config.ParseExtendedConfigV2(cfg.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// CLI flags override config-file values.
	if cfg.Port > 0 {
		masterCfg.Port = cfg.Port
	}
	if cfg.PSK != "" {
		masterCfg.PSK = cfg.PSK
	}

	runMaster(masterCfg)
}

// startMasterFromFile loads a master config directly from a file path and starts
// the master server. Used by tryAutoStart when to.conf is in master format.
func startMasterFromFile(configPath string) {
	masterCfg, err := config.ParseExtendedConfigV2(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading %s: %v\n", configPath, err)
		os.Exit(1)
	}
	runMaster(masterCfg)
}

// runMaster validates the PSK, sets up logging, and runs the master server.
func runMaster(masterCfg *config.MasterConfig) {
	if err := netutils.ValidatePSKStrength(masterCfg.PSK); err != nil {
		fmt.Fprintf(os.Stderr, "Error: PSK does not meet security requirements: %v\n", err)
		os.Exit(1)
	}

	logFile, err := resolveLogPath("traffic.log")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving log path: %v\n", err)
		os.Exit(1)
	}

	logger, err := logging.NewLogger(logFile, defaultLogMaxSizeMB, defaultLogMaxFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialise logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	server, err := NewMasterServer(masterCfg, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create master server: %v\n", err)
		os.Exit(1)
	}
	defer server.Stop(logger)

	logger.Info(fmt.Sprintf("Starting Traffic Orchestrator Master v%s", version))
	if err := server.Start(); err != nil {
		logger.Error(fmt.Sprintf("Master server error: %v", err))
		os.Exit(1)
	}
}

// handleAgentMode parses CLI flags, persists them as to.conf, then starts
// the agent. Falls back to auto-start if no flags are supplied.
func handleAgentMode(args []string) {
	if len(args) == 0 {
		tryAutoStart()
		return
	}

	cfg, err := config.ParseAgentArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'trafficorch --help' for usage.")
		os.Exit(1)
	}

	// Persist for the next run.
	if saveErr := config.SaveAgentConf(config.ToConfFile, cfg); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save %s: %v\n", config.ToConfFile, saveErr)
	} else {
		fmt.Printf("Configuration saved to %s.\n", config.ToConfFile)
	}

	startAgent(cfg)
}

// startAgent validates the PSK, initialises logging, tries to connect to master
// and — if the master is unreachable — falls back to standalone mode.
func startAgent(cfg *config.AgentConfig) {
	if err := netutils.ValidatePSKStrength(cfg.PSK); err != nil {
		fmt.Fprintf(os.Stderr, "Error: PSK does not meet security requirements: %v\n", err)
		os.Exit(1)
	}

	logFile, err := resolveLogPath("agent.log")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving log path: %v\n", err)
		os.Exit(1)
	}

	logger, err := logging.NewLogger(logFile, defaultLogMaxSizeMB, defaultLogMaxFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialise logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	logger.Info(fmt.Sprintf("Starting Traffic Orchestrator Agent v%s", version))

	// Try connected mode first.
	agent, err := NewAgent(cfg, logger)
	if err != nil {
		logger.Warn(fmt.Sprintf("Cannot connect to master (%v) — trying standalone mode...", err))
		mCfg := masterConnInfo{host: cfg.MasterHost, port: cfg.Port, psk: cfg.PSK}
		startStandaloneWithLogger(mCfg, cfg.AgentID, logger)
		return
	}

	// Non-root check (connected mode — warning sent to master).
	checkNonRootAndWarn(agent.agentID, agent.client, logger)

	if err := agent.Start(); err != nil {
		logger.Error(fmt.Sprintf("Agent error: %v", err))
		os.Exit(1)
	}
}

// startStandalone is the standalone entry point used when no master credentials
// are available from CLI (e.g. auto-start from instructions.conf alone).
func startStandalone(mCfg masterConnInfo, agentID string) {
	logFile, err := resolveLogPath("agent.log")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving log path: %v\n", err)
		os.Exit(1)
	}

	logger, err := logging.NewLogger(logFile, defaultLogMaxSizeMB, defaultLogMaxFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialise logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	logger.Info(fmt.Sprintf("Starting Traffic Orchestrator Agent v%s (standalone)", version))
	startStandaloneWithLogger(mCfg, agentID, logger)
}

// startStandaloneWithLogger creates a standalone agent and starts it.
func startStandaloneWithLogger(mCfg masterConnInfo, agentID string, logger *logging.Logger) {
	agent, err := newStandaloneAgent(config.InstructionsConfFile, mCfg, agentID, logger)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			msg := fmt.Sprintf(
				"Cannot connect to master and no %s found.\n"+
					"On first run the master must be reachable so the agent can receive its\n"+
					"initial traffic rules. Start the master, then retry.\n",
				config.InstructionsConfFile,
			)
			fmt.Fprint(os.Stderr, msg)
			logger.Error("Standalone start failed: " + config.InstructionsConfFile + " not found and master unreachable")
		} else {
			fmt.Fprintf(os.Stderr, "Standalone start failed: %v\n", err)
			logger.Error(fmt.Sprintf("Standalone start failed: %v", err))
		}
		os.Exit(1)
	}

	// Non-root check (standalone mode — warning logged only, no master to notify).
	checkNonRootAndWarn(agent.agentID, nil, logger)

	if err := agent.Start(); err != nil {
		logger.Error(fmt.Sprintf("Agent error: %v", err))
		os.Exit(1)
	}
}
