package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/cron"
)

// mockJobExecutor is a fake JobExecutor used to verify the cron tool's
// agent-processing code paths without spinning up a real agent.
type mockJobExecutor struct {
	response string
	err      error
	calls    int
	lastKey  string
	lastChan string
	lastChat string
}

func (m *mockJobExecutor) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	m.calls++
	m.lastKey = sessionKey
	m.lastChan = channel
	m.lastChat = chatID
	return m.response, m.err
}

// newCronTestFixture builds a CronTool backed by a real CronService, a real
// MessageBus, and a configurable mock executor. Its cleanup stops the service.
func newCronTestFixture(t *testing.T, executor *mockJobExecutor) (*CronTool, *bus.MessageBus, *cron.CronService) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := cron.NewCronService(storePath, nil)
	t.Cleanup(cs.Stop)

	mb := bus.NewMessageBus()
	if executor == nil {
		executor = &mockJobExecutor{response: "agent response"}
	}

	tool := NewCronTool(cs, executor, mb, t.TempDir(), false, 5*time.Second, nil)
	return tool, mb, cs
}

// consumeOutbound reads a single outbound message with a timeout.
func consumeOutbound(t *testing.T, mb *bus.MessageBus) bus.OutboundMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, ok := mb.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("timed out waiting for outbound message")
	}
	return msg
}

func TestCronTool_Identity(t *testing.T) {
	tool, _, _ := newCronTestFixture(t, nil)

	if tool.Name() != "cron" {
		t.Errorf("Name() = %q, want 'cron'", tool.Name())
	}
	if !strings.Contains(tool.Description(), "Schedule reminders") {
		t.Errorf("Description() = %q, want scheduling hint", tool.Description())
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters().type = %v, want 'object'", params["type"])
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("Parameters().properties not a map: %T", params["properties"])
	}
	for _, name := range []string{"action", "message", "command", "at_seconds", "every_seconds", "cron_expr", "job_id", "deliver", "scope", "session_key", "spawn"} {
		if _, ok := props[name]; !ok {
			t.Errorf("Parameter %q missing", name)
		}
	}
	action := props["action"].(map[string]interface{})
	enum := action["enum"].([]string)
	if len(enum) != 5 {
		t.Errorf("expected 5 actions, got %v", enum)
	}
	spawn := props["spawn"].(map[string]interface{})
	if spawn["required"] == nil {
		t.Error("spawn should require 'task'")
	}
}

func TestCronTool_ContextSetters(t *testing.T) {
	tool, _, _ := newCronTestFixture(t, nil)

	tool.SetContext("tg", "chat-1")
	if tool.channel != "tg" || tool.chatID != "chat-1" {
		t.Errorf("SetContext: channel=%q chatID=%q", tool.channel, tool.chatID)
	}

	tool.SetSessionContext("web", "chat-2", "sk-123")
	if tool.channel != "web" || tool.chatID != "chat-2" || tool.sessionKey != "sk-123" {
		t.Errorf("SetSessionContext: channel=%q chatID=%q sessionKey=%q", tool.channel, tool.chatID, tool.sessionKey)
	}
}

func TestCronTool_Execute(t *testing.T) {
	tool, _, _ := newCronTestFixture(t, nil)

	// Missing action.
	res := tool.Execute(context.Background(), map[string]interface{}{})
	if !res.IsError || !strings.Contains(res.ForLLM, "action is required") {
		t.Errorf("missing action: %+v", res)
	}

	// Unknown action.
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "bogus"})
	if !res.IsError || !strings.Contains(res.ForLLM, "unknown action") {
		t.Errorf("unknown action: %+v", res)
	}
}

