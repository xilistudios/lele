//go:build bedrock

package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/xilistudios/lele/pkg/providers/common"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

type (
	ToolCall               = protocoltypes.ToolCall
	FunctionCall           = protocoltypes.FunctionCall
	LLMResponse            = protocoltypes.LLMResponse
	UsageInfo              = protocoltypes.UsageInfo
	Message                = protocoltypes.Message
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
)

type Provider struct {
	client         *bedrockruntime.Client
	region         string
	requestTimeout time.Duration
}

type Option func(*providerConfig)

type providerConfig struct {
	region         string
	profile        string
	baseEndpoint   string
	requestTimeout time.Duration
}

func WithRegion(region string) Option {
	return func(c *providerConfig) {
		c.region = region
	}
}

func WithProfile(profile string) Option {
	return func(c *providerConfig) {
		c.profile = profile
	}
}

func WithBaseEndpoint(endpoint string) Option {
	return func(c *providerConfig) {
		c.baseEndpoint = endpoint
	}
}

func WithRequestTimeout(timeout time.Duration) Option {
	return func(c *providerConfig) {
		c.requestTimeout = timeout
	}
}

func NewProvider(ctx context.Context, opts ...Option) (*Provider, error) {
	pc := &providerConfig{}
	for _, opt := range opts {
		opt(pc)
	}

	var configOpts []func(*config.LoadOptions) error

	if pc.region != "" {
		configOpts = append(configOpts, config.WithRegion(pc.region))
	}

	if pc.profile != "" {
		configOpts = append(configOpts, config.WithSharedConfigProfile(pc.profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, configOpts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	if cfg.Region == "" {
		return nil, fmt.Errorf(
			"AWS region not configured: set AWS_REGION, AWS_DEFAULT_REGION, or use WithRegion option",
		)
	}

	var clientOpts []func(*bedrockruntime.Options)
	if pc.baseEndpoint != "" {
		clientOpts = append(clientOpts, func(o *bedrockruntime.Options) {
			o.BaseEndpoint = aws.String(pc.baseEndpoint)
		})
	}

	client := bedrockruntime.NewFromConfig(cfg, clientOpts...)

	return &Provider{
		client:         client,
		region:         cfg.Region,
		requestTimeout: pc.requestTimeout,
	}, nil
}

func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	effectiveTimeout := p.requestTimeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = common.DefaultRequestTimeout
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, effectiveTimeout)
		defer cancel()
	}

	input := &bedrockruntime.ConverseInput{
		ModelId: aws.String(model),
	}

	bedrockMessages, systemPrompts := convertMessages(messages)
	input.Messages = bedrockMessages

	if len(systemPrompts) > 0 {
		input.System = systemPrompts
	}

	var inferenceConfig *types.InferenceConfiguration

	if maxTokens, ok := common.AsInt(options["max_tokens"]); ok && maxTokens > 0 {
		if inferenceConfig == nil {
			inferenceConfig = &types.InferenceConfiguration{}
		}
		if maxTokens > math.MaxInt32 {
			maxTokens = math.MaxInt32
		}
		inferenceConfig.MaxTokens = aws.Int32(int32(maxTokens))
	}

	if inferenceConfig != nil {
		input.InferenceConfig = inferenceConfig
	}

	if len(tools) > 0 {
		toolConfig := convertTools(tools)
		if len(toolConfig.Tools) > 0 {
			input.ToolConfig = toolConfig
		}
	}

	applyBedrockCacheBreakpoints(input, options)

	output, err := p.client.Converse(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock converse: %w", err)
	}

	return parseResponse(output)
}

func (p *Provider) GetDefaultModel() string {
	return ""
}

func (p *Provider) Region() string {
	return p.region
}

