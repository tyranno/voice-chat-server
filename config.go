package main

import (
	"os"
	"strconv"
)

// Config holds the server configuration
type Config struct {
	Port        int    // HTTP server port
	BridgePort  int    // TCP bridge server port
	BridgeToken string // Token for bridge authentication
	DataDir     string // Directory for persistent data (devices.json etc)
	TLSEnabled  bool   // Enable HTTPS
	TLSCert     string // Path to TLS certificate
	TLSKey      string // Path to TLS private key
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

	return config
}