func TestCronTool_AddJob(t *testing.T) {
	tool, _, cs := newCronTestFixture(t, nil)
	tool.SetSessionContext("native", "chat-1", "sk-1")

	// at_seconds one-time
	res := tool.Execute(context.Background(), map[string]interface{}{
		"action":      "add",
		"message":     "Remind me",
		"at_seconds":  float64(600),
		"deliver":     true,
		"session_key": "sk-2",
	})
	if res.IsError {
		t.Fatalf("add at_seconds failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Cron job added") {
		t.Errorf("unexpected add result: %s", res.ForLLM)
	}
	if res.Silent != true {
		t.Errorf("add result should be silent")
	}

	// both every_seconds and a command
	res = tool.Execute(context.Background(), map[string]interface{}{
		"action":       "add",
		"message":      "Backup",
		"every_seconds": float64(3600),
		"command":      "echo hi",
	})
	if res.IsError {
		t.Fatalf("add every_seconds+command failed: %s", res.ForLLM)
	}

	// cron_expr
	res = tool.Execute(context.Background(), map[string]interface{}{
		"action":    "add",
		"message":   "Daily",
		"cron_expr": "0 9 * * *",
	})
	if res.IsError {
		t.Fatalf("add cron_expr failed: %s", res.ForLLM)
	}

	// spawn config
	res = tool.Execute(context.Background(), map[string]interface{}{
		"action":  "add",
		"message": "Spawn task",
		"every_seconds": float64(60),
		"spawn": map[string]interface{}{
			"task":     "do something",
			"label":    "lbl",
			"agent_id": "agent-1",
			"guidance": "be careful",
			"model":    "anthropic:claude-opus",
		},
	})
	if res.IsError {
		t.Fatalf("add spawn failed: %s", res.ForLLM)
	}
	if len(cs.ListJobs(true)) == 0 {
		t.Fatal("expected at least one job in cron service")
	}
	if got := len(cs.ListJobs(true)); got < 4 {
		t.Errorf("expected at least 4 jobs, got %d", got)
	}
}

func TestCronTool_AddJobErrors(t *testing.T) {
	tool, _, _ := newCronTestFixture(t, nil)

	// No session context (channel/chatID not set).
	res := tool.Execute(context.Background(), map[string]interface{}{
		"action":  "add",
		"message": "msg",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "no session context") {
		t.Errorf("expected no-session-context error got %+v", res)
	}

	// Context set but no message.
	tool.SetContext("native", "chat-1")
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "add"})
	if !res.IsError || !strings.Contains(res.ForLLM, "message is required") {
		t.Errorf("expected message-required error got %+v", res)
	}

	// No schedule specified.
	res = tool.Execute(context.Background(), map[string]interface{}{
		"action":  "add",
		"message": "msg",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "one of at_seconds") {
		t.Errorf("expected schedule-required error got %+v", res)
	}

	// Session scope but no session key.
	res = tool.Execute(context.Background(), map[string]interface{}{
		"action":       "add",
		"message":      "msg",
		"every_seconds": float64(60),
		"scope":        "session",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "session_key is required") {
		t.Errorf("expected session-key-required error got %+v", res)
	}
}

func TestCronTool_ListJobs(t *testing.T) {
	tool, _, cs := newCronTestFixture(t, nil)
	tool.SetSessionContext("native", "chat-1", "sk-1")

	// Empty list.
	res := tool.Execute(context.Background(), map[string]interface{}{"action": "list"})
	if res.IsError {
		t.Fatalf("list failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "No scheduled jobs") {
		t.Errorf("expected empty list message, got: %s", res.ForLLM)
	}

	// Add jobs with different schedule kinds.
	for _, tc := range []struct {
		message string
		args    map[string]interface{}
	}{
		{"every job", map[string]interface{}{"every_seconds": float64(3600)}},
		{"cron job", map[string]interface{}{"cron_expr": "0 9 * * *"}},
		{"at job", map[string]interface{}{"at_seconds": float64(600)}},
	} {
		args := map[string]interface{}{"action": "add", "message": tc.message}
		for k, v := range tc.args {
			args[k] = v
		}
		if r := tool.Execute(context.Background(), args); r.IsError {
			t.Fatalf("add %q failed: %s", tc.message, r.ForLLM)
		}
	}

	res = tool.Execute(context.Background(), map[string]interface{}{"action": "list"})
	if res.IsError {
		t.Fatalf("list failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "every job") || !strings.Contains(res.ForLLM, "cron job") || !strings.Contains(res.ForLLM, "at job") {
		t.Errorf("expected all jobs in list, got: %s", res.ForLLM)
	}

	// Add a session-scoped job to cover scope info line.
	tool.SetSessionContext("native", "chat-1", "sk-1")
	_ = tool.Execute(context.Background(), map[string]interface{}{
		"action":  "add",
		"message": "session job",
		"every_seconds": float64(60),
		"scope":   "session",
	})
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "list"})
	if !strings.Contains(res.ForLLM, "session:") {
		t.Errorf("expected session scope info in list, got: %s", res.ForLLM)
	}
	_ = cs
}

func TestCronTool_RemoveAndEnable(t *testing.T) {
	tool, _, _ := newCronTestFixture(t, nil)
	tool.SetSessionContext("native", "chat-1", "sk-1")

	// remove without job_id.
	res := tool.Execute(context.Background(), map[string]interface{}{"action": "remove"})
	if !res.IsError || !strings.Contains(res.ForLLM, "job_id is required") {
		t.Errorf("remove without job_id: %+v", res)
	}

	// add a job to manipulate.
	addRes := tool.Execute(context.Background(), map[string]interface{}{
		"action":  "add",
		"message": "to-remove",
		"every_seconds": float64(60),
	})
	if addRes.IsError {
		t.Fatalf("add failed: %s", addRes.ForLLM)
	}
	jobID := extractJobID(t, addRes.ForLLM)

	// disable.
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "disable", "job_id": jobID})
	if res.IsError {
		t.Fatalf("disable failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "disabled") {
		t.Errorf("expected disabled message, got: %s", res.ForLLM)
	}

	// enable.
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "enable", "job_id": jobID})
	if res.IsError {
		t.Fatalf("enable failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "enabled") {
		t.Errorf("expected enabled message, got: %s", res.ForLLM)
	}

	// enable non-existent job.
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "enable", "job_id": "nope"})
	if !res.IsError || !strings.Contains(res.ForLLM, "not found") {
		t.Errorf("enable missing job: %+v", res)
	}

	// remove.
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "remove", "job_id": jobID})
	if res.IsError || !strings.Contains(res.ForLLM, "removed") {
		t.Errorf("remove existing job: %+v", res)
	}

	// remove non-existent.
	res = tool.Execute(context.Background(), map[string]interface{}{"action": "remove", "job_id": jobID})
	if !res.IsError || !strings.Contains(res.ForLLM, "not found") {
		t.Errorf("remove missing job: %+v", res)
	}
}

