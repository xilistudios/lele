package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xilistudios/lele/pkg/auth"
)

// TestGeminiCodeAssistGenerateEnvelopeGolden pins the exact bytes of the Code
// Assist generateContent envelope for the simplest request (one user message,
// no tools). The golden literal is written by hand from spec §5 so that any
// accidental rename of a JSON key (notably `user_prompt_id`) shows up as a diff.
func TestGeminiCodeAssistGenerateEnvelopeGolden(t *testing.T) {
	got := buildCodeAssistRequest(
		[]Message{{Role: "user", Content: "Hello"}},
		nil,
		geminiCodeAssistDefaultModel,
		"test-project",
		"sess-abc",
		nil,
	)

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	// Key order follows the Go struct field order (Model, Project, UserPromptID,
	// Request) / (Contents, SystemInstruction, Tools, ToolConfig, SessionID).
	const want = `{"model":"gemini-pro-latest","project":"test-project","user_prompt_id":"","request":{"contents":[{"role":"user","parts":[{"text":"Hello"}]}],"session_id":"sess-abc"}}`

	if string(raw) != want {
		t.Fatalf("envelope golden mismatch:\n got: %s\nwant: %s", raw, want)
	}
}

// TestGeminiCodeAssistEnvelopeUsesSnakeUserPromptID guards the one deliberate
// snake_case key. jcode's CodeAssistGenerateRequest has no rename_all, so the
// keys are `model`/`project`/`user_prompt_id`/`request`. It also asserts the
// inner object does NOT carry the Antigravity envelope members
// (requestType/userAgent/requestId), which belong to a different envelope.
func TestGeminiCodeAssistEnvelopeUsesSnakeUserPromptID(t *testing.T) {
	got := buildCodeAssistRequest(
		[]Message{{Role: "user", Content: "Hello"}},
		nil,
		geminiCodeAssistDefaultModel,
		"test-project",
		"sess-abc",
		nil,
	)

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if _, ok := envelope["user_prompt_id"]; !ok {
		t.Fatalf("envelope must carry the snake_case key %q, got: %s", "user_prompt_id", raw)
	}
	if _, ok := envelope["userPromptId"]; ok {
		t.Fatalf("envelope must NOT camel-case user_prompt_id, got: %s", raw)
	}

	request, ok := envelope["request"].(map[string]any)
	if !ok {
		t.Fatalf("envelope is missing the inner %q object: %s", "request", raw)
	}
	for _, forbidden := range []string{"requestType", "userAgent", "requestId"} {
		if _, ok := request[forbidden]; ok {
			t.Fatalf("inner request must not carry Antigravity envelope key %q, got: %s", forbidden, raw)
		}
	}
	if _, ok := request["session_id"]; !ok {
		t.Fatalf("inner request must carry the snake_case key %q, got: %s", "session_id", raw)
	}
}

// geminiFunctionCallGuardWant is jcode's GEMINI_FUNCTION_CALL_GUARD, copied by
// hand from crates/jcode-provider-gemini/src/lib.rs. It is deliberately NOT the
// production const: an independent copy means a typo in geminiFunctionCallGuard
// fails the tests below instead of agreeing with itself.
//
// Rust swallows a trailing `\` plus the newline and the following indentation
// of a string literal, so the source spelling
//
//	"\n\n## Function calling\n\
//	     - When you call a tool, ... Never write \
//	     Python ... never wrap a call in \
//	     print(...) or a code block.\n\
//	     - Use the function name ... `default_api.` \
//	     or any other namespace to the function name."
//
// is exactly the value below (verified by compiling the Rust literal and
// diffing its stdout: see the F2-T13 evidence under /tmp/f2_t13/).
const geminiFunctionCallGuardWant = "\n\n## Function calling\n" +
	"- When you call a tool, emit a native function call, not code. Never write Python (or any language) that calls the tool, and never wrap a call in print(...) or a code block.\n" +
	"- Use the function name exactly as defined. Do not prepend `default_api.` or any other namespace to the function name."

// geminiFunctionCallGuardMarker is the guard's opening heading, used to assert
// the fragment is ABSENT where it must not be sent.
const geminiFunctionCallGuardMarker = "## Function calling"

// geminiGuardTestTools is the smallest tool set that makes hasTools true: one
// "function" tool. A non-"function" tool is skipped by
// codeAssistFunctionDeclarations and therefore must NOT trigger the guard.
func geminiGuardTestTools() []ToolDefinition {
	return []ToolDefinition{{
		Type:     "function",
		Function: ToolFunctionDefinition{Name: "read_file"},
	}}
}

// TestGeminiCodeAssistSystemInstructionAppendsFunctionCallGuard pins jcode's
// `build_system_instruction_with_tool_guard`: with tools advertised the system
// instruction is trim(system) + GEMINI_FUNCTION_CALL_GUARD, role "user".
func TestGeminiCodeAssistSystemInstructionAppendsFunctionCallGuard(t *testing.T) {
	const systemText = "  You are a careful assistant.  "

	got := buildCodeAssistRequest(
		[]Message{{Role: "system", Content: systemText}},
		geminiGuardTestTools(),
		geminiCodeAssistDefaultModel,
		"test-project",
		"",
		nil,
	)

	instruction := got.Request.SystemInstruction
	if instruction == nil {
		t.Fatal("SystemInstruction is nil, want the guarded system instruction")
	}
	if instruction.Role != "user" {
		t.Fatalf("SystemInstruction.Role = %q, want %q", instruction.Role, "user")
	}
	if len(instruction.Parts) != 1 {
		t.Fatalf("SystemInstruction.Parts = %d parts, want exactly 1", len(instruction.Parts))
	}

	text := instruction.Parts[0].Text
	want := strings.TrimSpace(systemText) + geminiFunctionCallGuardWant
	if text != want {
		t.Fatalf("system instruction text mismatch:\n got: %q\nwant: %q", text, want)
	}
	// The guard must follow the trimmed prompt verbatim and immediately: exactly
	// one occurrence, starting right after the trimmed system text.
	trimmed := strings.TrimSpace(systemText)
	if at := strings.Index(text, geminiFunctionCallGuardWant); at != len(trimmed) {
		t.Fatalf("guard starts at offset %d, want it to immediately follow the trimmed system text at %d", at, len(trimmed))
	}
}

