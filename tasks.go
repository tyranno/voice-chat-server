package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// TaskRequest is the incoming POST body for /api/task
type TaskRequest struct {
	InstanceID        string `json:"instanceId"`
	Mode              string `json:"mode"` // "ralph"
	Prompt            string `json:"prompt"`
	MaxIterations     int    `json:"maxIterations,omitempty"`
	CompletionPromise string `json:"completionPromise,omitempty"`
}

// TaskCancelRequest is the incoming POST body for /api/task/cancel
type TaskCancelRequest struct {
	InstanceID string `json:"instanceId"`
	TaskID     string `json:"taskId"`
}

// TaskEvent is what the server emits via SSE to the app
type TaskEvent struct {
	Type      string         `json:"type"` // progress | log | done | error | started
	TaskID    string         `json:"taskId,omitempty"`
	Iteration int            `json:"iteration,omitempty"`
	Message   string         `json:"message,omitempty"`
	Progress  int            `json:"progress,omitempty"`
	Line      string         `json:"line,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Artifacts []TaskArtifact `json:"artifacts,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// generateTaskID generates a unique task id
func generateTaskID() string {
	return fmt.Sprintf("task_%d", time.Now().UnixNano())
}

// RelayTask sends a task_start to the bridge and streams progress/log/done/error events back
func (rm *RelayManager) RelayTask(bridgeID, taskID string, msg TaskStartMessage, eventCh chan<- TaskEvent, fileCh chan<- FileResponseMessage) {
	defer close(eventCh)
	defer close(fileCh)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("RelayTask panic recovered: %v", r)
		}
	}()

	bridge := rm.bridgeManager.GetBridge(bridgeID)
	if bridge == nil {
		eventCh <- TaskEvent{Type: "error", TaskID: taskID, Error: fmt.Sprintf("bridge not found: %s", bridgeID)}
		return
	}

	tch := bridge.RegisterTask(taskID)
	defer bridge.UnregisterTask(taskID)

	if err := rm.bridgeManager.SendTaskStart(bridgeID, msg); err != nil {
		eventCh <- TaskEvent{Type: "error", TaskID: taskID, Error: fmt.Sprintf("send task_start failed: %v", err)}
		return
	}

	log.Printf("Task started: bridge=%s taskID=%s mode=%s", bridgeID, taskID, msg.Mode)
	eventCh <- TaskEvent{Type: "started", TaskID: taskID}

	// Tasks can be long-running; cap at 30 minutes
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()

	for {
		select {
		case prog, ok := <-tch.ProgressCh:
			if !ok {
				eventCh <- TaskEvent{Type: "error", TaskID: taskID, Error: "bridge disconnected"}
				return
			}
			eventCh <- TaskEvent{Type: "progress", TaskID: prog.TaskID, Iteration: prog.Iteration, Message: prog.Message, Progress: prog.Progress}

		case logL, ok := <-tch.LogCh:
			if !ok {
				continue
			}
			eventCh <- TaskEvent{Type: "log", TaskID: logL.TaskID, Line: logL.Line}

		case done, ok := <-tch.DoneCh:
			if !ok {
				eventCh <- TaskEvent{Type: "error", TaskID: taskID, Error: "bridge disconnected before done"}
				return
			}
			eventCh <- TaskEvent{Type: "done", TaskID: done.TaskID, Summary: done.Summary, Artifacts: done.Artifacts, Iteration: done.Iterations}
			return

		case errMsg, ok := <-tch.ErrorCh:
			if !ok {
				eventCh <- TaskEvent{Type: "error", TaskID: taskID, Error: "bridge disconnected"}
				return
			}
			eventCh <- TaskEvent{Type: "error", TaskID: errMsg.TaskID, Error: errMsg.Error}
			return

		case fmsg, ok := <-tch.FileCh:
			if !ok {
				continue
			}
			select {
			case fileCh <- fmsg:
			default:
			}

		case <-deadline.C:
			eventCh <- TaskEvent{Type: "error", TaskID: taskID, Error: "task deadline exceeded (30 minutes)"}
			// Try cancel cleanly
			_ = rm.bridgeManager.SendTaskCancel(bridgeID, taskID)
			return
		}
	}
}

// handleTask handles POST /api/task with SSE
func (api *APIServer) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.InstanceID == "" {
		http.Error(w, "instanceId required", http.StatusBadRequest)
		return
	}
	if req.Mode == "" {
		req.Mode = "ralph"
	}
	if req.Prompt == "" {
		http.Error(w, "prompt required", http.StatusBadRequest)
		return
	}
	if req.MaxIterations <= 0 {
		req.MaxIterations = 30
	}
	if req.CompletionPromise == "" {
		req.CompletionPromise = "DONE"
	}

	if api.bridgeManager.GetBridge(req.InstanceID) == nil {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	taskID := generateTaskID()
	startMsg := TaskStartMessage{
		Type:              MsgTypeTaskStart,
		TaskID:            taskID,
		Mode:              req.Mode,
		Prompt:            req.Prompt,
		MaxIterations:     req.MaxIterations,
		CompletionPromise: req.CompletionPromise,
		User:              "voicechat-app",
	}

	log.Printf("Task relay starting: instance=%s taskID=%s prompt=%.40q", req.InstanceID, taskID, req.Prompt)

	eventCh := make(chan TaskEvent, 50)
	fileCh := make(chan FileResponseMessage, 8)

	go api.relayManager.RelayTask(req.InstanceID, taskID, startMsg, eventCh, fileCh)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send taskID immediately so client can map state
	initEvt, _ := json.Marshal(map[string]string{"type": "init", "taskId": taskID})
	fmt.Fprintf(w, "data: %s\n\n", initEvt)
	flusher.Flush()

	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			if evt.Type == "done" || evt.Type == "error" {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}

		case fmsg, ok := <-fileCh:
			if !ok {
				continue
			}
			data, _ := json.Marshal(map[string]interface{}{
				"type":     "file",
				"taskId":   taskID,
				"filename": fmsg.Filename,
				"url":      fmsg.URL,
				"size":     fmsg.Size,
				"mimeType": fmsg.MimeType,
			})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-r.Context().Done():
			// Client disconnected - try cancel
			_ = api.bridgeManager.SendTaskCancel(req.InstanceID, taskID)
			return
		}
	}
}

// handleTaskCancel handles POST /api/task/cancel
func (api *APIServer) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req TaskCancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.InstanceID == "" || req.TaskID == "" {
		http.Error(w, "instanceId and taskId required", http.StatusBadRequest)
		return
	}
	if api.bridgeManager.GetBridge(req.InstanceID) == nil {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}
	if err := api.bridgeManager.SendTaskCancel(req.InstanceID, req.TaskID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancel-sent"})
}
