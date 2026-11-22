package tui

import (
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

type connectRowKind int

const (
	connectRowCustomOpenAI connectRowKind = iota
	connectRowCustomAnthropic
	connectRowAPIKey
	connectRowOAuth
	connectRowFreeType
)

type connectRow struct {
	kind    connectRowKind
	label   string
	typ     string
	name    string
	apiBase string

	keyHint           string
	modelHint         string
	defaultModel      string
	defaultModelAlias string

	oauthName string
}

type connectOAuthRowMeta struct {
	provider   string
	label      string
	configType string
	configName string
	authMethod string

	requiresEmptyAPIKey bool

	modelHint         string
	defaultModel      string
	defaultModelAlias string
}

var connectOAuthCatalog = map[string]connectOAuthRowMeta{
	"openai": {
		provider:          "openai",
		label:             "tui.connectRowOAuthOpenAI",
		configType:        "openai",
		configName:        "openai",
		authMethod:        "oauth",
		modelHint:         "gpt-5.2 (Codex)...",
		defaultModel:      "gpt-5.2",
		defaultModelAlias: "gpt-5.2",
	},
	"anthropic": {
		provider:   "anthropic",
		label:      "tui.connectRowOAuthAnthropic",
		configType: "anthropic",
		configName: "anthropic",
		authMethod: "oauth",
	},
	"gemini": {
		provider:            "gemini",
		label:               "tui.connectRowOAuthGemini",
		configType:          "gemini",
		configName:          "gemini",
		authMethod:          "oauth",
		requiresEmptyAPIKey: true,
	},
}

func connectRows() []connectRow {
	rows := make([]connectRow, 0, len(providerPresets)+3)
	rows = append(rows,
		connectRow{
			kind:  connectRowCustomOpenAI,
			label: i18n.T("tui.connectRowCustomOpenAI"),
			typ:   "openai",
		},
		connectRow{
			kind:  connectRowCustomAnthropic,
			label: i18n.T("tui.connectRowCustomAnthropic"),
			typ:   "anthropic",
		},
	)
	for i := range providerPresets {
		p := providerPresets[i]
		if meta, ok := connectOAuthCatalog[p.typ]; ok {
			modelHint, defaultModel, defaultModelAlias := p.modelHint, p.defaultModel, p.defaultModelAlias
			if meta.modelHint != "" {
				modelHint = meta.modelHint
			}
			if meta.defaultModel != "" {
				defaultModel = meta.defaultModel
			}
			if meta.defaultModelAlias != "" {
				defaultModelAlias = meta.defaultModelAlias
			}
			rows = append(rows, connectRow{
				kind:              connectRowOAuth,
				label:             i18n.T(meta.label),
				typ:               p.typ,
				name:              meta.provider,
				apiBase:           p.apiBase,
				modelHint:         modelHint,
				defaultModel:      defaultModel,
				defaultModelAlias: defaultModelAlias,
				oauthName:         meta.provider,
			})
		}
		rows = append(rows, connectRow{
			kind:              connectRowAPIKey,
			label:             p.label,
			typ:               p.typ,
			name:              p.typ,
			apiBase:           p.apiBase,
			keyHint:           p.keyHint,
			modelHint:         p.modelHint,
			defaultModel:      p.defaultModel,
			defaultModelAlias: p.defaultModelAlias,
		})
	}
	rows = append(rows, connectRow{
		kind:  connectRowFreeType,
		label: i18n.T("tui.connectCustomType"),
	})
	return rows
}

func connectRowAt(i int) *connectRow {
	if i < 0 {
		return nil
	}
	rows := connectRows()
	if i >= len(rows) {
		return nil
	}
	return &rows[i]
}

func connectRowByIndex(i int) (connectRow, bool) {
	if i < 0 {
		return connectRow{}, false
	}
	rows := connectRows()
	if i >= len(rows) {
		return connectRow{}, false
	}
	return rows[i], true
}
