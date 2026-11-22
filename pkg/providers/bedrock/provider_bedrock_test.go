//go:build bedrock

package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

func TestConvertMessages_SystemPrompts(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
	}

	bedrockMsgs, systemPrompts := convertMessages(messages)

	assert.Len(t, systemPrompts, 1)
	assert.Len(t, bedrockMsgs, 1)

	textBlock, ok := systemPrompts[0].(*types.SystemContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "You are a helpful assistant.", textBlock.Value)

	assert.Equal(t, types.ConversationRoleUser, bedrockMsgs[0].Role)
}

func TestConvertMessages_UserMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What is 2+2?"},
	}

	bedrockMsgs, systemPrompts := convertMessages(messages)

	assert.Empty(t, systemPrompts)
	assert.Len(t, bedrockMsgs, 1)
	assert.Equal(t, types.ConversationRoleUser, bedrockMsgs[0].Role)

	textBlock, ok := bedrockMsgs[0].Content[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "What is 2+2?", textBlock.Value)
}

func TestConvertMessages_AssistantMessage(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: "The answer is 4."},
	}

	bedrockMsgs, _ := convertMessages(messages)

	assert.Len(t, bedrockMsgs, 1)
	assert.Equal(t, types.ConversationRoleAssistant, bedrockMsgs[0].Role)

	textBlock, ok := bedrockMsgs[0].Content[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "The answer is 4.", textBlock.Value)
}

func TestConvertMessages_ToolResult(t *testing.T) {
	messages := []Message{
		{Role: "tool", Content: "Result from tool", ToolCallID: "call_123"},
	}

	bedrockMsgs, _ := convertMessages(messages)

	assert.Len(t, bedrockMsgs, 1)
	assert.Equal(t, types.ConversationRoleUser, bedrockMsgs[0].Role)

	toolResult, ok := bedrockMsgs[0].Content[0].(*types.ContentBlockMemberToolResult)
	require.True(t, ok)
	assert.Equal(t, "call_123", aws.ToString(toolResult.Value.ToolUseId))
}

func TestConvertMessages_MultipleToolResultsMerged(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What's the weather in NYC and LA?"},
		{
			Role:    "assistant",
			Content: "Let me check both cities.",
			ToolCalls: []protocoltypes.ToolCall{
				{ID: "call_nyc", Name: "get_weather", Arguments: map[string]any{"city": "NYC"}},
				{ID: "call_la", Name: "get_weather", Arguments: map[string]any{"city": "LA"}},
			},
		},
		{Role: "tool", Content: "NYC: 72°F, sunny", ToolCallID: "call_nyc"},
		{Role: "tool", Content: "LA: 85°F, clear", ToolCallID: "call_la"},
	}

	bedrockMsgs, _ := convertMessages(messages)

	assert.Len(t, bedrockMsgs, 3)

	assert.Equal(t, types.ConversationRoleUser, bedrockMsgs[0].Role)

	assert.Equal(t, types.ConversationRoleAssistant, bedrockMsgs[1].Role)

	assert.Equal(t, types.ConversationRoleUser, bedrockMsgs[2].Role)
	assert.Len(t, bedrockMsgs[2].Content, 2)

	result1, ok := bedrockMsgs[2].Content[0].(*types.ContentBlockMemberToolResult)
	require.True(t, ok)
	assert.Equal(t, "call_nyc", aws.ToString(result1.Value.ToolUseId))

	result2, ok := bedrockMsgs[2].Content[1].(*types.ContentBlockMemberToolResult)
	require.True(t, ok)
	assert.Equal(t, "call_la", aws.ToString(result2.Value.ToolUseId))
}

