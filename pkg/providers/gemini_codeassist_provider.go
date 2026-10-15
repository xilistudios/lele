package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/logger"
)

// This file holds the request/response wire helpers for Google's Code Assist
// "generateContent" RPC (spec §5 "Code Assist API calls") plus the provider
// runtime built on top of them. The helpers are deliberately side-effect free —
// no HTTP, no provider struct, no token source — so they can be golden-tested;
// the runtime (token source, project discovery, Chat) lives under the
// "Provider runtime" banner further down. Nothing here is wired into
// factory.go yet: provider selection is F2-T08.
//
// The authoritative wire evidence is .lele/specs/jcode-gemini.md §5; the Rust
// symbols it cites live in crates/jcode-provider-gemini/src/lib.rs. Per the
// task's "locate jcode symbols by NAME" rule, comments below name the jcode
// symbol instead of a line number.

// Google Code Assist wire constants (spec §5), mirroring the jcode symbols in
// crates/jcode-provider-gemini/src/lib.rs: CODE_ASSIST_ENDPOINT,
// CODE_ASSIST_API_VERSION and DEFAULT_MODEL. They are grouped here so the
// runtime and the wire helpers share one source of truth, exactly like
// antigravityBaseURL does for the sibling Antigravity provider.
const (
	// geminiCodeAssistBaseURL is jcode's CODE_ASSIST_ENDPOINT. The host is the
	// same value as antigravityBaseURL because both speak Cloud Code Assist;
	// what differs is the envelope, not the host.
	geminiCodeAssistBaseURL = "https://cloudcode-pa.googleapis.com"

	// geminiCodeAssistAPIVersion is jcode's CODE_ASSIST_API_VERSION. RPC paths
	// are POST "{base}/{version}:{method}" — e.g.
	// https://cloudcode-pa.googleapis.com/v1internal:generateContent (spec §5).
	geminiCodeAssistAPIVersion = "v1internal"

	// geminiCodeAssistDefaultModel is jcode's DEFAULT_MODEL (spec §5,
	// provider lib.rs). It is the non-dated server-side alias that always
	// resolves to the newest stable pro model, so the default cannot go stale.
	geminiCodeAssistDefaultModel = "gemini-pro-latest"
)

// -----------------------------------------------------------------------------
// Request types (spec §5, "Generate request")
// -----------------------------------------------------------------------------

// codeAssistGenerateRequest is the Cloud Code Assist generateContent envelope.
//
// WHY the tags are explicit: jcode's `CodeAssistGenerateRequest` (spec §5,
// provider lib.rs) carries NO `#[serde(rename_all = "camelCase")]`, so serde
// emits the field names verbatim. That makes `user_prompt_id` snake_case — the
// one key in this envelope that must NOT be camel-cased. The nested
// `VertexGenerateContentRequest` below IS camelCase; mixing the two styles is
// intentional and verified against the reference struct.
type codeAssistGenerateRequest struct {
	Model        string                       `json:"model"`
	Project      string                       `json:"project"`
	UserPromptID string                       `json:"user_prompt_id"`
	Request      vertexGenerateContentRequest `json:"request"`
}

// vertexGenerateContentRequest mirrors jcode's `VertexGenerateContentRequest`
// (spec §5, provider lib.rs), which is `#[serde(rename_all = "camelCase")]`
// except for two explicit renames that this struct reproduces with literal
// tags: `systemInstruction`, `toolConfig` and the deliberately snake_case
// `session_id`.
type vertexGenerateContentRequest struct {
	Contents          []codeAssistContent   `json:"contents"`
	SystemInstruction *codeAssistContent    `json:"systemInstruction,omitempty"`
	Tools             []codeAssistTool      `json:"tools,omitempty"`
	ToolConfig        *codeAssistToolConfig `json:"toolConfig,omitempty"`
	// SessionID is snake_case on purpose: jcode pins `#[serde(rename =
	// "session_id")]` inside an otherwise-camelCase struct (spec §5).
	SessionID string `json:"session_id,omitempty"`
}

// The Gemini body types below are a private, self-contained mirror of spec §5's
// `Gemini*` structs. pkg/providers/antigravity_provider.go already defines a
// sibling set (`antigravityContent`, `antigravityPart`, `antigravityTool`, ...)
// for the SAME Cloud Code Assist host, but those are NOT reusable here:
//
//   - `antigravityPart` has no `inlineData` member, and spec §5's `GeminiPart`
//     needs one for images (content mapping: images -> `inlineData:{mimeType,
//     data}`); and
//   - `antigravitySystemPrompt` has no `role`, while spec §5 sends the system
//     instruction as a `GeminiContent` whose role is "user".
//
// They are also unexported and Antigravity-specific. A parallel `codeAssist*`
// set keeps each envelope honest and named after the spec, so an Antigravity
// schema change can never silently mutate Gemini's wire. Existing shared
// helpers are reused instead (sanitizeSchemaForGemini, normalizeStoredToolCall,
// resolveToolResponseName) to stay DRY with that file's conventions.

// codeAssistContent mirrors spec §5's `GeminiContent` ({role, parts}).
type codeAssistContent struct {
	Role  string           `json:"role"`
	Parts []codeAssistPart `json:"parts"`
}

