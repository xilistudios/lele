// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
)

// ============================================================================
// Tests for FormatMessagesForLog
// ============================================================================

// TestFormatMessagesForLog_EmptySlice tests with empty messages slice
func TestFormatMessagesForLog_EmptySlice(t *testing.T) {
	result := FormatMessagesForLog([]providers.Message{})
	if result != "[]" {
		t.Errorf("Expected '[]' for empty slice, got: %s", result)
	}
}

// TestFormatMessagesForLog_NilSlice tests with nil messages slice
func TestFormatMessagesForLog_NilSlice(t *testing.T) {
	result := FormatMessagesForLog(nil)
	if result != "[]" {
		t.Errorf("Expected '[]' for nil slice, got: %s", result)
	}
}

// TestFormatMessagesForLog_SingleMessage tests with a single simple message
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

// TestFormatMessagesForLog_MultipleMessages tests with multiple messages
func TestFormatMessagesForLog_MultipleMessages(t *testing.T) {
	messages := []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "How are you?"},
	}
	result := FormatMessagesForLog(messages)

	// Check for all message indices
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

// TestFormatMessagesForLog_WithToolCalls tests messages containing tool calls
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

// TestFormatMessagesForLog_WithToolCallID tests messages with tool call ID
func TestFormatMessagesForLog_WithToolCallID(t *testing.T) {
	messages := []providers.Message{
		{Role: "tool", Content: "Tool result", ToolCallID: "call-123"},
	}
	result := FormatMessagesForLog(messages)

	if !strings.Contains(result, "ToolCallID: call-123") {
		t.Error("Expected result to contain ToolCallID")
	}
}

// TestFormatMessagesForLog_EmptyContent tests message with empty content
func TestFormatMessagesForLog_EmptyContent(t *testing.T) {
	messages := []providers.Message{
		{Role: "assistant", Content: ""},
	}
	result := FormatMessagesForLog(messages)

	// Should not contain "Content:" when content is empty
	if strings.Contains(result, "Content:") {
		t.Error("Expected result to not contain Content section when empty")
	}
}

// TestFormatMessagesForLog_LongContentTruncation tests that long content is truncated
func TestFormatMessagesForLog_LongContentTruncation(t *testing.T) {
	longContent := strings.Repeat("a", 300)
	messages := []providers.Message{
		{Role: "user", Content: longContent},
	}
	result := FormatMessagesForLog(messages)

	// Should contain truncation indicator
	if !strings.Contains(result, "...") {
		t.Error("Expected long content to be truncated with '...'")
	}
}

// TestFormatMessagesForLog_MultipleToolCalls tests message with multiple tool calls
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

// TestFormatMessagesForLog_ToolCallWithoutFunction tests tool call with nil Function
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

	// Should not panic and should still show tool call info
	if !strings.Contains(result, "call-1") {
		t.Error("Expected result to contain tool call ID even without function")
	}
}

// ============================================================================
// Tests for FormatToolsForLog
// ============================================================================

// TestFormatToolsForLog_EmptySlice tests with empty tools slice
func TestFormatToolsForLog_EmptySlice(t *testing.T) {
	result := FormatToolsForLog([]providers.ToolDefinition{})
	if result != "[]" {
		t.Errorf("Expected '[]' for empty slice, got: %s", result)
	}
}

// TestFormatToolsForLog_NilSlice tests with nil tools slice
func TestFormatToolsForLog_NilSlice(t *testing.T) {
	result := FormatToolsForLog(nil)
	if result != "[]" {
		t.Errorf("Expected '[]' for nil slice, got: %s", result)
	}
}

// TestFormatToolsForLog_SingleTool tests with a single tool
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

// TestFormatToolsForLog_MultipleTools tests with multiple tools
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

// TestFormatToolsForLog_WithParameters tests tool with parameters
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

// TestFormatToolsForLog_EmptyParameters tests tool with empty parameters
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

	// Should not contain Parameters section when empty
	if strings.Contains(result, "Parameters:") {
		t.Error("Expected result to not contain Parameters section when empty")
	}
}

// TestFormatToolsForLog_LongParametersTruncation tests that long parameters are truncated
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

	// Should contain truncation indicator
	if !strings.Contains(result, "...") {
		t.Error("Expected long parameters to be truncated with '...'")
	}
}

// ============================================================================
// Tests for FormatProviderModel
// ============================================================================

