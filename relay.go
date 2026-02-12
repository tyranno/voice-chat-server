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
func (rm *RelayManager) RelayChat(bridgeID, requestID string, messages []ChatMessage, responseCh chan<- string, errorCh chan<- error) {
	defer close(responseCh)
	defer close(errorCh)

	// Get the bridge connection
	bridge := rm.bridgeManager.GetBridge(bridgeID)
	if bridge == nil {
		errorCh <- fmt.Errorf("bridge not found: %s", bridgeID)
		return
	}

	// Send chat request to bridge
	err := rm.bridgeManager.SendChatRequest(bridgeID, requestID, messages)
	if err != nil {
		errorCh <- fmt.Errorf("failed to send chat request: %v", err)
		return
	}

	log.Printf("Chat request sent to bridge %s (request: %s)", bridgeID, requestID)

	// Wait for responses with timeout
	timeout := time.NewTimer(5 * time.Minute) // 5 minute timeout
	defer timeout.Stop()

	for {
		select {
		case response := <-bridge.ResponseCh:
			if response.RequestID != requestID {
				continue // Skip responses for other requests
			}

			// Send delta to response channel
			if response.Delta != "" {
				select {
				case responseCh <- response.Delta:
				case <-timeout.C:
					errorCh <- fmt.Errorf("timeout waiting for response")
					return
				}
			}

			// Check if this is the final response
			if response.Done {
				log.Printf("Chat request completed: %s", requestID)
				return
			}

		case chatError := <-bridge.ErrorCh:
			if chatError.RequestID != requestID {
				continue // Skip errors for other requests
			}

			errorCh <- fmt.Errorf("chat error: %s", chatError.Error)
			return

		case <-timeout.C:
			errorCh <- fmt.Errorf("timeout waiting for response")
			return
		}
	}
}

// ChatRequest represents an incoming chat request
type ChatRequest struct {
	InstanceID string        `json:"instanceId"`
	Messages   []ChatMessage `json:"messages"`
}

// ValidateChatRequest validates a chat request
func (rm *RelayManager) ValidateChatRequest(req *ChatRequest) error {
	if req.InstanceID == "" {
		return fmt.Errorf("instanceId is required")
	}

	if len(req.Messages) == 0 {
		return fmt.Errorf("messages are required")
	}

	// Validate message structure
	for i, msg := range req.Messages {
		if msg.Role == "" {
			return fmt.Errorf("message[%d]: role is required", i)
		}
		if msg.Content == "" {
			return fmt.Errorf("message[%d]: content is required", i)
		}
		// Validate role values
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