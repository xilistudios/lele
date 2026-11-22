package keyring

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound      = errors.New("keyring: secret not found")
	ErrAccessDenied  = errors.New("keyring: access denied by scope")
	ErrAgentWriteOff = errors.New("keyring: agent write access is disabled")
	ErrDisabled      = errors.New("keyring: module is disabled")
)

type ServiceConfig struct {
	Enabled          bool
	VaultPath        string
	Backend          string
	AuditLogSize     int
	AllowAgentSet    bool
	AllowAgentDelete bool
	LeleDir          string
}

type Service struct {
	cfg     ServiceConfig
	store   Store
	keyProv KeyProvider
	audit   *AuditRing

	mu         sync.RWMutex
	openedOnce sync.Once
	openErr    error
}

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

func (s *Service) Backend() string {
	if s.keyProv == nil {
		return ""
	}
	return s.keyProv.Backend()
}

func (s *Service) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

func (s *Service) LeleDir() string {
	if s == nil {
		return ""
	}
	return s.cfg.LeleDir
}

func (s *Service) GetForAgent(name, agentID, sessionKey string) (string, error) {
	if err := s.EnsureOpen(); err != nil {
		return "", err
	}

	s.mu.RLock()
	sec, err := s.store.Get(name)
	s.mu.RUnlock()
	if err != nil {
		s.record(name, agentID, sessionKey, "get", false)
		return "", err
	}

	if sec == nil {
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

func (s *Service) GetRaw(name string) (string, error) {
	if err := s.EnsureOpen(); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sec, err := s.store.Get(name)
	if err != nil {
		return "", err
	}
	if sec == nil {
		return "", ErrNotFound
	}
	return sec.Value, nil
}

func (s *Service) SetFromUI(name, value, description string, tags, scope []string, source string) error {
	return s.set(name, value, description, tags, scope, source, false)
}

func (s *Service) UpdateFromUI(name, value, description string, tags, scope []string, source string) error {
	return s.set(name, value, description, tags, scope, source, true)
}

func (s *Service) SetFromAgent(name, value, description string, tags, scope []string, agentID, sessionKey string) error {
	if !s.cfg.AllowAgentSet {
		s.record(name, agentID, sessionKey, "set", false)
		return ErrAgentWriteOff
	}
	if err := s.set(name, value, description, tags, scope, "agent:"+agentID, false); err != nil {
		s.record(name, agentID, sessionKey, "set", false)
		return err
	}
	s.record(name, agentID, sessionKey, "set", true)
	return nil
}

func (s *Service) set(name, value, description string, tags, scope []string, source string, mustExist bool) error {
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
	existing, err := s.store.Get(name)
	if err != nil {
		return err
	}
	if mustExist && existing == nil {
		return ErrNotFound
	}

	sec := &Secret{
		Name:        name,
		Description: description,
		Value:       value,
		Tags:        tags,
		Scope:       scope,
		UpdatedAt:   now,
		CreatedBy:   source,
	}
	if existing != nil {
		sec.CreatedAt = existing.CreatedAt
		if sec.CreatedBy == "" {
			sec.CreatedBy = existing.CreatedBy
		}
		if mustExist {
			sec.CreatedBy = existing.CreatedBy
			if sec.Value == "" {
				sec.Value = existing.Value
			}
		}
	} else {
		sec.CreatedAt = now
	}

	if err := s.store.Set(sec); err != nil {
		return err
	}
	return s.store.Flush()
}

func (s *Service) DeleteFromUI(name, source string) error {
	return s.delete(name)
}

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
	deleted, err := s.store.Delete(name)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNotFound
	}
	return s.store.Flush()
}

func (s *Service) ListForAgent(agentID string) ([]SecretMeta, error) {
	if err := s.EnsureOpen(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	all, err := s.store.List()
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}

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

func (s *Service) ListAll() ([]SecretMeta, error) {
	if err := s.EnsureOpen(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.List()
}

func (s *Service) Search(query string) ([]SecretMeta, error) {
	if err := s.EnsureOpen(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.Search(query)
}

func (s *Service) AuditLog() []AccessRecord {
	return s.audit.Records()
}

func (s *Service) Count() int {
	if err := s.EnsureOpen(); err != nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	metas, err := s.store.List()
	if err != nil {
		return 0
	}
	return len(metas)
}

func (s *Service) Status() map[string]interface{} {
	return map[string]interface{}{
		"enabled": s.cfg.Enabled,
		"backend": s.Backend(),
		"count":   s.Count(),
	}
}

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
