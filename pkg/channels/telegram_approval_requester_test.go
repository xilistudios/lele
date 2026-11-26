package channels

import (
	"context"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
)

func newApprovalCallbackTestChannel(t *testing.T, recorder *botAPIRecorder, am *ApprovalManager) *TelegramChannel {
	t.Helper()
	channel := newTelegramTestChannel(t, nil, recorder)
	channel.approvalManager = am
	return channel
}

func telegramApprovalUpdate(userID int64, username, action, approvalID string) telego.CallbackQuery {
	update := telegramCallbackUpdate(userID, username, "approval:"+action+":"+approvalID)
	return *update.CallbackQuery
}

func answerCallbackTexts(calls []botAPICall) []string {
	texts := make([]string, 0, len(calls))
	for _, call := range calls {
		if !strings.HasSuffix(call.method, "/answerCallbackQuery") {
			continue
		}
		texts = append(texts, call.body)
	}
	return texts
}

func countBotMethod(calls []botAPICall, suffix string) int {
	n := 0
	for _, call := range calls {
		if strings.HasSuffix(call.method, suffix) {
			n++
		}
	}
	return n
}

func TestTelegramApproval_OtherUserCannotApprove(t *testing.T) {
	entries := captureLogEntries(t)
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous command", 555, "111|alice")

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(222, "bob", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	calls := recorder.snapshot()
	if counted := countBotMethod(calls, "/answerCallbackQuery"); counted != 1 {
		t.Fatalf("answerCallbackQuery calls = %d, want exactly 1 (the denial must always answer): %#v", counted, calls)
	}
	texts := answerCallbackTexts(calls)
	if !strings.Contains(texts[0], "Only the user who requested this command can approve it") {
		t.Fatalf("denial ack = %q, want the requester-mismatch notice", texts[0])
	}
	if countBotMethod(calls, "/editMessageText") != 0 {
		t.Fatalf("a foreign approval must not rewrite the approval message: %#v", calls)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must stay pending after a foreign approval attempt")
	}

	warnings := loggedMessages(entries(), "Approval requester mismatch rejected")
	if len(warnings) != 1 {
		t.Fatalf("requester-mismatch warnings = %d, want 1", len(warnings))
	}
	if got := warnings[0].Fields["approval_id"]; got != approval.ID {
		t.Fatalf("warning approval_id = %v, want %q", got, approval.ID)
	}
	if got := warnings[0].Fields["caller_requester_id"]; got != "222|bob" {
		t.Fatalf("warning caller_requester_id = %v, want %q", got, "222|bob")
	}
}

func TestTelegramCallback_DenyAnswersQuery(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous command", 555, "111|alice")

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(222, "bob", "reject", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	calls := recorder.snapshot()
	if countBotMethod(calls, "/answerCallbackQuery") != 1 {
		t.Fatalf("a rejected foreign callback must be answered (no spinner), got %#v", calls)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("a foreign rejection must not consume the approval")
	}
}

func TestTelegramApproval_RequesterApproves(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous command", 555, "111|alice")

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(111, "alice", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	if am.GetApproval(approval.ID) != nil {
		t.Fatal("the requester must be able to consume the approval")
	}

	calls := recorder.snapshot()
	if countBotMethod(calls, "/editMessageText") != 1 {
		t.Fatalf("the approval message must be rewritten once, got %#v", calls)
	}
	texts := answerCallbackTexts(calls)
	if len(texts) != 1 || !strings.Contains(texts[0], "Command aprobado") {
		t.Fatalf("approval ack = %#v, want the approval confirmation", texts)
	}
}

func TestTelegramApproval_RequesterApprovesAfterUsernameChange(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous command", 555, "111|alice")

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(111, "alice_renamed", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	if am.GetApproval(approval.ID) != nil {
		t.Fatal("a renamed requester must still be able to approve")
	}
}

func TestTelegramApproval_SessionMismatchStillRejected(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:999", "rm -rf /tmp/x", "dangerous command", 999, "111|alice")

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(111, "alice", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts := answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "otro chat") {
		t.Fatalf("session-mismatch ack = %#v, want the foreign-chat notice", texts)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("a foreign-chat callback must not consume the approval")
	}
}

func TestTelegramApproval_ApprovalWithoutRequesterUsesSession(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous command", 555, "")

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(333, "carol", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	if am.GetApproval(approval.ID) != nil {
		t.Fatal("an approval without a recorded requester must stay resolvable from its chat")
	}
}

func TestTelegramApproval_NotFoundAnswered(t *testing.T) {
	entries := captureLogEntries(t)
	am := NewApprovalManager()

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(111, "alice", "approve", "does-not-exist")
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts := answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "expired or not found") {
		t.Fatalf("not-found ack = %#v, want the expiry notice", texts)
	}
	if got := len(loggedMessages(entries(), "Approval not found")); got != 1 {
		t.Fatalf("not-found warnings = %d, want 1", got)
	}
}

func TestTelegramApproval_ViewRequiresRequester(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous command", 555, "111|alice")

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	foreign := telegramApprovalUpdate(222, "bob", "view", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), foreign); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	calls := recorder.snapshot()
	texts := answerCallbackTexts(calls)
	if len(texts) != 1 || !strings.Contains(texts[0], "Only the user who requested this command can view it") {
		t.Fatalf("foreign view ack = %#v, want the requester-mismatch notice", texts)
	}
	if strings.Contains(texts[0], "rm -rf") {
		t.Fatalf("a foreign member must not see the command: %q", texts[0])
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("viewing must not consume the approval")
	}

	recorder.reset()
	owner := telegramApprovalUpdate(111, "alice", "view", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), owner); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts = answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "rm -rf /tmp/x") || !strings.Contains(texts[0], "dangerous command") {
		t.Fatalf("owner view ack = %#v, want the command and its reason", texts)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("viewing must not consume the approval")
	}
}

func TestTelegramApproval_ViewOfExpiredApprovalAnswered(t *testing.T) {
	am := NewApprovalManager()

	recorder := &botAPIRecorder{}
	channel := newApprovalCallbackTestChannel(t, recorder, am)

	query := telegramApprovalUpdate(111, "alice", "view", "does-not-exist")
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts := answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "expired or not found") {
		t.Fatalf("expired view ack = %#v, want the expiry notice", texts)
	}
}
