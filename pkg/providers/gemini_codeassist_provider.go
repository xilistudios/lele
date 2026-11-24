package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers/common"
)

const (
	geminiCodeAssistBaseURL = "https://cloudcode-pa.googleapis.com"

	geminiCodeAssistAPIVersion = "v1internal"

	geminiCodeAssistDefaultModel = "gemini-pro-latest"
)

type codeAssistGenerateRequest struct {
	Model        string                       `json:"model"`
	Project      string                       `json:"project"`
	UserPromptID string                       `json:"user_prompt_id"`
	Request      vertexGenerateContentRequest `json:"request"`
}

type vertexGenerateContentRequest struct {
	Contents          []codeAssistContent   `json:"contents"`
	SystemInstruction *codeAssistContent    `json:"systemInstruction,omitempty"`
	Tools             []codeAssistTool      `json:"tools,omitempty"`
	ToolConfig        *codeAssistToolConfig `json:"toolConfig,omitempty"`
	SessionID         string                `json:"session_id,omitempty"`
}

type codeAssistContent struct {
	Role  string           `json:"role"`
	Parts []codeAssistPart `json:"parts"`
}

type codeAssistPart struct {
	Text             string                      `json:"text,omitempty"`
	InlineData       *codeAssistInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *codeAssistFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *codeAssistFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                      `json:"thoughtSignature,omitempty"`
}

type codeAssistInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type codeAssistFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
	ID   string         `json:"id,omitempty"`
}

type codeAssistFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
}

type codeAssistTool struct {
	FunctionDeclarations []codeAssistFunctionDeclaration `json:"functionDeclarations"`
}

type codeAssistFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type codeAssistToolConfig struct {
	FunctionCallingConfig codeAssistFunctionCallingConfig `json:"functionCallingConfig"`
}

type codeAssistFunctionCallingConfig struct {
	Mode string `json:"mode"`
}

const codeAssistFunctionCallingModeAuto = "AUTO"

func buildCodeAssistRequest(
	messages []Message,
	tools []ToolDefinition,
	model, project, sessionID string,
	options map[string]any,
) codeAssistGenerateRequest {
	req := codeAssistGenerateRequest{
		Model:   model,
		Project: project,
		Request: vertexGenerateContentRequest{SessionID: sessionID},
	}

	toolCallNames := make(map[string]string)

	systemText := ""

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			systemText = msg.Content

		case "user":
			if msg.ToolCallID != "" {
				req.Request.Contents = append(req.Request.Contents, codeAssistFunctionResponseContent(msg, toolCallNames))
				continue
			}
			parts := codeAssistUserParts(msg)
			if len(parts) == 0 {
				continue
			}
			req.Request.Contents = append(req.Request.Contents, codeAssistContent{Role: "user", Parts: parts})

		case "assistant":
			content := codeAssistContent{Role: "model"}
			if msg.Content != "" {
				content.Parts = append(content.Parts, codeAssistPart{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				name, args, signature := normalizeStoredToolCall(tc)
				if name == "" {
					continue
				}
				if tc.ID != "" {
					toolCallNames[tc.ID] = name
				}
				content.Parts = append(content.Parts, codeAssistPart{
					ThoughtSignature: signature,
					FunctionCall: &codeAssistFunctionCall{
						Name: name,
						Args: args,
						ID:   tc.ID,
					},
				})
			}
			if len(content.Parts) > 0 {
				req.Request.Contents = append(req.Request.Contents, content)
			}

		case "tool":
			req.Request.Contents = append(req.Request.Contents, codeAssistFunctionResponseContent(msg, toolCallNames))
		}
	}

	declarations := codeAssistFunctionDeclarations(tools)
	hasTools := len(declarations) > 0
	if hasTools {
		req.Request.Tools = []codeAssistTool{{FunctionDeclarations: declarations}}
		req.Request.ToolConfig = &codeAssistToolConfig{
			FunctionCallingConfig: codeAssistFunctionCallingConfig{Mode: codeAssistFunctionCallingModeAuto},
		}
	}

	req.Request.SystemInstruction = buildCodeAssistSystemInstructionWithToolGuard(systemText, hasTools)

	return req
}

const geminiFunctionCallGuard = "\n\n## Function calling\n" +
	"- When you call a tool, emit a native function call, not code. Never write Python (or any language) that calls the tool, and never wrap a call in print(...) or a code block.\n" +
	"- Use the function name exactly as defined. Do not prepend `default_api.` or any other namespace to the function name."

