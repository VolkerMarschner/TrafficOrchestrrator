package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"trafficorch/pkg/comm"
	"trafficorch/pkg/config"
	"trafficorch/pkg/logging"
	"trafficorch/pkg/traffic"
	"trafficorch/pkg/update"
)

// masterConnInfo holds the details needed to (re)connect to a master.
type masterConnInfo struct {
	host string
	port int
	psk  string
}

// Agent handles agent-specific operations.
// It can run in two modes:
//   - Connected: maintains a live channel to the master.
//   - Standalone: executes rules loaded from instructions.conf; reconnects
//     when the TTL expires or the master becomes reachable again.
type Agent struct {
	client       *comm.AgentClient // nil in standalone mode
	agentID      string
	standalone   bool
	masterCfg    masterConnInfo
	currentRules []*config.TrafficRule
	mu           sync.RWMutex
	isRunning    int32 // accessed via sync/atomic
	stopChan     chan struct{}
	listenerMgr  *traffic.ListenerManager
	logger       *logging.Logger
}

// NewAgent creates and registers a connected agent.
// If the master is unreachable it returns an error; the caller may then try
// newStandaloneAgent instead.
func NewAgent(cfg *config.AgentConfig, logger *logging.Logger) (*Agent, error) {
	client, err := comm.NewAgentClient(cfg.MasterHost, cfg.Port, cfg.PSK)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master: %w", err)
	}

	hostname, _ := os.Hostname()
	platform := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)

	logger.Info(fmt.Sprintf("Agent connecting to master at %s:%d", cfg.MasterHost, cfg.Port))
	if err := client.Register(cfg.AgentID, hostname, platform, version); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to register with master: %w", err)
	}

	return &Agent{
		client:      client,
		agentID:     cfg.AgentID,
		standalone:  false,
		masterCfg:   masterConnInfo{cfg.MasterHost, cfg.Port, cfg.PSK},
		stopChan:    make(chan struct{}),
		listenerMgr: traffic.NewListenerManager(),
		logger:      logger,
	}, nil
}

// newStandaloneAgent creates an agent that operates from a local instructions.conf.
// If instrPath does not exist, an error is returned.
func newStandaloneAgent(instrPath string, fallbackCfg masterConnInfo, agentID string, logger *logging.Logger) (*Agent, error) {
	instrConf, err := config.LoadInstructionsConf(instrPath)
	if err != nil {
		return nil, fmt.Errorf("no instructions.conf found (%s): %w", instrPath, err)
	}

	logger.Info(fmt.Sprintf("Standalone mode: loaded %d rules from %s (received %s)",
		len(instrConf.Rules), instrPath, instrConf.ReceivedAt.Format(time.RFC3339)))

	if instrConf.TTL > 0 {
		if instrConf.IsExpired() {
			logger.Warn("instructions.conf TTL has already expired — will attempt to reconnect to master")
		} else {
			logger.Info(fmt.Sprintf("Instructions valid for another %s (TTL %ds)",
				instrConf.ExpiresIn().Round(time.Second), instrConf.TTL))
		}
	}

	// Use master conn info from instructions.conf; fall back to CLI-supplied values.
	mCfg := masterConnInfo{
		host: instrConf.MasterHost,
		port: instrConf.MasterPort,
		psk:  instrConf.PSK,
	}
	if fallbackCfg.host != "" {
		mCfg = fallbackCfg
	}

	id := instrConf.AgentID
	if agentID != "" {
		id = agentID
	}
	if id == "" {
		id = "agent-unknown"
	}

	a := &Agent{
		client:       nil,
		agentID:      id,
		standalone:   true,
		masterCfg:    mCfg,
		currentRules: instrConf.Rules,
		stopChan:     make(chan struct{}),
		listenerMgr:  traffic.NewListenerManager(),
		logger:       logger,
	}

	// Schedule TTL-based reconnect if appropriate.
	if instrConf.TTL > 0 {
		go a.ttlReconnectLoop(instrConf)
	}

	return a, nil
}

// ─── Startup ─────────────────────────────────────────────────────────────────

// Start begins the agent's main loop and blocks until shutdown.
func (a *Agent) Start() error {
	a.logger.Info(fmt.Sprintf("Agent %s started (standalone=%v)", a.agentID, a.standalone))
	atomic.StoreInt32(&a.isRunning, 1)

	if !a.standalone {
		go a.receiveMessages()
		go a.sendHeartbeatLoop()
	} else {
		a.applyRules(a.currentRules)
	}

	// Wait for OS shutdown signal or internal stop.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigChan:
		a.logger.Info("Shutdown signal received")
	case <-a.stopChan:
		a.logger.Info("Agent stopping")
	}

	return a.Stop()
}

