package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ToConfig converts the editable document to Config for validation.
func (doc *EditableDocument) ToConfig() (*Config, error) {
	cfg := DefaultConfig()

	// Copiar agents
	cfg.Agents.Defaults = AgentDefaults{
		Workspace:              doc.Agents.Defaults.Workspace,
		RestrictToWorkspace:    doc.Agents.Defaults.RestrictToWorkspace,
		Provider:               doc.Agents.Defaults.Provider,
		Model:                  doc.Agents.Defaults.Model,
		ModelFallbacks:         doc.Agents.Defaults.ModelFallbacks,
		ImageModel:             doc.Agents.Defaults.ImageModel,
		ImageModelFallbacks:    doc.Agents.Defaults.ImageModelFallbacks,
		MaxTokens:              doc.Agents.Defaults.MaxTokens,
		Temperature:            doc.Agents.Defaults.Temperature,
		MaxToolIterations:      doc.Agents.Defaults.MaxToolIterations,
		MaxReadLines:           doc.Agents.Defaults.MaxReadLines,
		SubagentTimeoutMinutes: doc.Agents.Defaults.SubagentTimeoutMinutes,
		SubagentMaxConcurrent:  doc.Agents.Defaults.SubagentMaxConcurrent,
		SubagentMaxRetries:     doc.Agents.Defaults.SubagentMaxRetries,
		LLMLoopTimeoutMinutes:  doc.Agents.Defaults.LLMLoopTimeoutMinutes,
	}

	for _, agent := range doc.Agents.List {
		cfg.Agents.List = append(cfg.Agents.List, AgentConfig{
			ID:        agent.ID,
			Default:   agent.Default,
			Name:      agent.Name,
			Workspace: agent.Workspace,
			Model:     agent.Model,
			Skills:    agent.Skills,
			Subagents: agent.Subagents,
		})
	}

	// Copy session.
	cfg.Session = SessionConfig{
		DMScope:                    doc.Session.DMScope,
		IdentityLinks:              doc.Session.IdentityLinks,
		Ephemeral:                  doc.Session.Ephemeral,
		EphemeralThreshold:         doc.Session.EphemeralThreshold,
		CompactionThresholdPercent: doc.Session.CompactionThresholdPercent,
		CompactionModel:            doc.Session.CompactionModel,
		EvictExcludedFromMemory:    doc.Session.EvictExcludedFromMemory,
		DurableInbound:             doc.Session.DurableInbound,
	}

	// Copiar bindings
	cfg.Bindings = doc.Bindings

	// Copiar groups
	cfg.Groups = doc.Groups

	// Copiar channels
	cfg.Channels.WhatsApp = WhatsAppConfig(doc.Channels.WhatsApp)
	cfg.Channels.Telegram = TelegramConfig{
		Enabled:   doc.Channels.Telegram.Enabled,
		Token:     doc.Channels.Telegram.Token.resolve(),
		Proxy:     doc.Channels.Telegram.Proxy,
		AllowFrom: doc.Channels.Telegram.AllowFrom,
		Verbose:   doc.Channels.Telegram.Verbose,
	}
	cfg.Channels.Discord = DiscordConfig{
		Enabled:   doc.Channels.Discord.Enabled,
		Token:     doc.Channels.Discord.Token.resolve(),
		AllowFrom: doc.Channels.Discord.AllowFrom,
	}
	cfg.Channels.Feishu = FeishuConfig{
		Enabled:           doc.Channels.Feishu.Enabled,
		AppID:             doc.Channels.Feishu.AppID.resolve(),
		AppSecret:         doc.Channels.Feishu.AppSecret.resolve(),
		EncryptKey:        doc.Channels.Feishu.EncryptKey.resolve(),
		VerificationToken: doc.Channels.Feishu.VerificationToken.resolve(),
		AllowFrom:         doc.Channels.Feishu.AllowFrom,
	}
	cfg.Channels.Slack = SlackConfig{
		Enabled:   doc.Channels.Slack.Enabled,
		BotToken:  doc.Channels.Slack.BotToken.resolve(),
		AppToken:  doc.Channels.Slack.AppToken.resolve(),
		AllowFrom: doc.Channels.Slack.AllowFrom,
	}
	cfg.Channels.LINE = LINEConfig{
		Enabled:            doc.Channels.LINE.Enabled,
		ChannelSecret:      doc.Channels.LINE.ChannelSecret.resolve(),
		ChannelAccessToken: doc.Channels.LINE.ChannelAccessToken.resolve(),
		WebhookHost:        doc.Channels.LINE.WebhookHost,
		WebhookPort:        doc.Channels.LINE.WebhookPort,
		WebhookPath:        doc.Channels.LINE.WebhookPath,
		AllowFrom:          doc.Channels.LINE.AllowFrom,
	}
	cfg.Channels.OneBot = OneBotConfig{
		Enabled:            doc.Channels.OneBot.Enabled,
		WSUrl:              doc.Channels.OneBot.WSUrl,
		AccessToken:        doc.Channels.OneBot.AccessToken.resolve(),
		ReconnectInterval:  doc.Channels.OneBot.ReconnectInterval,
		GroupTriggerPrefix: doc.Channels.OneBot.GroupTriggerPrefix,
		AllowFrom:          doc.Channels.OneBot.AllowFrom,
	}
	cfg.Channels.QQ = QQConfig{
		Enabled:   doc.Channels.QQ.Enabled,
		AppID:     doc.Channels.QQ.AppID.resolve(),
		AppSecret: doc.Channels.QQ.AppSecret.resolve(),
		AllowFrom: doc.Channels.QQ.AllowFrom,
	}
	cfg.Channels.DingTalk = DingTalkConfig{
		Enabled:      doc.Channels.DingTalk.Enabled,
		ClientID:     doc.Channels.DingTalk.ClientID.resolve(),
		ClientSecret: doc.Channels.DingTalk.ClientSecret.resolve(),
		AllowFrom:    doc.Channels.DingTalk.AllowFrom,
	}
	cfg.Channels.MaixCam = MaixCamConfig(doc.Channels.MaixCam)
	cfg.Channels.Native = NativeConfig{
		Enabled:           doc.Channels.Native.Enabled,
		Host:              doc.Channels.Native.Host,
		Port:              doc.Channels.Native.Port,
		TokenExpiryDays:   doc.Channels.Native.TokenExpiryDays,
		PinExpiryMinutes:  doc.Channels.Native.PinExpiryMinutes,
		MaxClients:        doc.Channels.Native.MaxClients,
		CORSOrigins:       doc.Channels.Native.CORSOrigins,
		SessionExpiryDays: doc.Channels.Native.SessionExpiryDays,
		MaxUploadSizeMB:   doc.Channels.Native.MaxUploadSizeMB,
		UploadTTLHours:    doc.Channels.Native.UploadTTLHours,
	}

	// Copiar providers
	cfg.Providers.Named = make(map[string]NamedProviderConfig)
	for name, provider := range doc.Providers {
		cfg.Providers.Named[name] = NamedProviderConfig{
			Type: provider.Type,
			ProviderConfig: ProviderConfig{
				APIKey:      provider.APIKey.resolve(),
				APIBase:     provider.APIBase,
				Proxy:       provider.Proxy,
				AuthMethod:  provider.AuthMethod,
				ConnectMode: provider.ConnectMode,
			},
			WebSearch: provider.WebSearch,
			Models:    provider.Models,
		}
	}

	// Copiar gateway
	cfg.Gateway = doc.Gateway

	// Copiar tools
	cfg.Tools.Web.Brave = BraveConfig{
		Enabled:    doc.Tools.Web.Brave.Enabled,
		APIKey:     doc.Tools.Web.Brave.APIKey.resolve(),
		MaxResults: doc.Tools.Web.Brave.MaxResults,
	}
	cfg.Tools.Web.DuckDuckGo = doc.Tools.Web.DuckDuckGo
	cfg.Tools.Web.Perplexity = PerplexityConfig{
		Enabled:    doc.Tools.Web.Perplexity.Enabled,
		APIKey:     doc.Tools.Web.Perplexity.APIKey.resolve(),
		MaxResults: doc.Tools.Web.Perplexity.MaxResults,
	}
	cfg.Tools.Web.SearXNG = SearXNGConfig{
		Enabled:     doc.Tools.Web.SearXNG.Enabled,
		InstanceURL: doc.Tools.Web.SearXNG.InstanceURL,
		Categories:  doc.Tools.Web.SearXNG.Categories,
		Language:    doc.Tools.Web.SearXNG.Language,
		SafeSearch:  doc.Tools.Web.SearXNG.SafeSearch,
		MaxResults:  doc.Tools.Web.SearXNG.MaxResults,
	}
	cfg.Tools.Cron = doc.Tools.Cron
	cfg.Tools.Exec = ExecConfig(doc.Tools.Exec)

	// Copiar heartbeat
	cfg.Heartbeat = doc.Heartbeat

	// Copy devices.
	cfg.Devices = doc.Devices

	// Copiar logs
	cfg.Logs = LogsConfig(doc.Logs)

	// Custom slash commands. The types are identical on both sides, so a
	// shallow copy is enough; the map is cloned to keep the document and the
	// resulting config independent.
	if len(doc.Commands) > 0 {
		cfg.Commands = make(map[string]CommandDefinition, len(doc.Commands))
		for name, def := range doc.Commands {
			cfg.Commands[name] = def
		}
	}
	cfg.Harness = doc.Harness

	data, err := json.Marshal(doc.toSerializable())
	if err != nil {
		return nil, err
	}
	validated := DefaultConfig()
	if err := json.Unmarshal(data, validated); err != nil {
		return nil, err
	}
	if validated.Session.EphemeralThreshold <= 0 {
		validated.Session.EphemeralThreshold = DefaultEphemeralThresholdSeconds
	}
	if validated.Session.CompactionThresholdPercent <= 0 || validated.Session.CompactionThresholdPercent > 100 {
		validated.Session.CompactionThresholdPercent = DefaultCompactionThresholdPercent
	}
	validated.Providers.ensureNamedDefaults()
	for providerName, providerCfg := range validated.Providers.Named {
		for modelName, modelCfg := range providerCfg.Models {
			if err := modelCfg.Validate(); err != nil {
				return nil, fmt.Errorf("provider %q, model %q: %w", providerName, modelName, err)
			}
		}
	}

	return validated, nil
}

