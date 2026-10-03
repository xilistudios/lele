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

	// lastSlowReload is the time of the last slow-path loadStore() taken by
	// ValidateToken/RefreshToken on a cache miss. Guarded by am.mu and
	// deliberately left at its ZERO value by SetStore/loadStore: the zero
	// time means "this manager has never reloaded", so the first slow-path
	// miss is always allowed to adopt clients another process persisted.
	// Only allowSlowReloadLocked reads/writes it.
	lastSlowReload time.Time

	// storeUnavailable records WHY the shared SQLite store could not be
	// opened by a binary that DOES support SQLite (locked file, permissions,
	// full disk, corrupted DB). Empty means "not degraded".
	//
	// It is deliberately separate from repo == nil: a nil repo is also the
	// legitimate steady state on platforms without SQLite, where JSON is the
	// real backend and the PINs minted here ARE redeemable. When this field
	// is set the pending PINs live in a database this process cannot read, so
	// GeneratePIN/PairWithPIN refuse with ErrPairingUnavailable instead of
	// answering "invalid PIN" for a PIN that is perfectly valid on the CLI
	// that minted it (#330). Guarded by am.mu.
	storeUnavailable string
}

// ErrPairingUnavailable is returned by GeneratePIN and PairWithPIN when this
// process runs DEGRADED: the binary supports SQLite but the shared store could
// not be opened, so the pending PINs (which live in the database) can neither
// be minted nor redeemed here. The REST layer maps it to 503 +
// "store_unavailable"; before #330 the same situation answered 400 "invalid
// PIN" forever, which pointed at the user's PIN instead of at the gateway's
// storage.
var ErrPairingUnavailable = errors.New("pairing unavailable")

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

	// A usable store supersedes any degradation recorded before it (the
	// gateway can be re-wired after a config reload).
	am.storeUnavailable = ""

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

// SetStoreUnavailable marks this manager as DEGRADED: the binary supports
// SQLite but the shared store could not be opened, so the pending PINs other
// processes write to the database are unreachable from here. Passing nil
// clears the condition; SetStore with a non-nil repo clears it implicitly.
//
// Callers must NOT use this for store.ErrUnsupportedPlatform: on a platform
// without SQLite the JSON file IS the shared backend, PINs minted here are
// redeemable, and pairing must keep working exactly as before (#330).
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

// StoreUnavailable reports whether this manager is degraded and the reason
// recorded by SetStoreUnavailable. It is what the status endpoints surface so
// a JSON-mode gateway is visible from the outside instead of only in its log.
func (am *AuthManager) StoreUnavailable() (reason string, degraded bool) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	return am.storeUnavailable, am.storeUnavailable != ""
}

// StorageBackend reports which backend currently holds this manager's clients
// and pending PINs: "sqlite" when a store repo is wired, "json" otherwise. A
// "json" answer is either a no-SQLite platform (legitimate) or a degraded
// gateway - StoreUnavailable tells the two apart (#330).
func (am *AuthManager) StorageBackend() string {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if am.repo != nil {
		return "sqlite"
	}
	return "json"
}

// pairingUnavailableErrLocked builds the diagnosable error returned in place of
// "invalid PIN" while degraded. am.mu must be held.
func (am *AuthManager) pairingUnavailableErrLocked() error {
	return fmt.Errorf("%w: this gateway could not open its SQLite store (%s) — pairing PINs live in that database, so none can be minted or redeemed here until the store is reachable again", ErrPairingUnavailable, am.storeUnavailable)
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
			// The MAP KEY is authoritative: stamp it onto the blob's field so
			// the invariant "ClientID == map key" holds for every loaded
			// client. persistClientLocked and rotateClientLocked look clients
			// up BY KEY, so a row whose stored client_id is empty or diverges
			// (migrated/manual blob) would otherwise be written back to a
			// phantom id — or skipped entirely, silently losing a rotation.
			info.ClientID = id
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
		// Same key-authority invariant as the SQLite branch above: the map key
		// wins over a divergent client_id in the JSON blob.
		client.ClientID = clientID
	}
	am.cleanupExpired()
	return nil
}

