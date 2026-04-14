package channels

import (
	"context"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/xilistudios/lele/pkg/config"
)

// handleAgentCallback processes callback queries for agent selection
func (c *TelegramChannel) handleAgentCallback(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}

	if c.agentLoop == nil {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent management not available"))
		return nil
	}

	parts := strings.SplitN(query.Data, ":", 3)
	if len(parts) < 3 || parts[0] != "agent" {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid action"))
		return nil
	}

	action := parts[1]
	agentID := parts[2]

	switch action {
	case "select":
		agentInfo, agentExists := c.agentLoop.GetAgentInfo(agentID)
		if !agentExists {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent not found"))
			return nil
		}

		agentName := agentInfo.Name
		if agentName == "" {
			agentName = agentID
		}

		chat := query.Message.GetChat()
		messageID := query.Message.GetMessageID()
		senderID := telegramSenderID(query.From.ID, query.From.Username)
		c.publishSystemCommand(senderID, chat.ID, messageID, telegramCommandText("agent", agentID), buildTelegramMetadata(messageID, &query.From, chat))

		text := formatAgentSelectedMessage(agentInfo, agentID)
		editMsg := tu.EditMessageText(tu.ID(chat.ID), messageID, text)
		editMsg.ParseMode = telego.ModeMarkdown
		_, _ = c.bot.EditMessageText(ctx, editMsg)

		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent selected: "+agentName))
	default:
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Unknown action"))
	}

	return nil
}

// formatAgentSelectedMessage formats a message when an agent is selected
func formatAgentSelectedMessage(agent AgentBasicInfo, agentID string) string {
	var parts []string
	parts = append(parts, "✅ *Agente seleccionado*")
	parts = append(parts, "")
	parts = append(parts, fmt.Sprintf("*Nombre:* %s", agent.Name))
	parts = append(parts, fmt.Sprintf("*ID:* `%s`", agentID))
	parts = append(parts, fmt.Sprintf("*Modelo:* `%s`", agent.Model))
	if agent.Workspace != "" && agent.Workspace != "workspace" {
		parts = append(parts, fmt.Sprintf("*Workspace:* `%s`", agent.Workspace))
	}
	if len(agent.SkillsFilter) > 0 {
		parts = append(parts, fmt.Sprintf("*Skills:* %s", strings.Join(agent.SkillsFilter, ", ")))
	}
	return strings.Join(parts, "\n")
}

// handleVerboseCallback processes callback queries for verbose level selection
func (c *TelegramChannel) handleVerboseCallback(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}

	if c.agentLoop == nil {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent management not available"))
		return nil
	}

	parts := strings.SplitN(query.Data, ":", 3)
	if len(parts) < 3 || parts[0] != "verbose" {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid action"))
		return nil
	}

	action := parts[1]
	level := parts[2]
	if action != "set" {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Unknown action"))
		return nil
	}

	sessionKey := telegramSessionKey(query.Message.GetChat().ID)
	previousLevel := c.agentLoop.GetVerboseLevel(sessionKey)

	if !c.agentLoop.SetVerboseLevel(sessionKey, level) {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Failed to set verbose level"))
		return nil
	}
	if err := c.config.PersistTelegramVerbose(config.DefaultConfigPath(), config.VerboseLevel(level)); err != nil {
		_ = c.agentLoop.SetVerboseLevel(sessionKey, previousLevel)
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Failed to update config.json"))
		return err
	}

	var emoji string
	switch level {
	case "off":
		emoji = "🔇"
	case "basic":
		emoji = "🛠️"
	case "full":
		emoji = "📋"
	default:
		emoji = "🔇"
	}

	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()
	updatedText := fmt.Sprintf(
		"*Verbose Mode Settings*\n\n"+
			"Current level: %s *%s*\n\n"+
			"*Available options:*\n"+
			"🔇 *off* - No tool execution notifications\n"+
			"🛠️ *basic* - Simplified tool descriptions\n"+
			"📋 *full* - Detailed tool calls and results\n\n"+
			"Use /verbose to cycle through levels.",
		emoji, level)

	editMsg := tu.EditMessageText(tu.ID(chatID), messageID, updatedText)
	editMsg.ParseMode = telego.ModeMarkdown
	_, _ = c.bot.EditMessageText(ctx, editMsg)

	_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Verbose level set to "+level))
	return nil
}

// handleThinkCallback processes callback queries for think level selection
func (c *TelegramChannel) handleThinkCallback(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}

	if c.agentLoop == nil {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Agent management not available"))
		return nil
	}

	parts := strings.SplitN(query.Data, ":", 3)
	if len(parts) < 3 || parts[0] != "think" {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid action"))
		return nil
	}

	action := parts[1]
	level := parts[2]
	if action != "set" {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Unknown action"))
		return nil
	}

	sessionKey := telegramSessionKey(query.Message.GetChat().ID)

	if !c.agentLoop.SetThinkLevel(sessionKey, level) {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Failed to set think level"))
		return nil
	}

	var emoji string
	displayLevel := level
	switch level {
	case "off":
		emoji = "🧠"
		displayLevel = "default"
	case "low":
		emoji = "💡"
	case "medium":
		emoji = "🤔"
	case "high":
		emoji = "🧩"
	default:
		emoji = "🧠"
	}

	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()
	updatedText := fmt.Sprintf(
		"*Think Mode Settings*\n\n"+
			"Current level: %s *%s*\n\n"+
			"*Available options:*\n"+
			"🧠 *off* - Use agent default reasoning\n"+
			"💡 *low* - Minimal reasoning effort\n"+
			"🤔 *medium* - Balanced reasoning effort\n"+
			"🧩 *high* - Maximum reasoning effort\n\n"+
			"Use /think to cycle through levels.",
		emoji, displayLevel)

	editMsg := tu.EditMessageText(tu.ID(chatID), messageID, updatedText)
	editMsg.ParseMode = telego.ModeMarkdown
	_, _ = c.bot.EditMessageText(ctx, editMsg)

	_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Think level set to "+level))
	return nil
}