func TestConvertMessages_AssistantWithToolCalls(t *testing.T) {
	messages := []Message{
		{
			Role:    "assistant",
			Content: "Let me calculate that.",
			ToolCalls: []protocoltypes.ToolCall{
				{
					ID:        "call_456",
					Name:      "calculator",
					Arguments: map[string]any{"expression": "2+2"},
				},
			},
		},
	}

	bedrockMsgs, _ := convertMessages(messages)

	assert.Len(t, bedrockMsgs, 1)
	assert.Len(t, bedrockMsgs[0].Content, 2)

	textBlock, ok := bedrockMsgs[0].Content[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Let me calculate that.", textBlock.Value)

	toolUse, ok := bedrockMsgs[0].Content[1].(*types.ContentBlockMemberToolUse)
	require.True(t, ok)
	assert.Equal(t, "call_456", aws.ToString(toolUse.Value.ToolUseId))
	assert.Equal(t, "calculator", aws.ToString(toolUse.Value.Name))
}

func TestConvertTools_Basic(t *testing.T) {
	tools := []ToolDefinition{
		{
			Function: protocoltypes.ToolFunctionDefinition{
				Name:        "get_weather",
				Description: "Get the current weather",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	toolConfig := convertTools(tools)

	assert.NotNil(t, toolConfig)
	assert.Len(t, toolConfig.Tools, 1)

	toolSpec, ok := toolConfig.Tools[0].(*types.ToolMemberToolSpec)
	require.True(t, ok)
	assert.Equal(t, "get_weather", aws.ToString(toolSpec.Value.Name))
	assert.Equal(t, "Get the current weather", aws.ToString(toolSpec.Value.Description))
}

func TestConvertTools_SkipsEmptyName(t *testing.T) {
	tools := []ToolDefinition{
		{
			Function: protocoltypes.ToolFunctionDefinition{
				Name:        "",
				Description: "Empty name tool",
			},
		},
		{
			Function: protocoltypes.ToolFunctionDefinition{
				Name:        "   ",
				Description: "Whitespace name tool",
			},
		},
		{
			Function: protocoltypes.ToolFunctionDefinition{
				Name:        "valid_tool",
				Description: "Valid tool",
			},
		},
	}

	toolConfig := convertTools(tools)

	assert.Len(t, toolConfig.Tools, 1)
	toolSpec := toolConfig.Tools[0].(*types.ToolMemberToolSpec)
	assert.Equal(t, "valid_tool", aws.ToString(toolSpec.Value.Name))
}

func TestConvertTools_NilParameters(t *testing.T) {
	tools := []ToolDefinition{
		{
			Function: protocoltypes.ToolFunctionDefinition{
				Name:        "simple_tool",
				Description: "A tool with no parameters",
				Parameters:  nil,
			},
		},
	}

	toolConfig := convertTools(tools)

	assert.Len(t, toolConfig.Tools, 1)
}

func TestBuildUserContent_TextOnly(t *testing.T) {
	msg := Message{Content: "Hello world"}

	content := buildUserContent(msg)

	assert.Len(t, content, 1)
	textBlock, ok := content[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Hello world", textBlock.Value)
}

func TestBuildUserContent_WithImage(t *testing.T) {
	b64Data := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII="

	msg := Message{
		Content: "Look at this image",
		Media:   []string{"data:image/png;base64," + b64Data},
	}

	content := buildUserContent(msg)

	assert.Len(t, content, 2)

	textBlock, ok := content[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Look at this image", textBlock.Value)

	imageBlock, ok := content[1].(*types.ContentBlockMemberImage)
	require.True(t, ok)
	assert.Equal(t, types.ImageFormatPng, imageBlock.Value.Format)
}

func TestBuildUserContent_SkipsInvalidBase64(t *testing.T) {
	msg := Message{
		Content: "Invalid image",
		Media:   []string{"data:image/png;base64,not-valid-base64!!!"},
	}

	content := buildUserContent(msg)

	assert.Len(t, content, 1)
}

func TestBuildUserContent_SkipsNonBase64Data(t *testing.T) {
	msg := Message{
		Content: "Non-base64 image",
		Media:   []string{"data:image/png,raw-data-here"},
	}

	content := buildUserContent(msg)

	assert.Len(t, content, 1)
}

func TestBuildUserContent_SkipsVideoParts(t *testing.T) {
	msg := Message{
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "Watch this"},
			{Type: "video_url", VideoURL: &protocoltypes.VideoURL{URL: "https://example.com/clip.mp4", FPS: 1}},
		},
	}

	content := buildUserContent(msg)

	assert.Len(t, content, 1)
	textBlock, ok := content[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Watch this", textBlock.Value)
}

func TestBuildAssistantContent_SkipsEmptyToolName(t *testing.T) {
	msg := Message{
		Content: "Response",
		ToolCalls: []protocoltypes.ToolCall{
			{ID: "1", Name: "", Arguments: map[string]any{}},
			{ID: "2", Name: "   ", Arguments: map[string]any{}},
			{ID: "3", Name: "valid", Arguments: map[string]any{}},
		},
	}

	content := buildAssistantContent(msg)

	assert.Len(t, content, 2)
}

func TestBuildAssistantContent_NilArguments(t *testing.T) {
	msg := Message{
		ToolCalls: []protocoltypes.ToolCall{
			{ID: "1", Name: "tool", Arguments: nil},
		},
	}

	content := buildAssistantContent(msg)

	assert.Len(t, content, 1)
	toolUse, ok := content[0].(*types.ContentBlockMemberToolUse)
	require.True(t, ok)
	assert.NotNil(t, toolUse.Value.Input)
}

func TestBuildAssistantContent_FunctionFallback(t *testing.T) {
	msg := Message{
		ToolCalls: []protocoltypes.ToolCall{
			{
				ID:   "1",
				Name: "",
				Function: &protocoltypes.FunctionCall{
					Name:      "fallback_tool",
					Arguments: `{"key":"value"}`,
				},
			},
		},
	}

	content := buildAssistantContent(msg)

	assert.Len(t, content, 1)
	toolUse, ok := content[0].(*types.ContentBlockMemberToolUse)
	require.True(t, ok)
	assert.Equal(t, "fallback_tool", aws.ToString(toolUse.Value.Name))
}

func TestParseResponse_TextOnly(t *testing.T) {
	output := &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: "Hello!"},
				},
			},
		},
		StopReason: types.StopReasonEndTurn,
		Usage: &types.TokenUsage{
			InputTokens:  aws.Int32(10),
			OutputTokens: aws.Int32(5),
		},
	}

	resp, err := parseResponse(output)

	require.NoError(t, err)
	assert.Equal(t, "Hello!", resp.Content)
	assert.Equal(t, "stop", resp.FinishReason)
	assert.Empty(t, resp.ToolCalls)
	assert.Equal(t, 10, resp.Usage.PromptTokens)
	assert.Equal(t, 5, resp.Usage.CompletionTokens)
}