func convertMessages(messages []Message) ([]types.Message, []types.SystemContentBlock) {
	var bedrockMessages []types.Message
	var systemPrompts []types.SystemContentBlock

	isToolResult := func(msg Message) bool {
		return (msg.Role == "tool" || (msg.Role == "user" && msg.ToolCallID != "")) && msg.ToolCallID != ""
	}

	makeToolResultBlock := func(msg Message) types.ContentBlock {
		return &types.ContentBlockMemberToolResult{
			Value: types.ToolResultBlock{
				ToolUseId: aws.String(msg.ToolCallID),
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{
						Value: msg.Content,
					},
				},
			},
		}
	}

	i := 0
	for i < len(messages) {
		msg := messages[i]

		switch {
		case msg.Role == "system":
			systemPrompts = append(systemPrompts, &types.SystemContentBlockMemberText{
				Value: msg.Content,
			})
			i++

		case isToolResult(msg):
			var toolResultBlocks []types.ContentBlock
			for i < len(messages) && isToolResult(messages[i]) {
				toolResultBlocks = append(toolResultBlocks, makeToolResultBlock(messages[i]))
				i++
			}
			bedrockMessages = append(bedrockMessages, types.Message{
				Role:    types.ConversationRoleUser,
				Content: toolResultBlocks,
			})

		case msg.Role == "user":
			content := buildUserContent(msg)
			bedrockMessages = append(bedrockMessages, types.Message{
				Role:    types.ConversationRoleUser,
				Content: content,
			})
			i++

		case msg.Role == "assistant":
			content := buildAssistantContent(msg)
			bedrockMessages = append(bedrockMessages, types.Message{
				Role:    types.ConversationRoleAssistant,
				Content: content,
			})
			i++

		case msg.Role == "tool" && msg.ToolCallID == "":
			content := buildUserContent(msg)
			bedrockMessages = append(bedrockMessages, types.Message{
				Role:    types.ConversationRoleUser,
				Content: content,
			})
			i++

		default:
			i++
		}
	}

	return bedrockMessages, systemPrompts
}

func buildUserContent(msg Message) []types.ContentBlock {
	var content []types.ContentBlock

	if len(msg.ContentParts) > 0 {
		for _, part := range msg.ContentParts {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					content = append(content, &types.ContentBlockMemberText{
						Value: part.Text,
					})
				}
			case "image_url":
				if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
					continue
				}
				blocks := dataURLToBedrockImages(part.ImageURL.URL)
				content = append(content, blocks...)
			case "video_url":
				continue
			}
		}

		if len(content) == 0 {
			content = append(content, &types.ContentBlockMemberText{Value: ""})
		}
		return content
	}

	if msg.Content != "" {
		content = append(content, &types.ContentBlockMemberText{
			Value: msg.Content,
		})
	}

	for _, mediaURL := range msg.Media {
		if strings.HasPrefix(mediaURL, "data:image/") {
			blocks := dataURLToBedrockImages(mediaURL)
			content = append(content, blocks...)
		}
	}

	if len(content) == 0 {
		content = append(content, &types.ContentBlockMemberText{Value: ""})
	}

	return content
}

func dataURLToBedrockImages(dataURL string) []types.ContentBlock {
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return nil
	}

	mediaType := ""
	header := parts[0]
	if idx := strings.Index(header, "/"); idx != -1 {
		end := strings.Index(header[idx:], ";")
		if end == -1 {
			end = len(header) - idx
		}
		mediaType = header[idx+1 : idx+end]
	}

	if !strings.Contains(header, ";base64") {
		return nil
	}

	var format types.ImageFormat
	switch mediaType {
	case "jpeg", "jpg":
		format = types.ImageFormatJpeg
	case "png":
		format = types.ImageFormatPng
	case "gif":
		format = types.ImageFormatGif
	case "webp":
		format = types.ImageFormatWebp
	default:
		return nil
	}

	const maxImageSize = 10 * 1024 * 1024
	decodedLen := base64.StdEncoding.DecodedLen(len(parts[1]))
	if decodedLen > maxImageSize {
		log.Printf("bedrock: skipping image exceeding size limit (%d bytes > %d)", decodedLen, maxImageSize)
		return nil
	}

	imageData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		log.Printf("bedrock: failed to decode base64 image data: %v", err)
		return nil
	}

	return []types.ContentBlock{
		&types.ContentBlockMemberImage{
			Value: types.ImageBlock{
				Format: format,
				Source: &types.ImageSourceMemberBytes{
					Value: imageData,
				},
			},
		},
	}
}