// TestGeminiCodeAssistSystemInstructionHasNoGuardWithoutTools pins the other
// half of build_system_instruction_with_tool_guard: no tools means the system
// instruction is exactly trim(system), with no guard fragment.
func TestGeminiCodeAssistSystemInstructionHasNoGuardWithoutTools(t *testing.T) {
	const systemText = "  You are a careful assistant.  "

	got := buildCodeAssistRequest(
		[]Message{{Role: "system", Content: systemText}},
		nil,
		geminiCodeAssistDefaultModel,
		"test-project",
		"",
		nil,
	)

	instruction := got.Request.SystemInstruction
	if instruction == nil {
		t.Fatal("SystemInstruction is nil, want the plain system instruction")
	}
	if instruction.Role != "user" {
		t.Fatalf("SystemInstruction.Role = %q, want %q", instruction.Role, "user")
	}
	if len(instruction.Parts) != 1 {
		t.Fatalf("SystemInstruction.Parts = %d parts, want exactly 1", len(instruction.Parts))
	}
	if text := instruction.Parts[0].Text; text != strings.TrimSpace(systemText) {
		t.Fatalf("system instruction text = %q, want the trimmed system text %q", text, strings.TrimSpace(systemText))
	}
	if strings.Contains(instruction.Parts[0].Text, geminiFunctionCallGuardMarker) {
		t.Fatalf("system instruction must not carry the function-call guard without tools, got: %q", instruction.Parts[0].Text)
	}
}

// TestGeminiCodeAssistSystemInstructionGuardOnlyWhenToolsAndNoSystem
// reproduces jcode's consequence on purpose: with tools advertised and NO
// system message, the request still carries a systemInstruction, and it is the
// guard alone.
//
// "Alone" means the guard as build_system_instruction returns it, i.e.
// TrimSpace(guard): jcode builds `system.trim() + GUARD` and then runs that
// through build_system_instruction, whose own `trim()` strips the guard's
// leading blank line when the prompt was empty. Verified by compiling the Rust
// helper (see /tmp/f2_t13/guard_semantics.txt): with tools and an empty prompt
// the value starts directly at "## Function calling".
func TestGeminiCodeAssistSystemInstructionGuardOnlyWhenToolsAndNoSystem(t *testing.T) {
	got := buildCodeAssistRequest(
		[]Message{{Role: "user", Content: "Hello"}},
		geminiGuardTestTools(),
		geminiCodeAssistDefaultModel,
		"test-project",
		"",
		nil,
	)

	instruction := got.Request.SystemInstruction
	if instruction == nil {
		t.Fatal("SystemInstruction is nil, want the guard-only system instruction")
	}
	if instruction.Role != "user" {
		t.Fatalf("SystemInstruction.Role = %q, want %q", instruction.Role, "user")
	}
	if len(instruction.Parts) != 1 {
		t.Fatalf("SystemInstruction.Parts = %d parts, want exactly 1", len(instruction.Parts))
	}
	if text := instruction.Parts[0].Text; text != strings.TrimSpace(geminiFunctionCallGuardWant) {
		t.Fatalf("system instruction text = %q, want the guard alone %q", text, strings.TrimSpace(geminiFunctionCallGuardWant))
	}
}

// TestGeminiCodeAssistEnvelopeGoldenWithToolsSystemAndImages closes the
// coverage gap of the simple golden above: it pins, byte for byte, the
// highest-risk part of the wire — the deliberate camelCase/snake_case mixing
// and the shapes that diverge from antigravity_provider.go.
//
// The marshalled string (not a parsed comparison) is asserted so key ORDER is
// pinned too: contents/systemInstruction/tools/toolConfig/session_id for the
// inner request, text/inlineData/functionCall/functionResponse/thoughtSignature
// for a part, and `user_prompt_id` (snake_case, emitted as "" because the fresh
// id belongs to Chat, not to this pure builder) on the outer envelope.
func TestGeminiCodeAssistEnvelopeGoldenWithToolsSystemAndImages(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "  You are a careful assistant.  "},
		{
			Role:    "assistant",
			Content: "Let me read that file.",
			ToolCalls: []ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: map[string]any{"path": "README.md"},
				Function: &FunctionCall{
					Name:             "read_file",
					Arguments:        `{"path":"README.md"}`,
					ThoughtSignature: "sig-abc",
				},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "file contents"},
		{
			Role: "user",
			ContentParts: []ContentPart{
				{Type: "text", Text: "What is in this image?"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,aGVsbG8="}},
			},
		},
	}
	tools := []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "read_file",
			Description: "Read a file from disk.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to read.",
					},
				},
				"required": []any{"path"},
			},
		},
	}}

	got := buildCodeAssistRequest(messages, tools, geminiCodeAssistDefaultModel, "test-project", "sess-golden", nil)

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	// Hand-written from spec §5. Notes on the deliberate details:
	//   - `user_prompt_id` is snake_case on the outer envelope;
	//   - `session_id` is snake_case INSIDE the camelCase inner object;
	//   - the system instruction carries the function-call guard because tools
	//     are advertised (its text is trim(system) + the guard);
	//   - the assistant part order is text, functionCall, thoughtSignature;
	//   - the tool result is a functionResponse with {"content": ...};
	//   - the image is inlineData:{mimeType,data};
	//   - the schema map keys are alphabetical because encoding/json sorts maps.
	//
	// The expected value is the MARSHALLED string, so the guard's newlines appear
	// here escaped (encoding/json writes "\n" for a newline): the guard fragment
	// below is the hand-written literal with exactly that escape applied. The
	// comparison is byte-for-byte, so key order is pinned too.
	const wantHead = `{"model":"gemini-pro-latest","project":"test-project","user_prompt_id":"","request":{"contents":[` +
		`{"role":"model","parts":[{"text":"Let me read that file."},` +
		`{"functionCall":{"name":"read_file","args":{"path":"README.md"},"id":"call-1"},"thoughtSignature":"sig-abc"}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"read_file","response":{"content":"file contents"},"id":"call-1"}}]},` +
		`{"role":"user","parts":[{"text":"What is in this image?"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}` +
		`],"systemInstruction":{"role":"user","parts":[{"text":"You are a careful assistant.`
	const wantTail = `"}]},` +
		`"tools":[{"functionDeclarations":[{"name":"read_file","description":"Read a file from disk.",` +
		`"parameters":{"properties":{"path":{"description":"Path to read.","type":"string"}},"required":["path"],"type":"object"}}]}],` +
		`"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}},` +
		`"session_id":"sess-golden"}}`
	want := wantHead + strings.ReplaceAll(geminiFunctionCallGuardWant, "\n", `\n`) + wantTail

	if string(raw) != want {
		t.Fatalf("envelope golden mismatch:\n got: %q\nwant: %q", raw, want)
	}
}