// codeAssistPart mirrors spec §5's `GeminiPart`. The field order follows the
// Rust declaration (text, inlineData, functionCall, functionResponse,
// thoughtSignature) so the golden test pins the same key order Go emits.
type codeAssistPart struct {
	Text             string                      `json:"text,omitempty"`
	InlineData       *codeAssistInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *codeAssistFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *codeAssistFunctionResponse `json:"functionResponse,omitempty"`
	// ThoughtSignature is the Gemini 3 signature that "must be replayed verbatim
	// on the functionCall part in later turns or the Cloud Code backend rejects
	// the request" (spec §5, GeminiPart.thought_signature).
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

// codeAssistInlineData mirrors spec §5's `InlineData` ({mimeType, data}).
type codeAssistInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// codeAssistFunctionCall mirrors spec §5's `GeminiFunctionCall` ({name, args,
// id}). args serializes as a JSON object; id is optional and echoed back so the
// model's later functionResponse can be correlated.
type codeAssistFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
	ID   string         `json:"id,omitempty"`
}

// codeAssistFunctionResponse mirrors spec §5's `GeminiFunctionResponse`
// ({name, response, id}). `response` is `{"content": ...}` for a successful
// tool result (spec §5: "response: {"content": …} | {"error": …}").
type codeAssistFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
}

// codeAssistTool mirrors spec §5's `GeminiTool` ({functionDeclarations:[...]}).
type codeAssistTool struct {
	FunctionDeclarations []codeAssistFunctionDeclaration `json:"functionDeclarations"`
}

// codeAssistFunctionDeclaration mirrors spec §5's `GeminiFunctionDeclaration`
// ({name, description, parameters}). Parameters carries the JSON Schema already
// normalized for Gemini by sanitizeSchemaForGemini.
type codeAssistFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// codeAssistToolConfig mirrors spec §5: "toolConfig = {"functionCallingConfig":
// {"mode":"AUTO"}} when tools present".
type codeAssistToolConfig struct {
	FunctionCallingConfig codeAssistFunctionCallingConfig `json:"functionCallingConfig"`
}

// codeAssistFunctionCallingConfig mirrors spec §5's
// `GeminiFunctionCallingConfig` ({mode}).
type codeAssistFunctionCallingConfig struct {
	Mode string `json:"mode"`
}

// codeAssistFunctionCallingModeAuto is the only mode jcode sends (spec §5).
const codeAssistFunctionCallingModeAuto = "AUTO"

// buildCodeAssistRequest converts lele's provider-neutral messages and tool
// definitions into a Code Assist generateContent envelope (spec §5).
//
// Envelope ownership is split with the caller: this function sets Model,
// Project and Request (including Request.SessionID); it deliberately does NOT
// set UserPromptID, which is a fresh id per call and belongs to the caller
// (jcode: "user_prompt_id = fresh Uuid::new_v4() per call", runtime
// generate_content).
//
// Conversion follows antigravity_provider.go's conventions (same role mapping,
// same tool shape, same helper reuse) because that file is the package's other
// Cloud Code Assist implementation, EXCEPT where spec §5 differs — those points
// are called out inline. `options` is accepted for signature parity with the
// provider's Chat but is currently unused: spec §5's
// VertexGenerateContentRequest has no `generationConfig` member, so nothing
// here emits one.
//
// Deliberate divergences from jcode, recorded here so they are not mistaken for
// oversights. Each is outside this task's scope and is a known follow-up:
//
//   - `session_id` is only ever read from options["session_id"] and NO
//     production caller sets that option, so every turn is a fresh server-side
//     session. jcode always sends `resume_session_id` or the session cached in
//     its runtime state. lele has no equivalent runtime-state cache, so the
//     field stays caller-supplied and is normally omitted.
//   - The response parser does not map `promptFeedback`/`blockReason`/
//     `finishMessage`: a safety-blocked response therefore surfaces as the
//     generic "no candidates" error, and a SAFETY/RECITATION finish reason is
//     folded into "stop".
//   - jcode's `thoughtSignature` replay/carry-forward policy
//     (SignaturePolicy::ReplayCarriedForward) and its DowngradeToolCallsToText
//     400-recovery are not implemented: lele replays the signature the part
//     carries, if any, and nothing more.
//   - Retries, catalog discovery and onboarding caching are out of F2 scope.
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

	// Registry of tool-call id -> function name, so a later tool result can be
	// named even when only the id survives in history (same approach as
	// antigravity_provider.go's buildRequest).
	toolCallNames := make(map[string]string)

	// The system prompt is captured here but turned into the wire's
	// `systemInstruction` only after the tool declarations are known, because
	// jcode appends geminiFunctionCallGuard whenever tools are advertised (see
	// buildCodeAssistSystemInstructionWithToolGuard).
	//
	// Deliberate lele simplification: when a request carries several system
	// messages the LAST non-blank one wins (each assignment overwrites the
	// previous). jcode has no such case to match — its runtime hands
	// build_contents a single system prompt — so this preserves this builder's
	// historical behavior instead of inventing a merge rule.
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
				// A user message carrying a tool_call_id is really a tool result.
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

	// Tool definitions -> spec §5 tools array. The tool shape
	// ({"functionDeclarations":[{name,description,parameters}]}) matches both
	// antigravity_provider.go and spec §5, so it is reused as-is; schema
	// sanitization uses the shared sanitizeSchemaForGemini.
	declarations := codeAssistFunctionDeclarations(tools)
	hasTools := len(declarations) > 0
	if hasTools {
		req.Request.Tools = []codeAssistTool{{FunctionDeclarations: declarations}}
		// Spec §5: toolConfig = {"functionCallingConfig":{"mode":"AUTO"}} when
		// tools are present. Note antigravity_provider.go omits this; spec §5
		// wins here.
		req.Request.ToolConfig = &codeAssistToolConfig{
			FunctionCallingConfig: codeAssistFunctionCallingConfig{Mode: codeAssistFunctionCallingModeAuto},
		}
	}

	// Spec §5: the system instruction is a GeminiContent with role "user".
	// Assigned AFTER the declarations above so hasTools is known: jcode appends
	// geminiFunctionCallGuard to the prompt whenever tools are advertised.
	req.Request.SystemInstruction = buildCodeAssistSystemInstructionWithToolGuard(systemText, hasTools)

	return req
}

