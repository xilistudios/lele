package channels

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/sipeed/picoclaw/pkg/config"
)

type TelegramCommander interface {
	Help(ctx context.Context, message telego.Message) error
	Start(ctx context.Context, message telego.Message) error
	Show(ctx context.Context, message telego.Message) error
	List(ctx context.Context, message telego.Message) error
	Models(ctx context.Context, message telego.Message) error
	New(ctx context.Context, message telego.Message) error
	Stop(ctx context.Context, message telego.Message) error
	Model(ctx context.Context, message telego.Message) error
	Status(ctx context.Context, message telego.Message) error
	Subagents(ctx context.Context, message telego.Message) error
	Agent(ctx context.Context, message telego.Message) error
	Verbose(ctx context.Context, message telego.Message, currentLevel string) error
}

type cmd struct {
	bot    *telego.Bot
	config *config.Config
}

func NewTelegramCommands(bot *telego.Bot, cfg *config.Config) TelegramCommander {
	return &cmd{
		bot:    bot,
		config: cfg,
	}
}

func commandArgs(text string) string {
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
func (c *cmd) Help(ctx context.Context, message telego.Message) error {
	msg := `/start - Start the bot
/help - Show this help message
/show [model|channel] - Show current configuration
/list [models|channels|agents] - List available options
/models - Show providers and models
/agent - Select or change current agent
	`
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   msg,
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Start(ctx context.Context, message telego.Message) error {
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   "Hello! I am PicoClaw 🦞",
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Show(ctx context.Context, message telego.Message) error {
	args := commandArgs(message.Text)
	if args == "" {
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   "Usage: /show [model|channel]",
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err
	}

	var response string
	switch args {
	case "model":
		response = fmt.Sprintf("Current Model: %s (Provider: %s)",
			c.config.Agents.Defaults.Model,
			c.config.Agents.Defaults.Provider)
	case "channel":
		response = "Current Channel: telegram"
	default:
		response = fmt.Sprintf("Unknown parameter: %s. Try 'model' or 'channel'.", args)
	}

	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   response,
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}
func (c *cmd) List(ctx context.Context, message telego.Message) error {
	args := commandArgs(message.Text)
	if args == "" {
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   "Usage: /list [models|channels]",
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err
	}

	var response string
	switch args {
	case "models":
		provider := c.config.Agents.Defaults.Provider
		if provider == "" {
			provider = "configured default"
		}
		response = fmt.Sprintf("Configured Model: %s\nProvider: %s\n\nTo change models, update config.yaml",
			c.config.Agents.Defaults.Model, provider)

	case "channels":
		var enabled []string
		if c.config.Channels.Telegram.Enabled {
			enabled = append(enabled, "telegram")
		}
		if c.config.Channels.WhatsApp.Enabled {
			enabled = append(enabled, "whatsapp")
		}
		if c.config.Channels.Feishu.Enabled {
			enabled = append(enabled, "feishu")
		}
		if c.config.Channels.Discord.Enabled {
			enabled = append(enabled, "discord")
		}
		if c.config.Channels.Slack.Enabled {
			enabled = append(enabled, "slack")
		}
		response = fmt.Sprintf("Enabled Channels:\n- %s", strings.Join(enabled, "\n- "))

	default:
		response = fmt.Sprintf("Unknown parameter: %s. Try 'models' or 'channels'.", args)
	}

	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   response,
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Models(ctx context.Context, message telego.Message) error {
	providers := make([]string, 0, len(c.config.Providers.Named))
	for name, provider := range c.config.Providers.Named {
		if len(provider.Models) > 0 {
			providers = append(providers, name)
		}
	}
	sort.Strings(providers)
	if len(providers) == 0 {
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   "No providers with configured models found.",
		})
		return err
	}

	rows := make([][]telego.InlineKeyboardButton, 0, len(providers))
	for _, provider := range providers {
		named := c.config.Providers.Named[provider]
		label := fmt.Sprintf("%s (%d)", provider, len(named.Models))
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(label).WithCallbackData("models:provider:"+provider),
		))
	}

	msg := tu.Message(tu.ID(message.Chat.ID), "Select a provider.").WithReplyMarkup(tu.InlineKeyboard(rows...))
	_, err := c.bot.SendMessage(ctx, msg)
	return err
}

