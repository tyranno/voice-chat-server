package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// APKHandler handles APK distribution endpoints
type APKHandler struct {
	dataDir string
}

// NewAPKHandler creates a new APK handler
func NewAPKHandler(dataDir string) *APKHandler {
	return &APKHandler{dataDir: dataDir}
}

func (h *APKHandler) apkDir() string {
	return filepath.Join(h.dataDir, "apk")
}

func (h *APKHandler) apkPath() string {
	return filepath.Join(h.apkDir(), "app-debug.apk")
}

func (h *APKHandler) metaPath() string {
	return filepath.Join(h.apkDir(), "meta.json")
}

// HandleLatest GET /api/apk/latest - returns APK metadata
func (h *APKHandler) HandleLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	metaFile := h.metaPath()
	data, err := os.ReadFile(metaFile)
	if err != nil {
		http.Error(w, "No APK available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// HandleDownload GET /api/apk/download - serves the APK file
func (h *APKHandler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apkFile := h.apkPath()
	info, err := os.Stat(apkFile)
	if err != nil {
		http.Error(w, "APK not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", "attachment; filename=voicechat.apk")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))

	f, err := os.Open(apkFile)
	if err != nil {
		http.Error(w, "Failed to open APK", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	io.Copy(w, f)
}

// HandleUpload POST /api/apk/upload - upload new APK with version info
func (h *APKHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	version := r.URL.Query().Get("version")
	if version == "" {
		http.Error(w, "version query param required", http.StatusBadRequest)
		return
	}

	// Validate version format
	if matched, _ := regexp.MatchString(`^\d+\.\d+\.\d+$`, version); !matched {
		http.Error(w, "version must be in X.Y.Z format", http.StatusBadRequest)
		return
	}

	versionCodeStr := r.URL.Query().Get("versionCode")
	versionCode := 1
	if versionCodeStr != "" {
		if vc, err := strconv.Atoi(versionCodeStr); err == nil {
			versionCode = vc
		}
	}

	// Ensure apk directory exists
	if err := os.MkdirAll(h.apkDir(), 0755); err != nil {
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}

	// Save APK file
	apkFile, err := os.Create(h.apkPath())
	if err != nil {
		http.Error(w, "Failed to save APK", http.StatusInternalServerError)
		return
	}
	defer apkFile.Close()

	size, err := io.Copy(apkFile, r.Body)
	if err != nil {
		http.Error(w, "Failed to write APK", http.StatusInternalServerError)
		return
	}

	// Save metadata
	meta := map[string]interface{}{
		"version":     version,
		"versionCode": versionCode,
		"size":        size,
		"downloadUrl": "/api/apk/download",
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(h.metaPath(), metaData, 0644); err != nil {
		http.Error(w, "Failed to save metadata", http.StatusInternalServerError)
		return
	}

	log.Printf("[APK] Uploaded v%s (code=%d, size=%d bytes)", version, versionCode, size)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","version":"%s","size":%d}`, version, size)
}
