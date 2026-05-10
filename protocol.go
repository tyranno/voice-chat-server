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
	MsgTypeFileResponse = "file_response"

	// Phase 3 - Ralph autonomous task messages
	MsgTypeTaskStart    = "task_start"    // Server -> Bridge: start a Ralph task
	MsgTypeTaskProgress = "task_progress" // Bridge -> Server: iteration progress
	MsgTypeTaskLog      = "task_log"      // Bridge -> Server: raw log line (optional)
	MsgTypeTaskDone     = "task_done"     // Bridge -> Server: task completed with summary
	MsgTypeTaskError    = "task_error"    // Bridge -> Server: task failed
	MsgTypeTaskCancel   = "task_cancel"   // Server -> Bridge: cancel an in-flight task
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
	User      string        `json:"user,omitempty"`
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

// FileResponseMessage carries a file attachment from bridge to app
type FileResponseMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Filename  string `json:"filename"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mimeType,omitempty"`
}

// TaskStartMessage: server -> bridge - kick off Ralph autonomous task
type TaskStartMessage struct {
	Type              string `json:"type"`
	TaskID            string `json:"taskId"`
	Mode              string `json:"mode"` // "ralph" | future modes
	Prompt            string `json:"prompt"`
	MaxIterations     int    `json:"maxIterations,omitempty"`
	CompletionPromise string `json:"completionPromise,omitempty"`
	User              string `json:"user,omitempty"`
}

// TaskProgressMessage: bridge -> server - iteration progress notification
type TaskProgressMessage struct {
	Type      string `json:"type"`
	TaskID    string `json:"taskId"`
	Iteration int    `json:"iteration"`
	Message   string `json:"message,omitempty"`
	Progress  int    `json:"progress,omitempty"` // 0-100, optional estimate
}

// TaskLogMessage: bridge -> server - raw stream line (optional, for debug)
type TaskLogMessage struct {
	Type   string `json:"type"`
	TaskID string `json:"taskId"`
	Line   string `json:"line"`
}

// TaskArtifact: file or other output from a task
type TaskArtifact struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind string `json:"kind,omitempty"` // "file" | "text"
}

// TaskDoneMessage: bridge -> server - task completed
type TaskDoneMessage struct {
	Type       string         `json:"type"`
	TaskID     string         `json:"taskId"`
	Summary    string         `json:"summary,omitempty"`
	Artifacts  []TaskArtifact `json:"artifacts,omitempty"`
	Iterations int            `json:"iterations,omitempty"`
}

// TaskErrorMessage: bridge -> server - task failed
type TaskErrorMessage struct {
	Type   string `json:"type"`
	TaskID string `json:"taskId"`
	Error  string `json:"error"`
}

// TaskCancelMessage: server -> bridge - cancel an in-flight task
type TaskCancelMessage struct {
	Type   string `json:"type"`
	TaskID string `json:"taskId"`
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