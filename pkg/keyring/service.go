package keyring

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Common errors returned by the Service.
var (
	ErrNotFound      = errors.New("keyring: secret not found")
	ErrAccessDenied  = errors.New("keyring: access denied by scope")
	ErrAgentWriteOff = errors.New("keyring: agent write access is disabled")
	ErrDisabled      = errors.New("keyring: module is disabled")
)

// ServiceConfig holds the tunables for a Service.
type ServiceConfig struct {
	Enabled          bool
	VaultPath        string
	Backend          string // "auto", "keychain", "file"
	AuditLogSize     int
	AllowAgentSet    bool
	AllowAgentDelete bool
	LeleDir          string
}

// Service is the business-logic layer for the keyring. It owns the store and
// key provider, enforces scoped access control, and records an audit trail.
//
// The store is opened lazily on first access; there is no interactive unlock
// step because the master key comes from the OS keychain or a protected file.
type Service struct {
	cfg     ServiceConfig
	store   Store
	keyProv KeyProvider
	audit   *AuditRing

	mu         sync.RWMutex
	openedOnce sync.Once
	openErr    error
}

// NewService creates a Service from the given configuration. The store and key
// provider are constructed but not opened until first use.
func NewService(cfg ServiceConfig) *Service {
	if cfg.AuditLogSize <= 0 {
		cfg.AuditLogSize = 1000
	}
	if cfg.VaultPath == "" {
		cfg.VaultPath = defaultVaultPath(cfg.LeleDir)
	}
	return &Service{
		cfg:     cfg,
		store:   NewFileStore(cfg.VaultPath),
		keyProv: NewKeyProvider(cfg.LeleDir, cfg.Backend),
		audit:   NewAuditRing(cfg.AuditLogSize),
	}
}

// NewServiceWithDeps creates a Service with explicit store and key provider.
// Primarily useful for tests.
func NewServiceWithDeps(cfg ServiceConfig, store Store, keyProv KeyProvider) *Service {
	if cfg.AuditLogSize <= 0 {
		cfg.AuditLogSize = 1000
	}
	return &Service{
		cfg:     cfg,
		store:   store,
		keyProv: keyProv,
		audit:   NewAuditRing(cfg.AuditLogSize),
	}
}

func defaultVaultPath(leleDir string) string {
	if leleDir == "" {
		leleDir = "."
	}
	return leleDir + "/keyring.enc"
}

// EnsureOpen lazily opens the store on first access.
func (s *Service) EnsureOpen() error {
	if !s.cfg.Enabled {
		return ErrDisabled
	}
	s.openedOnce.Do(func() {
		key, err := s.keyProv.GetKey()
		if err != nil {
			s.openErr = err
			return
		}
		s.openErr = s.store.Open(key)
	})
	return s.openErr
}

// Backend returns the active key provider backend name ("keychain" or "file").
func (s *Service) Backend() string {
	if s.keyProv == nil {
		return ""
	}
	return s.keyProv.Backend()
}

// GetForAgent retrieves a secret value on behalf of an agent, enforcing scope
// restrictions and recording an audit entry.
func (s *Service) GetForAgent(name, agentID, sessionKey string) (string, error) {
	if err := s.EnsureOpen(); err != nil {
		return "", err
	}

	s.mu.RLock()
	sec, ok := s.store.Get(name)
	s.mu.RUnlock()

	if !ok {
		s.record(name, agentID, sessionKey, "get", false)
		return "", ErrNotFound
	}
	if !sec.AllowsAgent(agentID) {
		s.record(name, agentID, sessionKey, "get", false)
		return "", fmt.Errorf("%w: secret %q is scoped and agent %q is not allowed", ErrAccessDenied, name, agentID)
	}

	s.record(name, agentID, sessionKey, "get", true)
	return sec.Value, nil
}

// GetRaw retrieves a secret value without scope checks. Intended for UI
// "reveal" actions and config placeholder resolution, not for agent access.
func (s *Service) GetRaw(name string) (string, error) {
	if err := s.EnsureOpen(); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sec, ok := s.store.Get(name)
	if !ok {
		return "", ErrNotFound
	}
	return sec.Value, nil
}

// SetFromUI stores or updates a secret from a UI source ("tui" or "webui").
func (s *Service) SetFromUI(name, value, description string, tags, scope []string, source string) error {
	return s.set(name, value, description, tags, scope, source)
}

