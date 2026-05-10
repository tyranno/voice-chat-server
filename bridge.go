package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// RequestChannels holds per-request channels to avoid fan-out bugs
type RequestChannels struct {
	ResponseCh chan ChatResponseMessage
	ErrorCh    chan ChatErrorMessage
	FileCh     chan FileResponseMessage
}

// TaskChannels holds per-task channels for Phase 3 Ralph autonomous tasks
type TaskChannels struct {
	ProgressCh chan TaskProgressMessage
	LogCh      chan TaskLogMessage
	DoneCh     chan TaskDoneMessage
	ErrorCh    chan TaskErrorMessage
	FileCh     chan FileResponseMessage
}

// BridgeConnection represents a connected bridge client
type BridgeConnection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	ConnectedAt time.Time `json:"connectedAt"`
	Conn        net.Conn  `json:"-"`
	LastPing    time.Time `json:"-"`
	// Per-request channel registry (replaces shared channels)
	requestChans map[string]*RequestChannels `json:"-"`
	requestMu    sync.RWMutex                `json:"-"`
	// Per-task channel registry (Phase 3)
	taskChans map[string]*TaskChannels `json:"-"`
	taskMu    sync.RWMutex             `json:"-"`
}

// RegisterRequest creates per-request channels
func (bc *BridgeConnection) RegisterRequest(requestID string) *RequestChannels {
	ch := &RequestChannels{
		ResponseCh: make(chan ChatResponseMessage, 50),
		ErrorCh:    make(chan ChatErrorMessage, 10),
		FileCh:     make(chan FileResponseMessage, 10),
	}
	bc.requestMu.Lock()
	bc.requestChans[requestID] = ch
	bc.requestMu.Unlock()
	return ch
}

// UnregisterRequest removes per-request channels
func (bc *BridgeConnection) UnregisterRequest(requestID string) {
	bc.requestMu.Lock()
	delete(bc.requestChans, requestID)
	bc.requestMu.Unlock()
}

// GetRequestChannels returns channels for a specific request
func (bc *BridgeConnection) GetRequestChannels(requestID string) *RequestChannels {
	bc.requestMu.RLock()
	defer bc.requestMu.RUnlock()
	return bc.requestChans[requestID]
}

// RegisterTask creates per-task channels (Phase 3)
func (bc *BridgeConnection) RegisterTask(taskID string) *TaskChannels {
	ch := &TaskChannels{
		ProgressCh: make(chan TaskProgressMessage, 100),
		LogCh:      make(chan TaskLogMessage, 200),
		DoneCh:     make(chan TaskDoneMessage, 1),
		ErrorCh:    make(chan TaskErrorMessage, 1),
		FileCh:     make(chan FileResponseMessage, 10),
	}
	bc.taskMu.Lock()
	bc.taskChans[taskID] = ch
	bc.taskMu.Unlock()
	return ch
}

// UnregisterTask removes per-task channels
func (bc *BridgeConnection) UnregisterTask(taskID string) {
	bc.taskMu.Lock()
	delete(bc.taskChans, taskID)
	bc.taskMu.Unlock()
}

