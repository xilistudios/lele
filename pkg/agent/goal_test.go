package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

func TestGoalManager_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	gm := NewGoalManager(dir)

	goal := gm.Set("session1", "Fix all lint errors", 10)
	if goal == nil {
		t.Fatal("Set returned nil")
	}
	if goal.Text != "Fix all lint errors" {
		t.Errorf("Text = %q, want %q", goal.Text, "Fix all lint errors")
	}
	if goal.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, want 10", goal.MaxTurns)
	}
	if goal.Status != GoalActive {
		t.Errorf("Status = %q, want %q", goal.Status, GoalActive)
	}

	got := gm.Get("session1")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Text != "Fix all lint errors" {
		t.Errorf("Get Text = %q, want %q", got.Text, "Fix all lint errors")
	}
}

func TestGoalManager_DefaultMaxTurns(t *testing.T) {
	gm := NewGoalManager("")
	goal := gm.Set("s1", "test", 0)
	if goal.MaxTurns != DefaultGoalMaxTurns {
		t.Errorf("MaxTurns = %d, want default %d", goal.MaxTurns, DefaultGoalMaxTurns)
	}
}

func TestGoalManager_PauseResume(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test goal", 5)

	if !gm.IsActive("s1") {
		t.Fatal("expected active after Set")
	}

	if !gm.Pause("s1") {
		t.Fatal("Pause returned false")
	}
	if gm.IsActive("s1") {
		t.Fatal("expected not active after Pause")
	}

	if !gm.Resume("s1") {
		t.Fatal("Resume returned false")
	}
	if !gm.IsActive("s1") {
		t.Fatal("expected active after Resume")
	}
}

func TestGoalManager_Clear(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test goal", 5)

	if !gm.Clear("s1") {
		t.Fatal("Clear returned false")
	}
	if gm.Get("s1") != nil {
		t.Fatal("expected nil after Clear")
	}
	if gm.Clear("s1") {
		t.Fatal("second Clear should return false")
	}
}

func TestGoalManager_IncrementTurn_BudgetExhaustion(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test", 3)

	if exhausted := gm.IncrementTurn("s1"); exhausted {
		t.Fatal("turn 1 should not exhaust budget of 3")
	}
	if exhausted := gm.IncrementTurn("s1"); exhausted {
		t.Fatal("turn 2 should not exhaust budget of 3")
	}
	if exhausted := gm.IncrementTurn("s1"); !exhausted {
		t.Fatal("turn 3 should exhaust budget of 3")
	}

	goal := gm.Get("s1")
	if goal.Status != GoalBlocked {
		t.Errorf("Status = %q, want %q", goal.Status, GoalBlocked)
	}
}

func TestGoalManager_MarkDone(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test", 10)
	gm.IncrementTurn("s1")
	gm.MarkDone("s1")

	goal := gm.Get("s1")
	if goal.Status != GoalDone {
		t.Errorf("Status = %q, want %q", goal.Status, GoalDone)
	}
	if gm.IsActive("s1") {
		t.Fatal("done goal should not be active")
	}
}

func TestGoalManager_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Create and persist a goal
	gm1 := NewGoalManager(dir)
	gm1.Set("telegram:12345", "Build the feature", 15)

	// Load from disk in a new manager
	gm2 := NewGoalManager(dir)
	goal := gm2.Get("telegram:12345")
	if goal == nil {
		t.Fatal("goal not loaded from disk")
	}
	if goal.Text != "Build the feature" {
		t.Errorf("Text = %q, want %q", goal.Text, "Build the feature")
	}
	if goal.MaxTurns != 15 {
		t.Errorf("MaxTurns = %d, want 15", goal.MaxTurns)
	}
}

func TestGoalManager_PersistenceClearRemovesFile(t *testing.T) {
	dir := t.TempDir()
	gm := NewGoalManager(dir)
	gm.Set("sess1", "goal", 5)

	// Verify file exists
	files, _ := os.ReadDir(dir)
	if len(files) == 0 {
		t.Fatal("expected persisted file")
	}

	gm.Clear("sess1")

	// Verify file removed
	files, _ = os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatalf("expected no files after clear, got %d", len(files))
	}
}