// ─── Rule application ─────────────────────────────────────────────────────────

// applyRules stops existing listeners, starts new ones for "listen" rules,
// and launches goroutines for "connect" rules.
func (a *Agent) applyRules(rules []*config.TrafficRule) {
	a.listenerMgr.StopAll()

	a.mu.Lock()
	a.currentRules = rules
	a.mu.Unlock()

	var connectRules []*comm.TrafficRule

	for _, rule := range rules {
		r := rule // capture
		if r.Role == "listen" {
			if err := a.listenerMgr.StartListener(r.Protocol, r.Port); err != nil {
				a.logger.Error(fmt.Sprintf("Failed to open %s listener on port %d: %v", r.Protocol, r.Port, err))
			} else {
				a.logger.Info(fmt.Sprintf("Listening on %s port %d", r.Protocol, r.Port))
			}
		} else {
			connectRules = append(connectRules, configRuleToComm(r))
		}
	}

	if len(connectRules) > 0 {
		go a.executeTraffic(connectRules)
	}
}

// configRuleToComm converts a config.TrafficRule to a comm.TrafficRule.
func configRuleToComm(r *config.TrafficRule) *comm.TrafficRule {
	return &comm.TrafficRule{
		Protocol: r.Protocol,
		Source:   r.Source,
		Target:   r.Target,
		Port:     r.Port,
		Interval: r.Interval,
		Count:    r.Count,
		Name:     r.Name,
		Role:     r.Role,
	}
}

// commRulesToConfig converts a slice of comm.TrafficRule to config.TrafficRule.
func commRulesToConfig(rules []*comm.TrafficRule) []*config.TrafficRule {
	out := make([]*config.TrafficRule, len(rules))
	for i, r := range rules {
		out[i] = &config.TrafficRule{
			Protocol: r.Protocol,
			Source:   r.Source,
			Target:   r.Target,
			Port:     r.Port,
			Interval: r.Interval,
			Count:    r.Count,
			Name:     r.Name,
			Role:     r.Role,
		}
	}
	return out
}

// ─── Connected-mode message loop ──────────────────────────────────────────────

// receiveMessages continuously reads and handles messages from the master.
func (a *Agent) receiveMessages() {
	for {
		if atomic.LoadInt32(&a.isRunning) == 0 {
			return
		}

		msg, msgBytes, err := a.client.ReadMessage()
		if err != nil {
			a.logger.Error(fmt.Sprintf("Error receiving message from master: %v", err))
			time.Sleep(reconnectDelay)
			continue
		}

		switch msg.Type {
		case comm.MsgConfigUpdate:
			var configMsg comm.ConfigUpdateMessage
			if err := json.Unmarshal(msgBytes, &configMsg); err == nil {
				a.logger.Info(fmt.Sprintf("Received CONFIG_UPDATE: %d rules (TTL=%ds)", len(configMsg.Rules), configMsg.TTL))
				cfgRules := commRulesToConfig(configMsg.Rules)
				a.applyRules(cfgRules)
				a.saveInstructions(configMsg.TTL, cfgRules)
			}

		case comm.MsgTrafficStart:
			var startMsg comm.TrafficStartMessage
			if err := json.Unmarshal(msgBytes, &startMsg); err == nil {
				a.startTraffic(startMsg.Rules)
			}

		case comm.MsgTrafficStop:
			a.stopTraffic()

		case comm.MsgUpdateAvailable:
			var updateMsg comm.UpdateAvailableMessage
			if err := json.Unmarshal(msgBytes, &updateMsg); err == nil {
				a.logger.Info(fmt.Sprintf("Update available: v%s (current: v%s)", updateMsg.NewVersion, version))
				go a.applyUpdate(updateMsg)
			}

		default:
			a.logger.Warn(fmt.Sprintf("Unknown message type: %s", msg.Type))
		}
	}
}

// applyUpdate downloads and installs a newer binary from the master's distribution
// server, then re-executes the new binary (Linux/macOS) or triggers a helper script (Windows).
func (a *Agent) applyUpdate(msg comm.UpdateAvailableMessage) {
	exe, err := os.Executable()
	if err != nil {
		a.logger.Error(fmt.Sprintf("Self-update: cannot locate own binary: %v", err))
		return
	}

	downloadURL := fmt.Sprintf("http://%s:%d/binary", a.masterCfg.host, msg.HTTPPort)
	a.logger.Info(fmt.Sprintf("Self-update: downloading v%s from %s", msg.NewVersion, downloadURL))

	// Pass current args to the restarted process, minus the internal --daemon-child flag.
	restartArgs := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		if arg != "--daemon-child" {
			restartArgs = append(restartArgs, arg)
		}
	}

	if err := update.Apply(downloadURL, msg.SHA256, exe, restartArgs); err != nil {
		a.logger.Error(fmt.Sprintf("Self-update failed: %v", err))
		return
	}
	// On success, Apply never returns — it exec's the new binary or calls os.Exit.
}

