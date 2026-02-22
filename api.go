package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// NewLineScanner reads lines from a reader (for SSE parsing)
type LineScanner struct {
	scanner *bufio.Scanner
}

func NewLineScanner(r io.Reader) *LineScanner {
	return &LineScanner{scanner: bufio.NewScanner(r)}
}

func (s *LineScanner) Scan() bool { return s.scanner.Scan() }
func (s *LineScanner) Text() string { return s.scanner.Text() }

// APIServer handles HTTP API requests
type APIServer struct {
	bridgeManager      *BridgeManager
	relayManager       *RelayManager
	config             *Config
	sttProxy           *STTProxy
	notifyHub          *NotificationHub
	fcmManager         *FcmManager
	conversationStore  *ConversationStore
	apkHandler         *APKHandler
}

// NewAPIServer creates a new API server
func NewAPIServer(bridgeManager *BridgeManager, relayManager *RelayManager, config *Config) *APIServer {
	// Initialize FCM manager
	fcmSAPath := config.FcmServiceAccount
	if fcmSAPath == "" {
		// Default path
		fcmSAPath = "/opt/voicechat/firebase-sa.json"
	}
	fcmMgr := NewFcmManager(config.DataDir, fcmSAPath)

	return &APIServer{
		bridgeManager:     bridgeManager,
		relayManager:      relayManager,
		config:            config,
		sttProxy:          NewSTTProxy("ws://127.0.0.1:2700"),
		notifyHub:         NewNotificationHub(),
		fcmManager:        fcmMgr,
		conversationStore: NewConversationStore(config.DataDir),
		apkHandler:        NewAPKHandler(config.DataDir),
	}
}

// StartHTTPServer starts the HTTP API server
func (api *APIServer) StartHTTPServer() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", api.cors(api.handleRoot))
	mux.HandleFunc("/health", api.cors(api.handleHealth))
	mux.HandleFunc("/api/instances", api.cors(api.handleInstances))
	mux.HandleFunc("/api/chat", api.cors(api.handleChat))
	mux.HandleFunc("/api/stt/stream", api.sttProxy.Handler())
	mux.HandleFunc("/api/notifications/ws", api.notifyHub.HandleWebSocket)
	mux.HandleFunc("/api/notify", api.cors(api.handleNotify))
	mux.HandleFunc("/api/fcm/register", api.cors(api.fcmManager.HandleRegister))
	mux.HandleFunc("/api/fcm/push", api.cors(api.fcmManager.HandleSendPush))
	mux.HandleFunc("/api/conversations", api.cors(api.handleConversations))
	mux.HandleFunc("/api/conversations/", api.cors(api.handleConversationByID))
	mux.HandleFunc("/api/apk/latest", api.cors(api.apkHandler.HandleLatest))
	mux.HandleFunc("/api/apk/download", api.cors(api.apkHandler.HandleDownload))
	mux.HandleFunc("/api/apk/upload", api.cors(api.apkHandler.HandleUpload))
	mux.HandleFunc("/api/youtube/search", api.cors(api.handleYouTubeSearch))
	mux.HandleFunc("/api/youtube/stream", api.cors(api.handleYouTubeStream))
	mux.HandleFunc("/api/youtube/proxy", api.cors(api.handleYouTubeProxy))
	mux.HandleFunc("/api/youtube/hls-proxy", api.cors(api.handleYouTubeHLSProxy))
	mux.HandleFunc("/api/youtube/hls-segment", api.cors(api.handleYouTubeHLSSegment))

	addr := fmt.Sprintf(":%d", api.config.Port)

	if api.config.TLSEnabled && api.config.TLSCert != "" && api.config.TLSKey != "" {
		log.Printf("HTTPS API Server listening on port %d (TLS enabled)", api.config.Port)
		return http.ListenAndServeTLS(addr, api.config.TLSCert, api.config.TLSKey, mux)
	}

	log.Printf("HTTP API Server listening on port %d", api.config.Port)
	return http.ListenAndServe(addr, mux)
}

