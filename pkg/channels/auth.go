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
	repo      *store.NativeClientRepo // SQLite store (nil = use JSON file). When set, both clients and pending PINs live in the DB.
}

// DesktopClientID is the fixed client ID for the built-in trusted client used
// by the desktop app when running in desktop mode. It is exempt from the
// MaxClients limit and never expires while the gateway runs.
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

// SetStore configures SQLite persistence for native clients. When set,
// both clients and pending pairing PINs are read/written through the
// repository instead of the JSON file.
func (am *AuthManager) SetStore(repo *store.NativeClientRepo) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.repo = repo
	if repo == nil {
		return
	}

	// Migration-on-first-set: if the in-memory store was loaded from JSON,
	// persist it to SQLite so subsequent loads come from the DB.
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

	// Reload from SQLite to pick up all clients, including those that
	// were persisted in a previous server run and are not present in the
	// JSON file (which is stale when SQLite is the active backend because
	// saveStoreUnlocked skips the JSON write when am.repo != nil).
	if err := am.loadStore(); err != nil {
		logger.WarnCF("native", "SetStore: could not reload store from SQLite, keeping current in-memory store", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func generateSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read failing is effectively impossible on
		// supported platforms, but a time-derived fallback is far
		// weaker than a random secret: say so loudly rather than
		// silently downgrading every client credential.
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
	// SQLite path
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
			store.Clients[id] = &info
		}

		// Load pending PINs from SQLite (the authoritative source).
		// JSON merge is removed (D6): PINs from native_clients.json are
		// abandoned — they may already have been redeemed (F-REPLAY) and
		// the CLI now writes PINs to the shared DB via SetStore.
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

	// JSON file path
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
	}
	am.cleanupExpired()
	return nil
}

func (am *AuthManager) saveStore() error {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.saveStoreUnlocked()
}

// pendingClientWrite is one client row that must be persisted because its
// serialised payload differs from the blob already stored for that id.
type pendingClientWrite struct {
	id      string
	payload string
}