// geminiFunctionCallGuard is jcode's GEMINI_FUNCTION_CALL_GUARD
// (crates/jcode-provider-gemini/src/lib.rs), copied character for character.
//
// It is appended to the system instruction whenever tools are advertised
// because Gemini-3 "thinking" models intermittently emit Python-style
// pseudo-code (e.g. `print(default_api.read(...))`) instead of a clean
// `functionCall`; the Cloud Code backend then rejects the turn with
// MALFORMED_FUNCTION_CALL and empty content. Explicitly forbidding code and
// namespaces measurably reduces that failure mode — this is a quality fix, not
// a cosmetic one, and tools are present on the normal agent path.
//
// Built by concatenating two double-quoted segments instead of using one raw
// literal so the text stays on gofmt-clean lines; the value is byte-identical
// to the Rust const (verified by compiling the Rust literal: see the F2-T13
// evidence under /tmp/f2_t13/).
const geminiFunctionCallGuard = "\n\n## Function calling\n" +
	"- When you call a tool, emit a native function call, not code. Never write Python (or any language) that calls the tool, and never wrap a call in print(...) or a code block.\n" +
	"- Use the function name exactly as defined. Do not prepend `default_api.` or any other namespace to the function name."

// buildCodeAssistSystemInstruction is jcode's `build_system_instruction`: the
// system prompt becomes a GeminiContent with role "user" and one text part.
// An all-whitespace (or empty) prompt yields nil, i.e. the key is omitted
// entirely (`skip_serializing_if = "Option::is_none"` in spec §5).
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

// buildCodeAssistSystemInstructionWithToolGuard is jcode's
// `build_system_instruction_with_tool_guard`: without tools it is exactly
// buildCodeAssistSystemInstruction; with tools the guard is appended to the
// trimmed prompt.
//
// Note the consequence, reproduced on purpose: with tools advertised and NO
// system message, the request still carries a systemInstruction consisting of
// the guard alone (the guard is non-blank, so buildCodeAssistSystemInstruction
// does not discard it).
func buildCodeAssistSystemInstructionWithToolGuard(system string, hasTools bool) *codeAssistContent {
	if !hasTools {
		return buildCodeAssistSystemInstruction(system)
	}
	return buildCodeAssistSystemInstruction(strings.TrimSpace(system) + geminiFunctionCallGuard)
}

// codeAssistFunctionDeclarations maps only the "function" tools, mirroring
// antigravity_provider.go's buildRequest (non-function tool types are skipped).
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

// codeAssistFunctionResponseContent builds the "user" content that carries a
// tool result as a functionResponse part (spec §5). The function name is
// resolved from the assistant's earlier tool call via resolveToolResponseName,
// reusing the shared helper for the same reason antigravity_provider.go does.
//
// Spec §5 shapes the response body as {"content": ...}; antigravity_provider.go
// uses {"result": ...}. The spec wins here (see the task's "ENVELOPE and JSON
// keys must follow spec §5"). No error/`{"error": ...}` branch is emitted
// because lele's Message carries no "this result is an error" flag.
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

// codeAssistUserParts converts a user message into Gemini parts. It follows the
// convention of pkg/providers/anthropic's buildAnthropicContentBlocks: when
// ContentParts exist they are the source of truth (so the "[image]" placeholder
// that protocoltypes folds into Content for persisted multimodal messages is
// never sent as text); otherwise Content is used.
//
// Images become inlineData parts from data: URLs (spec §5). Non-data http(s)
// URLs are skipped: inlining them would require a network fetch, which a pure
// helper must not perform.
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

// codeAssistInlineDataFromDataURL parses a "data:<mime>;base64,<payload>" URL
// into an inlineData part, or returns nil when the URL is not a base64 data URL.
// Mirrors the parsing done by pkg/providers/anthropic and the Bedrock provider.
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

// -----------------------------------------------------------------------------
// Response types + parser (spec §5, `CodeAssistGenerateResponse`)
// -----------------------------------------------------------------------------

