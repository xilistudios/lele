package channels

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/xilistudios/lele/pkg/logger"
)

func (c *TelegramChannel) handleModelsCallback(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}
	parts := strings.SplitN(query.Data, ":", 4)
	if len(parts) < 3 {
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid action"))
		return nil
	}

	switch parts[1] {
	case "provider":
		provider := parts[2]
		page := 0
		if len(parts) >= 4 {
			if p, err := strconv.Atoi(parts[3]); err == nil {
				page = p
			}
		}
		if err := c.sendProviderModelsMenu(ctx, query.Message.GetChat().ID, query.Message.GetMessageID(), provider, page); err != nil {
			return err
		}
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Provider selected"))
	case "page":
		if len(parts) < 4 {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid page action"))
			return nil
		}
		provider := parts[2]
		page, err := strconv.Atoi(parts[3])
		if err != nil {
			page = 0
		}
		if err := c.sendProviderModelsMenu(ctx, query.Message.GetChat().ID, query.Message.GetMessageID(), provider, page); err != nil {
			return err
		}
		_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Page updated"))
	case "model":
		if len(parts) < 4 {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Invalid model action"))
			return nil
		}
		provider := parts[2]
		model := parts[3]
		if c.applySelectedModel(query, provider, model) {
			if err := c.collapseModelsMenu(ctx, query); err != nil {
				logger.WarnCF("telegram", "Failed to collapse /models keyboard", map[string]interface{}{
					"error": err.Error(),
				})
			}
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Model selected"))
		} else {
			_ = c.bot.AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Model not applied"))
		}
	}

	return nil
}

func (c *TelegramChannel) sendProviderModelsMenu(ctx context.Context, chatID int64, messageID int, provider string, page int) error {
	isEdit := messageID > 0
	named, ok := c.config.Providers.GetNamed(provider)
	if !ok || len(named.Models) == 0 {
		if isEdit {
			_, err := c.bot.EditMessageText(ctx, tu.EditMessageText(tu.ID(chatID), messageID, "No models configured for this provider."))
			return err
		}
		_, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), "No models configured for this provider."))
		return err
	}

	models := make([]string, 0, len(named.Models))
	for name := range named.Models {
		models = append(models, name)
	}
	sort.Strings(models)

	start, end, currentPage, totalPages := modelPageBounds(len(models), page, 6)
	rows := make([][]telego.InlineKeyboardButton, 0, end-start)
	for _, model := range models[start:end] {
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(model).WithCallbackData(fmt.Sprintf("models:model:%s:%s", provider, model)),
		))
	}

	if totalPages > 1 {
		nav := make([]telego.InlineKeyboardButton, 0, 2)
		if currentPage > 0 {
			nav = append(nav, tu.InlineKeyboardButton("⬅️ Previous Page").
				WithCallbackData(fmt.Sprintf("models:page:%s:%d", provider, currentPage-1)))
		}
		if currentPage < totalPages-1 {
			nav = append(nav, tu.InlineKeyboardButton("➡️ Next Page").
				WithCallbackData(fmt.Sprintf("models:page:%s:%d", provider, currentPage+1)))
		}
		if len(nav) > 0 {
			rows = append(rows, tu.InlineKeyboardRow(nav...))
		}
	}

	text := fmt.Sprintf("Provider: %s\nSelect a model (page %d/%d):", provider, currentPage+1, totalPages)
	markup := tu.InlineKeyboard(rows...)
	if isEdit {
		edit := tu.EditMessageText(tu.ID(chatID), messageID, text)
		edit.ReplyMarkup = markup
		_, err := c.bot.EditMessageText(ctx, edit)
		return err
	}

	_, err := c.bot.SendMessage(ctx, tu.Message(tu.ID(chatID), text).WithReplyMarkup(markup))
	return err
}

func modelPageBounds(total, page, perPage int) (start, end, currentPage, totalPages int) {
	if perPage <= 0 {
		perPage = 6
	}
	if total <= 0 {
		return 0, 0, 0, 1
	}

	totalPages = (total + perPage - 1) / perPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start = page * perPage
	end = start + perPage
	if end > total {
		end = total
	}
	return start, end, page, totalPages
}

func (c *TelegramChannel) applySelectedModel(query telego.CallbackQuery, provider, model string) bool {
	if query.Message == nil {
		return false
	}

	senderID := telegramSenderID(query.From.ID, query.From.Username)
	if !c.IsAllowed(senderID) {
		return false
	}

	chat := query.Message.GetChat()
	messageID := query.Message.GetMessageID()
	c.publishSystemCommand(senderID, chat.ID, messageID, selectedModelCommand(provider, model), buildTelegramMetadata(messageID, &query.From, chat))
	return true
}

func selectedModelCommand(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if isModelReference(model) || provider == "" {
		return "/model " + model
	}
	return fmt.Sprintf("/model %s/%s", provider, model)
}

func isModelReference(model string) bool {
	idx := strings.Index(model, "/")
	return idx > 0 && idx < len(model)-1
}

func (c *TelegramChannel) collapseModelsMenu(ctx context.Context, query telego.CallbackQuery) error {
	if query.Message == nil {
		return nil
	}
	_, err := c.bot.EditMessageReplyMarkup(ctx, tu.EditMessageReplyMarkup(
		tu.ID(query.Message.GetChat().ID),
		query.Message.GetMessageID(),
		nil,
	))
	return err
}
