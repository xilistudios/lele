package agent

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
)

func TestFormatMessagesForLog_EmptySlice(t *testing.T) {
	result := FormatMessagesForLog([]providers.Message{})
	if result != "[]" {
		t.Errorf("Expected '[]' for empty slice, got: %s", result)
	}
}

func TestFormatMessagesForLog_NilSlice(t *testing.T) {
	result := FormatMessagesForLog(nil)
	if result != "[]" {
		t.Errorf("Expected '[]' for nil slice, got: %s", result)
	}
}

func TestFormatMessagesForLog_SingleMessage(t *testing.T) {
	messages := []providers.Message{
		{Role: "user", Content: "Hello"},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "[0] Role: user") {
		t.Error("Expected result to contain message index and role")
	}
	if !strings.Contains(result, "Content: Hello") {
		t.Error("Expected result to contain message content")
	}
}

func TestFormatMessagesForLog_MultipleMessages(t *testing.T) {
	messages := []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "How are you?"},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "[0] Role: user") {
		t.Error("Expected result to contain [0] for first message")
	}
	if !strings.Contains(result, "[1] Role: assistant") {
		t.Error("Expected result to contain [1] for second message")
	}
	if !strings.Contains(result, "[2] Role: user") {
		t.Error("Expected result to contain [2] for third message")
	}
}

func TestFormatMessagesForLog_WithToolCalls(t *testing.T) {
	messages := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:       "call-1",
					Type:     "function",
					Name:     "test_tool",
					Function: &providers.FunctionCall{Name: "test_tool", Arguments: `{"arg": "value"}`},
				},
			},
		},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "ToolCalls:") {
		t.Error("Expected result to contain ToolCalls section")
	}
	if !strings.Contains(result, "ID: call-1") {
		t.Error("Expected result to contain tool call ID")
	}
	if !strings.Contains(result, "Type: function") {
		t.Error("Expected result to contain tool call type")
	}
	if !strings.Contains(result, "Name: test_tool") {
		t.Error("Expected result to contain tool call name")
	}
	if !strings.Contains(result, "Arguments:") {
		t.Error("Expected result to contain arguments")
	}
}

func TestFormatMessagesForLog_WithToolCallID(t *testing.T) {
	messages := []providers.Message{
		{Role: "tool", Content: "Tool result", ToolCallID: "call-123"},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "ToolCallID: call-123") {
		t.Error("Expected result to contain ToolCallID")
	}
}

func TestFormatMessagesForLog_EmptyContent(t *testing.T) {
	messages := []providers.Message{
		{Role: "assistant", Content: ""},
	}
	result := FormatMessagesForLog(messages)

	if strings.Contains(result, "Content:") {
		t.Error("Expected result to not contain Content section when empty")
	}
}

func TestFormatMessagesForLog_LongContentTruncation(t *testing.T) {
	longContent := strings.Repeat("a", 300)
	messages := []providers.Message{
		{Role: "user", Content: longContent},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "...") {
		t.Error("Expected long content to be truncated with '...'")
	}
}

func TestFormatMessagesForLog_MultipleToolCalls(t *testing.T) {
	messages := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "call-1", Type: "function", Name: "tool1"},
				{ID: "call-2", Type: "function", Name: "tool2"},
			},
		},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "call-1") {
		t.Error("Expected result to contain first tool call")
	}
	if !strings.Contains(result, "call-2") {
		t.Error("Expected result to contain second tool call")
	}
}

func TestFormatMessagesForLog_ToolCallWithoutFunction(t *testing.T) {
	messages := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "call-1", Type: "function", Name: "test_tool", Function: nil},
			},
		},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "call-1") {
		t.Error("Expected result to contain tool call ID even without function")
	}
}

