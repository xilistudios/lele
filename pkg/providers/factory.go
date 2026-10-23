package providers

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	anthropicmessages "github.com/xilistudios/lele/pkg/providers/anthropic_messages"
	"github.com/xilistudios/lele/pkg/providers/openai_compat"
)

const defaultAnthropicAPIBase = "https://api.anthropic.com/v1"

var getCredential = auth.GetCredential

type providerType int

const (
	providerTypeHTTPCompat providerType = iota
	providerTypeClaudeAuth
	providerTypeCodexAuth
	providerTypeCodexCLIToken
	providerTypeClaudeCLI
	providerTypeCodexCLI
	providerTypeGitHubCopilot
	providerTypeAnthropic
	// providerTypeGeminiAuth is the Google Code Assist OAuth transport (the
	// "Gemini CLI" free-tier flow). Like providerTypeClaudeAuth it carries no
	// API key, but it speaks the Cloud Code Assist API
	// (cloudcode-pa.googleapis.com) instead of an OpenAI/Anthropic-compatible
	// endpoint. Appended at the end so no existing constant value shifts.
	providerTypeGeminiAuth
)

type providerSelection struct {
	providerType    providerType
	apiKey          string
	apiBase         string
	proxy           string
	model           string
	workspace       string
	connectMode     string
	enableWebSearch bool
	// providerName is the configured provider key (the Named entry name, or the
	// provider key when no Named entry exists). Detection only — never used to
	// select a transport.
	providerName string
	// providerTypeName is the configured `type` string (Named.Type, else the
	// provider key). Detection only.
	providerTypeName string
	// headers are the provider's static custom request headers (config
	// `headers`), applied last by the transports so an explicit entry wins
	// over transport defaults. Values may contain
	// protocoltypes.SessionHeaderPlaceholder, expanded per call.
	headers map[string]string
}

// defaultAPIBaseByType returns the default API base URL for a provider type.
// The catalog is the single source of truth for known providers; a few
// lele-specific overrides are checked first so existing behavior is preserved.
func defaultAPIBaseByType(providerType string) string {
	// Intentional lele overrides that must win over catalog defaults.
	switch providerType {
	case "github_copilot", "copilot":
		return "localhost:4321"
	case "alibaba":
		// lele historically uses the international coding-plan endpoint.
		return "https://coding-intl.dashscope.aliyuncs.com/v1"
	case "zai_coding_plan", "zai":
		return "https://api.z.ai/api/paas/v4"
	}
	if base := catalog.DefaultAPIBaseByType(providerType); base != "" {
		return base
	}
	return ""
}

// openCodeGoSessionHeaderName is the header the OpenCode Go gateway requires on
// inference calls; without it every request fails with 400 MissingSessionID
// (validated before auth).
const openCodeGoSessionHeaderName = "x-opencode-session"

// openCodeGoSessionHeader returns the session header name for this selection
// (or "" for every other target) and whether a missing session id must fail
// the call fast or be skipped silently.
//
// required=true only for detection from name/type: configuring a provider as
// `opencode_go` / `opencode-go` is explicit user intent to talk to the OpenCode
// Go gateway, so a call site that forgot providers.WithWireSessionID is a local
// programming error and must surface as common.ErrMissingWireSession instead of
// an opaque upstream 400.
//
// required=false for detection from api_base (URL sniff): the URL only says
// "this might be the gateway", so the header is opportunistic — sent when the
// call carries a session id, silently omitted when it does not. That keeps the
// sniff inert for unkeyed egresses (a lookalike host no longer matches at all
// — see isOpenCodeGoAPIBase), and FIX-B keeps it from ever failing the call
// (review MAYOR-1).
func openCodeGoSessionHeader(sel *providerSelection) (name string, required bool) {
	for _, s := range []string{sel.providerName, sel.providerTypeName} {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "opencode_go", "opencode-go":
			return openCodeGoSessionHeaderName, true
		}
	}
	if isOpenCodeGoAPIBase(sel.apiBase) {
		return openCodeGoSessionHeaderName, false
	}
	return "", false
}