// SetFromAgent stores a secret on behalf of an agent. It is gated by the
// AllowAgentSet configuration flag.
func (s *Service) SetFromAgent(name, value, description string, tags, scope []string, agentID, sessionKey string) error {
	if !s.cfg.AllowAgentSet {
		s.record(name, agentID, sessionKey, "set", false)
		return ErrAgentWriteOff
	}
	if err := s.set(name, value, description, tags, scope, "agent:"+agentID); err != nil {
		s.record(name, agentID, sessionKey, "set", false)
		return err
	}
	s.record(name, agentID, sessionKey, "set", true)
	return nil
}

func (s *Service) set(name, value, description string, tags, scope []string, source string) error {
	if err := s.EnsureOpen(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("keyring: secret name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	existing, ok := s.store.Get(name)

	sec := &Secret{
		Name:        name,
		Description: description,
		Value:       value,
		Tags:        tags,
		Scope:       scope,
		UpdatedAt:   now,
		CreatedBy:   source,
	}
	if ok && existing != nil {
		sec.CreatedAt = existing.CreatedAt
		if sec.CreatedBy == "" {
			sec.CreatedBy = existing.CreatedBy
		}
	} else {
		sec.CreatedAt = now
	}

	if err := s.store.Set(sec); err != nil {
		return err
	}
	return s.store.Flush()
}

// DeleteFromUI removes a secret from a UI source.
func (s *Service) DeleteFromUI(name, source string) error {
	return s.delete(name)
}

// DeleteFromAgent removes a secret on behalf of an agent. It is gated by the
// AllowAgentDelete configuration flag.
func (s *Service) DeleteFromAgent(name, agentID, sessionKey string) error {
	if !s.cfg.AllowAgentDelete {
		s.record(name, agentID, sessionKey, "delete", false)
		return ErrAgentWriteOff
	}
	if err := s.delete(name); err != nil {
		s.record(name, agentID, sessionKey, "delete", false)
		return err
	}
	s.record(name, agentID, sessionKey, "delete", true)
	return nil
}

func (s *Service) delete(name string) error {
	if err := s.EnsureOpen(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.store.Delete(name) {
		return ErrNotFound
	}
	return s.store.Flush()
}

// ListForAgent returns metadata for all secrets visible to the given agent.
// Secrets with a non-empty scope that excludes the agent are omitted.
func (s *Service) ListForAgent(agentID string) ([]SecretMeta, error) {
	if err := s.EnsureOpen(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	all := s.store.List()
	s.mu.RUnlock()

	out := make([]SecretMeta, 0, len(all))
	for _, meta := range all {
		if len(meta.Scope) == 0 {
			out = append(out, meta)
			continue
		}
		for _, id := range meta.Scope {
			if id == agentID {
				out = append(out, meta)
				break
			}
		}
	}
	s.record("", agentID, "", "list", true)
	return out, nil
}

// ListAll returns metadata for all secrets regardless of scope.
func (s *Service) ListAll() ([]SecretMeta, error) {
	if err := s.EnsureOpen(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.List(), nil
}

// Search finds secrets matching a query across name, tags, and description.
func (s *Service) Search(query string) ([]SecretMeta, error) {
	if err := s.EnsureOpen(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.Search(query), nil
}

// AuditLog returns the recorded access records in chronological order.
func (s *Service) AuditLog() []AccessRecord {
	return s.audit.Records()
}

// Count returns the number of stored secrets.
func (s *Service) Count() int {
	if err := s.EnsureOpen(); err != nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.store.List())
}

// Status returns a small summary suitable for UI status bars / API responses.
func (s *Service) Status() map[string]interface{} {
	return map[string]interface{}{
		"enabled": s.cfg.Enabled,
		"backend": s.Backend(),
		"count":   s.Count(),
	}
}

// record appends an audit entry.
func (s *Service) record(secretName, agentID, sessionKey, action string, granted bool) {
	if s.audit == nil {
		return
	}
	s.audit.Record(AccessRecord{
		SecretName: secretName,
		AgentID:    agentID,
		SessionKey: sessionKey,
		Action:     action,
		Timestamp:  time.Now(),
		Granted:    granted,
	})
}
