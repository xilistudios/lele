package config

func applyDefaults(doc *EditableDocument) *EditableDocument {
	defaults := defaultEditableDocument()

	// Apply defaults where values are missing.
	if doc.Agents.Defaults.Workspace == "" {
		doc.Agents.Defaults.Workspace = defaults.Agents.Defaults.Workspace
	}
	if doc.Agents.Defaults.Provider == "" {
		doc.Agents.Defaults.Provider = defaults.Agents.Defaults.Provider
	}
	if doc.Agents.Defaults.Model == "" {
		doc.Agents.Defaults.Model = defaults.Agents.Defaults.Model
	}
	if doc.Agents.Defaults.MaxTokens == 0 {
		doc.Agents.Defaults.MaxTokens = defaults.Agents.Defaults.MaxTokens
	}
	if doc.Agents.Defaults.MaxToolIterations == 0 {
		doc.Agents.Defaults.MaxToolIterations = defaults.Agents.Defaults.MaxToolIterations
	}
	if doc.Agents.Defaults.MaxReadLines == 0 {
		doc.Agents.Defaults.MaxReadLines = defaults.Agents.Defaults.MaxReadLines
	}

	// Defaults for the native channel.
	if doc.Channels.Native.Host == "" {
		doc.Channels.Native.Host = defaults.Channels.Native.Host
	}
	if doc.Channels.Native.Port == 0 {
		doc.Channels.Native.Port = defaults.Channels.Native.Port
	}
	if doc.Channels.Native.TokenExpiryDays == 0 {
		doc.Channels.Native.TokenExpiryDays = defaults.Channels.Native.TokenExpiryDays
	}
	if doc.Channels.Native.PinExpiryMinutes == 0 {
		doc.Channels.Native.PinExpiryMinutes = defaults.Channels.Native.PinExpiryMinutes
	}
	if doc.Channels.Native.MaxClients == 0 {
		doc.Channels.Native.MaxClients = defaults.Channels.Native.MaxClients
	}
	if doc.Channels.Native.CORSOrigins == nil {
		doc.Channels.Native.CORSOrigins = defaults.Channels.Native.CORSOrigins
	}
	if doc.Channels.Native.SessionExpiryDays == 0 {
		doc.Channels.Native.SessionExpiryDays = defaults.Channels.Native.SessionExpiryDays
	}
	if doc.Channels.Native.MaxUploadSizeMB == 0 {
		doc.Channels.Native.MaxUploadSizeMB = defaults.Channels.Native.MaxUploadSizeMB
	}
	if doc.Channels.Native.UploadTTLHours == 0 {
		doc.Channels.Native.UploadTTLHours = defaults.Channels.Native.UploadTTLHours
	}

	// Defaults for session.
	if doc.Session.EphemeralThreshold == 0 {
		doc.Session.EphemeralThreshold = defaults.Session.EphemeralThreshold
	}

	// Defaults for gateway.
	if doc.Gateway.Host == "" {
		doc.Gateway.Host = defaults.Gateway.Host
	}
	if doc.Gateway.Port == 0 {
		doc.Gateway.Port = defaults.Gateway.Port
	}

	// Defaults for heartbeat.
	if doc.Heartbeat.Interval == 0 {
		doc.Heartbeat.Interval = defaults.Heartbeat.Interval
	}

	// Defaults for logs.
	if doc.Logs.Path == "" {
		doc.Logs.Path = defaults.Logs.Path
	}
	if doc.Logs.MaxDays == 0 {
		doc.Logs.MaxDays = defaults.Logs.MaxDays
	}
	if doc.Logs.Rotation == "" {
		doc.Logs.Rotation = defaults.Logs.Rotation
	}

	// Defaults for tools.
	if doc.Tools.Cron.ExecTimeoutMinutes == 0 {
		doc.Tools.Cron.ExecTimeoutMinutes = defaults.Tools.Cron.ExecTimeoutMinutes
	}

	// Inicializar mapas si son nil
	if doc.Providers == nil {
		doc.Providers = EditableProvidersConfig{}
	}

	return doc
}