// extractJobID pulls the id out of a "Cron job added: X (id: <id>...)" string.
func extractJobID(t *testing.T, s string) string {
	t.Helper()
	idx := strings.Index(s, "(id: ")
	if idx < 0 {
		t.Fatalf("no id in result: %s", s)
	}
	rest := s[idx+len("(id: "):]
	end := strings.Index(rest, ")")
	if end < 0 {
		t.Fatalf("no closing paren in result: %s", s)
	}
	// The id may be followed by ", scope: ...". Take only the first token.
	if comma := strings.Index(rest, ","); comma >= 0 && comma < end {
		end = comma
	}
	return rest[:end]
}

func TestCronTool_ExecuteJob_Command(t *testing.T) {
	tool, mb, cs := newCronTestFixture(t, nil)
	_ = cs

	job := &cron.CronJob{
		ID:    "job-1",
		Name:  "cmd",
		Scope: "global",
		Payload: cron.CronPayload{
			Channel: "native",
			To:      "chat-1",
			Command: "echo cron-out",
		},
	}

	out := tool.ExecuteJob(context.Background(), job)
	if out != "ok" {
		t.Fatalf("ExecuteJob(command) = %q, want 'ok'", out)
	}

	msg := consumeOutbound(t, mb)
	if msg.Channel != "native" || msg.ChatID != "chat-1" {
		t.Errorf("unexpected outbound msg: %+v", msg)
	}
	if !strings.Contains(msg.Content, "cron-out") {
		t.Errorf("expected command output in message, got: %s", msg.Content)
	}
}

func TestCronTool_ExecuteJob_Command_Error(t *testing.T) {
	tool, mb, _ := newCronTestFixture(t, nil)

	job := &cron.CronJob{
		ID:    "job-err",
		Name:  "cmd",
		Scope: "global",
		Payload: cron.CronPayload{
			Command: "ls /does/not/exist_xyz_12345",
		},
	}

	out := tool.ExecuteJob(context.Background(), job)
	if out != "ok" {
		t.Fatalf("ExecuteJob command error = %q, want 'ok' (error is published)", out)
	}

	msg := consumeOutbound(t, mb)
	if !strings.Contains(msg.Content, "Error executing scheduled command") {
		t.Errorf("expected error message, got: %s", msg.Content)
	}
	if msg.Channel != "native" {
		t.Errorf("expected channel default to native, got %q", msg.Channel)
	}
	if !strings.Contains(msg.ChatID, "cron-job-err") {
		t.Errorf("expected synthetic cron chatID, got %q", msg.ChatID)
	}
}

func TestCronTool_ExecuteJob_Deliver(t *testing.T) {
	tool, mb, _ := newCronTestFixture(t, nil)

	job := &cron.CronJob{
		ID:    "job-deliver",
		Name:  "notice",
		Scope: "global",
		Payload: cron.CronPayload{
			Channel: "tg",
			To:      "chat-9",
			Deliver: true,
			Message: "hello user",
		},
	}

	if out := tool.ExecuteJob(context.Background(), job); out != "ok" {
		t.Fatalf("ExecuteJob(deliver) = %q, want ok", out)
	}

	msg := consumeOutbound(t, mb)
	if msg.Channel != "tg" || msg.ChatID != "chat-9" {
		t.Errorf("unexpected deliver msg: %+v", msg)
	}
	if msg.Content != "hello user" {
		t.Errorf("deliver content = %q", msg.Content)
	}
}

