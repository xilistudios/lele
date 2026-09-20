package config

import (
	"encoding/json"
)

// overlayChannelsWithPlaceholders re-reads the "channels" section of a saved
// file on top of the document that LoadConfig already filled with the runtime
// defaults, so secret fields keep their ENV / keyring placeholders.
//
// Every parser below is a strict overlay: it receives the current channel as
// its base and only replaces the keys present in the file. toSerializable
// prunes default-valued keys, so an absent key is normal and must never reset
// a non-zero default (telegram's verbose level, line's webhook host/port/path,
// onebot's ws_url and reconnect_interval, ...).
func overlayChannelsWithPlaceholders(cfg *EditableChannelsConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) {

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return
	}

	// Parse Telegram with placeholders.
	if telegramRaw, ok := rawMap["telegram"]; ok {
		cfg.Telegram = parseTelegramWithPlaceholders(cfg.Telegram, telegramRaw, basePath+".telegram", secretsByPath)
	}

	// Parse Discord with placeholders.
	if discordRaw, ok := rawMap["discord"]; ok {
		cfg.Discord = parseDiscordWithPlaceholders(cfg.Discord, discordRaw, basePath+".discord", secretsByPath)
	}

	// Parse Feishu with placeholders.
	if feishuRaw, ok := rawMap["feishu"]; ok {
		cfg.Feishu = parseFeishuWithPlaceholders(cfg.Feishu, feishuRaw, basePath+".feishu", secretsByPath)
	}

	// Parse Slack with placeholders.
	if slackRaw, ok := rawMap["slack"]; ok {
		cfg.Slack = parseSlackWithPlaceholders(cfg.Slack, slackRaw, basePath+".slack", secretsByPath)
	}

	// Parse LINE with placeholders.
	if lineRaw, ok := rawMap["line"]; ok {
		cfg.LINE = parseLINEWithPlaceholders(cfg.LINE, lineRaw, basePath+".line", secretsByPath)
	}

	// Parse OneBot with placeholders.
	if onebotRaw, ok := rawMap["onebot"]; ok {
		cfg.OneBot = parseOneBotWithPlaceholders(cfg.OneBot, onebotRaw, basePath+".onebot", secretsByPath)
	}

	// Parse QQ with placeholders.
	if qqRaw, ok := rawMap["qq"]; ok {
		cfg.QQ = parseQQWithPlaceholders(cfg.QQ, qqRaw, basePath+".qq", secretsByPath)
	}

	// Parse DingTalk with placeholders.
	if dingtalkRaw, ok := rawMap["dingtalk"]; ok {
		cfg.DingTalk = parseDingTalkWithPlaceholders(cfg.DingTalk, dingtalkRaw, basePath+".dingtalk", secretsByPath)
	}
}

