// Package main implements the Traffic Orchestrator Master CLI entry point.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"trafficorch/pkg/comm"
	"trafficorch/pkg/config"
	"trafficorch/pkg/logging"
	"trafficorch/pkg/registry"
	"trafficorch/pkg/update"
)

// MasterServer wraps the communication master server.
type MasterServer struct {
	server      *comm.MasterServer
	configPath  string
	cfg         *config.MasterConfig
	rules       []*config.TrafficRule
	ruleMu      sync.RWMutex
	fileWatcher chan struct{}
	logger      *logging.Logger

	// v0.4.5 additions
	reg            *registry.Registry
	httpSrv        *http.Server
	binaryPath     string // path to own executable
	binarySHA      string // pre-computed SHA-256 of own binary
	updateNotified sync.Map // agentID → struct{}: agents already sent UPDATE_AVAILABLE
}

// NewMasterServer creates a new master server instance.
func NewMasterServer(cfg *config.MasterConfig, logger *logging.Logger) (*MasterServer, error) {
	// ── Agent registry ────────────────────────────────────────────────────────
	reg, err := registry.New(registryFile)
	if err != nil {
		return nil, fmt.Errorf("failed to initialise agent registry: %w", err)
	}

	ms := &MasterServer{
		configPath:  cfg.ConfigPath,
		cfg:         cfg,
		fileWatcher: make(chan struct{}, 1),
		logger:      logger,
		reg:         reg,
	}

	ms.server, err = comm.NewMasterServer(
		cfg.PSK,
		cfg.Port,
		ms.onAgentRegister,
		ms.onTrafficRequest,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create master server: %w", err)
	}

	// Register optional callbacks (v0.4.5).
	ms.server.SetOnHeartbeat(ms.onAgentHeartbeat)
	ms.server.SetOnDisconnect(ms.onAgentDisconnect)

	// Load initial configuration.
	if err := ms.loadConfig(); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Start the binary distribution HTTP server.
	if err := ms.startDistributionServer(); err != nil {
		logger.Warn(fmt.Sprintf("Distribution server unavailable: %v", err))
	}

	// Start file watcher for automatic config reload.
	go ms.watchConfigFile()

	ms.logger.Info(fmt.Sprintf("Master server initialised on port %d (TTL=%ds)", cfg.Port, cfg.TTL))
	return ms, nil
}

// ─── Callbacks ────────────────────────────────────────────────────────────────

// onAgentRegister is called when a new agent registers.
func (ms *MasterServer) onAgentRegister(agentID string, hostname string) {
	agentIPs := ms.server.GetAgentIPs()
	agentIP := agentIPs[agentID]
	agentVer := ms.server.GetAgentVersion(agentID)
	agentPlatform := ms.server.GetAgentPlatform(agentID)

	ms.logger.Info(fmt.Sprintf("New agent registered: %s (%s) v%s @ %s",
		agentID, hostname, agentVer, agentIP))

	// Upsert into persistent registry.
	ms.reg.Upsert(registry.AgentRecord{
		ID:       agentID,
		Hostname: hostname,
		IP:       agentIP,
		Version:  agentVer,
		Platform: agentPlatform,
		LastSeen: time.Now(),
		Status:   "online",
	})

	// Give the channel a moment to settle before pushing config.
	time.Sleep(200 * time.Millisecond)
	ms.distributeRulesToAgent(agentID)
}

// onTrafficRequest handles traffic generation requests from agents.
func (ms *MasterServer) onTrafficRequest(agentID string, rules []*comm.TrafficRule) {
	ms.logger.Info(fmt.Sprintf("Traffic request from %s: %d rules", agentID, len(rules)))
}

// onAgentHeartbeat is called on every agent heartbeat.
func (ms *MasterServer) onAgentHeartbeat(agentID string, hb comm.HeartbeatMessage) {
	ms.reg.UpdateSeen(agentID, hb.AgentVersion)

	// Check whether the agent needs an update.
	if hb.AgentVersion != "" && needsUpdate(hb.AgentVersion, version) {
		if _, alreadySent := ms.updateNotified.LoadOrStore(agentID, struct{}{}); !alreadySent {
			ms.sendUpdateNotification(agentID)
		}
	}
}

