// Package comm implements the communication protocol between Master and Agents.
package comm

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// hmacSize is the byte length of a SHA-256 HMAC signature appended to every message.
const hmacSize = 32

// Channel represents a PSK-authenticated, length-prefixed message channel over a net.Conn.
type Channel struct {
	conn net.Conn
	psk  string
	mu   sync.Mutex
}

// NewChannel wraps an existing net.Conn in a Channel using the given pre-shared key.
func NewChannel(conn net.Conn, psk string) *Channel {
	return &Channel{conn: conn, psk: psk}
}

// ReadMessage reads and HMAC-validates one message from the channel.
// A read deadline equal to ChannelIdleTimeout is set before each read so that
// permanently-silent connections are eventually cleaned up.
// Returns the parsed BaseMessage (for type dispatch) and the raw JSON bytes.
func (c *Channel) ReadMessage() (*BaseMessage, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Set a per-read deadline so idle connections are not held open forever.
	if err := c.conn.SetReadDeadline(time.Now().Add(ChannelIdleTimeout)); err != nil {
		return nil, nil, fmt.Errorf("failed to set read deadline: %w", err)
	}

	var lenBuf [4]byte
	if _, err := io.ReadFull(c.conn, lenBuf[:]); err != nil {
		return nil, nil, err // caller checks net.Error.Timeout()
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])

	body := make([]byte, msgLen)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return nil, nil, fmt.Errorf("failed to read message body: %w", err)
	}

	// Clear the deadline after a successful read.
	_ = c.conn.SetReadDeadline(time.Time{})

	if len(body) < hmacSize {
		return nil, nil, fmt.Errorf("message too short: need at least %d bytes for HMAC", hmacSize)
	}

	messageData := body[:len(body)-hmacSize]
	signature := body[len(body)-hmacSize:]

	expectedSig := c.signMessage(messageData)
	if !hmac.Equal(signature, expectedSig) {
		return nil, nil, fmt.Errorf("PSK verification failed: message signature mismatch")
	}

	msg, err := Deserialize(messageData)
	if err != nil {
		return nil, nil, err
	}

	return msg, messageData, nil
}

// WriteMessage serialises msg to JSON, appends an HMAC signature, and sends it
// over the channel with a 4-byte big-endian length prefix.
func (c *Channel) WriteMessage(msg interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	body, err := Serialize(msg)
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	signature := c.signMessage(body)
	bodyWithSig := append(body, signature...)

	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(bodyWithSig)))

	if _, err := c.conn.Write(lenBuf); err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	if _, err := c.conn.Write(bodyWithSig); err != nil {
		return fmt.Errorf("failed to write message body: %w", err)
	}

	return nil
}

// signMessage returns a SHA-256 HMAC of data using the channel's PSK.
func (c *Channel) signMessage(data []byte) []byte {
	h := hmac.New(sha256.New, []byte(c.psk))
	h.Write(data)
	return h.Sum(nil)
}

// Close closes the underlying network connection.
func (c *Channel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// ─── Master-side ─────────────────────────────────────────────────────────────

// agentConn bundles a Channel with metadata about the connected agent.
type agentConn struct {
	channel  *Channel
	remoteIP string // extracted from TCP remote address (or self-reported by agent)
	hostname string // self-reported in REGISTER
}

// MasterServer listens for incoming agent connections and dispatches messages.
type MasterServer struct {
	listener   net.Listener
	psk        string
	agents     map[string]*agentConn
	mu         sync.RWMutex
	onRegister func(agentID string, hostname string)
	onTraffic  func(agentID string, rules []*TrafficRule)
}

// NewMasterServer creates a MasterServer that listens on port, authenticates
// with psk, and invokes the supplied callbacks.
func NewMasterServer(psk string, port int, onRegister func(string, string), onTraffic func(string, []*TrafficRule)) (*MasterServer, error) {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to start listener on %s: %w", addr, err)
	}

	server := &MasterServer{
		listener:   listener,
		psk:        psk,
		agents:     make(map[string]*agentConn),
		onRegister: onRegister,
		onTraffic:  onTraffic,
	}

	go server.acceptLoop()

	return server, nil
}

