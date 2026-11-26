package channels

import (
	"context"
	"strings"
	"testing"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/routing"
)

func productSessionKey(t *testing.T, peerKind, peerID string) string {
	t.Helper()
	return routing.BuildAgentPeerSessionKey(routing.SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &routing.RoutePeer{Kind: peerKind, ID: peerID},
	})
}

func newRoutedApprovalChannel(t *testing.T, recorder *botAPIRecorder, am *ApprovalManager) *TelegramChannel {
	t.Helper()
	channel := newApprovalCallbackTestChannel(t, recorder, am)
	channel.agentLoop = newNativeTestAgentLoop(&config.Config{})
	return channel
}

func telegramChatApprovalUpdate(chatID int64, chatType string, userID int64, username, action, approvalID string) telego.CallbackQuery {
	return telego.CallbackQuery{
		ID:      "callback-routed",
		From:    telego.User{ID: userID, Username: username},
		Data:    "approval:" + action + ":" + approvalID,
		Message: &telego.Message{MessageID: 31, Chat: telego.Chat{ID: chatID, Type: chatType}},
	}
}

func TestTelegramApproval_CallbackKeyMatchesTheProductSessionKey(t *testing.T) {
	am := NewApprovalManager()
	channel := newRoutedApprovalChannel(t, &botAPIRecorder{}, am)

	cases := []struct {
		peerKind string
		peerID   string
	}{
		{peerKind: "group", peerID: "-100123"},
		{peerKind: "direct", peerID: "111"},
	}

	for _, tc := range cases {
		t.Run(tc.peerKind, func(t *testing.T) {
			want := productSessionKey(t, tc.peerKind, tc.peerID)
			if got := channel.resolveSessionKey(tc.peerKind, tc.peerID); got != want {
				t.Fatalf("resolveSessionKey(%q, %q) = %q, want the product key %q", tc.peerKind, tc.peerID, got, want)
			}
		})
	}
}

func TestTelegramApproval_RequesterApprovesInGroupChat(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval(productSessionKey(t, "group", "-100123"), "rm -rf /tmp/x", "dangerous command", -100123, "111")

	recorder := &botAPIRecorder{}
	channel := newRoutedApprovalChannel(t, recorder, am)

	query := telegramChatApprovalUpdate(-100123, "group", 111, "alice", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts := answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "Command aprobado") {
		t.Fatalf("ack = %#v, want the approval confirmation (the requester must reach the requester check, not the session gate)", texts)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("the requester must consume the approval in a group chat")
	}
}

func TestTelegramApproval_OtherUserDeniedInGroupChat(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval(productSessionKey(t, "group", "-100123"), "rm -rf /tmp/x", "dangerous command", -100123, "111")

	recorder := &botAPIRecorder{}
	channel := newRoutedApprovalChannel(t, recorder, am)

	query := telegramChatApprovalUpdate(-100123, "group", 222, "bob", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts := answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "Only the user who requested this command can approve it") {
		t.Fatalf("ack = %#v, want the requester-mismatch notice", texts)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("a foreign user must not consume the approval")
	}
}

func TestTelegramApproval_RequesterApprovesInDirectChat(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval(productSessionKey(t, "direct", "111"), "rm -rf /tmp/x", "dangerous command", 111, "111")

	recorder := &botAPIRecorder{}
	channel := newRoutedApprovalChannel(t, recorder, am)

	query := telegramChatApprovalUpdate(111, "private", 111, "alice", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts := answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "Command aprobado") {
		t.Fatalf("ack = %#v, want the approval confirmation", texts)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("the requester must consume the approval in a direct chat")
	}
}

func TestTelegramApproval_ForeignGroupStillRejectedOnRealKey(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval(productSessionKey(t, "group", "-100999"), "rm -rf /tmp/x", "dangerous command", -100999, "111")

	recorder := &botAPIRecorder{}
	channel := newRoutedApprovalChannel(t, recorder, am)

	query := telegramChatApprovalUpdate(-100123, "group", 111, "alice", "approve", approval.ID)
	if err := channel.handleApprovalCallback(context.Background(), query); err != nil {
		t.Fatalf("handleApprovalCallback() error = %v", err)
	}

	texts := answerCallbackTexts(recorder.snapshot())
	if len(texts) != 1 || !strings.Contains(texts[0], "otro chat") {
		t.Fatalf("ack = %#v, want the foreign-chat notice", texts)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("a callback from another chat must not consume the approval")
	}
}
