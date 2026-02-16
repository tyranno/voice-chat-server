package main

import (
	"crypto/rand"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// APIServer handles HTTP API requests
type APIServer struct {
	bridgeManager *BridgeManager
	relayManager  *RelayManager
	config        *Config
	sttProxy      *STTProxy
	convStore     *ConversationStore
	notifHub      *NotificationHub
	fcmManager    *FcmManager
}

// NewAPIServer creates a new API server
func NewAPIServer(bridgeManager *BridgeManager, relayManager *RelayManager, config *Config) *APIServer {
	return &APIServer{
		bridgeManager: bridgeManager,
		relayManager:  relayManager,
		config:        config,
		sttProxy:      NewSTTProxy("ws://127.0.0.1:2700"),
		convStore:     NewConversationStore(config.DataDir),
		notifHub:      NewNotificationHub(),
		fcmManager:    NewFcmManager(config.DataDir, config.FcmServiceAccount),
	}
}

// StartHTTPServer starts the HTTP API server
func (api *APIServer) StartHTTPServer() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", api.cors(api.handleHealth))
	mux.HandleFunc("/api/instances", api.cors(api.handleInstances))
	mux.HandleFunc("/api/chat", api.cors(api.handleChat))
	mux.HandleFunc("/api/apk/latest", api.cors(api.handleApkLatest))
	mux.HandleFunc("/api/apk/download", api.cors(api.handleApkDownload))
	mux.HandleFunc("/api/apk/upload", api.cors(api.handleApkUpload))
	mux.HandleFunc("/api/tts", api.cors(api.handleTTS))
	mux.Handle("/api/stt/stream", api.sttProxy.Handler())
	mux.HandleFunc("/api/files/upload", api.cors(api.handleFileUpload))
	mux.HandleFunc("/api/files/list", api.cors(api.handleFileList))
	mux.HandleFunc("/api/files/", api.cors(api.handleFileDownload))

	// Conversation management
	mux.HandleFunc("/api/conversations", api.cors(api.handleConversations))
	mux.HandleFunc("/api/conversations/", api.cors(api.handleConversationAction))

	// Notifications (WebSocket + REST)
	mux.HandleFunc("/api/ws/notifications", api.notifHub.HandleWebSocket)
	mux.HandleFunc("/api/notifications/send", api.cors(api.notifHub.HandleSendNotification))

	// YouTube search proxy
	mux.HandleFunc("/api/youtube/search", api.cors(api.handleYouTubeSearch))

	// FCM push notifications
	mux.HandleFunc("/api/fcm/register", api.cors(api.fcmManager.HandleRegister))
	mux.HandleFunc("/api/fcm/send", api.cors(api.fcmManager.HandleSendPush))

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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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

// handleTTS proxies TTS requests to Google Cloud Text-to-Speech API
func (api *APIServer) handleTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if api.config.GoogleTTSAPIKey == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "TTS not configured"})
		return
	}

	var req struct {
		Text  string  `json:"text"`
		Lang  string  `json:"lang"`
		Voice string  `json:"voice"`
		Rate  float64 `json:"rate"`
	}

	if r.Method == http.MethodGet {
		// GET: read from query params
		q := r.URL.Query()
		req.Text = q.Get("text")
		req.Lang = q.Get("lang")
		req.Voice = q.Get("voice")
		if rateStr := q.Get("rate"); rateStr != "" {
			if v, err := strconv.ParseFloat(rateStr, 64); err == nil {
				req.Rate = v
			}
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	}

	if req.Text == "" {
		http.Error(w, "Missing text", http.StatusBadRequest)
		return
	}
	if req.Lang == "" {
		req.Lang = "ko-KR"
	}
	if req.Voice == "" {
		req.Voice = "ko-KR-Neural2-A"
	}
	if req.Rate <= 0 || req.Rate > 4.0 {
		req.Rate = 1.0
	}

	// Build Google TTS API request
	gReq := map[string]interface{}{
		"input": map[string]string{"text": req.Text},
		"voice": map[string]string{"languageCode": req.Lang, "name": req.Voice},
		"audioConfig": map[string]interface{}{
			"audioEncoding": "MP3",
			"speakingRate":  req.Rate,
			"pitch":         0.0,
		},
	}
	body, _ := json.Marshal(gReq)

	apiURL := fmt.Sprintf("https://texttospeech.googleapis.com/v1/text:synthesize?key=%s", api.config.GoogleTTSAPIKey)
	resp, err := http.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[TTS] Google API error: %v", err)
		http.Error(w, "TTS API error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[TTS] Google API HTTP %d: %s", resp.StatusCode, string(respBody))
		http.Error(w, "TTS API error", http.StatusBadGateway)
		return
	}

	var gResp struct {
		AudioContent string `json:"audioContent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gResp); err != nil {
		log.Printf("[TTS] Failed to decode response: %v", err)
		http.Error(w, "TTS decode error", http.StatusInternalServerError)
		return
	}

	audioBytes, err := base64.StdEncoding.DecodeString(gResp.AudioContent)
	if err != nil {
		log.Printf("[TTS] Failed to decode audio: %v", err)
		http.Error(w, "Audio decode error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(audioBytes)))
	w.Write(audioBytes)
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
	// Derive OpenClaw user from conversationId for session separation
	user := "voicechat-app"
	if chatReq.ConversationID != "" {
		user = "vc-" + chatReq.ConversationID
	}
	log.Printf("Starting chat relay: instance=%s, requestID=%s, conversation=%s", chatReq.InstanceID, requestID, chatReq.ConversationID)

	responseCh := make(chan string)
	errorCh := make(chan error)
	fileCh := make(chan FileResponseMessage, 100)

	go api.relayManager.RelayChat(chatReq.InstanceID, requestID, chatReq.Messages, user, responseCh, errorCh, fileCh)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Phase 1: Stream deltas
	streaming := true
	for streaming {
		select {
		case delta, ok := <-responseCh:
			if !ok {
				streaming = false
				continue
			}
			deltaData := map[string]string{"delta": delta}
			dataBytes, _ := json.Marshal(deltaData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()

		case err, ok := <-errorCh:
			if !ok || err == nil {
				streaming = false
				continue
			}
			errorData := map[string]string{"error": err.Error()}
			dataBytes, _ := json.Marshal(errorData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return

		case fileMsg := <-fileCh:
			fileData := map[string]interface{}{
				"file": map[string]interface{}{
					"url":      fileMsg.URL,
					"filename": fileMsg.Filename,
					"size":     fileMsg.Size,
				},
			}
			dataBytes, _ := json.Marshal(fileData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}

	// Phase 2: Drain file events after streaming completes
	drainTimeout := time.NewTimer(30 * time.Second)
	defer drainTimeout.Stop()
	for {
		select {
		case fileMsg, ok := <-fileCh:
			if !ok {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			fileData := map[string]interface{}{
				"file": map[string]interface{}{
					"url":      fileMsg.URL,
					"filename": fileMsg.Filename,
					"size":     fileMsg.Size,
				},
			}
			dataBytes, _ := json.Marshal(fileData)
			fmt.Fprintf(w, "data: %s\n\n", string(dataBytes))
			flusher.Flush()
		case <-drainTimeout.C:
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		case <-r.Context().Done():
			return
		}
	}
}

// handleApkLatest returns metadata about the latest APK
func (api *APIServer) handleApkLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apkDir := filepath.Join(api.config.DataDir, "apk")
	versionFile := filepath.Join(apkDir, "version.json")

	data, err := os.ReadFile(versionFile)
	if err != nil {
		http.Error(w, "No APK available", http.StatusNotFound)
		return
	}

	// Parse to validate and add download URL
	var meta map[string]interface{}
	if err := json.Unmarshal(data, &meta); err != nil {
		http.Error(w, "Invalid version info", http.StatusInternalServerError)
		return
	}

	// Check APK file exists
	apkPath := filepath.Join(apkDir, "app-debug.apk")
	info, err := os.Stat(apkPath)
	if err != nil {
		http.Error(w, "APK file not found", http.StatusNotFound)
		return
	}
	meta["size"] = info.Size()
	meta["downloadUrl"] = "/api/apk/download"

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

// handleApkDownload serves the APK file
func (api *APIServer) handleApkDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apkPath := filepath.Join(api.config.DataDir, "apk", "app-debug.apk")
	if _, err := os.Stat(apkPath); err != nil {
		http.Error(w, "APK not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", "attachment; filename=voicechat.apk")
	http.ServeFile(w, r, apkPath)
}

// handleApkUpload handles POST /api/apk/upload
func (api *APIServer) handleApkUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	const maxApkSize = 200 << 20 // 200MB
	r.Body = http.MaxBytesReader(w, r.Body, maxApkSize)
	if err := r.ParseMultipartForm(maxApkSize); err != nil {
		http.Error(w, "File too large (max 200MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, _, err := r.FormFile("apk")
	if err != nil {
		http.Error(w, "Missing 'apk' file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	version := r.FormValue("version")
	versionCode := r.FormValue("versionCode")
	if version == "" || versionCode == "" {
		http.Error(w, "Missing 'version' or 'versionCode' form field", http.StatusBadRequest)
		return
	}

	apkDir := filepath.Join(api.config.DataDir, "apk")
	os.MkdirAll(apkDir, 0755)

	apkPath := filepath.Join(apkDir, "app-debug.apk")
	dst, err := os.Create(apkPath)
	if err != nil {
		http.Error(w, "Failed to save APK", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Failed to write APK", http.StatusInternalServerError)
		return
	}

	vc, _ := strconv.Atoi(versionCode)
	meta := map[string]interface{}{
		"version":     version,
		"versionCode": vc,
		"updatedAt":   time.Now().UTC().Format(time.RFC3339),
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(filepath.Join(apkDir, "version.json"), metaBytes, 0644)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"size":    written,
		"version": version,
	})
}

const maxUploadSize = 50 << 20 // 50MB

func (api *APIServer) filesDir() string {
	return filepath.Join(api.config.DataDir, "files")
}

// handleFileUpload handles POST /api/files/upload
func (api *APIServer) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "File too large (max 50MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Generate unique ID
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	fileID := hex.EncodeToString(idBytes)

	// Create directory for this file
	dir := filepath.Join(api.filesDir(), fileID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("Failed to create file dir: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Save file with original filename
	filename := header.Filename
	dst, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		log.Printf("Failed to create file: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		log.Printf("Failed to write file: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Save metadata
	meta := map[string]interface{}{
		"id":         fileID,
		"filename":   filename,
		"size":       written,
		"uploadedAt": time.Now().UTC().Format(time.RFC3339),
	}
	metaBytes, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(dir, "meta.json"), metaBytes, 0644)

	// Build full download URL
	scheme := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	downloadURL := fmt.Sprintf("%s://%s/api/files/%s/%s", scheme, r.Host, fileID, url.PathEscape(filename))

	log.Printf("File uploaded: id=%s, name=%s, size=%d", fileID, filename, written)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          fileID,
		"filename":    filename,
		"size":        written,
		"downloadUrl": downloadURL,
	})
}

// handleFileDownload handles GET /api/files/:id/:filename
func (api *APIServer) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /api/files/{id}/{filename}
	path := strings.TrimPrefix(r.URL.Path, "/api/files/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	fileID := parts[0]
	filename, _ := url.PathUnescape(parts[1])

	// Sanitize to prevent directory traversal
	if strings.Contains(fileID, "..") || strings.Contains(filename, "..") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(api.filesDir(), fileID, filename)
	if _, err := os.Stat(filePath); err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Set content type based on extension
	ext := filepath.Ext(filename)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	// Use RFC 5987 encoding for filename to support Korean/Unicode
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(filename)))

	http.ServeFile(w, r, filePath)
}

// handleFileList handles GET /api/files/list
func (api *APIServer) handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries, err := os.ReadDir(api.filesDir())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	var files []map[string]interface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(api.filesDir(), entry.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta map[string]interface{}
		if json.Unmarshal(data, &meta) == nil {
			files = append(files, meta)
		}
	}

	if files == nil {
		files = []map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// === Conversation API ===

// handleConversations handles GET (list) and POST (create) on /api/conversations
func (api *APIServer) handleConversations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		convs, err := api.convStore.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(convs)

	case http.MethodPost:
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			req.Title = "새 대화"
		}
		if req.Title == "" {
			req.Title = "새 대화"
		}
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		conv, err := api.convStore.Create(id, req.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(conv)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleConversationAction handles /api/conversations/:id and /api/conversations/:id/messages
func (api *APIServer) handleConversationAction(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/conversations/{id} or /api/conversations/{id}/messages
	path := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Missing conversation ID", http.StatusBadRequest)
		return
	}
	convID := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
	}

	switch {
	case subPath == "messages" && r.Method == http.MethodGet:
		// GET /api/conversations/:id/messages
		msgs, err := api.convStore.GetMessages(convID)
		if err != nil {
			http.Error(w, "Conversation not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)

	case subPath == "messages" && r.Method == http.MethodPut:
		// PUT /api/conversations/:id/messages — replace all messages
		var msgs []ConversationMessage
		if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if err := api.convStore.SetMessages(convID, msgs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	case subPath == "" && r.Method == http.MethodDelete:
		// DELETE /api/conversations/:id
		if err := api.convStore.Delete(convID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	case subPath == "" && r.Method == http.MethodPatch:
		// PATCH /api/conversations/:id — update title
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if err := api.convStore.UpdateTitle(convID, req.Title); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}
