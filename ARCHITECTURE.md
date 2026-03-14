# Traffic Orchestrator — Technical Architecture

**Version:** 0.4.5
**Author:** Claudia (Lead Architect)
**Date:** 2026-03-13
**Status:** ✅ Updated — v0.4.5: daemon mode, binary distribution, agent registry, self-update incorporated

---

## Executive Summary

Traffic Orchestrator (TO) is a lightweight, single-binary system for orchestrating network traffic flows in demo/lab environments (up to 100 hosts). It operates on a Master-Agent architecture where:

- **Master** reads traffic configuration and orchestrates agents
- **Agents** execute traffic generation tasks (TCP/UDP connections)
- **Communication** happens over a PSK-secured control channel

**Key Design Principles:**
- Single binary for both Master and Agent roles ✅
- No dependencies, no installation process ✅
- Flat-file configuration (human-readable) ✅
- Security by default (PSK, input validation, TLS) ✅

---

## 1. System Architecture

### 1.1 High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                        MASTER NODE                          │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Traffic Orchestrator Binary (--master mode)         │  │
│  ├──────────────────────────────────────────────────────┤  │
│  │  • Config Parser (traffic_list.conf)                 │  │
│  │  • Agent Registry (track connected agents)           │  │
│  │  • Command Dispatcher (send instructions to agents)  │  │
│  │  • File Watcher (monitor config changes)             │  │
│  └──────────────────────────────────────────────────────┘  │
│                            ↓                                │
│                   Control Channel (PSK-secured)             │
│                            ↓                                │
└─────────────────────────────────────────────────────────────┘
                             ↓
        ┌────────────────────┼────────────────────┐
        ↓                    ↓                    ↓
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  AGENT 1      │   │  AGENT 2      │   │  AGENT 3      │
│  (192.168.1.1)│   │  (192.168.1.2)│   │  (192.168.1.3)│
├───────────────┤   ├───────────────┤   ├───────────────┤
│ • Listener    │   │ • Listener    │   │ • Listener    │
│ • Connector   │   │ • Connector   │   │ • Connector   │
│ • Reporter    │   │ • Reporter    │   │ • Reporter    │
└───────────────┘   └───────────────┘   └───────────────┘
        ↓                    ↓                    ↓
     Data Plane (Direct TCP/UDP connections between agents)
```

### 1.2 Communication Flow Example

**Scenario:** Master wants Agent A to connect to Agent B via TCP port 443

```
1. Master → Agent B: "LISTEN tcp://0.0.0.0:443 timeout=30s"
2. Agent B → Master: "ACK: Listening on tcp://0.0.0.0:443"
3. Master → Agent A: "CONNECT tcp://192.168.1.2:443 count=5 interval=3s"
4. Agent A → Master: "ACK: Starting connections"
5. Agent A ↔ Agent B: [Data Plane] 5x TCP Handshake + Teardown
6. Agent A → Master: "DONE: 5/5 successful"
7. Master → Agent B: "STOP tcp://0.0.0.0:443"
```

---

## 2. Tech Stack Decisions

### 2.1 Language: **Go**

**Rationale:**
- ✅ Single binary compilation (no runtime dependencies)
- ✅ Excellent networking libraries (`net`, `tcp`, `udp`)
- ✅ Cross-platform support (Linux, Windows)
- ✅ Built-in concurrency (goroutines for handling multiple agents)
- ✅ Strong standard library for crypto/TLS

**Alternative Considered:** Rust (rejected due to steeper learning curve for maintainability)

### 2.2 Key Go Packages

| Package | Purpose |
|---------|---------|
| `net` | TCP/UDP networking (both control channel and data plane) |
| `crypto/tls` | Secure control channel (TLS 1.3 with PSK) |
| `encoding/json` | Command/response serialization (Master ↔ Agent) |
| `bufio` | Line-by-line config file parsing |
| `fsnotify/fsnotify` | File watcher for config hot-reload |
| `spf13/cobra` | CLI argument parsing (--master, --port, --psk) |
| `rs/zerolog` | Structured logging |
| `sync` | Thread-safe state management (RWMutex, atomic) |

### 2.3 Configuration Format Decision

**Config File Format:** ✅ **FLAT FILE** (as per original spec)

**Rationale:**
- ✅ Keeps spec simplicity (no parser complexity)
- ✅ Easy manual editing (vi/nano friendly)
- ✅ Familiar to operators (INI-like syntax)
- ✅ No external dependencies

**Parser Strategy:**
```go
// Line-by-line parsing
// 1. Parse target definitions: TARGET1=192.168.1.100
// 2. Skip comment lines (starting with #)
// 3. Parse flow lines: TCP  TARGET1  445  5  loop
```

**Example Config (from spec):**
```
Target1=192.168.1.100
Target2=192.168.1.102
Target3=192.168.1.103