// codeAssistGenerateResponse mirrors jcode's `CodeAssistGenerateResponse`
// (spec §5): the inner generateContent payload is wrapped in a "response"
// object, exactly like the SSE frames the Antigravity provider unwraps.
type codeAssistGenerateResponse struct {
	TraceID  string                    `json:"traceId,omitempty"`
	Response *codeAssistVertexResponse `json:"response,omitempty"`
}

// codeAssistVertexResponse mirrors jcode's `VertexGenerateContentResponse`.
type codeAssistVertexResponse struct {
	Candidates    []codeAssistCandidate    `json:"candidates,omitempty"`
	UsageMetadata *codeAssistUsageMetadata `json:"usageMetadata,omitempty"`
}

// codeAssistCandidate mirrors jcode's `GeminiCandidate`.
type codeAssistCandidate struct {
	Content      *codeAssistContent `json:"content,omitempty"`
	FinishReason string             `json:"finishReason,omitempty"`
}

// codeAssistUsageMetadata mirrors spec §5's usageMetadata. jcode also carries
// cachedContentTokenCount, which lele has no field for; TotalTokens is derived
// as prompt + candidates because this response has no total member.
type codeAssistUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

// parseCodeAssistResponse decodes a generateContent body (spec §5) into lele's
// provider-neutral LLMResponse, matching the field conventions used by
// antigravity_provider.go's parseSSEResponse: text parts are joined verbatim,
// functionCall parts become ToolCalls, FinishReason is "stop" (or "tool_calls"
// when a function call is present, "length" on MAX_TOKENS), and usage is
// populated only when the server reported something.
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

// codeAssistToolCallID returns the id for a parsed function call. It prefers
// the server-supplied id (spec §5, GeminiFunctionCall.id) and otherwise
// synthesizes a DETERMINISTIC id from the name and position. The sibling
// AntigravityProvider uses time.Now().UnixNano() here, which makes its output
// untestable; a pure parser must be reproducible.
func codeAssistToolCallID(call *codeAssistFunctionCall, index int) string {
	if call.ID != "" {
		return call.ID
	}
	return fmt.Sprintf("call_%s_%d", call.Name, index)
}

// -----------------------------------------------------------------------------
// Provider runtime (spec §5 handshake + generateContent, spec §6 token refresh)
// -----------------------------------------------------------------------------
//
// Everything above this line is pure wire code. What follows is the runtime
// that turns it into an LLMProvider: token source, project discovery and Chat.
// It is deliberately minimal — jcode's retry policy/Retry-After, model catalog
// discovery+cache, onboarding cache, SSE streaming, schema-400 recovery and the
// Developer-API `x-goog-api-key` path are all out of scope for F2 and are NOT
// implemented here (see .lele/tasks/oauth-providers-f2-tasks.md §1 "OUT OF
// SCOPE"). Nothing selects this provider yet: factory wiring is F2-T08.

// geminiOAuthConfig returns the OAuth preset used to refresh Gemini Code Assist
// credentials. It is a package-level variable — exactly like claudeOAuthConfig
// above and the `var getCredential = auth.GetCredential` seam in factory.go —
// so that tests in pkg/providers can point the Gemini token endpoint at a local
// httptest server: auth.EnsureFreshCredential receives the provider config as an
// argument, so overriding this seam is the only way to redirect the token
// endpoint from this package. Production always uses auth.GeminiOAuthConfig.
var geminiOAuthConfig = auth.GeminiOAuthConfig

const (
	// geminiCodeAssistHTTPTimeout is the total request timeout of spec §5's
	// `gemini_http_client()` (reqwest `.timeout(Duration::from_secs(90))`).
	geminiCodeAssistHTTPTimeout = 90 * time.Second

	// geminiCodeAssistXGoogAPIClient is the value of the `x-goog-api-client`
	// default header sent by jcode's `gemini_http_client()` (spec §5): the
	// official Gemini CLI presents the Node client identity.
	geminiCodeAssistXGoogAPIClient = "gl-node/26.4.0"

	// geminiCodeAssistUserAgentPrefix is the constant part of jcode's
	// `gemini_user_agent()` (spec §5). The model embedded in the UA is ALWAYS
	// geminiCodeAssistDefaultModel, never the per-request model — jcode formats
	// the UA once for the whole client (DEFAULT_MODEL) and reuses it for every
	// call, including calls that asked for another model.
	geminiCodeAssistUserAgentPrefix = "GeminiCLI-tui/0.57.0"

	// geminiCodeAssistLegacyTier is jcode's USER_TIER_LEGACY (spec §5): the
	// tier used for onboarding when the account advertises no default tier.
	geminiCodeAssistLegacyTier = "legacy-tier"

	// geminiCodeAssistFreeTier is jcode's USER_TIER_FREE (spec §5). It selects
	// the reduced onboarding request shape in codeAssistOnboardRequest: free-tier
	// accounts may onboard without naming a Cloud project.
	geminiCodeAssistFreeTier = "free-tier"

	// geminiOnboardPollTimeout bounds the onboardUser long-running-operation
	// wait ("keep the polling bounded (a few minutes)"). Without it a wedged
	// operation would hang a chat turn forever.
	geminiOnboardPollTimeout = 5 * time.Minute
)