func TestFormatMessagesForLog_VideoPart(t *testing.T) {
	messages := []providers.Message{
		{
			Role: "user",
			ContentParts: []providers.ContentPart{
				{Type: "text", Text: "Analyze this video"},
				{Type: "video_url", VideoURL: &providers.VideoURL{URL: "data:video/mp4;base64,SECRETBASE64PAYLOAD", FPS: 1}},
			},
		},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "video_url: [video]") {
		t.Errorf("Expected video_url part to render as [video], got: %s", result)
	}
	if strings.Contains(result, "SECRETBASE64PAYLOAD") || strings.Contains(result, "data:video") {
		t.Errorf("Video URL/base64 payload must never appear in logs: %s", result)
	}
}

func TestFormatToolsForLog_EmptySlice(t *testing.T) {
	result := FormatToolsForLog([]providers.ToolDefinition{})
	if result != "[]" {
		t.Errorf("Expected '[]' for empty slice, got: %s", result)
	}
}

func TestFormatToolsForLog_NilSlice(t *testing.T) {
	result := FormatToolsForLog(nil)
	if result != "[]" {
		t.Errorf("Expected '[]' for nil slice, got: %s", result)
	}
}

func TestFormatToolsForLog_SingleTool(t *testing.T) {
	tools := []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "test_tool",
				Description: "A test tool",
				Parameters:  map[string]interface{}{},
			},
		},
	}
	result := FormatToolsForLog(tools)

	if !strings.Contains(result, "[0] Type: function") {
		t.Error("Expected result to contain tool index and type")
	}
	if !strings.Contains(result, "Name: test_tool") {
		t.Error("Expected result to contain tool name")
	}
	if !strings.Contains(result, "Description: A test tool") {
		t.Error("Expected result to contain tool description")
	}
}

func TestFormatToolsForLog_MultipleTools(t *testing.T) {
	tools := []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "tool1",
				Description: "First tool",
			},
		},
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "tool2",
				Description: "Second tool",
			},
		},
	}
	result := FormatToolsForLog(tools)

	if !strings.Contains(result, "[0]") {
		t.Error("Expected result to contain [0]")
	}
	if !strings.Contains(result, "[1]") {
		t.Error("Expected result to contain [1]")
	}
	if !strings.Contains(result, "tool1") {
		t.Error("Expected result to contain tool1")
	}
	if !strings.Contains(result, "tool2") {
		t.Error("Expected result to contain tool2")
	}
}

func TestFormatToolsForLog_WithParameters(t *testing.T) {
	tools := []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "search",
				Description: "Search tool",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		},
	}
	result := FormatToolsForLog(tools)

	if !strings.Contains(result, "Parameters:") {
		t.Error("Expected result to contain Parameters section")
	}
}

func TestFormatToolsForLog_EmptyParameters(t *testing.T) {
	tools := []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "simple_tool",
				Description: "Simple tool without parameters",
				Parameters:  map[string]interface{}{},
			},
		},
	}
	result := FormatToolsForLog(tools)

	if strings.Contains(result, "Parameters:") {
		t.Error("Expected result to not contain Parameters section when empty")
	}
}

func TestFormatToolsForLog_LongParametersTruncation(t *testing.T) {
	longParams := map[string]interface{}{
		"data": strings.Repeat("x", 300),
	}
	tools := []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "tool_with_long_params",
				Description: "Tool with long parameters",
				Parameters:  longParams,
			},
		},
	}
	result := FormatToolsForLog(tools)

	if !strings.Contains(result, "...") {
		t.Error("Expected long parameters to be truncated with '...'")
	}
}