// TestGeminiCodeAssistParseResponse checks that text parts across candidates
// are joined and that usageMetadata maps onto UsageInfo.
func TestGeminiCodeAssistParseResponse(t *testing.T) {
	body := []byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "},{"text":"world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}}`)

	resp, err := parseCodeAssistResponse(body)
	if err != nil {
		t.Fatalf("parseCodeAssistResponse: %v", err)
	}

	if resp.Content != "Hello world" {
		t.Fatalf("content: got %q want %q", resp.Content, "Hello world")
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("finish reason: got %q want %q", resp.FinishReason, "stop")
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.Usage == nil {
		t.Fatal("expected usage info, got nil")
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Fatalf("usage: got %+v want prompt=10 completion=5 total=15", *resp.Usage)
	}
}

// TestGeminiCodeAssistParseResponseToolCall checks that a functionCall part
// becomes exactly one ToolCall and flips the finish reason to "tool_calls".
func TestGeminiCodeAssistParseResponseToolCall(t *testing.T) {
	body := []byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Madrid"}}}]},"finishReason":"STOP"}]}}`)

	resp, err := parseCodeAssistResponse(body)
	if err != nil {
		t.Fatalf("parseCodeAssistResponse: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected exactly 1 tool call, got %d", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.Name != "get_weather" {
		t.Fatalf("tool call name: got %q want %q", call.Name, "get_weather")
	}
	if call.Arguments["city"] != "Madrid" {
		t.Fatalf("tool call args[city]: got %v want %q", call.Arguments["city"], "Madrid")
	}
	if call.Function == nil || call.Function.Name != "get_weather" {
		t.Fatalf("tool call Function field not populated: %+v", call.Function)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish reason: got %q want %q", resp.FinishReason, "tool_calls")
	}
	if resp.Content != "" {
		t.Fatalf("expected empty text content, got %q", resp.Content)
	}
}

// TestGeminiCodeAssistParseResponseEmptyCandidates requires a clear error when
// the response is well-formed but carries no candidates.
func TestGeminiCodeAssistParseResponseEmptyCandidates(t *testing.T) {
	_, err := parseCodeAssistResponse([]byte(`{"response":{"candidates":[]}}`))
	if err == nil {
		t.Fatal("expected an error for a present-but-empty candidates list, got nil")
	}
}

// -----------------------------------------------------------------------------
// Runtime test helpers
// -----------------------------------------------------------------------------

// setupGeminiTestEnv isolates the credential store used by the token source and
// by project persistence: a throwaway LELE_CONFIG_DIR plus UseStore(nil)
// selects the legacy JSON backend. UseStore(nil) is the only store seam
// exported to this package (pkg/auth's in-package SQLite helpers are not
// importable), and an isolated config dir makes that backend safe to write to.
// It also pins the two project env vars: loadCodeAssistProject reads them into
// the loadCodeAssist request (and uses them as the project fallback), so a
// developer shell that exports them would otherwise change what these tests
// observe.
//
// UseStore(nil) deliberately resets pkg/auth's package-global store and cannot
// restore it: pkg/auth exposes no getter for the active repository (only the
// unexported currentRepo), so there is no prior value to hand back to a
// t.Cleanup. The tests in this file must therefore stay serial — no
// t.Parallel — and must not depend on a store a previous test installed.
func setupGeminiTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	auth.UseStore(nil)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT_ID", "")
}

// geminiTokenServer is a fake Gemini token endpoint that counts requests and
// rotates the refresh token, like Google's does.
type geminiTokenServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits int
}

func (s *geminiTokenServer) HitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

func newGeminiTokenServer(t *testing.T) *geminiTokenServer {
	t.Helper()
	ts := &geminiTokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		ts.hits++
		ts.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// pointGeminiOAuthConfigAt redirects the Gemini token endpoint to the fake
// server by overriding the package-level seam, restoring the original value
// when the test ends.
func pointGeminiOAuthConfigAt(t *testing.T, srv *httptest.Server) {
	t.Helper()
	original := geminiOAuthConfig
	geminiOAuthConfig = func() auth.OAuthProviderConfig {
		cfg := auth.GeminiOAuthConfig()
		cfg.TokenURL = srv.URL
		cfg.HTTPClient = srv.Client()
		return cfg
	}
	t.Cleanup(func() { geminiOAuthConfig = original })
}

// storedGeminiCredential builds an expired oauth credential that NeedsRefresh()
// and carries the given refresh token.
func storedGeminiCredential(refreshToken string) *auth.AuthCredential {
	return &auth.AuthCredential{
		AccessToken:  "stale-access",
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(-time.Hour),
		Provider:     "gemini",
		AuthMethod:   "oauth",
	}
}

// capturedCodeAssistRequest is one request a provider made to the stub.
type capturedCodeAssistRequest struct {
	path   string
	header http.Header
	body   []byte
}

// geminiCodeAssistStub stands in for cloudcode-pa.googleapis.com: it records
// every request and answers it with the status/body the test's handler returns.
// A nil handler answers 200 {} (enough for tests that only assert an endpoint
// is NOT called).
type geminiCodeAssistStub struct {
	*httptest.Server
	handler func(req capturedCodeAssistRequest) (int, string)

	mu       sync.Mutex
	requests []capturedCodeAssistRequest
}

func newGeminiCodeAssistStub(t *testing.T, handler func(req capturedCodeAssistRequest) (int, string)) *geminiCodeAssistStub {
	t.Helper()
	stub := &geminiCodeAssistStub{handler: handler}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured := capturedCodeAssistRequest{path: r.URL.Path, header: r.Header.Clone(), body: body}

		stub.mu.Lock()
		stub.requests = append(stub.requests, captured)
		stub.mu.Unlock()

		status, responseBody := http.StatusOK, "{}"
		if stub.handler != nil {
			status, responseBody = stub.handler(captured)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(stub.Close)
	return stub
}

// requestsFor returns the requests the stub received for one path.
func (s *geminiCodeAssistStub) requestsFor(path string) []capturedCodeAssistRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []capturedCodeAssistRequest
	for _, req := range s.requests {
		if req.path == path {
			out = append(out, req)
		}
	}
	return out
}

// newGeminiTestProvider builds a provider whose token source returns token and
// whose base URL is the stub (the package-private test seam; production always
// leaves baseURL empty and therefore talks to geminiCodeAssistBaseURL).
func newGeminiTestProvider(t *testing.T, stub *httptest.Server, token string) *GeminiCodeAssistProvider {
	t.Helper()
	p := NewGeminiCodeAssistProvider(token, func() (string, error) { return token, nil })
	p.baseURL = stub.URL
	return p
}

// requireGeminiHeaders asserts the headers spec §5 puts on every Code Assist
// request.
func requireGeminiHeaders(t *testing.T, header http.Header, token string) {
	t.Helper()
	if got := header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("Authorization = %q, want %q", got, "Bearer "+token)
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := header.Get("X-Goog-Api-Client"); got != geminiCodeAssistXGoogAPIClient {
		t.Errorf("x-goog-api-client = %q, want %q", got, geminiCodeAssistXGoogAPIClient)
	}
	// The UA always embeds the DEFAULT model, even for calls that requested a
	// different one (jcode formats gemini_user_agent() from DEFAULT_MODEL).
	wantUA := geminiCodeAssistUserAgentPrefix + "/" + geminiCodeAssistDefaultModel + " ("
	ua := header.Get("User-Agent")
	if !strings.HasPrefix(ua, wantUA) || !strings.HasSuffix(ua, "; terminal)") {
		t.Errorf("User-Agent = %q, want %q…(os; arch; terminal)", ua, wantUA)
	}
}

// -----------------------------------------------------------------------------
// Token source
// -----------------------------------------------------------------------------

// TestGeminiTokenSourceUsesCoordinator is the mandatory mutation #8 guard: two
// concurrent callers of the token source must collapse into exactly ONE
// token-endpoint request (single-flight, spec §6) and both must observe the
// fresh access token, with the rotated refresh token persisted. Replacing
// auth.EnsureFreshCredential with a plain store read makes this test fail
// (0 hits and a stale token).
func TestGeminiTokenSourceUsesCoordinator(t *testing.T) {
	setupGeminiTestEnv(t)
	srv := newGeminiTokenServer(t)
	pointGeminiOAuthConfigAt(t, srv.Server)

	if err := auth.SetCredential("gemini", storedGeminiCredential("old-refresh")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	tokenSource := createGeminiTokenSource()

	const callers = 2
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = tokenSource()
		}(i)
	}
	wg.Wait()

	if got := srv.HitCount(); got != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1 (single-flight)", got)
	}
	for i := range errs {
		if errs[i] != nil {
			t.Errorf("caller %d error: %v", i, errs[i])
		}
		if tokens[i] != "fresh-access" {
			t.Errorf("caller %d token = %q, want %q", i, tokens[i], "fresh-access")
		}
	}

	stored, err := auth.GetCredential("gemini")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored == nil {
		t.Fatal("GetCredential() = nil, want the rotated credential")
	}
	if stored.RefreshToken != "rotated-refresh" {
		t.Errorf("stored RefreshToken = %q, want %q", stored.RefreshToken, "rotated-refresh")
	}
	if stored.AccessToken != "fresh-access" {
		t.Errorf("stored AccessToken = %q, want %q", stored.AccessToken, "fresh-access")
	}
}