func TestGoalManager_DoneGoalsNotRestored(t *testing.T) {
	dir := t.TempDir()
	gm1 := NewGoalManager(dir)
	gm1.Set("s1", "done goal", 5)
	gm1.MarkDone("s1")

	// Load from disk - done goals should not be restored
	gm2 := NewGoalManager(dir)
	if gm2.Get("s1") != nil {
		t.Fatal("done goal should not be restored from disk")
	}
}

func TestGoalManager_FormatStatus(t *testing.T) {
	gm := NewGoalManager("")
	goal := gm.Set("s1", "Fix tests", 20)
	status := goal.FormatStatus()
	if status == "" {
		t.Fatal("FormatStatus returned empty string")
	}
	if !goalContains(status, "Fix tests") {
		t.Errorf("status should contain goal text, got: %s", status)
	}
	if !goalContains(status, "active") {
		t.Errorf("status should contain 'active', got: %s", status)
	}
}

func TestContainsDone(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"DONE", true},
		{"done", true},
		{"DONE.", true},
		{"CONTINUE", false},
		{"NOT DONE", false},
		{"I think it's done", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := containsDone(tt.input); got != tt.want {
			t.Errorf("containsDone(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"telegram:12345", "telegram_12345"},
		{"native:abc-def", "native_abc-def"},
		{"simple", "simple"},
		{"a/b\\c:d", "a_b_c_d"},
	}
	for _, tt := range tests {
		if got := sanitizeFileName(tt.input); got != tt.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// mockGoalJudge is a test judge that always returns a fixed result.
type mockGoalJudge struct {
	done bool
}

func (m *mockGoalJudge) JudgeGoal(_ context.Context, _, _, _ string) (bool, string, error) {
	if m.done {
		return true, "DONE", nil
	}
	return false, "CONTINUE", nil
}

func TestGoalManager_SetJudge(t *testing.T) {
	gm := NewGoalManager("")
	judge := &mockGoalJudge{done: true}
	gm.SetJudge(judge)

	if gm.judge == nil {
		t.Fatal("judge not set")
	}

	isDone, answer, err := gm.judge.JudgeGoal(context.Background(), "key", "test", "response")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if !isDone {
		t.Error("expected isDone=true")
	}
	if answer != "DONE" {
		t.Errorf("answer = %q, want DONE", answer)
	}
}

func TestGoalManager_MultipleSessions(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "goal one", 10)
	gm.Set("s2", "goal two", 20)

	g1 := gm.Get("s1")
	g2 := gm.Get("s2")
	if g1.Text != "goal one" || g2.Text != "goal two" {
		t.Error("sessions should have independent goals")
	}

	gm.Clear("s1")
	if gm.Get("s1") != nil {
		t.Error("s1 should be cleared")
	}
	if gm.Get("s2") == nil {
		t.Error("s2 should still exist")
	}
}

func TestGoalManager_PersistDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "goals")
	gm := NewGoalManager(dir)
	gm.Set("s1", "nested goal", 5)

	// Verify nested dir was created
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("nested goals dir was not created")
	}
}

func goalContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && goalContainsStr(s, substr))
}

func goalContainsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mockSummaryProvider returns a fixed summary for testing.
type mockSummaryProvider struct {
	summary string
}

func (m *mockSummaryProvider) GetSummary(_ string) string {
	return m.summary
}

func TestSummaryGoalJudge_FetchesSummaryAndEvaluates(t *testing.T) {
	provider := &mockProvider{mockResponse: "DONE"}
	summary := &mockSummaryProvider{summary: "The agent refactored the auth module and added tests."}
	judge := NewSummaryGoalJudge(provider, "mock-model", summary, nil)

	isDone, answer, err := judge.JudgeGoal(context.Background(), "skey", "Refactor auth module", "Added tests and verified build.")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if !isDone {
		t.Error("expected isDone=true when provider returns DONE")
	}
	if answer != "DONE" {
		t.Errorf("answer = %q, want DONE", answer)
	}
}