func TestCronTool_ExecuteJob_AgentPath(t *testing.T) {
	executor := &mockJobExecutor{response: "agent result"}
	tool, mb, _ := newCronTestFixture(t, executor)

	job := &cron.CronJob{
		ID:    "job-agent",
		Name:  "complex",
		Scope: "global",
		Payload: cron.CronPayload{
			Deliver: false,
			Message: "do the complex thing",
		},
	}

	if out := tool.ExecuteJob(context.Background(), job); out != "ok" {
		t.Fatalf("ExecuteJob(agent) = %q, want ok", out)
	}
	if executor.calls != 1 {
		t.Errorf("expected 1 executor call, got %d", executor.calls)
	}
	if executor.lastKey != "cron-job-agent" {
		t.Errorf("expected synthetic global session key, got %q", executor.lastKey)
	}

	// The agent path (non-session) returns "ok" and publishes nothing itself
	// when not session-scoped. Verify no outbound is published quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, ok := mb.SubscribeOutbound(ctx); ok {
		t.Error("did not expect an outbound message for global agent path")
	}
}

func TestCronTool_ExecuteJob_AgentPath_SessionScoped(t *testing.T) {
	executor := &mockJobExecutor{response: "session result"}
	tool, mb, _ := newCronTestFixture(t, executor)

	job := &cron.CronJob{
		ID:    "job-sess",
		Name:  "sess",
		Scope: "session",
		Payload: cron.CronPayload{
			Deliver:    false,
			Message:    "session task",
			SessionKey: "sk-origin",
		},
	}

	if out := tool.ExecuteJob(context.Background(), job); out != "ok" {
		t.Fatalf("ExecuteJob(session agent) = %q, want ok", out)
	}
	if executor.lastKey != "sk-origin" {
		t.Errorf("expected originating session key, got %q", executor.lastKey)
	}

	// Should publish a completion notification via notifySession.
	msg := consumeOutbound(t, mb)
	if !strings.Contains(msg.Content, "✅") || !strings.Contains(msg.Content, "job-sess") {
		t.Errorf("expected completion notification, got: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "session result") {
		t.Errorf("expected session result in notification, got: %s", msg.Content)
	}
}

func TestCronTool_ExecuteJob_AgentPath_Error(t *testing.T) {
	executor := &mockJobExecutor{err: os.ErrPermission}
	tool, mb, _ := newCronTestFixture(t, executor)

	job := &cron.CronJob{
		ID:    "job-sess-err",
		Name:  "sess",
		Scope: "session",
		Payload: cron.CronPayload{
			Deliver:    false,
			Message:    "task",
			SessionKey: "sk-origin",
		},
	}

	out := tool.ExecuteJob(context.Background(), job)
	if !strings.Contains(out, "Error:") {
		t.Errorf("expected error in return, got %q", out)
	}

	msg := consumeOutbound(t, mb)
	if !strings.Contains(msg.Content, "❌") {
		t.Errorf("expected failure notification, got: %s", msg.Content)
	}
}

func TestCronTool_ExecuteJob_Command_SessionScoped(t *testing.T) {
	tool, mb, _ := newCronTestFixture(t, nil)

	job := &cron.CronJob{
		ID:    "job-cmd-sess",
		Name:  "cmd",
		Scope: "session",
		Payload: cron.CronPayload{
			Command:    "echo xx",
			SessionKey: "sk-origin",
		},
	}

	if out := tool.ExecuteJob(context.Background(), job); out != "ok" {
		t.Fatalf("ExecuteJob(session command) = %q, want ok", out)
	}

	// Two messages: command output + completion notification.
	msg1 := consumeOutbound(t, mb)
	if !strings.Contains(msg1.Content, "Scheduled command") {
		t.Errorf("expected scheduled command message, got: %s", msg1.Content)
	}
	msg2 := consumeOutbound(t, mb)
	if !strings.Contains(msg2.Content, "✅") {
		t.Errorf("expected completion notification, got: %s", msg2.Content)
	}
	_ = msg2
}

func TestCronTool_ExecuteJob_Spawn(t *testing.T) {
	executor := &mockJobExecutor{response: "spawn complete"}
	tool, mb, _ := newCronTestFixture(t, executor)

	job := &cron.CronJob{
		ID:    "job-spawn",
		Name:  "spawner",
		Scope: "global",
		Payload: cron.CronPayload{
			Message: "context msg",
			Spawn: &cron.SpawnConfig{
				Task:     "run analysis",
				Label:    "analysis",
				AgentID:  "agent-x",
				Guidance: "be thorough",
				Model:    "anthropic:claude-opus",
			},
		},
	}

	res := tool.ExecuteJob(context.Background(), job)
	if res != "spawn complete" {
		t.Fatalf("ExecuteJob(spawn) = %q, want 'spawn complete'", res)
	}
	if executor.calls != 1 {
		t.Errorf("expected 1 executor call, got %d", executor.calls)
	}
	if !strings.Contains(executor.lastKey, "cron-spawn-job-spawn") {
		t.Errorf("expected cron-spawn session key, got %q", executor.lastKey)
	}
	// Should have published a completion notification for... actually global.
	// Global spawn path returns response without notification. Just check none
	// pending.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, ok := mb.SubscribeOutbound(ctx); ok {
		t.Error("did not expect outbound for global spawn")
	}
}

