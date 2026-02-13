package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// RegisteredDevice represents an app device that registered with the server
type RegisteredDevice struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Token      string    `json:"token"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// DeviceStore manages registered app devices (JSON file backed)
type DeviceStore struct {
	devices  map[string]*RegisteredDevice // key: token
	filePath string
	mu       sync.RWMutex
}

func NewDeviceStore(filePath string) *DeviceStore {
	ds := &DeviceStore{
		devices:  make(map[string]*RegisteredDevice),
		filePath: filePath,
	}
	ds.load()
	return ds
}

// Register creates a new device and returns its token
func (ds *DeviceStore) Register(name string) (*RegisteredDevice, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	device := &RegisteredDevice{
		ID:         generateDeviceID(),
		Name:       name,
		Token:      token,
		CreatedAt:  time.Now(),
		LastSeenAt: time.Now(),
	}

	ds.devices[token] = device
	ds.save()

	log.Printf("[DeviceStore] Registered device: %s (%s)", device.Name, device.ID)
	return device, nil
}

// Validate checks if a token belongs to a registered device
func (ds *DeviceStore) Validate(token string) *RegisteredDevice {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	device, exists := ds.devices[token]
	if !exists {
		return nil
	}
	return device
}

// Touch updates the last seen time for a device
func (ds *DeviceStore) Touch(token string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if device, exists := ds.devices[token]; exists {
		device.LastSeenAt = time.Now()
		// Don't save on every touch to reduce I/O
	}
}

// Remove deletes a registered device
func (ds *DeviceStore) Remove(deviceID string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	for token, device := range ds.devices {
		if device.ID == deviceID {
			delete(ds.devices, token)
			ds.save()
			log.Printf("[DeviceStore] Removed device: %s (%s)", device.Name, device.ID)
			return true
		}
	}
	return false
}

// List returns all registered devices
func (ds *DeviceStore) List() []RegisteredDevice {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	list := make([]RegisteredDevice, 0, len(ds.devices))
	for _, d := range ds.devices {
		list = append(list, *d)
	}
	return list
}

func (ds *DeviceStore) load() {
	data, err := os.ReadFile(ds.filePath)
	if err != nil {
		log.Printf("[DeviceStore] No existing data: %v", err)
		return
	}

	var devices []RegisteredDevice
	if err := json.Unmarshal(data, &devices); err != nil {
		log.Printf("[DeviceStore] Failed to parse: %v", err)
		return
	}

	for i := range devices {
		ds.devices[devices[i].Token] = &devices[i]
	}
	log.Printf("[DeviceStore] Loaded %d devices", len(ds.devices))
}

func (ds *DeviceStore) save() {
	list := make([]RegisteredDevice, 0, len(ds.devices))
	for _, d := range ds.devices {
		list = append(list, *d)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		log.Printf("[DeviceStore] Failed to marshal: %v", err)
		return
	}

	if err := os.WriteFile(ds.filePath, data, 0644); err != nil {
		log.Printf("[DeviceStore] Failed to save: %v", err)
	}
}

func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func generateDeviceID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return fmt.Sprintf("dev_%s", hex.EncodeToString(bytes))
}