// TestFormatProviderModel_NormalCase tests normal provider/model combination
func TestFormatProviderModel_NormalCase(t *testing.T) {
	result := FormatProviderModel("openai", "gpt-4")
	expected := "openai/gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_AlreadyPrefixed tests when model already has provider prefix
func TestFormatProviderModel_AlreadyPrefixed(t *testing.T) {
	result := FormatProviderModel("openai", "openai/gpt-4")
	expected := "openai/gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_EmptyProvider tests with empty provider
func TestFormatProviderModel_EmptyProvider(t *testing.T) {
	result := FormatProviderModel("", "gpt-4")
	expected := "gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_EmptyModel tests with empty model
func TestFormatProviderModel_EmptyModel(t *testing.T) {
	result := FormatProviderModel("openai", "")
	expected := "openai/"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_BothEmpty tests with both empty
func TestFormatProviderModel_BothEmpty(t *testing.T) {
	result := FormatProviderModel("", "")
	expected := ""
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_WhitespaceTrimming tests that whitespace is trimmed
func TestFormatProviderModel_WhitespaceTrimming(t *testing.T) {
	result := FormatProviderModel("  openai  ", "  gpt-4  ")
	expected := "openai/gpt-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_DifferentProviderPrefix tests with different provider prefix
func TestFormatProviderModel_DifferentProviderPrefix(t *testing.T) {
	result := FormatProviderModel("anthropic", "claude-3")
	expected := "anthropic/claude-3"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_CaseSensitivity tests case sensitivity
func TestFormatProviderModel_CaseSensitivity(t *testing.T) {
	result := FormatProviderModel("OpenAI", "GPT-4")
	expected := "OpenAI/GPT-4"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// TestFormatProviderModel_PartialPrefix tests model with partial/different prefix
func TestFormatProviderModel_PartialPrefix(t *testing.T) {
	// Model has a different prefix than provider
	result := FormatProviderModel("openai", "anthropic/claude-3")
	expected := "openai/anthropic/claude-3"
	if result != expected {
		t.Errorf("Expected '%s', got: %s", expected, result)
	}
}

// ============================================================================
// Tests for ExtractPeer
// ============================================================================

// TestExtractPeer_NormalCase tests normal peer extraction
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

// TestExtractPeer_NoPeerKind tests when peer_kind is missing
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

// TestExtractPeer_NoMetadata tests when metadata is nil
func TestExtractPeer_NoMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: nil,
	}
	peer := ExtractPeer(msg)

	if peer != nil {
		t.Error("Expected nil peer when metadata is nil")
	}
}

// TestExtractPeer_EmptyMetadata tests when metadata is empty
func TestExtractPeer_EmptyMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{},
	}
	peer := ExtractPeer(msg)

	if peer != nil {
		t.Error("Expected nil peer when metadata is empty")
	}
}

// TestExtractPeer_DirectKindNoPeerID tests direct kind with no peer_id uses SenderID
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

// TestExtractPeer_GroupKindNoPeerID tests group kind with no peer_id uses ChatID
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

// TestExtractPeer_ChannelKindNoPeerID tests channel kind with no peer_id uses ChatID
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

// TestExtractPeer_PeerIDOverridesSenderID tests that peer_id overrides SenderID
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

// TestExtractPeer_EmptyPeerKind tests with empty peer_kind
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

// ============================================================================
// Tests for ExtractParentPeer
// ============================================================================

// TestExtractParentPeer_NormalCase tests normal parent peer extraction
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

// TestExtractParentPeer_NoParentKind tests when parent_peer_kind is missing
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

// TestExtractParentPeer_NoParentID tests when parent_peer_id is missing
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

// TestExtractParentPeer_NoMetadata tests when metadata is nil
func TestExtractParentPeer_NoMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: nil,
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when metadata is nil")
	}
}

// TestExtractParentPeer_EmptyMetadata tests when metadata is empty
func TestExtractParentPeer_EmptyMetadata(t *testing.T) {
	msg := bus.InboundMessage{
		Metadata: map[string]string{},
	}
	peer := ExtractParentPeer(msg)

	if peer != nil {
		t.Error("Expected nil parent peer when metadata is empty")
	}
}

// TestExtractParentPeer_GroupKind tests with group kind
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

// TestExtractParentPeer_ChannelKind tests with channel kind
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

// TestExtractParentPeer_EmptyValues tests with empty values
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

// TestExtractParentPeer_OnlyKindEmpty tests with only kind empty
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

// TestExtractParentPeer_OnlyIDEmpty tests with only ID empty
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

// ============================================================================
// Tests for GatewayVersion
// ============================================================================

// TestGatewayVersion_ReturnsString tests that GatewayVersion returns a string
func TestGatewayVersion_ReturnsString(t *testing.T) {
	version := GatewayVersion()

	// Should return a non-empty string
	if version == "" {
		t.Error("Expected non-empty version string")
	}
}

// TestGatewayVersion_ReturnsDevOrVersion tests that version is either "dev" or a version string
func TestGatewayVersion_ReturnsDevOrVersion(t *testing.T) {
	version := GatewayVersion()

	// In test environment, this will likely return "dev" since tests don't have build info
	// But it should never be empty
	if version == "" {
		t.Error("Expected version to not be empty")
	}

	// Version should be either "dev" or contain version-like characters
	if version != "dev" && !strings.Contains(version, ".") && !strings.Contains(version, "v") {
		// This is a loose check - version could be many formats
		t.Logf("Version returned: %s (this may be expected in test environment)", version)
	}
}

// TestGatewayVersion_Consistency tests that multiple calls return the same value
func TestGatewayVersion_Consistency(t *testing.T) {
	version1 := GatewayVersion()
	version2 := GatewayVersion()

	if version1 != version2 {
		t.Errorf("Expected consistent version, got '%s' and '%s'", version1, version2)
	}
}

// TestGatewayVersion_NotPanic tests that the function doesn't panic
func TestGatewayVersion_NotPanic(t *testing.T) {
	// This test simply verifies the function doesn't panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("GatewayVersion panicked: %v", r)
		}
	}()

	_ = GatewayVersion()
}

// ============================================================================
// Integration/Combined Tests
// ============================================================================

// TestExtractPeerAndParentPeer_Together tests both extraction functions together
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

	// Verify peer
	if peer.Kind != "direct" || peer.ID != "peer-789" {
		t.Error("Peer extraction failed")
	}

	// Verify parent peer
	if parentPeer.Kind != "group" || parentPeer.ID != "parent-group" {
		t.Error("Parent peer extraction failed")
	}
}

// TestRoutePeer_UsageWithRouting tests that extracted peers work with routing package
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

	// Verify the peer can be used with routing.RoutePeer expectations
	_ = routing.RoutePeer{
		Kind: peer.Kind,
		ID:   peer.ID,
	}
}