# --- Fileserver (TARGET1) ---
TCP     TARGET1     445     5       loop    # SMB
TCP     TARGET1     139     10      loop    # NetBIOS

# --- Webserver (TARGET2) ---
TCP     TARGET2     80      3       loop    # HTTP
TCP     TARGET2     443     4       loop    # HTTPS

# --- DNS (TARGET3) ---
UDP     TARGET3     53      2       loop    # DNS
```

**Validation Rules:**
- Target names must match regex: `^TARGET\d+$` or valid IPv4/IPv6
- Protocol must be `TCP` or `UDP`
- Port must be 1-65535
- Interval must be positive integer (seconds)
- Count must be positive integer or literal `loop`

---

## 3. Data Models

### 3.1 Core Types (Go Structs)

```go
// Config represents the traffic configuration file
type Config struct {
    Targets map[string]string  // TARGET1 -> IP mapping
    Flows   []Flow
}

// Flow represents a single traffic generation task
type Flow struct {
    ID       string        // Auto-generated (flow-001, flow-002, ...)
    Protocol string        // "TCP" or "UDP"
    Target   string        // TARGET1 or direct IP
    Port     int           // 1-65535
    Interval time.Duration // Seconds between connections
    Count    int           // -1 for loop, else positive integer
}

// Agent represents a connected agent node
type Agent struct {
    ID         string    // Unique ID (hostname or IP)
    Address    string    // IP:Port of agent
    LastSeen   time.Time // For health checks
    Status     string    // "connected", "busy", "offline"
}

// Command is sent from Master to Agent
type Command struct {
    Type    string            `json:"type"`    // "LISTEN", "CONNECT", "STOP"
    Payload map[string]string `json:"payload"` // Protocol, Port, Target, etc.
}

// Response is sent from Agent to Master
type Response struct {
    Status  string `json:"status"`  // "ACK", "ERROR", "DONE"
    Message string `json:"message"`
}
```

### 3.2 State Management

**Master State:**
- Registry of connected agents (`map[string]*Agent`)
- Active flows (`map[string]*ActiveFlow`)
- Config watcher goroutine

**Agent State:**
- Active listeners (`map[int]*Listener`) — Port → Listener
- Active connections (`[]*Connection`)
- Connection to Master (persistent TCP/TLS)

---

## 4. Security Architecture

### 4.1 Authentication & Encryption

**Control Channel Security:**
- **TLS 1.3** with PSK (Pre-Shared Key)
- PSK is passed via `--psk` flag (validated length ≥32 chars)
- No hardcoded keys — must be provided at runtime

**Implementation:**
```go
// Master side
tlsConfig := &tls.Config{
    MinVersion: tls.VersionTLS13,
    CipherSuites: []uint16{
        tls.TLS_AES_256_GCM_SHA384,
    },
    GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
        return validatePSK(chi, psk), nil
    },
}
```

**Agent Authentication:**
- Agents must provide valid PSK on first connection
- Master validates PSK before registering agent
- Invalid PSK → connection closed immediately

### 4.2 Input Validation

**All external inputs are validated:**

| Input Source | Validation Rules |
|--------------|-----------------|
| Config File | Schema validation (YAML structure), Port range (1-65535), Target IPs (valid IPv4/IPv6) |
| CLI Args | `--master` must be valid hostname/IP, `--port` must be 1-65535, `--psk` minimum length 32 chars |
| Network Commands | JSON schema validation, Protocol must be "TCP" or "UDP", No path traversal in payload |

**Example Validation:**
```go
func validatePort(port int) error {
    if port < 1 || port > 65535 {
        return fmt.Errorf("invalid port %d: must be 1-65535", port)
    }
    return nil
}