// -----------------------------------------------------------------------------
// Project discovery
// -----------------------------------------------------------------------------

// TestGeminiLoadCodeAssistProjectFromStoredCredential pins D7's shortcut: a
// credential that already carries a project id is used as-is, with ZERO network
// calls (no loadCodeAssist, no onboarding).
func TestGeminiLoadCodeAssistProjectFromStoredCredential(t *testing.T) {
	setupGeminiTestEnv(t)
	stub := newGeminiCodeAssistStub(t, nil)

	cred := storedGeminiCredential("refresh")
	cred.ProjectID = "stored-proj"
	if err := auth.SetCredential("gemini", cred); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	p := newGeminiTestProvider(t, stub.Server, "tok")
	project, err := p.loadCodeAssistProject(context.Background(), "tok")
	if err != nil {
		t.Fatalf("loadCodeAssistProject() error: %v", err)
	}
	if project != "stored-proj" {
		t.Errorf("project = %q, want %q", project, "stored-proj")
	}
	if got := len(stub.requests); got != 0 {
		t.Errorf("stub received %d request(s), want 0 (stored project needs no network): %+v", got, stub.requests)
	}
}

// TestGeminiLoadCodeAssistProjectFromEnv pins the env override (spec §5
// `google_cloud_project_from_env`): GOOGLE_CLOUD_PROJECT wins,
// GOOGLE_CLOUD_PROJECT_ID is the fallback, both are trimmed and an empty value
// is ignored.
//
// C1: the env project is a REQUEST INPUT, not a short-circuit. jcode's
// setup_runtime_state always POSTs loadCodeAssist (its response carries
// currentTier), so this test now asserts the call HAPPENS, that the env project
// rides on the wire as cloudaicompanionProject AND metadata.duetProject, and
// that it is the result when the (currentTier-bearing) response names no
// project.
func TestGeminiLoadCodeAssistProjectFromEnv(t *testing.T) {
	cases := []struct {
		name      string
		project   string
		projectID string
		want      string
	}{
		{name: "primary var", project: "env-proj", want: "env-proj"},
		{name: "primary var trimmed", project: "  env-proj  ", want: "env-proj"},
		{name: "fallback var", project: "  ", projectID: "env-proj-id", want: "env-proj-id"},
		{name: "primary wins over fallback", project: "env-proj", projectID: "other", want: "env-proj"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupGeminiTestEnv(t)
			t.Setenv("GOOGLE_CLOUD_PROJECT", tc.project)
			t.Setenv("GOOGLE_CLOUD_PROJECT_ID", tc.projectID)

			// The handshake IS expected: a currentTier response with no
			// cloudaicompanionProject is exactly the case where the env project
			// is the answer. Any other path (in particular onboarding) is a bug.
			stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
				if req.path != "/v1internal:loadCodeAssist" {
					t.Errorf("unexpected request to %q", req.path)
					return http.StatusInternalServerError, `{}`
				}
				return http.StatusOK, `{"currentTier":{"id":"free-tier"}}`
			})

			p := newGeminiTestProvider(t, stub.Server, "tok")
			project, err := p.loadCodeAssistProject(context.Background(), "tok")
			if err != nil {
				t.Fatalf("loadCodeAssistProject() error: %v", err)
			}
			if project != tc.want {
				t.Errorf("project = %q, want %q", project, tc.want)
			}

			requests := stub.requestsFor("/v1internal:loadCodeAssist")
			if len(requests) != 1 {
				t.Fatalf("loadCodeAssist requests = %d, want exactly 1 (jcode always calls it, env project or not)", len(requests))
			}
			// The env project must be on the wire, both as the request's
			// cloudaicompanionProject and as metadata.duetProject (jcode's
			// load_code_assist_request / client_metadata).
			if want := `"cloudaicompanionProject":"` + tc.want + `"`; !strings.Contains(string(requests[0].body), want) {
				t.Errorf("loadCodeAssist body must contain %s, got: %s", want, requests[0].body)
			}

			var body map[string]any
			if err := json.Unmarshal(requests[0].body, &body); err != nil {
				t.Fatalf("loadCodeAssist body is not JSON: %v (%s)", err, requests[0].body)
			}
			metadata, ok := body["metadata"].(map[string]any)
			if !ok {
				t.Fatalf("loadCodeAssist body has no metadata object: %s", requests[0].body)
			}
			if got := metadata["duetProject"]; got != tc.want {
				t.Errorf("metadata.duetProject = %v, want %q", got, tc.want)
			}
		})
	}
}

