package config

import (
	"encoding/json"
	"os"
)

// LoadEditableDocument loads the config file without expanding ENV vars.
func LoadEditableDocument(path string) (*EditableDocument, *DocumentMetadata, error) {
	runtimeCfg, err := LoadConfig(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, nil, err
		}
		runtimeCfg = DefaultConfig()
	}

	doc := editableDocumentFromConfig(runtimeCfg)
	secretsByPath := make(map[string]string)

	data, readErr := os.ReadFile(path)
	if readErr == nil {
		if err := overlaySecretsFromRaw(doc, data, secretsByPath); err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(readErr) {
		return nil, nil, readErr
	}

	// Apply defaults where values are missing.
	doc = applyDefaults(doc)

	// Determine sections that require a restart.
	restartSections := detectRestartRequiredSections(doc)

	meta := &DocumentMetadata{
		ConfigPath:              path,
		Source:                  "file",
		CanSave:                 true,
		RestartRequiredSections: restartSections,
		SecretsByPath:           secretsByPath,
	}

	return doc, meta, nil
}

func overlaySecretsFromRaw(doc *EditableDocument, data []byte, secretsByPath map[string]string) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if channelsRaw, ok := raw["channels"]; ok {
		overlayChannelsWithPlaceholders(&doc.Channels, channelsRaw, "channels", secretsByPath)
	}
	if providersRaw, ok := raw["providers"]; ok {
		overlayProvidersWithPlaceholders(doc.Providers, providersRaw, "providers", secretsByPath)
	}
	if toolsRaw, ok := raw["tools"]; ok {
		overlayToolsWithPlaceholders(&doc.Tools, toolsRaw, "tools", secretsByPath)
	}
	return nil
}
