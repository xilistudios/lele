package channels

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils"
)

const (
	lineAPIBase          = "https://api.line.me/v2/bot"
	lineDataAPIBase      = "https://api-data.line.me/v2/bot"
	lineReplyEndpoint    = lineAPIBase + "/message/reply"
	linePushEndpoint     = lineAPIBase + "/message/push"
	lineContentEndpoint  = lineDataAPIBase + "/message/%s/content"
	lineBotInfoEndpoint  = lineAPIBase + "/info"
	lineLoadingEndpoint  = lineAPIBase + "/chat/loading/start"
	lineReplyTokenMaxAge = 25 * time.Second
)

type replyTokenEntry struct {
	token     string
	timestamp time.Time
}

type LINEChannel struct {
	*BaseChannel
	config         config.LINEConfig
	botUserID      string
	botBasicID     string
	botDisplayName string
	replyTokens    sync.Map
	quoteTokens    sync.Map
	ctx            context.Context
	cancel         context.CancelFunc
}

func NewLINEChannel(cfg config.LINEConfig, messageBus *bus.MessageBus) (*LINEChannel, error) {
	if cfg.ChannelSecret == "" || cfg.ChannelAccessToken == "" {
		return nil, fmt.Errorf("line channel_secret and channel_access_token are required")
	}

	base := NewBaseChannel("line", cfg, messageBus, cfg.AllowFrom)

	return &LINEChannel{
		BaseChannel: base,
		config:      cfg,
	}, nil
}

