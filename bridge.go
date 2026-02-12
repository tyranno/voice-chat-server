package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// BridgeConnection represents a connected bridge client
type BridgeConnection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	ConnectedAt time.Time `json:"connectedAt"`
	Conn        net.Conn  `json:"-"`
	LastPing    time.Time `json:"-"`
	ResponseCh  chan ChatResponseMessage `json:"-"`
	ErrorCh     chan ChatErrorMessage    `json:"-"`
}

// BridgeManager manages all bridge connections
type BridgeManager struct {
	connections map[string]*BridgeConnection
	mutex       sync.RWMutex
	config      *Config
}

// NewBridgeManager creates a new bridge manager
func NewBridgeManager(config *Config) *BridgeManager {
	return &BridgeManager{
		connections: make(map[string]*BridgeConnection),
		config:      config,
	}
}

// StartTCPServer starts the TCP server for bridge connections
func (bm *BridgeManager) StartTCPServer() error {
	addr := fmt.Sprintf(":%d", bm.config.BridgePort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start TCP server: %v", err)
	}

	log.Printf("TCP Bridge Server listening on port %d", bm.config.BridgePort)

	// Start heartbeat checker
	go bm.heartbeatChecker()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go bm.handleBridgeConnection(conn)
	}
}

// handleBridgeConnection handles a new bridge connection
func (bm *BridgeManager) handleBridgeConnection(conn net.Conn) {
	defer conn.Close()

	log.Printf("New bridge connection from %s", conn.RemoteAddr())

	// Wait for register message
	data, err := ReadMessage(conn)
	if err != nil {
		log.Printf("Failed to read register message: %v", err)
		return
	}

	var regMsg RegisterMessage
	if err := json.Unmarshal(data, &regMsg); err != nil {
		log.Printf("Failed to unmarshal register message: %v", err)
		return
	}

	if regMsg.Type != MsgTypeRegister {
		log.Printf("Expected register message, got: %s", regMsg.Type)
		return
	}

	// Validate bridge token
	if err := ValidateBridgeToken(bm.config, regMsg.Token); err != nil {
		log.Printf("Bridge authentication failed: %v", err)
		return
	}

	// Create bridge connection
	bridge := &BridgeConnection{
		ID:          generateID(),
		Name:        regMsg.Name,
		Status:      "online",
		ConnectedAt: time.Now(),
		Conn:        conn,
		LastPing:    time.Now(),
		ResponseCh:  make(chan ChatResponseMessage, 100),
		ErrorCh:     make(chan ChatErrorMessage, 100),
	}

	// Register the bridge
	bm.mutex.Lock()
	bm.connections[bridge.ID] = bridge
	bm.mutex.Unlock()

	log.Printf("Bridge registered: %s (%s)", bridge.Name, bridge.ID)

	// Handle messages in separate goroutines
	go bm.bridgeMessageHandler(bridge)
	go bm.bridgeResponseHandler(bridge)

	// Keep connection alive
	select {}
}

// bridgeMessageHandler handles incoming messages from bridge
func (bm *BridgeManager) bridgeMessageHandler(bridge *BridgeConnection) {
	defer func() {
		bm.removeBridge(bridge.ID)
		bridge.Conn.Close()
	}()

	for {
		data, err := ReadMessage(bridge.Conn)
		if err != nil {
			log.Printf("Failed to read message from bridge %s: %v", bridge.ID, err)
			return
		}

		var baseMsg Message
		if err := json.Unmarshal(data, &baseMsg); err != nil {
			log.Printf("Failed to unmarshal message: %v", err)
			continue
		}

		switch baseMsg.Type {
		case MsgTypeHeartbeat:
			bridge.LastPing = time.Now()

		case MsgTypeChatResponse:
			var respMsg ChatResponseMessage
			if err := json.Unmarshal(data, &respMsg); err != nil {
				log.Printf("Failed to unmarshal chat response: %v", err)
				continue
			}
			select {
			case bridge.ResponseCh <- respMsg:
			default:
				log.Printf("Response channel full for bridge %s", bridge.ID)
			}

		case MsgTypeChatError:
			var errMsg ChatErrorMessage
			if err := json.Unmarshal(data, &errMsg); err != nil {
				log.Printf("Failed to unmarshal chat error: %v", err)
				continue
			}
			select {
			case bridge.ErrorCh <- errMsg:
			default:
				log.Printf("Error channel full for bridge %s", bridge.ID)
			}

		default:
			log.Printf("Unknown message type from bridge %s: %s", bridge.ID, baseMsg.Type)
		}
	}
}

// bridgeResponseHandler handles responses to be sent to bridge
func (bm *BridgeManager) bridgeResponseHandler(bridge *BridgeConnection) {
	// This will be used by the relay system to send chat requests
}

// GetInstances returns all connected instances
func (bm *BridgeManager) GetInstances() []BridgeConnection {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()

	instances := make([]BridgeConnection, 0, len(bm.connections))
	for _, bridge := range bm.connections {
		// Create a copy without the connection and channels
		instance := BridgeConnection{
			ID:          bridge.ID,
			Name:        bridge.Name,
			Status:      bridge.Status,
			ConnectedAt: bridge.ConnectedAt,
		}
		instances = append(instances, instance)
	}

	return instances
}

// GetBridge returns a bridge connection by ID
func (bm *BridgeManager) GetBridge(id string) *BridgeConnection {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()
	return bm.connections[id]
}

// removeBridge removes a bridge from the connections
func (bm *BridgeManager) removeBridge(id string) {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	if bridge, exists := bm.connections[id]; exists {
		log.Printf("Bridge disconnected: %s (%s)", bridge.Name, bridge.ID)
		close(bridge.ResponseCh)
		close(bridge.ErrorCh)
		delete(bm.connections, id)
	}
}

// heartbeatChecker checks for inactive bridges and removes them
func (bm *BridgeManager) heartbeatChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bm.checkHeartbeats()
		}
	}
}

// checkHeartbeats checks for inactive bridges
func (bm *BridgeManager) checkHeartbeats() {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()

	now := time.Now()
	timeout := 60 * time.Second

	for id, bridge := range bm.connections {
		if now.Sub(bridge.LastPing) > timeout {
			log.Printf("Bridge timeout: %s (%s)", bridge.Name, bridge.ID)
			bridge.Status = "offline"
			bridge.Conn.Close()
			delete(bm.connections, id)
		}
	}
}

// SendChatRequest sends a chat request to a specific bridge
func (bm *BridgeManager) SendChatRequest(bridgeID, requestID string, messages []ChatMessage) error {
	bridge := bm.GetBridge(bridgeID)
	if bridge == nil {
		return fmt.Errorf("bridge not found: %s", bridgeID)
	}

	chatReq := ChatRequestMessage{
		Type:      MsgTypeChatRequest,
		RequestID: requestID,
		Messages:  messages,
	}

	return SendMessage(bridge.Conn, chatReq)
}

// generateID generates a unique ID for bridge connections
func generateID() string {
	return fmt.Sprintf("bridge_%d", time.Now().UnixNano())
}