func (s *MasterServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			log.Printf("comm: listener error: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

func (s *MasterServer) handleConnection(conn net.Conn) {
	// Extract agent IP from the TCP remote address.
	remoteIP := ""
	if host, _, err := net.SplitHostPort(conn.RemoteAddr().String()); err == nil {
		remoteIP = host
	}

	channel := NewChannel(conn, s.psk)
	defer channel.Close()

	msg, msgBytes, err := channel.ReadMessage()
	if err != nil {
		log.Printf("comm: failed to read registration from %s: %v", conn.RemoteAddr(), err)
		return
	}

	if msg.Type != MsgRegister {
		log.Printf("comm: expected REGISTER from %s, got %s", conn.RemoteAddr(), msg.Type)
		return
	}

	var regMsg RegisterMessage
	if err := json.Unmarshal(msgBytes, &regMsg); err != nil {
		log.Printf("comm: failed to parse REGISTER from %s: %v", conn.RemoteAddr(), err)
		return
	}

	if regMsg.AgentID == "" {
		log.Printf("comm: REGISTER from %s is missing agent_id", conn.RemoteAddr())
		return
	}

	// Prefer self-reported IP (useful behind NAT); fall back to socket remote addr.
	if regMsg.AgentIP != "" {
		remoteIP = regMsg.AgentIP
	}

	s.mu.Lock()
	s.agents[regMsg.AgentID] = &agentConn{
		channel:  channel,
		remoteIP: remoteIP,
		hostname: regMsg.Hostname,
	}
	s.mu.Unlock()

	// Send ACK before invoking onRegister: the callback may push a CONFIG_UPDATE
	// and the agent must receive REGISTER_ACK first.
	ack := &RegisterAckMessage{
		BaseMessage: BaseMessage{
			Type:      MsgRegisterAck,
			Timestamp: time.Now().Unix(),
			Version:   ProtocolVersion,
		},
		AgentID: regMsg.AgentID,
		Status:  "success",
	}

	if err := channel.WriteMessage(ack); err != nil {
		log.Printf("comm: failed to send ACK to %s: %v", regMsg.AgentID, err)
		s.removeAgent(regMsg.AgentID)
		return
	}

	// Notify asynchronously so processMessages can start without waiting for
	// the callback (which may sleep before sending the initial CONFIG_UPDATE).
	if s.onRegister != nil {
		go s.onRegister(regMsg.AgentID, regMsg.Hostname)
	}

	s.processMessages(channel, regMsg.AgentID)
}

func (s *MasterServer) processMessages(channel *Channel, agentID string) {
	for {
		msg, msgBytes, err := channel.ReadMessage()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Printf("comm: agent %s channel idle timeout — disconnecting", agentID)
			} else {
				log.Printf("comm: lost connection to agent %s: %v", agentID, err)
			}
			s.removeAgent(agentID)
			return
		}

		switch msg.Type {
		case MsgHeartbeat:
			var hb HeartbeatMessage
			if err := json.Unmarshal(msgBytes, &hb); err != nil {
				log.Printf("comm: failed to parse heartbeat from %s: %v", agentID, err)
				continue
			}
			log.Printf("comm: heartbeat from %s – CPU %.1f%% MEM %dMB active-rules %d",
				agentID, hb.CPUUsage, hb.MemoryUsage/1024/1024, hb.ActiveRules)

		case MsgStatus:
			var status StatusMessage
			if err := json.Unmarshal(msgBytes, &status); err != nil {
				log.Printf("comm: failed to parse status from %s: %v", agentID, err)
				continue
			}
			log.Printf("comm: agent %s status: %s", agentID, status.State)

		case MsgError:
			var errMsg ErrorMessage
			if err := json.Unmarshal(msgBytes, &errMsg); err != nil {
				log.Printf("comm: failed to parse error from %s: %v", agentID, err)
				continue
			}
			log.Printf("comm: agent %s reported error [%s]: %s", agentID, errMsg.Code, errMsg.Message)

		case MsgWarning:
			var warnMsg WarningMessage
			if err := json.Unmarshal(msgBytes, &warnMsg); err != nil {
				log.Printf("comm: failed to parse warning from %s: %v", agentID, err)
				continue
			}
			log.Printf("comm: WARNING from agent %s [%s]: %s", agentID, warnMsg.Code, warnMsg.Message)

		default:
			log.Printf("comm: unknown message type %q from agent %s", msg.Type, agentID)
		}
	}
}