// TestGeminiLoadCodeAssistResponseWinsOverEnv pins jcode's precedence: the
// server's cloudaicompanionProject beats the env project (setup_runtime_state
// step 3 checks the response first, the env project second).
func TestGeminiLoadCodeAssistResponseWinsOverEnv(t *testing.T) {
	setupGeminiTestEnv(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj")

	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		if req.path != "/v1internal:loadCodeAssist" {
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
		return http.StatusOK, `{"currentTier":{"id":"free-tier"},"cloudaicompanionProject":"proj-won"}`
	})

	p := newGeminiTestProvider(t, stub.Server, "tok")
	project, err := p.loadCodeAssistProject(context.Background(), "tok")
	if err != nil {
		t.Fatalf("loadCodeAssistProject() error: %v", err)
	}
	if project != "proj-won" {
		t.Errorf("project = %q, want the response's %q (response wins over env)", project, "proj-won")
	}
	if got := len(stub.requestsFor("/v1internal:loadCodeAssist")); got != 1 {
		t.Errorf("loadCodeAssist requests = %d, want 1", got)
	}
}

// TestGeminiLoadCodeAssistOmitsEmptyProjectField guards the omitempty contract
// on the request: with no env project the key `cloudaicompanionProject` must be
// ABSENT from the body (not present-and-empty), exactly like jcode's
// `skip_serializing_if = "Option::is_none"` on LoadCodeAssistRequest.
func TestGeminiLoadCodeAssistOmitsEmptyProjectField(t *testing.T) {
	setupGeminiTestEnv(t)

	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		if req.path != "/v1internal:loadCodeAssist" {
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
		return http.StatusOK, `{"currentTier":{"id":"free-tier"}}`
	})

	p := newGeminiTestProvider(t, stub.Server, "tok")
	_, err := p.loadCodeAssistProject(context.Background(), "tok")
	// The handshake succeeds, but there is no project anywhere (no env, no
	// response project): the actionable error is the expected outcome.
	if err == nil {
		t.Fatal("expected an error when no project can be determined, got nil")
	}

	requests := stub.requestsFor("/v1internal:loadCodeAssist")
	if len(requests) != 1 {
		t.Fatalf("loadCodeAssist requests = %d, want 1", len(requests))
	}
	if strings.Contains(string(requests[0].body), "cloudaicompanionProject") {
		t.Errorf("loadCodeAssist body must omit cloudaicompanionProject entirely when the env carries none, got: %s", requests[0].body)
	}
	var body map[string]any
	if err := json.Unmarshal(requests[0].body, &body); err != nil {
		t.Fatalf("loadCodeAssist body is not JSON: %v (%s)", err, requests[0].body)
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("loadCodeAssist body has no metadata object: %s", requests[0].body)
	}
	if _, ok := metadata["duetProject"]; ok {
		t.Errorf("metadata must omit an empty duetProject: %s", requests[0].body)
	}
}

// TestGeminiLoadCodeAssistProjectFromResponse pins the wire of the handshake:
// POST {base}/v1internal:loadCodeAssist with the spec §5 body, an OAuth bearer
// token and the official-CLI identity headers, and a project taken from the
// response's cloudaicompanionProject — then persisted onto the credential.
func TestGeminiLoadCodeAssistProjectFromResponse(t *testing.T) {
	setupGeminiTestEnv(t)
	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		if req.path != "/v1internal:loadCodeAssist" {
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
		return http.StatusOK, `{"currentTier":{"id":"free-tier"},"cloudaicompanionProject":"proj-1"}`
	})

	if err := auth.SetCredential("gemini", storedGeminiCredential("refresh")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	p := newGeminiTestProvider(t, stub.Server, "tok")
	project, err := p.loadCodeAssistProject(context.Background(), "tok")
	if err != nil {
		t.Fatalf("loadCodeAssistProject() error: %v", err)
	}
	if project != "proj-1" {
		t.Errorf("project = %q, want %q", project, "proj-1")
	}

	requests := stub.requestsFor("/v1internal:loadCodeAssist")
	if len(requests) != 1 {
		t.Fatalf("loadCodeAssist requests = %d, want 1", len(requests))
	}
	requireGeminiHeaders(t, requests[0].header, "tok")

	var body map[string]any
	if err := json.Unmarshal(requests[0].body, &body); err != nil {
		t.Fatalf("loadCodeAssist body is not JSON: %v (%s)", err, requests[0].body)
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("loadCodeAssist body has no metadata object: %s", requests[0].body)
	}
	for key, want := range map[string]string{
		"ideType":    "IDE_UNSPECIFIED",
		"platform":   "PLATFORM_UNSPECIFIED",
		"pluginType": "GEMINI",
	} {
		if got := metadata[key]; got != want {
			t.Errorf("metadata.%s = %v, want %q", key, got, want)
		}
	}
	// The env project is absent (and therefore omitted) when the environment
	// carries none: spec §5's `cloudaicompanionProject` is optional.
	if _, ok := body["cloudaicompanionProject"]; ok {
		t.Errorf("loadCodeAssist body must omit an empty cloudaicompanionProject: %s", requests[0].body)
	}
	if _, ok := metadata["duetProject"]; ok {
		t.Errorf("metadata must omit an empty duetProject: %s", requests[0].body)
	}

	// D7: a discovered project is persisted so the next turn skips the
	// handshake.
	stored, err := auth.GetCredential("gemini")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored == nil || stored.ProjectID != "proj-1" {
		t.Errorf("stored ProjectID = %+v, want proj-1", stored)
	}
}

