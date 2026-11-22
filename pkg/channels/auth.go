package channels

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

type AuthManager struct {
	cfg       *config.NativeConfig
	store     *ClientStore
	storePath string
	mu        sync.RWMutex
	secret    string
	repo      *store.NativeClientRepo

	lastSlowReload time.Time

	storeUnavailable string
}

var ErrPairingUnavailable = errors.New("pairing unavailable")

const DesktopClientID = "desktop-local"

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

func (am *AuthManager) SetStore(repo *store.NativeClientRepo) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.repo = repo
	if repo == nil {
		return
	}

	am.storeUnavailable = ""

	if am.store != nil && len(am.store.Clients) > 0 {
		for id, client := range am.store.Clients {
			data, err := json.Marshal(client)
			if err != nil {
				logger.ErrorCF("native", fmt.Sprintf("SetStore: marshal client %s: %v", id, err), nil)
				continue
			}
			if err := repo.SetClient(id, string(data)); err != nil {
				logger.ErrorCF("native", fmt.Sprintf("SetStore: persist client %s: %v", id, err), nil)
			}
		}
	}

	if err := am.loadStore(); err != nil {
		logger.WarnCF("native", "SetStore: could not reload store from SQLite, keeping current in-memory store", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func (am *AuthManager) SetStoreUnavailable(err error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if err == nil {
		am.storeUnavailable = ""
		return
	}

	am.storeUnavailable = err.Error()
	logger.ErrorCF("native", "Pairing degraded: shared SQLite store is not open, PINs stored in it can neither be minted nor redeemed", map[string]interface{}{
		"error":  am.storeUnavailable,
		"effect": "POST /api/v1/auth/pair and GET /api/v1/auth/pin answer 503 store_unavailable",
	})
}

func (am *AuthManager) StoreUnavailable() (reason string, degraded bool) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	return am.storeUnavailable, am.storeUnavailable != ""
}

func (am *AuthManager) StorageBackend() string {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if am.repo != nil {
		return "sqlite"
	}
	return "json"
}

func (am *AuthManager) pairingUnavailableErrLocked() error {
	return fmt.Errorf("%w: this gateway could not open its SQLite store (%s) — pairing PINs live in that database, so none can be minted or redeemed here until the store is reachable again", ErrPairingUnavailable, am.storeUnavailable)
}

func generateSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Printf("[auth] CRITICAL: crypto/rand failed (%v); using a time-derived client secret fallback", err)
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
	if am.repo != nil {
		clients, err := am.repo.ListClients()
		if err != nil {
			return fmt.Errorf("list clients from sqlite: %w", err)
		}
		store := &ClientStore{
			Clients:     make(map[string]*ClientInfo, len(clients)),
			PendingPINs: make(map[string]*PendingPIN),
		}
		for id, clientJSON := range clients {
			var info ClientInfo
			if err := json.Unmarshal([]byte(clientJSON), &info); err != nil {
				logger.WarnCF("native", fmt.Sprintf("loadStore: unmarshal client %s: %v", id, err), nil)
				continue
			}
			if len(info.SessionKeys) == 0 {
				info.SessionKeys = []string{id}
			}
			info.ClientID = id
			store.Clients[id] = &info
		}

		pinRows, err := am.repo.ListPendingPINs(time.Now().UnixNano())
		if err != nil {
			logger.WarnCF("native", "loadStore: could not list pending PINs from SQLite", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			for pin, blob := range pinRows {
				var pending PendingPIN
				if err := json.Unmarshal([]byte(blob), &pending); err != nil {
					logger.WarnCF("native", fmt.Sprintf("loadStore: unmarshal pending PIN %s: %v", pin, err), nil)
					continue
				}
				store.PendingPINs[pin] = &pending
			}
		}

		store.LastModified = time.Now()
		am.store = store
		return nil
	}

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
			client.SessionKeys = []string{clientID}
		}
		client.ClientID = clientID
	}
	am.cleanupExpired()
	return nil
}

func (am *AuthManager) saveStore() error {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.saveStoreUnlocked()
}

type pendingClientWrite struct {
	id      string
	payload string
}

