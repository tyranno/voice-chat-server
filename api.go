package main

import (
	"crypto/rand"
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
	"strings"
	"time"
)

// APIServer handles HTTP API requests
type APIServer struct {
	bridgeManager *BridgeManager
	relayManager  *RelayManager
	config        *Config
	sttProxy      *STTProxy
}

// NewAPIServer creates a new API server
func NewAPIServer(bridgeManager *BridgeManager, relayManager *RelayManager, config *Config) *APIServer {
	return &APIServer{
		bridgeManager: bridgeManager,
		relayManager:  relayManager,
		config:        config,
		sttProxy:      NewSTTProxy("ws://127.0.0.1:2700"),
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
	mux.Handle("/api/stt/stream", api.sttProxy.Handler())
	mux.HandleFunc("/api/files/upload", api.cors(api.handleFileUpload))
	mux.HandleFunc("/api/files/list", api.cors(api.handleFileList))
	mux.HandleFunc("/api/files/", api.cors(api.handleFileDownload))

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
	if r.Method != http.MethodGet {
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

	downloadURL := fmt.Sprintf("/api/files/%s/%s", fileID, url.PathEscape(filename))

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
