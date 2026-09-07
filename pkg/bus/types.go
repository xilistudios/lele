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
	// SpoolID is the internal handle of the durable spool row that backs this
	// message, or 0 when durability is disabled or the row could not be
	// written. It is never serialised: the id belongs to the database, not to
	// the wire, and the replay path re-attaches it from the row it claimed.
	SpoolID int64 `json:"-"`
	// DedupeID is the idempotency key this message is recorded under in the
	// processed_messages ledger. Unlike SpoolID it must survive JSON
	// round-trips: it travels inside the durable spool payload, so a replayed
	// message can still be recognised as a duplicate.
	DedupeID string `json:"dedupe_id,omitempty"`
}

type OutboundMessage struct {
	Channel        string            `json:"channel"`
	ChatID         string            `json:"chat_id"`
	Event          string            `json:"event,omitempty"`
	Content        string            `json:"content"`
	Attachments    []FileAttachment  `json:"attachments,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	ReplyTo        string            `json:"reply_to,omitempty"`        // Message ID to reply to
	MessageID      string            `json:"message_id,omitempty"`      // Original command message ID
	ReplyMarkup    interface{}       `json:"reply_markup,omitempty"`    // Optional inline keyboard markup for Telegram
	IsIntermediate bool              `json:"is_intermediate,omitempty"` // If true, does not stop typing indicator
	TextMode       string            `json:"text_mode,omitempty"`       // "markdown" (default) or "html"
	PlainText      string            `json:"plain_text,omitempty"`      // Fallback plain text if formatting fails
	LinkPreview    *bool             `json:"link_preview,omitempty"`    // Enable/disable link previews (nil = default true)
}

// MessageHandler is a function that handles incoming messages
type MessageHandler func(InboundMessage) error

// ApprovalStatus represents the current status of an approval request
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
)

// ApprovalRequest represents a request for command approval
type ApprovalRequest struct {
	ID          string         `json:"id"`
	SessionKey  string         `json:"session_key"`
	Command     string         `json:"command"`
	Reason      string         `json:"reason,omitempty"`
	Status      ApprovalStatus `json:"status"`
	RequestedAt time.Time      `json:"requested_at"`
}
