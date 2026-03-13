// Package main implements the Traffic Orchestrator CLI entry point.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"trafficorch/pkg/config"
	"trafficorch/pkg/logging"
	"trafficorch/pkg/netutils"
)

const version = "0.2.0"

func main() {
	// No mode specified → try agent.conf, otherwise show help
	if len(os.Args) < 2 {
		tryAgentConfOrHelp()
		return
	}

	mode := os.Args[1]

	config.DebugMode = false

	switch mode {
	case "--master", "-m":
		handleMasterMode(os.Args[2:])
	case "--agent", "-a":
		handleAgentMode(os.Args[2:])
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

// tryAgentConfOrHelp checks for agent.conf in the current directory.
// If found and valid, the agent starts immediately without any CLI flags.
// If not found, the help page is printed and the process exits cleanly.
func tryAgentConfOrHelp() {
	cfg, err := config.LoadAgentConf(config.AgentConfFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Traffic Orchestrator v%s\n\n", version)
			fmt.Printf("No mode specified and no %s found in current directory.\n\n", config.AgentConfFile)
		} else {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n\n", config.AgentConfFile, err)
		}
		printUsage()
		os.Exit(0)
	}

	fmt.Printf("Traffic Orchestrator v%s — loading configuration from %s\n", version, config.AgentConfFile)
	startAgent(cfg)
}

func printUsage() {
	fmt.Printf(`Traffic Orchestrator - Network Traffic Generator

Version: %s

Usage: trafficorch <mode> [options]

Modes:
  --master, -m    Run as Master (coordinates agents)
    Options:
      --config <FILE>   Path to traffic config file (required)
      --port   <PORT>   Override listen port from config
      --psk    <KEY>    Override pre-shared key from config

  --agent, -a     Run as Agent (generates traffic on command)
    Options:
      --master <HOST>   Master host or IP (required)
      --port   <PORT>   Master port (required)
      --psk    <KEY>    Pre-shared key (required)
      --id     <ID>     Agent identifier (optional)
    Note: supplied options are saved to agent.conf for subsequent runs.
          On next start without arguments the saved configuration is used.

  --version, -v   Show version information
  --help, -h      Show this help message

Auto-start (agent.conf):
  If no arguments are given, trafficorch looks for agent.conf in the current
  directory and starts in agent mode automatically.  The file is created
  automatically the first time you run with --agent … flags.

Environment variables:
  TRAFFICORCH_PSK        Pre-shared key (alternative to --psk)
  TRAFFICORCH_LOG_DIR    Directory for log files (default: current directory)
`, version)
}

// resolveLogPath returns an absolute, safe log file path.
// Reads TRAFFICORCH_LOG_DIR env var; defaults to current directory.
func resolveLogPath(filename string) (string, error) {
	logDir := os.Getenv("TRAFFICORCH_LOG_DIR")
	if logDir == "" {
		logDir = "."
	}

	absDir, err := filepath.Abs(logDir)
	if err != nil {
		return "", fmt.Errorf("invalid log directory %q: %w", logDir, err)
	}

	// Reject path traversal in filename
	if filepath.Base(filename) != filename {
		return "", fmt.Errorf("log filename must not contain path separators: %q", filename)
	}

	return filepath.Join(absDir, filename), nil
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

	// CLI flags override config-file values
	if cfg.Port > 0 {
		masterCfg.Port = cfg.Port
	}
	if cfg.PSK != "" {
		masterCfg.PSK = cfg.PSK
	}

	// SEC-5: Validate PSK strength before starting
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
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
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

// handleAgentMode parses CLI flags, persists them as agent.conf, then starts
// the agent.  If no flags are supplied it falls back to tryAgentConfOrHelp.
func handleAgentMode(args []string) {
	if len(args) == 0 {
		tryAgentConfOrHelp()
		return
	}

	cfg, err := config.ParseAgentArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'trafficorch --help' for usage.")
		os.Exit(1)
	}

	// Persist parameters for the next run
	if saveErr := config.SaveAgentConf(config.AgentConfFile, cfg); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save %s: %v\n", config.AgentConfFile, saveErr)
	} else {
		fmt.Printf("Configuration saved to %s (used automatically on next start).\n", config.AgentConfFile)
	}

	startAgent(cfg)
}

// startAgent validates the PSK, initialises logging and runs the agent.
// It is shared by handleAgentMode (CLI path) and tryAgentConfOrHelp (conf path).
func startAgent(cfg *config.AgentConfig) {
	// SEC-5: Validate PSK strength before connecting
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
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	agent, err := NewAgent(cfg, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create agent: %v\n", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Starting Traffic Orchestrator Agent v%s", version))
	if err := agent.Start(); err != nil {
		logger.Error(fmt.Sprintf("Agent error: %v", err))
		os.Exit(1)
	}
}