// saveInstructions persists the received rules and connection info to instructions.conf.
func (a *Agent) saveInstructions(ttl int, rules []*config.TrafficRule) {
	instrConf := &config.InstructionsConf{
		ReceivedAt: time.Now(),
		TTL:        ttl,
		MasterHost: a.masterCfg.host,
		MasterPort: a.masterCfg.port,
		PSK:        a.masterCfg.psk,
		AgentID:    a.agentID,
		Rules:      rules,
	}
	if err := config.SaveInstructionsConf(config.InstructionsConfFile, instrConf); err != nil {
		a.logger.Warn(fmt.Sprintf("Could not save instructions.conf: %v", err))
	} else {
		a.logger.Info("Instructions saved to instructions.conf")
	}
}

// ─── Traffic execution ────────────────────────────────────────────────────────

func (a *Agent) startTraffic(rules []*comm.TrafficRule) {
	if atomic.LoadInt32(&a.isRunning) != 0 {
		go a.executeTraffic(rules)
	}
}

func (a *Agent) executeTraffic(rules []*comm.TrafficRule) {
	a.logger.Info(fmt.Sprintf("Starting traffic generation for %d rules", len(rules)))

	var wg sync.WaitGroup
	for _, rule := range rules {
		if rule.Role == "listen" {
			continue // listeners are handled by applyRules
		}
		wg.Add(1)
		go func(r *comm.TrafficRule) {
			defer wg.Done()
			a.executeSingleRule(r)
		}(rule)
	}

	wg.Wait()
	a.logger.Info(fmt.Sprintf("Traffic generation completed for %d rules", len(rules)))
}

func (a *Agent) executeSingleRule(rule *comm.TrafficRule) {
	address := net.JoinHostPort(rule.Target, strconv.Itoa(rule.Port))
	connCount := 0

	a.logger.Info(fmt.Sprintf("Starting rule: %s (%s → %s)", rule.Name, rule.Protocol, address))

	for {
		if atomic.LoadInt32(&a.isRunning) == 0 {
			return
		}

		var err error

		switch rule.Protocol {
		case "TCP":
			err = a.dialTCP(address, rule.Name)
		case "UDP":
			err = a.dialUDP(address, rule.Name)
		default:
			a.logger.Error(fmt.Sprintf("Unsupported protocol: %s", rule.Protocol))
			return
		}

		if err != nil {
			a.logger.Warn(fmt.Sprintf("Connection failed to %s (%s): %v", address, rule.Protocol, err))
		} else {
			connCount++
		}

		if rule.Count > 0 && connCount >= rule.Count {
			break
		}

		if rule.Interval > 0 {
			time.Sleep(time.Duration(rule.Interval) * time.Second)
		} else {
			time.Sleep(defaultConnectionDelay)
		}
	}

	a.logger.Info(fmt.Sprintf("Rule %s COMPLETED: %d connections generated", rule.Name, connCount))
}

// dialTCP dials address, sends a random payload, then closes the connection.
func (a *Agent) dialTCP(address, ruleName string) error {
	conn, err := net.DialTimeout("tcp", address, connectTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	payload := randomPayload(64)
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, werr := conn.Write(payload); werr != nil {
		a.logger.Warn(fmt.Sprintf("TCP write error to %s: %v", address, werr))
	}

	time.Sleep(tcpHoldDuration)
	a.logger.Info(fmt.Sprintf("TCP connection to %s (%s): %d bytes sent", address, ruleName, len(payload)))
	return nil
}

// dialUDP sends a random payload UDP datagram to address.
func (a *Agent) dialUDP(address, ruleName string) error {
	conn, err := net.DialTimeout("udp", address, connectTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	payload := randomPayload(64)
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, werr := conn.Write(payload); werr != nil {
		return fmt.Errorf("UDP send error: %w", werr)
	}

	a.logger.Info(fmt.Sprintf("UDP datagram to %s (%s): %d bytes sent", address, ruleName, len(payload)))
	return nil
}

// stopTraffic signals traffic goroutines to stop (future: implement per-rule cancel).
func (a *Agent) stopTraffic() {
	a.logger.Info("Stopping traffic generation...")
	// TODO: implement per-rule cancellation channels
}

// ─── Heartbeat ────────────────────────────────────────────────────────────────

func (a *Agent) sendHeartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if atomic.LoadInt32(&a.isRunning) == 0 {
				return
			}

			cpuUsage, memUsage := a.getSystemStats()

			a.mu.RLock()
			activeRules := len(a.currentRules)
			a.mu.RUnlock()

			if err := a.client.SendHeartbeat(version, cpuUsage, memUsage, activeRules); err != nil {
				a.logger.Warn(fmt.Sprintf("Failed to send heartbeat: %v", err))
			}

		case <-a.stopChan:
			return
		}
	}
}

