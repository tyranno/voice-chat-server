package main

import (
	"os"
	"strconv"
)

// Config holds the server configuration
type Config struct {
	Port        int    // HTTP server port
	BridgePort  int    // TCP bridge server port
	AuthToken   string // Token for app authentication
	BridgeToken string // Token for bridge authentication
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	config := &Config{
		Port:        8080,
		BridgePort:  9090,
		AuthToken:   "default-auth-token",
		BridgeToken: "default-bridge-token",
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

	if authToken := os.Getenv("AUTH_TOKEN"); authToken != "" {
		config.AuthToken = authToken
	}

	if bridgeToken := os.Getenv("BRIDGE_TOKEN"); bridgeToken != "" {
		config.BridgeToken = bridgeToken
	}

	return config
}