// cors wraps a handler with CORS headers
func (api *APIServer) cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// handleRoot handles GET / and returns API summary
func (api *APIServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"service": "voicechat-server",
		"status":  "ok",
		"health":  "/health",
		"apis": []string{
			"/api/instances",
			"/api/chat",
			"/api/stt/stream",
			"/api/notifications/ws",
			"/api/notify",
			"/api/fcm/register",
			"/api/fcm/push",
			"/api/conversations",
			"/api/apk/latest",
			"/api/apk/download",
			"/api/apk/upload",
			"/api/youtube/search",
		},
		"timestamp": time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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

	// Add local OpenClaw instance if configured
	if api.config.LocalOpenclawURL != "" {
		localInstance := BridgeConnection{
			ID:          "local",
			Name:        api.config.LocalOpenclawName,
			Status:      "online",
			ConnectedAt: time.Now(),
		}
		// Prepend local instance (always first, always online)
		instances = append([]BridgeConnection{localInstance}, instances...)
	}

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

	// Local OpenClaw instance — direct HTTP proxy
	if chatReq.InstanceID == "local" && api.config.LocalOpenclawURL != "" {
		api.handleLocalChat(w, r, &chatReq)
		return
	}

	if api.bridgeManager.GetBridge(chatReq.InstanceID) == nil {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	requestID := generateRequestID()
	log.Printf("Starting chat relay: instance=%s, requestID=%s", chatReq.InstanceID, requestID)

	responseCh := make(chan string)
	errorCh := make(chan error)
	fileCh := make(chan FileResponseMessage, 8)

	go api.relayManager.RelayChat(chatReq.InstanceID, requestID, chatReq.Messages, "", responseCh, errorCh, fileCh)

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

		case err, ok := <-errorCh:
			if !ok || err == nil {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
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

// handleLocalChat proxies chat to local OpenClaw gateway via OpenAI-compatible API
func (api *APIServer) handleLocalChat(w http.ResponseWriter, r *http.Request, chatReq *ChatRequest) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Build OpenAI-compatible request
	openaiMessages := make([]map[string]string, len(chatReq.Messages))
	for i, msg := range chatReq.Messages {
		openaiMessages[i] = map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		}
	}

	body := map[string]interface{}{
		"model":    "openclaw",
		"stream":   true,
		"user":     "voicechat-app",
		"messages": openaiMessages,
	}
	bodyData, _ := json.Marshal(body)

	url := api.config.LocalOpenclawURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(r.Context(), "POST", url, strings.NewReader(string(bodyData)))
	if err != nil {
		errorData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", errorData)
		flusher.Flush()
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-openclaw-agent-id", "main")
	if api.config.LocalOpenclawToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+api.config.LocalOpenclawToken)
	}

	client := &http.Client{Timeout: 0} // no timeout for streaming
	resp, err := client.Do(httpReq)
	if err != nil {
		errorData, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("OpenClaw error: %v", err)})
		fmt.Fprintf(w, "data: %s\n\n", errorData)
		flusher.Flush()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody := make([]byte, 1024)
		n, _ := resp.Body.Read(respBody)
		errorData, _ := json.Marshal(map[string]string{"error": fmt.Sprintf("OpenClaw HTTP %d: %s", resp.StatusCode, string(respBody[:n]))})
		fmt.Fprintf(w, "data: %s\n\n", errorData)
		flusher.Flush()
		return
	}

	// Stream SSE from OpenClaw to client, converting format
	scanner := NewLineScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var parsed struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			continue
		}
		if len(parsed.Choices) > 0 && parsed.Choices[0].Delta.Content != "" {
			deltaData, _ := json.Marshal(map[string]string{"delta": parsed.Choices[0].Delta.Content})
			fmt.Fprintf(w, "data: %s\n\n", string(deltaData))
			flusher.Flush()
		}
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	log.Printf("Local OpenClaw chat completed")
}

