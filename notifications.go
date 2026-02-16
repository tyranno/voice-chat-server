package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// NotificationHub manages connected notification clients
type NotificationHub struct {
	mu      sync.RWMutex
	clients map[*NotificationConn]bool
}

type NotificationConn struct {
	conn       *websocket.Conn
	instanceID string
	send       chan []byte
}

type NotificationMessage struct {
	Type             string `json:"type"`
	ID               string `json:"id,omitempty"`
	NotificationType string `json:"notificationType,omitempty"` // info, success, warning, error
	Title            string `json:"title,omitempty"`
	Message          string `json:"message,omitempty"`
	Timestamp        int64  `json:"timestamp,omitempty"`
}

func NewNotificationHub() *NotificationHub {
	return &NotificationHub{
		clients: make(map[*NotificationConn]bool),
	}
}

// Broadcast sends a notification to all connected clients
func (h *NotificationHub) Broadcast(notifType, title, message string) {
	msg := NotificationMessage{
		Type:             "notification",
		ID:               time.Now().Format("20060102150405.000"),
		NotificationType: notifType,
		Title:            title,
		Message:          message,
		Timestamp:        time.Now().UnixMilli(),
	}
	data, _ := json.Marshal(msg)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			// Client buffer full, skip
		}
	}
}

// SendTo sends a notification to clients connected with a specific instanceID
func (h *NotificationHub) SendTo(instanceID, notifType, title, message string) {
	msg := NotificationMessage{
		Type:             "notification",
		ID:               time.Now().Format("20060102150405.000"),
		NotificationType: notifType,
		Title:            title,
		Message:          message,
		Timestamp:        time.Now().UnixMilli(),
	}
	data, _ := json.Marshal(msg)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if client.instanceID == instanceID || instanceID == "" {
			select {
			case client.send <- data:
			default:
			}
		}
	}
}

// ClientCount returns the number of connected clients
func (h *NotificationHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *NotificationHub) addClient(c *NotificationConn) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
	log.Printf("[Notifications] Client connected (total: %d)", h.ClientCount())
}

func (h *NotificationHub) removeClient(c *NotificationConn) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	log.Printf("[Notifications] Client disconnected (total: %d)", h.ClientCount())
}

// HandleWebSocket upgrades HTTP to WebSocket for notification streaming
func (h *NotificationHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Notifications] Upgrade error: %v", err)
		return
	}

	client := &NotificationConn{
		conn: conn,
		send: make(chan []byte, 64),
	}

	h.addClient(client)

	// Writer goroutine
	go func() {
		defer conn.Close()
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case msg, ok := <-client.send:
				if !ok {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-pingTicker.C:
				ping := NotificationMessage{Type: "ping", Timestamp: time.Now().UnixMilli()}
				data, _ := json.Marshal(ping)
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			}
		}
	}()

	// Reader goroutine (handles identify + pong)
	go func() {
		defer func() {
			h.removeClient(client)
			close(client.send)
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]interface{}
			if json.Unmarshal(message, &msg) == nil {
				if msg["type"] == "identify" {
					if id, ok := msg["instanceId"].(string); ok {
						client.instanceID = id
						log.Printf("[Notifications] Client identified: %s", id)
					}
				}
			}
		}
	}()
}

// REST endpoint to send notifications from bridge/openclaw
func (h *NotificationHub) HandleSendNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instanceId"`
		Type       string `json:"type"`
		Title      string `json:"title"`
		Message    string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "info"
	}

	h.SendTo(req.InstanceID, req.Type, req.Title, req.Message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"clients": h.ClientCount(),
	})
}
