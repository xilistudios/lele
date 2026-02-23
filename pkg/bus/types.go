package bus

type InboundMessage struct {
	Channel    string            `json:"channel"`
	SenderID   string            `json:"sender_id"`
	ChatID     string            `json:"chat_id"`
	Content    string            `json:"content"`
	Media      []string          `json:"media,omitempty"`
	SessionKey string            `json:"session_key"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type OutboundMessage struct {
	Channel   string `json:"channel"`
	ChatID    string `json:"chat_id"`
	Content   string `json:"content"`
	ReplyTo   string `json:"reply_to,omitempty"`   // Message ID to reply to
	MessageID string `json:"message_id,omitempty"` // Original command message ID
}

type MessageHandler func(InboundMessage) error
