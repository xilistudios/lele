package tools

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
)

type MessagePayload struct {
	Content     string
	Attachments []bus.FileAttachment
}

type SendCallback func(channel, chatID string, payload MessagePayload) error

type MessageTool struct {
	sendCallback   SendCallback
	defaultChannel string
	defaultChatID  string
	sentInRound    bool // Tracks whether a message was sent in the current processing round
}

func NewMessageTool() *MessageTool {
	return &MessageTool{}
}

func (t *MessageTool) Name() string {
	return "message"
}

func (t *MessageTool) Description() string {
	return "Send a message or file attachments to the user on a chat channel. Use this when you want to communicate something or deliver a generated file."
}

func (t *MessageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Optional text content to send. If file_paths is present, this text is used as message body or caption.",
			},
			"file_paths": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
				"description": "Optional local file paths to send back to the user.",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target channel (telegram, whatsapp, etc.)",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target chat/user ID",
			},
		},
		"required": []string{},
	}
}

func (t *MessageTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
	t.sentInRound = false // Reset send tracking for new processing round
}

// HasSentInRound returns true if the message tool sent a message during the current round.
func (t *MessageTool) HasSentInRound() bool {
	return t.sentInRound
}

func (t *MessageTool) SetSendCallback(callback SendCallback) {
	t.sendCallback = callback
}

func (t *MessageTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	content, _ := args["content"].(string)
	attachments, err := parseMessageAttachments(args["file_paths"])
	if err != nil {
		return &ToolResult{ForLLM: err.Error(), IsError: true}
	}
	if strings.TrimSpace(content) == "" && len(attachments) == 0 {
		return &ToolResult{ForLLM: "content or file_paths is required", IsError: true}
	}

	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)

	if channel == "" {
		channel = t.defaultChannel
	}
	if chatID == "" {
		chatID = t.defaultChatID
	}

	if channel == "" || chatID == "" {
		return &ToolResult{ForLLM: "No target channel/chat specified", IsError: true}
	}

	if t.sendCallback == nil {
		return &ToolResult{ForLLM: "Message sending not configured", IsError: true}
	}

	if err := t.sendCallback(channel, chatID, MessagePayload{Content: content, Attachments: attachments}); err != nil {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("sending message: %v", err),
			IsError: true,
			Err:     err,
		}
	}

	t.sentInRound = true
	// Silent: user already received the message directly
	return &ToolResult{
		ForLLM: fmt.Sprintf("Message sent to %s:%s", channel, chatID),
		Silent: true,
	}
}

func parseMessageAttachments(raw interface{}) ([]bus.FileAttachment, error) {
	if raw == nil {
		return nil, nil
	}

	var paths []string
	switch values := raw.(type) {
	case []string:
		paths = values
	case []interface{}:
		paths = make([]string, 0, len(values))
		for _, value := range values {
			path, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("file_paths must contain only strings")
			}
			paths = append(paths, path)
		}
	default:
		return nil, fmt.Errorf("file_paths must be an array of strings")
	}

	attachments := make([]bus.FileAttachment, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("cannot access file %s: %w", path, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("file path %s is a directory", path)
		}
		attachments = append(attachments, bus.FileAttachment{
			Name:     filepath.Base(path),
			Path:     path,
			MIMEType: mimeTypeForPath(path),
			Kind:     "file",
		})
	}

	return attachments, nil
}

func mimeTypeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "application/octet-stream"
	}
	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		return mimeType
	}
	return "application/octet-stream"
}
