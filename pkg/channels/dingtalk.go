package channels

import (
	"context"
	"fmt"
	"sync"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils"
)

type DingTalkChannel struct {
	*BaseChannel
	config          config.DingTalkConfig
	clientID        string
	clientSecret    string
	streamClient    *client.StreamClient
	ctx             context.Context
	cancel          context.CancelFunc
	sessionWebhooks sync.Map
}

func NewDingTalkChannel(cfg config.DingTalkConfig, messageBus *bus.MessageBus) (*DingTalkChannel, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("dingtalk client_id and client_secret are required")
	}

	base := NewBaseChannel("dingtalk", cfg, messageBus, cfg.AllowFrom)

	return &DingTalkChannel{
		BaseChannel:  base,
		config:       cfg,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
	}, nil
}

func (c *DingTalkChannel) Start(ctx context.Context) error {

	c.ctx, c.cancel = context.WithCancel(ctx)

	cred := client.NewAppCredentialConfig(c.clientID, c.clientSecret)

	c.streamClient = client.NewStreamClient(
		client.WithAppCredential(cred),
		client.WithAutoReconnect(true),
	)

	c.streamClient.RegisterChatBotCallbackRouter(c.onChatBotMessageReceived)

	if err := c.streamClient.Start(c.ctx); err != nil {
		return fmt.Errorf("failed to start stream client: %w", err)
	}

	c.setRunning(true)
	return nil
}

func (c *DingTalkChannel) Stop(ctx context.Context) error {

	if c.cancel != nil {
		c.cancel()
	}

	if c.streamClient != nil {
		c.streamClient.Close()
	}

	c.setRunning(false)
	return nil
}

func (c *DingTalkChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("dingtalk channel not running")
	}

	sessionWebhookRaw, ok := c.sessionWebhooks.Load(msg.ChatID)
	if !ok {
		return fmt.Errorf("no session_webhook found for chat %s, cannot send message", msg.ChatID)
	}

	sessionWebhook, ok := sessionWebhookRaw.(string)
	if !ok {
		return fmt.Errorf("invalid session_webhook type for chat %s", msg.ChatID)
	}

	logger.DebugCF("dingtalk", "Sending message", map[string]interface{}{
		"chat_id": msg.ChatID,
		"preview": utils.Truncate(msg.Content, 100),
	})

	return c.SendDirectReply(ctx, sessionWebhook, msg.Content)
}

func (c *DingTalkChannel) onChatBotMessageReceived(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	content := data.Text.Content
	if content == "" {
		if contentMap, ok := data.Content.(map[string]interface{}); ok {
			if textContent, ok := contentMap["content"].(string); ok {
				content = textContent
			}
		}
	}

	if content == "" {
		return nil, nil
	}

	senderID := data.SenderStaffId
	senderNick := data.SenderNick
	chatID := senderID
	if data.ConversationType != "1" {
		chatID = data.ConversationId
	}

	c.sessionWebhooks.Store(chatID, data.SessionWebhook)

	metadata := map[string]string{
		"sender_name":       senderNick,
		"conversation_id":   data.ConversationId,
		"conversation_type": data.ConversationType,
		"platform":          "dingtalk",
		"session_webhook":   data.SessionWebhook,
	}

	peerKind := "direct"
	if data.ConversationType != "1" {
		peerKind = "group"
	}
	metadata["peer_kind"] = peerKind
	metadata["peer_id"] = chatID

	logger.DebugCF("dingtalk", "Received message", map[string]interface{}{
		"sender_nick": senderNick,
		"sender_id":   senderID,
		"preview":     utils.Truncate(content, 50),
	})

	c.HandleMessage(senderID, chatID, content, nil, metadata)

	return nil, nil
}

func (c *DingTalkChannel) SendDirectReply(ctx context.Context, sessionWebhook, content string) error {
	replier := chatbot.NewChatbotReplier()

	contentBytes := []byte(content)
	titleBytes := []byte("Lele")

	err := replier.SimpleReplyMarkdown(
		ctx,
		sessionWebhook,
		titleBytes,
		contentBytes,
	)

	if err != nil {
		return fmt.Errorf("failed to send reply: %w", err)
	}

	return nil
}
