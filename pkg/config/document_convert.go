package config

import (
	"encoding/json"
	"fmt"
)

func (doc *EditableDocument) ToConfig() (*Config, error) {
	cfg := DefaultConfig()

	cfg.Agents.Defaults = AgentDefaults{
		Workspace:                doc.Agents.Defaults.Workspace,
		RestrictToWorkspace:      doc.Agents.Defaults.RestrictToWorkspace,
		Provider:                 doc.Agents.Defaults.Provider,
		Model:                    doc.Agents.Defaults.Model,
		ModelFallbacks:           doc.Agents.Defaults.ModelFallbacks,
		ImageModel:               doc.Agents.Defaults.ImageModel,
		ImageModelFallbacks:      doc.Agents.Defaults.ImageModelFallbacks,
		MaxTokens:                doc.Agents.Defaults.MaxTokens,
		Temperature:              doc.Agents.Defaults.Temperature,
		ThinkingLevel:            doc.Agents.Defaults.ThinkingLevel,
		MaxToolIterations:        doc.Agents.Defaults.MaxToolIterations,
		MaxReadLines:             doc.Agents.Defaults.MaxReadLines,
		MemoryMaxLines:           doc.Agents.Defaults.MemoryMaxLines,
		SubagentTimeoutMinutes:   doc.Agents.Defaults.SubagentTimeoutMinutes,
		SubagentMaxConcurrent:    doc.Agents.Defaults.SubagentMaxConcurrent,
		SubagentMaxRetries:       doc.Agents.Defaults.SubagentMaxRetries,
		SubagentMaxIterations:    doc.Agents.Defaults.SubagentMaxIterations,
		SubagentRetentionMinutes: doc.Agents.Defaults.SubagentRetentionMinutes,
		LLMLoopTimeoutMinutes:    doc.Agents.Defaults.LLMLoopTimeoutMinutes,
		PromptCache:              doc.Agents.Defaults.PromptCache,
	}

	for _, agent := range doc.Agents.List {
		cfg.Agents.List = append(cfg.Agents.List, AgentConfig{
			ID:            agent.ID,
			Default:       agent.Default,
			Name:          agent.Name,
			Workspace:     agent.Workspace,
			Model:         agent.Model,
			Skills:        agent.Skills,
			Subagents:     agent.Subagents,
			Temperature:   agent.Temperature,
			ThinkingLevel: agent.ThinkingLevel,
			Tools:         agent.Tools,
		})
	}

	cfg.Session = SessionConfig{
		DMScope:                    doc.Session.DMScope,
		IdentityLinks:              doc.Session.IdentityLinks,
		Ephemeral:                  doc.Session.Ephemeral,
		EphemeralThreshold:         doc.Session.EphemeralThreshold,
		CompactionThresholdPercent: doc.Session.CompactionThresholdPercent,
		CompactionModel:            doc.Session.CompactionModel,
		EvictExcludedFromMemory:    doc.Session.EvictExcludedFromMemory,
		DurableInbound:             doc.Session.DurableInbound,
		DurableOutbound:            doc.Session.DurableOutbound,
		Resume:                     doc.Session.Resume,
	}

	cfg.Bindings = doc.Bindings

	cfg.Groups = doc.Groups

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
		RateLimit:         doc.Channels.Native.RateLimit,
	}
	cfg.Channels.Web.Enabled = doc.Channels.Web.Enabled

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

	cfg.Gateway = doc.Gateway

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

	cfg.Heartbeat = doc.Heartbeat

	cfg.Devices = doc.Devices

	cfg.Logs = LogsConfig(doc.Logs)

	if len(doc.Commands) > 0 {
		cfg.Commands = make(map[string]CommandDefinition, len(doc.Commands))
		for name, def := range doc.Commands {
			cfg.Commands[name] = def
		}
	}
	cfg.Harness = doc.Harness

	serializable := doc.toSerializable()
	data, err := json.Marshal(serializable)
	if err != nil {
		return nil, err
	}
	validated := DefaultConfig()
	if err := json.Unmarshal(data, validated); err != nil {
		return nil, err
	}
	if !pinnedSessionEphemeral(serializable) {
		validated.Session.Ephemeral = SessionEphemeralFileDefault
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

func SaveEditableDocument(path string, doc *EditableDocument) error {
	next, err := jsonTreeOf(doc.toSerializable())
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	raw, exists := rawDocumentFromFile(path)
	var base map[string]interface{}
	if exists {
		base, err = baseDocumentForFile(path)
		if err != nil {
			return err
		}
	} else {
		raw = map[string]json.RawMessage{}
	}

	merged, err := mergeConfigDocument(raw, base, next)
	if err != nil {
		return fmt.Errorf("failed to merge config: %w", err)
	}

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := writeConfigFile(path, data); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

func baseDocumentForFile(path string) (map[string]interface{}, error) {
	loaded, err := LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load current config: %w", err)
	}
	base, err := jsonTreeOf(editableDocumentFromConfig(loaded).toSerializable())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}
	return base, nil
}