func buildCodeAssistSystemInstruction(system string) *codeAssistContent {
	trimmed := strings.TrimSpace(system)
	if trimmed == "" {
		return nil
	}
	return &codeAssistContent{
		Role:  "user",
		Parts: []codeAssistPart{{Text: trimmed}},
	}
}

func buildCodeAssistSystemInstructionWithToolGuard(system string, hasTools bool) *codeAssistContent {
	if !hasTools {
		return buildCodeAssistSystemInstruction(system)
	}
	return buildCodeAssistSystemInstruction(strings.TrimSpace(system) + geminiFunctionCallGuard)
}

func codeAssistFunctionDeclarations(tools []ToolDefinition) []codeAssistFunctionDeclaration {
	var declarations []codeAssistFunctionDeclaration
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		declarations = append(declarations, codeAssistFunctionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  sanitizeSchemaForGemini(t.Function.Parameters),
		})
	}
	return declarations
}

func codeAssistFunctionResponseContent(msg Message, toolCallNames map[string]string) codeAssistContent {
	return codeAssistContent{
		Role: "user",
		Parts: []codeAssistPart{{
			FunctionResponse: &codeAssistFunctionResponse{
				Name:     resolveToolResponseName(msg.ToolCallID, toolCallNames),
				Response: map[string]any{"content": msg.Content},
				ID:       msg.ToolCallID,
			},
		}},
	}
}

func codeAssistUserParts(msg Message) []codeAssistPart {
	if len(msg.ContentParts) == 0 {
		if text := msg.TextContent(); text != "" {
			return []codeAssistPart{{Text: text}}
		}
		return nil
	}

	parts := make([]codeAssistPart, 0, len(msg.ContentParts))
	for _, cp := range msg.ContentParts {
		switch cp.Type {
		case "text":
			if strings.TrimSpace(cp.Text) != "" {
				parts = append(parts, codeAssistPart{Text: cp.Text})
			}
		case "image_url":
			if cp.ImageURL == nil {
				continue
			}
			if inline := codeAssistInlineDataFromDataURL(cp.ImageURL.URL); inline != nil {
				parts = append(parts, codeAssistPart{InlineData: inline})
			}
		}
	}
	return parts
}

func codeAssistInlineDataFromDataURL(raw string) *codeAssistInlineData {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "data:") {
		return nil
	}
	comma := strings.Index(trimmed, ",")
	if comma < 0 {
		return nil
	}
	header := trimmed[len("data:"):comma]
	if !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return nil
	}
	mediaType := strings.TrimSuffix(header, ";base64")
	payload := trimmed[comma+1:]
	if mediaType == "" || payload == "" {
		return nil
	}
	return &codeAssistInlineData{MimeType: mediaType, Data: payload}
}

type codeAssistGenerateResponse struct {
	TraceID  string                    `json:"traceId,omitempty"`
	Response *codeAssistVertexResponse `json:"response,omitempty"`
}

type codeAssistVertexResponse struct {
	Candidates    []codeAssistCandidate    `json:"candidates,omitempty"`
	UsageMetadata *codeAssistUsageMetadata `json:"usageMetadata,omitempty"`
}

type codeAssistCandidate struct {
	Content      *codeAssistContent `json:"content,omitempty"`
	FinishReason string             `json:"finishReason,omitempty"`
}

type codeAssistUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

func parseCodeAssistResponse(body []byte) (*LLMResponse, error) {
	var envelope codeAssistGenerateResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decoding code assist response: %w", err)
	}
	if envelope.Response == nil {
		return nil, fmt.Errorf("code assist response carried no %q payload", "response")
	}
	vertex := envelope.Response
	if len(vertex.Candidates) == 0 {
		return nil, fmt.Errorf("code assist response carried no candidates")
	}

	var contentParts []string
	var toolCalls []ToolCall
	var finishReason string

	for _, candidate := range vertex.Candidates {
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					contentParts = append(contentParts, part.Text)
				}
				if part.FunctionCall != nil {
					argumentsJSON, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, ToolCall{
						ID:        codeAssistToolCallID(part.FunctionCall, len(toolCalls)),
						Name:      part.FunctionCall.Name,
						Arguments: part.FunctionCall.Args,
						Function: &FunctionCall{
							Name:             part.FunctionCall.Name,
							Arguments:        string(argumentsJSON),
							ThoughtSignature: part.ThoughtSignature,
						},
					})
				}
			}
		}
		if candidate.FinishReason != "" {
			finishReason = candidate.FinishReason
		}
	}

	mappedFinish := "stop"
	if len(toolCalls) > 0 {
		mappedFinish = "tool_calls"
	}
	if finishReason == "MAX_TOKENS" {
		mappedFinish = "length"
	}

	var usage *UsageInfo
	if vertex.UsageMetadata != nil &&
		(vertex.UsageMetadata.PromptTokenCount > 0 || vertex.UsageMetadata.CandidatesTokenCount > 0) {
		usage = &UsageInfo{
			PromptTokens:     vertex.UsageMetadata.PromptTokenCount,
			CompletionTokens: vertex.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      vertex.UsageMetadata.PromptTokenCount + vertex.UsageMetadata.CandidatesTokenCount,
		}
	}

	return &LLMResponse{
		Content:      strings.Join(contentParts, ""),
		ToolCalls:    toolCalls,
		FinishReason: mappedFinish,
		Usage:        usage,
	}, nil
}