func defaultEditableDocument() *EditableDocument {
	defaults := DefaultConfig()
	return &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:           defaults.Agents.Defaults.Workspace,
				RestrictToWorkspace: defaults.Agents.Defaults.RestrictToWorkspace,
				Provider:            defaults.Agents.Defaults.Provider,
				Model:               defaults.Agents.Defaults.Model,
				MaxTokens:           defaults.Agents.Defaults.MaxTokens,
				MaxToolIterations:   defaults.Agents.Defaults.MaxToolIterations,
				MaxReadLines:        defaults.Agents.Defaults.MaxReadLines,
			},
			List: []EditableAgentConfig{},
		},
		Session: EditableSessionConfig{
			Ephemeral:          defaults.Session.Ephemeral,
			EphemeralThreshold: defaults.Session.EphemeralThreshold,
		},
		Bindings: []AgentBinding{},
		Channels: EditableChannelsConfig{
			WhatsApp: EditableWhatsAppConfig{
				Enabled:   defaults.Channels.WhatsApp.Enabled,
				BridgeURL: defaults.Channels.WhatsApp.BridgeURL,
				AllowFrom: defaults.Channels.WhatsApp.AllowFrom,
			},
			Telegram: EditableTelegramConfig{
				Enabled:   defaults.Channels.Telegram.Enabled,
				Token:     SecretValue{Mode: SecretModeEmpty},
				Proxy:     defaults.Channels.Telegram.Proxy,
				AllowFrom: defaults.Channels.Telegram.AllowFrom,
				Verbose:   defaults.Channels.Telegram.Verbose,
			},
			Feishu: EditableFeishuConfig{
				Enabled:   defaults.Channels.Feishu.Enabled,
				AllowFrom: defaults.Channels.Feishu.AllowFrom,
				AppID:     SecretValue{Mode: SecretModeEmpty},
				AppSecret: SecretValue{Mode: SecretModeEmpty},
			},
			Discord: EditableDiscordConfig{
				Enabled:   defaults.Channels.Discord.Enabled,
				AllowFrom: defaults.Channels.Discord.AllowFrom,
				Token:     SecretValue{Mode: SecretModeEmpty},
			},
			MaixCam: EditableMaixCamConfig{
				Enabled:   defaults.Channels.MaixCam.Enabled,
				Host:      defaults.Channels.MaixCam.Host,
				Port:      defaults.Channels.MaixCam.Port,
				AllowFrom: defaults.Channels.MaixCam.AllowFrom,
			},
			QQ: EditableQQConfig{
				Enabled:   defaults.Channels.QQ.Enabled,
				AllowFrom: defaults.Channels.QQ.AllowFrom,
				AppID:     SecretValue{Mode: SecretModeEmpty},
				AppSecret: SecretValue{Mode: SecretModeEmpty},
			},
			DingTalk: EditableDingTalkConfig{
				Enabled:   defaults.Channels.DingTalk.Enabled,
				AllowFrom: defaults.Channels.DingTalk.AllowFrom,
				ClientID:  SecretValue{Mode: SecretModeEmpty},
			},
			Slack: EditableSlackConfig{
				Enabled:   defaults.Channels.Slack.Enabled,
				AllowFrom: defaults.Channels.Slack.AllowFrom,
				BotToken:  SecretValue{Mode: SecretModeEmpty},
				AppToken:  SecretValue{Mode: SecretModeEmpty},
			},
			LINE: EditableLINEConfig{
				Enabled:     defaults.Channels.LINE.Enabled,
				WebhookHost: defaults.Channels.LINE.WebhookHost,
				WebhookPort: defaults.Channels.LINE.WebhookPort,
				WebhookPath: defaults.Channels.LINE.WebhookPath,
				AllowFrom:   defaults.Channels.LINE.AllowFrom,
			},
			OneBot: EditableOneBotConfig{
				Enabled:            defaults.Channels.OneBot.Enabled,
				WSUrl:              defaults.Channels.OneBot.WSUrl,
				ReconnectInterval:  defaults.Channels.OneBot.ReconnectInterval,
				GroupTriggerPrefix: defaults.Channels.OneBot.GroupTriggerPrefix,
				AllowFrom:          defaults.Channels.OneBot.AllowFrom,
				AccessToken:        SecretValue{Mode: SecretModeEmpty},
			},
			Native: EditableNativeConfig{
				Enabled:           defaults.Channels.Native.Enabled,
				Host:              defaults.Channels.Native.Host,
				Port:              defaults.Channels.Native.Port,
				TokenExpiryDays:   defaults.Channels.Native.TokenExpiryDays,
				PinExpiryMinutes:  defaults.Channels.Native.PinExpiryMinutes,
				MaxClients:        defaults.Channels.Native.MaxClients,
				CORSOrigins:       defaults.Channels.Native.CORSOrigins,
				SessionExpiryDays: defaults.Channels.Native.SessionExpiryDays,
				MaxUploadSizeMB:   defaults.Channels.Native.MaxUploadSizeMB,
				UploadTTLHours:    defaults.Channels.Native.UploadTTLHours,
			},
		},
		Providers: EditableProvidersConfig{},
		Gateway:   defaults.Gateway,
		Tools: EditableToolsConfig{
			Web: EditableWebToolsConfig{
				Brave: EditableBraveConfig{
					Enabled:    defaults.Tools.Web.Brave.Enabled,
					APIKey:     SecretValue{Mode: SecretModeEmpty},
					MaxResults: defaults.Tools.Web.Brave.MaxResults,
				},
				DuckDuckGo: defaults.Tools.Web.DuckDuckGo,
				Perplexity: EditablePerplexityConfig{
					Enabled:    defaults.Tools.Web.Perplexity.Enabled,
					APIKey:     SecretValue{Mode: SecretModeEmpty},
					MaxResults: defaults.Tools.Web.Perplexity.MaxResults,
				},
			},
			Cron: defaults.Tools.Cron,
			Exec: EditableExecConfig{
				EnableDenyPatterns: defaults.Tools.Exec.EnableDenyPatterns,
			},
		},
		Heartbeat: defaults.Heartbeat,
		Devices:   defaults.Devices,
		Logs: EditableLogsConfig{
			Enabled:  defaults.Logs.Enabled,
			Path:     defaults.Logs.Path,
			MaxDays:  defaults.Logs.MaxDays,
			Rotation: defaults.Logs.Rotation,
		},
	}
}