// parseTelegramWithPlaceholders overlays raw onto base (absent keys are kept).
func parseTelegramWithPlaceholders(base EditableTelegramConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableTelegramConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	// Parse simple fields.
	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if proxy, ok := rawMap["proxy"]; ok {
		json.Unmarshal(proxy, &cfg.Proxy)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if verbose, ok := rawMap["verbose"]; ok {
		json.Unmarshal(verbose, &cfg.Verbose)
	}

	// Parse token with placeholder detection.
	if tokenRaw, ok := rawMap["token"]; ok {
		cfg.Token = parseSecretValue(tokenRaw, basePath+".token", secretsByPath)
	}

	return cfg
}

// parseDiscordWithPlaceholders overlays raw onto base (absent keys are kept).
func parseDiscordWithPlaceholders(base EditableDiscordConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableDiscordConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if tokenRaw, ok := rawMap["token"]; ok {
		cfg.Token = parseSecretValue(tokenRaw, basePath+".token", secretsByPath)
	}

	return cfg
}

// parseFeishuWithPlaceholders overlays raw onto base (absent keys are kept).
func parseFeishuWithPlaceholders(base EditableFeishuConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableFeishuConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if appID, ok := rawMap["app_id"]; ok {
		cfg.AppID = parseSecretValue(appID, basePath+".app_id", secretsByPath)
	}
	if appSecret, ok := rawMap["app_secret"]; ok {
		cfg.AppSecret = parseSecretValue(appSecret, basePath+".app_secret", secretsByPath)
	}
	if encryptKey, ok := rawMap["encrypt_key"]; ok {
		cfg.EncryptKey = parseSecretValue(encryptKey, basePath+".encrypt_key", secretsByPath)
	}
	if verificationToken, ok := rawMap["verification_token"]; ok {
		cfg.VerificationToken = parseSecretValue(verificationToken, basePath+".verification_token", secretsByPath)
	}

	return cfg
}

// parseSlackWithPlaceholders overlays raw onto base (absent keys are kept).
func parseSlackWithPlaceholders(base EditableSlackConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableSlackConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if botToken, ok := rawMap["bot_token"]; ok {
		cfg.BotToken = parseSecretValue(botToken, basePath+".bot_token", secretsByPath)
	}
	if appToken, ok := rawMap["app_token"]; ok {
		cfg.AppToken = parseSecretValue(appToken, basePath+".app_token", secretsByPath)
	}

	return cfg
}

// parseLINEWithPlaceholders overlays raw onto base (absent keys are kept).
func parseLINEWithPlaceholders(base EditableLINEConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableLINEConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if webhookHost, ok := rawMap["webhook_host"]; ok {
		json.Unmarshal(webhookHost, &cfg.WebhookHost)
	}
	if webhookPort, ok := rawMap["webhook_port"]; ok {
		json.Unmarshal(webhookPort, &cfg.WebhookPort)
	}
	if webhookPath, ok := rawMap["webhook_path"]; ok {
		json.Unmarshal(webhookPath, &cfg.WebhookPath)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if channelSecret, ok := rawMap["channel_secret"]; ok {
		cfg.ChannelSecret = parseSecretValue(channelSecret, basePath+".channel_secret", secretsByPath)
	}
	if channelAccessToken, ok := rawMap["channel_access_token"]; ok {
		cfg.ChannelAccessToken = parseSecretValue(channelAccessToken, basePath+".channel_access_token", secretsByPath)
	}

	return cfg
}

// parseOneBotWithPlaceholders overlays raw onto base (absent keys are kept).
func parseOneBotWithPlaceholders(base EditableOneBotConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableOneBotConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if wsUrl, ok := rawMap["ws_url"]; ok {
		json.Unmarshal(wsUrl, &cfg.WSUrl)
	}
	if reconnectInterval, ok := rawMap["reconnect_interval"]; ok {
		json.Unmarshal(reconnectInterval, &cfg.ReconnectInterval)
	}
	if groupTriggerPrefix, ok := rawMap["group_trigger_prefix"]; ok {
		json.Unmarshal(groupTriggerPrefix, &cfg.GroupTriggerPrefix)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if accessToken, ok := rawMap["access_token"]; ok {
		cfg.AccessToken = parseSecretValue(accessToken, basePath+".access_token", secretsByPath)
	}

	return cfg
}

// parseQQWithPlaceholders overlays raw onto base (absent keys are kept).
func parseQQWithPlaceholders(base EditableQQConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableQQConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if appID, ok := rawMap["app_id"]; ok {
		cfg.AppID = parseSecretValue(appID, basePath+".app_id", secretsByPath)
	}
	if appSecret, ok := rawMap["app_secret"]; ok {
		cfg.AppSecret = parseSecretValue(appSecret, basePath+".app_secret", secretsByPath)
	}

	return cfg
}

// parseDingTalkWithPlaceholders overlays raw onto base (absent keys are kept).
func parseDingTalkWithPlaceholders(base EditableDingTalkConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableDingTalkConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if allowFrom, ok := rawMap["allow_from"]; ok {
		json.Unmarshal(allowFrom, &cfg.AllowFrom)
	}
	if clientID, ok := rawMap["client_id"]; ok {
		cfg.ClientID = parseSecretValue(clientID, basePath+".client_id", secretsByPath)
	}
	if clientSecret, ok := rawMap["client_secret"]; ok {
		cfg.ClientSecret = parseSecretValue(clientSecret, basePath+".client_secret", secretsByPath)
	}

	return cfg
}