// isOpenCodeGoAPIBase reports whether apiBase points at an OpenCode Go route.
//
// Parsed, not a substring: the apiBase is parsed with net/url and BOTH halves
// must match.
//
// Host: exactly "opencode.ai" or one of its subdomains, any port (so an
// explicit ":443" in api_base matches — review NIT-2, where the old literal
// substring missed "https://opencode.ai:443/zen/go/v1" and the header was
// silently omitted, producing an upstream 400 MissingSessionID).
//
// Path: one of the Go routes that identify the gateway — /zen/go/... (the
// canonical documented base) and /inference/go/... (the OpenAI alias) — with
// a segment boundary ("/zen/gopher" and "/zen/go-v2" are not "/zen/go"), and
// case-insensitively, so the first-class Zen base
// https://opencode.ai/zen/v1 does NOT match. Query and fragment are split off
// by the parser, so "/zen/go?x=1" still matches exactly like before.
//
// Two deliberate consequences of the real host check, both pinned in
// TestOpenCodeGoSessionHeader:
//   - a lookalike host carrying the Go path
//     (https://proxy.example.com/opencode.ai/zen/go) NO LONGER matches at
//     all — strictly better than the old substring residual, which matched
//     but only ever yielded an inert opportunistic header;
//   - an apiBase net/url cannot parse falls back to no-match (no header)
//     instead of matching by accident.
func isOpenCodeGoAPIBase(apiBase string) bool {
	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "opencode.ai" && !strings.HasSuffix(host, ".opencode.ai") {
		return false
	}
	path := strings.ToLower(u.Path)
	for _, route := range []string{"/zen/go", "/inference/go"} {
		// Path-segment boundary: the exact route or a subpath of it, never a
		// longer segment (e.g. "/zen/gopher" is not "/zen/go").
		if path == route || strings.HasPrefix(path, route+"/") {
			return true
		}
	}
	return false
}

func selectionFromNamedProvider(cfg *config.Config, providerName, model string, named config.NamedProviderConfig) (providerSelection, error) {
	typ := named.Type
	if typ == "" {
		typ = providerName
	}
	sel := providerSelection{
		providerType:     providerTypeHTTPCompat,
		apiKey:           named.APIKey,
		apiBase:          named.APIBase,
		proxy:            named.Proxy,
		model:            model,
		connectMode:      named.ConnectMode,
		providerName:     providerName,
		providerTypeName: typ,
		headers:          named.Headers,
	}

	if mc, ok := named.Models[model]; ok {
		if strings.TrimSpace(mc.Model) != "" {
			sel.model = strings.TrimSpace(mc.Model)
		}
	}
	// If not found by exact key, try matching against model values
	if sel.model == model {
		lowerModel := strings.ToLower(strings.ReplaceAll(model, ".", "-"))
		for _, aliasVal := range named.Models {
			resolvedVal := strings.ToLower(strings.TrimSpace(aliasVal.Model))
			if resolvedVal == lowerModel || strings.HasSuffix(resolvedVal, "/"+lowerModel) {
				if strings.TrimSpace(aliasVal.Model) != "" {
					sel.model = strings.TrimSpace(aliasVal.Model)
				}
				break
			}
		}
	}
	if sel.apiBase == "" {
		sel.apiBase = defaultAPIBaseByType(typ)
	}

	switch typ {
	case "openai", "gpt":
		sel.enableWebSearch = true
		if named.WebSearch != nil {
			sel.enableWebSearch = *named.WebSearch
		}
		switch named.AuthMethod {
		case "codex-cli":
			sel.providerType = providerTypeCodexCLIToken
			return sel, nil
		case "oauth", "token":
			sel.providerType = providerTypeCodexAuth
			return sel, nil
		}
	case "anthropic", "claude":
		if named.AuthMethod == "oauth" || named.AuthMethod == "token" {
			if sel.apiBase == "" {
				sel.apiBase = defaultAnthropicAPIBase
			}
			sel.providerType = providerTypeClaudeAuth
			return sel, nil
		}
		if sel.apiKey != "" {
			if sel.apiBase == "" {
				sel.apiBase = defaultAnthropicAPIBase
			}
			sel.providerType = providerTypeAnthropic
			return sel, nil
		}
	case "gemini":
		// Gemini / Google Code Assist OAuth, decided from the Named entry for
		// belt-and-braces symmetry with the anthropic case above, which also
		// reads named.AuthMethod: a caller whose decision data lives only in
		// Providers.Named["gemini"] gets the Code Assist transport here, just as
		// the cfg.Providers.Gemini pre-check in resolveProviderSelectionByName
		// gives it to a caller whose data lives in the typed field.
		//
		// No known or shipped production caller produces that shape, so this
		// case is defence-in-depth for a future Named-only caller rather than a
		// live fix: every Config an ordinary production path hands to the
		// selector carries the typed Providers.Gemini field too.
		// config.LoadConfig unmarshals the flat "providers.gemini" object and
		// ProvidersConfig.UnmarshalJSON fills the typed field next to
		// Providers.Named (pkg/config/config.go); EditableDocument.ToConfig()
		// round-trips through that same UnmarshalJSON, so its output is already
		// served by the pre-check; and the auth login/logout writer in cmd/lele
		// writes the typed field and mirrors auth_method into both copies via
		// setNamedAuthMethod. The Named-entry-only shape is what this case
		// covers.
		//
		// Nothing here *guarantees* the typed field is filled, which is why the
		// case is kept: a hand-edited config carrying two case-variant keys for
		// one provider ("Gemini" and "gemini") decodes through the alias merge
		// in ProvidersConfig.UnmarshalJSON and can leave Named["gemini"] holding
		// auth_method while the typed field is emptied. For that config this
		// case is what still selects the Code Assist transport.
		//
		// Condition mirrors the documented contract: the Code Assist transport
		// is selected only when auth_method == "oauth" AND no API key is
		// configured. An api_key, an unset auth_method, or "token" keeps the
		// developer API-key path byte-identical — the case deliberately does NOT
		// return in that case, it falls through to the generic tail below, which
		// resolves defaultAPIBaseByType("gemini") exactly as before.
		//
		// Only "gemini" is matched: NormalizeProvider maps the "google" spelling
		// to "gemini" before the selection starts (model_ref.go), and the raw
		// un-normalized "google" spelling (CreateProviderForCandidate with a
		// "google/<model>" prefix) finds no built-in GetNamed("google") entry
		// (ok=false), so it never arrives here spelled "google" — it is handled
		// by the pre-check in resolveProviderSelectionByName instead. The two
		// selection sites are therefore intentionally not redundant: the
		// pre-check owns the typed field (and the raw "google" spelling, which
		// GetNamed cannot resolve), while this case is its Named-entry
		// counterpart.
		if named.AuthMethod == "oauth" && sel.apiKey == "" {
			sel.providerType = providerTypeGeminiAuth
			return sel, nil
		}
	case "claude-cli", "claude-code", "claudecode":
		workspace := cfg.WorkspacePath()
		if workspace == "" {
			workspace = "."
		}
		sel.providerType = providerTypeClaudeCLI
		sel.workspace = workspace
		return sel, nil
	case "codex-cli", "codex-code":
		workspace := cfg.WorkspacePath()
		if workspace == "" {
			workspace = "."
		}
		sel.providerType = providerTypeCodexCLI
		sel.workspace = workspace
		return sel, nil
	case "deepseek":
		if sel.model != "deepseek-chat" && sel.model != "deepseek-reasoner" {
			sel.model = "deepseek-chat"
		}
	case "github_copilot", "copilot":
		sel.providerType = providerTypeGitHubCopilot
		if sel.apiBase == "" {
			sel.apiBase = "localhost:4321"
		}
		return sel, nil
	}

	// MiniMax publishes an Anthropic-compatible Messages API at its default
	// /anthropic/v1 base. Any provider whose resolved base path contains
	// /anthropic is also treated as Anthropic Messages.
	if sel.providerType == providerTypeHTTPCompat &&
		(typ == "minimax" || typ == "minimax_cn" || strings.Contains(sel.apiBase, "/anthropic")) {
		sel.providerType = providerTypeAnthropic
		return sel, nil
	}

	if sel.providerType == providerTypeHTTPCompat {
		if sel.apiBase == "" {
			return providerSelection{}, fmt.Errorf("no API base configured for provider (model: %s)", model)
		}
		// Only require an API key when no explicit api_base was provided by the
		// user (i.e., we would fall back to a default cloud endpoint that needs
		// auth).  Named providers with an explicit api_base are often local
		// proxies or self-hosted servers that do not require authentication.
		if sel.apiKey == "" && !strings.HasPrefix(sel.model, "bedrock/") && named.APIBase == "" {
			return providerSelection{}, fmt.Errorf("no API key configured for provider (model: %s)", model)
		}
	}
	return sel, nil
}

