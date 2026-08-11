package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// WorkspaceSkillsConfig stores per-workspace skill enable/disable state.
// Skills not listed in either field default to enabled (backward compatible).
type WorkspaceSkillsConfig struct {
	Enabled  []string `json:"enabled,omitempty"`
	Disabled []string `json:"disabled,omitempty"`
}

// workspaceConfigFileName is the config file name inside .lele/
const workspaceConfigFileName = "workspace.json"

// LoadWorkspaceConfig reads .lele/workspace.json from the workspace dir.
// The skills config is stored under the "skills" key in the workspace.json file.
// If the file doesn't exist, returns an empty config (all skills enabled by default).
func LoadWorkspaceConfig(workspaceDir string) (*WorkspaceSkillsConfig, error) {
	cfgPath := filepath.Join(workspaceDir, ".lele", workspaceConfigFileName)

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &WorkspaceSkillsConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read workspace config: %w", err)
	}

	// Parse as wrapper object with "skills" key
	var wrapper struct {
		Skills *WorkspaceSkillsConfig `json:"skills"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse workspace config: %w", err)
	}

	if wrapper.Skills != nil {
		return wrapper.Skills, nil
	}

	// No skills key found — return empty config
	return &WorkspaceSkillsConfig{}, nil
}

// SaveWorkspaceConfig writes the config to .lele/workspace.json.
// Creates the .lele directory if it doesn't exist.
// The skills config is stored under the "skills" key in the workspace.json file.
func SaveWorkspaceConfig(workspaceDir string, cfg *WorkspaceSkillsConfig) error {
	cfgDir := filepath.Join(workspaceDir, ".lele")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		return fmt.Errorf("failed to create .lele directory: %w", err)
	}

	cfgPath := filepath.Join(cfgDir, workspaceConfigFileName)

	// Read existing config to preserve non-skills fields
	existing := make(map[string]json.RawMessage)
	if existingData, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(existingData, &existing) // best effort
	}

	// Marshal skills config and merge into existing
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

// IsEnabled checks if a skill should be loaded. Default: true for unknown skills.
func (c *WorkspaceSkillsConfig) IsEnabled(skillName string) bool {
	// Check disabled list first
	for _, d := range c.Disabled {
		if d == skillName {
			return false
		}
	}
	// If explicitly enabled, return true
	for _, e := range c.Enabled {
		if e == skillName {
			return true
		}
	}
	// Default: enabled (backward compatible)
	return true
}

// SetEnabled marks a skill as enabled (removes from disabled list if present).
func (c *WorkspaceSkillsConfig) SetEnabled(skillName string) {
	// Remove from disabled
	c.Disabled = removeFromSlice(c.Disabled, skillName)
	// Add to enabled if not already there
	for _, e := range c.Enabled {
		if e == skillName {
			return
		}
	}
	c.Enabled = append(c.Enabled, skillName)
}

// SetDisabled marks a skill as disabled (removes from enabled list if present).
func (c *WorkspaceSkillsConfig) SetDisabled(skillName string) {
	// Remove from enabled
	c.Enabled = removeFromSlice(c.Enabled, skillName)
	// Add to disabled if not already there
	for _, d := range c.Disabled {
		if d == skillName {
			return
		}
	}
	c.Disabled = append(c.Disabled, skillName)
}

// Toggle flips the enabled/disabled state of a skill.
// Returns true if the skill is now enabled, false if disabled.
func (c *WorkspaceSkillsConfig) Toggle(skillName string) bool {
	if c.IsEnabled(skillName) {
		c.SetDisabled(skillName)
		return false
	}
	c.SetEnabled(skillName)
	return true
}

// removeFromSlice removes a string from a slice (if present).
func removeFromSlice(slice []string, item string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

// WorkspaceConfigManager provides thread-safe access to workspace skills config.
type WorkspaceConfigManager struct {
	mu        sync.RWMutex
	workspace string
	config    *WorkspaceSkillsConfig
}

// NewWorkspaceConfigManager creates a new manager for the given workspace.
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

// IsEnabled checks if a skill is enabled.
func (m *WorkspaceConfigManager) IsEnabled(skillName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.IsEnabled(skillName)
}

// SetEnabled enables a skill and persists the config.
func (m *WorkspaceConfigManager) SetEnabled(skillName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.SetEnabled(skillName)
	return SaveWorkspaceConfig(m.workspace, m.config)
}

// SetDisabled disables a skill and persists the config.
func (m *WorkspaceConfigManager) SetDisabled(skillName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.SetDisabled(skillName)
	return SaveWorkspaceConfig(m.workspace, m.config)
}

// Toggle flips the state and persists. Returns new enabled state.
func (m *WorkspaceConfigManager) Toggle(skillName string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	enabled := m.config.Toggle(skillName)
	return enabled, SaveWorkspaceConfig(m.workspace, m.config)
}

// GetConfig returns a copy of the current config.
func (m *WorkspaceConfigManager) GetConfig() WorkspaceSkillsConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.config
}
