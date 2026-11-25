package channels

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/logger"
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

func enableApprovalLogCapture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prevLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	if err := logger.EnableMultiFileLogging(dir); err != nil {
		logger.SetLevel(prevLevel)
		t.Fatalf("EnableMultiFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})
	return dir
}

func readCapturedErrorLog(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "errors-"+time.Now().Format("2006-01-02")+".log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read captured error log: %v", err)
	}
	return string(data)
}

func TestTelegramApproval_OtherUserCannotApprove(t *testing.T) {
	logDir := enableApprovalLogCapture(t)
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

	logs := readCapturedErrorLog(t, logDir)
	if !strings.Contains(logs, "Approval requester mismatch rejected") {
		t.Fatalf("missing requester-mismatch warning in the error log: %q", logs)
	}
	if !strings.Contains(logs, approval.ID) {
		t.Fatalf("warning must carry the approval id %q: %q", approval.ID, logs)
	}
}

func TestTelegramApproval_DenyByOtherUserAnswersQuery(t *testing.T) {
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
