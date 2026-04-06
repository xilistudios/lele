package channels

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
)

type AuthManager struct {
	cfg       *config.NativeConfig
	store     *ClientStore
	storePath string
	mu        sync.RWMutex
	secret    string
}

func NewAuthManager(cfg *config.NativeConfig, leleDir string) (*AuthManager, error) {
	am := &AuthManager{
		cfg:       cfg,
		storePath: filepath.Join(leleDir, "native_clients.json"),
		secret:    generateSecret(),
	}

	if err := am.loadStore(); err != nil {
		logger.WarnCF("native", "Could not load client store, creating new", map[string]interface{}{
			"error": err.Error(),
		})
		am.store = &ClientStore{
			Clients:     make(map[string]*ClientInfo),
			PendingPINs: make(map[string]*PendingPIN),
		}
	}

	return am, nil
}

func generateSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-secret-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func generatePIN() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "123456"
	}
	num := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
	return fmt.Sprintf("%06d", num%1000000)
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-token-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func generateClientID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (am *AuthManager) loadStore() error {
	data, err := os.ReadFile(am.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			am.store = &ClientStore{
				Clients:     make(map[string]*ClientInfo),
				PendingPINs: make(map[string]*PendingPIN),
			}
			return nil
		}
		return err
	}

	var store ClientStore
	if err := json.Unmarshal(data, &store); err != nil {
		return err
	}

	am.store = &store
	for clientID, client := range am.store.Clients {
		if len(client.SessionKeys) == 0 {
			client.SessionKeys = []string{"native:" + clientID}
		}
	}
	am.cleanupExpired()
	return nil
}

func (am *AuthManager) saveStore() error {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.saveStoreUnlocked()
}

