// Package comm implements the communication protocol between Master and Agents.
package comm

import (
	"encoding/json"
	"fmt"
)

// MessageType defines the type of message in the protocol.
type MessageType string

const (
	MsgRegister        MessageType = "REGISTER"         // Agent registers with master
	MsgRegisterAck     MessageType = "REGISTER_ACK"     // Master acknowledges registration
	MsgHeartbeat       MessageType = "HEARTBEAT"        // Periodic heartbeat from agent
	MsgConfigUpdate    MessageType = "CONFIG_UPDATE"    // New config loaded on master
	MsgTrafficStart    MessageType = "TRAFFIC_START"    // Start traffic generation
	MsgTrafficStop     MessageType = "TRAFFIC_STOP"     // Stop all traffic
	MsgStatus          MessageType = "STATUS"           // Agent status update
	MsgError           MessageType = "ERROR"            // Error message
	MsgWarning         MessageType = "WARNING"          // Non-fatal warning from agent
	MsgUpdateAvailable MessageType = "UPDATE_AVAILABLE" // Master notifies agent of a newer binary
)

// BaseMessage is the base structure for all messages.
type BaseMessage struct {
	Type      MessageType `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Version   string      `json:"version"`
}

// RegisterMessage is sent by agents to register with the master.
type RegisterMessage struct {
	AgentID      string `json:"agent_id"`
	Hostname     string `json:"hostname,omitempty"`
	Platform     string `json:"platform,omitempty"`
	AgentIP      string `json:"agent_ip,omitempty"`      // Self-reported IP for extended-format routing
	AgentVersion string `json:"agent_version,omitempty"` // Binary version of the registering agent
	BaseMessage
}

// RegisterAckMessage is sent by master to acknowledge agent registration.
type RegisterAckMessage struct {
	BaseMessage
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`  // "success" or "error"
	Message string `json:"message,omitempty"`
}

// HeartbeatMessage is sent periodically by agents to master.
type HeartbeatMessage struct {
	BaseMessage
	AgentID      string  `json:"agent_id"`
	AgentVersion string  `json:"agent_version,omitempty"` // Binary version of the agent
	CPUUsage     float64 `json:"cpu_usage,omitempty"`
	MemoryUsage  int64   `json:"memory_usage_bytes,omitempty"`
	ActiveRules  int     `json:"active_rules,omitempty"`
}

// ConfigUpdateMessage is sent by master to agents when config changes.
// TTL tells the agent how long (seconds) to honour these instructions
// without a master connection. 0 means the instructions never expire.
type ConfigUpdateMessage struct {
	BaseMessage
	TTL   int            `json:"ttl,omitempty"`
	Rules []*TrafficRule `json:"rules"`
}

// TrafficStartMessage instructs agents to start traffic generation.
type TrafficStartMessage struct {
	BaseMessage
	AgentID string         `json:"agent_id,omitempty"` // Specific agent or empty for all
	Rules   []*TrafficRule `json:"rules"`
}

// TrafficStopMessage instructs agents to stop all traffic.
type TrafficStopMessage struct {
	BaseMessage
	AgentID string `json:"agent_id,omitempty"` // Specific agent or empty for all
}

// StatusMessage provides status updates from agents.
type StatusMessage struct {
	BaseMessage
	AgentID           string `json:"agent_id"`
	State             string `json:"state"` // "idle", "generating", "error"
	ErrorMsg          string `json:"error_msg,omitempty"`
	ActiveConnections int    `json:"active_connections,omitempty"`
}

// ErrorMessage is used for error reporting.
type ErrorMessage struct {
	BaseMessage
	AgentID string `json:"agent_id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WarningMessage is sent by an agent to report a non-fatal condition to the master.
type WarningMessage struct {
	BaseMessage
	AgentID string `json:"agent_id"`
	Code    string `json:"code"`    // e.g. "NON_ROOT"
	Message string `json:"message"`
}

// UpdateAvailableMessage is sent by the master when it carries a newer binary
// than the agent's current version.  The agent downloads the binary from the
// master's distribution HTTP server (port HTTPPort) and restarts itself.
type UpdateAvailableMessage struct {
	BaseMessage
	NewVersion string `json:"new_version"`  // Version string of the new binary
	HTTPPort   int    `json:"http_port"`    // Port of master's binary distribution server
	SHA256     string `json:"sha256"`       // Hex-encoded SHA-256 of the binary at /binary
}

// TrafficRule represents a traffic generation rule shared between config and comm.
type TrafficRule struct {
	Protocol string `json:"protocol"`         // "TCP" or "UDP"
	Source   string `json:"source,omitempty"` // Source IP (extended format only)
	Target   string `json:"target,omitempty"` // Destination IP (empty for "listen" rules)
	Port     int    `json:"port"`
	Interval int    `json:"interval"`         // Seconds between connections
	Count    int    `json:"count"`            // -1 = loop forever
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`   // "connect" (default) or "listen"
}

// NewBaseMessage creates a new base message with current timestamp.
func NewBaseMessage(msgType MessageType) BaseMessage {
	return BaseMessage{
		Type:    msgType,
		Version: "1.0",
	}
}

// Serialize serializes a message to JSON bytes.
func Serialize(msg interface{}) ([]byte, error) {
	return json.Marshal(msg)
}

// Deserialize deserializes JSON bytes into a BaseMessage.
func Deserialize(data []byte) (*BaseMessage, error) {
	var msg BaseMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to deserialize message: %w", err)
	}
	return &msg, nil
}