// geminiOnboardPollInterval is the delay between onboarding LRO polls (spec §5:
// jcode sleeps 5s before each `get_operation`). It is a variable so tests can
// shrink it to milliseconds; production never writes it.
var geminiOnboardPollInterval = 5 * time.Second

// GeminiCodeAssistProvider implements LLMProvider on top of Google's Cloud Code
// Assist RPC (`{base}/{version}:{method}`) authenticated with a Gemini OAuth
// credential (spec §5). It is the Gemini counterpart of the sibling, currently
// unselected, AntigravityProvider — same host, different envelope.
type GeminiCodeAssistProvider struct {
	// token is the static seed access token handed to the constructor (the
	// factory passes cred.AccessToken). It is only consulted when the caller
	// supplied no token source of its own — see resolveTokenSource.
	token string

	tokenSource  func() (string, error)
	httpClient   *http.Client
	defaultModel string

	// baseURL overrides geminiCodeAssistBaseURL and exists ONLY as a test seam:
	// every request path is built from endpoint(), so an httptest server can
	// stand in for cloudcode-pa.googleapis.com. Production never sets it, which
	// is why the production constant is untouched and stays the default.
	baseURL string
}

// NewGeminiCodeAssistProvider builds a Code Assist provider.
//
// `token` is the seed access token, kept on the struct for signature parity
// with the sibling constructors (NewClaudeProviderWithTokenSource and
// anthropicprovider.NewProviderWithTokenSource, whose factory call sites pass
// cred.AccessToken). Token resolution for every request is the chain in
// resolveTokenSource:
//
//  1. tokenSource, when non-nil — the factory installs the coordinator-backed
//     createGeminiTokenSource() there, so N concurrent turns collapse into at
//     most ONE token refresh (spec §6);
//  2. the `token` seed, when non-empty — a plain static bearer token, for
//     callers that already hold a live token and must not touch the store;
//  3. createGeminiTokenSource() — the constructor deliberately stores a nil
//     tokenSource UNCHANGED (it does not eagerly install a source) so that this
//     last fallback still applies at request time.
//
// The client mirrors spec §5's `gemini_http_client()`: 90s total timeout.
func NewGeminiCodeAssistProvider(token string, tokenSource func() (string, error)) *GeminiCodeAssistProvider {
	return &GeminiCodeAssistProvider{
		token:       token,
		tokenSource: tokenSource,
		httpClient:  &http.Client{Timeout: geminiCodeAssistHTTPTimeout},
	}
}

// resolveTokenSource applies NewGeminiCodeAssistProvider's resolution chain:
// explicit token source → static seed token → coordinator-backed source. It is
// called per request (never cached) because the seed/fallback decision must
// follow the struct as a test or a later caller mutates it, and it never
// returns nil, so Chat cannot panic on a nil call.
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

// GetDefaultModel returns the default model id, falling back to
// geminiCodeAssistDefaultModel when the struct carries none (e.g. a zero-value
// provider built by a test).
func (p *GeminiCodeAssistProvider) GetDefaultModel() string {
	if p.defaultModel != "" {
		return p.defaultModel
	}
	return geminiCodeAssistDefaultModel
}

// createGeminiTokenSource returns a token source that reads the stored Gemini
// credential, refreshing it through the shared single-flight coordinator
// (spec §6: every OAuth Code Assist call funnels through
// `load_or_refresh_tokens()`, which refreshes at most once across concurrent
// callers).
func createGeminiTokenSource() func() (string, error) {
	return func() (string, error) {
		// Routing through the coordinator (instead of reading the store
		// directly) gives Gemini the same single-flight refresh as Claude and
		// codex: N concurrent requests sharing a key collapse into at most ONE
		// token request. It also owns the freshness margin — the 5-minute
		// NeedsRefresh window already subsumes the 60s margin of the reference
		// implementation (spec §6 FRESHNESS_MARGIN_MS) — so no extra margin is
		// applied here.
		cred, err := auth.EnsureFreshCredential("gemini", geminiOAuthConfig(), auth.RefreshPolicy{RequireOAuthMethod: true})
		if err != nil {
			// The coordinator already contextualizes the failure with
			// "loading auth credentials:" / "refreshing token:" /
			// "saving refreshed token:" prefixes; wrapping it again would nest
			// them. Parity with createClaudeTokenSource.
			return "", err
		}
		if cred == nil {
			return "", fmt.Errorf("no credentials for gemini. Run: lele auth login --provider gemini")
		}
		return cred.AccessToken, nil
	}
}

// -----------------------------------------------------------------------------
// Project discovery (spec §5 "Project id discovery" + charter D7)
// -----------------------------------------------------------------------------