func codeAssistToolCallID(call *codeAssistFunctionCall, index int) string {
	if call.ID != "" {
		return call.ID
	}
	return fmt.Sprintf("call_%s_%d", call.Name, index)
}

var geminiOAuthConfig = auth.GeminiOAuthConfig

const (
	geminiCodeAssistHTTPTimeout = 90 * time.Second

	geminiCodeAssistXGoogAPIClient = "gl-node/26.4.0"

	geminiCodeAssistUserAgentPrefix = "GeminiCLI-tui/0.57.0"

	geminiCodeAssistLegacyTier = "legacy-tier"

	geminiCodeAssistFreeTier = "free-tier"

	geminiOnboardPollTimeout = 5 * time.Minute
)

var geminiOnboardPollInterval = 5 * time.Second

type GeminiCodeAssistProvider struct {
	token string

	tokenSource  func() (string, error)
	httpClient   *http.Client
	defaultModel string

	baseURL string
}

func NewGeminiCodeAssistProvider(token string, tokenSource func() (string, error)) *GeminiCodeAssistProvider {
	return &GeminiCodeAssistProvider{
		token:       token,
		tokenSource: tokenSource,
		httpClient:  &http.Client{Timeout: geminiCodeAssistHTTPTimeout},
	}
}

func (p *GeminiCodeAssistProvider) resolveTokenSource() func() (string, error) {
	if p.tokenSource != nil {
		return p.tokenSource
	}
	if p.token != "" {
		seed := p.token
		return func() (string, error) { return seed, nil }
	}
	return createGeminiTokenSource()
}

func (p *GeminiCodeAssistProvider) GetDefaultModel() string {
	if p.defaultModel != "" {
		return p.defaultModel
	}
	return geminiCodeAssistDefaultModel
}

func createGeminiTokenSource() func() (string, error) {
	return func() (string, error) {
		cred, err := auth.EnsureFreshCredential("gemini", geminiOAuthConfig(), auth.RefreshPolicy{RequireOAuthMethod: true})
		if err != nil {
			return "", err
		}
		if cred == nil {
			return "", fmt.Errorf("no credentials for gemini. Run: lele auth login --provider gemini")
		}
		return cred.AccessToken, nil
	}
}

