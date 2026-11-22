package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type WorkspaceSkillsConfig struct {
	Enabled  []string `json:"enabled,omitempty"`
	Disabled []string `json:"disabled,omitempty"`
}

const workspaceConfigFileName = "workspace.json"

func LoadWorkspaceConfig(workspaceDir string) (*WorkspaceSkillsConfig, error) {
	cfgPath := filepath.Join(workspaceDir, ".lele", workspaceConfigFileName)

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &WorkspaceSkillsConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read workspace config: %w", err)
	}

	var wrapper struct {
		Skills *WorkspaceSkillsConfig `json:"skills"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse workspace config: %w", err)
	}

	if wrapper.Skills != nil {
		return wrapper.Skills, nil
	}

	return &WorkspaceSkillsConfig{}, nil
}

func SaveWorkspaceConfig(workspaceDir string, cfg *WorkspaceSkillsConfig) error {
	cfgDir := filepath.Join(workspaceDir, ".lele")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		return fmt.Errorf("failed to create .lele directory: %w", err)
	}

	cfgPath := filepath.Join(cfgDir, workspaceConfigFileName)

	existing := make(map[string]json.RawMessage)
	if existingData, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(existingData, &existing)
	}

	skillsData, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal skills config: %w", err)
	}
	existing["skills"] = skillsData

	finalData, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workspace config: %w", err)
	}

	if err := os.WriteFile(cfgPath, finalData, 0644); err != nil {
		return fmt.Errorf("failed to write workspace config: %w", err)
	}

	return nil
}

func (c *WorkspaceSkillsConfig) IsEnabled(skillName string) bool {
	for _, d := range c.Disabled {
		if d == skillName {
			return false
		}
	}
	for _, e := range c.Enabled {
		if e == skillName {
			return true
		}
	}
	return true
}

func (c *WorkspaceSkillsConfig) SetEnabled(skillName string) {
	c.Disabled = removeFromSlice(c.Disabled, skillName)
	for _, e := range c.Enabled {
		if e == skillName {
			return
		}
	}
	c.Enabled = append(c.Enabled, skillName)
}

func (c *WorkspaceSkillsConfig) SetDisabled(skillName string) {
	c.Enabled = removeFromSlice(c.Enabled, skillName)
	for _, d := range c.Disabled {
		if d == skillName {
			return
		}
	}
	c.Disabled = append(c.Disabled, skillName)
}

func (c *WorkspaceSkillsConfig) Toggle(skillName string) bool {
	if c.IsEnabled(skillName) {
		c.SetDisabled(skillName)
		return false
	}
	c.SetEnabled(skillName)
	return true
}

func removeFromSlice(slice []string, item string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

type WorkspaceConfigManager struct {
	mu        sync.RWMutex
	workspace string
	config    *WorkspaceSkillsConfig
}

func NewWorkspaceConfigManager(workspace string) (*WorkspaceConfigManager, error) {
	cfg, err := LoadWorkspaceConfig(workspace)
	if err != nil {
		return nil, err
	}
	return &WorkspaceConfigManager{
		workspace: workspace,
		config:    cfg,
	}, nil
}

func (m *WorkspaceConfigManager) IsEnabled(skillName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.IsEnabled(skillName)
}

func (m *WorkspaceConfigManager) SetEnabled(skillName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.SetEnabled(skillName)
	return SaveWorkspaceConfig(m.workspace, m.config)
}

func (m *WorkspaceConfigManager) SetDisabled(skillName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.SetDisabled(skillName)
	return SaveWorkspaceConfig(m.workspace, m.config)
}

func (m *WorkspaceConfigManager) Toggle(skillName string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	enabled := m.config.Toggle(skillName)
	return enabled, SaveWorkspaceConfig(m.workspace, m.config)
}

func (m *WorkspaceConfigManager) GetConfig() WorkspaceSkillsConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.config
}