func createClaudeAuthProvider(apiBase string) (LLMProvider, error) {
	if apiBase == "" {
		apiBase = defaultAnthropicAPIBase
	}
	cred, err := getCredential("anthropic")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for anthropic. Run: lele auth login --provider anthropic")
	}
	oauth := cred.AuthMethod == "oauth"
	return NewClaudeProviderWithTokenSourceBaseURLAndOAuthMode(cred.AccessToken, createClaudeTokenSource(), apiBase, oauth), nil
}

// createGeminiAuthProvider builds the Gemini Code Assist provider selected when
// providers.gemini.auth_method is "oauth" and no API key is set. It is the
// Gemini sibling of createClaudeAuthProvider: the credential is read from the
// store under the canonical key "gemini" (the key `lele auth login --provider
// gemini` writes, also reachable through the "gemini-cli" alias) and every
// request resolves its bearer token through createGeminiTokenSource(), so
// concurrent turns collapse into at most one refresh.
//
// apiBase is accepted for signature symmetry with createClaudeAuthProvider —
// createProviderFromSelection hands both builders sel.apiBase — but Code Assist
// is not an OpenAI-compatible endpoint a user can repoint: every request is
// built from the provider's fixed geminiCodeAssistBaseURL
// (cloudcode-pa.googleapis.com), so a configured providers.gemini.api_base has
// no effect on this path.
func createGeminiAuthProvider(apiBase string) (LLMProvider, error) {
	cred, err := getCredential("gemini")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for gemini. Run: lele auth login --provider gemini")
	}
	return NewGeminiCodeAssistProvider(cred.AccessToken, createGeminiTokenSource()), nil
}