func (c *cmd) New(ctx context.Context, message telego.Message) error {
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   "🔄 Nueva conversación iniciada. Historial limpiado.",
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Stop(ctx context.Context, message telego.Message) error {
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   "⏹️ Agente detenido.",
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Model(ctx context.Context, message telego.Message) error {
	args := commandArgs(message.Text)
	if args == "" {
		response := fmt.Sprintf("Modelo actual: %s\nProveedor: %s\n\nUsa /model <nombre> para cambiar",
			c.config.Agents.Defaults.Model,
			c.config.Agents.Defaults.Provider)
		_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID},
			Text:   response,
			ReplyParameters: &telego.ReplyParameters{
				MessageID: message.MessageID,
			},
		})
		return err
	}
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   fmt.Sprintf("Cambiando modelo a: %s\n(Nota: este comando aún no cambia el modelo en runtime)", args),
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Status(ctx context.Context, message telego.Message) error {
	response := fmt.Sprintf("📊 Estado del Gateway\n\nModelo: %s\nProveedor: %s\nToken máximo: %d\nTemperatura: %.1f",
		c.config.Agents.Defaults.Model,
		c.config.Agents.Defaults.Provider,
		c.config.Agents.Defaults.MaxTokens,
		*c.config.Agents.Defaults.Temperature)
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   response,
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Subagents(ctx context.Context, message telego.Message) error {
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   "🤖 Subagentes: No hay subagentes activos actualmente.",
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Agent(ctx context.Context, message telego.Message) error {
	args := commandArgs(message.Text)
	if args == "" {
		// Verificar si hay agentes configurados
		agents := c.config.Agents.List
		if len(agents) == 0 {
			_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
				ChatID: telego.ChatID{ID: message.Chat.ID},
				Text:   "No hay agentes configurados.",
				ReplyParameters: &telego.ReplyParameters{
					MessageID: message.MessageID,
				},
			})
			return err
		}

		// Crear botones para cada agente
		rows := make([][]telego.InlineKeyboardButton, 0, len(agents))
		for _, agent := range agents {
			label := agent.Name
			if label == "" {
				label = agent.ID
			}
			label = fmt.Sprintf("🤖 %s", label)
			rows = append(rows, tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(label).WithCallbackData("agent:select:"+agent.ID),
			))
		}

		msg := tu.Message(tu.ID(message.Chat.ID),
			"🤖 *Selecciona un agente*\n\n"+
			"El agente determina el modelo, workspace y habilidades disponibles.").WithReplyMarkup(tu.InlineKeyboard(rows...))
		msg.ParseMode = telego.ModeMarkdown
		_, err := c.bot.SendMessage(ctx, msg)
		return err
	}

	// Cambio directo de agente
	_, err := c.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID},
		Text:   fmt.Sprintf("Cambiando al agente: %s", args),
		ReplyParameters: &telego.ReplyParameters{
			MessageID: message.MessageID,
		},
	})
	return err
}

func (c *cmd) Verbose(ctx context.Context, message telego.Message, currentLevel string) error {
	// Build the message showing current level and available options
	var currentEmoji string
	switch currentLevel {
	case "off":
		currentEmoji = "🔇"
	case "basic":
		currentEmoji = "🛠️"
	case "full":
		currentEmoji = "📋"
	default:
		currentEmoji = "🔇"
	}

	response := fmt.Sprintf(
		"*Verbose Mode Settings*\n\n"+
			"Current level: %s *%s*\n\n"+
			"*Available options:*\n"+
			"🔇 *off* - No tool execution notifications\n"+
			"🛠️ *basic* - Simplified tool descriptions\n"+
			"📋 *full* - Detailed tool calls and results\n\n"+
			"Use /verbose to cycle through levels.",
		currentEmoji, currentLevel)

	// Create inline keyboard with the three options
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔇 off").WithCallbackData("verbose:set:off"),
			tu.InlineKeyboardButton("🛠️ basic").WithCallbackData("verbose:set:basic"),
			tu.InlineKeyboardButton("📋 full").WithCallbackData("verbose:set:full"),
		),
	}

	msg := tu.Message(tu.ID(message.Chat.ID), response).WithReplyMarkup(tu.InlineKeyboard(rows...))
	msg.ParseMode = telego.ModeMarkdown

	_, err := c.bot.SendMessage(ctx, msg)
	return err
}
