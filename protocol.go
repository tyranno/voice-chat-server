package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
)

// Message types
const (
	MsgTypeRegister     = "register"
	MsgTypeHeartbeat    = "heartbeat"
	MsgTypeChatRequest  = "chat_request"
	MsgTypeChatResponse = "chat_response"
	MsgTypeChatError    = "chat_error"
)

// Base message structure
type Message struct {
	Type string `json:"type"`
}

// Register message from bridge
type RegisterMessage struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// Heartbeat message
type HeartbeatMessage struct {
	Type string `json:"type"`
}

// Chat message structure
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat request from server to bridge
type ChatRequestMessage struct {
	Type      string        `json:"type"`
	RequestID string        `json:"requestId"`
	Messages  []ChatMessage `json:"messages"`
}

// Chat response from bridge to server
type ChatResponseMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Delta     string `json:"delta"`
	Done      bool   `json:"done"`
}

// Chat error message
type ChatErrorMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Error     string `json:"error"`
}

// SendMessage sends a JSON message over TCP with 4-byte length header
func SendMessage(conn net.Conn, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// Write 4-byte length header (big-endian)
	length := uint32(len(data))
	if err := binary.Write(conn, binary.BigEndian, length); err != nil {
		return err
	}

	// Write JSON data
	_, err = conn.Write(data)
	return err
}

// ReadMessage reads a JSON message from TCP with 4-byte length header
func ReadMessage(conn net.Conn) ([]byte, error) {
	// Read 4-byte length header
	var length uint32
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	// Read JSON data
	data := make([]byte, length)
	_, err := io.ReadFull(conn, data)
	return data, err
}