// GetTaskChannels returns channels for a specific task
func (bc *BridgeConnection) GetTaskChannels(taskID string) *TaskChannels {
	bc.taskMu.RLock()
	defer bc.taskMu.RUnlock()
	return bc.taskChans[taskID]
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
	
	var listener net.Listener
	var err error
	
	if (bm.config.BridgeTLSEnabled || bm.config.TLSEnabled) && bm.config.TLSCert != "" && bm.config.TLSKey != "" {
		// TLS enabled
		cert, err := tls.LoadX509KeyPair(bm.config.TLSCert, bm.config.TLSKey)
		if err != nil {
			return fmt.Errorf("failed to load TLS cert: %v", err)
		}
		tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
		listener, err = tls.Listen("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to start TLS TCP server: %v", err)
		}
		log.Printf("TCP Bridge Server listening on port %d (TLS enabled)", bm.config.BridgePort)
	} else {
		// Plain TCP
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("failed to start TCP server: %v", err)
		}
		log.Printf("TCP Bridge Server listening on port %d", bm.config.BridgePort)
	}

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
		ID:           generateID(),
		Name:         regMsg.Name,
		Status:       "online",
		ConnectedAt:  time.Now(),
		Conn:         conn,
		LastPing:     time.Now(),
		requestChans: make(map[string]*RequestChannels),
		taskChans:    make(map[string]*TaskChannels),
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
			// Send heartbeat response so bridge's ReadDeadline doesn't expire
			SendMessage(bridge.Conn, HeartbeatMessage{Type: MsgTypeHeartbeat})

		case MsgTypeChatResponse:
			var respMsg ChatResponseMessage
			if err := json.Unmarshal(data, &respMsg); err != nil {
				log.Printf("Failed to unmarshal chat response: %v", err)
				continue
			}
			if ch := bridge.GetRequestChannels(respMsg.RequestID); ch != nil {
				select {
				case ch.ResponseCh <- respMsg:
				default:
					log.Printf("Response channel full for request %s", respMsg.RequestID)
				}
			}

		case MsgTypeChatError:
			var errMsg ChatErrorMessage
			if err := json.Unmarshal(data, &errMsg); err != nil {
				log.Printf("Failed to unmarshal chat error: %v", err)
				continue
			}
			if ch := bridge.GetRequestChannels(errMsg.RequestID); ch != nil {
				select {
				case ch.ErrorCh <- errMsg:
				default:
				}
			}

		case MsgTypeFileResponse:
			var fileMsg FileResponseMessage
			if err := json.Unmarshal(data, &fileMsg); err != nil {
				log.Printf("Failed to unmarshal file response: %v", err)
				continue
			}
			// Try chat first
			if ch := bridge.GetRequestChannels(fileMsg.RequestID); ch != nil {
				select {
				case ch.FileCh <- fileMsg:
				default:
				}
			} else if tch := bridge.GetTaskChannels(fileMsg.RequestID); tch != nil {
				// File can also come from a task (RequestID == TaskID)
				select {
				case tch.FileCh <- fileMsg:
				default:
				}
			}

		case MsgTypeTaskProgress:
			var progMsg TaskProgressMessage
			if err := json.Unmarshal(data, &progMsg); err != nil {
				log.Printf("Failed to unmarshal task progress: %v", err)
				continue
			}
			if tch := bridge.GetTaskChannels(progMsg.TaskID); tch != nil {
				select {
				case tch.ProgressCh <- progMsg:
				default:
				}
			}

		case MsgTypeTaskLog:
			var logMsg TaskLogMessage
			if err := json.Unmarshal(data, &logMsg); err != nil {
				log.Printf("Failed to unmarshal task log: %v", err)
				continue
			}
			if tch := bridge.GetTaskChannels(logMsg.TaskID); tch != nil {
				select {
				case tch.LogCh <- logMsg:
				default:
				}
			}

		case MsgTypeTaskDone:
			var doneMsg TaskDoneMessage
			if err := json.Unmarshal(data, &doneMsg); err != nil {
				log.Printf("Failed to unmarshal task done: %v", err)
				continue
			}
			if tch := bridge.GetTaskChannels(doneMsg.TaskID); tch != nil {
				select {
				case tch.DoneCh <- doneMsg:
				default:
				}
			}

		case MsgTypeTaskError:
			var errMsg TaskErrorMessage
			if err := json.Unmarshal(data, &errMsg); err != nil {
				log.Printf("Failed to unmarshal task error: %v", err)
				continue
			}
			if tch := bridge.GetTaskChannels(errMsg.TaskID); tch != nil {
				select {
				case tch.ErrorCh <- errMsg:
				default:
				}
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
		// Close all per-request channels
		bridge.requestMu.Lock()
		for reqID, ch := range bridge.requestChans {
			close(ch.ResponseCh)
			close(ch.ErrorCh)
			close(ch.FileCh)
			delete(bridge.requestChans, reqID)
		}
		bridge.requestMu.Unlock()
		// Close all per-task channels
		bridge.taskMu.Lock()
		for taskID, tch := range bridge.taskChans {
			close(tch.ProgressCh)
			close(tch.LogCh)
			close(tch.DoneCh)
			close(tch.ErrorCh)
			close(tch.FileCh)
			delete(bridge.taskChans, taskID)
		}
		bridge.taskMu.Unlock()
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
func (bm *BridgeManager) SendChatRequest(bridgeID, requestID string, messages []ChatMessage, user string) error {
	bridge := bm.GetBridge(bridgeID)
	if bridge == nil {
		return fmt.Errorf("bridge not found: %s", bridgeID)
	}

	chatReq := ChatRequestMessage{
		Type:      MsgTypeChatRequest,
		RequestID: requestID,
		Messages:  messages,
		User:      user,
	}

	return SendMessage(bridge.Conn, chatReq)
}

// SendTaskStart sends a Ralph task start request to a specific bridge
func (bm *BridgeManager) SendTaskStart(bridgeID string, msg TaskStartMessage) error {
	bridge := bm.GetBridge(bridgeID)
	if bridge == nil {
		return fmt.Errorf("bridge not found: %s", bridgeID)
	}
	msg.Type = MsgTypeTaskStart
	return SendMessage(bridge.Conn, msg)
}

// SendTaskCancel sends a task cancel request to a specific bridge
func (bm *BridgeManager) SendTaskCancel(bridgeID, taskID string) error {
	bridge := bm.GetBridge(bridgeID)
	if bridge == nil {
		return fmt.Errorf("bridge not found: %s", bridgeID)
	}
	return SendMessage(bridge.Conn, TaskCancelMessage{Type: MsgTypeTaskCancel, TaskID: taskID})
}

// generateID generates a unique ID for bridge connections
func generateID() string {
	return fmt.Sprintf("bridge_%d", time.Now().UnixNano())
}