func (a *Agent) getSystemStats() (float64, int64) {
	return 0.0, 0 // TODO: implement real CPU/memory stats
}

// ─── Standalone / TTL reconnect ───────────────────────────────────────────────

// ttlReconnectLoop waits for the TTL to expire then tries to reconnect to master.
// On successful reconnection the agent switches to connected mode.
func (a *Agent) ttlReconnectLoop(instrConf *config.InstructionsConf) {
	waitDuration := instrConf.ExpiresIn()
	if waitDuration > 0 {
		a.logger.Info(fmt.Sprintf("TTL reconnect scheduled in %s", waitDuration.Round(time.Second)))
		select {
		case <-time.After(waitDuration):
		case <-a.stopChan:
			return
		}
	}

	a.logger.Info("TTL expired — attempting to reconnect to master...")
	a.reconnectToMaster()
}

// reconnectToMaster attempts to establish a new master connection in a retry loop.
// On success the agent switches to connected mode and starts normal operation.
func (a *Agent) reconnectToMaster() {
	for attempt := 1; ; attempt++ {
		if atomic.LoadInt32(&a.isRunning) == 0 {
			return
		}

		a.logger.Info(fmt.Sprintf("Reconnect attempt %d to %s:%d...",
			attempt, a.masterCfg.host, a.masterCfg.port))

		client, err := comm.NewAgentClient(a.masterCfg.host, a.masterCfg.port, a.masterCfg.psk)
		if err != nil {
			a.logger.Warn(fmt.Sprintf("Reconnect attempt %d failed: %v — retrying in 30s", attempt, err))
			select {
			case <-time.After(30 * time.Second):
				continue
			case <-a.stopChan:
				return
			}
		}

		hostname, _ := os.Hostname()
		platform := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)

		if err := client.Register(a.agentID, hostname, platform, version); err != nil {
			client.Close()
			a.logger.Warn(fmt.Sprintf("Reconnect registration failed: %v — retrying in 30s", err))
			select {
			case <-time.After(30 * time.Second):
				continue
			case <-a.stopChan:
				return
			}
		}

		a.logger.Info("Reconnected to master — switching to connected mode")

		a.mu.Lock()
		a.client = client
		a.standalone = false
		a.mu.Unlock()

		go a.receiveMessages()
		go a.sendHeartbeatLoop()
		return
	}
}

// ─── Non-root warning ─────────────────────────────────────────────────────────

// checkNonRootAndWarn prints a warning if the process is not running as root
// on Linux/macOS, where ports ≤ 1024 require elevated privileges.
// The warning is also sent to the master if a client connection is available.
func checkNonRootAndWarn(agentID string, client *comm.AgentClient, logger *logging.Logger) {
	// On Windows os.Getuid() returns -1; skip the check.
	if runtime.GOOS == "windows" {
		return
	}
	if os.Getuid() == 0 {
		return // running as root — all ports accessible
	}

	msg := fmt.Sprintf(
		"Agent %s is running as non-root (uid=%d). "+
			"Only ports > 1024 can be configured — attempts to open ports 1–1023 will fail.",
		agentID, os.Getuid(),
	)

	fmt.Fprintf(os.Stderr, "WARNING: %s\n", msg)
	logger.Warn(msg)

	if client != nil {
		if err := client.SendWarning(agentID, "NON_ROOT", msg); err != nil {
			logger.Warn(fmt.Sprintf("Could not send non-root warning to master: %v", err))
		}
	}
}

// ─── Shutdown ─────────────────────────────────────────────────────────────────

// Stop gracefully shuts down the agent.
func (a *Agent) Stop() error {
	a.logger.Info("Shutting down Agent...")
	atomic.StoreInt32(&a.isRunning, 0)

	a.listenerMgr.StopAll()

	if a.client != nil {
		return a.client.Close()
	}

	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// randomPayload returns a slice of n pseudo-random printable ASCII bytes.
// Defined here in addition to traffic/generator.go so the agent can generate
// payloads without importing the traffic package.
func randomPayload(n int) []byte {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	r := time.Now().UnixNano()
	for i := range b {
		r = r*6364136223846793005 + 1442695040888963407 // LCG
		b[i] = charset[((r>>33)^r)%int64(len(charset))]
	}
	return b
}

// updateRules replaces the agent's rule set (used by connected-mode receiveMessages).
func (a *Agent) updateRules(rules []*comm.TrafficRule) {
	cfgRules := commRulesToConfig(rules)
	a.applyRules(cfgRules)
}