func createCodexAuthProvider(enableWebSearch bool) (LLMProvider, error) {
	cred, err := getCredential("openai")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for openai. Run: lele auth login --provider openai")
	}
	p := NewCodexProviderWithTokenSource(cred.AccessToken, cred.AccountID, createCodexTokenSource())
	p.enableWebSearch = enableWebSearch
	return p, nil
}

func resolveProviderSelection(cfg *config.Config) (providerSelection, error) {
	// Handle nil Providers
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}

	rawModel := strings.TrimSpace(cfg.Agents.Defaults.Model)
	providerName := NormalizeProvider(cfg.Agents.Defaults.Provider)
	model := rawModel

	resolvedModel := cfg.Providers.ResolveModelAlias(rawModel, providerName)
	if ref := ParseModelRef(resolvedModel, providerName); ref != nil {
		model = ref.Model

		defaultProvider := NormalizeProvider(config.DefaultConfig().Agents.Defaults.Provider)
		if providerName == "" || providerName == defaultProvider || ref.Provider == providerName {
			providerName = ref.Provider
		}
		// OpenRouter requires fully qualified model names like
		// "deepseek/deepseek-v4-pro". ParseModelRef already correctly
		// extracts the model name from both "openrouter:deepseek/deepseek-v4-pro"
		// (colon format) and "openrouter/deepseek/deepseek-v4-pro" (slash format).
		// ref.Model always contains the correct model name for the API.
		if providerName == "openrouter" {
			model = ref.Model
		}
	}
	return resolveProviderSelectionByName(cfg, providerName, model)
}

// resolveProviderSelectionForProvider resolves provider selection for a specific provider name
func resolveProviderSelectionForProvider(cfg *config.Config, providerName string) (providerSelection, error) {
	return resolveProviderSelectionByName(cfg, providerName, "")
}