// TestGeminiLoadCodeAssistProjectOnboardsWhenNoTier pins spec §5 step 4-5: a
// response with no currentTier starts the onboarding operation, the LRO is
// polled at {base}/{version}/{name} until done, and the project comes from
// response.cloudaicompanionProject.id.
func TestGeminiLoadCodeAssistProjectOnboardsWhenNoTier(t *testing.T) {
	setupGeminiTestEnv(t)

	originalInterval := geminiOnboardPollInterval
	geminiOnboardPollInterval = time.Millisecond
	t.Cleanup(func() { geminiOnboardPollInterval = originalInterval })

	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		switch req.path {
		case "/v1internal:loadCodeAssist":
			return http.StatusOK, `{"allowedTiers":[{"id":"free-tier","isDefault":true},{"id":"legacy-tier"}]}`
		case "/v1internal:onboardUser":
			return http.StatusOK, `{"name":"operations/abc","done":false}`
		case "/v1internal/operations/abc":
			return http.StatusOK, `{"name":"operations/abc","done":true,"response":{"cloudaicompanionProject":{"id":"proj-lro"}}}`
		default:
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
	})

	p := newGeminiTestProvider(t, stub.Server, "tok")
	project, err := p.loadCodeAssistProject(context.Background(), "tok")
	if err != nil {
		t.Fatalf("loadCodeAssistProject() error: %v", err)
	}
	if project != "proj-lro" {
		t.Errorf("project = %q, want %q", project, "proj-lro")
	}

	onboard := stub.requestsFor("/v1internal:onboardUser")
	if len(onboard) != 1 {
		t.Fatalf("onboardUser requests = %d, want 1", len(onboard))
	}
	requireGeminiHeaders(t, onboard[0].header, "tok")

	var body map[string]any
	if err := json.Unmarshal(onboard[0].body, &body); err != nil {
		t.Fatalf("onboardUser body is not JSON: %v (%s)", err, onboard[0].body)
	}
	if got := body["tierId"]; got != "free-tier" {
		t.Errorf("onboardUser tierId = %v, want the isDefault=true tier %q", got, "free-tier")
	}

	polls := stub.requestsFor("/v1internal/operations/abc")
	if len(polls) < 1 {
		t.Fatalf("operation polls = %d, want at least 1", len(polls))
	}
	requireGeminiHeaders(t, polls[0].header, "tok")
}

// shrinkGeminiOnboardPollInterval makes the onboarding LRO poll delay
// millisecond-scale for the duration of a test (the seam exists for exactly
// this); production keeps 5s.
func shrinkGeminiOnboardPollInterval(t *testing.T) {
	t.Helper()
	original := geminiOnboardPollInterval
	geminiOnboardPollInterval = time.Millisecond
	t.Cleanup(func() { geminiOnboardPollInterval = original })
}

// TestGeminiOnboardUserFreeTierOmitsProject pins jcode's free-tier branch of
// setup_runtime_state steps 4-5: FREE-tier onboarding sends `{tierId, metadata}`
// with metadata.duetProject = None and NO cloudaicompanionProject, even when an
// env project is configured (free accounts may not name a Cloud project). With
// no project on the LRO, the env project is the final fallback.
func TestGeminiOnboardUserFreeTierOmitsProject(t *testing.T) {
	setupGeminiTestEnv(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj")
	shrinkGeminiOnboardPollInterval(t)

	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		switch req.path {
		case "/v1internal:loadCodeAssist":
			return http.StatusOK, `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`
		case "/v1internal:onboardUser":
			return http.StatusOK, `{"name":"operations/free","done":true}`
		default:
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
	})

	p := newGeminiTestProvider(t, stub.Server, "tok")
	project, err := p.loadCodeAssistProject(context.Background(), "tok")
	if err != nil {
		t.Fatalf("loadCodeAssistProject() error: %v", err)
	}
	// The LRO carried no project, so the env project is the fallback.
	if project != "env-proj" {
		t.Errorf("project = %q, want the env fallback %q", project, "env-proj")
	}

	onboard := stub.requestsFor("/v1internal:onboardUser")
	if len(onboard) != 1 {
		t.Fatalf("onboardUser requests = %d, want 1", len(onboard))
	}
	var body map[string]any
	if err := json.Unmarshal(onboard[0].body, &body); err != nil {
		t.Fatalf("onboardUser body is not JSON: %v (%s)", err, onboard[0].body)
	}
	if got := body["tierId"]; got != "free-tier" {
		t.Errorf("onboardUser tierId = %v, want %q", got, "free-tier")
	}
	if _, ok := body["cloudaicompanionProject"]; ok {
		t.Errorf("free-tier onboarding must omit cloudaicompanionProject, got: %s", onboard[0].body)
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("onboardUser body has no metadata object: %s", onboard[0].body)
	}
	if _, ok := metadata["duetProject"]; ok {
		t.Errorf("free-tier onboarding must send fresh metadata with duetProject absent, got: %s", onboard[0].body)
	}
}

// TestGeminiOnboardUserNonFreeTierSendsProject pins the OTHER branch of jcode's
// setup_runtime_state steps 4-5: any tier but free-tier sends `{tierId,
// cloudaicompanionProject: <envProject>, metadata}` with duetProject =
// <envProject>. This branch is only reachable from C1 on: before the fix the
// env project short-circuited the handshake, so envProject was always empty
// here.
func TestGeminiOnboardUserNonFreeTierSendsProject(t *testing.T) {
	setupGeminiTestEnv(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj")
	shrinkGeminiOnboardPollInterval(t)

	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		switch req.path {
		case "/v1internal:loadCodeAssist":
			return http.StatusOK, `{"allowedTiers":[{"id":"standard-tier","isDefault":true}]}`
		case "/v1internal:onboardUser":
			return http.StatusOK, `{"name":"operations/paid","done":true,"response":{"cloudaicompanionProject":{"id":"proj-paid"}}}`
		default:
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
	})

	p := newGeminiTestProvider(t, stub.Server, "tok")
	project, err := p.loadCodeAssistProject(context.Background(), "tok")
	if err != nil {
		t.Fatalf("loadCodeAssistProject() error: %v", err)
	}
	if project != "proj-paid" {
		t.Errorf("project = %q, want the onboarding LRO project %q", project, "proj-paid")
	}

	onboard := stub.requestsFor("/v1internal:onboardUser")
	if len(onboard) != 1 {
		t.Fatalf("onboardUser requests = %d, want 1", len(onboard))
	}
	var body map[string]any
	if err := json.Unmarshal(onboard[0].body, &body); err != nil {
		t.Fatalf("onboardUser body is not JSON: %v (%s)", err, onboard[0].body)
	}
	if got := body["tierId"]; got != "standard-tier" {
		t.Errorf("onboardUser tierId = %v, want %q", got, "standard-tier")
	}
	if got := body["cloudaicompanionProject"]; got != "env-proj" {
		t.Errorf("onboardUser cloudaicompanionProject = %v, want the env project %q", got, "env-proj")
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("onboardUser body has no metadata object: %s", onboard[0].body)
	}
	if got := metadata["duetProject"]; got != "env-proj" {
		t.Errorf("onboardUser metadata.duetProject = %v, want %q", got, "env-proj")
	}
}