func validateProtocol(proto string) error {
    if proto != "TCP" && proto != "UDP" {
        return fmt.Errorf("invalid protocol %s: must be TCP or UDP", proto)
    }
    return nil
}
```

### 4.3 No Hardcoded Secrets

**PSK Distribution Strategy:**
- PSK is **never** stored in code or config files
- Must be passed via CLI flag: `--psk <key>`
- Example: `./trafficorch --master 192.168.1.1 --port 9000 --psk $(cat psk.secret)`

**🤔 OPEN QUESTION FOR REVIEW:**
- Should we support PSK from environment variable (`TO_PSK`) as fallback?
- Kathy: Security implications?

### 4.4 Safe File Operations

**Config File Handling:**
- Canonicalize paths (resolve symlinks, remove `..`)
- Reject any path containing `..` (no traversal)
- Only read from designated config directory

```go
func loadConfig(path string) (*Config, error) {
    // Canonicalize path
    absPath, err := filepath.Abs(path)
    if err != nil {
        return nil, fmt.Errorf("invalid config path: %w", err)
    }
    
    // Reject traversal
    if strings.Contains(absPath, "..") {
        return nil, fmt.Errorf("path traversal detected: %s", path)
    }
    
    // Read and parse
    data, err := os.ReadFile(absPath)
    if err != nil {
        return nil, fmt.Errorf("failed to read config: %w", err)
    }
    
    // Validate YAML structure
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("invalid YAML: %w", err)
    }
    
    return &cfg, nil
}
```

---

## 5. Error Handling Strategy

### 5.1 Typed Errors

**Go Error Wrapping:**
```go
var (
    ErrInvalidConfig   = errors.New("invalid configuration")
    ErrAgentOffline    = errors.New("agent is offline")
    ErrConnectionFailed = errors.New("connection failed")
    ErrTimeout         = errors.New("operation timeout")
)

// Example usage
if err := validateConfig(cfg); err != nil {
    return fmt.Errorf("%w: missing required field 'targets'", ErrInvalidConfig)
}
```

### 5.2 User-Facing Error Messages

**Actionable Errors:**
- ❌ Bad: `"error: nil pointer dereference"`
- ✅ Good: `"Failed to start listener on port 443: port already in use. Please choose a different port or stop the conflicting service."`

**Implementation:**
```go
func startListener(port int) error {
    ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
    if err != nil {
        if strings.Contains(err.Error(), "bind: address already in use") {
            return fmt.Errorf("port %d is already in use — stop the conflicting service or use a different port", port)
        }
        return fmt.Errorf("failed to start listener on port %d: %w", port, err)
    }
    // ...
}
```

### 5.3 Fail-Fast on Invalid Config

**Startup Validation:**
- Config is validated **before** starting Master or Agent
- Invalid config → log clear error + exit with code 1
- **Never** silently fall back to defaults

```go
func main() {
    cfg, err := loadConfig(*configPath)
    if err != nil {
        log.Fatal().Err(err).Msg("Invalid configuration — cannot start")
        os.Exit(1)
    }
    
    if err := validateConfig(cfg); err != nil {
        log.Fatal().Err(err).Msg("Configuration validation failed")
        os.Exit(1)
    }
    
    // Proceed only if config is valid
    // ...
}
```

---

## 6. Master Logic

### 6.1 Startup Sequence

```
1. Parse CLI args (--master, --port, --psk)
2. Load traffic config (traffic_list.yaml)
3. Validate config (targets, flows, ports)
4. Start control channel listener (TLS with PSK)
5. Start file watcher for config hot-reload
6. Wait for agents to register
7. Begin orchestrating traffic flows
```

### 6.2 Agent Registration

**Flow:**
```
Agent → Master: HELLO {id: "agent-01", version: "1.0"}
Master: Validate PSK
Master → Agent: WELCOME {registered: true}
Master: Add agent to registry
```

**Health Checks:**
- Master sends periodic `PING` to all agents (every 30s)
- Agent must respond with `PONG` within 5s
- No response → mark agent as offline (remove from active flows)

### 6.3 Flow Orchestration

**For each flow in config:**
1. Resolve target (TARGET1 → 192.168.1.100)
2. Find available agent for target IP (or any agent if random traffic)
3. Send `LISTEN` command to target agent
4. Wait for `ACK` from target agent
5. Send `CONNECT` command to source agent(s)
6. Monitor completion (`DONE` responses)
7. Send `STOP` command to target agent

**Concurrency:**
- Each flow runs in a separate goroutine
- Synchronization via channels and mutexes

### 6.4 Config Hot-Reload

**Implementation:**
```go
watcher, _ := fsnotify.NewWatcher()
watcher.Add(*configPath)

go func() {
    for {
        select {
        case event := <-watcher.Events:
            if event.Op&fsnotify.Write == fsnotify.Write {
                log.Info().Msg("Config file changed — reloading...")
                newCfg, err := loadConfig(*configPath)
                if err != nil {
                    log.Error().Err(err).Msg("Failed to reload config")
                    continue
                }
                applyNewConfig(newCfg)
            }
        }
    }
}()
```

**Config Change Strategy:**
- Stop old flows gracefully (finish ongoing connections)
- Start new flows from updated config
- Do **not** restart the binary

---

## 7. Agent Logic

### 7.1 Startup Sequence

```
1. Determine configuration source (priority order):
   a. CLI flags given (--agent --master … --port … --psk …)
      → parse flags → save to agent.conf → attempt connected mode
   b. No flags / --agent with no flags
      → look for agent.conf in working directory
        → found:     load and attempt connected mode (step 2)
        → not found: look for instructions.conf (standalone mode, step 3)
        → neither:   print help, exit 0

