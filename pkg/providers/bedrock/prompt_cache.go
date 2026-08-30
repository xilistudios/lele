//go:build bedrock

package bedrock

import (
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// bedrockCachePoint builds a Converse-API cache point block.
func bedrockCachePoint(ttl string) types.CachePointBlock {
	cp := types.CachePointBlock{Type: types.CachePointTypeDefault}
	if ttl == "1h" {
		cp.Ttl = types.CacheTTLOneHour
	} else {
		cp.Ttl = types.CacheTTLFiveMinutes
	}
	return cp
}

// applyBedrockSystemCache appends a cache point after the last text system
// block so the (static) system prompt is cached. It returns the (possibly
// reallocated) slice.
func applyBedrockSystemCache(system []types.SystemContentBlock, ttl string) []types.SystemContentBlock {
	for i := len(system) - 1; i >= 0; i-- {
		if _, ok := system[i].(*types.SystemContentBlockMemberText); ok {
			return append(system, &types.SystemContentBlockMemberCachePoint{
				Value: bedrockCachePoint(ttl),
			})
		}
	}
	return system
}

// applyBedrockToolsCache appends a cache point after the last tool spec so the
// tool definitions prefix is cached.
func applyBedrockToolsCache(tools []types.Tool, ttl string) []types.Tool {
	for i := len(tools) - 1; i >= 0; i-- {
		if _, ok := tools[i].(*types.ToolMemberToolSpec); ok {
			return append(tools, &types.ToolMemberCachePoint{
				Value: bedrockCachePoint(ttl),
			})
		}
	}
	return tools
}

// applyBedrockHistoryCache appends a cache point to the last message when it
// ends with plain text (user or assistant). Messages ending in tool_use /
// tool_result blocks are skipped: their position shifts every turn, so a
// breakpoint there would never be reused.
func applyBedrockHistoryCache(messages []types.Message, ttl string) {
	if len(messages) == 0 {
		return
	}
	last := &messages[len(messages)-1]
	if len(last.Content) == 0 {
		return
	}
	for i := len(last.Content) - 1; i >= 0; i-- {
		if _, ok := last.Content[i].(*types.ContentBlockMemberText); ok {
			last.Content = append(last.Content, &types.ContentBlockMemberCachePoint{
				Value: bedrockCachePoint(ttl),
			})
			return
		}
	}
}

// applyBedrockCacheBreakpoints places cache points after the stable request
// prefixes (system, tools, history). See protocoltypes for the option keys.
func applyBedrockCacheBreakpoints(input *bedrockruntime.ConverseInput, options map[string]any) {
	if !protocoltypes.CacheEnabled(options) || input == nil {
		return
	}
	ttl := protocoltypes.CacheTTLOptions(options)
	if len(input.System) > 0 {
		input.System = applyBedrockSystemCache(input.System, ttl)
	}
	if input.ToolConfig != nil && len(input.ToolConfig.Tools) > 0 {
		input.ToolConfig.Tools = applyBedrockToolsCache(input.ToolConfig.Tools, ttl)
	}
	if len(input.Messages) > 0 {
		applyBedrockHistoryCache(input.Messages, ttl)
	}
}
