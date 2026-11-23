package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

const attachmentRowSessionKey = "native:attachment-display-row"

const attachmentRowUserText = "plot the failures for me"

const attachmentRowAssistantText = "Rendering the chart now."

const attachmentRowToolContent = "wrote chart.png (1280x720)"

const attachmentRowCaption = "here is the chart"

const attachmentRowCallID = "call_chart"

func newAttachmentRowAgent(t *testing.T) (*AgentInstance, string) {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{APIKey: "test-key"},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	t.Cleanup(func() { _ = al.StopWithin(5 * time.Second) })

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent in the registry")
	}
	if agent.Sessions == nil {
		t.Fatal("the loop's default agent has no SessionManager")
	}

	dbPath := filepath.Join(tmpDir, "attachment-display-row.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	agent.Sessions.SetSessionRepo(s.Sessions())

	return agent, dbPath
}

func reloadAttachmentRowHistory(t *testing.T, dbPath, key string) []providers.Message {
	t.Helper()

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q) on reload: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })

	reloaded := session.NewSessionManager()
	reloaded.SetSessionRepo(s.Sessions())
	return reloaded.GetHistoryView(key)
}

func displayRowsOf(history []providers.Message) []providers.Message {
	rows := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		if strings.HasPrefix(msg.ToolCallID, "file:") {
			rows = append(rows, msg)
		}
	}
	return rows
}

func seedAttachmentRowHistory(t *testing.T, sessions *session.SessionManager, key, attachmentPath string) providers.MessageAttachment {
	t.Helper()

	sessions.AddMessage(key, "user", attachmentRowUserText)
	sessions.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: attachmentRowAssistantText,
		ToolCalls: []providers.ToolCall{{
			ID:        attachmentRowCallID,
			Type:      "function",
			Name:      "exec",
			Arguments: map[string]interface{}{"command": "plot-failures --out chart.png"},
			Function: &providers.FunctionCall{
				Name:      "exec",
				Arguments: `{"command":"plot-failures --out chart.png"}`,
			},
		}},
	})
	sessions.AddFullMessage(key, providers.Message{
		Role:       "tool",
		ToolCallID: attachmentRowCallID,
		Content:    attachmentRowToolContent,
	})

	attachment := providers.MessageAttachment{
		Name:     "chart.png",
		Path:     attachmentPath,
		MIMEType: "image/png",
		Kind:     "image",
	}
	sessions.AddAttachmentMessage(key, attachmentRowCaption, []providers.MessageAttachment{attachment})

	if err := sessions.Save(key); err != nil {
		t.Fatalf("Save(%q): %v", key, err)
	}
	return attachment
}

