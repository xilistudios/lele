package bus

import "time"

type FileAttachment struct {
	Name      string `json:"name,omitempty"`
	Path      string `json:"path,omitempty"`
	MIMEType  string `json:"mime_type,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Caption   string `json:"caption,omitempty"`
	Temporary bool   `json:"temporary,omitempty"`
}

type InboundMessage struct {
	Channel     string            `json:"channel"`
	SenderID    string            `json:"sender_id"`
	ChatID      string            `json:"chat_id"`
	Content     string            `json:"content"`
	Media       []string          `json:"media,omitempty"`
	Attachments []FileAttachment  `json:"attachments,omitempty"`
	SessionKey  string            `json:"session_key"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	SpoolID     int64             `json:"-"`
	DedupeID    string            `json:"dedupe_id,omitempty"`
}

type OutboundMessage struct {
	Channel        string            `json:"channel"`
	ChatID         string            `json:"chat_id"`
	Event          string            `json:"event,omitempty"`
	Content        string            `json:"content"`
	Attachments    []FileAttachment  `json:"attachments,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	ReplyTo        string            `json:"reply_to,omitempty"`
	MessageID      string            `json:"message_id,omitempty"`
	ReplyMarkup    interface{}       `json:"reply_markup,omitempty"`
	IsIntermediate bool              `json:"is_intermediate,omitempty"`
	TextMode       string            `json:"text_mode,omitempty"`
	PlainText      string            `json:"plain_text,omitempty"`
	LinkPreview    *bool             `json:"link_preview,omitempty"`
	SpoolID        int64             `json:"-"`
}

type MessageHandler func(InboundMessage) error

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
)

type ApprovalRequest struct {
	ID          string         `json:"id"`
	SessionKey  string         `json:"session_key"`
	Command     string         `json:"command"`
	Reason      string         `json:"reason,omitempty"`
	Status      ApprovalStatus `json:"status"`
	RequestedAt time.Time      `json:"requested_at"`
}