func (p *GeminiCodeAssistProvider) loadCodeAssistProject(ctx context.Context, accessToken string) (string, error) {
	cred, err := auth.GetCredential("gemini")
	if err != nil {
		return "", fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred != nil && cred.ProjectID != "" {
		return cred.ProjectID, nil
	}

	envProject := geminiCloudProjectFromEnv()

	body, err := p.codeAssistPost(ctx, "loadCodeAssist", accessToken, newCodeAssistHandshakeRequest(envProject))
	if err != nil {
		return "", fmt.Errorf("Gemini Code Assist setup failed during loadCodeAssist: %w", err)
	}

	var load codeAssistHandshakeResponse
	if err := json.Unmarshal(body, &load); err != nil {
		return "", fmt.Errorf("decoding loadCodeAssist response: %w", err)
	}

	project, err := p.completeLoadCodeAssist(ctx, accessToken, load, envProject)
	if err != nil {
		return "", err
	}
	if project == "" {
		return "", codeAssistProjectError(load)
	}

	if cred != nil {
		cred.ProjectID = project
		_ = auth.SetCredential("gemini", cred)
	}
	return project, nil
}

func (p *GeminiCodeAssistProvider) completeLoadCodeAssist(
	ctx context.Context,
	accessToken string,
	load codeAssistHandshakeResponse,
	envProject string,
) (string, error) {
	if load.CurrentTier != nil {
		if load.CloudAICompanionProject != "" {
			return load.CloudAICompanionProject, nil
		}
		return envProject, nil
	}

	tier := codeAssistOnboardTier(load)
	logger.DebugCF("provider.gemini-codeassist", "Onboarding Gemini Code Assist account", map[string]any{
		"tier": tier.ID,
	})
	project, err := p.onboardUser(ctx, accessToken, tier, envProject)
	if err != nil {
		return "", err
	}
	if project != "" {
		return project, nil
	}
	return envProject, nil
}

func (p *GeminiCodeAssistProvider) onboardUser(
	ctx context.Context,
	accessToken string,
	tier codeAssistUserTier,
	envProject string,
) (string, error) {
	body, err := p.codeAssistPost(ctx, "onboardUser", accessToken, codeAssistOnboardRequest(tier, envProject))
	if err != nil {
		return "", fmt.Errorf("Gemini Code Assist onboarding failed: %w", err)
	}

	var op codeAssistOperationResponse
	if err := json.Unmarshal(body, &op); err != nil {
		return "", fmt.Errorf("decoding onboardUser response: %w", err)
	}

	deadline := time.Now().Add(geminiOnboardPollTimeout)
	for !op.Done {
		if op.Name == "" {
			return "", fmt.Errorf("Gemini onboarding returned no operation name")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(geminiOnboardPollInterval):
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("Gemini onboarding timed out after %s waiting for operation %q", geminiOnboardPollTimeout, op.Name)
		}
		polled, err := p.getCodeAssistOperation(ctx, accessToken, op.Name)
		if err != nil {
			return "", fmt.Errorf("Gemini onboarding polling failed: %w", err)
		}
		if err := json.Unmarshal(polled, &op); err != nil {
			return "", fmt.Errorf("decoding onboarding operation response: %w", err)
		}
	}

	if op.Response == nil || op.Response.CloudAICompanionProject == nil {
		return "", nil
	}
	return op.Response.CloudAICompanionProject.ID, nil
}

func geminiCloudProjectFromEnv() string {
	for _, name := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"} {
		if project := strings.TrimSpace(os.Getenv(name)); project != "" {
			return project
		}
	}
	return ""
}

func codeAssistOnboardTier(load codeAssistHandshakeResponse) codeAssistUserTier {
	for _, tier := range load.AllowedTiers {
		if tier.IsDefault {
			return tier
		}
	}
	return codeAssistUserTier{ID: geminiCodeAssistLegacyTier}
}

func codeAssistOnboardRequest(tier codeAssistUserTier, envProject string) codeAssistOnboardUserRequest {
	if tier.ID == geminiCodeAssistFreeTier {
		return codeAssistOnboardUserRequest{
			TierID:   tier.ID,
			Metadata: geminiClientMetadata(""),
		}
	}
	return codeAssistOnboardUserRequest{
		TierID:                  tier.ID,
		CloudAICompanionProject: envProject,
		Metadata:                geminiClientMetadata(envProject),
	}
}

func codeAssistProjectError(load codeAssistHandshakeResponse) error {
	var reasons []string
	for _, tier := range load.IneligibleTiers {
		if tier.ReasonMessage != "" {
			reasons = append(reasons, tier.ReasonMessage)
		}
	}
	if len(reasons) > 0 {
		return fmt.Errorf("gemini code assist is not available for this account: %s (set GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT_ID to use a specific project)", strings.Join(reasons, ", "))
	}
	return fmt.Errorf("could not determine a Google Cloud project for Gemini Code Assist: set GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT_ID (see Gemini Code Assist Workspace auth docs)")
}

type codeAssistHandshakeRequest struct {
	CloudAICompanionProject string                   `json:"cloudaicompanionProject,omitempty"`
	Metadata                codeAssistClientMetadata `json:"metadata"`
}

type codeAssistClientMetadata struct {
	IDEType     string `json:"ideType"`
	Platform    string `json:"platform"`
	PluginType  string `json:"pluginType"`
	DuetProject string `json:"duetProject,omitempty"`
}

func geminiClientMetadata(envProject string) codeAssistClientMetadata {
	return codeAssistClientMetadata{
		IDEType:     "IDE_UNSPECIFIED",
		Platform:    "PLATFORM_UNSPECIFIED",
		PluginType:  "GEMINI",
		DuetProject: envProject,
	}
}

type codeAssistOnboardUserRequest struct {
	TierID                  string                   `json:"tierId,omitempty"`
	CloudAICompanionProject string                   `json:"cloudaicompanionProject,omitempty"`
	Metadata                codeAssistClientMetadata `json:"metadata"`
}