func TestFormatProviderModel_NormalCase(t *testing.T) {
	result := FormatProviderModel("openai", "gpt-4")
	expected := "openai:gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_AlreadyPrefixed(t *testing.T) {
	result := FormatProviderModel("openai", "openai:gpt-4")
	expected := "openai:gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_EmptyProvider(t *testing.T) {
	result := FormatProviderModel("", "gpt-4")
	expected := "gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_EmptyModel(t *testing.T) {
	result := FormatProviderModel("openai", "")
	expected := "openai:"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_BothEmpty(t *testing.T) {
	result := FormatProviderModel("", "")
	expected := ""
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_WhitespaceTrimming(t *testing.T) {
	result := FormatProviderModel("  openai  ", "  gpt-4  ")
	expected := "openai:gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_DifferentProviderPrefix(t *testing.T) {
	result := FormatProviderModel("anthropic", "claude-3")
	expected := "anthropic:claude-3"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_CaseSensitivity(t *testing.T) {
	result := FormatProviderModel("OpenAI", "GPT-4")
	expected := "OpenAI:GPT-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestFormatProviderModel_PartialPrefix(t *testing.T) {
	result := FormatProviderModel("openai", "anthropic:claude-3")
	expected := "openai:anthropic:claude-3"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

func TestExtractPeer_NormalCase(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"peer_kind": "direct",
			"peer_id":   "user-123",
		},
	}
	peer := ExtractPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil peer")
	}
	if peer.Kind != "direct" {
		t.Errorf("Expected Kind 'direct', got: %s", peer.Kind)
	}
	if peer.ID != "user-123" {
		t.Errorf("Expected ID 'user-123', got: %s", peer.ID)
	}
}

func TestExtractPeer_NoPeerKind(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"peer_id": "user-123",
		},
	}
	peer := ExtractPeer(msg)

	if peer != nil {
		t.Error("Expected nil peer when peer_kind is missing")
	}
}

func TestExtractPeer_NoMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: nil,
	}
	peer := ExtractPeer(msg)

	if peer != nil {
		t.Error("Expected nil peer when metadata is nil")
	}
}

func TestExtractPeer_EmptyMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{},
	}
	peer := ExtractPeer(msg)

	if peer != nil {
		t.Error("Expected nil peer when metadata is empty")
	}
}

func TestExtractPeer_DirectKindNoPeerID(t *testing.T) {
	msg := bus.InboundMessage{
		SenderID: "sender-456",
		Metadata: map[string]string{
			"peer_kind": "direct",
		},
	}
	peer := ExtractPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil peer")
	}
	if peer.Kind != "direct" {
		t.Errorf("Expected Kind 'direct', got: %s", peer.Kind)
	}
	if peer.ID != "sender-456" {
		t.Errorf("Expected ID 'sender-456', got: %s", peer.ID)
	}
}

func TestExtractPeer_GroupKindNoPeerID(t *testing.T) {
	msg := bus.InboundMessage{
		ChatID:   "chat-789",
		SenderID: "sender-456",
		Metadata: map[string]string{
			"peer_kind": "group",
		},
	}
	peer := ExtractPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil peer")
	}
	if peer.Kind != "group" {
		t.Errorf("Expected Kind 'group', got: %s", peer.Kind)
	}
	if peer.ID != "chat-789" {
		t.Errorf("Expected ID 'chat-789', got: %s", peer.ID)
	}
}

func TestExtractPeer_ChannelKindNoPeerID(t *testing.T) {
	msg := bus.InboundMessage{
		ChatID: "channel-abc",
		Metadata: map[string]string{
			"peer_kind": "channel",
		},
	}
	peer := ExtractPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil peer")
	}
	if peer.Kind != "channel" {
		t.Errorf("Expected Kind 'channel', got: %s", peer.Kind)
	}
	if peer.ID != "channel-abc" {
		t.Errorf("Expected ID 'channel-abc', got: %s", peer.ID)
	}
}

func TestExtractPeer_PeerIDOverridesSenderID(t *testing.T) {
	msg := bus.InboundMessage{
		SenderID: "sender-456",
		Metadata: map[string]string{
			"peer_kind": "direct",
			"peer_id":   "explicit-peer",
		},
	}
	peer := ExtractPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil peer")
	}
	if peer.ID != "explicit-peer" {
		t.Errorf("Expected ID 'explicit-peer', got: %s", peer.ID)
	}
}

func TestExtractPeer_EmptyPeerKind(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"peer_kind": "",
			"peer_id":   "user-123",
		},
	}
	peer := ExtractPeer(msg)

	if peer != nil {
		t.Error("Expected nil peer when peer_kind is empty")
	}
}

func TestExtractParentPeer_NormalCase(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_kind": "direct",
			"parent_peer_id":   "parent-123",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil parent peer")
	}
	if peer.Kind != "direct" {
		t.Errorf("Expected Kind 'direct', got: %s", peer.Kind)
	}
	if peer.ID != "parent-123" {
		t.Errorf("Expected ID 'parent-123', got: %s", peer.ID)
	}
}