// loadCodeAssistProject resolves the Google Cloud project a Code Assist request
// must name, and persists a project discovered over the network onto the stored
// credential so later turns skip the handshake (D7, mirroring
// createAntigravityTokenSource).
//
// Precedence:
//
//  1. cred.ProjectID from the stored credential — already discovered once, so
//     returned with ZERO network calls. This is the ONLY short-circuit, and it
//     is a DELIBERATE F2 DIVERGENCE from jcode: jcode caches the resolved
//     project in its in-memory `GeminiRuntimeState` (produced by
//     `setup_runtime_state`, which lele does not have yet), so it re-derives the
//     project per process; lele uses the credential store as the stand-in for
//     that cache. jcode itself would STILL call `loadCodeAssist` here, even with
//     a known project, because that call is what returns `currentTier` and
//     therefore the entitlement decision (see 3 below).
//  2. Otherwise `loadCodeAssist` is ALWAYS POSTed. The env project
//     (GOOGLE_CLOUD_PROJECT, then GOOGLE_CLOUD_PROJECT_ID, via
//     `google_cloud_project_from_env()` / `cloud_project()`) does not return
//     early any more: it rides along on the request as
//     `cloudaicompanionProject` and as `metadata.duetProject`
//     (`load_code_assist_request` + `client_metadata`), both omitted when empty.
//  3. The project is then resolved in jcode's order: the response's
//     `cloudaicompanionProject` → the env project → the onboarding LRO
//     (`response.cloudaicompanionProject.id`, polling `{base}/{version}/{name}`
//     until done) → an actionable error naming both env vars
//     (`ineligible_or_project_error`). See completeLoadCodeAssist.
//
// The credential is re-read here rather than carried in from the caller: this
// function both consumes it (ProjectID) and writes it back (ProjectID), and
// re-reading guarantees it is the copy the coordinator most recently rotated.
// A nil credential is tolerated — it only means a newly discovered project
// cannot be persisted, which is not an error for the current turn.
func (p *GeminiCodeAssistProvider) loadCodeAssistProject(ctx context.Context, accessToken string) (string, error) {
	cred, err := auth.GetCredential("gemini")
	if err != nil {
		return "", fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred != nil && cred.ProjectID != "" {
		return cred.ProjectID, nil
	}

	// The env project is a REQUEST INPUT, not a short-circuit. jcode's
	// setup_runtime_state POSTs loadCodeAssist unconditionally (runtime
	// `fn setup_runtime_state` step 1) so that the response's currentTier drives
	// the entitlement/onboarding decision; skipping this call made lele's
	// `cloudaicompanionProject`/`duetProject` request fields permanently empty.
	envProject := geminiCloudProjectFromEnv()

	// Network handshake (spec §5 `setup_runtime_state`). jcode tolerates a
	// VPC-SC error by synthesizing a "standard-tier" response; that recovery and
	// the VALIDATION_REQUIRED branch are deliberately NOT ported in F2 (minimal
	// runtime): a SECURITY_POLICY_VIOLATED response surfaces as an API error.
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

	// Persist a server-discovered project (D7): without this, every turn would
	// repeat loadCodeAssist/onboardUser. Best-effort, exactly like
	// createAntigravityTokenSource — a store failure must not fail the turn.
	if cred != nil {
		cred.ProjectID = project
		_ = auth.SetCredential("gemini", cred)
	}
	return project, nil
}

// completeLoadCodeAssist turns a loadCodeAssist response into a project id,
// onboarding the account when the response says it has no tier yet (spec §5
// steps 3-5, mirrored from jcode `setup_runtime_state`).
//
// The resolution order is jcode's, branch by branch:
//
//   - currentTier present (steps 3): response `cloudaicompanionProject` → env
//     project → caller raises ineligible_or_project_error;
//   - currentTier absent (steps 4-5): `choose_onboard_tier` → onboardUser →
//     onboarding LRO project → env project → caller raises
//     ineligible_or_project_error.
//
// It returns ("", nil) when neither the response nor the env carries a project;
// the caller turns that into an actionable error. An error means the handshake
// itself failed (onboarding could not complete), which must NOT be reported as
// "no project found".
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

	// No current tier: this account has never onboarded. Pick the tier the
	// server marks as default, else jcode's legacy-tier fallback, and wait for
	// the onboarding operation to finish.
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

// onboardUser POSTs `{base}/{version}:onboardUser` and polls the returned
// long-running operation until it reports done (spec §5 step 5), returning the
// project the operation produced ("" when it produced none).
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

	// Bound the wait (geminiOnboardPollTimeout) and honour ctx: a wedged
	// operation must not pin a chat turn forever.
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

// geminiCloudProjectFromEnv is jcode's `google_cloud_project_from_env()`
// (spec §5): GOOGLE_CLOUD_PROJECT wins, GOOGLE_CLOUD_PROJECT_ID is the fallback,
// both are trimmed and an empty value is ignored.
func geminiCloudProjectFromEnv() string {
	for _, name := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"} {
		if project := strings.TrimSpace(os.Getenv(name)); project != "" {
			return project
		}
	}
	return ""
}

// codeAssistOnboardTier is jcode's `choose_onboard_tier()` (spec §5): the first
// allowedTiers entry with isDefault == true, else the legacy tier.
func codeAssistOnboardTier(load codeAssistHandshakeResponse) codeAssistUserTier {
	for _, tier := range load.AllowedTiers {
		if tier.IsDefault {
			return tier
		}
	}
	return codeAssistUserTier{ID: geminiCodeAssistLegacyTier}
}

