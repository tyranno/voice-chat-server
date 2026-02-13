package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// APIServer handles HTTP API requests
type APIServer struct {
	bridgeManager *BridgeManager
	relayManager  *RelayManager
	deviceStore   *DeviceStore
	config        *Config
}

// NewAPIServer creates a new API server
func NewAPIServer(bridgeManager *BridgeManager, relayManager *RelayManager, config *Config) *APIServer {
	// Ensure data dir exists
	os.MkdirAll(config.DataDir, 0755)

	return &APIServer{
		bridgeManager: bridgeManager,
		relayManager:  relayManager,
		deviceStore:   NewDeviceStore(filepath.Join(config.DataDir, "devices.json")),
		config:        config,
	}
}

// StartHTTPServer starts the HTTP API server
func (api *APIServer) StartHTTPServer() error {
	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("/health", api.handleHealth)
	mux.HandleFunc("/api/register", api.handleRegister)

	// Protected endpoints (registered device token)
	authMux := http.NewServeMux()
	authMux.HandleFunc("/api/instances", api.handleInstances)
	authMux.HandleFunc("/api/chat", api.handleChat)

	// Apply device auth middleware
	mux.Handle("/api/", api.deviceAuthMiddleware(authMux))

	addr := fmt.Sprintf(":%d", api.config.Port)

	if api.config.TLSEnabled && api.config.TLSCert != "" && api.config.TLSKey != "" {
		log.Printf("HTTPS API Server listening on port %d (TLS enabled)", api.config.Port)
		return http.ListenAndServeTLS(addr, api.config.TLSCert, api.config.TLSKey, mux)
	}

	log.Printf("HTTP API Server listening on port %d", api.config.Port)
	return http.ListenAndServe(addr, mux)
}

// deviceAuthMiddleware validates registered device tokens
func (api *APIServer) deviceAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip register endpoint (handled separately)
		if r.URL.Path == "/api/register" {
			next.ServeHTTP(w, r)
			return
		}

		token, err := ExtractBearerToken(r)
		if err != nil {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}

		device := api.deviceStore.Validate(token)
		if device == nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		api.deviceStore.Touch(token)
		next.ServeHTTP(w, r)
	})
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

// handleRegister handles POST /api/register
// Requires access code, returns device token
func (api *APIServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AccessCode string `json:"accessCode"`
		DeviceName string `json:"deviceName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate access code
	if req.AccessCode != api.config.AccessCode {
		log.Printf("[API] Registration failed: invalid access code from %s", r.RemoteAddr)
		http.Error(w, "invalid access code", http.StatusForbidden)
		return
	}

	if req.DeviceName == "" {
		http.Error(w, "deviceName is required", http.StatusBadRequest)
		return
	}

	// Register device
	device, err := api.deviceStore.Register(req.DeviceName)
	if err != nil {
		log.Printf("[API] Registration error: %v", err)
		http.Error(w, "registration failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":    device.ID,
		"name":  device.Name,
		"token": device.Token,
	})

	log.Printf("[API] Device registered: %s (%s) from %s", device.Name, device.ID, r.RemoteAddr)
}

// handleInstances handles GET /api/instances
func (api *APIServer) handleInstances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	instances := api.bridgeManager.GetInstances()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(instances)
}

// handleChat handles POST /api/chat with SSE streaming
func (api *APIServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var chatReq ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := api.relayManager.ValidateChatRequest(&chatReq); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if api.bridgeManager.GetBridge(chatReq.InstanceID) == nil {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	requestID := generateRequestID()
	log.Printf("Starting chat relay: instance=%s, requestID=%s", chatReq.InstanceID, requestID)

	responseCh := make(chan string)
	errorCh := make(chan error)

	go api.relayManager.RelayChat(chatReq.InstanceID, requestID, chatReq.Messages, responseCh, errorCh)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	for {
		select {
		case delta, ok := <-responseCh:
			if !ok {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			deltaData := map[string]string{"delta": delta}
			dataBytes, _ := json.Marshal(deltaData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()

		case err := <-errorCh:
			errorData := map[string]string{"error": err.Error()}
			dataBytes, _ := json.Marshal(errorData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()
			return

		case <-r.Context().Done():
			return
		}
	}
}