func TestSummaryGoalJudge_Continue(t *testing.T) {
	provider := &mockProvider{mockResponse: "CONTINUE"}
	judge := NewSummaryGoalJudge(provider, "mock-model", &mockSummaryProvider{summary: "Started the refactor, not finished."}, nil)

	isDone, _, err := judge.JudgeGoal(context.Background(), "skey", "Refactor auth module", "Began work.")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if isDone {
		t.Error("expected isDone=false when provider returns CONTINUE")
	}
}

func TestSummaryGoalJudge_NoProviderError(t *testing.T) {
	judge := NewSummaryGoalJudge(nil, "mock-model", &mockSummaryProvider{summary: "x"}, nil)
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "resp")
	if err == nil {
		t.Fatal("expected error when no provider configured")
	}
}

func TestSummaryGoalJudge_ProviderError(t *testing.T) {
	provider := &mockProvider{shouldError: true}
	judge := NewSummaryGoalJudge(provider, "mock-model", &mockSummaryProvider{summary: "x"}, nil)
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "resp")
	if err == nil {
		t.Fatal("expected error when provider returns an error")
	}
}

func TestSummaryGoalJudge_EmptyResponseIsError(t *testing.T) {
	provider := &mockProvider{returnEmpty: true}
	judge := NewSummaryGoalJudge(provider, "mock-model", &mockSummaryProvider{summary: "s"}, nil)

	isDone, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "resp")
	if err == nil {
		t.Fatal("expected error when provider returns an empty response")
	}
	if isDone {
		t.Error("expected isDone=false when judge returns an error")
	}
}

// capturingJudgeProvider records the options passed to Chat so tests can
// assert the judge's LLM call configuration.
type capturingJudgeProvider struct {
	gotOpts map[string]interface{}
}

func (c *capturingJudgeProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	c.gotOpts = opts
	return &providers.LLMResponse{Content: "DONE", ToolCalls: []providers.ToolCall{}}, nil
}

func (c *capturingJudgeProvider) GetDefaultModel() string { return "mock-model" }

func TestSummaryGoalJudge_DisablesReasoningAndUsesHeadroom(t *testing.T) {
	provider := &capturingJudgeProvider{}
	judge := NewSummaryGoalJudge(provider, "mock-model", &mockSummaryProvider{summary: "s"}, nil)

	isDone, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "resp")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if !isDone {
		t.Error("expected isDone=true when provider returns DONE")
	}

	if provider.gotOpts == nil {
		t.Fatal("expected options to be passed to Chat")
	}

	maxTokens, ok := provider.gotOpts["max_tokens"].(int)
	if !ok {
		t.Fatalf("max_tokens is not an int: %T", provider.gotOpts["max_tokens"])
	}
	if maxTokens < 100 {
		t.Errorf("max_tokens = %d, want >= 100 for reasoning headroom", maxTokens)
	}

	reasoning, ok := provider.gotOpts["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("reasoning is not a map[string]interface{}: %T", provider.gotOpts["reasoning"])
	}
	if enabled, ok := reasoning["enabled"].(bool); !ok || enabled {
		t.Errorf("reasoning.enabled = %v, want false", reasoning["enabled"])
	}
}

// recordingProvider records the model passed to Chat and returns a fixed
// response, so tests can assert which model the judge used.
type recordingProvider struct {
	mockResponse string
	lastModel    string
}

func (m *recordingProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	m.lastModel = model
	response := "Mock response"
	if m.mockResponse != "" {
		response = m.mockResponse
	}
	return &providers.LLMResponse{
		Content:   response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *recordingProvider) GetDefaultModel() string {
	return "mock-model"
}

func TestSummaryGoalJudge_UsesSessionModelResolver(t *testing.T) {
	provider := &recordingProvider{mockResponse: "DONE"}
	summary := &mockSummaryProvider{summary: "The agent finished the work."}
	var usedModel string
	resolver := func(sessionKey string) string {
		usedModel = "session-model-x"
		return "session-model-x"
	}

	judge := NewSummaryGoalJudge(provider, "fallback-model", summary, resolver)
	done, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "response")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if !done {
		t.Error("expected isDone=true when provider returns DONE")
	}
	if usedModel != "session-model-x" {
		t.Fatalf("resolver not called: usedModel = %q, want session-model-x", usedModel)
	}
	if provider.lastModel != "session-model-x" {
		t.Fatalf("expected session model, got %q", provider.lastModel)
	}
}