func SaveMinimalConfig(path string, cfg *Config) error {
	return SaveEditableDocument(path, editableDocumentFromConfig(cfg))
}

func (doc *EditableDocument) toSerializable() map[string]interface{} {
	result := make(map[string]interface{})

	def := DefaultConfig()

	result["agents"] = map[string]interface{}{
		"defaults": doc.Agents.Defaults,
	}
	if len(doc.Agents.List) > 0 {
		result["agents"].(map[string]interface{})["list"] = doc.Agents.List
	}

	session := make(map[string]interface{})
	putIfDiff(session, "ephemeral", doc.Session.Ephemeral, SessionEphemeralFileDefault)
	putIfDiff(session, "ephemeral_threshold", doc.Session.EphemeralThreshold, DefaultEphemeralThresholdSeconds)
	putIfDiff(session, "compaction_threshold_percent", doc.Session.CompactionThresholdPercent, DefaultCompactionThresholdPercent)
	putIfDiff(session, "compaction_model", doc.Session.CompactionModel, def.Session.CompactionModel)
	putIfDiff(session, "evict_excluded_from_memory", doc.Session.EvictExcludedFromMemory, def.Session.EvictExcludedFromMemory)
	putIfDiff(session, "dm_scope", doc.Session.DMScope, def.Session.DMScope)
	if len(doc.Session.IdentityLinks) > 0 {
		session["identity_links"] = doc.Session.IdentityLinks
	}
	if doc.Session.DurableInbound != nil {
		session["durable_inbound"] = *doc.Session.DurableInbound
	}
	if doc.Session.DurableOutbound != nil {
		session["durable_outbound"] = *doc.Session.DurableOutbound
	}
	if doc.Session.Resume != nil {
		session["resume_enabled"] = *doc.Session.Resume
	}
	putSection(result, "session", session)

	if len(doc.Bindings) > 0 {
		result["bindings"] = doc.Bindings
	}

	if doc.Groups.Enabled || len(doc.Groups.List) > 0 {
		result["groups"] = doc.Groups
	}

	channels := make(map[string]interface{})

	native := make(map[string]interface{})
	putIfDiff(native, "enabled", doc.Channels.Native.Enabled, def.Channels.Native.Enabled)
	putIfDiff(native, "host", doc.Channels.Native.Host, def.Channels.Native.Host)
	putIfDiff(native, "port", doc.Channels.Native.Port, def.Channels.Native.Port)
	putIfDiff(native, "token_expiry_days", doc.Channels.Native.TokenExpiryDays, def.Channels.Native.TokenExpiryDays)
	putIfDiff(native, "pin_expiry_minutes", doc.Channels.Native.PinExpiryMinutes, def.Channels.Native.PinExpiryMinutes)
	putIfDiff(native, "max_clients", doc.Channels.Native.MaxClients, def.Channels.Native.MaxClients)
	putSliceIfDiff(native, "cors_origins", doc.Channels.Native.CORSOrigins, def.Channels.Native.CORSOrigins)
	putIfDiff(native, "session_expiry_days", doc.Channels.Native.SessionExpiryDays, def.Channels.Native.SessionExpiryDays)
	putIfDiff(native, "max_upload_size_mb", doc.Channels.Native.MaxUploadSizeMB, def.Channels.Native.MaxUploadSizeMB)
	putIfDiff(native, "upload_ttl_hours", doc.Channels.Native.UploadTTLHours, def.Channels.Native.UploadTTLHours)
	putIfDiff(native, "rate_limit", doc.Channels.Native.RateLimit, def.Channels.Native.RateLimit)
	putSection(channels, "native", native)

	webChannel := make(map[string]interface{})
	putIfDiff(webChannel, "enabled", doc.Channels.Web.Enabled, def.Channels.Web.Enabled)
	putSection(channels, "web", webChannel)

	telegram := make(map[string]interface{})
	putIfDiff(telegram, "enabled", doc.Channels.Telegram.Enabled, def.Channels.Telegram.Enabled)
	putSecretIfDiff(telegram, "token", doc.Channels.Telegram.Token, def.Channels.Telegram.Token)
	putIfDiff(telegram, "proxy", doc.Channels.Telegram.Proxy, def.Channels.Telegram.Proxy)
	putSliceIfDiff(telegram, "allow_from", doc.Channels.Telegram.AllowFrom, def.Channels.Telegram.AllowFrom)
	putIfDiff(telegram, "verbose", doc.Channels.Telegram.Verbose, def.Channels.Telegram.Verbose)
	putSection(channels, "telegram", telegram)

	discord := make(map[string]interface{})
	putIfDiff(discord, "enabled", doc.Channels.Discord.Enabled, def.Channels.Discord.Enabled)
	putSecretIfDiff(discord, "token", doc.Channels.Discord.Token, def.Channels.Discord.Token)
	putSliceIfDiff(discord, "allow_from", doc.Channels.Discord.AllowFrom, def.Channels.Discord.AllowFrom)
	putSection(channels, "discord", discord)

	feishu := make(map[string]interface{})
	putIfDiff(feishu, "enabled", doc.Channels.Feishu.Enabled, def.Channels.Feishu.Enabled)
	putSecretIfDiff(feishu, "app_id", doc.Channels.Feishu.AppID, def.Channels.Feishu.AppID)
	putSecretIfDiff(feishu, "app_secret", doc.Channels.Feishu.AppSecret, def.Channels.Feishu.AppSecret)
	putSecretIfDiff(feishu, "encrypt_key", doc.Channels.Feishu.EncryptKey, def.Channels.Feishu.EncryptKey)
	putSecretIfDiff(feishu, "verification_token", doc.Channels.Feishu.VerificationToken, def.Channels.Feishu.VerificationToken)
	putSliceIfDiff(feishu, "allow_from", doc.Channels.Feishu.AllowFrom, def.Channels.Feishu.AllowFrom)
	putSection(channels, "feishu", feishu)

	slack := make(map[string]interface{})
	putIfDiff(slack, "enabled", doc.Channels.Slack.Enabled, def.Channels.Slack.Enabled)
	putSecretIfDiff(slack, "bot_token", doc.Channels.Slack.BotToken, def.Channels.Slack.BotToken)
	putSecretIfDiff(slack, "app_token", doc.Channels.Slack.AppToken, def.Channels.Slack.AppToken)
	putSliceIfDiff(slack, "allow_from", doc.Channels.Slack.AllowFrom, def.Channels.Slack.AllowFrom)
	putSection(channels, "slack", slack)

	line := make(map[string]interface{})
	putIfDiff(line, "enabled", doc.Channels.LINE.Enabled, def.Channels.LINE.Enabled)
	putSecretIfDiff(line, "channel_secret", doc.Channels.LINE.ChannelSecret, def.Channels.LINE.ChannelSecret)
	putSecretIfDiff(line, "channel_access_token", doc.Channels.LINE.ChannelAccessToken, def.Channels.LINE.ChannelAccessToken)
	putIfDiff(line, "webhook_host", doc.Channels.LINE.WebhookHost, def.Channels.LINE.WebhookHost)
	putIfDiff(line, "webhook_port", doc.Channels.LINE.WebhookPort, def.Channels.LINE.WebhookPort)
	putIfDiff(line, "webhook_path", doc.Channels.LINE.WebhookPath, def.Channels.LINE.WebhookPath)
	putSliceIfDiff(line, "allow_from", doc.Channels.LINE.AllowFrom, def.Channels.LINE.AllowFrom)
	putSection(channels, "line", line)

	onebot := make(map[string]interface{})
	putIfDiff(onebot, "enabled", doc.Channels.OneBot.Enabled, def.Channels.OneBot.Enabled)
	putIfDiff(onebot, "ws_url", doc.Channels.OneBot.WSUrl, def.Channels.OneBot.WSUrl)
	putSecretIfDiff(onebot, "access_token", doc.Channels.OneBot.AccessToken, def.Channels.OneBot.AccessToken)
	putIfDiff(onebot, "reconnect_interval", doc.Channels.OneBot.ReconnectInterval, def.Channels.OneBot.ReconnectInterval)
	putSliceIfDiff(onebot, "group_trigger_prefix", doc.Channels.OneBot.GroupTriggerPrefix, def.Channels.OneBot.GroupTriggerPrefix)
	putSliceIfDiff(onebot, "allow_from", doc.Channels.OneBot.AllowFrom, def.Channels.OneBot.AllowFrom)
	putSection(channels, "onebot", onebot)

	qq := make(map[string]interface{})
	putIfDiff(qq, "enabled", doc.Channels.QQ.Enabled, def.Channels.QQ.Enabled)
	putSecretIfDiff(qq, "app_id", doc.Channels.QQ.AppID, def.Channels.QQ.AppID)
	putSecretIfDiff(qq, "app_secret", doc.Channels.QQ.AppSecret, def.Channels.QQ.AppSecret)
	putSliceIfDiff(qq, "allow_from", doc.Channels.QQ.AllowFrom, def.Channels.QQ.AllowFrom)
	putSection(channels, "qq", qq)

	dingtalk := make(map[string]interface{})
	putIfDiff(dingtalk, "enabled", doc.Channels.DingTalk.Enabled, def.Channels.DingTalk.Enabled)
	putSecretIfDiff(dingtalk, "client_id", doc.Channels.DingTalk.ClientID, def.Channels.DingTalk.ClientID)
	putSecretIfDiff(dingtalk, "client_secret", doc.Channels.DingTalk.ClientSecret, def.Channels.DingTalk.ClientSecret)
	putSliceIfDiff(dingtalk, "allow_from", doc.Channels.DingTalk.AllowFrom, def.Channels.DingTalk.AllowFrom)
	putSection(channels, "dingtalk", dingtalk)

	whatsapp := make(map[string]interface{})
	putIfDiff(whatsapp, "enabled", doc.Channels.WhatsApp.Enabled, def.Channels.WhatsApp.Enabled)
	putIfDiff(whatsapp, "bridge_url", doc.Channels.WhatsApp.BridgeURL, def.Channels.WhatsApp.BridgeURL)
	putSliceIfDiff(whatsapp, "allow_from", doc.Channels.WhatsApp.AllowFrom, def.Channels.WhatsApp.AllowFrom)
	putSection(channels, "whatsapp", whatsapp)

	maixcam := make(map[string]interface{})
	putIfDiff(maixcam, "enabled", doc.Channels.MaixCam.Enabled, def.Channels.MaixCam.Enabled)
	putIfDiff(maixcam, "host", doc.Channels.MaixCam.Host, def.Channels.MaixCam.Host)
	putIfDiff(maixcam, "port", doc.Channels.MaixCam.Port, def.Channels.MaixCam.Port)
	putSliceIfDiff(maixcam, "allow_from", doc.Channels.MaixCam.AllowFrom, def.Channels.MaixCam.AllowFrom)
	putSection(channels, "maixcam", maixcam)

	if len(channels) > 0 {
		result["channels"] = channels
	}

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
		if len(provider.Headers) > 0 {
			prov["headers"] = provider.Headers
		}
		writeSecret(prov, "api_key", provider.APIKey)
		providers[name] = prov
	}
	if len(providers) > 0 {
		result["providers"] = providers
	}

	gateway := make(map[string]interface{})
	putIfDiff(gateway, "host", doc.Gateway.Host, def.Gateway.Host)
	putIfDiff(gateway, "port", doc.Gateway.Port, def.Gateway.Port)
	if len(gateway) > 0 {
		result["gateway"] = gateway
	}

	tools := make(map[string]interface{})

	cron := make(map[string]interface{})
	putIfDiff(cron, "exec_timeout_minutes", doc.Tools.Cron.ExecTimeoutMinutes, def.Tools.Cron.ExecTimeoutMinutes)
	putSection(tools, "cron", cron)

	exec := make(map[string]interface{})
	putIfDiff(exec, "enable_deny_patterns", doc.Tools.Exec.EnableDenyPatterns, def.Tools.Exec.EnableDenyPatterns)
	putSliceIfDiff(exec, "custom_deny_patterns", doc.Tools.Exec.CustomDenyPatterns, def.Tools.Exec.CustomDenyPatterns)
	putIfDiff(exec, "timeout_seconds", doc.Tools.Exec.TimeoutSeconds, def.Tools.Exec.TimeoutSeconds)
	putSliceIfDiff(exec, "whitelist_commands", doc.Tools.Exec.WhitelistCommands, def.Tools.Exec.WhitelistCommands)
	putSection(tools, "exec", exec)

	duckduckgo := make(map[string]interface{})
	putIfDiff(duckduckgo, "enabled", doc.Tools.Web.DuckDuckGo.Enabled, def.Tools.Web.DuckDuckGo.Enabled)
	putIfDiff(duckduckgo, "max_results", doc.Tools.Web.DuckDuckGo.MaxResults, def.Tools.Web.DuckDuckGo.MaxResults)

	brave := make(map[string]interface{})
	putIfDiff(brave, "enabled", doc.Tools.Web.Brave.Enabled, def.Tools.Web.Brave.Enabled)
	putIfDiff(brave, "max_results", doc.Tools.Web.Brave.MaxResults, def.Tools.Web.Brave.MaxResults)
	putSecretIfDiff(brave, "api_key", doc.Tools.Web.Brave.APIKey, def.Tools.Web.Brave.APIKey)

	perplexity := make(map[string]interface{})
	putIfDiff(perplexity, "enabled", doc.Tools.Web.Perplexity.Enabled, def.Tools.Web.Perplexity.Enabled)
	putIfDiff(perplexity, "max_results", doc.Tools.Web.Perplexity.MaxResults, def.Tools.Web.Perplexity.MaxResults)
	putSecretIfDiff(perplexity, "api_key", doc.Tools.Web.Perplexity.APIKey, def.Tools.Web.Perplexity.APIKey)

	searxng := make(map[string]interface{})
	putIfDiff(searxng, "enabled", doc.Tools.Web.SearXNG.Enabled, def.Tools.Web.SearXNG.Enabled)
	putIfDiff(searxng, "instance_url", doc.Tools.Web.SearXNG.InstanceURL, def.Tools.Web.SearXNG.InstanceURL)
	putIfDiff(searxng, "categories", doc.Tools.Web.SearXNG.Categories, def.Tools.Web.SearXNG.Categories)
	putIfDiff(searxng, "language", doc.Tools.Web.SearXNG.Language, def.Tools.Web.SearXNG.Language)
	putIfDiff(searxng, "safesearch", doc.Tools.Web.SearXNG.SafeSearch, def.Tools.Web.SearXNG.SafeSearch)
	putIfDiff(searxng, "max_results", doc.Tools.Web.SearXNG.MaxResults, def.Tools.Web.SearXNG.MaxResults)

	web := make(map[string]interface{})
	putSection(web, "brave", brave)
	putSection(web, "duckduckgo", duckduckgo)
	putSection(web, "perplexity", perplexity)
	putSection(web, "searxng", searxng)
	putSection(tools, "web", web)
	putSection(result, "tools", tools)

	heartbeat := make(map[string]interface{})
	putIfDiff(heartbeat, "enabled", doc.Heartbeat.Enabled, def.Heartbeat.Enabled)
	putIfDiff(heartbeat, "interval", doc.Heartbeat.Interval, def.Heartbeat.Interval)
	putSection(result, "heartbeat", heartbeat)

	devices := make(map[string]interface{})
	putIfDiff(devices, "enabled", doc.Devices.Enabled, def.Devices.Enabled)
	putIfDiff(devices, "monitor_usb", doc.Devices.MonitorUSB, def.Devices.MonitorUSB)
	putSection(result, "devices", devices)

	logs := make(map[string]interface{})
	putIfDiff(logs, "enabled", doc.Logs.Enabled, def.Logs.Enabled)
	putIfDiff(logs, "path", doc.Logs.Path, def.Logs.Path)
	putIfDiff(logs, "max_days", doc.Logs.MaxDays, def.Logs.MaxDays)
	putIfDiff(logs, "rotation", doc.Logs.Rotation, def.Logs.Rotation)
	putSection(result, "logs", logs)

	if len(doc.Commands) > 0 {
		result["commands"] = doc.Commands
	}
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
		Workspace:                cfg.Agents.Defaults.Workspace,
		RestrictToWorkspace:      cfg.Agents.Defaults.RestrictToWorkspace,
		Provider:                 cfg.Agents.Defaults.Provider,
		Model:                    cfg.Agents.Defaults.Model,
		ModelFallbacks:           cfg.Agents.Defaults.ModelFallbacks,
		ImageModel:               cfg.Agents.Defaults.ImageModel,
		ImageModelFallbacks:      cfg.Agents.Defaults.ImageModelFallbacks,
		MaxTokens:                cfg.Agents.Defaults.MaxTokens,
		Temperature:              cfg.Agents.Defaults.Temperature,
		ThinkingLevel:            cfg.Agents.Defaults.ThinkingLevel,
		MaxToolIterations:        cfg.Agents.Defaults.MaxToolIterations,
		MaxReadLines:             cfg.Agents.Defaults.MaxReadLines,
		MemoryMaxLines:           cfg.Agents.Defaults.MemoryMaxLines,
		SubagentTimeoutMinutes:   cfg.Agents.Defaults.SubagentTimeoutMinutes,
		SubagentMaxConcurrent:    cfg.Agents.Defaults.SubagentMaxConcurrent,
		SubagentMaxRetries:       cfg.Agents.Defaults.SubagentMaxRetries,
		SubagentMaxIterations:    cfg.Agents.Defaults.SubagentMaxIterations,
		SubagentRetentionMinutes: cfg.Agents.Defaults.SubagentRetentionMinutes,
		LLMLoopTimeoutMinutes:    cfg.Agents.Defaults.LLMLoopTimeoutMinutes,
		PromptCache:              cfg.Agents.Defaults.PromptCache,
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
		Native:   EditableNativeConfig{Enabled: cfg.Channels.Native.Enabled, Host: cfg.Channels.Native.Host, Port: cfg.Channels.Native.Port, TokenExpiryDays: cfg.Channels.Native.TokenExpiryDays, PinExpiryMinutes: cfg.Channels.Native.PinExpiryMinutes, MaxClients: cfg.Channels.Native.MaxClients, CORSOrigins: cfg.Channels.Native.CORSOrigins, SessionExpiryDays: cfg.Channels.Native.SessionExpiryDays, MaxUploadSizeMB: cfg.Channels.Native.MaxUploadSizeMB, UploadTTLHours: cfg.Channels.Native.UploadTTLHours, RateLimit: cfg.Channels.Native.RateLimit},
		Web:      EditableWebConfig{Enabled: cfg.Channels.Web.Enabled},
	}
	doc.Providers = EditableProvidersConfig{}
	for name, provider := range cfg.Providers.ListNamed() {
		if provider.APIKey == "" && len(provider.Models) == 0 && len(provider.Headers) == 0 {
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
			Headers:     provider.Headers,
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

	if len(cfg.Commands) > 0 {
		doc.Commands = make(map[string]CommandDefinition, len(cfg.Commands))
		for name, def := range cfg.Commands {
			doc.Commands[name] = def
		}
	}
	doc.Harness = cfg.Harness
	return doc
}