2. Connected mode (requires live master):
   a. Validate PSK strength
   b. Dial TCP to master; if unreachable → fall back to standalone (step 3)
   c. Send REGISTER message (includes self-reported IP)
   d. Receive REGISTER_ACK
   e. Check non-root privilege (see §7.4); send WARNING to master if non-root
   f. Enter message loop:
      - Receive CONFIG_UPDATE → save rules to instructions.conf → apply rules
      - Receive TRAFFIC_START → begin generating traffic
   g. Send HEARTBEAT every 30 s
   h. On SIGINT/SIGTERM → stop listeners, cancel goroutines, exit

3. Standalone mode (no live master required):
   a. Load instructions.conf; verify it exists and is not expired (TTL check)
   b. Check non-root privilege; log warning locally
   c. Apply cached rules (start "listen" listeners, launch "connect" goroutines)
   d. Launch TTL reconnect goroutine (see §7.3)
   e. On SIGINT/SIGTERM → stop listeners, cancel goroutines, exit
```

**agent.conf** (introduced in v0.2.0) is a simple KEY=VALUE file written
atomically (temp file + rename) to the working directory whenever an agent
is started with explicit CLI flags.  All four keys are supported:
`MASTER`, `PORT`, `PSK`, `ID`.

**instructions.conf** (introduced in v0.3.0) is a JSON file written whenever
the agent receives a `CONFIG_UPDATE` from the master.  It stores the full
rule set, the master connection parameters, a `received_at` timestamp, and the
TTL (time-to-live in seconds, 0 = never expires).  Permissions are set to
0600.  Atomic write (temp file + rename) prevents partial reads.

### 7.2 Rule Roles — connect vs listen

Starting with v0.3.0 each traffic rule carries a `role` field:

| Role | Behaviour |
|------|-----------|
| `connect` | Agent dials the target host:port and sends a random 64-byte payload |
| `listen` | Agent opens a TCP/UDP listener on the specified port and accepts incoming connections/datagrams |

**Rule distribution by the master (extended config format):**
- Source agent IP matches rule `SOURCE` → rule sent with `role=connect`
- Dest agent IP matches rule `DEST` → rule sent with `role=listen`
- Simple format (no SOURCE field) → all agents receive `role=connect`

### 7.3 TTL Reconnect Loop

When an agent runs in standalone mode with a finite TTL, a background
goroutine waits for `TTL` seconds and then attempts to reconnect to the
master.  The retry loop uses a 30 s back-off between attempts.  On success
the agent switches back to connected mode and rewrites `instructions.conf`
with the fresh rules received from the master.

```
standalone start
      │
      ▼
apply cached rules
      │
      └─► [TTL goroutine]
               │
               ▼  (wait TTL seconds)
          dial master
               │
          ┌────┴─────┐
          │ success  │ failure (retry every 30 s)
          ▼          └──────────────────────────┐
     receive CONFIG_UPDATE                      │
     save instructions.conf              keep retrying
     apply new rules                            │
     (now in connected mode)   ◄────────────────┘
