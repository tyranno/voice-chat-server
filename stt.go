package main

import (
	"io"
	"log"
	"net/http"
	"net/url"

	"golang.org/x/net/websocket"
)

// STTProxy proxies WebSocket connections from the app to the local VOSK STT server.
// App sends PCM audio binary frames, receives JSON text frames with recognition results.
type STTProxy struct {
	voskURL string // e.g. "ws://127.0.0.1:2700"
}

func NewSTTProxy(voskURL string) *STTProxy {
	return &STTProxy{voskURL: voskURL}
}

// Handler returns an http.Handler for the WebSocket upgrade
func (p *STTProxy) Handler() http.Handler {
	return websocket.Handler(p.handleWS)
}

func (p *STTProxy) handleWS(clientConn *websocket.Conn) {
	clientConn.PayloadType = websocket.BinaryFrame
	remoteAddr := clientConn.Request().RemoteAddr
	log.Printf("[STT] Client connected: %s", remoteAddr)

	// Connect to local VOSK server
	voskURL, _ := url.Parse(p.voskURL)
	origin := "http://localhost/"
	voskConn, err := websocket.Dial(voskURL.String(), "", origin)
	if err != nil {
		log.Printf("[STT] Failed to connect to VOSK server: %v", err)
		clientConn.Close()
		return
	}
	defer voskConn.Close()
	defer clientConn.Close()

	log.Printf("[STT] Connected to VOSK server for client %s", remoteAddr)

	// Client → VOSK (binary audio)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, err := clientConn.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("[STT] Client read error: %v", err)
				}
				return
			}
			if n > 0 {
				_, err = voskConn.Write(buf[:n])
				if err != nil {
					log.Printf("[STT] VOSK write error: %v", err)
					return
				}
			}
		}
	}()

	// VOSK → Client (text results)
	go func() {
		voskConn.PayloadType = websocket.TextFrame
		buf := make([]byte, 4096)
		for {
			n, err := voskConn.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("[STT] VOSK read error: %v", err)
				}
				return
			}
			if n > 0 {
				clientConn.PayloadType = websocket.TextFrame
				_, err = clientConn.Write(buf[:n])
				if err != nil {
					log.Printf("[STT] Client write error: %v", err)
					return
				}
			}
		}
	}()

	<-done
	log.Printf("[STT] Client disconnected: %s", remoteAddr)
}