func buildAssistantContent(msg Message) []types.ContentBlock {
	var content []types.ContentBlock

	if msg.Content != "" {
		content = append(content, &types.ContentBlockMemberText{
			Value: msg.Content,
		})
	}

	for _, tc := range msg.ToolCalls {
		if strings.TrimSpace(tc.ID) == "" {
			log.Printf("bedrock: skipping tool call with empty ID (name: %q)", tc.Name)
			continue
		}

		toolName := tc.Name
		if toolName == "" && tc.Function != nil {
			toolName = tc.Function.Name
		}
		if strings.TrimSpace(toolName) == "" {
			continue
		}

		args := tc.Arguments
		if args == nil && tc.Function != nil && tc.Function.Arguments != "" {
			args = common.DecodeToolCallArguments(json.RawMessage(tc.Function.Arguments), toolName)
		}
		if args == nil {
			args = map[string]any{}
		}

		inputDoc := document.NewLazyDocument(args)

		content = append(content, &types.ContentBlockMemberToolUse{
			Value: types.ToolUseBlock{
				ToolUseId: aws.String(tc.ID),
				Name:      aws.String(toolName),
				Input:     inputDoc,
			},
		})
	}

	if len(content) == 0 {
		content = append(content, &types.ContentBlockMemberText{Value: ""})
	}

	return content
}

func convertTools(tools []ToolDefinition) *types.ToolConfiguration {
	bedrockTools := make([]types.Tool, 0, len(tools))

	for _, tool := range tools {
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}

		params := tool.Function.Parameters
		if params == nil {
			params = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}

		inputSchema := document.NewLazyDocument(params)

		bedrockTools = append(bedrockTools, &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(tool.Function.Name),
				Description: aws.String(tool.Function.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: inputSchema,
				},
			},
		})
	}

	return &types.ToolConfiguration{
		Tools: bedrockTools,
	}
}

func parseResponse(output *bedrockruntime.ConverseOutput) (*LLMResponse, error) {
	var content strings.Builder
	toolCalls := make([]ToolCall, 0)

	if output.Output != nil {
		if msgOutput, ok := output.Output.(*types.ConverseOutputMemberMessage); ok {
			for _, block := range msgOutput.Value.Content {
				switch b := block.(type) {
				case *types.ContentBlockMemberText:
					content.WriteString(b.Value)

				case *types.ContentBlockMemberToolUse:
					args := make(map[string]any)
					if b.Value.Input != nil {
						if err := b.Value.Input.UnmarshalSmithyDocument(&args); err != nil {
							log.Printf("bedrock: failed to unmarshal tool input for tool %q (id %q): %v",
								aws.ToString(b.Value.Name),
								aws.ToString(b.Value.ToolUseId),
								err,
							)
							args = make(map[string]any)
						}
					}

					argsJSON, err := json.Marshal(args)
					if err != nil {
						log.Printf("bedrock: failed to marshal tool arguments for tool %q (id %q): %v",
							aws.ToString(b.Value.Name),
							aws.ToString(b.Value.ToolUseId),
							err,
						)
						argsJSON = []byte("{}")
					}

					toolCalls = append(toolCalls, ToolCall{
						ID:        aws.ToString(b.Value.ToolUseId),
						Name:      aws.ToString(b.Value.Name),
						Arguments: args,
						Function: &FunctionCall{
							Name:      aws.ToString(b.Value.Name),
							Arguments: string(argsJSON),
						},
					})
				}
			}
		}
	}

	finishReason := "stop"
	switch output.StopReason {
	case types.StopReasonToolUse:
		finishReason = "tool_calls"
	case types.StopReasonMaxTokens:
		finishReason = "length"
	case types.StopReasonEndTurn:
		finishReason = "stop"
	case types.StopReasonStopSequence:
		finishReason = "stop"
	case types.StopReasonContentFiltered:
		finishReason = "content_filter"
	}

	var usage *UsageInfo
	if output.Usage != nil {
		cacheRead := int(aws.ToInt32(output.Usage.CacheReadInputTokens))
		cacheWrite := int(aws.ToInt32(output.Usage.CacheWriteInputTokens))
		prompt := int(aws.ToInt32(output.Usage.InputTokens)) + cacheRead + cacheWrite
		completion := int(aws.ToInt32(output.Usage.OutputTokens))
		usage = &UsageInfo{
			PromptTokens:             prompt,
			CompletionTokens:         completion,
			TotalTokens:              prompt + completion,
			CacheReadInputTokens:     cacheRead,
			CacheCreationInputTokens: cacheWrite,
		}
	}

	return &LLMResponse{
		Content:      content.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}