```

### 7.4 Non-root Warning

On Linux and macOS the agent checks `os.Getuid()` at startup:
- If non-root: a warning is printed to stderr AND logged.
- In connected mode: a `WARNING` message is also sent to the master's log.
- Impact: port binding will fail for ports ≤ 1024; configure only ports > 1024
  for non-root agents.

### 7.2 Command Execution

**LISTEN Command:**
```go
func handleListenCommand(cmd Command) error {
    port := cmd.Payload["port"]
    protocol := cmd.Payload["protocol"]
    
    ln, err := net.Listen(protocol, fmt.Sprintf(":%s", port))
    if err != nil {
        return err
    }
    
    // Store listener (so we can stop it later)
    activeListeners[port] = ln
    
    // Accept connections in background
    go func() {
        for {
            conn, err := ln.Accept()
            if err != nil {
                break  // Listener closed
            }
            conn.Close()  // Immediately tear down (per spec)
        }
    }()
    
    return nil
}
```

**CONNECT Command:**
```go
func handleConnectCommand(cmd Command) error {
    target := cmd.Payload["target"]
    port := cmd.Payload["port"]
    count := parseCount(cmd.Payload["count"])  // "loop" → -1, else integer
    interval := parseDuration(cmd.Payload["interval"])
    
    successCount := 0
    for i := 0; count == -1 || i < count; i++ {
        conn, err := net.Dial("tcp", fmt.Sprintf("%s:%s", target, port))
        if err != nil {
            log.Error().Err(err).Msg("Connection failed")
            continue
        }
        conn.Close()  // Tear down immediately
        successCount++
        
        time.Sleep(interval)
    }
    
    return reportDone(successCount, count)
}
```

**STOP Command:**
```go
func handleStopCommand(cmd Command) error {
    port := cmd.Payload["port"]
    
    ln, exists := activeListeners[port]
    if !exists {
        return fmt.Errorf("no listener on port %s", port)
    }
    
    ln.Close()
    delete(activeListeners, port)
    
    return nil
}
```

---

## 8. Network Protocol Design

### 8.1 Control Channel Message Format

**JSON over TLS:**
```json
{
  "type": "COMMAND",
  "id": "cmd-12345",
  "payload": {
    "action": "LISTEN",
    "protocol": "TCP",
    "port": "443",
    "timeout": "30s"
  }
}
```

**Response:**
```json
{
  "type": "RESPONSE",
  "id": "cmd-12345",
  "status": "ACK",
  "message": "Listening on TCP port 443"
}
```

### 8.2 Timeout Handling

**Master-side:**
- Wait max 10s for agent `ACK` on commands
- Timeout → log warning, mark command as failed

**Agent-side:**
- `LISTEN` commands have optional timeout (default: infinite)
- After timeout, automatically close listener

---

## 9. Testing Strategy

### 9.1 Unit Tests

**Coverage:**
- Config parsing and validation
- Command serialization/deserialization
- Error handling (invalid inputs, network failures)

**Example Test:**
```go
func TestValidateConfig(t *testing.T) {
    tests := []struct {
        name    string
        config  Config
        wantErr bool
    }{
        {
            name: "valid config",
            config: Config{
                Targets: map[string]string{"TARGET1": "192.168.1.1"},
                Flows: []Flow{{Protocol: "TCP", Port: 443}},
            },
            wantErr: false,
        },
        {
            name: "invalid port",
            config: Config{
                Flows: []Flow{{Protocol: "TCP", Port: 99999}},
            },
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateConfig(&tt.config)
            if (err != nil) != tt.wantErr {
                t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### 9.2 Integration Tests

**Scenarios:**
- Master-Agent handshake with valid/invalid PSK
- LISTEN → CONNECT → STOP flow
- Config hot-reload without restart
- Agent health check (PING/PONG)

**Mock Network:**
- Use Go's `net.Pipe()` for in-memory TCP connections
- Mock file system for config watcher tests

---

## 10. Deployment & Operations

### 10.1 Binary Structure

**Single Binary with Dual Modes:**
```bash
# Master mode
./trafficorch --mode=master --config=traffic_list.yaml --port=9000 --psk=<secret>

# Agent mode (auto-detected if no --mode=master)
./trafficorch --master=192.168.1.1 --port=9000 --psk=<secret>
```

### 10.2 Logging

**Structured Logging with `zerolog`:**
```go
log.Info().
    Str("agent_id", "agent-01").
    Str("flow", "SMB to Fileserver").
    Msg("Flow orchestration started")

log.Error().
    Err(err).
    Int("port", 443).
    Msg("Failed to start listener")
```

**Log Levels:**
- `DEBUG`: Low-level protocol details (command payloads)
- `INFO`: Flow start/stop, agent registration
- `WARN`: Retries, timeouts
- `ERROR`: Failed connections, config errors

### 10.3 Service Deployment (Linux)

**Systemd Unit Example:**
```ini
[Unit]
Description=Traffic Orchestrator Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/trafficorch --master=192.168.1.1 --port=9000 --psk-file=/etc/trafficorch/psk.secret
Restart=always

[Install]
WantedBy=multi-user.target
```

---

## 11. Open Questions & TODOs

### 🤔 Questions — RESOLVED ✅

1. **Config Format:** ✅ **FLAT FILE** (as per spec)
   - Decision: Keep original flat-file format
   - No migration to YAML

2. **PSK Distribution:** ✅ **BOTH** (CLI + env var)
   - CLI flag: `--psk <key>`
   - Env var fallback: `TO_PSK`

3. **Data Plane Encryption:** ✅ **OPTIONAL** (not required, but doesn't hurt)
   - Agent-to-Agent traffic can be plaintext
   - Optional: Add TLS support for data plane in future version

4. **Target Resolution:** ✅ **TARGET1 is a specific agent**
   - `TARGET1=192.168.1.100` means "the agent running on 192.168.1.100"
   - Master must ensure that specific agent is online before orchestrating flow

5. **Loop Termination:** 🚧 **TO BE DESIGNED** (see below)

---

### 🔄 Loop Termination Strategy (Detailed Design)

**Problem:** Flows with `count: loop` run indefinitely — how to stop them gracefully?

**Solution: Multi-Level Shutdown Strategy**

#### Level 1: SIGTERM Handler (Graceful Binary Shutdown)
```go
// Catch SIGTERM/SIGINT
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

go func() {
    <-sigChan
    log.Info().Msg("Shutdown signal received — stopping all flows...")
    
    // Master: Send STOP_ALL to all agents
    // Agent: Stop all active loops + listeners
    gracefulShutdown()
    
    os.Exit(0)
}()
```

**Use Case:** Admin runs `systemctl stop trafficorch` or `Ctrl+C`

---

#### Level 2: Master Command (Selective Flow Control)

**New Commands:**

| Command | Description |
|---------|-------------|
| `STOP_FLOW <flow_id>` | Stop a specific flow by ID |
| `STOP_ALL` | Stop all active flows on an agent |
| `PAUSE_FLOW <flow_id>` | Pause a flow (can be resumed later) |
| `RESUME_FLOW <flow_id>` | Resume a paused flow |

**Implementation:**
```go
// Agent side
type FlowController struct {
    flows map[string]*ActiveFlow  // flow_id → flow state
    mu    sync.RWMutex
}

func (fc *FlowController) StopFlow(flowID string) error {
    fc.mu.Lock()
    defer fc.mu.Unlock()
    
    flow, exists := fc.flows[flowID]
    if !exists {
        return fmt.Errorf("flow %s not found", flowID)
    }
    
    // Signal goroutine to stop
    close(flow.stopChan)
    
    // Wait for goroutine to finish (max 5s)
    select {
    case <-flow.doneChan:
        log.Info().Str("flow_id", flowID).Msg("Flow stopped gracefully")
    case <-time.After(5 * time.Second):
        log.Warn().Str("flow_id", flowID).Msg("Flow did not stop in time — force kill")
    }
    
    delete(fc.flows, flowID)
    return nil
}
```

**Use Case:** Master wants to stop a single flow without restarting everything

---

#### Level 3: Config Hot-Reload (Remove Flow from Config)

**Behavior:**
- Admin removes a flow from `traffic_list.conf`
- File watcher detects change
- Master sends `STOP_FLOW` to affected agents
- Flow terminates gracefully

**Implementation:**
```go
func applyConfigChange(oldCfg, newCfg *Config) {
    // Find removed flows
    for _, oldFlow := range oldCfg.Flows {
        if !existsInConfig(oldFlow, newCfg) {
            log.Info().Str("flow", oldFlow.Name).Msg("Flow removed from config — stopping...")
            stopFlow(oldFlow.ID)
        }
    }
    
    // Add new flows
    for _, newFlow := range newCfg.Flows {
        if !existsInConfig(newFlow, oldCfg) {
            log.Info().Str("flow", newFlow.Name).Msg("New flow detected — starting...")
            startFlow(newFlow)
        }
    }
}
```

**Use Case:** Operator edits config file, flow stops automatically (no manual intervention)

---

#### Level 4: Master CLI Interface (Optional, Future)

**Interactive Commands:**
```bash
# Start master in interactive mode
$ ./trafficorch --mode=master --config=traffic_list.conf --interactive

> list flows
ID        Name                  Status    Source      Target      Count
flow-001  SMB to Fileserver     running   agent-02    TARGET1     loop
flow-002  DNS Queries           running   agent-03    TARGET3     loop

> stop flow-001
Stopping flow-001... Done.

> stop all
Stopping all flows... Done.
```

**Implementation:** Simple REPL with `bufio.Scanner`

**Use Case:** Real-time control during demos/debugging

---

### ✅ Recommended Implementation Order:

**Phase 1 (MVP):**
- ✅ SIGTERM handler (graceful shutdown)
- ✅ `STOP_ALL` command from Master

**Phase 2:**
- ✅ Config hot-reload → auto-stop removed flows
- ✅ `STOP_FLOW <id>` for selective control

**Phase 3 (Future):**
- ✅ `PAUSE_FLOW` / `RESUME_FLOW`
- ✅ Interactive CLI

---

### 🔐 Security Consideration:

**Question:** Should STOP commands require authentication?

**Answer:** YES — all Master→Agent commands use the same TLS+PSK channel, so authentication is implicit. No additional auth needed.

---

### 📊 Flow State Machine

```
┌─────────┐    START     ┌─────────┐    PAUSE     ┌────────┐
│ STOPPED │ ──────────> │ RUNNING │ ──────────> │ PAUSED │
└─────────┘              └─────────┘              └────────┘
     ↑                        │                        │
     │         STOP           │         RESUME         │
     └────────────────────────┴────────────────────────┘
```

**States:**
- `STOPPED`: Flow is not active
- `RUNNING`: Flow is generating traffic
- `PAUSED`: Flow is idle, can be resumed

---

### 🎯 Final Recommendation:

**For MVP (v1.0):**
1. **SIGTERM handler** — Catches Ctrl+C and `systemctl stop`
2. **STOP_ALL command** — Master can stop all flows on an agent
3. **Config hot-reload** — Removing a flow from config stops it automatically

**This covers 90% of use cases!** 🚀

Other shutdown methods (PAUSE/RESUME, interactive CLI) can be added in v1.1+.

---

## 12. Next Steps

### Before Coding:

1. ✅ **Review this document** with Kathy (QA perspective)
2. ✅ **Resolve open questions** with Volker
3. ✅ **Finalize tech stack** (confirm Go, package choices)
4. ✅ **Define MVP scope** (which features for v1.0?)

### Implementation Order:

**Phase 1: Core Infrastructure**
- [ ] CLI argument parsing (`cobra`)
- [ ] Config parsing and validation
- [ ] TLS control channel (PSK-based)
- [ ] Basic Master-Agent communication

**Phase 2: Traffic Orchestration**
- [ ] Master: Flow orchestration logic
- [ ] Agent: LISTEN/CONNECT/STOP command handlers
- [ ] Agent registry and health checks

**Phase 3: Advanced Features**
- [ ] Config hot-reload (fsnotify)
- [ ] Structured logging (zerolog)
- [ ] Graceful shutdown (SIGTERM)

**Phase 4: Testing & Deployment**
- [ ] Unit tests (80%+ coverage)
- [ ] Integration tests (E2E scenarios)
- [ ] README and documentation
- [ ] Binary packaging (Linux, Windows)

---

## 13. Conclusion

This architecture provides a **secure, scalable, and maintainable** foundation for the Traffic Orchestrator project. Key highlights:

✅ **Single binary** with dual-mode operation (Master/Agent)  
✅ **PSK-secured TLS** for control channel  
✅ **Flat-file config** with hot-reload support  
✅ **Robust error handling** and input validation  
✅ **Clear separation** of control plane (Master ↔ Agent) and data plane (Agent ↔ Agent)  

**Ready for review by Kathy and Volker!** 🚀

---

---

## 14. Profile System (v0.4.0)

### 14.1 Overview

The profile system replaces the per-host repetition of traffic rules with
reusable **role-based profiles** (`.profile` files).  Instead of writing
individual rules for every host, an operator:

1. Defines profiles for each host role (e.g. `domain_controller`, `windows_client`).
2. Tags target hosts in the config (`#tag:dc`, `#tag:client`).
3. Assigns profiles to hosts in the `[ASSIGNMENTS]` section.

The master resolves profiles to concrete rules at agent-registration time,
substituting `SELF` and `group:<tag>` placeholders with real IP addresses.

### 14.2 Profile File Format

Profile files use the `.profile` extension and live in the directory configured
by `PROFILE_DIR`.  Each file has two INI sections:

```ini
[META]
NAME        = domain_controller
DESCRIPTION = Windows AD Domain Controller
VERSION     = 1.0
EXTENDS     = base_windows          # optional single inheritance
TAGS        = windows, active-directory

[RULES]
# PROTOCOL  ROLE     SRC   DST           PORT  INTERVAL  COUNT  #name
TCP          connect  SELF  group:dc      389   15        3      #ldap-replication
TCP          listen   SELF  -             389   -         -      #ldap-listener
UDP          connect  SELF  ANY           53    15        2      #dns-query
```

**Rule columns:**

| Column | Values | Notes |
|--------|--------|-------|
| `PROTOCOL` | `TCP` / `UDP` | |
| `ROLE` | `connect` / `listen` | `connect` = dial out; `listen` = open port |
| `SRC` | `SELF`, IP, target name | `SELF` = this agent's own IP |
| `DST` | `SELF`, IP, target, `group:<tag>`, `ANY`, `-` | `-` = unused (listen rules) |
| `PORT` | 1–65535 | |
| `INTERVAL` | seconds or `-` | `-` = 0 (immediate) |
| `COUNT` | number, `loop`, or `-` | `-` or `loop` = repeat forever |
| `#name` | optional label | trailing inline comment |

Whitespace between columns is not significant — one space or ten spaces, the
parser treats them identically.

### 14.3 Master Config with Profiles

```ini
[MASTER]
PORT        = 9000
PSK         = ChangeMe2026!
TTL         = 300
PROFILE_DIR = ./profiles        # directory containing *.profile files

[TARGETS]
DC1     = 10.0.0.1   #tag:dc
DC2     = 10.0.0.2   #tag:dc
CLIENT1 = 10.0.0.10  #tag:client
WEB1    = 10.0.0.20  #tag:web

[ASSIGNMENTS]
DC1     = domain_controller
DC2     = domain_controller
CLIENT1 = windows_client
WEB1    = web_server
```

**How tags work:** `#tag:dc` annotations on target lines cause the master to
build a `tagMap` (`dc → [10.0.0.1, 10.0.0.2]`).  Profile rules that reference
`group:dc` are expanded to one concrete rule per IP in that tag group.

### 14.4 Rule Resolution Algorithm

When an agent with IP `10.0.0.10` registers and is assigned profile
`windows_client`:

```
1. Flatten profile chain: windows_client → base_windows (EXTENDS) + own rules
2. For each ProfileRule:
   a. ROLE = "listen"  → emit TrafficRule{Role:listen, Port:…}
   b. ROLE = "connect" →
      - Resolve SRC: SELF → 10.0.0.10
      - Skip if resolved SRC ≠ agent IP (rule belongs to another host)
      - Resolve DST:
          group:dc  → [10.0.0.1, 10.0.0.2]
          ANY       → all IPs in TargetMap
          SELF      → 10.0.0.10
          <name>    → TargetMap lookup
          <bare IP> → as-is
      - Emit one TrafficRule per resolved destination
```

### 14.5 Backward Compatibility

The profile system is **fully additive**.  Existing configs with direct traffic
rules (`[RULES]` sections) continue to work unchanged.  Profile-based and
direct rules are applied in order — a config can use both simultaneously.

If `PROFILE_DIR` is not set, or no `[ASSIGNMENTS]` are defined, the master
falls back to the v0.3.0 direct-rule distribution logic.

---


## 15. Deployment & Maintenance (v0.4.5)

### 15.1 Daemon Mode

Both master and agent can be started as a background process with `-d` / `--daemon`:

```bash
# Start master as daemon
./trafficorch -d --master --config to.conf

# Start agent as daemon
./trafficorch -d --agent --master 10.0.0.1 --port 9000 --psk mysecretkey
```

The parent process prints the child PID and exits immediately.
A PID file (`trafficorch.pid`) is written to the working directory.

**Implementation:**
- Non-Windows: `exec.Command + SysProcAttr{Setsid:true}` — fully detached from terminal
- Windows: `CreationFlags: DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`

### 15.2 Binary Distribution Server (Port 9001)

The master automatically starts an HTTP server on **port 9001** that serves:

| Endpoint | Description |
|----------|-------------|
| `GET /binary`  | The master binary as `application/octet-stream` |
| `GET /sha256`  | Hex SHA-256 checksum of the binary |
| `GET /version` | Current master version string |
| `GET /agents`  | Agent registry as JSON array |

**Bootstrap new agent without prior installation:**
```bash
curl -O http://<master-ip>:9001/binary
chmod +x binary
./binary --agent --master <master-ip> --port 9000 --psk <key>
```

No PSK is required for the binary download — the binary itself is not a secret.
Authentication happens on the control channel (port 9000) via PSK.

### 15.3 Agent Registry (`agents.json`)

The master maintains a persistent JSON registry of all agents:

```json
[
  {
    "id": "agent-dc01",
    "hostname": "DC01",
    "ip": "10.0.0.10",
    "version": "0.4.5",
    "platform": "linux/amd64",
    "first_seen": "2026-03-14T10:00:00Z",
    "last_seen": "2026-03-14T11:23:45Z",
    "status": "online"
  }
]
```

View the registry on the master node:
```bash
./trafficorch --status
```

Or fetch remotely:
```bash
curl http://<master-ip>:9001/agents | jq .
```

### 15.4 Automatic Self-Update

When an agent connects with a version **older** than the master, the master:

1. Sends an `UPDATE_AVAILABLE` message over the control channel (port 9000)
2. The message includes: `new_version`, `http_port`, `sha256`

The agent then:
1. Downloads the binary from `http://<master-host>:<http_port>/binary`
2. Verifies the SHA-256 checksum against the value from the signed control message
3. Replaces the current binary and restarts:
   - **Linux/macOS:** atomic `os.Rename()` + `syscall.Exec()` (same PID group)
   - **Windows:** downloads as `trafficorch_new.exe`, launches a helper `.bat` that swaps the files after process exit

Each agent is notified **at most once per connection session**. If the update fails, the agent continues running and will be notified again on its next connection.

---

**Document Status:** ✅ Updated — v0.4.5 implemented and tested
**Last updated:** 2026-03-14
**Changes in v0.4.5:** Daemon mode (-d), binary distribution (port 9001), agent registry (agents.json), self-update via control channel
**Changes in v0.4.0:** Profile system — `.profile` files, host tagging, `[ASSIGNMENTS]`, SELF/group/ANY placeholders, EXTENDS inheritance, backward-compatible with v0.3.x direct rules
