package main

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func main() {
	// Load configuration
	config := LoadConfig()

	log.Printf("Starting Voice Chat Server...")
	log.Printf("HTTP Port: %d", config.Port)
	log.Printf("Bridge Port: %d", config.BridgePort)

	// Create bridge manager
	bridgeManager := NewBridgeManager(config)

	// Create relay manager
	relayManager := NewRelayManager(bridgeManager, config)

	// Create API server
	apiServer := NewAPIServer(bridgeManager, relayManager, config)

	// Use WaitGroup to manage both servers
	var wg sync.WaitGroup

	// Start TCP Bridge Server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := bridgeManager.StartTCPServer(); err != nil {
			log.Fatalf("TCP server failed: %v", err)
		}
	}()

	// Start HTTP API Server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := apiServer.StartHTTPServer(); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("Voice Chat Server started successfully")
	log.Printf("HTTP API available at http://localhost:%d", config.Port)
	log.Printf("TCP Bridge server listening on port %d", config.BridgePort)

	// Wait for shutdown signal
	<-sigChan
	log.Printf("Received shutdown signal, stopping servers...")

	// Note: In a production environment, you might want to implement
	// proper graceful shutdown by closing listeners and waiting for
	// existing connections to finish. For this implementation, we'll
	// let the OS handle cleanup when the process exits.

	log.Printf("Servers stopped")
}