func planClientWrites(clients map[string]*ClientInfo, stored map[string]string) ([]pendingClientWrite, error) {
	writes := make([]pendingClientWrite, 0, len(clients))

	for id, client := range clients {
		clientJSON, err := json.Marshal(client)
		if err != nil {
			return nil, fmt.Errorf("marshal client %s: %w", id, err)
		}
		if persisted, ok := stored[id]; ok && persisted == string(clientJSON) {
			continue
		}
		writes = append(writes, pendingClientWrite{id: id, payload: string(clientJSON)})
	}

	return writes, nil
}

func (am *AuthManager) saveStoreUnlocked() error {
	am.store.LastModified = time.Now()

	if am.repo != nil {
		existing, err := am.repo.ListClients()
		if err != nil {
			return fmt.Errorf("list clients for cleanup: %w", err)
		}
		for id := range existing {
			if _, ok := am.store.Clients[id]; !ok {
				if err := am.repo.DeleteClient(id); err != nil {
					return fmt.Errorf("delete stale client %s: %w", id, err)
				}
			}
		}
		writes, err := planClientWrites(am.store.Clients, existing)
		if err != nil {
			return err
		}
		for _, write := range writes {
			if err := am.repo.SetClient(write.id, write.payload); err != nil {
				return fmt.Errorf("save client %s: %w", write.id, err)
			}
		}
		return nil
	}

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

const maxPendingPINs = 10

func (am *AuthManager) GeneratePIN(deviceName string) (*PendingPIN, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.storeUnavailable != "" {
		return nil, am.pairingUnavailableErrLocked()
	}

	expiryMinutes := am.cfg.PinExpiryMinutes
	if expiryMinutes <= 0 {
		expiryMinutes = 5
	}

	if am.repo != nil {
		now := time.Now()
		nowNano := now.UnixNano()

		if deleted, err := am.repo.DeleteExpiredPendingPINs(nowNano); err != nil {
			logger.WarnCF("native", "GeneratePIN: DeleteExpiredPendingPINs failed", map[string]interface{}{
				"error": err.Error(),
			})
		} else if deleted > 0 {
			logger.DebugCF("native", fmt.Sprintf("GeneratePIN: purged %d expired pending PINs", deleted), nil)
		}

		count, err := am.repo.CountPendingPINs(nowNano)
		if err != nil {
			logger.WarnCF("native", "GeneratePIN: CountPendingPINs failed", map[string]interface{}{
				"error": err.Error(),
			})
		}
		if count >= maxPendingPINs {
			evictedPin, err := am.repo.EvictOldestPendingPIN(nowNano)
			if err != nil {
				logger.WarnCF("native", "GeneratePIN: EvictOldestPendingPIN failed", map[string]interface{}{
					"error": err.Error(),
				})
			} else if evictedPin != "" {
				var evictedDevice string
				var ageSeconds string
				if p, ok := am.store.PendingPINs[evictedPin]; ok {
					evictedDevice = p.DeviceName
					ageSeconds = time.Since(p.Created).Round(time.Second).String()
					delete(am.store.PendingPINs, evictedPin)
				}
				logger.WarnCF("native", "Pending PIN cap reached, evicted oldest pending PIN", map[string]interface{}{
					"evicted_device": evictedDevice,
					"age_seconds":    ageSeconds,
				})
			}
		}

		pin := generatePIN()
		createdAt := now.UnixNano()
		expiresAt := now.Add(time.Duration(expiryMinutes) * time.Minute).UnixNano()
		pendingJSON, _ := json.Marshal(&PendingPIN{
			PIN:        pin,
			DeviceName: deviceName,
			Created:    now,
			Expires:    now.Add(time.Duration(expiryMinutes) * time.Minute),
		})

		const maxRetries = 20
		for i := 0; i < maxRetries; i++ {
			err := am.repo.InsertPendingPIN(pin, string(pendingJSON), createdAt, expiresAt)
			if err == nil {
				var pending PendingPIN
				json.Unmarshal(pendingJSON, &pending)
				am.store.PendingPINs[pin] = &pending
				return &pending, nil
			}
			if !errors.Is(err, store.ErrDuplicate) {
				logger.ErrorCF("native", "GeneratePIN: InsertPendingPIN failed", map[string]interface{}{
					"error": err.Error(),
				})
				return nil, fmt.Errorf("generate PIN: %w", err)
			}
			pin = generatePIN()
			pendingJSON, _ = json.Marshal(&PendingPIN{
				PIN:        pin,
				DeviceName: deviceName,
				Created:    now,
				Expires:    now.Add(time.Duration(expiryMinutes) * time.Minute),
			})
		}

		return nil, fmt.Errorf("generate PIN: exhausted %d retries due to collisions", maxRetries)
	}

	am.cleanupExpired()

	if len(am.store.PendingPINs) >= maxPendingPINs {
		oldestPIN := ""
		var oldest *PendingPIN
		for pin, pending := range am.store.PendingPINs {
			if oldest == nil || pending.Created.Before(oldest.Created) {
				oldestPIN = pin
				oldest = pending
			}
		}
		if oldest != nil {
			delete(am.store.PendingPINs, oldestPIN)
			logger.WarnCF("native", "Pending PIN cap reached, evicted oldest pending PIN", map[string]interface{}{
				"evicted_device": oldest.DeviceName,
				"age_seconds":    time.Since(oldest.Created).Round(time.Second).String(),
			})
		}
	}

	pin := generatePIN()
	for _, exists := am.store.PendingPINs[pin]; exists; {
		pin = generatePIN()
	}

	now := time.Now()
	pending := &PendingPIN{
		PIN:        pin,
		DeviceName: deviceName,
		Created:    now,
		Expires:    now.Add(time.Duration(expiryMinutes) * time.Minute),
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

	pin = strings.TrimSpace(pin)

	if am.storeUnavailable != "" {
		return nil, "", "", am.pairingUnavailableErrLocked()
	}

	if am.repo != nil {

		pendingJSON, expiresAt, found, err := am.repo.GetPendingPIN(pin)
		if err != nil {
			logger.ErrorCF("native", "PairWithPIN: GetPendingPIN failed", map[string]interface{}{
				"error": err.Error(),
			})
			return nil, "", "", fmt.Errorf("invalid PIN")
		}
		if !found {
			return nil, "", "", fmt.Errorf("invalid PIN")
		}

		var pending PendingPIN
		if err := json.Unmarshal([]byte(pendingJSON), &pending); err != nil {
			logger.WarnCF("native", fmt.Sprintf("PairWithPIN: unmarshal pending PIN: %v", err), nil)
			am.repo.DeletePendingPIN(pin)
			return nil, "", "", fmt.Errorf("invalid PIN")
		}

		if expiresAt <= time.Now().UnixNano() {
			am.repo.DeletePendingPIN(pin)
			return nil, "", "", fmt.Errorf("PIN expired")
		}

		if pending.DeviceName != "" && deviceName != "" && pending.DeviceName != deviceName {
			return nil, "", "", fmt.Errorf("device name mismatch")
		}
		if pending.DeviceName == "" && strings.TrimSpace(deviceName) == "" {
			return nil, "", "", fmt.Errorf("device_name is required")
		}

		if deleted, err := am.repo.DeleteExpiredPendingPINs(time.Now().UnixNano()); err != nil {
			logger.WarnCF("native", "PairWithPIN: DeleteExpiredPendingPINs failed", map[string]interface{}{
				"error": err.Error(),
			})
		} else if deleted > 0 {
			logger.DebugCF("native", fmt.Sprintf("PairWithPIN: purged %d expired pending PINs", deleted), nil)
		}

		maxClients := am.cfg.MaxClients
		if maxClients <= 0 {
			maxClients = 5
		}
		clientCount, err := am.repo.CountClients()
		if err != nil {
			logger.ErrorCF("native", "PairWithPIN: CountClients failed", map[string]interface{}{
				"error": err.Error(),
			})
			return nil, "", "", fmt.Errorf("maximum clients reached")
		}
		if clientCount >= maxClients {
			return nil, "", "", fmt.Errorf("maximum clients reached")
		}

		_, taken, err := am.repo.TakePendingPIN(pin, time.Now().UnixNano())
		if err != nil {
			logger.ErrorCF("native", "PairWithPIN: TakePendingPIN failed", map[string]interface{}{
				"error": err.Error(),
			})
			return nil, "", "", fmt.Errorf("invalid PIN")
		}
		if !taken {
			return nil, "", "", fmt.Errorf("invalid PIN")
		}

		delete(am.store.PendingPINs, pin)

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
			SessionKeys: []string{clientID},
		}

		am.store.Clients[clientID] = client

		if err := am.persistClientLocked(clientID); err != nil {
			delete(am.store.Clients, clientID)
			logger.ErrorCF("native", "Failed to save store after pairing; removing in-memory client", map[string]interface{}{
				"error": err.Error(),
			})
			return nil, "", "", fmt.Errorf("pairing failed: could not persist client")
		}

		return client, token, refreshToken, nil
	}

	if err := am.loadStore(); err != nil {
		logger.WarnCF("native", "Could not reload store before pairing", map[string]interface{}{
			"error": err.Error(),
		})
	}

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

	if pending.DeviceName == "" && strings.TrimSpace(deviceName) == "" {
		return nil, "", "", fmt.Errorf("device_name is required")
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
		SessionKeys: []string{clientID},
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

func (am *AuthManager) RegisterDesktopClient(token, refreshToken string) error {
	if token == "" || refreshToken == "" {
		return fmt.Errorf("desktop token must not be empty")
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	if am.store == nil {
		return fmt.Errorf("client store not initialized")
	}

	client := &ClientInfo{
		ClientID:    DesktopClientID,
		TokenHash:   hashToken(token),
		RefreshHash: hashToken(refreshToken),
		DeviceName:  "Lele Desktop",
		Created:     time.Now(),
		Expires:     time.Now().AddDate(10, 0, 0),
		LastSeen:    time.Now(),
		SessionKeys: []string{DesktopClientID},
	}

	am.store.Clients[DesktopClientID] = client

	if err := am.persistClientLocked(DesktopClientID); err != nil {
		logger.ErrorCF("native", "Failed to save store after registering desktop client", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return nil
}

func (am *AuthManager) ValidateToken(token string) (*ClientInfo, bool) {
	tokenHash := hashToken(token)

	am.mu.RLock()
	client := am.findClientByTokenHash(tokenHash)
	hasRepo := am.repo != nil
	am.mu.RUnlock()

	if client != nil {
		return client, true
	}
	if !hasRepo {
		return nil, false
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	if !am.allowSlowReloadLocked(time.Now()) {
		return nil, false
	}

	if err := am.loadStore(); err != nil {
		logger.WarnCF("native", "ValidateToken: could not reload client store", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, false
	}

	if client := am.findClientByTokenHash(tokenHash); client != nil {
		return client, true
	}
	return nil, false
}

func (am *AuthManager) findClientByTokenHash(hash string) *ClientInfo {
	now := time.Now()
	for _, client := range am.store.Clients {
		if client.TokenHash != hash {
			continue
		}
		if now.After(client.Expires) {
			return nil
		}
		return client
	}
	return nil
}

const slowReloadMinInterval = 250 * time.Millisecond

var errRefreshUnavailable = errors.New("auth store reload suppressed, retry")

func (am *AuthManager) allowSlowReloadLocked(now time.Time) bool {
	if !am.lastSlowReload.IsZero() && now.Sub(am.lastSlowReload) < slowReloadMinInterval {
		return false
	}
	am.lastSlowReload = now
	return true
}

const authReconcileInterval = 15 * time.Second

func (am *AuthManager) reconcileClientsWithStore() ([]string, error) {
	am.mu.RLock()
	repo := am.repo
	am.mu.RUnlock()

	if repo == nil {
		return nil, nil
	}

	rows, err := repo.ListClients()
	if err != nil {
		return nil, fmt.Errorf("list clients for reconciliation: %w", err)
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	var dropped []string
	for id, client := range am.store.Clients {
		if _, ok := rows[id]; ok {
			continue
		}
		delete(am.store.Clients, id)
		dropped = append(dropped, client.ClientID)
	}
	if len(dropped) > 0 {
		logger.WarnCF("native", "auth: dropped clients revoked by another process", map[string]interface{}{
			"count":      len(dropped),
			"client_ids": dropped,
		})
	}
	return dropped, nil
}

const rotationGrace = 60 * time.Second

func (am *AuthManager) RefreshToken(refreshToken string) (*ClientInfo, string, string, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	refreshHash := hashToken(refreshToken)

	clientID, client := am.findClientByRefreshHash(refreshHash)

	reloadSuppressed := false
	if client == nil && am.repo != nil {
		if am.allowSlowReloadLocked(time.Now()) {
			if err := am.loadStore(); err != nil {
				logger.WarnCF("native", "RefreshToken: could not reload client store", map[string]interface{}{
					"error": err.Error(),
				})
			} else {
				clientID, client = am.findClientByRefreshHash(refreshHash)
			}
		} else {
			reloadSuppressed = true
		}
	}

	if client == nil {
		replayID, replayClient, age := am.findClientByPrevRefreshHash(refreshHash)
		if replayClient == nil {
			if reloadSuppressed {
				return nil, "", "", errRefreshUnavailable
			}
			return nil, "", "", fmt.Errorf("invalid refresh token")
		}

		replayed, newToken, newRefreshToken, err := am.rotateClientLocked(replayID, replayClient)
		if err != nil {
			return nil, "", "", err
		}

		replayClient.PrevRefreshHash = ""
		replayClient.PrevTokenHash = ""
		if err := am.persistClientLocked(replayID); err != nil {
			logger.ErrorCF("native", "Failed to persist consumed replay grace", map[string]interface{}{
				"client_id": replayID,
				"error":     err.Error(),
			})
		}

		logger.WarnCF("native", "refresh replay accepted within rotation grace", map[string]interface{}{
			"client_id":   replayID,
			"age_seconds": age.Round(time.Second).String(),
		})
		return replayed, newToken, newRefreshToken, nil
	}

	return am.rotateClientLocked(clientID, client)
}

func (am *AuthManager) findClientByRefreshHash(hash string) (string, *ClientInfo) {
	for id, client := range am.store.Clients {
		if client.RefreshHash == hash {
			return id, client
		}
	}
	return "", nil
}

func (am *AuthManager) findClientByPrevRefreshHash(hash string) (string, *ClientInfo, time.Duration) {
	now := time.Now()
	for id, client := range am.store.Clients {
		if client.PrevRefreshHash == "" || client.PrevRefreshHash != hash {
			continue
		}
		if age := now.Sub(client.RotatedAt); age <= rotationGrace {
			return id, client, age
		}
	}
	return "", nil, 0
}

func (am *AuthManager) rotateClientLocked(clientID string, client *ClientInfo) (*ClientInfo, string, string, error) {
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

	client.PrevTokenHash = client.TokenHash
	client.PrevRefreshHash = client.RefreshHash
	now := time.Now()
	client.RotatedAt = now

	client.TokenHash = hashToken(newToken)
	client.RefreshHash = hashToken(newRefreshToken)
	client.touchLastSeen(now)
	client.Expires = now.AddDate(0, 0, expiryDays)

	if err := am.persistClientLocked(clientID); err != nil {
		logger.ErrorCF("native", "Failed to save store after refresh", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return client, newToken, newRefreshToken, nil
}

func (am *AuthManager) persistClientLocked(clientID string) error {
	if am.repo == nil {
		return am.saveStoreUnlocked()
	}

	client, ok := am.store.Clients[clientID]
	if !ok {
		logger.DebugCF("native", "persistClientLocked: client not in memory, nothing written", map[string]interface{}{
			"client_id": clientID,
		})
		return nil
	}

	data, err := json.Marshal(client)
	if err != nil {
		return fmt.Errorf("marshal client %s: %w", clientID, err)
	}
	if err := am.repo.SetClient(clientID, string(data)); err != nil {
		return fmt.Errorf("save client %s: %w", clientID, err)
	}
	am.store.LastModified = time.Now()
	return nil
}

func (am *AuthManager) UpdateLastSeen(clientID string) {
	am.mu.RLock()
	client, exists := am.store.Clients[clientID]
	am.mu.RUnlock()

	if !exists {
		return
	}

	client.touchLastSeen(time.Now())
}

func (c *ClientInfo) touchLastSeen(t time.Time) {
	c.lastSeenNanos.Store(t.UnixNano())
}

func (c *ClientInfo) effectiveLastSeen() time.Time {
	seen := c.LastSeen
	if nanos := c.lastSeenNanos.Load(); nanos > 0 {
		if hot := time.Unix(0, nanos); hot.After(seen) {
			seen = hot
		}
	}
	return seen
}

func (c *ClientInfo) snapshot() *ClientInfo {
	return &ClientInfo{
		ClientID:        c.ClientID,
		TokenHash:       c.TokenHash,
		RefreshHash:     c.RefreshHash,
		DeviceName:      c.DeviceName,
		Created:         c.Created,
		Expires:         c.Expires,
		LastSeen:        c.effectiveLastSeen(),
		SessionKeys:     append([]string(nil), c.SessionKeys...),
		PrevTokenHash:   c.PrevTokenHash,
		PrevRefreshHash: c.PrevRefreshHash,
		RotatedAt:       c.RotatedAt,
	}
}

func (c *ClientInfo) MarshalJSON() ([]byte, error) {
	type alias ClientInfo

	return json.Marshal(struct {
		*alias
		LastSeen time.Time `json:"last_seen"`
	}{(*alias)(c), c.effectiveLastSeen()})
}

func (am *AuthManager) TrackSessionKey(clientID, sessionKey string) {
	if sessionKey == "" {
		logger.DebugCF("native", "TrackSessionKey: empty session key, skipping", map[string]interface{}{
			"client_id": clientID,
		})
		return
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	client, exists := am.store.Clients[clientID]
	if !exists {
		logger.DebugCF("native", "TrackSessionKey: client not found in store", map[string]interface{}{
			"client_id":   clientID,
			"session_key": sessionKey,
		})
		return
	}

	for _, existing := range client.SessionKeys {
		if existing == sessionKey {
			logger.DebugCF("native", "TrackSessionKey: session key already tracked", map[string]interface{}{
				"client_id":   clientID,
				"session_key": sessionKey,
				"total_keys":  len(client.SessionKeys),
			})
			return
		}
	}

	client.SessionKeys = append(client.SessionKeys, sessionKey)
	logger.InfoCF("native", "TrackSessionKey: new session key tracked", map[string]interface{}{
		"client_id":   clientID,
		"session_key": sessionKey,
		"total_keys":  len(client.SessionKeys),
	})
	if err := am.persistClientLocked(clientID); err != nil {
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

	return client.snapshot(), true
}

func (am *AuthManager) RemoveClient(clientID string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if _, exists := am.store.Clients[clientID]; !exists {
		return fmt.Errorf("client not found")
	}

	delete(am.store.Clients, clientID)

	var err error
	if am.repo != nil {
		err = am.repo.DeleteClient(clientID)
	} else {
		err = am.saveStoreUnlocked()
	}
	if err != nil {
		logger.ErrorCF("native", "Failed to save store after removing client", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return nil
}

func (am *AuthManager) RemoveSessionKey(clientID, sessionKey string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	client, exists := am.store.Clients[clientID]
	if !exists {
		return fmt.Errorf("client not found")
	}

	for i, key := range client.SessionKeys {
		if key == sessionKey {
			client.SessionKeys = append(client.SessionKeys[:i], client.SessionKeys[i+1:]...)
			if err := am.persistClientLocked(clientID); err != nil {
				logger.ErrorCF("native", "Failed to save store after removing session key", map[string]interface{}{
					"client_id":   clientID,
					"session_key": sessionKey,
					"error":       err.Error(),
				})
			}
			return nil
		}
	}

	return fmt.Errorf("session key not found")
}

func (am *AuthManager) ListClients() []*ClientInfo {
	am.mu.RLock()
	defer am.mu.RUnlock()

	clients := make([]*ClientInfo, 0, len(am.store.Clients))
	for _, client := range am.store.Clients {
		clients = append(clients, client.snapshot())
	}
	return clients
}

func (am *AuthManager) GetPendingPINs() []*PendingPIN {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if am.repo != nil {
		pinRows, err := am.repo.ListPendingPINs(time.Now().UnixNano())
		if err != nil {
			logger.WarnCF("native", "GetPendingPINs: ListPendingPINs failed", map[string]interface{}{
				"error": err.Error(),
			})
			return []*PendingPIN{}
		}
		pins := make([]*PendingPIN, 0, len(pinRows))
		for pin, blob := range pinRows {
			var pending PendingPIN
			if err := json.Unmarshal([]byte(blob), &pending); err != nil {
				logger.WarnCF("native", fmt.Sprintf("GetPendingPINs: unmarshal PIN %s: %v", pin, err), nil)
				continue
			}
			pins = append(pins, &pending)
		}
		return pins
	}

	pins := make([]*PendingPIN, 0, len(am.store.PendingPINs))
	for _, pending := range am.store.PendingPINs {
		pins = append(pins, pending)
	}
	return pins
}
