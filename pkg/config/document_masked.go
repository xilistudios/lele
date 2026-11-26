package config

import "encoding/json"

type secretField struct {
	path   string
	secret *SecretValue
}

func (doc *EditableDocument) secretFields() []secretField {
	return []secretField{
		{"channels.telegram.token", &doc.Channels.Telegram.Token},
		{"channels.feishu.app_id", &doc.Channels.Feishu.AppID},
		{"channels.feishu.app_secret", &doc.Channels.Feishu.AppSecret},
		{"channels.feishu.encrypt_key", &doc.Channels.Feishu.EncryptKey},
		{"channels.feishu.verification_token", &doc.Channels.Feishu.VerificationToken},
		{"channels.discord.token", &doc.Channels.Discord.Token},
		{"channels.qq.app_id", &doc.Channels.QQ.AppID},
		{"channels.qq.app_secret", &doc.Channels.QQ.AppSecret},
		{"channels.dingtalk.client_id", &doc.Channels.DingTalk.ClientID},
		{"channels.dingtalk.client_secret", &doc.Channels.DingTalk.ClientSecret},
		{"channels.slack.bot_token", &doc.Channels.Slack.BotToken},
		{"channels.slack.app_token", &doc.Channels.Slack.AppToken},
		{"channels.line.channel_secret", &doc.Channels.LINE.ChannelSecret},
		{"channels.line.channel_access_token", &doc.Channels.LINE.ChannelAccessToken},
		{"channels.onebot.access_token", &doc.Channels.OneBot.AccessToken},
		{"tools.web.brave.api_key", &doc.Tools.Web.Brave.APIKey},
		{"tools.web.perplexity.api_key", &doc.Tools.Web.Perplexity.APIKey},
	}
}

func (doc *EditableDocument) forEachSecret(fn func(path string, sv *SecretValue)) {
	for _, field := range doc.secretFields() {
		fn(field.path, field.secret)
	}
	for name, provider := range doc.Providers {
		fn("providers."+name+".api_key", &provider.APIKey)
		// the callback writes through a pointer into the range copy, so the entry must be stored back
		doc.Providers[name] = provider
	}
}

func (doc *EditableDocument) SecretValues() map[string]SecretValue {
	values := make(map[string]SecretValue)
	doc.forEachSecret(func(path string, sv *SecretValue) {
		values[path] = *sv
	})
	return values
}

func (doc *EditableDocument) MaskedCopy() (*EditableDocument, error) {
	masked, err := doc.deepCopy()
	if err != nil {
		return nil, err
	}
	masked.forEachSecret(func(_ string, sv *SecretValue) {
		if sv.EnvDefault != nil {
			sv.HasDefault = true
			sv.EnvDefault = nil
		}
		if sv.Mode != SecretModeLiteral || sv.Value == "" {
			return
		}
		sv.HasValue = true
		sv.Value = ""
	})
	return masked, nil
}

func (doc *EditableDocument) RestoreMaskedSecrets(stored map[string]SecretValue) {
	doc.forEachSecret(func(path string, sv *SecretValue) {
		kept, ok := stored[path]
		if !ok {
			return
		}
		if sv.Mode == SecretModeEnv && sv.EnvDefault == nil && kept.EnvDefault != nil {
			sv.EnvDefault = kept.EnvDefault
			sv.HasDefault = true
		}
		if sv.Mode == SecretModeLiteral && sv.Value == "" && kept.Mode == SecretModeLiteral {
			sv.Value = kept.Value
			sv.HasValue = true
		}
	})
}

func (doc *EditableDocument) deepCopy() (*EditableDocument, error) {
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var copy EditableDocument
	if err := json.Unmarshal(encoded, &copy); err != nil {
		return nil, err
	}
	return &copy, nil
}
