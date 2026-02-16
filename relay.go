package main

import (
	"fmt"
	"log"
	"time"
)

// RelayManager handles message relaying between apps and bridges
type RelayManager struct {
	bridgeManager *BridgeManager
	config        *Config
}

// NewRelayManager creates a new relay manager
func NewRelayManager(bridgeManager *BridgeManager, config *Config) *RelayManager {
	return &RelayManager{
		bridgeManager: bridgeManager,
		config:        config,
	}
}

// RelayChat relays a chat request to the specified bridge and streams responses
func (rm *RelayManager) RelayChat(bridgeID, requestID string, messages []ChatMessage, user string, responseCh chan<- string, errorCh chan<- error, fileCh chan<- FileResponseMessage) {
	defer close(responseCh)
	defer close(errorCh)
	defer close(fileCh)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("RelayChat panic recovered: %v", r)
		}
	}()

	// Get the bridge connection
	bridge := rm.bridgeManager.GetBridge(bridgeID)
	if bridge == nil {
		errorCh <- fmt.Errorf("bridge not found: %s", bridgeID)
		return
	}

	// Register per-request channels (fixes shared channel fan-out bug)
	reqCh := bridge.RegisterRequest(requestID)
	defer bridge.UnregisterRequest(requestID)

	// Send chat request to bridge
	err := rm.bridgeManager.SendChatRequest(bridgeID, requestID, messages, user)
	if err != nil {
		errorCh <- fmt.Errorf("failed to send chat request: %v", err)
		return
	}

	log.Printf("Chat request sent to bridge %s (request: %s)", bridgeID, requestID)

	// Wait for responses with timeout
	timeout := time.NewTimer(2 * time.Minute)
	defer timeout.Stop()

	for {
		select {
		case response, ok := <-reqCh.ResponseCh:
			if !ok {
				errorCh <- fmt.Errorf("bridge disconnected")
				return
			}
			if response.Delta != "" {
				select {
				case responseCh <- response.Delta:
				case <-timeout.C:
					errorCh <- fmt.Errorf("timeout")
					return
				}
			}
			if response.Done {
				log.Printf("Chat request completed: %s", requestID)
				// Drain file events briefly (non-blocking)
				go rm.drainFileEvents(reqCh, fileCh, 10*time.Second)
				return
			}

		case chatError, ok := <-reqCh.ErrorCh:
			if !ok {
				errorCh <- fmt.Errorf("bridge disconnected")
				return
			}
			errorCh <- fmt.Errorf("chat error: %s", chatError.Error)
			return

		case fileMsg, ok := <-reqCh.FileCh:
			if !ok {
				continue
			}
			select {
			case fileCh <- fileMsg:
			default:
			}

		case <-timeout.C:
			errorCh <- fmt.Errorf("timeout waiting for response")
			return
		}
	}
}

// drainFileEvents waits briefly for file events after chat completion
func (rm *RelayManager) drainFileEvents(reqCh *RequestChannels, fileCh chan<- FileResponseMessage, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case fileMsg, ok := <-reqCh.FileCh:
			if !ok {
				return
			}
			select {
			case fileCh <- fileMsg:
			default:
			}
		case <-timer.C:
			return
		}
	}
}

// ChatRequest represents an incoming chat request
type ChatRequest struct {
	InstanceID     string        `json:"instanceId"`
	Messages       []ChatMessage `json:"messages"`
	ConversationID string        `json:"conversationId,omitempty"`
}

// ValidateChatRequest validates a chat request
func (rm *RelayManager) ValidateChatRequest(req *ChatRequest) error {
	if req.InstanceID == "" {
		return fmt.Errorf("instanceId is required")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages are required")
	}
	for i, msg := range req.Messages {
		if msg.Role == "" {
			return fmt.Errorf("message[%d]: role is required", i)
		}
		if msg.Content == "" {
			return fmt.Errorf("message[%d]: content is required", i)
		}
		if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system" {
			return fmt.Errorf("message[%d]: invalid role '%s'", i, msg.Role)
		}
	}
	return nil
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}