func resolveProviderSelectionByName(cfg *config.Config, providerName string, model string) (providerSelection, error) {
	// Handle nil Providers
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}

	lowerModel := strings.ToLower(model)

	sel := providerSelection{
		providerType:     providerTypeHTTPCompat,
		model:            model,
		providerName:     providerName,
		providerTypeName: providerName,
	}

	// First, prefer explicit provider configuration.
	if providerName != "" {
		// Gemini / Google Code Assist OAuth. Providers.Gemini is the single
		// config behind BOTH spellings of this provider: NormalizeProvider maps
		// "google" to "gemini" before the selection starts, and an un-normalized
		// "google" (CreateProviderForCandidate, e.g. the raw "google/model"
		// prefix used by a fallback candidate) reaches the `case "gemini",
		// "google"` switch below — both read cfg.Providers.Gemini, so testing the
		// two spellings here keeps them interchangeable rather than giving each
		// one its own transport.
		//
		// Mirroring the anthropic OAuth branch: only auth_method "oauth" WITHOUT
		// an API key switches to the Code Assist transport, so an unset
		// auth_method, "token", or a configured API key keeps the API-key path
		// byte-identical.
		//
		// The check deliberately lives here and not inside `case "gemini",
		// "google"`: GetNamed always resolves a built-in provider name
		// (ensureNamedDefaults seeds "gemini" from Providers.Gemini), so
		// providerName == "gemini" returns through selectionFromNamedProvider
		// before that switch is ever reached. A check placed there would leave
		// the main flow picking the API-key transport and failing with "no API
		// key configured for provider".
		if (providerName == "gemini" || providerName == "google") &&
			cfg.Providers.Gemini.AuthMethod == "oauth" && cfg.Providers.Gemini.APIKey == "" {
			sel.providerType = providerTypeGeminiAuth
			return sel, nil
		}
		if named, ok := cfg.Providers.GetNamed(providerName); ok {
			return selectionFromNamedProvider(cfg, providerName, model, named)
		}
		// Custom headers: ONE copy for every legacy arm below.
		//
		// Built-in providers are always seeded into Providers.Named by
		// ensureNamedDefaults (GetNamed calls it), and the seeded copy carries the
		// typed field's `headers` — so the GetNamed return above already delivers
		// the map for a canonical name, and selectionFromNamedProvider is the only
		// selection constructor that needs it.
		//
		// The switch below is therefore reachable only for spellings GetNamed
		// cannot resolve: the aliases it accepts itself ("claude", "gpt", "glm",
		// "google", …) — CreateProviderForCandidate does not normalize its input.
		// Those arms hand-build the selection from the typed Providers.<Name>
		// fields, which is exactly where `headers` lives for a config written as
		// "providers": {"anthropic": {…}}, so without this copy the map would be
		// silently dropped there.
		//
		// This copy is exact only where NormalizeProvider(alias) equals the seeded
		// Named key derived from the SAME typed field the arm reads ("gpt" →
		// "openai", "claude" → "anthropic", "glm" → "zhipu", "google" → "gemini").
		// It is NOT a general collapse of the ~20 case arms: three spellings
		// normalize to themselves while the seeded key is longer
		// ("zai" vs "zai_coding_plan", "modelark" vs "modelark_coding_plan",
		// "copilot" vs "github_copilot"), so those arms are fixed explicitly below
		// (see the MINOR-1 audit in the accompanying report).
		if named, ok := cfg.Providers.GetNamed(NormalizeProvider(providerName)); ok {
			sel.headers = named.Headers
		}
		switch providerName {
		case "groq":
			if cfg.Providers.Groq.APIKey != "" {
				sel.apiKey = cfg.Providers.Groq.APIKey
				sel.apiBase = cfg.Providers.Groq.APIBase
				sel.proxy = cfg.Providers.Groq.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://api.groq.com/openai/v1"
				}
			}
		case "openai", "gpt":
			if cfg.Providers.OpenAI.AuthMethod == "codex-cli" && cfg.Providers.OpenAI.APIKey == "" {
				sel.providerType = providerTypeCodexCLIToken
				return sel, nil
			}
			if (cfg.Providers.OpenAI.AuthMethod == "oauth" || cfg.Providers.OpenAI.AuthMethod == "token") && cfg.Providers.OpenAI.APIKey == "" {
				sel.providerType = providerTypeCodexAuth
				return sel, nil
			}
			if cfg.Providers.OpenAI.APIKey != "" || cfg.Providers.OpenAI.AuthMethod != "" {
				sel.enableWebSearch = cfg.Providers.OpenAI.WebSearch
				sel.apiKey = cfg.Providers.OpenAI.APIKey
				sel.apiBase = cfg.Providers.OpenAI.APIBase
				sel.proxy = cfg.Providers.OpenAI.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://api.openai.com/v1"
				}
			}
		case "anthropic", "claude":
			if (cfg.Providers.Anthropic.AuthMethod == "oauth" || cfg.Providers.Anthropic.AuthMethod == "token") && cfg.Providers.Anthropic.APIKey == "" {
				sel.apiBase = cfg.Providers.Anthropic.APIBase
				if sel.apiBase == "" {
					sel.apiBase = defaultAnthropicAPIBase
				}
				sel.providerType = providerTypeClaudeAuth
				return sel, nil
			}
			if cfg.Providers.Anthropic.APIKey != "" || cfg.Providers.Anthropic.AuthMethod != "" {
				sel.apiKey = cfg.Providers.Anthropic.APIKey
				sel.apiBase = cfg.Providers.Anthropic.APIBase
				sel.proxy = cfg.Providers.Anthropic.Proxy
				sel.providerType = providerTypeAnthropic
				if sel.apiBase == "" {
					sel.apiBase = defaultAnthropicAPIBase
				}
			}
		case "openrouter":
			if cfg.Providers.OpenRouter.APIKey != "" {
				sel.apiKey = cfg.Providers.OpenRouter.APIKey
				sel.proxy = cfg.Providers.OpenRouter.Proxy
				if cfg.Providers.OpenRouter.APIBase != "" {
					sel.apiBase = cfg.Providers.OpenRouter.APIBase
				} else {
					sel.apiBase = "https://openrouter.ai/api/v1"
				}
			}
		case "nanogpt":
			if named, ok := cfg.Providers.GetNamed("nanogpt"); ok && named.APIKey != "" {
				sel.apiKey = named.APIKey
				sel.apiBase = named.APIBase
				sel.proxy = named.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://nano-gpt.com/api/v1"
				}
			}
		case "chutes":
			if named, ok := cfg.Providers.GetNamed("chutes"); ok && named.APIKey != "" {
				sel.apiKey = named.APIKey
				sel.apiBase = named.APIBase
				sel.proxy = named.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://llm.chutes.ai/v1"
				}
			}
		case "alibaba":
			if named, ok := cfg.Providers.GetNamed("alibaba"); ok && named.APIKey != "" {
				sel.apiKey = named.APIKey
				sel.apiBase = named.APIBase
				sel.proxy = named.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://coding-intl.dashscope.aliyuncs.com/v1"
				}
			}
		case "zhipu", "glm":
			if cfg.Providers.Zhipu.APIKey != "" {
				sel.apiKey = cfg.Providers.Zhipu.APIKey
				sel.apiBase = cfg.Providers.Zhipu.APIBase
				sel.proxy = cfg.Providers.Zhipu.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://open.bigmodel.cn/api/paas/v4"
				}
			}
		case "gemini", "google":
			// The Code Assist OAuth selection happens above, at the top of the
			// `providerName != ""` branch (see the gemini pre-check): the
			// normalized spellings never reach this case, and the raw "google"
			// that does is routed through that same pre-check. What is left here
			// is the unchanged API-key path.
			if cfg.Providers.Gemini.APIKey != "" {
				sel.apiKey = cfg.Providers.Gemini.APIKey
				sel.apiBase = cfg.Providers.Gemini.APIBase
				sel.proxy = cfg.Providers.Gemini.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://generativelanguage.googleapis.com/v1beta"
				}
			}
		case "vllm":
			if cfg.Providers.VLLM.APIBase != "" {
				sel.apiKey = cfg.Providers.VLLM.APIKey
				sel.apiBase = cfg.Providers.VLLM.APIBase
				sel.proxy = cfg.Providers.VLLM.Proxy
			}
		case "shengsuanyun":
			if cfg.Providers.ShengSuanYun.APIKey != "" {
				sel.apiKey = cfg.Providers.ShengSuanYun.APIKey
				sel.apiBase = cfg.Providers.ShengSuanYun.APIBase
				sel.proxy = cfg.Providers.ShengSuanYun.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://router.shengsuanyun.com/api/v1"
				}
			}
		case "nvidia":
			if cfg.Providers.Nvidia.APIKey != "" {
				sel.apiKey = cfg.Providers.Nvidia.APIKey
				sel.apiBase = cfg.Providers.Nvidia.APIBase
				sel.proxy = cfg.Providers.Nvidia.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://integrate.api.nvidia.com/v1"
				}
			}
		case "claude-cli", "claude-code", "claudecode":
			workspace := cfg.WorkspacePath()
			if workspace == "" {
				workspace = "."
			}
			sel.providerType = providerTypeClaudeCLI
			sel.workspace = workspace
			return sel, nil
		case "codex-cli", "codex-code":
			workspace := cfg.WorkspacePath()
			if workspace == "" {
				workspace = "."
			}
			sel.providerType = providerTypeCodexCLI
			sel.workspace = workspace
			return sel, nil
		case "deepseek":
			if cfg.Providers.DeepSeek.APIKey != "" {
				sel.apiKey = cfg.Providers.DeepSeek.APIKey
				sel.apiBase = cfg.Providers.DeepSeek.APIBase
				sel.proxy = cfg.Providers.DeepSeek.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://api.deepseek.com/v1"
				}
				if !strings.HasPrefix(model, "deepseek-") {
					sel.model = "deepseek-chat"
				}
			}
		case "github_copilot", "copilot":
			// MINOR-1 audit: "copilot" is reachable here (GetNamed has no
			// "copilot" key) and NormalizeProvider does NOT collapse it to the
			// seeded "github_copilot", so the single copy above leaves
			// sel.headers empty while this arm reads the typed field. Carried
			// for uniformity: the copilot transport speaks gRPC/stdio through
			// the copilot SDK and reads no HTTP headers today, so this copy
			// changes nothing on the wire — it keeps the selection faithful to
			// config so a future header-aware copilot transport cannot inherit
			// the drop by accident.
			sel.headers = cfg.Providers.GitHubCopilot.Headers
			sel.providerType = providerTypeGitHubCopilot
			if cfg.Providers.GitHubCopilot.APIBase != "" {
				sel.apiBase = cfg.Providers.GitHubCopilot.APIBase
			} else {
				sel.apiBase = "localhost:4321"
			}
			sel.connectMode = cfg.Providers.GitHubCopilot.ConnectMode
			return sel, nil
		case "zai_coding_plan", "zai":
			// MINOR-1 audit: NormalizeProvider("zai") == "zai" while
			// ensureNamedDefaults seeds the key "zai_coding_plan", so the single
			// copy above misses this arm and the alias silently dropped the
			// typed field's headers. Copy it explicitly.
			sel.headers = cfg.Providers.ZAICodingPlan.Headers
			if cfg.Providers.ZAICodingPlan.APIKey != "" {
				sel.apiKey = cfg.Providers.ZAICodingPlan.APIKey
				sel.apiBase = cfg.Providers.ZAICodingPlan.APIBase
				sel.proxy = cfg.Providers.ZAICodingPlan.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://api.z.ai/api/paas/v4"
				}
			}
		case "modelark_coding_plan", "modelark":
			// MINOR-1 audit: same shape as "zai" above —
			// NormalizeProvider("modelark") == "modelark" but the seeded Named
			// key is "modelark_coding_plan", so the single copy misses this arm
			// and the alias silently dropped the typed field's headers.
			sel.headers = cfg.Providers.ModelArkCodingPlan.Headers
			if cfg.Providers.ModelArkCodingPlan.APIKey != "" {
				sel.apiKey = cfg.Providers.ModelArkCodingPlan.APIKey
				sel.apiBase = cfg.Providers.ModelArkCodingPlan.APIBase
				sel.proxy = cfg.Providers.ModelArkCodingPlan.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://ark.ap-southeast.bytepluses.com/api/coding/v3"
				}
			}
		}
	}

	// Fallback: infer provider from model and configured keys.
	if sel.apiKey == "" && sel.apiBase == "" {
		if ref := ParseModelRef(model, ""); ref != nil {
			if named, ok := cfg.Providers.GetNamed(ref.Provider); ok {
				if named.APIKey != "" || ref.Provider == "openai" || ref.Provider == "anthropic" {
					return selectionFromNamedProvider(cfg, ref.Provider, ref.Model, named)
				}
			}
		}
		switch {
		case (strings.Contains(lowerModel, "kimi") || strings.Contains(lowerModel, "moonshot") || strings.HasPrefix(model, "moonshot/")) && cfg.Providers.Moonshot.APIKey != "":
			sel.apiKey = cfg.Providers.Moonshot.APIKey
			sel.apiBase = cfg.Providers.Moonshot.APIBase
			sel.proxy = cfg.Providers.Moonshot.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://api.moonshot.cn/v1"
			}
		case strings.HasPrefix(model, "openrouter/") ||
			strings.HasPrefix(model, "anthropic/") ||
			strings.HasPrefix(model, "openai/") ||
			strings.HasPrefix(model, "meta-llama/") ||
			strings.HasPrefix(model, "deepseek/") ||
			strings.HasPrefix(model, "google/"):
			sel.apiKey = cfg.Providers.OpenRouter.APIKey
			sel.proxy = cfg.Providers.OpenRouter.Proxy
			if cfg.Providers.OpenRouter.APIBase != "" {
				sel.apiBase = cfg.Providers.OpenRouter.APIBase
			} else {
				sel.apiBase = "https://openrouter.ai/api/v1"
			}
		case (strings.Contains(lowerModel, "claude") || strings.HasPrefix(model, "anthropic/")) &&
			(cfg.Providers.Anthropic.APIKey != "" || cfg.Providers.Anthropic.AuthMethod != ""):
			if cfg.Providers.Anthropic.AuthMethod == "oauth" || cfg.Providers.Anthropic.AuthMethod == "token" {
				sel.apiBase = cfg.Providers.Anthropic.APIBase
				if sel.apiBase == "" {
					sel.apiBase = defaultAnthropicAPIBase
				}
				sel.providerType = providerTypeClaudeAuth
				return sel, nil
			}
			sel.apiKey = cfg.Providers.Anthropic.APIKey
			sel.apiBase = cfg.Providers.Anthropic.APIBase
			sel.proxy = cfg.Providers.Anthropic.Proxy
			if sel.apiBase == "" {
				sel.apiBase = defaultAnthropicAPIBase
			}
		case (strings.Contains(lowerModel, "gpt") || strings.HasPrefix(model, "openai/")) &&
			(cfg.Providers.OpenAI.APIKey != "" || cfg.Providers.OpenAI.AuthMethod != ""):
			sel.enableWebSearch = cfg.Providers.OpenAI.WebSearch
			if cfg.Providers.OpenAI.AuthMethod == "codex-cli" {
				sel.providerType = providerTypeCodexCLIToken
				return sel, nil
			}
			if cfg.Providers.OpenAI.AuthMethod == "oauth" || cfg.Providers.OpenAI.AuthMethod == "token" {
				sel.providerType = providerTypeCodexAuth
				return sel, nil
			}
			sel.apiKey = cfg.Providers.OpenAI.APIKey
			sel.apiBase = cfg.Providers.OpenAI.APIBase
			sel.proxy = cfg.Providers.OpenAI.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://api.openai.com/v1"
			}
		case (strings.Contains(lowerModel, "gemini") || strings.HasPrefix(model, "google/")) && cfg.Providers.Gemini.APIKey != "":
			sel.apiKey = cfg.Providers.Gemini.APIKey
			sel.apiBase = cfg.Providers.Gemini.APIBase
			sel.proxy = cfg.Providers.Gemini.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://generativelanguage.googleapis.com/v1beta"
			}
		case (strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "zhipu") || strings.Contains(lowerModel, "zai")) && cfg.Providers.Zhipu.APIKey != "":
			sel.apiKey = cfg.Providers.Zhipu.APIKey
			sel.apiBase = cfg.Providers.Zhipu.APIBase
			sel.proxy = cfg.Providers.Zhipu.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://open.bigmodel.cn/api/paas/v4"
			}
		case (strings.Contains(lowerModel, "groq") || strings.HasPrefix(model, "groq/")) && cfg.Providers.Groq.APIKey != "":
			sel.apiKey = cfg.Providers.Groq.APIKey
			sel.apiBase = cfg.Providers.Groq.APIBase
			sel.proxy = cfg.Providers.Groq.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://api.groq.com/openai/v1"
			}
		case (strings.Contains(lowerModel, "nvidia") || strings.HasPrefix(model, "nvidia/")) && cfg.Providers.Nvidia.APIKey != "":
			sel.apiKey = cfg.Providers.Nvidia.APIKey
			sel.apiBase = cfg.Providers.Nvidia.APIBase
			sel.proxy = cfg.Providers.Nvidia.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://integrate.api.nvidia.com/v1"
			}
		case (strings.Contains(lowerModel, "ollama") || strings.HasPrefix(model, "ollama/")) && cfg.Providers.Ollama.APIKey != "":
			sel.apiKey = cfg.Providers.Ollama.APIKey
			sel.apiBase = cfg.Providers.Ollama.APIBase
			sel.proxy = cfg.Providers.Ollama.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "http://localhost:11434/v1"
			}
		case cfg.Providers.VLLM.APIBase != "":
			sel.apiKey = cfg.Providers.VLLM.APIKey
			sel.apiBase = cfg.Providers.VLLM.APIBase
			sel.proxy = cfg.Providers.VLLM.Proxy
		default:
			if cfg.Providers.OpenRouter.APIKey != "" {
				sel.apiKey = cfg.Providers.OpenRouter.APIKey
				sel.proxy = cfg.Providers.OpenRouter.Proxy
				if cfg.Providers.OpenRouter.APIBase != "" {
					sel.apiBase = cfg.Providers.OpenRouter.APIBase
				} else {
					sel.apiBase = "https://openrouter.ai/api/v1"
				}
			} else {
				return providerSelection{}, fmt.Errorf("no API key configured for model: %s", model)
			}
		}
	}

	// MiniMax / Anthropic-path bases use the Anthropic Messages transport.
	if sel.providerType == providerTypeHTTPCompat && sel.apiBase != "" &&
		(providerName == "minimax" || providerName == "minimax_cn" || strings.Contains(sel.apiBase, "/anthropic")) {
		sel.providerType = providerTypeAnthropic
	}

	if sel.providerType == providerTypeHTTPCompat {
		if sel.apiKey == "" && !strings.HasPrefix(model, "bedrock/") {
			return providerSelection{}, fmt.Errorf("no API key configured for provider (model: %s)", model)
		}
		if sel.apiBase == "" {
			return providerSelection{}, fmt.Errorf("no API base configured for provider (model: %s)", model)
		}
	}

	return sel, nil
}