func (s *MasterServer) removeAgent(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ac, ok := s.agents[agentID]; ok {
		ac.channel.Close()
		delete(s.agents, agentID)
	}
}

// GetAgents returns the IDs of all currently connected agents.
func (s *MasterServer) GetAgents() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.agents))
	for id := range s.agents {
		ids = append(ids, id)
	}
	return ids
}

// GetAgentIPs returns a map of agentID → remoteIP for all connected agents.
// Used by the master to match agents against SOURCE/DEST IPs in extended rules.
func (s *MasterServer) GetAgentIPs() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := make(map[string]string, len(s.agents))
	for id, ac := range s.agents {
		m[id] = ac.remoteIP
	}
	return m
}

// StartTraffic sends a traffic-start command to one agent (by ID) or all agents.
func (s *MasterServer) StartTraffic(agentID string, rules []*TrafficRule) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if agentID == "" {
		for id, ac := range s.agents {
			msg := &TrafficStartMessage{
				BaseMessage: BaseMessage{Type: MsgTrafficStart, Timestamp: time.Now().Unix(), Version: ProtocolVersion},
				Rules:       rules,
			}
			if err := ac.channel.WriteMessage(msg); err != nil {
				log.Printf("comm: failed to send traffic-start to agent %s: %v", id, err)
			}
		}
		return nil
	}

	ac, ok := s.agents[agentID]
	if !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	msg := &TrafficStartMessage{
		BaseMessage: BaseMessage{Type: MsgTrafficStart, Timestamp: time.Now().Unix(), Version: ProtocolVersion},
		AgentID:     agentID,
		Rules:       rules,
	}
	return ac.channel.WriteMessage(msg)
}

// StopTraffic sends a traffic-stop command to one agent (by ID) or all agents.
func (s *MasterServer) StopTraffic(agentID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if agentID == "" {
		for id, ac := range s.agents {
			msg := &TrafficStopMessage{
				BaseMessage: BaseMessage{Type: MsgTrafficStop, Timestamp: time.Now().Unix(), Version: ProtocolVersion},
			}
			if err := ac.channel.WriteMessage(msg); err != nil {
				log.Printf("comm: failed to send traffic-stop to agent %s: %v", id, err)
			}
		}
		return nil
	}

	ac, ok := s.agents[agentID]
	if !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	msg := &TrafficStopMessage{
		BaseMessage: BaseMessage{Type: MsgTrafficStop, Timestamp: time.Now().Unix(), Version: ProtocolVersion},
		AgentID:     agentID,
	}
	return ac.channel.WriteMessage(msg)
}

// CloseAllAgents closes every active agent connection and resets the agent map.
func (s *MasterServer) CloseAllAgents() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ac := range s.agents {
		ac.channel.Close()
	}
	s.agents = make(map[string]*agentConn)
}

// SendToAllAgents broadcasts msg to every connected agent.
func (s *MasterServer) SendToAllAgents(msg interface{}) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for id, ac := range s.agents {
		if err := ac.channel.WriteMessage(msg); err != nil {
			log.Printf("comm: failed to send to agent %s: %v", id, err)
		}
	}
	return nil
}

