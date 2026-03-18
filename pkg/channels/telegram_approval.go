package channels

import (
	"context"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/xilistudios/lele/pkg/logger"
)

// handleApprovalCallback processes callback queries from approval inline keyboards
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

	var approved bool
	switch action {
	case "approve":
		approved = true
	case "reject":
		approved = false
	case "view":
		return c.handleApprovalView(ctx, query, approvalID)
	default:
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Unknown action"))
	}

	logger.DebugCF("telegram", "handleApprovalCallback calling HandleApproval", map[string]interface{}{
		"approval_id": approvalID,
		"approved":    approved,
	})

	approval, err := c.approvalManager.HandleApproval(approvalID, approved)
	if err != nil {
		logger.WarnCF("telegram", "Failed to handle approval", map[string]interface{}{
			"error":       err.Error(),
			"approval_id": approvalID,
		})
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval request expired or not found"))
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

// handleApprovalView handles the "view" action to show the full command
func (c *TelegramChannel) handleApprovalView(ctx context.Context, query telego.CallbackQuery, approvalID string) error {
	approval := c.approvalManager.GetApproval(approvalID)
	if approval == nil {
		return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Approval request not found or expired"))
	}

	text := fmt.Sprintf("Comando:\n`%s`\n\nRazón: %s", approval.Command, approval.Reason)
	return c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText(text))
}