func (am *AuthManager) saveStoreUnlocked() error {
	am.store.LastModified = time.Now()
	data, err := json.MarshalIndent(am.store, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(am.storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpPath := am.storePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, am.storePath)
}

func (am *AuthManager) cleanupExpired() {
	now := time.Now()

	for pin, pending := range am.store.PendingPINs {
		if now.After(pending.Expires) {
			delete(am.store.PendingPINs, pin)
		}
	}

	for clientID, client := range am.store.Clients {
		if now.After(client.Expires) {
			delete(am.store.Clients, clientID)
		}
	}
}

func (am *AuthManager) GeneratePIN(deviceName string) (*PendingPIN, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.cleanupExpired()

	if len(am.store.PendingPINs) >= 10 {
		return nil, fmt.Errorf("too many pending PIN requests")
	}

	pin := generatePIN()
	for _, exists := am.store.PendingPINs[pin]; exists; {
		pin = generatePIN()
	}

	expiryMinutes := am.cfg.PinExpiryMinutes
	if expiryMinutes <= 0 {
		expiryMinutes = 5
	}

	pending := &PendingPIN{
		PIN:        pin,
		DeviceName: deviceName,
		Created:    time.Now(),
		Expires:    time.Now().Add(time.Duration(expiryMinutes) * time.Minute),
	}

	am.store.PendingPINs[pin] = pending

	if err := am.saveStoreUnlocked(); err != nil {
		logger.ErrorCF("native", "Failed to save store after generating PIN", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return pending, nil
}

func (am *AuthManager) PairWithPIN(pin, deviceName string) (*ClientInfo, string, string, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if err := am.loadStore(); err != nil {
		logger.WarnCF("native", "Could not reload store before pairing", map[string]interface{}{
			"error": err.Error(),
		})
	}

	pin = strings.TrimSpace(pin)

	pending, exists := am.store.PendingPINs[pin]
	if !exists {
		return nil, "", "", fmt.Errorf("invalid PIN")
	}

	if time.Now().After(pending.Expires) {
		delete(am.store.PendingPINs, pin)
		return nil, "", "", fmt.Errorf("PIN expired")
	}

	if pending.DeviceName != "" && deviceName != "" && pending.DeviceName != deviceName {
		return nil, "", "", fmt.Errorf("device name mismatch")
	}

	am.cleanupExpired()

	maxClients := am.cfg.MaxClients
	if maxClients <= 0 {
		maxClients = 5
	}

	if len(am.store.Clients) >= maxClients {
		return nil, "", "", fmt.Errorf("maximum clients reached")
	}

	clientID := generateClientID()
	token := generateToken()
	refreshToken := generateToken()

	expiryDays := am.cfg.TokenExpiryDays
	if expiryDays <= 0 {
		expiryDays = 30
	}

	finalDeviceName := deviceName
	if finalDeviceName == "" {
		finalDeviceName = pending.DeviceName
	}
	if finalDeviceName == "" {
		finalDeviceName = "Unknown Device"
	}

	client := &ClientInfo{
		ClientID:    clientID,
		TokenHash:   hashToken(token),
		RefreshHash: hashToken(refreshToken),
		DeviceName:  finalDeviceName,
		Created:     time.Now(),
		Expires:     time.Now().AddDate(0, 0, expiryDays),
		LastSeen:    time.Now(),
		SessionKeys: []string{"native:" + clientID},
	}

	am.store.Clients[clientID] = client
	delete(am.store.PendingPINs, pin)

	if err := am.saveStoreUnlocked(); err != nil {
		logger.ErrorCF("native", "Failed to save store after pairing", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return client, token, refreshToken, nil
}

func (am *AuthManager) ValidateToken(token string) (*ClientInfo, bool) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	tokenHash := hashToken(token)

	for _, client := range am.store.Clients {
		if client.TokenHash == tokenHash {
			if time.Now().After(client.Expires) {
				return nil, false
			}
			return client, true
		}
	}

	return nil, false
}

func (am *AuthManager) RefreshToken(refreshToken string) (*ClientInfo, string, string, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	refreshHash := hashToken(refreshToken)

	for clientID, client := range am.store.Clients {
		if client.RefreshHash == refreshHash {
			if time.Now().After(client.Expires) {
				delete(am.store.Clients, clientID)
				return nil, "", "", fmt.Errorf("client expired")
			}

			newToken := generateToken()
			newRefreshToken := generateToken()

			expiryDays := am.cfg.TokenExpiryDays
			if expiryDays <= 0 {
				expiryDays = 30
			}

			client.TokenHash = hashToken(newToken)
			client.RefreshHash = hashToken(newRefreshToken)
			client.LastSeen = time.Now()
			client.Expires = time.Now().AddDate(0, 0, expiryDays)

			if err := am.saveStoreUnlocked(); err != nil {
				logger.ErrorCF("native", "Failed to save store after refresh", map[string]interface{}{
					"error": err.Error(),
				})
			}

			return client, newToken, newRefreshToken, nil
		}
	}

	return nil, "", "", fmt.Errorf("invalid refresh token")
}

func (am *AuthManager) UpdateLastSeen(clientID string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if client, exists := am.store.Clients[clientID]; exists {
		client.LastSeen = time.Now()
	}
}

func (am *AuthManager) TrackSessionKey(clientID, sessionKey string) {
	if sessionKey == "" {
		return
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	client, exists := am.store.Clients[clientID]
	if !exists {
		return
	}

	for _, existing := range client.SessionKeys {
		if existing == sessionKey {
			return
		}
	}

	client.SessionKeys = append(client.SessionKeys, sessionKey)
	if err := am.saveStoreUnlocked(); err != nil {
		logger.ErrorCF("native", "Failed to save store after tracking session", map[string]interface{}{
			"client_id":   clientID,
			"session_key": sessionKey,
			"error":       err.Error(),
		})
	}
}

func (am *AuthManager) GetClient(clientID string) (*ClientInfo, bool) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	client, exists := am.store.Clients[clientID]
	if !exists {
		return nil, false
	}

	copy := *client
	copy.SessionKeys = append([]string(nil), client.SessionKeys...)
	return &copy, true
}

func (am *AuthManager) RemoveClient(clientID string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if _, exists := am.store.Clients[clientID]; !exists {
		return fmt.Errorf("client not found")
	}

	delete(am.store.Clients, clientID)

	if err := am.saveStoreUnlocked(); err != nil {
		logger.ErrorCF("native", "Failed to save store after removing client", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return nil
}

func (am *AuthManager) ListClients() []*ClientInfo {
	am.mu.RLock()
	defer am.mu.RUnlock()

	clients := make([]*ClientInfo, 0, len(am.store.Clients))
	for _, client := range am.store.Clients {
		clients = append(clients, client)
	}
	return clients
}

func (am *AuthManager) GetPendingPINs() []*PendingPIN {
	am.mu.RLock()
	defer am.mu.RUnlock()

	pins := make([]*PendingPIN, 0, len(am.store.PendingPINs))
	for _, pending := range am.store.PendingPINs {
		pins = append(pins, pending)
	}
	return pins
}