// codeAssistOnboardRequest reproduces the branch jcode's `setup_runtime_state`
// makes between steps 4 and 5 when building the onboardUser body: onboarding
// onto the FREE tier omits the project AND sends fresh metadata whose
// `duetProject` is None, while every other tier sends `{tierId,
// cloudaicompanionProject: <envProject>, metadata}` with the metadata built from
// that same env project. This is a genuine `if tier.id == Some(USER_TIER_FREE)`
// in jcode, not an artefact of an empty env project, so it is reproduced
// literally (an omitted field is the Go equivalent of `skip_serializing_if =
// "Option::is_none"`).
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

// codeAssistProjectError is jcode's `ineligible_or_project_error()` (spec §5):
// prefer the server's own reason messages, and fall back to a message that
// names both env vars.
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

// -----------------------------------------------------------------------------
// Code Assist RPC plumbing (spec §5 `post_json` / `get_operation` / client)
// -----------------------------------------------------------------------------

// codeAssistHandshakeRequest mirrors spec §5's LoadCodeAssistRequest.
type codeAssistHandshakeRequest struct {
	// CloudAICompanionProject is the `cloudaicompanionProject` field jcode fills
	// with the env project (`load_code_assist_request`). It is omitted when
	// empty.
	CloudAICompanionProject string                   `json:"cloudaicompanionProject,omitempty"`
	Metadata                codeAssistClientMetadata `json:"metadata"`
}

// codeAssistClientMetadata mirrors spec §5's `ClientMetadata`, whose values come
// from jcode's `client_metadata()`.
type codeAssistClientMetadata struct {
	IDEType     string `json:"ideType"`
	Platform    string `json:"platform"`
	PluginType  string `json:"pluginType"`
	DuetProject string `json:"duetProject,omitempty"`
}

// geminiClientMetadata is jcode's `client_metadata()` (spec §5): the client
// identity every loadCodeAssist/onboardUser request carries. duetProject is the
// env project when there is one, and is omitted otherwise.
func geminiClientMetadata(envProject string) codeAssistClientMetadata {
	return codeAssistClientMetadata{
		IDEType:     "IDE_UNSPECIFIED",
		Platform:    "PLATFORM_UNSPECIFIED",
		PluginType:  "GEMINI",
		DuetProject: envProject,
	}
}

// codeAssistOnboardUserRequest mirrors spec §5's OnboardUserRequest. Its exact
// shape (which fields are present) is decided by codeAssistOnboardRequest,
// because jcode branches on the chosen tier.
type codeAssistOnboardUserRequest struct {
	TierID                  string                   `json:"tierId,omitempty"`
	CloudAICompanionProject string                   `json:"cloudaicompanionProject,omitempty"`
	Metadata                codeAssistClientMetadata `json:"metadata"`
}

// codeAssistHandshakeResponse mirrors spec §5's LoadCodeAssistResponse (the
// members F2 needs; the model catalog it also carries is out of scope).
type codeAssistHandshakeResponse struct {
	CurrentTier             *codeAssistUserTier    `json:"currentTier,omitempty"`
	AllowedTiers            []codeAssistUserTier   `json:"allowedTiers,omitempty"`
	IneligibleTiers         []codeAssistIneligible `json:"ineligibleTiers,omitempty"`
	CloudAICompanionProject string                 `json:"cloudaicompanionProject,omitempty"`
}

// codeAssistUserTier mirrors spec §5's `GeminiUserTier` ({id,name,isDefault}).
type codeAssistUserTier struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

// codeAssistIneligible mirrors spec §5's `IneligibleTier`; only the reason
// message is used, for the actionable setup error.
type codeAssistIneligible struct {
	ReasonMessage string `json:"reasonMessage,omitempty"`
}

// codeAssistOperationResponse mirrors spec §5's LongRunningOperationResponse.
type codeAssistOperationResponse struct {
	Name     string                   `json:"name,omitempty"`
	Done     bool                     `json:"done,omitempty"`
	Response *codeAssistOnboardResult `json:"response,omitempty"`
}

// codeAssistOnboardResult mirrors spec §5's OnboardUserResponse.
type codeAssistOnboardResult struct {
	CloudAICompanionProject *codeAssistProjectRef `json:"cloudaicompanionProject,omitempty"`
}

// codeAssistProjectRef mirrors spec §5's ProjectRef.
type codeAssistProjectRef struct {
	ID string `json:"id,omitempty"`
}

// newCodeAssistHandshakeRequest builds the loadCodeAssist body with jcode's
// `client_metadata()` values (spec §5), carrying the env project (when there is
// one) both as the request's cloudaicompanionProject and as the metadata
// duetProject.
func newCodeAssistHandshakeRequest(envProject string) codeAssistHandshakeRequest {
	return codeAssistHandshakeRequest{
		CloudAICompanionProject: envProject,
		Metadata:                geminiClientMetadata(envProject),
	}
}

// endpoint returns the base URL of the Code Assist API: the test seam when set,
// geminiCodeAssistBaseURL (jcode's CODE_ASSIST_ENDPOINT) otherwise. This mirrors
// jcode's `base_url()`, minus its CODE_ASSIST_ENDPOINT env override, which is
// not part of F2.
func (p *GeminiCodeAssistProvider) endpoint() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return geminiCodeAssistBaseURL
}