// handleNotify POST /api/notify — Bridge(OpenClaw)가 알림 전송
func (api *APIServer) handleNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instanceId"` // 비어있으면 전체 브로드캐스트
		Title      string `json:"title"`
		Body       string `json:"body"`
		Action     string `json:"action,omitempty"` // 딥링크 등
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Title == "" && req.Body == "" {
		http.Error(w, "title or body required", http.StatusBadRequest)
		return
	}

	// Send via WebSocket hub
	api.notifyHub.SendTo(req.InstanceID, "info", req.Title, req.Body)
	sent := api.notifyHub.ClientCount()

	log.Printf("[Notify] WebSocket sent (clients=%d, instanceId=%q, title=%q)", sent, req.InstanceID, req.Title)

	// FCM fallback: if no WebSocket clients received the notification, try FCM push
	fcmSent := 0
	if sent == 0 && api.fcmManager != nil {
		var fcmErr error
		if req.InstanceID != "" {
			fcmErr = api.fcmManager.SendPushTo(req.InstanceID, req.Title, req.Body)
		} else {
			fcmErr = api.fcmManager.SendPush(req.Title, req.Body)
		}
		if fcmErr != nil {
			log.Printf("[Notify] FCM fallback failed: %v", fcmErr)
		} else {
			fcmSent = 1
			log.Printf("[Notify] FCM fallback sent successfully")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sent":    sent,
		"fcmSent": fcmSent,
	})
}

// handleConversations handles GET /api/conversations (list) and POST /api/conversations (create)
func (api *APIServer) handleConversations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.handleListConversations(w, r)
	case http.MethodPost:
		api.handleCreateConversation(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListConversations handles GET /api/conversations
func (api *APIServer) handleListConversations(w http.ResponseWriter, r *http.Request) {
	conversations, err := api.conversationStore.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(conversations)
}

// handleCreateConversation handles POST /api/conversations
func (api *APIServer) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		req.Title = "새 대화"
	}
	if req.ID == "" {
		req.ID = fmt.Sprintf("%d", time.Now().UnixMilli())
	}

	conversation, err := api.conversationStore.Create(req.ID, req.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(conversation)
}

// handleConversationByID handles requests to /api/conversations/{id} or /api/conversations/{id}/messages
func (api *APIServer) handleConversationByID(w http.ResponseWriter, r *http.Request) {
	// Parse URL path to extract conversation ID and endpoint
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/conversations/") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	// Remove prefix to get the remaining path
	remaining := strings.TrimPrefix(path, "/api/conversations/")
	parts := strings.Split(remaining, "/")
	
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Conversation ID required", http.StatusBadRequest)
		return
	}

	conversationID := parts[0]

	// Check if it's a messages endpoint
	if len(parts) >= 2 && parts[1] == "messages" {
		switch r.Method {
		case http.MethodGet:
			api.handleGetMessages(w, r, conversationID)
		case http.MethodPut:
			api.handleSaveMessages(w, r, conversationID)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Handle conversation-level operations
	switch r.Method {
	case http.MethodDelete:
		api.handleDeleteConversation(w, r, conversationID)
	case http.MethodPatch:
		api.handleUpdateTitle(w, r, conversationID)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetMessages handles GET /api/conversations/{id}/messages
func (api *APIServer) handleGetMessages(w http.ResponseWriter, r *http.Request, conversationID string) {
	messages, err := api.conversationStore.GetMessages(conversationID)
	if err != nil {
		http.Error(w, "Conversation not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// handleSaveMessages handles PUT /api/conversations/{id}/messages
func (api *APIServer) handleSaveMessages(w http.ResponseWriter, r *http.Request, conversationID string) {
	var messages []ConversationMessage
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := api.conversationStore.SetMessages(conversationID, messages); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "success",
	})
}

// handleDeleteConversation handles DELETE /api/conversations/{id}
func (api *APIServer) handleDeleteConversation(w http.ResponseWriter, r *http.Request, conversationID string) {
	if err := api.conversationStore.Delete(conversationID); err != nil {
		http.Error(w, "Failed to delete conversation", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "success",
	})
}

// handleUpdateTitle handles PATCH /api/conversations/{id}
func (api *APIServer) handleUpdateTitle(w http.ResponseWriter, r *http.Request, conversationID string) {
	var req struct {
		Title string `json:"title"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}

	if err := api.conversationStore.UpdateTitle(conversationID, req.Title); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "success",
	})
}