func TestSummaryGoalJudge_FallbackModelWhenNilResolver(t *testing.T) {
	provider := &recordingProvider{mockResponse: "DONE"}
	summary := &mockSummaryProvider{summary: "The agent finished the work."}

	judge := NewSummaryGoalJudge(provider, "fallback-model", summary, nil)
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "response")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if provider.lastModel != "fallback-model" {
		t.Fatalf("expected fallback model, got %q", provider.lastModel)
	}
}

func TestSummaryGoalJudge_FallbackModelWhenResolverEmpty(t *testing.T) {
	provider := &recordingProvider{mockResponse: "DONE"}
	summary := &mockSummaryProvider{summary: "The agent finished the work."}
	resolver := func(sessionKey string) string { return "" }

	judge := NewSummaryGoalJudge(provider, "fallback-model", summary, resolver)
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "response")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if provider.lastModel != "fallback-model" {
		t.Fatalf("expected fallback model when resolver returns empty, got %q", provider.lastModel)
	}
}

func TestSummaryGoalJudge_StripsProviderPrefix(t *testing.T) {
	provider := &recordingProvider{mockResponse: "DONE"}
	summary := &mockSummaryProvider{summary: "The agent finished the work."}
	resolver := func(sessionKey string) string { return "llmproxy:mimo-v2.5-pro" }

	judge := NewSummaryGoalJudge(provider, "fallback-model", summary, resolver)
	// Config left nil: provider-routing is skipped, but the prefix is still
	// stripped before the API call.
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "response")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if provider.lastModel != "mimo-v2.5-pro" {
		t.Fatalf("expected stripped model, got %q", provider.lastModel)
	}
}

func TestSummaryGoalJudge_StripsProviderPrefixOnFallback(t *testing.T) {
	provider := &recordingProvider{mockResponse: "DONE"}
	summary := &mockSummaryProvider{summary: "The agent finished the work."}

	judge := NewSummaryGoalJudge(provider, "llmproxy:mimo-v2.5-pro", summary, nil)
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "response")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if provider.lastModel != "mimo-v2.5-pro" {
		t.Fatalf("expected stripped fallback model, got %q", provider.lastModel)
	}
}