// TestGeminiLoadCodeAssistProjectErrorNamesEnvVars requires the actionable
// fallback error (spec §5 `ineligible_or_project_error`) to name BOTH env vars
// when neither the response nor the environment yields a project, and to prefer
// the server's own reason messages when it sends any.
func TestGeminiLoadCodeAssistProjectErrorNamesEnvVars(t *testing.T) {
	cases := []struct {
		name     string
		response string
		want     []string
	}{
		{
			name:     "no project anywhere",
			response: `{"currentTier":{"id":"free-tier"}}`,
			want:     []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"},
		},
		{
			name:     "ineligible account",
			response: `{"currentTier":{"id":"free-tier"},"ineligibleTiers":[{"reasonMessage":"Not eligible"}]}`,
			want:     []string{"Not eligible", "GOOGLE_CLOUD_PROJECT"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupGeminiTestEnv(t)
			stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
				return http.StatusOK, tc.response
			})

			p := newGeminiTestProvider(t, stub.Server, "tok")
			_, err := p.loadCodeAssistProject(context.Background(), "tok")
			if err == nil {
				t.Fatal("expected an error when no project can be determined, got nil")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must mention %q", err.Error(), want)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Chat
// -----------------------------------------------------------------------------

// TestGeminiChatWire runs a full turn against a stub that serves BOTH the
// handshake and generateContent, and pins everything spec §5 requires on the
// wire: the {base}/{version}:generateContent path, the OAuth bearer token, the
// JSON content type, the official-CLI UA / x-goog-api-client, and the envelope
// keys model/project/user_prompt_id/request.
func TestGeminiChatWire(t *testing.T) {
	setupGeminiTestEnv(t)
	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		switch req.path {
		case "/v1internal:loadCodeAssist":
			return http.StatusOK, `{"currentTier":{"id":"free-tier"},"cloudaicompanionProject":"proj-wire"}`
		case "/v1internal:generateContent":
			return http.StatusOK, `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello from Code Assist"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}}`
		default:
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
	})

	p := newGeminiTestProvider(t, stub.Server, "tok-123")
	resp, err := p.Chat(
		context.Background(),
		[]Message{{Role: "user", Content: "Hi"}},
		nil,
		"gemini-2.5-pro",
		map[string]any{"session_id": "sess-1"},
	)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	if resp.Content != "Hello from Code Assist" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello from Code Assist")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 3 || resp.Usage.TotalTokens != 10 {
		t.Errorf("Usage = %+v, want prompt=7 completion=3 total=10", resp.Usage)
	}

	requests := stub.requestsFor("/v1internal:generateContent")
	if len(requests) != 1 {
		t.Fatalf("generateContent requests = %d, want 1", len(requests))
	}
	requireGeminiHeaders(t, requests[0].header, "tok-123")

	var envelope map[string]any
	if err := json.Unmarshal(requests[0].body, &envelope); err != nil {
		t.Fatalf("generateContent body is not JSON: %v (%s)", err, requests[0].body)
	}
	if got := envelope["model"]; got != "gemini-2.5-pro" {
		t.Errorf("body model = %v, want %q", got, "gemini-2.5-pro")
	}
	if got := envelope["project"]; got != "proj-wire" {
		t.Errorf("body project = %v, want %q (discovered by loadCodeAssist)", got, "proj-wire")
	}
	promptID, _ := envelope["user_prompt_id"].(string)
	if _, err := uuid.Parse(promptID); err != nil {
		t.Errorf("body user_prompt_id = %q, want a fresh UUID per call: %v", promptID, err)
	}

	request, ok := envelope["request"].(map[string]any)
	if !ok {
		t.Fatalf("body has no request object: %s", requests[0].body)
	}
	if got := request["session_id"]; got != "sess-1" {
		t.Errorf("request.session_id = %v, want the session id passed in options", got)
	}
	contents, ok := request["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("request.contents = %v, want one user content", request["contents"])
	}
	first, _ := contents[0].(map[string]any)
	if got := first["role"]; got != "user" {
		t.Errorf("contents[0].role = %v, want %q", got, "user")
	}
	parts, _ := first["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("contents[0].parts = %v, want one text part", first["parts"])
	}
	part, _ := parts[0].(map[string]any)
	if got := part["text"]; got != "Hi" {
		t.Errorf("contents[0].parts[0].text = %v, want %q", got, "Hi")
	}
}

// TestGeminiChatDefaultModel pins the empty-model fallback: the envelope is
// built with geminiCodeAssistDefaultModel ("gemini-pro-latest"), the same model
// jcode's DEFAULT_MODEL resolves to.
func TestGeminiChatDefaultModel(t *testing.T) {
	setupGeminiTestEnv(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj")
	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		switch req.path {
		case "/v1internal:loadCodeAssist":
			// C1: the handshake always runs; the env project is the fallback
			// when the response names none.
			return http.StatusOK, `{"currentTier":{"id":"free-tier"}}`
		case "/v1internal:generateContent":
			return http.StatusOK, `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}}`
		default:
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
	})

	p := newGeminiTestProvider(t, stub.Server, "tok")
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "Hi"}}, nil, "", nil); err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	requests := stub.requestsFor("/v1internal:generateContent")
	if len(requests) != 1 {
		t.Fatalf("generateContent requests = %d, want 1", len(requests))
	}
	var envelope map[string]any
	if err := json.Unmarshal(requests[0].body, &envelope); err != nil {
		t.Fatalf("generateContent body is not JSON: %v", err)
	}
	if got := envelope["model"]; got != geminiCodeAssistDefaultModel {
		t.Errorf("body model = %v, want %q", got, geminiCodeAssistDefaultModel)
	}
	if got := envelope["project"]; got != "env-proj" {
		t.Errorf("body project = %v, want %q (env override)", got, "env-proj")
	}
}

// TestGeminiChatUsesStaticTokenWhenNoTokenSource pins the middle link of the
// constructor's token chain (C2): NewGeminiCodeAssistProvider(seed, nil) must
// authenticate with the seed token itself and NEVER fall through to
// createGeminiTokenSource().
//
// The credential store is deliberately primed with an EXPIRED credential and
// the token endpoint with a hit counter, so the two behaviours are
// distinguishable: if the provider wrongly fell through to the coordinator, the
// request would carry "fresh-access" (after exactly one token-endpoint hit)
// instead of the seed.
func TestGeminiChatUsesStaticTokenWhenNoTokenSource(t *testing.T) {
	setupGeminiTestEnv(t)
	tokenSrv := newGeminiTokenServer(t)
	pointGeminiOAuthConfigAt(t, tokenSrv.Server)
	if err := auth.SetCredential("gemini", storedGeminiCredential("old-refresh")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		switch req.path {
		case "/v1internal:loadCodeAssist":
			return http.StatusOK, `{"currentTier":{"id":"free-tier"},"cloudaicompanionProject":"proj-static"}`
		case "/v1internal:generateContent":
			return http.StatusOK, `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}}`
		default:
			t.Errorf("unexpected request to %q", req.path)
			return http.StatusInternalServerError, `{}`
		}
	})

	p := NewGeminiCodeAssistProvider("static-seed", nil)
	p.baseURL = stub.URL

	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "Hi"}}, nil, "gemini-pro-latest", nil); err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	if got := tokenSrv.HitCount(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (the static seed needs no refresh)", got)
	}
	requests := stub.requestsFor("/v1internal:generateContent")
	if len(requests) != 1 {
		t.Fatalf("generateContent requests = %d, want 1", len(requests))
	}
	requireGeminiHeaders(t, requests[0].header, "static-seed")
}