// SendToAgent sends msg to a single agent identified by agentID.
func (s *MasterServer) SendToAgent(agentID string, msg interface{}) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ac, ok := s.agents[agentID]
	if !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}
	return ac.channel.WriteMessage(msg)
}

// ─── Agent-side ───────────────────────────────────────────────────────────────

// AgentClient manages the connection from an agent to the master.
type AgentClient struct {
	conn    net.Conn
	channel *Channel
	master  string
	port    int
	psk     string
}

// NewAgentClient dials the master at host:port, authenticates via psk, and returns a ready client.
func NewAgentClient(master string, port int, psk string) (*AgentClient, error) {
	addr := net.JoinHostPort(master, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, MasterConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master at %s: %w", addr, err)
	}

	return &AgentClient{
		conn:    conn,
		channel: NewChannel(conn, psk),
		master:  master,
		port:    port,
		psk:     psk,
	}, nil
}

// Register sends a REGISTER message to the master and waits for acknowledgement.
func (c *AgentClient) Register(agentID string, hostname string, platform string) error {
	msg := &RegisterMessage{
		BaseMessage: BaseMessage{
			Type:      MsgRegister,
			Timestamp: time.Now().Unix(),
			Version:   ProtocolVersion,
		},
		AgentID:  agentID,
		Hostname: hostname,
		Platform: platform,
	}

	if err := c.channel.WriteMessage(msg); err != nil {
		return fmt.Errorf("failed to send registration: %w", err)
	}

	respMsg, msgBytes, err := c.channel.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read registration response: %w", err)
	}

	if respMsg.Type != MsgRegisterAck {
		return fmt.Errorf("expected REGISTER_ACK, got %s", respMsg.Type)
	}

	var ack RegisterAckMessage
	if err := json.Unmarshal(msgBytes, &ack); err != nil {
		return fmt.Errorf("failed to parse REGISTER_ACK: %w", err)
	}

	if ack.Status != "success" {
		return fmt.Errorf("registration rejected by master: %s", ack.Message)
	}

	// Send an initial heartbeat to confirm the channel is alive.
	go func() {
		time.Sleep(1 * time.Second)
		_ = c.SendHeartbeat(0.0, 0, 0)
	}()

	return nil
}

// StartTraffic sends a traffic-start request with the given rules to the master.
func (c *AgentClient) StartTraffic(rules []*TrafficRule) error {
	msg := &TrafficStartMessage{
		BaseMessage: BaseMessage{
			Type:      MsgTrafficStart,
			Timestamp: time.Now().Unix(),
			Version:   ProtocolVersion,
		},
		Rules: rules,
	}
	return c.channel.WriteMessage(msg)
}

// SendHeartbeat sends a heartbeat with the current resource metrics to the master.
func (c *AgentClient) SendHeartbeat(cpuUsage float64, memoryUsage int64, activeRules int) error {
	msg := &HeartbeatMessage{
		BaseMessage: BaseMessage{
			Type:      MsgHeartbeat,
			Timestamp: time.Now().Unix(),
			Version:   ProtocolVersion,
		},
		CPUUsage:    cpuUsage,
		MemoryUsage: memoryUsage,
		ActiveRules: activeRules,
	}
	return c.channel.WriteMessage(msg)
}

// SendWarning sends a non-fatal warning message to the master.
func (c *AgentClient) SendWarning(agentID, code, message string) error {
	msg := &WarningMessage{
		BaseMessage: BaseMessage{
			Type:      MsgWarning,
			Timestamp: time.Now().Unix(),
			Version:   ProtocolVersion,
		},
		AgentID: agentID,
		Code:    code,
		Message: message,
	}
	return c.channel.WriteMessage(msg)
}

// Close closes the connection to the master.
func (c *AgentClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// ReadMessage reads the next message from the master channel.
func (c *AgentClient) ReadMessage() (*BaseMessage, []byte, error) {
	return c.channel.ReadMessage()
}