func CreateProvider(cfg *config.Config) (LLMProvider, error) {
	sel, err := resolveProviderSelection(cfg)
	if err != nil {
		return nil, err
	}

	return createProviderFromSelection(&sel)
}

// CreateProviderForCandidate creates a provider for a specific fallback candidate
func CreateProviderForCandidate(cfg *config.Config, providerName string) (LLMProvider, error) {
	sel, err := resolveProviderSelectionForProvider(cfg, providerName)
	if err != nil {
		return nil, err
	}
	return createProviderFromSelection(&sel)
}

func createProviderFromSelection(sel *providerSelection) (LLMProvider, error) {
	sessionHeader, sessionHeaderRequired := openCodeGoSessionHeader(sel)
	switch sel.providerType {
	case providerTypeClaudeAuth:
		return createClaudeAuthProvider(sel.apiBase)
	case providerTypeGeminiAuth:
		return createGeminiAuthProvider(sel.apiBase)
	case providerTypeCodexAuth:
		return createCodexAuthProvider(sel.enableWebSearch)
	case providerTypeCodexCLIToken:
		c := NewCodexProviderWithTokenSource("", "", CreateCodexCliTokenSource())
		c.enableWebSearch = sel.enableWebSearch
		return c, nil
	case providerTypeClaudeCLI:
		return NewClaudeCliProvider(sel.workspace), nil
	case providerTypeCodexCLI:
		return NewCodexCliProvider(sel.workspace), nil
	case providerTypeGitHubCopilot:
		return NewGitHubCopilotProvider(sel.apiBase, sel.connectMode, sel.model)
	case providerTypeAnthropic:
		return anthropicmessages.NewProviderWithOptions(sel.apiKey, sel.apiBase, anthropicmessages.Options{SessionHeader: sessionHeader, SessionHeaderRequired: sessionHeaderRequired, Headers: sel.headers}), nil
	default:
		return NewHTTPProviderWithOptions(sel.apiKey, sel.apiBase, sel.proxy, openai_compat.Options{SessionHeader: sessionHeader, SessionHeaderRequired: sessionHeaderRequired, Headers: sel.headers}), nil
	}
}