func (c *LINEChannel) Start(ctx context.Context) error {

	c.ctx, c.cancel = context.WithCancel(ctx)

	if err := c.fetchBotInfo(); err != nil {
		logger.WarnCF("line", "Failed to fetch bot info (mention detection disabled)", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		logger.InfoCF("line", "Bot info fetched", map[string]interface{}{
			"bot_user_id":  c.botUserID,
			"basic_id":     c.botBasicID,
			"display_name": c.botDisplayName,
		})
	}

	c.setRunning(true)
	return nil
}

func (c *LINEChannel) fetchBotInfo() error {
	req, err := http.NewRequest(http.MethodGet, lineBotInfoEndpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.config.ChannelAccessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bot info API returned status %d", resp.StatusCode)
	}

	var info struct {
		UserID      string `json:"userId"`
		BasicID     string `json:"basicId"`
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return err
	}

	c.botUserID = info.UserID
	c.botBasicID = info.BasicID
	c.botDisplayName = info.DisplayName
	return nil
}

func (c *LINEChannel) Stop(ctx context.Context) error {

	if c.cancel != nil {
		c.cancel()
	}

	c.setRunning(false)
	return nil
}

func (c *LINEChannel) RegisterWebhook(mux *http.ServeMux) {
	path := c.config.WebhookPath
	if path == "" {
		path = "/webhook/line"
	}
	mux.HandleFunc(path, c.webhookHandler)
}

func (c *LINEChannel) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.ErrorCF("line", "Failed to read request body", map[string]interface{}{
			"error": err.Error(),
		})
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("X-Line-Signature")
	if !c.verifySignature(body, signature) {
		logger.WarnC("line", "Invalid webhook signature")
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var payload struct {
		Events []lineEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		logger.ErrorCF("line", "Failed to parse webhook payload", map[string]interface{}{
			"error": err.Error(),
		})
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)

	for _, event := range payload.Events {
		go c.processEvent(event)
	}
}

func (c *LINEChannel) verifySignature(body []byte, signature string) bool {
	if signature == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(c.config.ChannelSecret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

type lineEvent struct {
	Type       string          `json:"type"`
	ReplyToken string          `json:"replyToken"`
	Source     lineSource      `json:"source"`
	Message    json.RawMessage `json:"message"`
	Timestamp  int64           `json:"timestamp"`
}

type lineSource struct {
	Type    string `json:"type"`
	UserID  string `json:"userId"`
	GroupID string `json:"groupId"`
	RoomID  string `json:"roomId"`
}

type lineMessage struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Text       string `json:"text"`
	QuoteToken string `json:"quoteToken"`
	Mention    *struct {
		Mentionees []lineMentionee `json:"mentionees"`
	} `json:"mention"`
	ContentProvider struct {
		Type string `json:"type"`
	} `json:"contentProvider"`
}

type lineMentionee struct {
	Index  int    `json:"index"`
	Length int    `json:"length"`
	Type   string `json:"type"`
	UserID string `json:"userId"`
}

func (c *LINEChannel) processEvent(event lineEvent) {
	if event.Type != "message" {
		logger.DebugCF("line", "Ignoring non-message event", map[string]interface{}{
			"type": event.Type,
		})
		return
	}

	senderID := event.Source.UserID
	chatID := c.resolveChatID(event.Source)
	isGroup := event.Source.Type == "group" || event.Source.Type == "room"

	var msg lineMessage
	if err := json.Unmarshal(event.Message, &msg); err != nil {
		logger.ErrorCF("line", "Failed to parse message", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	if isGroup && !c.isBotMentioned(msg) {
		logger.DebugCF("line", "Ignoring group message without mention", map[string]interface{}{
			"chat_id": chatID,
		})
		return
	}

	if event.ReplyToken != "" {
		c.replyTokens.Store(chatID, replyTokenEntry{
			token:     event.ReplyToken,
			timestamp: time.Now(),
		})
	}

	if msg.QuoteToken != "" {
		c.quoteTokens.Store(chatID, msg.QuoteToken)
	}

	var content string
	var mediaPaths []string
	localFiles := []string{}

	defer func() {
		for _, file := range localFiles {
			if err := os.Remove(file); err != nil {
				logger.DebugCF("line", "Failed to cleanup temp file", map[string]interface{}{
					"file":  file,
					"error": err.Error(),
				})
			}
		}
	}()

	switch msg.Type {
	case "text":
		content = msg.Text
		if isGroup {
			content = c.stripBotMention(content, msg)
		}
	case "image":
		localPath := c.downloadContent(msg.ID, "image.jpg")
		if localPath != "" {
			localFiles = append(localFiles, localPath)
			mediaPaths = append(mediaPaths, localPath)
			content = "[image]"
		}
	case "audio":
		localPath := c.downloadContent(msg.ID, "audio.m4a")
		if localPath != "" {
			localFiles = append(localFiles, localPath)
			mediaPaths = append(mediaPaths, localPath)
			content = "[audio]"
		}
	case "video":
		localPath := c.downloadContent(msg.ID, "video.mp4")
		if localPath != "" {
			localFiles = append(localFiles, localPath)
			mediaPaths = append(mediaPaths, localPath)
			content = "[video]"
		}
	case "file":
		content = "[file]"
	case "sticker":
		content = "[sticker]"
	default:
		content = fmt.Sprintf("[%s]", msg.Type)
	}

	if strings.TrimSpace(content) == "" {
		return
	}

	metadata := map[string]string{
		"platform":    "line",
		"source_type": event.Source.Type,
		"message_id":  msg.ID,
	}

	peerKind := "direct"
	if isGroup {
		peerKind = "group"
	}
	metadata["peer_kind"] = peerKind
	metadata["peer_id"] = chatID

	logger.DebugCF("line", "Received message", map[string]interface{}{
		"sender_id":    senderID,
		"chat_id":      chatID,
		"message_type": msg.Type,
		"is_group":     isGroup,
		"preview":      utils.Truncate(content, 50),
	})

	c.sendLoading(senderID)

	c.HandleMessage(senderID, chatID, content, mediaPaths, metadata)
}

func (c *LINEChannel) isBotMentioned(msg lineMessage) bool {
	if msg.Mention != nil {
		for _, m := range msg.Mention.Mentionees {
			if m.Type == "all" {
				return true
			}
			if c.botUserID != "" && m.UserID == c.botUserID {
				return true
			}
		}
		if c.botDisplayName != "" {
			for _, m := range msg.Mention.Mentionees {
				if m.Index >= 0 && m.Length > 0 {
					runes := []rune(msg.Text)
					end := m.Index + m.Length
					if end <= len(runes) {
						mentionText := string(runes[m.Index:end])
						if strings.Contains(mentionText, c.botDisplayName) {
							return true
						}
					}
				}
			}
		}
	}

	if c.botDisplayName != "" && strings.Contains(msg.Text, "@"+c.botDisplayName) {
		return true
	}

	return false
}

func (c *LINEChannel) stripBotMention(text string, msg lineMessage) string {
	stripped := false

	if msg.Mention != nil {
		runes := []rune(text)
		for i := len(msg.Mention.Mentionees) - 1; i >= 0; i-- {
			m := msg.Mention.Mentionees[i]
			shouldStrip := false
			if c.botUserID != "" && m.UserID == c.botUserID {
				shouldStrip = true
			} else if c.botDisplayName != "" && m.Index >= 0 && m.Length > 0 {
				end := m.Index + m.Length
				if end <= len(runes) {
					mentionText := string(runes[m.Index:end])
					if strings.Contains(mentionText, c.botDisplayName) {
						shouldStrip = true
					}
				}
			}
			if shouldStrip {
				start := m.Index
				end := m.Index + m.Length
				if start >= 0 && end <= len(runes) {
					runes = append(runes[:start], runes[end:]...)
					stripped = true
				}
			}
		}
		if stripped {
			return strings.TrimSpace(string(runes))
		}
	}

	if c.botDisplayName != "" {
		text = strings.ReplaceAll(text, "@"+c.botDisplayName, "")
	}

	return strings.TrimSpace(text)
}

func (c *LINEChannel) resolveChatID(source lineSource) string {
	switch source.Type {
	case "group":
		return source.GroupID
	case "room":
		return source.RoomID
	default:
		return source.UserID
	}
}

func (c *LINEChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("line channel not running")
	}

	var quoteToken string
	if qt, ok := c.quoteTokens.LoadAndDelete(msg.ChatID); ok {
		quoteToken = qt.(string)
	}

	if entry, ok := c.replyTokens.LoadAndDelete(msg.ChatID); ok {
		tokenEntry := entry.(replyTokenEntry)
		if time.Since(tokenEntry.timestamp) < lineReplyTokenMaxAge {
			if err := c.sendReply(ctx, tokenEntry.token, msg.Content, quoteToken); err == nil {
				logger.DebugCF("line", "Message sent via Reply API", map[string]interface{}{
					"chat_id": msg.ChatID,
					"quoted":  quoteToken != "",
				})
				return nil
			}
			logger.DebugC("line", "Reply API failed, falling back to Push API")
		}
	}

	return c.sendPush(ctx, msg.ChatID, msg.Content, quoteToken)
}

func buildTextMessage(content, quoteToken string) map[string]string {
	msg := map[string]string{
		"type": "text",
		"text": content,
	}
	if quoteToken != "" {
		msg["quoteToken"] = quoteToken
	}
	return msg
}

func (c *LINEChannel) sendReply(ctx context.Context, replyToken, content, quoteToken string) error {
	payload := map[string]interface{}{
		"replyToken": replyToken,
		"messages":   []map[string]string{buildTextMessage(content, quoteToken)},
	}

	return c.callAPI(ctx, lineReplyEndpoint, payload)
}

func (c *LINEChannel) sendPush(ctx context.Context, to, content, quoteToken string) error {
	payload := map[string]interface{}{
		"to":       to,
		"messages": []map[string]string{buildTextMessage(content, quoteToken)},
	}

	return c.callAPI(ctx, linePushEndpoint, payload)
}

func (c *LINEChannel) sendLoading(chatID string) {
	payload := map[string]interface{}{
		"chatId":         chatID,
		"loadingSeconds": 60,
	}
	if err := c.callAPI(c.ctx, lineLoadingEndpoint, payload); err != nil {
		logger.DebugCF("line", "Failed to send loading indicator", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func (c *LINEChannel) callAPI(ctx context.Context, endpoint string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.ChannelAccessToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("LINE API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (c *LINEChannel) downloadContent(messageID, filename string) string {
	url := fmt.Sprintf(lineContentEndpoint, messageID)
	return utils.DownloadFile(url, filename, utils.DownloadOptions{
		LoggerPrefix: "line",
		ExtraHeaders: map[string]string{
			"Authorization": "Bearer " + c.config.ChannelAccessToken,
		},
	})
}