// SaveEditableDocument guarda el documento editable en archivo
func SaveEditableDocument(path string, doc *EditableDocument) error {
	// Convert to a serializable format while preserving placeholders.
	serializable := doc.toSerializable()

	data, err := json.MarshalIndent(serializable, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write the file with restrictive permissions.
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// toSerializable converts the document to a serializable format
// that can include ENV placeholders.
func (doc *EditableDocument) toSerializable() map[string]interface{} {
	result := make(map[string]interface{})

	// Agents
	result["agents"] = map[string]interface{}{
		"defaults": map[string]interface{}{
			"workspace":                doc.Agents.Defaults.Workspace,
			"restrict_to_workspace":    doc.Agents.Defaults.RestrictToWorkspace,
			"provider":                 doc.Agents.Defaults.Provider,
			"model":                    doc.Agents.Defaults.Model,
			"model_fallbacks":          doc.Agents.Defaults.ModelFallbacks,
			"image_model":              doc.Agents.Defaults.ImageModel,
			"image_model_fallbacks":    doc.Agents.Defaults.ImageModelFallbacks,
			"max_tokens":               doc.Agents.Defaults.MaxTokens,
			"temperature":              doc.Agents.Defaults.Temperature,
			"max_tool_iterations":      doc.Agents.Defaults.MaxToolIterations,
			"subagent_timeout_minutes": doc.Agents.Defaults.SubagentTimeoutMinutes,
			"subagent_max_concurrent":  doc.Agents.Defaults.SubagentMaxConcurrent,
			"subagent_max_retries":     doc.Agents.Defaults.SubagentMaxRetries,
			"llm_loop_timeout_minutes": doc.Agents.Defaults.LLMLoopTimeoutMinutes,
		},
	}
	if len(doc.Agents.List) > 0 {
		result["agents"].(map[string]interface{})["list"] = doc.Agents.List
	}

	// Session.
	if doc.Session.DMScope != "" || doc.Session.Ephemeral || len(doc.Session.IdentityLinks) > 0 || doc.Session.CompactionModel != "" || doc.Session.EvictExcludedFromMemory ||
		doc.Session.DurableInbound != nil {
		session := map[string]interface{}{
			"ephemeral":                    doc.Session.Ephemeral,
			"ephemeral_threshold":          doc.Session.EphemeralThreshold,
			"compaction_threshold_percent": doc.Session.CompactionThresholdPercent,
			"compaction_model":             doc.Session.CompactionModel,
			"evict_excluded_from_memory":   doc.Session.EvictExcludedFromMemory,
		}
		// Tri-state: only write the key when the user configured it, so an
		// untouched file keeps inheriting the code default.
		if doc.Session.DurableInbound != nil {
			session["durable_inbound"] = *doc.Session.DurableInbound
		}
		if doc.Session.DMScope != "" {
			session["dm_scope"] = doc.Session.DMScope
		}
		if len(doc.Session.IdentityLinks) > 0 {
			session["identity_links"] = doc.Session.IdentityLinks
		}
		result["session"] = session
	}

	// Bindings
	if len(doc.Bindings) > 0 {
		result["bindings"] = doc.Bindings
	}

	// Groups
	if doc.Groups.Enabled || len(doc.Groups.List) > 0 {
		result["groups"] = doc.Groups
	}

	// Channels
	channels := make(map[string]interface{})

	// Native
	channels["native"] = map[string]interface{}{
		"enabled":             doc.Channels.Native.Enabled,
		"host":                doc.Channels.Native.Host,
		"port":                doc.Channels.Native.Port,
		"token_expiry_days":   doc.Channels.Native.TokenExpiryDays,
		"pin_expiry_minutes":  doc.Channels.Native.PinExpiryMinutes,
		"max_clients":         doc.Channels.Native.MaxClients,
		"cors_origins":        doc.Channels.Native.CORSOrigins,
		"session_expiry_days": doc.Channels.Native.SessionExpiryDays,
		"max_upload_size_mb":  doc.Channels.Native.MaxUploadSizeMB,
		"upload_ttl_hours":    doc.Channels.Native.UploadTTLHours,
	}

	// Telegram
	telegram := map[string]interface{}{
		"enabled":    doc.Channels.Telegram.Enabled,
		"proxy":      doc.Channels.Telegram.Proxy,
		"allow_from": doc.Channels.Telegram.AllowFrom,
		"verbose":    doc.Channels.Telegram.Verbose,
	}
	writeSecret(telegram, "token", doc.Channels.Telegram.Token)
	channels["telegram"] = telegram

	// Discord
	discord := map[string]interface{}{
		"enabled":    doc.Channels.Discord.Enabled,
		"allow_from": doc.Channels.Discord.AllowFrom,
	}
	writeSecret(discord, "token", doc.Channels.Discord.Token)
	channels["discord"] = discord

	// Feishu
	feishu := map[string]interface{}{
		"enabled":    doc.Channels.Feishu.Enabled,
		"allow_from": doc.Channels.Feishu.AllowFrom,
	}
	writeSecret(feishu, "app_id", doc.Channels.Feishu.AppID)
	writeSecret(feishu, "app_secret", doc.Channels.Feishu.AppSecret)
	channels["feishu"] = feishu

	// Slack
	slack := map[string]interface{}{
		"enabled":    doc.Channels.Slack.Enabled,
		"allow_from": doc.Channels.Slack.AllowFrom,
	}
	writeSecret(slack, "bot_token", doc.Channels.Slack.BotToken)
	channels["slack"] = slack

	// LINE
	line := map[string]interface{}{
		"enabled":      doc.Channels.LINE.Enabled,
		"webhook_host": doc.Channels.LINE.WebhookHost,
		"webhook_port": doc.Channels.LINE.WebhookPort,
		"webhook_path": doc.Channels.LINE.WebhookPath,
		"allow_from":   doc.Channels.LINE.AllowFrom,
	}
	writeSecret(line, "channel_secret", doc.Channels.LINE.ChannelSecret)
	writeSecret(line, "channel_access_token", doc.Channels.LINE.ChannelAccessToken)
	channels["line"] = line

	// OneBot
	onebot := map[string]interface{}{
		"enabled":              doc.Channels.OneBot.Enabled,
		"ws_url":               doc.Channels.OneBot.WSUrl,
		"reconnect_interval":   doc.Channels.OneBot.ReconnectInterval,
		"group_trigger_prefix": doc.Channels.OneBot.GroupTriggerPrefix,
		"allow_from":           doc.Channels.OneBot.AllowFrom,
	}
	writeSecret(onebot, "access_token", doc.Channels.OneBot.AccessToken)
	channels["onebot"] = onebot

	// QQ
	qq := map[string]interface{}{
		"enabled":    doc.Channels.QQ.Enabled,
		"allow_from": doc.Channels.QQ.AllowFrom,
	}
	writeSecret(qq, "app_id", doc.Channels.QQ.AppID)
	writeSecret(qq, "app_secret", doc.Channels.QQ.AppSecret)
	channels["qq"] = qq

	// DingTalk
	dingtalk := map[string]interface{}{
		"enabled":    doc.Channels.DingTalk.Enabled,
		"allow_from": doc.Channels.DingTalk.AllowFrom,
	}
	writeSecret(dingtalk, "client_id", doc.Channels.DingTalk.ClientID)
	writeSecret(dingtalk, "client_secret", doc.Channels.DingTalk.ClientSecret)
	channels["dingtalk"] = dingtalk

	// WhatsApp
	channels["whatsapp"] = map[string]interface{}{
		"enabled":    doc.Channels.WhatsApp.Enabled,
		"bridge_url": doc.Channels.WhatsApp.BridgeURL,
		"allow_from": doc.Channels.WhatsApp.AllowFrom,
	}

	// MaixCam
	channels["maixcam"] = map[string]interface{}{
		"enabled":    doc.Channels.MaixCam.Enabled,
		"host":       doc.Channels.MaixCam.Host,
		"port":       doc.Channels.MaixCam.Port,
		"allow_from": doc.Channels.MaixCam.AllowFrom,
	}

	result["channels"] = channels

	// Providers
	providers := make(map[string]interface{})
	for name, provider := range doc.Providers {
		prov := map[string]interface{}{
			"type":        provider.Type,
			"api_base":    provider.APIBase,
			"proxy":       provider.Proxy,
			"auth_method": provider.AuthMethod,
		}
		if provider.ConnectMode != "" {
			prov["connect_mode"] = provider.ConnectMode
		}
		if provider.WebSearch != nil {
			prov["web_search"] = *provider.WebSearch
		}
		if len(provider.Models) > 0 {
			prov["models"] = provider.Models
		}
		writeSecret(prov, "api_key", provider.APIKey)
		providers[name] = prov
	}
	if len(providers) > 0 {
		result["providers"] = providers
	}

	// Gateway
	result["gateway"] = map[string]interface{}{
		"host": doc.Gateway.Host,
		"port": doc.Gateway.Port,
	}

	// Tools
	tools := map[string]interface{}{
		"cron": map[string]interface{}{
			"exec_timeout_minutes": doc.Tools.Cron.ExecTimeoutMinutes,
		},
		"exec": map[string]interface{}{
			"enable_deny_patterns": doc.Tools.Exec.EnableDenyPatterns,
			"custom_deny_patterns": doc.Tools.Exec.CustomDenyPatterns,
			"timeout_seconds":      doc.Tools.Exec.TimeoutSeconds,
			"whitelist_commands":   doc.Tools.Exec.WhitelistCommands,
		},
	}

	// Web tools
	web := map[string]interface{}{
		"duckduckgo": map[string]interface{}{
			"enabled":     doc.Tools.Web.DuckDuckGo.Enabled,
			"max_results": doc.Tools.Web.DuckDuckGo.MaxResults,
		},
	}

	brave := map[string]interface{}{
		"enabled":     doc.Tools.Web.Brave.Enabled,
		"max_results": doc.Tools.Web.Brave.MaxResults,
	}
	writeSecret(brave, "api_key", doc.Tools.Web.Brave.APIKey)
	web["brave"] = brave

	perplexity := map[string]interface{}{
		"enabled":     doc.Tools.Web.Perplexity.Enabled,
		"max_results": doc.Tools.Web.Perplexity.MaxResults,
	}
	writeSecret(perplexity, "api_key", doc.Tools.Web.Perplexity.APIKey)
	web["perplexity"] = perplexity

	web["searxng"] = map[string]interface{}{
		"enabled":      doc.Tools.Web.SearXNG.Enabled,
		"instance_url": doc.Tools.Web.SearXNG.InstanceURL,
		"categories":   doc.Tools.Web.SearXNG.Categories,
		"language":     doc.Tools.Web.SearXNG.Language,
		"safesearch":   doc.Tools.Web.SearXNG.SafeSearch,
		"max_results":  doc.Tools.Web.SearXNG.MaxResults,
	}

	tools["web"] = web
	result["tools"] = tools

	// Heartbeat
	result["heartbeat"] = map[string]interface{}{
		"enabled":  doc.Heartbeat.Enabled,
		"interval": doc.Heartbeat.Interval,
	}

	// Devices.
	result["devices"] = map[string]interface{}{
		"enabled":     doc.Devices.Enabled,
		"monitor_usb": doc.Devices.MonitorUSB,
	}

	// Logs
	result["logs"] = map[string]interface{}{
		"enabled":  doc.Logs.Enabled,
		"path":     doc.Logs.Path,
		"max_days": doc.Logs.MaxDays,
		"rotation": doc.Logs.Rotation,
	}

	// Custom slash commands. ToConfig round-trips through this map, so any
	// section missing here is silently dropped from the runtime config.
	if len(doc.Commands) > 0 {
		result["commands"] = doc.Commands
	}
	// Emit the harness section when ANY of its flags is set: gating on
	// AllowShell alone would silently drop allow_absolute_files:true on
	// save (ToConfig re-unmarshals from this map).
	if doc.Harness.AllowShell || doc.Harness.AllowAbsoluteFiles {
		result["harness"] = doc.Harness
	}

	return result
}

func editableDocumentFromConfig(cfg *Config) *EditableDocument {
	if cfg == nil {
		return defaultEditableDocument()
	}
	doc := defaultEditableDocument()
	doc.Agents.Defaults = EditableAgentDefaults{
		Workspace:              cfg.Agents.Defaults.Workspace,
		RestrictToWorkspace:    cfg.Agents.Defaults.RestrictToWorkspace,
		Provider:               cfg.Agents.Defaults.Provider,
		Model:                  cfg.Agents.Defaults.Model,
		ModelFallbacks:         cfg.Agents.Defaults.ModelFallbacks,
		ImageModel:             cfg.Agents.Defaults.ImageModel,
		ImageModelFallbacks:    cfg.Agents.Defaults.ImageModelFallbacks,
		MaxTokens:              cfg.Agents.Defaults.MaxTokens,
		Temperature:            cfg.Agents.Defaults.Temperature,
		MaxToolIterations:      cfg.Agents.Defaults.MaxToolIterations,
		MaxReadLines:           cfg.Agents.Defaults.MaxReadLines,
		SubagentTimeoutMinutes: cfg.Agents.Defaults.SubagentTimeoutMinutes,
		SubagentMaxConcurrent:  cfg.Agents.Defaults.SubagentMaxConcurrent,
		SubagentMaxRetries:     cfg.Agents.Defaults.SubagentMaxRetries,
		LLMLoopTimeoutMinutes:  cfg.Agents.Defaults.LLMLoopTimeoutMinutes,
	}
	doc.Agents.List = make([]EditableAgentConfig, 0, len(cfg.Agents.List))
	for _, agent := range cfg.Agents.List {
		doc.Agents.List = append(doc.Agents.List, EditableAgentConfig(agent))
	}
	doc.Session = EditableSessionConfig(cfg.Session)
	doc.Bindings = append([]AgentBinding(nil), cfg.Bindings...)
	doc.Groups = cfg.Groups
	doc.Channels = EditableChannelsConfig{
		WhatsApp: EditableWhatsAppConfig(cfg.Channels.WhatsApp),
		Telegram: EditableTelegramConfig{Enabled: cfg.Channels.Telegram.Enabled, Token: literalOrEmptySecret(cfg.Channels.Telegram.Token), Proxy: cfg.Channels.Telegram.Proxy, AllowFrom: cfg.Channels.Telegram.AllowFrom, Verbose: cfg.Channels.Telegram.Verbose},
		Feishu:   EditableFeishuConfig{Enabled: cfg.Channels.Feishu.Enabled, AppID: literalOrEmptySecret(cfg.Channels.Feishu.AppID), AppSecret: literalOrEmptySecret(cfg.Channels.Feishu.AppSecret), EncryptKey: literalOrEmptySecret(cfg.Channels.Feishu.EncryptKey), VerificationToken: literalOrEmptySecret(cfg.Channels.Feishu.VerificationToken), AllowFrom: cfg.Channels.Feishu.AllowFrom},
		Discord:  EditableDiscordConfig{Enabled: cfg.Channels.Discord.Enabled, Token: literalOrEmptySecret(cfg.Channels.Discord.Token), AllowFrom: cfg.Channels.Discord.AllowFrom},
		MaixCam:  EditableMaixCamConfig(cfg.Channels.MaixCam),
		QQ:       EditableQQConfig{Enabled: cfg.Channels.QQ.Enabled, AppID: literalOrEmptySecret(cfg.Channels.QQ.AppID), AppSecret: literalOrEmptySecret(cfg.Channels.QQ.AppSecret), AllowFrom: cfg.Channels.QQ.AllowFrom},
		DingTalk: EditableDingTalkConfig{Enabled: cfg.Channels.DingTalk.Enabled, ClientID: literalOrEmptySecret(cfg.Channels.DingTalk.ClientID), ClientSecret: literalOrEmptySecret(cfg.Channels.DingTalk.ClientSecret), AllowFrom: cfg.Channels.DingTalk.AllowFrom},
		Slack:    EditableSlackConfig{Enabled: cfg.Channels.Slack.Enabled, BotToken: literalOrEmptySecret(cfg.Channels.Slack.BotToken), AppToken: literalOrEmptySecret(cfg.Channels.Slack.AppToken), AllowFrom: cfg.Channels.Slack.AllowFrom},
		LINE:     EditableLINEConfig{Enabled: cfg.Channels.LINE.Enabled, ChannelSecret: literalOrEmptySecret(cfg.Channels.LINE.ChannelSecret), ChannelAccessToken: literalOrEmptySecret(cfg.Channels.LINE.ChannelAccessToken), WebhookHost: cfg.Channels.LINE.WebhookHost, WebhookPort: cfg.Channels.LINE.WebhookPort, WebhookPath: cfg.Channels.LINE.WebhookPath, AllowFrom: cfg.Channels.LINE.AllowFrom},
		OneBot:   EditableOneBotConfig{Enabled: cfg.Channels.OneBot.Enabled, WSUrl: cfg.Channels.OneBot.WSUrl, AccessToken: literalOrEmptySecret(cfg.Channels.OneBot.AccessToken), ReconnectInterval: cfg.Channels.OneBot.ReconnectInterval, GroupTriggerPrefix: cfg.Channels.OneBot.GroupTriggerPrefix, AllowFrom: cfg.Channels.OneBot.AllowFrom},
		Native:   EditableNativeConfig{Enabled: cfg.Channels.Native.Enabled, Host: cfg.Channels.Native.Host, Port: cfg.Channels.Native.Port, TokenExpiryDays: cfg.Channels.Native.TokenExpiryDays, PinExpiryMinutes: cfg.Channels.Native.PinExpiryMinutes, MaxClients: cfg.Channels.Native.MaxClients, CORSOrigins: cfg.Channels.Native.CORSOrigins, SessionExpiryDays: cfg.Channels.Native.SessionExpiryDays, MaxUploadSizeMB: cfg.Channels.Native.MaxUploadSizeMB, UploadTTLHours: cfg.Channels.Native.UploadTTLHours},
	}
	doc.Providers = EditableProvidersConfig{}
	for name, provider := range cfg.Providers.ListNamed() {
		// Only include providers that have actual data
		if provider.APIKey == "" && len(provider.Models) == 0 {
			continue
		}
		doc.Providers[name] = EditableNamedProviderConfig{
			Type:        provider.Type,
			APIKey:      literalOrEmptySecret(provider.APIKey),
			APIBase:     provider.APIBase,
			Proxy:       provider.Proxy,
			AuthMethod:  provider.AuthMethod,
			ConnectMode: provider.ConnectMode,
			WebSearch:   provider.WebSearch,
			Models:      provider.Models,
		}
	}
	doc.Gateway = cfg.Gateway
	doc.Tools = EditableToolsConfig{
		Web: EditableWebToolsConfig{
			Brave:      EditableBraveConfig{Enabled: cfg.Tools.Web.Brave.Enabled, APIKey: literalOrEmptySecret(cfg.Tools.Web.Brave.APIKey), MaxResults: cfg.Tools.Web.Brave.MaxResults},
			DuckDuckGo: cfg.Tools.Web.DuckDuckGo,
			Perplexity: EditablePerplexityConfig{Enabled: cfg.Tools.Web.Perplexity.Enabled, APIKey: literalOrEmptySecret(cfg.Tools.Web.Perplexity.APIKey), MaxResults: cfg.Tools.Web.Perplexity.MaxResults},
			SearXNG:    EditableSearXNGConfig{Enabled: cfg.Tools.Web.SearXNG.Enabled, InstanceURL: cfg.Tools.Web.SearXNG.InstanceURL, Categories: cfg.Tools.Web.SearXNG.Categories, Language: cfg.Tools.Web.SearXNG.Language, SafeSearch: cfg.Tools.Web.SearXNG.SafeSearch, MaxResults: cfg.Tools.Web.SearXNG.MaxResults},
		},
		Cron: cfg.Tools.Cron,
		Exec: EditableExecConfig(cfg.Tools.Exec),
	}
	doc.Heartbeat = cfg.Heartbeat
	doc.Devices = cfg.Devices
	doc.Logs = EditableLogsConfig(cfg.Logs)

	// Custom slash commands: same element types, so clone the map directly.
	if len(cfg.Commands) > 0 {
		doc.Commands = make(map[string]CommandDefinition, len(cfg.Commands))
		for name, def := range cfg.Commands {
			doc.Commands[name] = def
		}
	}
	doc.Harness = cfg.Harness
	return doc
}