func TestAttachmentDisplayRowIsInHistoryButNotInProviderContext(t *testing.T) {
	agent, dbPath := newAttachmentRowAgent(t)
	attachmentPath := filepath.Join(filepath.Dir(dbPath), "attachments", "chart.png")
	attachment := seedAttachmentRowHistory(t, agent.Sessions, attachmentRowSessionKey, attachmentPath)

	history := agent.Sessions.GetHistoryView(attachmentRowSessionKey)
	if len(history) != 4 {
		t.Fatalf("seeded history len = %d, want 4 (user, assistant tool call, tool result, display row)", len(history))
	}

	rows := displayRowsOf(history)
	if len(rows) != 1 {
		t.Fatalf("display rows = %d, want exactly 1", len(rows))
	}
	display := rows[0]
	if display.Role != "tool" {
		t.Errorf("display row role = %q, want tool", display.Role)
	}
	if !display.ExcludeFromContext {
		t.Error("display row is not excluded from the provider context")
	}
	if len(display.ToolCalls) != 0 {
		t.Errorf("display row carries %d tool calls, want none", len(display.ToolCalls))
	}
	if len(display.Attachments) != 1 {
		t.Fatalf("display row attachments = %d, want 1", len(display.Attachments))
	}
	if got := display.Attachments[0]; got.Path != attachment.Path || got.Name != attachment.Name ||
		got.MIMEType != attachment.MIMEType || got.Kind != attachment.Kind {
		t.Errorf("display row attachment = %+v, want %+v", got, attachment)
	}

	reloaded := reloadAttachmentRowHistory(t, dbPath, attachmentRowSessionKey)
	if len(reloaded) != len(history) {
		t.Fatalf("reloaded history len = %d, want %d", len(reloaded), len(history))
	}
	reloadedRows := displayRowsOf(reloaded)
	if len(reloadedRows) != 1 {
		t.Fatalf("reloaded display rows = %d, want exactly 1", len(reloadedRows))
	}
	reloadedDisplay := reloadedRows[0]
	if reloadedDisplay.ToolCallID != display.ToolCallID {
		t.Errorf("reloaded display row tool_call_id = %q, want %q", reloadedDisplay.ToolCallID, display.ToolCallID)
	}
	if reloadedDisplay.Role != "tool" {
		t.Errorf("reloaded display row role = %q, want tool", reloadedDisplay.Role)
	}
	if !reloadedDisplay.ExcludeFromContext {
		t.Error("reloaded display row lost exclude_from_context")
	}
	if len(reloadedDisplay.Attachments) != 1 {
		t.Fatalf("reloaded display row attachments = %d, want 1", len(reloadedDisplay.Attachments))
	}
	if got := reloadedDisplay.Attachments[0]; got.Path != attachment.Path || got.Name != attachment.Name ||
		got.MIMEType != attachment.MIMEType || got.Kind != attachment.Kind {
		t.Errorf("reloaded display row attachment = %+v, want %+v", got, attachment)
	}

	contextMessages := filterContextMessages(reloaded)
	if len(contextMessages) != len(reloaded)-1 {
		t.Errorf("context messages = %d, want %d (only the display row leaves the context)",
			len(contextMessages), len(reloaded)-1)
	}
	for i, msg := range contextMessages {
		if strings.HasPrefix(msg.ToolCallID, "file:") {
			t.Errorf("context message %d is the display row (tool_call_id %q)", i, msg.ToolCallID)
		}
		if strings.Contains(msg.Content, attachmentRowCaption) {
			t.Errorf("context message %d carries the display caption: %q", i, msg.Content)
		}
		for _, a := range msg.Attachments {
			if a.Path == attachmentPath {
				t.Errorf("context message %d carries the display attachment %q", i, a.Path)
			}
		}
	}
	assertContextContains(t, contextMessages, attachmentRowUserText)
	assertContextContains(t, contextMessages, attachmentRowAssistantText)
	assertContextContains(t, contextMessages, attachmentRowToolContent)

	healed, changed := healAssistantToolCalls(reloaded)
	if changed {
		t.Fatalf("healAssistantToolCalls rewrote a canonical history (%d rows in, %d rows out)", len(reloaded), len(healed))
	}
	cleaned, removed := dropBlankAssistantMessages(healed)
	if removed {
		t.Fatalf("dropBlankAssistantMessages dropped rows from a history without blank assistants (%d in, %d out)",
			len(healed), len(cleaned))
	}
	if len(cleaned) != len(reloaded) {
		t.Fatalf("heal+drop changed the row count: %d in, %d out", len(reloaded), len(cleaned))
	}
	healedRows := displayRowsOf(cleaned)
	if len(healedRows) != 1 {
		t.Fatalf("display rows after heal+drop = %d, want exactly 1", len(healedRows))
	}
	if healedRows[0].ToolCallID != reloadedDisplay.ToolCallID || !healedRows[0].ExcludeFromContext {
		t.Errorf("display row lost its identity or its exclusion flag through heal+drop: %+v", healedRows[0])
	}
	if len(healedRows[0].Attachments) != 1 || healedRows[0].Attachments[0].Path != attachmentPath {
		t.Errorf("display row attachments after heal+drop = %+v, want the seeded path %q",
			healedRows[0].Attachments, attachmentPath)
	}
}
