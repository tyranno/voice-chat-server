package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// NotificationHub 알림 WebSocket 연결 관리
type NotificationHub struct {
	mu      sync.RWMutex
	clients map[string]*websocket.Conn // instanceId → WebSocket conn
}

func NewNotificationHub() *NotificationHub {
	return &NotificationHub{
		clients: make(map[string]*websocket.Conn),
	}
}

func (h *NotificationHub) Register(instanceID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// 기존 연결이 있으면 닫기
	if old, ok := h.clients[instanceID]; ok {
		old.Close()
	}
	h.clients[instanceID] = conn
	log.Printf("[Notify] Client registered: %s", instanceID)
}

func (h *NotificationHub) Unregister(instanceID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.clients[instanceID]; ok && existing == conn {
		delete(h.clients, instanceID)
		log.Printf("[Notify] Client unregistered: %s", instanceID)
	}
}

// Send 알림을 특정 인스턴스에 전송 (instanceID 비어있으면 전체 브로드캐스트)
func (h *NotificationHub) Send(instanceID string, payload []byte) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sent := 0
	if instanceID != "" {
		if conn, ok := h.clients[instanceID]; ok {
			if _, err := conn.Write(payload); err == nil {
				sent++
			}
		}
	} else {
		for _, conn := range h.clients {
			if _, err := conn.Write(payload); err == nil {
				sent++
			}
		}
	}
	return sent
}

// APIServer handles HTTP API requests
type APIServer struct {
	bridgeManager      *BridgeManager
	relayManager       *RelayManager
	config             *Config
	sttProxy           *STTProxy
	notificationHub    *NotificationHub
	conversationStore  *ConversationStore
	apkHandler         *APKHandler
}

// NewAPIServer creates a new API server
func NewAPIServer(bridgeManager *BridgeManager, relayManager *RelayManager, config *Config) *APIServer {
	return &APIServer{
		bridgeManager:     bridgeManager,
		relayManager:      relayManager,
		config:            config,
		sttProxy:          NewSTTProxy("ws://127.0.0.1:2700"),
		notificationHub:   NewNotificationHub(),
		conversationStore: NewConversationStore(config.DataDir),
		apkHandler:        NewAPKHandler(config.DataDir),
	}
}

// StartHTTPServer starts the HTTP API server
func (api *APIServer) StartHTTPServer() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", api.cors(api.handleHealth))
	mux.HandleFunc("/api/instances", api.cors(api.handleInstances))
	mux.HandleFunc("/api/chat", api.cors(api.handleChat))
	mux.HandleFunc("/api/stt/stream", api.sttProxy.Handler())
	mux.Handle("/api/notifications/ws", websocket.Handler(api.handleNotificationWS))
	mux.HandleFunc("/api/notify", api.cors(api.handleNotify))
	mux.HandleFunc("/api/conversations", api.cors(api.handleConversations))
	mux.HandleFunc("/api/conversations/", api.cors(api.handleConversationByID))
	mux.HandleFunc("/api/apk/latest", api.cors(api.apkHandler.HandleLatest))
	mux.HandleFunc("/api/apk/download", api.cors(api.apkHandler.HandleDownload))
	mux.HandleFunc("/api/apk/upload", api.cors(api.apkHandler.HandleUpload))
	mux.HandleFunc("/api/youtube/search", api.cors(api.handleYouTubeSearch))

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

// handleNotificationWS WebSocket 알림 연결 처리
func (api *APIServer) handleNotificationWS(conn *websocket.Conn) {
	remoteAddr := conn.Request().RemoteAddr

	// 클라이언트가 첫 메시지로 instanceId를 보내야 함
	var registerMsg struct {
		InstanceID string `json:"instanceId"`
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := websocket.JSON.Receive(conn, &registerMsg); err != nil || registerMsg.InstanceID == "" {
		log.Printf("[Notify] Registration failed from %s: %v", remoteAddr, err)
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{}) // 타임아웃 해제

	api.notificationHub.Register(registerMsg.InstanceID, conn)
	defer api.notificationHub.Unregister(registerMsg.InstanceID, conn)

	// ping/pong 루프 — 클라이언트가 ping 보내면 pong 응답
	buf := make([]byte, 512)
	for {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("[Notify] Read error (%s): %v", registerMsg.InstanceID, err)
			}
			return
		}
		// ping 메시지 처리
		if n > 0 {
			var msg map[string]string
			if json.Unmarshal(buf[:n], &msg) == nil && msg["type"] == "ping" {
				pong, _ := json.Marshal(map[string]string{"type": "pong"})
				conn.Write(pong)
			}
		}
	}
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

	payload := map[string]string{
		"type":   "notification",
		"title":  req.Title,
		"body":   req.Body,
		"action": req.Action,
	}
	data, _ := json.Marshal(payload)
	sent := api.notificationHub.Send(req.InstanceID, data)

	log.Printf("[Notify] Sent to %d clients (instanceId=%q, title=%q)", sent, req.InstanceID, req.Title)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sent": sent,
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
	conversations := api.conversationStore.ListConversations()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(conversations)
}

// handleCreateConversation handles POST /api/conversations
func (api *APIServer) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		req.Title = "새 대화"
	}

	conversation := api.conversationStore.CreateConversation(req.Title)
	if conversation == nil {
		http.Error(w, "Failed to create conversation", http.StatusInternalServerError)
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
	conversation := api.conversationStore.GetConversation(conversationID)
	if conversation == nil {
		http.Error(w, "Conversation not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(conversation.Messages)
}

// handleSaveMessages handles PUT /api/conversations/{id}/messages
func (api *APIServer) handleSaveMessages(w http.ResponseWriter, r *http.Request, conversationID string) {
	var messages []ConvMessage
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := api.conversationStore.SaveMessages(conversationID, messages); err != nil {
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
	if err := api.conversationStore.DeleteConversation(conversationID); err != nil {
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