func TestCronTool_ExecuteJob_Spawn_Error(t *testing.T) {
	executor := &mockJobExecutor{err: os.ErrPermission}
	tool, mb, _ := newCronTestFixture(t, executor)

	job := &cron.CronJob{
		ID:    "job-spawn-err",
		Name:  "spawner",
		Scope: "session",
		Payload: cron.CronPayload{
			SessionKey: "sk-origin",
			Spawn:      &cron.SpawnConfig{Task: "x"},
		},
	}

	res := tool.ExecuteJob(context.Background(), job)
	if !strings.Contains(res, "Error:") {
		t.Errorf("expected error, got %q", res)
	}
	msg := consumeOutbound(t, mb)
	if !strings.Contains(msg.Content, "❌") {
		t.Errorf("expected failure notification, got: %s", msg.Content)
	}
}

func TestCronTool_ExecuteJob_Spawn_SessionScoped_Success(t *testing.T) {
	executor := &mockJobExecutor{response: "done"}
	tool, mb, _ := newCronTestFixture(t, executor)

	job := &cron.CronJob{
		ID:    "job-spawn-ok",
		Name:  "spawner",
		Scope: "session",
		Payload: cron.CronPayload{
			SessionKey: "sk-origin",
			Spawn:      &cron.SpawnConfig{Task: "x"},
		},
	}

	if res := tool.ExecuteJob(context.Background(), job); res != "done" {
		t.Fatalf("ExecuteJob(spawn ok) = %q, want done", res)
	}
	msg := consumeOutbound(t, mb)
	if !strings.Contains(msg.Content, "✅") || !strings.Contains(msg.Content, "done") {
		t.Errorf("expected completion notification, got: %s", msg.Content)
	}
}

func TestFormatSystemSpawnMessage(t *testing.T) {
	// nil spawn -> message only.
	if got := formatSystemSpawnMessage(&cron.CronJob{Payload: cron.CronPayload{Message: "m"}}); got != "m" {
		t.Errorf("nil spawn = %q, want 'm'", got)
	}

	job := &cron.CronJob{
		Payload: cron.CronPayload{
			Message: "ctx message",
			Spawn: &cron.SpawnConfig{
				Task:     "the task",
				Label:    "the label",
				AgentID:  "the agent",
				Guidance: "the guidance",
				Model:    "the model",
			},
		},
	}
	got := formatSystemSpawnMessage(job)
	for _, want := range []string{"SYSTEM_SPAWN:", "TASK: the task", "LABEL: the label", "AGENT_ID: the agent", "GUIDANCE: the guidance", "MODEL: the model", "CONTEXT: ctx message"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatSystemSpawnMessage missing %q: %s", want, got)
		}
	}

	// When message == task, no CONTEXT line.
	job2 := &cron.CronJob{
		Payload: cron.CronPayload{
			Message: "same",
			Spawn:   &cron.SpawnConfig{Task: "same"},
		},
	}
	got2 := formatSystemSpawnMessage(job2)
	if strings.Contains(got2, "CONTEXT:") {
		t.Errorf("should not contain CONTEXT when message==task: %s", got2)
	}
	parts := strings.Split(got2, "\n")
	if len(parts) != 2 {
		t.Errorf("expected exactly SYSTEM_SPAWN + TASK, got %d lines: %s", len(parts), got2)
	}
}