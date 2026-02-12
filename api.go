package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// APIServer handles HTTP API requests
type APIServer struct {
	bridgeManager *BridgeManager
	relayManager  *RelayManager
	config        *Config
}

// NewAPIServer creates a new API server
func NewAPIServer(bridgeManager *BridgeManager, relayManager *RelayManager, config *Config) *APIServer {
	return &APIServer{
		bridgeManager: bridgeManager,
		relayManager:  relayManager,
		config:        config,
	}
}

// StartHTTPServer starts the HTTP API server
func (api *APIServer) StartHTTPServer() error {
	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("/health", api.handleHealth)

	// Protected endpoints with authentication
	authMux := http.NewServeMux()
	authMux.HandleFunc("/api/instances", api.handleInstances)
	authMux.HandleFunc("/api/chat", api.handleChat)

	// Apply auth middleware to protected routes
	mux.Handle("/api/", AuthMiddleware(api.config)(authMux))

	addr := fmt.Sprintf(":%d", api.config.Port)
	log.Printf("HTTP API Server listening on port %d", api.config.Port)

	return http.ListenAndServe(addr, mux)
}

// handleHealth handles health check requests
func (api *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().UTC(),
		"instances": len(api.bridgeManager.GetInstances()),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleInstances handles GET /api/instances
func (api *APIServer) handleInstances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	instances := api.bridgeManager.GetInstances()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(instances); err != nil {
		log.Printf("Failed to encode instances response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	log.Printf("Instances requested, returned %d instances", len(instances))
}

// handleChat handles POST /api/chat with SSE streaming
func (api *APIServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var chatReq ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
		log.Printf("Failed to decode chat request: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate request
	if err := api.relayManager.ValidateChatRequest(&chatReq); err != nil {
		log.Printf("Chat request validation failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if bridge exists
	if api.bridgeManager.GetBridge(chatReq.InstanceID) == nil {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	// Set headers for Server-Sent Events
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	// Generate request ID
	requestID := generateRequestID()

	log.Printf("Starting chat relay: instance=%s, requestID=%s", chatReq.InstanceID, requestID)

	// Create channels for streaming
	responseCh := make(chan string)
	errorCh := make(chan error)

	// Start relay in goroutine
	go api.relayManager.RelayChat(chatReq.InstanceID, requestID, chatReq.Messages, responseCh, errorCh)

	// Stream responses
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	for {
		select {
		case delta, ok := <-responseCh:
			if !ok {
				// Channel closed, send done signal
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				log.Printf("Chat relay completed: requestID=%s", requestID)
				return
			}

			// Send delta as SSE
			deltaData := map[string]string{"delta": delta}
			dataBytes, _ := json.Marshal(deltaData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()

		case err := <-errorCh:
			// Send error as SSE
			errorData := map[string]string{"error": err.Error()}
			dataBytes, _ := json.Marshal(errorData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()
			log.Printf("Chat relay error: requestID=%s, error=%v", requestID, err)
			return

		case <-r.Context().Done():
			// Client disconnected
			log.Printf("Client disconnected: requestID=%s", requestID)
			return
		}
	}
}