func TestParseResponse_StopReasons(t *testing.T) {
	tests := []struct {
		stopReason     types.StopReason
		expectedFinish string
	}{
		{types.StopReasonEndTurn, "stop"},
		{types.StopReasonToolUse, "tool_calls"},
		{types.StopReasonMaxTokens, "length"},
		{types.StopReasonStopSequence, "stop"},
		{types.StopReasonContentFiltered, "content_filter"},
	}

	for _, tt := range tests {
		t.Run(string(tt.stopReason), func(t *testing.T) {
			output := &bedrockruntime.ConverseOutput{
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: "test"},
						},
					},
				},
				StopReason: tt.stopReason,
			}

			resp, err := parseResponse(output)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedFinish, resp.FinishReason)
		})
	}
}

func TestParseResponse_WithToolCalls(t *testing.T) {
	toolInput := document.NewLazyDocument(map[string]any{
		"location": "San Francisco",
		"unit":     "celsius",
	})

	output := &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: "Let me check the weather."},
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("call_weather_123"),
							Name:      aws.String("get_weather"),
							Input:     toolInput,
						},
					},
				},
			},
		},
		StopReason: types.StopReasonToolUse,
		Usage: &types.TokenUsage{
			InputTokens:  aws.Int32(20),
			OutputTokens: aws.Int32(15),
		},
	}

	resp, err := parseResponse(output)

	require.NoError(t, err)
	assert.Equal(t, "Let me check the weather.", resp.Content)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	assert.Len(t, resp.ToolCalls, 1)

	tc := resp.ToolCalls[0]
	assert.Equal(t, "call_weather_123", tc.ID)
	assert.Equal(t, "get_weather", tc.Name)

	require.NotNil(t, tc.Function)
	assert.Equal(t, "get_weather", tc.Function.Name)

	assert.NotNil(t, tc.Arguments)

	assert.Equal(t, 20, resp.Usage.PromptTokens)
	assert.Equal(t, 15, resp.Usage.CompletionTokens)
	assert.Equal(t, 35, resp.Usage.TotalTokens)
}

func TestParseResponse_MultipleToolCalls(t *testing.T) {
	output := &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("call_1"),
							Name:      aws.String("tool_a"),
							Input:     document.NewLazyDocument(map[string]any{"arg": "value1"}),
						},
					},
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("call_2"),
							Name:      aws.String("tool_b"),
							Input:     document.NewLazyDocument(map[string]any{"arg": "value2"}),
						},
					},
				},
			},
		},
		StopReason: types.StopReasonToolUse,
	}

	resp, err := parseResponse(output)

	require.NoError(t, err)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	assert.Len(t, resp.ToolCalls, 2)

	assert.Equal(t, "call_1", resp.ToolCalls[0].ID)
	assert.Equal(t, "tool_a", resp.ToolCalls[0].Name)
	assert.NotNil(t, resp.ToolCalls[0].Arguments)
	assert.NotNil(t, resp.ToolCalls[0].Function)
	assert.Equal(t, "tool_a", resp.ToolCalls[0].Function.Name)

	assert.Equal(t, "call_2", resp.ToolCalls[1].ID)
	assert.Equal(t, "tool_b", resp.ToolCalls[1].Name)
	assert.NotNil(t, resp.ToolCalls[1].Arguments)
	assert.NotNil(t, resp.ToolCalls[1].Function)
	assert.Equal(t, "tool_b", resp.ToolCalls[1].Function.Name)
}

func TestParseResponse_ToolCallWithNilInput(t *testing.T) {
	output := &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("call_nil"),
							Name:      aws.String("no_args_tool"),
							Input:     nil,
						},
					},
				},
			},
		},
		StopReason: types.StopReasonToolUse,
	}

	resp, err := parseResponse(output)

	require.NoError(t, err)
	assert.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_nil", resp.ToolCalls[0].ID)
	assert.Equal(t, "no_args_tool", resp.ToolCalls[0].Name)
	assert.NotNil(t, resp.ToolCalls[0].Arguments)
	assert.Empty(t, resp.ToolCalls[0].Arguments)
}
