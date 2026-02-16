package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ConversationMeta holds metadata for a conversation
type ConversationMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	MessageCount int `json:"messageCount"`
}

// ConversationMessage is a single chat message
type ConversationMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// ConversationStore manages conversations on disk
type ConversationStore struct {
	baseDir string
	mu      sync.RWMutex
}

func NewConversationStore(dataDir string) *ConversationStore {
	dir := filepath.Join(dataDir, "conversations")
	os.MkdirAll(dir, 0755)
	return &ConversationStore{baseDir: dir}
}

func (s *ConversationStore) convDir(id string) string {
	return filepath.Join(s.baseDir, id)
}

func (s *ConversationStore) metaPath(id string) string {
	return filepath.Join(s.convDir(id), "meta.json")
}

func (s *ConversationStore) messagesPath(id string) string {
	return filepath.Join(s.convDir(id), "messages.json")
}

// List returns all conversations sorted by updatedAt desc
func (s *ConversationStore) List() ([]ConversationMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return []ConversationMeta{}, nil
	}

	var convs []ConversationMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMeta(e.Name())
		if err != nil {
			continue
		}
		convs = append(convs, meta)
	}

	sort.Slice(convs, func(i, j int) bool {
		return convs[i].UpdatedAt > convs[j].UpdatedAt
	})

	return convs, nil
}

// Create creates a new conversation
func (s *ConversationStore) Create(id, title string) (ConversationMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.convDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ConversationMeta{}, err
	}

	now := time.Now().UnixMilli()
	meta := ConversationMeta{
		ID:        id,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.writeMeta(meta); err != nil {
		return ConversationMeta{}, err
	}

	// Initialize empty messages
	if err := s.writeMessages(id, []ConversationMessage{}); err != nil {
		return ConversationMeta{}, err
	}

	return meta, nil
}

// GetMessages returns all messages for a conversation
func (s *ConversationStore) GetMessages(id string) ([]ConversationMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.readMessages(id)
}

// AppendMessages adds messages and updates metadata
func (s *ConversationStore) AppendMessages(id string, msgs []ConversationMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, _ := s.readMessages(id)
	existing = append(existing, msgs...)

	if err := s.writeMessages(id, existing); err != nil {
		return err
	}

	// Update meta
	meta, err := s.readMeta(id)
	if err != nil {
		return err
	}
	meta.UpdatedAt = time.Now().UnixMilli()
	meta.MessageCount = len(existing)

	// Derive title from first user message if still default
	if meta.Title == "새 대화" || meta.Title == "" {
		for _, m := range existing {
			if m.Role == "user" && m.Content != "" {
				title := m.Content
				if len([]rune(title)) > 30 {
					title = string([]rune(title)[:30]) + "…"
				}
				meta.Title = title
				break
			}
		}
	}

	return s.writeMeta(meta)
}

// SetMessages replaces all messages for a conversation
func (s *ConversationStore) SetMessages(id string, msgs []ConversationMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.writeMessages(id, msgs); err != nil {
		return err
	}

	meta, err := s.readMeta(id)
	if err != nil {
		return err
	}
	meta.UpdatedAt = time.Now().UnixMilli()
	meta.MessageCount = len(msgs)

	if meta.Title == "새 대화" || meta.Title == "" {
		for _, m := range msgs {
			if m.Role == "user" && m.Content != "" {
				title := m.Content
				if len([]rune(title)) > 30 {
					title = string([]rune(title)[:30]) + "…"
				}
				meta.Title = title
				break
			}
		}
	}

	return s.writeMeta(meta)
}

// Delete removes a conversation
func (s *ConversationStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.convDir(id)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("conversation not found")
	}
	return os.RemoveAll(dir)
}

// UpdateTitle updates a conversation's title
func (s *ConversationStore) UpdateTitle(id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readMeta(id)
	if err != nil {
		return err
	}
	meta.Title = title
	return s.writeMeta(meta)
}

// --- internal helpers ---

func (s *ConversationStore) readMeta(id string) (ConversationMeta, error) {
	data, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return ConversationMeta{}, err
	}
	var meta ConversationMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return ConversationMeta{}, err
	}
	return meta, nil
}

func (s *ConversationStore) writeMeta(meta ConversationMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(meta.ID), data, 0644)
}

func (s *ConversationStore) readMessages(id string) ([]ConversationMessage, error) {
	data, err := os.ReadFile(s.messagesPath(id))
	if err != nil {
		return []ConversationMessage{}, nil
	}
	var msgs []ConversationMessage
	if err := json.Unmarshal(data, &msgs); err != nil {
		return []ConversationMessage{}, nil
	}
	return msgs, nil
}

func (s *ConversationStore) writeMessages(id string, msgs []ConversationMessage) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	return os.WriteFile(s.messagesPath(id), data, 0644)
}

func init() {
	_ = log.Println // suppress unused import
}