// TestGeminiChatReportsHTTPError requires a non-2xx Code Assist response to
// surface as an error carrying the status code and (truncated) body.
func TestGeminiChatReportsHTTPError(t *testing.T) {
	setupGeminiTestEnv(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-proj")
	// C1: the handshake runs first, so it must succeed for the turn to reach
	// generateContent; only generateContent returns the 429 under test.
	stub := newGeminiCodeAssistStub(t, func(req capturedCodeAssistRequest) (int, string) {
		if req.path == "/v1internal:loadCodeAssist" {
			return http.StatusOK, `{"currentTier":{"id":"free-tier"}}`
		}
		return http.StatusTooManyRequests, `{"error":{"code":429,"message":"RATE_LIMIT_EXCEEDED"}}`
	})

	p := newGeminiTestProvider(t, stub.Server, "tok")
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "Hi"}}, nil, "gemini-pro-latest", nil)
	if err == nil {
		t.Fatal("expected an error for HTTP 429, got nil")
	}
	for _, want := range []string{"generateContent", "429", "RATE_LIMIT_EXCEEDED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}
}

// TestGeminiChatPropagatesTokenSourceError requires an authentication failure
// to abort the turn before any request is made.
func TestGeminiChatPropagatesTokenSourceError(t *testing.T) {
	setupGeminiTestEnv(t)
	stub := newGeminiCodeAssistStub(t, nil)

	p := NewGeminiCodeAssistProvider("seed", func() (string, error) {
		return "", errGeminiTestAuth
	})
	p.baseURL = stub.URL

	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "Hi"}}, nil, "gemini-pro-latest", nil)
	if err == nil {
		t.Fatal("expected the token source error to surface, got nil")
	}
	if !strings.Contains(err.Error(), errGeminiTestAuth.Error()) {
		t.Errorf("error %q must wrap the token source error %q", err.Error(), errGeminiTestAuth)
	}
	if got := len(stub.requests); got != 0 {
		t.Errorf("stub received %d request(s), want 0 after an auth failure", got)
	}
}

// -----------------------------------------------------------------------------
// Small units
// -----------------------------------------------------------------------------

var errGeminiTestAuth = errors.New("gemini test auth failure")

// TestGeminiGetDefaultModel pins the default-model accessor, including the
// empty-field fallback.
func TestGeminiGetDefaultModel(t *testing.T) {
	if got := (&GeminiCodeAssistProvider{}).GetDefaultModel(); got != geminiCodeAssistDefaultModel {
		t.Errorf("zero-value GetDefaultModel() = %q, want %q", got, geminiCodeAssistDefaultModel)
	}
	if got := (&GeminiCodeAssistProvider{defaultModel: "gemini-2.5-flash"}).GetDefaultModel(); got != "gemini-2.5-flash" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "gemini-2.5-flash")
	}
}

// TestGeminiCodeAssistUserAgent pins jcode's `gemini_user_agent()` (spec §5):
// the UA is built from the DEFAULT model (never the requested one) and the
// OS/arch names are the ones Rust's std::env::consts emits, which is what the
// official Gemini CLI sends. It is load-bearing: jcode measured 429
// RATE_LIMIT_EXCEEDED with a custom UA and no throttling with this identity.
func TestGeminiCodeAssistUserAgent(t *testing.T) {
	osCases := map[string]string{
		"linux":   "linux",
		"darwin":  "macos", // Rust's std::env::consts::OS calls it "macos"
		"macos":   "macos",
		"windows": "windows",
		"plan9":   "plan9", // unknown values pass through, like jcode's `other` arm
	}
	for in, want := range osCases {
		if got := geminiUserAgentOS(in); got != want {
			t.Errorf("geminiUserAgentOS(%q) = %q, want %q", in, got, want)
		}
	}

	archCases := map[string]string{
		"amd64":   "x64",
		"x86_64":  "x64",
		"arm64":   "arm64",
		"aarch64": "arm64",
		"386":     "x86",
		"riscv64": "riscv64",
	}
	for in, want := range archCases {
		if got := geminiUserAgentArch(in); got != want {
			t.Errorf("geminiUserAgentArch(%q) = %q, want %q", in, got, want)
		}
	}

	want := "GeminiCLI-tui/0.57.0/" + geminiCodeAssistDefaultModel + " (linux; x64; terminal)"
	got := "GeminiCLI-tui/0.57.0/" + geminiCodeAssistDefaultModel + " (" + geminiUserAgentOS("linux") + "; " + geminiUserAgentArch("amd64") + "; terminal)"
	if got != want {
		t.Fatalf("composed UA = %q, want %q", got, want)
	}
	// The live UA must follow the same shape on this machine.
	if ua := geminiCodeAssistUserAgent(); !strings.HasPrefix(ua, "GeminiCLI-tui/0.57.0/"+geminiCodeAssistDefaultModel+" (") || !strings.HasSuffix(ua, "; terminal)") {
		t.Errorf("geminiCodeAssistUserAgent() = %q, want the GeminiCLI-tui/0.57.0/<default-model> (os; arch; terminal) shape", ua)
	}
}

// TestGeminiSessionIDFromOptions pins the option plumbing: a string session id
// is passed through (trimmed), anything else yields "".
func TestGeminiSessionIDFromOptions(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]any
		want    string
	}{
		{name: "nil options", options: nil, want: ""},
		{name: "missing key", options: map[string]any{"max_tokens": 1}, want: ""},
		{name: "string value", options: map[string]any{"session_id": "sess"}, want: "sess"},
		{name: "trimmed", options: map[string]any{"session_id": "  sess  "}, want: "sess"},
		{name: "wrong type", options: map[string]any{"session_id": 42}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := geminiSessionIDFromOptions(tc.options); got != tc.want {
				t.Errorf("geminiSessionIDFromOptions(%v) = %q, want %q", tc.options, got, tc.want)
			}
		})
	}
}