func TestExtractParentPeer_NoParentKind(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_id": "parent-123",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when parent_peer_kind is missing")
	}
}

func TestExtractParentPeer_NoParentID(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_kind": "direct",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when parent_peer_id is missing")
	}
}

func TestExtractParentPeer_NoMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: nil,
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when metadata is nil")
	}
}

func TestExtractParentPeer_EmptyMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{},
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when metadata is empty")
	}
}

func TestExtractParentPeer_GroupKind(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_kind": "group",
			"parent_peer_id":   "group-456",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil parent peer")
	}
	if peer.Kind != "group" {
		t.Errorf("Expected Kind 'group', got: %s", peer.Kind)
	}
	if peer.ID != "group-456" {
		t.Errorf("Expected ID 'group-456', got: %s", peer.ID)
	}
}

func TestExtractParentPeer_ChannelKind(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_kind": "channel",
			"parent_peer_id":   "channel-789",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil parent peer")
	}
	if peer.Kind != "channel" {
		t.Errorf("Expected Kind 'channel', got: %s", peer.Kind)
	}
	if peer.ID != "channel-789" {
		t.Errorf("Expected ID 'channel-789', got: %s", peer.ID)
	}
}

func TestExtractParentPeer_EmptyValues(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_kind": "",
			"parent_peer_id":   "",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when both values are empty")
	}
}

func TestExtractParentPeer_OnlyKindEmpty(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_kind": "",
			"parent_peer_id":   "parent-123",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when kind is empty")
	}
}

func TestExtractParentPeer_OnlyIDEmpty(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"parent_peer_kind": "direct",
			"parent_peer_id":   "",
		},
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when ID is empty")
	}
}

func TestGatewayVersion_ReturnsString(t *testing.T) {
	version := GatewayVersion()

	if version == "" {
		t.Error("Expected non-empty version string")
	}
}

func TestGatewayVersion_ReturnsDevOrVersion(t *testing.T) {
	version := GatewayVersion()

	if version == "" {
		t.Error("Expected version to not be empty")
	}

	if version != "dev" && !strings.Contains(version, ".") && !strings.Contains(version, "v") {
		t.Logf("Version returned: %s (this may be expected in test environment)", version)
	}
}

func TestGatewayVersion_Consistency(t *testing.T) {
	version1 := GatewayVersion()
	version2 := GatewayVersion()

	if version1 != version2 {
		t.Errorf("Expected consistent version, got '%s' and '%s'", version1, version2)
	}
}

func TestGatewayVersion_NotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("GatewayVersion panicked: %v", r)
		}
	}()

	_ = GatewayVersion()
}

func TestExtractPeerAndParentPeer_Together(t *testing.T) {
	msg := bus.InboundMessage{
		SenderID: "sender-123",
		ChatID:   "chat-456",
		Metadata: map[string]string{
			"peer_kind":        "direct",
			"peer_id":          "peer-789",
			"parent_peer_kind": "group",
			"parent_peer_id":   "parent-group",
		},
	}

	peer := ExtractPeer(msg)
	parentPeer := ExtractParentPeer(msg)

	if peer == nil {
		t.Fatal("Expected non-nil peer")
	}
	if parentPeer == nil {
		t.Fatal("Expected non-nil parent peer")
	}

	if peer.Kind != "direct" || peer.ID != "peer-789" {
		t.Error("Peer extraction failed")
	}

	if parentPeer.Kind != "group" || parentPeer.ID != "parent-group" {
		t.Error("Parent peer extraction failed")
	}
}

func TestRoutePeer_UsageWithRouting(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{
			"peer_kind": "direct",
			"peer_id":   "user-123",
		},
	}

	peer := ExtractPeer(msg)
	if peer == nil {
		t.Fatal("Expected non-nil peer")
	}

	_ = routing.RoutePeer{
		Kind: peer.Kind,
		ID:   peer.ID,
	}
}