type codeAssistHandshakeResponse struct {
	CurrentTier             *codeAssistUserTier    `json:"currentTier,omitempty"`
	AllowedTiers            []codeAssistUserTier   `json:"allowedTiers,omitempty"`
	IneligibleTiers         []codeAssistIneligible `json:"ineligibleTiers,omitempty"`
	CloudAICompanionProject string                 `json:"cloudaicompanionProject,omitempty"`
}

type codeAssistUserTier struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

type codeAssistIneligible struct {
	ReasonMessage string `json:"reasonMessage,omitempty"`
}

type codeAssistOperationResponse struct {
	Name     string                   `json:"name,omitempty"`
	Done     bool                     `json:"done,omitempty"`
	Response *codeAssistOnboardResult `json:"response,omitempty"`
}

type codeAssistOnboardResult struct {
	CloudAICompanionProject *codeAssistProjectRef `json:"cloudaicompanionProject,omitempty"`
}

type codeAssistProjectRef struct {
	ID string `json:"id,omitempty"`
}

func newCodeAssistHandshakeRequest(envProject string) codeAssistHandshakeRequest {
	return codeAssistHandshakeRequest{
		CloudAICompanionProject: envProject,
		Metadata:                geminiClientMetadata(envProject),
	}
}

func (p *GeminiCodeAssistProvider) endpoint() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return geminiCodeAssistBaseURL
}

func (p *GeminiCodeAssistProvider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return &http.Client{Timeout: geminiCodeAssistHTTPTimeout}
}

func (p *GeminiCodeAssistProvider) codeAssistPost(ctx context.Context, method, accessToken string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding gemini code assist %s request: %w", method, err)
	}
	url := fmt.Sprintf("%s/%s:%s", p.endpoint(), geminiCodeAssistAPIVersion, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("building gemini code assist %s request: %w", method, err)
	}
	return p.doCodeAssist(req, method, accessToken)
}

func (p *GeminiCodeAssistProvider) getCodeAssistOperation(ctx context.Context, accessToken, name string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/%s", p.endpoint(), geminiCodeAssistAPIVersion, strings.TrimPrefix(name, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building gemini code assist operation request: %w", err)
	}
	return p.doCodeAssist(req, "operation", accessToken)
}

func (p *GeminiCodeAssistProvider) doCodeAssist(req *http.Request, method, accessToken string) ([]byte, error) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", geminiCodeAssistUserAgent())
	req.Header.Set("X-Goog-Api-Client", geminiCodeAssistXGoogAPIClient)

	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini code assist %s request: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := common.ReadLimitedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading gemini code assist %s response: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, codeAssistHTTPError(method, resp.StatusCode, body)
	}
	return body, nil
}

func codeAssistHTTPError(method string, statusCode int, body []byte) error {
	return fmt.Errorf("gemini code assist %s failed (HTTP %d): %s", method, statusCode, truncateString(string(body), 500))
}

func geminiCodeAssistUserAgent() string {
	return fmt.Sprintf("%s/%s (%s; %s; terminal)",
		geminiCodeAssistUserAgentPrefix,
		geminiCodeAssistDefaultModel,
		geminiUserAgentOS(runtime.GOOS),
		geminiUserAgentArch(runtime.GOARCH),
	)
}

func geminiUserAgentOS(goos string) string {
	switch goos {
	case "darwin", "macos":
		return "macos"
	default:
		return goos
	}
}

func geminiUserAgentArch(goarch string) string {
	switch goarch {
	case "amd64", "x86_64":
		return "x64"
	case "arm64", "aarch64":
		return "arm64"
	case "386":
		return "x86"
	default:
		return goarch
	}
}

func (p *GeminiCodeAssistProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	tokenSource := p.resolveTokenSource()
	accessToken, err := tokenSource()
	if err != nil {
		return nil, fmt.Errorf("gemini code assist auth: %w", err)
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("gemini code assist: no access token. Run: lele auth login --provider gemini")
	}

	if model == "" {
		model = p.GetDefaultModel()
	}

	project, err := p.loadCodeAssistProject(ctx, accessToken)
	if err != nil {
		return nil, err
	}

	request := buildCodeAssistRequest(messages, tools, model, project, geminiSessionIDFromOptions(options), options)
	request.UserPromptID = uuid.NewString()

	body, err := p.codeAssistPost(ctx, "generateContent", accessToken, request)
	if err != nil {
		return nil, err
	}
	return parseCodeAssistResponse(body)
}

func geminiSessionIDFromOptions(options map[string]any) string {
	if options == nil {
		return ""
	}
	if sessionID, ok := options["session_id"].(string); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

var _ LLMProvider = (*GeminiCodeAssistProvider)(nil)
