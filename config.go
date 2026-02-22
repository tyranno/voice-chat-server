package main

import (
	"os"
	"strconv"
)

// Config holds the server configuration
type Config struct {
	Port             int    // HTTP server port
	BridgePort       int    // TCP bridge server port
	BridgeToken      string // Token for bridge authentication
	DataDir          string // Directory for persistent data (devices.json etc)
	TLSEnabled       bool   // Enable HTTPS
	TLSCert          string // Path to TLS certificate
	TLSKey           string // Path to TLS private key
	GoogleTTSAPIKey   string // Google Cloud TTS API key
	FcmServiceAccount string // Firebase service account JSON path
	LocalOpenclawURL  string // Local OpenClaw gateway URL (e.g. http://localhost:18789)
	LocalOpenclawToken string // Bearer token for local OpenClaw
	LocalOpenclawName  string // Display name for local instance
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	config := &Config{
		Port:        8080,
		BridgePort:  9090,
		BridgeToken: "default-bridge-token",
		DataDir:     "/opt/voicechat/data",
	}

	if port := os.Getenv("PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Port = p
		}
	}

	if bridgePort := os.Getenv("BRIDGE_PORT"); bridgePort != "" {
		if p, err := strconv.Atoi(bridgePort); err == nil {
			config.BridgePort = p
		}
	}

	if dataDir := os.Getenv("DATA_DIR"); dataDir != "" {
		config.DataDir = dataDir
	}

	if bridgeToken := os.Getenv("BRIDGE_TOKEN"); bridgeToken != "" {
		config.BridgeToken = bridgeToken
	}

	// TLS settings
	if tlsEnabled := os.Getenv("TLS_ENABLED"); tlsEnabled == "true" || tlsEnabled == "1" {
		config.TLSEnabled = true
	}
	if tlsCert := os.Getenv("TLS_CERT"); tlsCert != "" {
		config.TLSCert = tlsCert
	}
	if tlsKey := os.Getenv("TLS_KEY"); tlsKey != "" {
		config.TLSKey = tlsKey
	}

	if ttsKey := os.Getenv("GOOGLE_TTS_API_KEY"); ttsKey != "" {
		config.GoogleTTSAPIKey = ttsKey
	}

	if fcmSA := os.Getenv("FCM_SERVICE_ACCOUNT"); fcmSA != "" {
		config.FcmServiceAccount = fcmSA
	}

	// Local OpenClaw gateway (runs on same server)
	if url := os.Getenv("LOCAL_OPENCLAW_URL"); url != "" {
		config.LocalOpenclawURL = url
	}
	if token := os.Getenv("LOCAL_OPENCLAW_TOKEN"); token != "" {
		config.LocalOpenclawToken = token
	}
	if name := os.Getenv("LOCAL_OPENCLAW_NAME"); name != "" {
		config.LocalOpenclawName = name
	} else if config.LocalOpenclawURL != "" {
		config.LocalOpenclawName = "서버 (GCP)"
	}

	return config
}