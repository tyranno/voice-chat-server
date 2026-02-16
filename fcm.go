package main

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// FcmManager manages FCM tokens and sends push notifications via FCM v1 API
type FcmManager struct {
	mu          sync.RWMutex
	tokens      map[string]string // instanceId -> fcm token
	dataDir     string
	projectID   string
	clientEmail string
	privateKey  *rsa.PrivateKey
	tokenURI    string

	// cached access token
	accessToken    string
	tokenExpiresAt time.Time
	tokenMu        sync.Mutex
}

type serviceAccountKey struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

func NewFcmManager(dataDir, saKeyPath string) *FcmManager {
	fm := &FcmManager{
		tokens:  make(map[string]string),
		dataDir: dataDir,
	}
	fm.loadTokens()

	if saKeyPath != "" {
		if err := fm.loadServiceAccount(saKeyPath); err != nil {
			log.Printf("[FCM] Service account load error: %v", err)
		} else {
			log.Printf("[FCM] Service account loaded: %s (project: %s)", fm.clientEmail, fm.projectID)
		}
	}
	return fm
}

func (fm *FcmManager) loadServiceAccount(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sa serviceAccountKey
	if err := json.Unmarshal(data, &sa); err != nil {
		return err
	}

	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return fmt.Errorf("failed to decode PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("not an RSA key")
	}

	fm.projectID = sa.ProjectID
	fm.clientEmail = sa.ClientEmail
	fm.privateKey = rsaKey
	fm.tokenURI = sa.TokenURI
	if fm.tokenURI == "" {
		fm.tokenURI = "https://oauth2.googleapis.com/token"
	}
	return nil
}

func (fm *FcmManager) tokensPath() string {
	return filepath.Join(fm.dataDir, "fcm_tokens.json")
}

func (fm *FcmManager) loadTokens() {
	data, err := os.ReadFile(fm.tokensPath())
	if err != nil {
		return
	}
	json.Unmarshal(data, &fm.tokens)
	log.Printf("[FCM] Loaded %d tokens", len(fm.tokens))
}

func (fm *FcmManager) saveTokens() {
	data, _ := json.MarshalIndent(fm.tokens, "", "  ")
	os.MkdirAll(fm.dataDir, 0755)
	os.WriteFile(fm.tokensPath(), data, 0644)
}

func (fm *FcmManager) RegisterToken(instanceID, token string) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	if instanceID == "" {
		instanceID = "default"
	}
	fm.tokens[instanceID] = token
	fm.saveTokens()
	log.Printf("[FCM] Token registered for instance: %s", instanceID)
}

// getAccessToken returns a valid OAuth2 access token, refreshing if needed
func (fm *FcmManager) getAccessToken() (string, error) {
	fm.tokenMu.Lock()
	defer fm.tokenMu.Unlock()

	if fm.accessToken != "" && time.Now().Before(fm.tokenExpiresAt) {
		return fm.accessToken, nil
	}

	if fm.privateKey == nil {
		return "", fmt.Errorf("no service account key loaded")
	}

	// Create JWT
	now := time.Now()
	header := base64URLEncode([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := fmt.Sprintf(`{"iss":"%s","scope":"https://www.googleapis.com/auth/firebase.messaging","aud":"%s","iat":%d,"exp":%d}`,
		fm.clientEmail, fm.tokenURI, now.Unix(), now.Add(time.Hour).Unix())
	claimsEnc := base64URLEncode([]byte(claims))

	signingInput := header + "." + claimsEnc
	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, fm.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}
	jwt := signingInput + "." + base64URLEncode(sig)

	// Exchange JWT for access token
	body := "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Ajwt-bearer&assertion=" + jwt
	resp, err := http.Post(fm.tokenURI, "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	fm.accessToken = tokenResp.AccessToken
	fm.tokenExpiresAt = now.Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)
	log.Printf("[FCM] Access token refreshed, expires in %ds", tokenResp.ExpiresIn)
	return fm.accessToken, nil
}

func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}

// SendPush sends a push notification to all registered devices
func (fm *FcmManager) SendPush(title, message string) error {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	var lastErr error
	for instanceID, token := range fm.tokens {
		err := fm.sendToToken(token, title, message)
		if err != nil {
			log.Printf("[FCM] Send failed for %s: %v", instanceID, err)
			lastErr = err
		} else {
			log.Printf("[FCM] Push sent to %s", instanceID)
		}
	}
	return lastErr
}

func (fm *FcmManager) SendPushTo(instanceID, title, message string) error {
	fm.mu.RLock()
	defer fm.mu.RUnlock()

	token, ok := fm.tokens[instanceID]
	if !ok {
		token, ok = fm.tokens["default"]
		if !ok {
			// Try first available token
			for _, t := range fm.tokens {
				token = t
				ok = true
				break
			}
		}
	}
	if !ok {
		return fmt.Errorf("no FCM token available")
	}
	return fm.sendToToken(token, title, message)
}

func (fm *FcmManager) sendToToken(token, title, message string) error {
	accessToken, err := fm.getAccessToken()
	if err != nil {
		return fmt.Errorf("access token error: %v", err)
	}

	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", fm.projectID)

	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"token": token,
			"notification": map[string]string{
				"title": title,
				"body":  message,
			},
			"data": map[string]string{
				"title":   title,
				"message": message,
			},
			"android": map[string]interface{}{
				"priority": "high",
			},
		},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("FCM v1 API error %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[FCM] Response: %s", string(respBody))
	return nil
}

// HTTP Handlers

func (fm *FcmManager) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Token      string `json:"token"`
		InstanceID string `json:"instanceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Token == "" {
		http.Error(w, "Missing token", http.StatusBadRequest)
		return
	}
	fm.RegisterToken(req.InstanceID, req.Token)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (fm *FcmManager) HandleSendPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		InstanceID string `json:"instanceId"`
		Title      string `json:"title"`
		Message    string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var err error
	if req.InstanceID != "" {
		err = fm.SendPushTo(req.InstanceID, req.Title, req.Message)
	} else {
		err = fm.SendPush(req.Title, req.Message)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