// client returns the HTTP client to use, defaulting to one with the spec §5
// timeout so a zero-value provider cannot end up with an unbounded request.
func (p *GeminiCodeAssistProvider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return &http.Client{Timeout: geminiCodeAssistHTTPTimeout}
}

// codeAssistPost calls the RPC `{endpoint}/{version}:{method}` (spec §5
// `post_json`) and returns the response body.
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

// getCodeAssistOperation GETs an LRO by name (spec §5 `get_operation`): the path
// is `{endpoint}/{version}/{name}`, with any leading slash on the server-issued
// name trimmed.
func (p *GeminiCodeAssistProvider) getCodeAssistOperation(ctx context.Context, accessToken, name string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/%s", p.endpoint(), geminiCodeAssistAPIVersion, strings.TrimPrefix(name, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building gemini code assist operation request: %w", err)
	}
	return p.doCodeAssist(req, "operation", accessToken)
}

// doCodeAssist performs a Code Assist request with the headers spec §5 requires.
//
// The UA is LOAD-BEARING, not cosmetic: jcode's doc comment records an A/B test
// where the same token, project and body run under `jcode/1.0 (gemini)` were
// throttled with 429 RATE_LIMIT_EXCEEDED after 1-2 requests, while the official
// Gemini CLI identity (the UA built by `gemini_user_agent()`) ran unlimited. It
// is sent on every call because jcode attaches it to the shared client's
// defaults, not just to generateContent.
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading gemini code assist %s response: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, codeAssistHTTPError(method, resp.StatusCode, body)
	}
	return body, nil
}

// codeAssistHTTPError formats a non-2xx Code Assist response, in the shape of
// antigravity_provider.go's parseAntigravityError (method + status + truncated
// body). It is its own function rather than a call to that one: this function
// does not rewrite 429s with antigravity-specific quota wording, because the
// Gemini retry/rate-limit policy is explicitly out of scope for F2 and lele
// must surface the server's raw message.
func codeAssistHTTPError(method string, statusCode int, body []byte) error {
	return fmt.Errorf("gemini code assist %s failed (HTTP %d): %s", method, statusCode, truncateString(string(body), 500))
}

// geminiCodeAssistUserAgent mirrors jcode's `gemini_user_agent()` (spec §5).
//
// The embedded model is geminiCodeAssistDefaultModel and never the requested
// model: jcode formats the UA once from DEFAULT_MODEL. jcode's
// JCODE_GEMINI_USER_AGENT override is not ported — lele has no env-var policy
// for vendor user agents and F2 must not invent one.
func geminiCodeAssistUserAgent() string {
	return fmt.Sprintf("%s/%s (%s; %s; terminal)",
		geminiCodeAssistUserAgentPrefix,
		geminiCodeAssistDefaultModel,
		geminiUserAgentOS(runtime.GOOS),
		geminiUserAgentArch(runtime.GOARCH),
	)
}

// geminiUserAgentOS maps a Go GOOS to the string jcode's
// `std::env::consts::OS` would produce ("darwin" is Rust's "macos"); every
// other value is passed through, like jcode's `other` arms.
func geminiUserAgentOS(goos string) string {
	switch goos {
	case "darwin", "macos":
		return "macos"
	default:
		return goos
	}
}

// geminiUserAgentArch maps a Go GOARCH (or a Rust ARCH name) to the string
// jcode's `gemini_user_agent()` emits: `aarch64 -> arm64`, `x86_64 -> x64`,
// anything else verbatim.
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

// -----------------------------------------------------------------------------
// Chat (spec §5 `generate_content`)
// -----------------------------------------------------------------------------

// Chat implements LLMProvider.Chat against `{base}/{version}:generateContent`.
func (p *GeminiCodeAssistProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	// Token resolution follows NewGeminiCodeAssistProvider's chain (explicit
	// source → static seed → coordinator); resolveTokenSource never returns nil,
	// so a zero-value provider cannot panic here.
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
		return nil, err // already contextualized for the user
	}

	request := buildCodeAssistRequest(messages, tools, model, project, geminiSessionIDFromOptions(options), options)
	// spec §5: `user_prompt_id` is a fresh Uuid::new_v4() per call (jcode
	// generate_content). It is set here, not in the pure builder, because the
	// builder must stay deterministic for its golden test.
	request.UserPromptID = uuid.NewString()

	body, err := p.codeAssistPost(ctx, "generateContent", accessToken, request)
	if err != nil {
		return nil, err
	}
	return parseCodeAssistResponse(body)
}

// geminiSessionIDFromOptions returns the caller-supplied session id, or "".
//
// No other provider reads a session option today (grep: pkg/providers passes
// options straight through and only max_tokens/temperature/thinking keys are
// consulted), so this key is introduced here. It is named after spec §5's
// snake_case `session_id` field, and it is advisory: an absent session simply
// means the Code Assist backend correlates the turn itself.
func geminiSessionIDFromOptions(options map[string]any) string {
	if options == nil {
		return ""
	}
	if sessionID, ok := options["session_id"].(string); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

// The provider must satisfy the interface it is written for.
var _ LLMProvider = (*GeminiCodeAssistProvider)(nil)
