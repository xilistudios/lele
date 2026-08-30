//go:build bedrock

package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestApplyBedrockCacheBreakpoints(t *testing.T) {
	input := &bedrockruntime.ConverseInput{
		System: []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: "static prompt"},
		},
		Messages: []types.Message{
			{Role: types.ConversationRoleUser, Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: "hello"},
			}},
		},
		ToolConfig: &types.ToolConfiguration{
			Tools: []types.Tool{
				&types.ToolMemberToolSpec{Value: types.ToolSpecification{Name: strPtr("alpha")}},
				&types.ToolMemberToolSpec{Value: types.ToolSpecification{Name: strPtr("zeta")}},
			},
		},
	}

	applyBedrockCacheBreakpoints(input, map[string]any{
		"prompt_cache":     true,
		"prompt_cache_ttl": "1h",
	})

	// System cache point appended.
	if len(input.System) != 2 {
		t.Fatalf("system blocks = %d, want 2", len(input.System))
	}
	cp, ok := input.System[1].(*types.SystemContentBlockMemberCachePoint)
	if !ok {
		t.Fatalf("system[1] = %T, want cache point", input.System[1])
	}
	if cp.Value.Ttl != types.CacheTTLOneHour {
		t.Errorf("system cache TTL = %q, want 1h", cp.Value.Ttl)
	}

	// Tools: cache point after the last spec.
	if len(input.ToolConfig.Tools) != 3 {
		t.Fatalf("tools = %d, want 3 (2 specs + cache point)", len(input.ToolConfig.Tools))
	}
	tp, ok := input.ToolConfig.Tools[2].(*types.ToolMemberCachePoint)
	if !ok {
		t.Fatalf("tools[2] = %T, want cache point", input.ToolConfig.Tools[2])
	}
	if tp.Value.Ttl != types.CacheTTLOneHour {
		t.Errorf("tools cache TTL = %q, want 1h", tp.Value.Ttl)
	}

	// History: cache point on the last message.
	last := input.Messages[0]
	if len(last.Content) != 2 {
		t.Fatalf("last message blocks = %d, want 2", len(last.Content))
	}
	if _, ok := last.Content[1].(*types.ContentBlockMemberCachePoint); !ok {
		t.Errorf("last block = %T, want cache point", last.Content[1])
	}
}

func TestApplyBedrockCacheBreakpoints_DisabledIsNoop(t *testing.T) {
	input := &bedrockruntime.ConverseInput{
		System: []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: "s"}},
		Messages: []types.Message{{Role: types.ConversationRoleUser, Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "hi"},
		}}},
	}
	applyBedrockCacheBreakpoints(input, map[string]any{"max_tokens": 10})
	if len(input.System) != 1 {
		t.Error("system must be untouched when caching is off")
	}
	if len(input.Messages[0].Content) != 1 {
		t.Error("messages must be untouched when caching is off")
	}
}

func TestApplyBedrockHistoryCache_SkipsToolResultTail(t *testing.T) {
	messages := []types.Message{{
		Role: types.ConversationRoleUser,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{}},
		},
	}}
	applyBedrockHistoryCache(messages, "5m")
	if len(messages[0].Content) != 1 {
		t.Error("tool_result tail must not receive a cache point")
	}
}

func TestApplyBedrockCacheBreakpoints_DefaultTTL(t *testing.T) {
	input := &bedrockruntime.ConverseInput{
		System: []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: "s"}},
	}
	applyBedrockCacheBreakpoints(input, map[string]any{"prompt_cache": true})
	cp := input.System[1].(*types.SystemContentBlockMemberCachePoint)
	if cp.Value.Ttl != types.CacheTTLFiveMinutes {
		t.Errorf("default TTL = %q, want 5m", cp.Value.Ttl)
	}
}

func strPtr(s string) *string { return &s }
