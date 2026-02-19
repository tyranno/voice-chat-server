package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type STTProxy struct {
	voskURL  string
	upgrader websocket.Upgrader
}

func NewSTTProxy(voskURL string) *STTProxy {
	return &STTProxy{
		voskURL: voskURL,
		upgrader: websocket.Upgrader{
			CheckOrigin:    func(r *http.Request) bool { return true },
			ReadBufferSize: 8192, WriteBufferSize: 4096,
		},
	}
}

func (p *STTProxy) Handler() http.HandlerFunc { return p.handleWS }

func (p *STTProxy) handleWS(w http.ResponseWriter, r *http.Request) {
	clientConn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[STT] WebSocket upgrade failed: %v", err)
		return
	}
	defer clientConn.Close()
	remoteAddr := r.RemoteAddr
	log.Printf("[STT] Client connected: %s", remoteAddr)

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	voskConn, _, err := dialer.Dial(p.voskURL, nil)
	if err != nil {
		log.Printf("[STT] Failed to connect to VOSK (%s): %v", p.voskURL, err)
		clientConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","text":"STT 서버 연결 실패"}`))
		return
	}
	defer voskConn.Close()
	log.Printf("[STT] Connected to VOSK for %s", remoteAddr)

	var wg sync.WaitGroup
	wg.Add(2)

	// Client → VOSK
	go func() {
		defer wg.Done()
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("[STT] Client read error: %v", err)
				}
				voskConn.WriteMessage(websocket.TextMessage, []byte(`{"eof":1}`))
				return
			}
			if msgType == websocket.TextMessage {
				voskConn.WriteMessage(websocket.TextMessage, data)
			} else {
				voskConn.WriteMessage(websocket.BinaryMessage, data)
			}
		}
	}()

	// VOSK → Client
	go func() {
		defer wg.Done()
		for {
			_, data, err := voskConn.ReadMessage()
			if err != nil {
				if err != io.EOF && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Printf("[STT] VOSK read error: %v", err)
				}
				return
			}

			// Parse Vosk JSON response
			var voskResp map[string]interface{}
			if err := json.Unmarshal(data, &voskResp); err != nil {
				log.Printf("[STT] VOSK parse error: %v (raw: %s)", err, string(data))
				continue
			}

			var appResp map[string]string

			if partial, ok := voskResp["partial"].(string); ok && partial != "" {
				appResp = map[string]string{"type": "partial", "text": partial}
			} else if text, ok := voskResp["text"].(string); ok && text != "" && text != "인식 중..." && text != "인식 중" {
				appResp = map[string]string{"type": "final", "text": text}
				log.Printf("[STT] Final: %s", text)
			}

			if appResp != nil {
				out, _ := json.Marshal(appResp)
				if err := clientConn.WriteMessage(websocket.TextMessage, out); err != nil {
					log.Printf("[STT] Client write error: %v", err)
					return
				}
			}
		}
	}()

	wg.Wait()
	log.Printf("[STT] Client disconnected: %s", remoteAddr)
}