func TestBuildGoalJudgePrompt(t *testing.T) {
	prompt := buildGoalJudgePrompt("Fix the bug", "The agent reproduced the bug and identified the cause.", "Applied the fix.")
	for _, want := range []string{"GOAL: Fix the bug", "CONVERSATION SUMMARY", "The agent reproduced the bug", "APPLIED", "Reply DONE or CONTINUE"} {
		if !goalContains(strings.ToUpper(prompt), strings.ToUpper(want)) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildGoalJudgePrompt_NoSummary(t *testing.T) {
	prompt := buildGoalJudgePrompt("Fix the bug", "", "Applied the fix.")
	if !goalContains(prompt, "No conversation summary available yet.") {
		t.Errorf("prompt should note missing summary:\n%s", prompt)
	}
}

func TestBuildGoalJudgePrompt_TruncatesLongResponse(t *testing.T) {
	longResp := strings.Repeat("x", 5000)
	prompt := buildGoalJudgePrompt("Goal", "summary", longResp)
	if goalContains(prompt, strings.Repeat("x", 4001)) {
		t.Error("prompt should truncate long responses")
	}
	if !goalContains(prompt, "[truncated]") {
		t.Error("prompt should mark truncated response")
	}
}

func TestBuildSubagentJudgePrompt(t *testing.T) {
	prompt := buildSubagentJudgePrompt("Fix the bug", "The agent reproduced the bug and identified the cause.", "Applied the fix.")
	for _, want := range []string{
		"GOAL: Fix the bug",
		"CONVERSATION SUMMARY",
		"The agent reproduced the bug",
		"APPLIED",
		"CONTINUE:",
		"DONE",
	} {
		if !goalContains(strings.ToUpper(prompt), strings.ToUpper(want)) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildSubagentJudgePrompt_NoSummary(t *testing.T) {
	prompt := buildSubagentJudgePrompt("Fix the bug", "", "Applied the fix.")
	if !goalContains(prompt, "No conversation summary available yet.") {
		t.Errorf("prompt should note missing summary:\n%s", prompt)
	}
}

func TestExtractContinuationGuidance(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   string
	}{
		{"continue with colon", "CONTINUE: fix the auth bug", "fix the auth bug"},
		{"continue lowercase with spaces", "continue:  check the tests", "check the tests"},
		{"done", "DONE", ""},
		{"continue no colon", "CONTINUE", ""},
		{"empty", "", ""},
		{"not done", "NOT DONE", ""},
		{"continue with space before colon", "CONTINUE : review the code", "review the code"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractContinuationGuidance(tt.answer); got != tt.want {
				t.Errorf("extractContinuationGuidance(%q) = %q, want %q", tt.answer, got, tt.want)
			}
		})
	}
}

// mockSubagentRunner simulates a SubagentManager for the subagent goal judge.
type mockSubagentRunner struct {
	mu       sync.Mutex
	tasks    map[string]*tools.SubagentTask
	result   string
	status   string
	spawnErr error
}

func newMockSubagentRunner() *mockSubagentRunner {
	return &mockSubagentRunner{
		tasks:  make(map[string]*tools.SubagentTask),
		status: tools.SubagentStatusCompleted,
	}
}

func (m *mockSubagentRunner) SpawnWithOptions(_ context.Context, task, label, agentID, originChannel, originChatID string, _ tools.AsyncCallback, opts tools.SpawnOptions) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.spawnErr != nil {
		return "", m.spawnErr
	}
	id := fmt.Sprintf("goalsub-%d", len(m.tasks)+1)
	t := &tools.SubagentTask{
		ID:            id,
		Task:          task,
		Label:         label,
		AgentID:       agentID,
		OriginChannel: originChannel,
		OriginChatID:  originChatID,
		Status:        m.status,
		Result:        m.result,
		MaxRetries:    opts.MaxRetries,
	}
	t.InitDoneChannel()
	// Signal completion immediately.
	t.SignalDone()
	m.tasks[id] = t
	return id, nil
}

func (m *mockSubagentRunner) GetTask(taskID string) (*tools.SubagentTask, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[taskID]
	return t, ok
}

func TestSubagentGoalJudge_Done(t *testing.T) {
	runner := newMockSubagentRunner()
	runner.result = "DONE"
	summary := &mockSummaryProvider{summary: "The agent finished the refactor and all tests pass."}
	judge := NewSubagentGoalJudge(runner, summary, "", "goal", "skey", 5*time.Second)

	isDone, answer, err := judge.JudgeGoal(context.Background(), "skey", "Refactor auth module", "Done, tests pass.")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if !isDone {
		t.Error("expected isDone=true when subagent returns DONE")
	}
	if answer != "DONE" {
		t.Errorf("answer = %q, want DONE", answer)
	}
}

func TestSubagentGoalJudge_Continue(t *testing.T) {
	runner := newMockSubagentRunner()
	runner.result = "CONTINUE"
	judge := NewSubagentGoalJudge(runner, &mockSummaryProvider{summary: "Started work."}, "", "goal", "skey", 5*time.Second)

	isDone, _, err := judge.JudgeGoal(context.Background(), "skey", "Refactor auth module", "Began work.")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if isDone {
		t.Error("expected isDone=false when subagent returns CONTINUE")
	}
}

func TestSubagentGoalJudge_NoRunner(t *testing.T) {
	judge := NewSubagentGoalJudge(nil, &mockSummaryProvider{summary: "x"}, "", "goal", "skey", 5*time.Second)
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "resp")
	if err == nil {
		t.Fatal("expected error when no runner configured")
	}
}

func TestSubagentGoalJudge_SpawnError(t *testing.T) {
	runner := newMockSubagentRunner()
	runner.spawnErr = fmt.Errorf("maximum concurrent subagents reached")
	judge := NewSubagentGoalJudge(runner, &mockSummaryProvider{summary: "x"}, "", "goal", "skey", 5*time.Second)
	_, _, err := judge.JudgeGoal(context.Background(), "skey", "goal", "resp")
	if err == nil {
		t.Fatal("expected error when spawn fails")
	}
}