// saveStore is a WHOLE-STORE save. It is a latent bomb in SQLite mode and
// must NOT be used on a production code path: saveStoreUnlocked upserts every
// client in this process's map and DELETEs every table row absent from it, so
// any row that another process paired or rotated (i.e. newer than this map,
// the normal state in a multi-process setup) is silently reverted or dropped
// — precisely the pattern that made an expelled WebUI session reappear.
//
// Production mutators must use the DIRECTED writes instead:
// persistClientLocked (one client row) or DeleteClient (one row), both of
// which leave foreign rows untouched.
//
// This whole-store entry point is kept only for migrations/tests (auth_test.go
// still drives it); am.mu is taken here, unlike the ...Unlocked variant.
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

	// Degraded gateway: a PIN minted into the JSON file while the rest of the
	// system uses SQLite is a dead PIN (it disappears as soon as the store
	// opens again). Refuse with a diagnosable error instead (#330).
	if am.storeUnavailable != "" {
		return nil, am.pairingUnavailableErrLocked()
	}

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

	// Documented limit (reviewed, intentionally left as-is): this is the JSON
	// branch, reached only when am.repo == nil, so it cannot clobber a shared
	// SQLite store. In SQLite mode the single-use PIN lives in the DB and this
	// whole-store save is never executed — which is why GeneratePIN needs no
	// directed-write treatment. A future pin-row API (repo.SetPendingPIN) is
	// the place to make even the JSON path directed; today a pending PIN is
	// written exactly here and nowhere else.
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

	// Degraded gateway: the PIN was written to the shared SQLite store by a
	// healthy CLI, and this process cannot read that store. Looking it up in
	// the JSON file answers "invalid PIN" for a perfectly valid PIN, forever,
	// so refuse with the real reason instead (#330).
	if am.storeUnavailable != "" {
		return nil, "", "", am.pairingUnavailableErrLocked()
	}

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

		// Directed write: the newly paired client is the only row that
		// changed, and it is the only row this call may touch — a whole-store
		// save would upsert every row from a possibly stale map and delete
		// rows paired by another process (see persistClientLocked), i.e. it
		// could expel a session that is perfectly valid on the shared DB.
		if err := am.persistClientLocked(clientID); err != nil {
			// Fail closed: remove the in-memory client so no phantom
			// credential survives until the next restart. The PIN is
			// already consumed (TakePendingPIN succeeded) — that is
			// intentional: better to require a fresh PIN than to emit
			// a credential that cannot survive a gateway restart.
			// Atomicity of PIN consumption + client persistence in a
			// single transaction is tracked in #327.
			//
			// The failed directed write leaves at most this one row
			// absent/unwritten in the DB; other processes' rows are
			// untouched, which is the point of not falling back to a
			// whole-store save here.
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

	// Directed write: registering the built-in desktop client must not
	// rewrite/delete the rows other processes paired (see
	// persistClientLocked).
	if err := am.persistClientLocked(DesktopClientID); err != nil {
		logger.ErrorCF("native", "Failed to save store after registering desktop client", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return nil
}

func (am *AuthManager) ValidateToken(token string) (*ClientInfo, bool) {
	tokenHash := hashToken(token)

	// Hot path: shared lock, no reload. An unknown-but-invalid token is the
	// only case that pays for the slow path below.
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

	// Slow path (miss): this process's map may simply be behind the shared
	// SQLite store — another process (CLI, TUI, a second gateway) can pair or
	// rotate a client after this manager loaded its copy. Reloading once
	// before declaring the token invalid is what keeps a session alive
	// instead of expelling a client that is perfectly valid on disk.
	//
	// Double-checked locking: the reload needs the write lock, so it is
	// re-scanned here under am.mu and never on the hot path.
	am.mu.Lock()
	defer am.mu.Unlock()

	// Budget the reload: this path is reachable anonymously (an invalid token
	// matches nothing), so an attacker could otherwise force a full loadStore
	// — SELECT of every blob + unmarshal per row — under the exclusive lock
	// once per request. Inside the budget the miss is reported as a miss
	// without touching the store; a legitimate token written by another
	// process is still adopted by the next request after the interval.
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

// findClientByTokenHash returns the live client whose access-token hash
// matches hash, or nil when there is none.
//
// Callers must hold at least am.mu.RLock. Expiry is part of the match
// (unchanged semantics): an expired client is still kept in the map until
// cleanupExpired/RefreshToken removes it, so it must not validate.
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

// slowReloadMinInterval caps how often a single AuthManager may take its
// slow-path (cache-miss) reload of the whole client store.
//
// Why a cap is needed: a miss in ValidateToken/RefreshToken is reachable
// WITHOUT any credential — an unknown token simply does not match — and the
// reload it triggers runs loadStore() under the EXCLUSIVE am.mu: a SELECT of
// every client blob plus a json.Unmarshal per row (and ListPendingPINs).
// The HTTP rate limiter in front of these handlers is disabled by default and
// is per-IP, so an unauthenticated flood of invalid tokens would otherwise
// force one full reload per request, amplifying cost and serialising all
// authenticated traffic (which shares the same lock) behind the attacker.
//
// One reload per interval per process is enough for the feature the slow path
// exists for: adopting a client that another process (CLI, TUI, a second
// gateway) paired or rotated in the shared SQLite DB. That is a rare, human
// paced event, not a per-request need.
//
// The interval is deliberately short (a few reloads per second is still a
// negligible load, since a reload is one SELECT + N unmarshals over a table
// of at most a handful of rows) so that the cap cannot itself expel a session:
// the only legitimate case it can delay is a client whose credential another
// process rotated in the sub-second window right after this manager's last
// slow reload. That delay is signalled EXPLICITLY, not implicitly: a refresh
// whose reload was suppressed because the budget was spent is answered with
// HTTP 429 + Retry-After (see errRefreshUnavailable/handleRefresh), and the
// WebUI treats a 429 as non-fatal — it backs off and retries — so the session
// survives instead of being expelled. A larger window would widen that race
// without measurably improving the DoS bound.
const slowReloadMinInterval = 250 * time.Millisecond

// errRefreshUnavailable signals that the refresh token could not be checked
// against the shared store because the slow-path reload was rate-limited, NOT
// that the credential is invalid. handleRefresh maps it to HTTP 429 +
// Retry-After so the client backs off and retries instead of treating it as a
// fatal, session-ending rejection.
var errRefreshUnavailable = errors.New("auth store reload suppressed, retry")

// allowSlowReloadLocked reports whether a slow-path store reload may run now,
// and records the allowance when it does. The first call on a manager
// (lastSlowReload is zero) is always allowed so a freshly started process can
// immediately adopt clients another process persisted; afterwards reloads are
// rate-limited to one per slowReloadMinInterval per manager, bounding the work
// an unauthenticated flood of invalid tokens can force (each miss would
// otherwise trigger a full loadStore under the exclusive lock).
//
// Callers MUST hold am.mu for writing (it mutates lastSlowReload and guards
// the loadStore that follows).
func (am *AuthManager) allowSlowReloadLocked(now time.Time) bool {
	if !am.lastSlowReload.IsZero() && now.Sub(am.lastSlowReload) < slowReloadMinInterval {
		return false
	}
	am.lastSlowReload = now
	return true
}

// authReconcileInterval bounds how long a client revoked by ANOTHER process
// keeps authenticating inside this one. See reconcileClientsWithStore.
const authReconcileInterval = 15 * time.Second

// reconcileClientsWithStore drops from this process's in-memory map every
// client that no longer exists in the shared SQLite store, and returns the
// auth client IDs it removed.
//
// It closes the other half of #327. The snapshot-sync bug is fixed (mutators
// write single rows, so a gateway write can no longer resurrect a client the
// CLI deleted), but that only stops the row from coming BACK: the gateway's
// map still holds the ClientInfo it loaded at startup, and ValidateToken's hot
// path answers a map HIT without ever consulting the DB. A token revoked with
// `lele client remove` therefore kept authenticating until this process
// restarted — exactly the impact the issue reports.
//
// Why a poll and not a hot-path check: the miss case already reloads lazily
// (see slowReloadMinInterval), but a hit has nothing to tell it that its own
// entry is stale, and verifying every hit against the DB would put a query
// plus an exclusive-lock window in front of every authenticated request to
// answer a question that changes a few times a day. One SELECT over a table of
// a handful of rows per interval bounds revocation at authReconcileInterval
// instead of at the lifetime of the process.
//
// Deletions only, deliberately: this must never ADD or overwrite entries.
// Adoption (a client another process paired) is already covered by the
// reload-on-miss, and copying DB rows into the map here would revert an
// in-memory rotation whose persist failed and was logged-and-swallowed.
// Removing entries cannot resurrect anything, which is the failure mode this
// whole area is careful about.
//
// Consequence worth stating: a client whose directed write FAILED (a
// persistClientLocked error is logged and swallowed by design, e.g. SQLITE_BUSY
// or a full disk) lives only in this map, so this drops it after
// authReconcileInterval. That is the intended reading of "the DB is
// authoritative in SQLite mode" and is no worse than today — the very first
// cache-miss already runs loadStore(), which replaces the map with the table
// and erases such a client. Making a lost persist survivable would require the
// mutators to fail the request instead of logging, which is a separate call.
//
// The JSON backend (am.repo == nil) is a single-writer document owned by this
// process, so there is nothing to reconcile and the call is a no-op.
func (am *AuthManager) reconcileClientsWithStore() ([]string, error) {
	am.mu.RLock()
	repo := am.repo
	am.mu.RUnlock()

	if repo == nil {
		return nil, nil
	}

	// Read outside am.mu: the only thing the caller's authenticated traffic
	// can ever wait on here is the short map edit below, not the query.
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

// rotationGrace is how long the refresh token a client was rotated AWAY from
// is still accepted once, issuing a fresh pair.
//
// Refresh is deliberately single-use, which makes a lost response fatal for
// the client: it keeps presenting a token the server already replaced,
// gets "invalid refresh token", and a WebUI treats that as a revoked session
// (self-logout, while the server-side client record is perfectly alive). The
// window is short because replaying an already-superseded token is only safe
// for the client that just lost the answer — after a minute, a previous token
// showing up again is far more likely to be a leaked credential than a
// retry, so it is rejected.
const rotationGrace = 60 * time.Second

// RefreshToken exchanges a refresh token for a new token pair (single use,
// both credentials rotate).
//
// A presented token is accepted in three cases, in this order:
//
//  1. it matches a client's CURRENT RefreshHash (normal rotation);
//  2. it matches it only after reloading the store from SQLite — the
//     multi-process case: another process rotated or persisted the client
//     after this manager built its in-memory copy. That reload is
//     rate-limited per manager (see slowReloadMinInterval): when the budget
//     is spent the token is NOT declared invalid, it is reported as
//     errRefreshUnavailable so the caller answers 429 + Retry-After and the
//     client retries after the interval (the credential may well be valid on
//     disk; we simply could not afford to look). The rate limit protects
//     against an unauthenticated flood of unknown tokens.
//  3. it matches a client's PREVIOUS RefreshHash within rotationGrace — the
//     response of that rotation was lost, so the client is retrying with the
//     credential it still holds and gets a usable pair (see rotationGrace).
//     This grace is consumed globally: the replay clears Prev* on the
//     persisted row, so the superseded token buys exactly one replacement
//     pair across all processes, not one per process.
//
// Persistence is directed: only the rotated client's row is written. The
// full-store saveStoreUnlocked is unusable here because its cleanup step
// DELETEs rows present in the DB but absent from this process's map, i.e. it
// reverts clients other processes paired — as well as rewriting every row.
func (am *AuthManager) RefreshToken(refreshToken string) (*ClientInfo, string, string, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	refreshHash := hashToken(refreshToken)

	clientID, client := am.findClientByRefreshHash(refreshHash)

	// Cache miss: reload once from the DB (loadStore must be called with
	// am.mu held — same pattern as the JSON branch of PairWithPIN) and
	// rescan before deciding the token is unknown.
	//
	// The reload is rate-limited per manager (allowSlowReloadLocked): this
	// branch is reachable with an invalid, unauthenticated token, and each
	// reload is a full store read under the exclusive lock.
	//
	// A SUPPRESSED reload (budget spent) is tracked separately: if the token
	// is also not replayable, the rejection must NOT claim the credential is
	// invalid — the shared store was simply not consulted, so the token may
	// be perfectly valid on disk (another process rotated it). That case
	// returns errRefreshUnavailable, which the HTTP layer maps to 429 +
	// Retry-After so the client backs off and retries instead of expelling
	// the session.
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
		// Replay inside the grace window: the client never saw the pair the
		// previous rotation issued, so it retries with the token it has.
		// Rotating again hands it a usable pair.
		//
		// The grace is CONSUMED by clearing Prev* on the persisted row: after
		// this call any other process that reloads the store finds no replay
		// material for the token just used, so a superseded refresh token buys
		// exactly one replacement pair GLOBALLY, not one per process. Without
		// that explicit consume, the re-arm done by rotateClientLocked (Prev*
		// move on to this rotation's hashes) only bounds the replay inside
		// this process: a second, stale process keeps its frozen Prev* +
		// RotatedAt and would accept the same old token again, each acceptance
		// minting another 30-day pair — O(number of processes with a stale
		// map) redemptions of a single superseded credential.
		replayID, replayClient, age := am.findClientByPrevRefreshHash(refreshHash)
		if replayClient == nil {
			if reloadSuppressed {
				// We could not consult the shared store this instant; the token
				// may be valid on disk. Ask the client to retry (429 + Retry-After)
				// rather than expelling it with a fatal 400.
				return nil, "", "", errRefreshUnavailable
			}
			// No reload was owed (budget available but the token was absent
			// after loading, or repo == nil): the credential really is invalid.
			return nil, "", "", fmt.Errorf("invalid refresh token")
		}

		replayed, newToken, newRefreshToken, err := am.rotateClientLocked(replayID, replayClient)
		if err != nil {
			return nil, "", "", err
		}

		// Explicit global consume: the token that was just replayed is now
		// dead for every process, including ones whose in-memory copy still
		// points at it (they either reload and see Prev* empty, or are
		// rejected on their next attempt).
		//
		// Consequence for parallel clients: once this consume lands, the tab
		// that was holding the "current" token of that rotated client can no
		// longer be rescued server-side — its token is superseded on the row.
		// That tab depends on the frontend adopting the pair this replay
		// issued (ADOPTION_RETRY_DELAY_MS); this is the intended trade-off of
		// consuming the grace globally instead of once per process.
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

// findClientByRefreshHash returns the id and live pointer of the client whose
// CURRENT refresh hash matches hash ("", nil when there is none).
// Callers must hold am.mu (write lock; the caller rotates the result).
func (am *AuthManager) findClientByRefreshHash(hash string) (string, *ClientInfo) {
	for id, client := range am.store.Clients {
		if client.RefreshHash == hash {
			return id, client
		}
	}
	return "", nil
}

// findClientByPrevRefreshHash returns the id, live pointer and age of the
// rotation whose PREVIOUS refresh hash matches hash, when that rotation is
// still inside rotationGrace. Clients without a recorded rotation
// (PrevRefreshHash empty: legacy blobs, freshly paired clients) never match.
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

// rotateClientLocked issues a fresh token pair for client and persists just
// that client. am.mu must be held for writing.
//
// The pre-rotation hashes are recorded before they are overwritten (Prev* /
// RotatedAt), which is what makes the rotationGrace replay possible and what
// pushes an older replay out of the window on the next rotation.
func (am *AuthManager) rotateClientLocked(clientID string, client *ClientInfo) (*ClientInfo, string, string, error) {
	// Unchanged behaviour: an expired client is dropped from memory and the
	// caller is told the session expired. The row is left in the DB as
	// before, so a later reload re-reads it and rejects it again on the next
	// attempt.
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

	// Record what this rotation supersedes BEFORE the hashes are overwritten.
	// These are already hashes — never re-hash them.
	client.PrevTokenHash = client.TokenHash
	client.PrevRefreshHash = client.RefreshHash
	// time.Now() once: RotatedAt and LastSeen describe the same event.
	now := time.Now()
	client.RotatedAt = now

	client.TokenHash = hashToken(newToken)
	client.RefreshHash = hashToken(newRefreshToken)
	// Publish through the same lock-free field as UpdateLastSeen so
	// LastSeen stays write-once-per-instance (creation + deserialise,
	// both before the client is shared) and never has to be written
	// under the lock while readers hold live pointers.
	client.touchLastSeen(now)
	client.Expires = now.AddDate(0, 0, expiryDays)

	// Persist under the MAP KEY (clientID), never client.ClientID: the key is
	// what identifies the row in the store, and loadStore forces the two to
	// agree (it stamps info.ClientID = id), but a blob that predates that
	// normalisation (migrated/manual row with an empty or divergent
	// client_id) would make persistClientLocked miss the row and silently
	// drop the rotation.
	if err := am.persistClientLocked(clientID); err != nil {
		logger.ErrorCF("native", "Failed to save store after refresh", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return client, newToken, newRefreshToken, nil
}

// persistClientLocked persists the CURRENT in-memory state of exactly one
// client row. am.mu must be held (readers must not observe a half-written
// row) and the client must already exist in am.store.Clients.
//
// It is the directed-write primitive for every mutator that touches a single
// client, and it exists because saveStoreUnlocked is a WHOLE-STORE operation:
// it upserts every client in memory (one transaction per row, on the auth hot
// path) and DELETEs every row that exists in the table but not in this
// process's map. In a multi-process setup (gateway + CLI/TUI sharing one
// SQLite DB) rows in the DB are routinely newer than this map, so that delete
// step silently drops — and the upsert step silently reverts — pairings and
// rotations performed by another process. That is exactly how an expelled
// WebUI session reappeared: TrackSessionKey ran on the WebUI hot path (GET
// /api/session) and, via saveStoreUnlocked, rewrote the whole table from a
// stale map on every new session key. One directed write of the row that
// actually changed does neither.
//
// Backend split:
//   - SQLite (am.repo != nil): marshal and SetClient ONLY the affected row.
//   - JSON (am.repo == nil): unchanged behaviour — saveStoreUnlocked writes
//     the whole file. Correct there because the JSON store is the single
//     writer's read-modify-write document; there is no per-row API.
//
// Errors are returned for the caller to log and swallow, as before: the
// mutation is already effective in memory, and the next write or reload
// reconciles the row.
func (am *AuthManager) persistClientLocked(clientID string) error {
	if am.repo == nil {
		// JSON backend: unchanged behaviour. Single writer, whole file, and
		// the read-modify-write above keeps other processes' entries.
		return am.saveStoreUnlocked()
	}

	client, ok := am.store.Clients[clientID]
	if !ok {
		// Nothing to write: the caller deleted the row (or the client was
		// dropped from memory before persisting). Deleting rows absent from
		// the map is saveStoreUnlocked's dangerous behaviour, never a
		// directed write's.
		//
		// Logged (not just returned) because a caller that meant to persist a
		// live client would otherwise lose the write in silence — e.g. a
		// clientID that never made it into the map (key/field mismatch after
		// a raw DB edit).
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
	// saveStoreUnlocked always stamped this; keep the invariant even though
	// the row itself carries the authoritative timestamps.
	am.store.LastModified = time.Now()
	return nil
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
	// Directed write (SQLite) / whole-file save (JSON): this runs on the
	// WebUI hot path (validating or adopting a session key via GET
	// /api/session), so a whole-store save here would rewrite every row and
	// delete rows this process does not know about — reverting the rotations
	// and pairings another process just persisted. See persistClientLocked.
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

	// Revocation must remove ONLY the target row in SQLite.
	// saveStoreUnlocked is wrong here: on top of the revoke it would upsert
	// every other client from this (possibly stale) map and DELETE every row
	// present in the table but absent from it — i.e. revoking one client
	// would also drop clients this process had not loaded yet, including ones
	// another process just paired. The JSON backend keeps the whole-file save
	// (single writer, no per-row API).
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
			// Same directed write as TrackSessionKey: only this client's row
			// changed, so a whole-store save would clobber rows owned by
			// other processes sharing the DB.
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
