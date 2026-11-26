package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/xilistudios/lele/pkg/logger"
)

func (c *TelegramChannel) handleApprovalCallback(ctx context.Context, query telego.CallbackQuery) error {
	logger.DebugCF("telegram", "handleApprovalCallback called", map[string]interface{}{
		"data":                 query.Data,
		"approval_manager_nil": c.approvalManager == nil,
	})

	if c.approvalManager == nil {
		logger.ErrorC("telegram", "handleApprovalCallback: approval manager is nil")
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval system not available"))
	}

	parts := strings.SplitN(query.Data, ":", 3)
	if len(parts) != 3 || parts[0] != "approval" {
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid callback data"))
	}

	action := parts[1]
	approvalID := parts[2]

	logger.DebugCF("telegram", "handleApprovalCallback parsed", map[string]interface{}{
		"action":      action,
		"approval_id": approvalID,
	})

	callerSessionKey := c.resolveSessionKey(telegramCallbackPeerInfo(query))
	callerRequesterID := telegramSenderID(query.From.ID, query.From.Username)

	switch action {
	case "view":
		return c.handleApprovalView(ctx, query, approvalID, callerSessionKey, callerRequesterID)
	case "approve", "reject":
	default:
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Unknown action"))
	}

	approved := action == "approve"

	if query.Message == nil {
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval request not available"))
	}

	logger.DebugCF("telegram", "handleApprovalCallback calling HandleApprovalForRequester", map[string]interface{}{
		"approval_id":         approvalID,
		"approved":            approved,
		"caller_session_key":  callerSessionKey,
		"caller_requester_id": callerRequesterID,
	})

	approval, err := c.approvalManager.HandleApprovalForRequester(approvalID, approved, callerSessionKey, callerRequesterID)
	if err != nil {
		return c.answerApprovalDenial(ctx, query, approvalID, callerSessionKey, callerRequesterID, err, "approve")
	}

	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()

	var statusEmoji, statusText string
	if approved {
		statusEmoji = "✅"
		statusText = "APROBADO"
	} else {
		statusEmoji = "❌"
		statusText = "RECHAZADO"
	}

	updatedText := fmt.Sprintf("%s **Comando %s**\n`%s`", statusEmoji, statusText, approval.Command)

	editMsg := tu.EditMessageText(tu.ID(chatID), messageID, markdownToTelegramHTML(updatedText))
	editMsg.ParseMode = telego.ModeHTML

	if _, editErr := c.bot.EditMessageText(ctx, editMsg); editErr != nil {
		logger.DebugCF("telegram", "Failed to edit approval message", map[string]interface{}{
			"error": editErr.Error(),
		})
	}

	feedback := fmt.Sprintf("Command %s", strings.ToLower(statusText))
	return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(feedback))
}

func (c *TelegramChannel) handleApprovalView(ctx context.Context, query telego.CallbackQuery, approvalID, callerSessionKey, callerRequesterID string) error {
	approval, err := c.approvalManager.CheckApprovalForRequester(approvalID, callerSessionKey, callerRequesterID)
	if err != nil {
		return c.answerApprovalDenial(ctx, query, approvalID, callerSessionKey, callerRequesterID, err, "view")
	}

	text := fmt.Sprintf("Comando:\n`%s`\n\nRazón: %s", approval.Command, approval.Reason)
	return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(text))
}

func (c *TelegramChannel) answerApprovalDenial(ctx context.Context, query telego.CallbackQuery, approvalID, callerSessionKey, callerRequesterID string, err error, verb string) error {
	switch {
	case errors.Is(err, ErrApprovalRequesterMismatch):
		logger.WarnCF("telegram", "Approval requester mismatch rejected", map[string]interface{}{
			"approval_id":         approvalID,
			"caller_requester_id": callerRequesterID,
			"caller_session_key":  callerSessionKey,
		})
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(fmt.Sprintf("⚠️ Only the user who requested this command can %s it", verb)))
	case errors.Is(err, ErrApprovalSessionMismatch):
		logger.WarnCF("telegram", "Approval session mismatch rejected", map[string]interface{}{
			"approval_id":        approvalID,
			"caller_session_key": callerSessionKey,
		})
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("⚠️ Esta aprobación pertenece a otro chat"))
	case errors.Is(err, ErrApprovalNotFound):
		logger.WarnCF("telegram", "Approval not found", map[string]interface{}{
			"approval_id":        approvalID,
			"caller_session_key": callerSessionKey,
		})
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval request expired or not found"))
	default:
		logger.ErrorCF("telegram", "Failed to handle approval", map[string]interface{}{
			"error":              err.Error(),
			"approval_id":        approvalID,
			"caller_session_key": callerSessionKey,
		})
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("⚠️ Approval could not be processed"))
	}
}