// onAgentDisconnect is called when an agent disconnects.
func (ms *MasterServer) onAgentDisconnect(agentID string) {
	ms.logger.Info(fmt.Sprintf("Agent %s disconnected", agentID))
	ms.reg.SetOffline(agentID)
	ms.updateNotified.Delete(agentID) // allow re-notification on next connect
}

// ─── Update notification ──────────────────────────────────────────────────────

// sendUpdateNotification sends an UPDATE_AVAILABLE message to an agent.
func (ms *MasterServer) sendUpdateNotification(agentID string) {
	if ms.binarySHA == "" {
		return // distribution server not available
	}
	msg := &comm.UpdateAvailableMessage{
		BaseMessage: comm.BaseMessage{
			Type:      comm.MsgUpdateAvailable,
			Timestamp: time.Now().Unix(),
			Version:   comm.ProtocolVersion,
		},
		NewVersion: version,
		HTTPPort:   distributionPort,
		SHA256:     ms.binarySHA,
	}
	if err := ms.server.SendToAgent(agentID, msg); err != nil {
		ms.logger.Warn(fmt.Sprintf("Failed to send UPDATE_AVAILABLE to %s: %v", agentID, err))
	} else {
		ms.logger.Info(fmt.Sprintf("Sent UPDATE_AVAILABLE to agent %s (new: v%s)", agentID, version))
	}
}

// ─── Distribution HTTP server ─────────────────────────────────────────────────

// startDistributionServer starts the HTTP server on distributionPort.
func (ms *MasterServer) startDistributionServer() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate own executable: %w", err)
	}
	ms.binaryPath = exe

	sha, err := update.BinaryChecksum(exe)
	if err != nil {
		return fmt.Errorf("cannot compute binary checksum: %w", err)
	}
	ms.binarySHA = sha

	mux := http.NewServeMux()
	mux.HandleFunc("/binary", ms.handleBinaryDownload)
	mux.HandleFunc("/sha256", ms.handleSHA256)
	mux.HandleFunc("/version", ms.handleVersion)
	mux.HandleFunc("/agents", ms.handleAgents)

	ms.httpSrv = &http.Server{
		Addr:         fmt.Sprintf(":%d", distributionPort),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		ms.logger.Info(fmt.Sprintf("Distribution server listening on port %d (SHA256: %s...)",
			distributionPort, ms.binarySHA[:16]))
		if err := ms.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			ms.logger.Error(fmt.Sprintf("Distribution server error: %v", err))
		}
	}()

	return nil
}