// planClientWrites marshals every in-memory client and returns only the rows
// that actually need writing.
//
// Background: saveStoreUnlocked used to rewrite EVERY client row on EVERY
// save. TrackSessionKey triggers a save on each new session key, i.e. from the
// WebUI hot path, and each NativeClientRepo.SetClient is its own transaction
// against a single-connection SQLite pool — so one new session key cost one
// SELECT plus one write transaction per paired client, all while holding
// AuthManager.mu.
//
// Comparing the marshalled payload against the blob already in the table
// (read by the caller's ListClients inside the same locked section) removes
// the redundant writes without changing persistence semantics: a row whose
// bytes are identical would be updated to the exact same value (SetClient's
// ON CONFLICT only sets the client column, created_at is preserved), so
// skipping it is a no-op on the final DB contents. Rows that are new (id
// absent from stored) are always written, and the ids returned are a subset of
// the ids written before — the set of rows in the table is unchanged.
//
// Remaining cost left alone (out of scope: no transactional/bulk API exists on
// store.NativeClientRepo): the writes that do happen are still one transaction
// each, and the SELECT still reads every client blob. Batching them would need
// a new repo method, e.g. SetClients(map[string]string) in a single tx.
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

	// SQLite path — delete removed clients, then upsert the ones that changed.
	if am.repo != nil {
		// A single SELECT gives us both the row set (stale rows must be
		// deleted) and the persisted payloads (unchanged rows must not be
		// rewritten). See planClientWrites.
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

	// JSON file path
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

// maxPendingPINs caps how many pairing PINs can be pending simultaneously.
//
// The cap exists to bound memory and pairing-slot exhaustion, not to enforce
// a request rate limit: /auth/pin sits behind withAuth, so only authenticated
// callers (e.g. the WebUI settings page) can mint PINs. Because of that, the
// cap is enforced with purge-then-evict semantics: expired PINs are removed
// first, and if the store is still full the OLDEST pending PIN is evicted
// (FIFO) rather than failing the new pairing request. A stale-but-unexpired
// PIN holds a slot for its entire expiry window otherwise, so plain rejection
// lets ~10 abandoned requests block all legitimate pairings for up to
// PinExpiryMinutes (GW-M8).
const maxPendingPINs = 10

// GeneratePIN creates a new 6-digit PIN for device pairing. The PIN expires
// after the configured PinExpiryMinutes (default 5). Only up to maxPendingPINs
// concurrent pending PINs are allowed: expired PINs are purged first, and if
// the cap is still reached the oldest pending PIN is evicted to make room.
//
// Security: this method MUST be called from an authenticated context (e.g.
// the WebUI settings page behind withAuth). Exposing it to unauthenticated
// callers defeats the out-of-band property of the PIN — the issuer's identity
// is what makes the PIN meaningful as a pairing credential.
//
// The deviceName parameter is optional but recommended: when provided, it is
// stored with the pending PIN so PairWithPIN can verify the redeeming device
// matches. When empty, PairWithPIN will require the caller to supply one at
// redemption time.
//
// In SQLite mode the single-use guarantee comes from DELETE … RETURNING
// in TakePendingPIN (PairWithPIN), not from the sync.RWMutex which only
// protects intra-process.
func (am *AuthManager) GeneratePIN(deviceName string) (*PendingPIN, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	expiryMinutes := am.cfg.PinExpiryMinutes
	if expiryMinutes <= 0 {
		expiryMinutes = 5
	}

	if am.repo != nil {
		// SQLite path — purge-then-evict against the DB.
		now := time.Now()
		nowNano := now.UnixNano()

		// Step 1: purge expired PINs.
		if deleted, err := am.repo.DeleteExpiredPendingPINs(nowNano); err != nil {
			logger.WarnCF("native", "GeneratePIN: DeleteExpiredPendingPINs failed", map[string]interface{}{
				"error": err.Error(),
			})
		} else if deleted > 0 {
			logger.DebugCF("native", fmt.Sprintf("GeneratePIN: purged %d expired pending PINs", deleted), nil)
		}

		// Step 2: cap check + evict oldest if needed.
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
				// Log with same fields as the JSON path for observability.
				// We don't have the evicted PendingPIN struct here (the
				// DB only stores the blob), so we log the pin and remove
				// it from the in-memory map if present.
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

		// Step 3: generate PIN with collision retry against the DB.
		// Limit retries to avoid a potential infinite loop in an auth
		// path (R11). With 10⁶ possible PINs and ≤10 rows the collision
		// probability is negligible, but the limit is a safety net.
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
				// Also keep in-memory map in sync for callers like
				// GetPendingPINs that read the map before next loadStore.
				var pending PendingPIN
				json.Unmarshal(pendingJSON, &pending)
				am.store.PendingPINs[pin] = &pending
				return &pending, nil
			}
			// Distinguish UNIQUE constraint violation (collision ⇒ retry)
			// from other DB errors (fatal ⇒ return immediately).
			if !errors.Is(err, store.ErrDuplicate) {
				logger.ErrorCF("native", "GeneratePIN: InsertPendingPIN failed", map[string]interface{}{
					"error": err.Error(),
				})
				return nil, fmt.Errorf("generate PIN: %w", err)
			}
			// Collision — regenerate.
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

	// JSON file path — unchanged.
	am.cleanupExpired()

	// Purge-then-evict: cleanupExpired already removed expired PINs, but
	// non-expired entries can still fill every slot (e.g. abandoned pairing
	// requests). Evict the oldest one so a new pairing always succeeds
	// instead of being blocked until the stale entries expire (GW-M8).
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

	if am.repo != nil {
		// SQLite path — validate-then-atomic-take (D4).
		// Single-use guarantee: DELETE … RETURNING in TakePendingPIN
		// ensures exactly one winner across concurrent processes.
		// The sync.RWMutex only protects intra-process; the DB
		// transaction is what makes the redeem atomic.

		// Step 1: fetch the pending PIN.
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

		// Step 2: expired ⇒ delete + error (same text as the JSON path).
		// Use the DB column (expiresAt) rather than the deserialized
		// blob: direct SQL mutations (e.g. test backdoors) must be
		// authoritative for the TTL policy.
		if expiresAt <= time.Now().UnixNano() {
			am.repo.DeletePendingPIN(pin)
			return nil, "", "", fmt.Errorf("PIN expired")
		}

		// Step 3: device_name checks — return WITHOUT deleting the
		// PIN so it remains redeemable (same semantics as JSON path).
		if pending.DeviceName != "" && deviceName != "" && pending.DeviceName != deviceName {
			return nil, "", "", fmt.Errorf("device name mismatch")
		}
		if pending.DeviceName == "" && strings.TrimSpace(deviceName) == "" {
			return nil, "", "", fmt.Errorf("device_name is required")
		}

		// Opportunistic GC: purge expired PIN rows (not related to the
		// MaxClients count checked below, which is a different table).
		if deleted, err := am.repo.DeleteExpiredPendingPINs(time.Now().UnixNano()); err != nil {
			logger.WarnCF("native", "PairWithPIN: DeleteExpiredPendingPINs failed", map[string]interface{}{
				"error": err.Error(),
			})
		} else if deleted > 0 {
			logger.DebugCF("native", fmt.Sprintf("PairWithPIN: purged %d expired pending PINs", deleted), nil)
		}

		// Step 4: MaxClients — count from the DB (the in-memory map
		// is stale across processes, E5).
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

		// Step 5: atomic take — DELETE … RETURNING.
		// If another process redeemed this PIN between our Get and
		// our Take, found will be false → "invalid PIN".
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

		// Cosmetic: remove from the in-memory map so
		// GetPendingPINs doesn't show a stale entry until next
		// loadStore. The DB is the source of truth (D5).
		delete(am.store.PendingPINs, pin)

		// Step 6: create client + persist.
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

		if err := am.saveStoreUnlocked(); err != nil {
			// Fail closed: remove the in-memory client so no phantom
			// credential survives until the next restart. The PIN is
			// already consumed (TakePendingPIN succeeded) — that is
			// intentional: better to require a fresh PIN than to emit
			// a credential that cannot survive a gateway restart.
			// Atomicity of PIN consumption + client persistence in a
			// single transaction is tracked in #327.
			delete(am.store.Clients, clientID)
			logger.ErrorCF("native", "Failed to save store after pairing; removing in-memory client", map[string]interface{}{
				"error": err.Error(),
			})
			return nil, "", "", fmt.Errorf("pairing failed: could not persist client")
		}

		return client, token, refreshToken, nil
	}

	// JSON file path — unchanged (full reload to pick up concurrent changes).
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

	// If the PIN was issued without a device_name (e.g. CLI flow), require
	// the caller to supply one. This prevents pairing without any device
	// identification — an empty device_name on both sides would bypass the
	// name check entirely, which was the CRITICAL-3 bypass vector.
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

// RegisterDesktopClient registers a built-in trusted client used by the
// desktop app when running in desktop mode. The client is keyed by a fixed
// client ID ("desktop-local") so repeated registrations (sidecar restarts)
// replace the previous entry instead of accumulating. The provided token and
// refreshToken are stored as hashes only; the raw values never touch disk.
// The client never expires while the gateway runs in desktop mode.
func (am *AuthManager) RegisterDesktopClient(token, refreshToken string) error {
	if token == "" || refreshToken == "" {
		return fmt.Errorf("desktop token must not be empty")
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	if am.store == nil {
		return fmt.Errorf("client store not initialized")
	}

	// The desktop client is exempt from the MaxClients limit: it is a
	// built-in client, and the map assignment below is an upsert keyed by
	// the fixed DesktopClientID. For an existing key the map does not grow,
	// and for a new key at capacity we insert regardless rather than evict.
	client := &ClientInfo{
		ClientID:    DesktopClientID,
		TokenHash:   hashToken(token),
		RefreshHash: hashToken(refreshToken),
		DeviceName:  "Lele Desktop",
		Created:     time.Now(),
		Expires:     time.Now().AddDate(10, 0, 0), // effectively non-expiring
		LastSeen:    time.Now(),
		SessionKeys: []string{DesktopClientID},
	}

	am.store.Clients[DesktopClientID] = client

	if err := am.saveStoreUnlocked(); err != nil {
		logger.ErrorCF("native", "Failed to save store after registering desktop client", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return nil
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
			// Publish through the same lock-free field as UpdateLastSeen so
			// LastSeen stays write-once-per-instance (creation + deserialise,
			// both before the client is shared) and never has to be written
			// under the lock while readers hold live pointers.
			client.touchLastSeen(time.Now())
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

// UpdateLastSeen records that clientID just made an authenticated request.
//
// This is the hottest write in the channel: it runs on every HTTP request
// (NativeChannel.authMiddleware) and on every WebSocket connect, so it must
// never take AuthManager.mu exclusively — that would serialise all
// authenticated traffic against ValidateToken/GetClient.
//
// The timestamp is therefore published into the client's atomic.Int64 field:
// the manager lock is only held for the map lookup (RLock, shared with all
// other readers) and the value itself is written lock-free. The in-memory
// client struct is the source of truth for readers of GetClient/ListClients
// and for persistence, which fold the atomic value into LastSeen (see
// effectiveLastSeen and MarshalJSON).
func (am *AuthManager) UpdateLastSeen(clientID string) {
	am.mu.RLock()
	client, exists := am.store.Clients[clientID]
	am.mu.RUnlock()

	if !exists {
		return
	}

	client.touchLastSeen(time.Now())
}

// touchLastSeen publishes t as the client's most recent last-seen value.
// Safe for concurrent use: it only stores into the client's atomic field, so
// it never races with readers holding AuthManager.mu.RLock.
func (c *ClientInfo) touchLastSeen(t time.Time) {
	c.lastSeenNanos.Store(t.UnixNano())
}

// effectiveLastSeen returns the most recent last-seen timestamp: whichever is
// newer of the plain LastSeen field (set at construction and when a client is
// deserialised) and the lock-free hot-path value.
//
// Locking contract: the plain field must only ever be written BEFORE the
// client is published in AuthManager.store (construction / deserialisation /
// whole-entry replacement, all of which happen under am.mu). No setter mutates
// it on a published client — that is what used to require the exclusive lock —
// so reading it here is race-free for any caller holding a pointer obtained
// through the store. A future setter that writes LastSeen directly must hold
// am.mu for writing, and callers of this method must then hold at least RLock
// (all current callers do).
func (c *ClientInfo) effectiveLastSeen() time.Time {
	seen := c.LastSeen
	if nanos := c.lastSeenNanos.Load(); nanos > 0 {
		if hot := time.Unix(0, nanos); hot.After(seen) {
			seen = hot
		}
	}
	return seen
}

// snapshot returns a copy of the client safe to hand out to callers, carrying
// the freshest last-seen value.
//
// Fields are copied one by one on purpose: a struct assignment (copy := *c)
// would copy the embedded atomic.Int64, which must never be copied after first
// use — go vet's copylocks check rejects it too.
func (c *ClientInfo) snapshot() *ClientInfo {
	return &ClientInfo{
		ClientID:    c.ClientID,
		TokenHash:   c.TokenHash,
		RefreshHash: c.RefreshHash,
		DeviceName:  c.DeviceName,
		Created:     c.Created,
		Expires:     c.Expires,
		LastSeen:    c.effectiveLastSeen(),
		SessionKeys: append([]string(nil), c.SessionKeys...),
	}
}

// MarshalJSON serialises the client with the freshest last-seen value.
//
// The hot-path timestamp lives in an unexported atomic.Int64 (json:"-"), which
// encoding/json would drop, so the persisted blob would silently fall back to
// the value LastSeen had at the last locked write. Folding the atomic in here
// keeps the JSON contract (same "last_seen" RFC3339Nano value as before) for
// both the JSON file store and the SQLite blob.
//
// The embedded alias keeps this in sync with the struct definition: it
// contributes every exported field (tags included) without a hand-maintained
// field list, and the outer LastSeen is at depth 0 so it wins over the
// embedded one at depth 1.
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

	// snapshot() rather than a struct copy: the client now embeds an
	// atomic.Int64, which must not be copied (and go vet rejects it).
	// It also folds in the lock-free last-seen value written by
	// UpdateLastSeen, so callers always observe a fresh timestamp.
	return client.snapshot(), true
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
			if err := am.saveStoreUnlocked(); err != nil {
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

// ListClients returns a snapshot of every paired client, each carrying its
// freshest last-seen value (the lock-free value published by UpdateLastSeen
// lives in an atomic field, so live pointers would hand callers a stale
// timestamp — and an atomic that must not be copied).
// The returned clients are copies: no caller mutates them (all consumers read
// metadata / session-key sets), and callers must not rely on shared state.
func (am *AuthManager) ListClients() []*ClientInfo {
	am.mu.RLock()
	defer am.mu.RUnlock()

	clients := make([]*ClientInfo, 0, len(am.store.Clients))
	for _, client := range am.store.Clients {
		clients = append(clients, client.snapshot())
	}
	return clients
}

// GetPendingPINs returns all non-expired pending PINs. In SQLite mode
// the authoritative source is the DB; we keep RLock to preserve the
// concurrency contract of this method (callers expect a consistent
// snapshot, even though the DB read is itself atomic). DB errors are
// logged and result in an empty list — the function signature does not
// return error, and its only consumers (clientPendingCmd and
// clientStatusCmd in cmd/lele/client.go) simply print the result.
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

	// JSON path: read from in-memory map.
	pins := make([]*PendingPIN, 0, len(am.store.PendingPINs))
	for _, pending := range am.store.PendingPINs {
		pins = append(pins, pending)
	}
	return pins
}
