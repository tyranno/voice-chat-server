package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// STTProxy proxies WebSocket connections from the app to the local VOSK STT server.
type STTProxy struct {
	voskURL string
}

func NewSTTProxy(voskURL string) *STTProxy {
	return &STTProxy{voskURL: voskURL}
}

func (p *STTProxy) Handler() http.Handler {
	return http.HandlerFunc(p.handleHTTP)
}

func (p *STTProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Upgrade client connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[STT] Upgrade error: %v", err)
		return
	}
	defer clientConn.Close()

	remoteAddr := r.RemoteAddr
	log.Printf("[STT] Client connected: %s", remoteAddr)

	// Connect to VOSK server
	voskConn, _, err := websocket.DefaultDialer.Dial(p.voskURL, nil)
	if err != nil {
		log.Printf("[STT] Failed to connect to VOSK server: %v", err)
		return
	}
	defer voskConn.Close()

	log.Printf("[STT] Connected to VOSK server for client %s", remoteAddr)

	// Client → VOSK (binary audio)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("[STT] Client read error: %v", err)
				}
				// Send EOF to VOSK before closing
				voskConn.WriteMessage(websocket.TextMessage, []byte(`{"eof":1}`))
				return
			}
			if msgType == websocket.BinaryMessage {
				if err := voskConn.WriteMessage(websocket.BinaryMessage, data); err != nil {
					log.Printf("[STT] VOSK write error: %v", err)
					return
				}
			} else if msgType == websocket.TextMessage {
				if err := voskConn.WriteMessage(websocket.TextMessage, data); err != nil {
					log.Printf("[STT] VOSK write text error: %v", err)
					return
				}
			}
		}
	}()

	// VOSK → Client (text results)
	go func() {
		for {
			_, data, err := voskConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("[STT] VOSK read error: %v", err)
				}
				return
			}
			if err := clientConn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("[STT] Client write error: %v", err)
				return
			}
		}
	}()

	<-done
	log.Printf("[STT] Client disconnected: %s", remoteAddr)
}