func (ms *MasterServer) handleBinaryDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f, err := os.Open(ms.binaryPath)
	if err != nil {
		http.Error(w, "binary unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "binary unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("X-SHA256", ms.binarySHA)
	w.Header().Set("X-Version", version)
	if r.Method == http.MethodHead {
		return
	}
	io.Copy(w, f) //nolint:errcheck
}

func (ms *MasterServer) handleSHA256(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintln(w, ms.binarySHA)
}

func (ms *MasterServer) handleVersion(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintln(w, version)
}

func (ms *MasterServer) handleAgents(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	records := ms.reg.All()
	if err := json.NewEncoder(w).Encode(records); err != nil {
		ms.logger.Warn(fmt.Sprintf("Failed to encode agents response: %v", err))
	}
}

// ─── Config loading ───────────────────────────────────────────────────────────

// loadConfig re-parses the configuration file and updates the active rule set.
func (ms *MasterServer) loadConfig() error {
	freshCfg, err := config.ParseExtendedConfigV2(ms.configPath)
	if err != nil {
		return fmt.Errorf("failed to parse config file %q: %w", ms.configPath, err)
	}

	// Load profiles when PROFILE_DIR is configured.
	if freshCfg.ProfileDir != "" {
		profiles, err := config.LoadProfileDir(freshCfg.ProfileDir)
		if err != nil {
			ms.logger.Warn(fmt.Sprintf("Could not load profiles from %q: %v", freshCfg.ProfileDir, err))
		} else {
			freshCfg.Profiles = profiles
			ms.logger.Info(fmt.Sprintf("Loaded %d profile(s) from %s", len(profiles), freshCfg.ProfileDir))
		}
	}

	ms.ruleMu.Lock()
	ms.rules = freshCfg.TrafficRules
	ms.cfg.TTL = freshCfg.TTL
	ms.cfg.TargetMap = freshCfg.TargetMap
	ms.cfg.Assignments = freshCfg.Assignments
	ms.cfg.TagMap = freshCfg.TagMap
	ms.cfg.Profiles = freshCfg.Profiles
	ms.cfg.ProfileDir = freshCfg.ProfileDir
	ms.ruleMu.Unlock()

	ms.logger.Info(fmt.Sprintf("Loaded %d traffic rule(s) from %s (TTL=%ds, profiles=%d, assignments=%d)",
		len(freshCfg.TrafficRules), ms.configPath, freshCfg.TTL,
		len(freshCfg.Profiles), len(freshCfg.Assignments)))
	return nil
}

// watchConfigFile monitors the config file for changes.
func (ms *MasterServer) watchConfigFile() {
	var lastModTime time.Time

	for {
		select {
		case <-time.After(configWatchInterval):
			info, err := os.Stat(ms.configPath)
			if err != nil {
				ms.logger.Error(fmt.Sprintf("Config file not found: %s", ms.configPath))
				continue
			}
			modTime := info.ModTime()
			if !modTime.Equal(lastModTime) && !lastModTime.IsZero() {
				ms.logger.Info("Config file changed, reloading...")
				go ms.loadConfigAndNotify()
			}
			lastModTime = modTime

		case <-ms.fileWatcher:
			ms.logger.Info("Config reload triggered manually")
			go ms.loadConfigAndNotify()
		}
	}
}

// loadConfigAndNotify reloads config and pushes updates to all agents.
func (ms *MasterServer) loadConfigAndNotify() {
	if err := ms.loadConfig(); err != nil {
		ms.logger.Error(fmt.Sprintf("Failed to reload config: %v", err))
		return
	}
	ms.notifyAllAgents()
}

// notifyAllAgents distributes the current ruleset to every connected agent.
func (ms *MasterServer) notifyAllAgents() {
	agentIPs := ms.server.GetAgentIPs()
	for agentID := range agentIPs {
		ms.distributeRulesToAgent(agentID)
	}
}

// ─── Rule distribution ────────────────────────────────────────────────────────

// distributeRulesToAgent builds a per-agent rule set and sends a CONFIG_UPDATE.
//
// Distribution logic (v0.4.0+):
//
//  1. Profile-based (preferred, v0.4.0+):
//     If the agent IP has entries in [ASSIGNMENTS] and profiles are loaded,
//     rules are resolved from the assigned profiles (SELF/group:/ANY expanded).
//
//  2. Direct rule fallback (v0.3.0 / legacy):
//     Rules with Source=="" → sent to ALL agents (connect).
//     Rules with Source!="" → sent to the matching source agent (connect) or
//     the matching destination agent (listen).
//
// Both paths are additive: a config can mix profile assignments and direct rules.
func (ms *MasterServer) distributeRulesToAgent(agentID string) {
	agentIPs := ms.server.GetAgentIPs()
	agentIP := agentIPs[agentID]

	ms.ruleMu.RLock()
	allRules := ms.rules
	ttl := ms.cfg.TTL
	assignments := ms.cfg.Assignments
	tagMap := ms.cfg.TagMap
	targetMap := ms.cfg.TargetMap
	profiles := ms.cfg.Profiles
	ms.ruleMu.RUnlock()

	var agentRules []*comm.TrafficRule

	// ── Profile-based distribution (v0.4.0) ──────────────────────────────────
	if len(profiles) > 0 && len(assignments) > 0 && agentIP != "" {
		profileNames := config.LookupAssignments(agentIP, assignments, targetMap)
		if len(profileNames) > 0 {
			resolved, err := config.ResolveProfileRules(profiles, profileNames, agentIP, targetMap, tagMap)
			if err != nil {
				ms.logger.Error(fmt.Sprintf("Profile resolution failed for agent %s (IP=%s): %v", agentID, agentIP, err))
			} else {
				for _, r := range resolved {
					agentRules = append(agentRules, &comm.TrafficRule{
						Protocol: r.Protocol,
						Role:     r.Role,
						Source:   r.Source,
						Target:   r.Target,
						Port:     r.Port,
						Interval: r.Interval,
						Count:    r.Count,
						Name:     r.Name,
					})
				}
				ms.logger.Info(fmt.Sprintf("Agent %s (IP=%s): %d rule(s) from profile(s) %v",
					agentID, agentIP, len(agentRules), profileNames))
			}
		}
	}

	// ── Direct rule distribution (legacy / additive fallback) ─────────────────
	for _, rule := range allRules {
		if rule.Source == "" {
			agentRules = append(agentRules, &comm.TrafficRule{
				Protocol: rule.Protocol,
				Target:   rule.Target,
				Port:     rule.Port,
				Interval: rule.Interval,
				Count:    rule.Count,
				Name:     rule.Name,
				Role:     "connect",
			})
		} else {
			if agentIP == rule.Source {
				agentRules = append(agentRules, &comm.TrafficRule{
					Protocol: rule.Protocol,
					Source:   rule.Source,
					Target:   rule.Target,
					Port:     rule.Port,
					Count:    rule.Count,
					Name:     rule.Name,
					Role:     "connect",
				})
			} else if agentIP == rule.Target {
				agentRules = append(agentRules, &comm.TrafficRule{
					Protocol: rule.Protocol,
					Port:     rule.Port,
					Name:     rule.Name,
					Role:     "listen",
				})
			}
		}
	}

	if len(agentRules) == 0 {
		ms.logger.Debug(fmt.Sprintf("No rules applicable to agent %s (IP=%s)", agentID, agentIP))
		return
	}

	msg := &comm.ConfigUpdateMessage{
		BaseMessage: comm.BaseMessage{
			Type:      comm.MsgConfigUpdate,
			Timestamp: time.Now().Unix(),
			Version:   "1.0",
		},
		TTL:   ttl,
		Rules: agentRules,
	}

	if err := ms.server.SendToAgent(agentID, msg); err != nil {
		ms.logger.Error(fmt.Sprintf("Failed to send config to agent %s: %v", agentID, err))
	} else {
		ms.logger.Info(fmt.Sprintf("Sent %d rule(s) to agent %s (TTL=%ds)", len(agentRules), agentID, ttl))
	}
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

// Start starts the master server and blocks until a shutdown signal is received.
func (ms *MasterServer) Start() error {
	ms.logger.Info(fmt.Sprintf("Starting Traffic Orchestrator Master v%s", version))
	ms.logger.Info(fmt.Sprintf("Listening on port %d with PSK authentication", ms.cfg.Port))
	ms.logger.Info(fmt.Sprintf("Config file: %s (%d rules loaded)", ms.configPath, len(ms.rules)))

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	ms.logger.Info("Shutdown signal received")

	return ms.Stop(ms.logger)
}

// Stop gracefully shuts down the master server and the HTTP distribution server.
func (ms *MasterServer) Stop(logger *logging.Logger) error {
	logger.Info("Shutting down Master server...")
	ms.server.CloseAllAgents()

	if ms.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ms.httpSrv.Shutdown(ctx); err != nil {
			logger.Warn(fmt.Sprintf("HTTP server shutdown: %v", err))
		}
	}
	return nil
}

// GetConfigPath returns the current configuration file path.
func (ms *MasterServer) GetConfigPath() string {
	return ms.configPath
}

// ReloadConfig manually triggers a config reload.
func (ms *MasterServer) ReloadConfig() error {
	ms.fileWatcher <- struct{}{}
	return nil
}

// ─── Version helpers ──────────────────────────────────────────────────────────

// needsUpdate reports whether agentVer is strictly older than masterVer.
func needsUpdate(agentVer, masterVer string) bool {
	return compareVersions(agentVer, masterVer) < 0
}

// compareVersions compares two "major.minor.patch" version strings.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func compareVersions(a, b string) int {
	pa := parseVer(a)
	pb := parseVer(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVer(v string) [3]int {
	var maj, min, pat int
	fmt.Sscanf(v, "%d.%d.%d", &maj, &min, &pat)
	return [3]int{maj, min, pat}
}
