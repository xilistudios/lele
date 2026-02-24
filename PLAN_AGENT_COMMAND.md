# Plan de Implementación: Comando /agent para PicoClaw

## Resumen

Agregar un comando `/agent` en Telegram que permita a los usuarios:
1. Ver el agente actual que están usando
2. Ver detalles del agente (modelo, workspace, skills)
3. Cambiar a otro agente disponible mediante botones interactivos

---

## Estructura de Archivos a Modificar/Crear

```
pkg/
├── agent/
│   └── loop.go              # Agregar métodos públicos para gestión de agentes
├── channels/
│   ├── telegram.go          # Agregar handlers y comandos
│   ├── telegram_commands.go # Agregar método Agent a la interfaz
│   └── telegram_agent.go    # NUEVO: Handler específico para /agent
```

---

## Paso 1: Agregar Métodos Públicos en AgentLoop

**Archivo:** `pkg/agent/loop.go`

Agregar estos métodos públicos a la estructura `AgentLoop`:

```go
// ListAvailableAgents devuelve la lista de IDs de todos los agentes registrados
func (al *AgentLoop) ListAvailableAgents() []string {
    return al.registry.ListAgentIDs()
}

// GetAgentInstance devuelve la instancia de un agente específico
func (al *AgentLoop) GetAgentInstance(agentID string) (*AgentInstance, bool) {
    return al.registry.GetAgent(agentID)
}

// SetSessionAgent establece el agente activo para una sesión específica
func (al *AgentLoop) SetSessionAgent(sessionKey, agentID string) {
    al.sessionModels.Store(sessionKey, agentID)
}

// GetSessionAgent obtiene el agente activo de una sesión (retorna "main" si no está establecido)
func (al *AgentLoop) GetSessionAgent(sessionKey string) string {
    if agentID, ok := al.sessionModels.Load(sessionKey); ok {
        return agentID.(string)
    }
    // Retornar el agente default
    if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil {
        return defaultAgent.ID
    }
    return "main"
}

// GetDefaultAgentID devuelve el ID del agente por defecto
func (al *AgentLoop) GetDefaultAgentID() string {
    if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil {
        return defaultAgent.ID
    }
    return "main"
}
```

**Ubicación:** Agregar después del método `processMessage` (línea ~350)

---

## Paso 2: Crear Handler para /agent

**Archivo Nuevo:** `pkg/channels/telegram_agent.go`

```go
package channels

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/sipeed/picoclaw/pkg/agent"
)

// AgentInfo contiene información resumida de un agente
type AgentInfo struct {
	ID        string
	Name      string
	Model     string
	Workspace string
	IsDefault bool
}

// AgentCommandHandler maneja el comando /agent y sus callbacks
type AgentCommandHandler struct {
	channel *TelegramChannel
}

// NewAgentCommandHandler crea un nuevo handler para el comando /agent
func NewAgentCommandHandler(channel *TelegramChannel) *AgentCommandHandler {
	return &AgentCommandHandler{channel: channel}
}

// Execute maneja el comando /agent inicial
func (h *AgentCommandHandler) Execute(ctx context.Context, message *telego.Message) error {
	chatID := message.Chat.ID
	sessionKey := fmt.Sprintf("telegram:%d", chatID)

	// Obtener agente actual
	currentAgentID := h.channel.agentLoop.GetSessionAgent(sessionKey)

	// Crear y enviar la tarjeta de agente
	return h.sendAgentCard(ctx, chatID, currentAgentID, 0)
}

// sendAgentCard envía o actualiza la tarjeta del agente
func (h *AgentCommandHandler) sendAgentCard(ctx context.Context, chatID int64, agentID string, messageID int) error {
	// Obtener información del agente
	agentInstance, ok := h.channel.agentLoop.GetAgentInstance(agentID)
	if !ok {
		return fmt.Errorf("agente no encontrado: %s", agentID)
	}

	// Obtener lista de agentes disponibles para los botones
	availableAgents := h.getAvailableAgentsInfo()

	// Construir mensaje
	text := h.formatAgentCard(agentInstance, agentID)

	// Construir botones
	keyboard := h.buildAgentButtons(availableAgents, agentID)

	if messageID > 0 {
		// Actualizar mensaje existente
		editMsg := tu.EditMessageText(tu.ID(chatID), messageID, text)
		editMsg.ParseMode = telego.ModeMarkdown
		editMsg.ReplyMarkup = tu.InlineKeyboard(keyboard...)
		_, err := h.channel.bot.EditMessageText(ctx, editMsg)
		return err
	}

	// Enviar nuevo mensaje
	msg := tu.Message(tu.ID(chatID), text)
	msg.ParseMode = telego.ModeMarkdown
	msg.ReplyMarkup = tu.InlineKeyboard(keyboard...)
	_, err := h.channel.bot.SendMessage(ctx, msg)
	return err
}

// formatAgentCard formatea la información del agente en Markdown
func (h *AgentCommandHandler) formatAgentCard(agent *agent.AgentInstance, agentID string) string {
	var builder strings.Builder

	builder.WriteString("🤖 *Agente Actual*\n\n")
	builder.WriteString(fmt.Sprintf("*ID:* `%s`\n", agentID))

	if agent.Name != "" {
		builder.WriteString(fmt.Sprintf("*Nombre:* %s\n", agent.Name))
	}

	builder.WriteString(fmt.Sprintf("*Modelo:* `%s`\n", agent.Model))

	if len(agent.Fallbacks) > 0 {
		builder.WriteString(fmt.Sprintf("*Fallbacks:* `%s`\n", strings.Join(agent.Fallbacks, "`, `")))
	}

	builder.WriteString(fmt.Sprintf("*Workspace:* `%s`\n", agent.Workspace))
	builder.WriteString(fmt.Sprintf("*Max Iteraciones:* %d\n", agent.MaxIterations))
	builder.WriteString(fmt.Sprintf("*Max Tokens:* %d\n", agent.MaxTokens))
	builder.WriteString(fmt.Sprintf("*Temperatura:* %.1f\n", agent.Temperature))

	if len(agent.SkillsFilter) > 0 {
		builder.WriteString(fmt.Sprintf("*Skills:* %s\n", strings.Join(agent.SkillsFilter, ", ")))
	}

	// Determinar si es el default
	defaultAgentID := h.channel.agentLoop.GetDefaultAgentID()
	if agentID == defaultAgentID {
		builder.WriteString("\n✅ *Este es el agente por defecto*")
	}

	return builder.String()
}

// getAvailableAgentsInfo obtiene información de todos los agentes disponibles
func (h *AgentCommandHandler) getAvailableAgentsInfo() []AgentInfo {
	agentIDs := h.channel.agentLoop.ListAvailableAgents()
	defaultAgentID := h.channel.agentLoop.GetDefaultAgentID()

	var agents []AgentInfo
	for _, id := range agentIDs {
		if instance, ok := h.channel.agentLoop.GetAgentInstance(id); ok {
			name := instance.Name
			if name == "" {
				name = id
			}
			agents = append(agents, AgentInfo{
				ID:        id,
				Name:      name,
				Model:     instance.Model,
				Workspace: instance.Workspace,
				IsDefault: id == defaultAgentID,
			})
		}
	}

	// Ordenar: primero el default, luego por nombre
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].IsDefault != agents[j].IsDefault {
			return agents[i].IsDefault
		}
		return agents[i].Name < agents[j].Name
	})

	return agents
}

// buildAgentButtons construye los botones inline para cambiar de agente
func (h *AgentCommandHandler) buildAgentButtons(agents []AgentInfo, currentAgentID string) [][]telego.InlineKeyboardButton {
	var rows [][]telego.InlineKeyboardButton

	// Fila de agentes disponibles
	for _, agent := range agents {
		if agent.ID == currentAgentID {
			// Agente actual marcado con check
			label := fmt.Sprintf("✓ %s", agent.Name)
			rows = append(rows, tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(label).WithCallbackData("agent:current:"+agent.ID),
			))
		} else {
			// Botón para cambiar a este agente
			label := fmt.Sprintf("→ %s", agent.Name)
			rows = append(rows, tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(label).WithCallbackData("agent:switch:"+agent.ID),
			))
		}
	}

	return rows
}

// HandleCallback maneja los callbacks de los botones inline
func (h *AgentCommandHandler) HandleCallback(ctx context.Context, query telego.CallbackQuery) error {
	// Verificar que el mensaje exista
	if query.Message == nil {
		return h.answerCallback(ctx, query.ID, "Error: mensaje no encontrado", true)
	}

	chatID := query.Message.Chat.ID
	messageID := query.Message.MessageID
	sessionKey := fmt.Sprintf("telegram:%d", chatID)

	// Parsear el callback data: "agent:action:agentID"
	parts := strings.Split(query.Data, ":")
	if len(parts) != 3 || parts[0] != "agent" {
		return h.answerCallback(ctx, query.ID, "Error: datos inválidos", true)
	}

	action := parts[1]
	agentID := parts[2]

	switch action {
	case "switch":
		// Cambiar al agente seleccionado
		h.channel.agentLoop.SetSessionAgent(sessionKey, agentID)

		// Actualizar la tarjeta del agente
		if err := h.sendAgentCard(ctx, chatID, agentID, messageID); err != nil {
			return h.answerCallback(ctx, query.ID, "Error al cambiar agente", true)
		}

		return h.answerCallback(ctx, query.ID, fmt.Sprintf("Agente cambiado a: %s", agentID), false)

	case "current":
		// El usuario presionó el agente actual
		return h.answerCallback(ctx, query.ID, "Este ya es tu agente actual", false)

	default:
		return h.answerCallback(ctx, query.ID, "Acción desconocida", true)
	}
}

// answerCallback responde a un callback query
func (h *AgentCommandHandler) answerCallback(ctx context.Context, callbackID, text string, showAlert bool) error {
	_, err := h.channel.bot.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       showAlert,
	})
	return err
}
```

---

## Paso 3: Modificar TelegramCommands Interface

**Archivo:** `pkg/channels/telegram_commands.go`

### 3.1 Agregar método a la interfaz TelegramCommander

```go
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
	Agent(ctx context.Context, message telego.Message) error  // <-- NUEVO
}
```

### 3.2 Implementar el método en la estructura cmd

Agregar al final del archivo, antes del cierre del package:

```go
func (c *cmd) Agent(ctx context.Context, message telego.Message) error {
	handler := NewAgentCommandHandler(&TelegramChannel{bot: c.bot, config: c.config})
	return handler.Execute(ctx, &message)
}
```

**Nota:** La implementación anterior usa una instancia temporal. Lo ideal es que el AgentCommandHandler sea un campo de TelegramChannel para acceder correctamente al agentLoop.

---

## Paso 4: Modificar Telegram.go

**Archivo:** `pkg/channels/telegram.go`

### 4.1 Agregar campo agentHandler a TelegramChannel

```go
type TelegramChannel struct {
	*BaseChannel
	bot          *telego.Bot
	commands     TelegramCommander
	config       *config.Config
	chatIDs      map[string]int64
	transcriber  *voice.GroqTranscriber
	placeholders sync.Map // chatID -> messageID
	stopThinking sync.Map // chatID -> thinkingCancel
	agentHandler *AgentCommandHandler  // <-- NUEVO
	agentLoop    *agent.AgentLoop      // <-- NUEVO (referencia al AgentLoop)
}
```

### 4.2 Modificar NewTelegramChannel para aceptar AgentLoop

```go
func NewTelegramChannel(cfg *config.Config, msgBus *bus.MessageBus, agentLoop *agent.AgentLoop) (*TelegramChannel, error) {
	var opts []telego.BotOption
	telegramCfg := cfg.Channels.Telegram

	if telegramCfg.Proxy != "" {
		proxyURL, parseErr := url.Parse(telegramCfg.Proxy)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", telegramCfg.Proxy, parseErr)
		}
		opts = append(opts, telego.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}))
	}

	bot, err := telego.NewBot(telegramCfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	base := NewBaseChannel("telegram", telegramCfg, msgBus, telegramCfg.AllowFrom)
	channel := &TelegramChannel{
		BaseChannel:  base,
		commands:     NewTelegramCommands(bot, cfg),
		bot:          bot,
		config:       cfg,
		chatIDs:      make(map[string]int64),
		transcriber:  nil,
		placeholders: sync.Map{},
		stopThinking: sync.Map{},
		agentLoop:    agentLoop,                           // <-- NUEVO
	}

	// Crear el handler de agentes
	channel.agentHandler = NewAgentCommandHandler(channel)  // <-- NUEVO

	return channel, nil
}
```

### 4.3 Registrar handler del comando /agent en Start()

En el método `Start()`, después de los handlers existentes, agregar:

```go
// Handler para /agent
bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
	return c.handleCommandWithSession(ctx, &message, "agent")
}, th.CommandEqual("agent"))

// Handler para callbacks de agentes
bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
	return c.agentHandler.HandleCallback(ctx, query)
}, th.AnyCallbackQueryWithMessage(), th.CallbackDataPrefix("agent:"))
```

### 4.4 Agregar /agent a los comandos del menú

En `Start()`, actualizar `SetMyCommands`:

```go
err = c.bot.SetMyCommands(ctx, &telego.SetMyCommandsParams{
	Commands: []telego.BotCommand{
		{Command: "new", Description: "Start a new conversation"},
		{Command: "stop", Description: "Stop the agent"},
		{Command: "agent", Description: "Show and switch between agents"},  // <-- NUEVO
		{Command: "model", Description: "Show models and change current model"},
		{Command: "models", Description: "Select provider/model from UI"},
		{Command: "status", Description: "Show model, tokens and gateway version"},
		{Command: "compact", Description: "Compact conversation history and save tokens"},
		{Command: "subagents", Description: "List and manage running subagents"},
		{Command: "verbose", Description: "Toggle verbose mode for tool execution"},
	},
})
```

### 4.5 Actualizar manejador de comandos

En `handleCommandWithSession()`, agregar el case para "agent":

```go
func (c *TelegramChannel) handleCommandWithSession(ctx context.Context, message *telego.Message, cmd string) error {
	// ... código existente ...
	switch cmd {
	case "new":
		// ... código existente ...
	case "agent":
		return c.agentHandler.Execute(ctx, message)
	// ... otros casos ...
	}
}
```

---

## Paso 5: Modificar Manager.go (Cambios en Creación de Channels)

**Archivo:** `pkg/channels/manager.go`

Buscar donde se crea el TelegramChannel y pasar el agentLoop:

```go
// En el método que crea los canales (probablemente en StartAll o similar)
if cfg.Channels.Telegram.Enabled {
	// ACTUALIZAR esta línea para pasar agentLoop
	tgChannel, err := NewTelegramChannel(cfg, msgBus, agentLoop)
	if err != nil {
		return fmt.Errorf("failed to create telegram channel: %w", err)
	}
	cm.channels["telegram"] = tgChannel
}
```

**Nota:** Esto requiere que el `AgentLoop` ya esté creado antes de inicializar los canales.

---

## Paso 6: Integración con el Enrutamiento de Mensajes

**Archivo:** `pkg/agent/loop.go` (Modificar processMessage)

Modificar `processMessage` para respetar el agente de sesión:

```go
func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// ... código de logging existente ...

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	// Check for commands
	if response, handled := al.handleCommand(ctx, msg); handled {
		return response, nil
	}

	// === NUEVO: Verificar si hay un agente específico para esta sesión ===
	sessionAgentID := ""
	if msg.SessionKey != "" {
		sessionAgentID = al.GetSessionAgent(msg.SessionKey)
	}

	var agent *AgentInstance
	var route routing.ResolvedRoute

	if sessionAgentID != "" {
		// Usar el agente de sesión si está configurado
		if a, ok := al.registry.GetAgent(sessionAgentID); ok {
			agent = a
			route = routing.ResolvedRoute{
				AgentID:    sessionAgentID,
				SessionKey: msg.SessionKey,
				MatchedBy:  "session_override",
			}
		}
	}

	if agent == nil {
		// Route normal si no hay override de sesión
		route = al.registry.ResolveRoute(routing.RouteInput{
			Channel:    msg.Channel,
			AccountID:  msg.Metadata["account_id"],
			Peer:       extractPeer(msg),
			ParentPeer: extractParentPeer(msg),
			GuildID:    msg.Metadata["guild_id"],
			TeamID:     msg.Metadata["team_id"],
		})

		var ok bool
		agent, ok = al.registry.GetAgent(route.AgentID)
		if !ok {
			agent = al.registry.GetDefaultAgent()
		}
	}
	// === FIN NUEVO ===

	// ... resto del código ...
}
```

---

## Paso 7: Actualización de main.go para pasar AgentLoop

**Archivo:** `cmd/picoclaw/main.go`

En la función `gatewayCmd()`, donde se crea el ChannelManager:

```go
// Después de crear agentLoop
agentLoop := agent.NewAgentLoop(cfg, msgBus, provider)

// Configurar el ChannelManager con el agentLoop
channelManager, err := channels.NewManager(cfg, msgBus, agentLoop)
if err != nil {
    fmt.Printf("Error creating channel manager: %v\n", err)
    os.Exit(1)
}
```

Luego modificar `channels.NewManager` para aceptar el parámetro.

---

## Flujo de Datos Completo

```
Usuario envía: /agent
    ↓
TelegramChannel.handleCommandWithSession()
    ↓
AgentCommandHandler.Execute()
    → Obtiene currentAgentID desde agentLoop.GetSessionAgent()
    → Llama sendAgentCard()
        → Obtiene AgentInstance con agentLoop.GetAgentInstance()
        → Formatea tarjeta con formatAgentCard()
        → Obtiene lista de agentes con getAvailableAgentsInfo()
        → Construye botones con buildAgentButtons()
        → Envía mensaje a Telegram
    ↓
Usuario presiona botón "→ coder"
    ↓
TelegramChannel recibe callback "agent:switch:coder"
    ↓
AgentCommandHandler.HandleCallback()
    → Parsea: action="switch", agentID="coder"
    → Llama agentLoop.SetSessionAgent(sessionKey, "coder")
    → Actualiza mensaje con sendAgentCard()
    → Responde callback con confirmation
    ↓
Mensajes siguientes en la sesión:
    ↓
AgentLoop.processMessage()
    → Detecta sessionAgentID con GetSessionAgent()
    → Usa el agente "coder" en lugar del default
```

---

## Ejemplo de Configuración (config.json)

```json
{
  "agents": {
    "defaults": {
      "model": "gpt-4o",
      "provider": "openai",
      "workspace": "~/.picoclaw/workspace"
    },
    "list": [
      {
        "id": "main",
        "name": "General",
        "default": true,
        "model": "gpt-4o"
      },
      {
        "id": "coder",
        "name": "Programador",
        "model": "claude-sonnet-4",
        "workspace": "~/.picoclaw/workspace-coder",
        "skills": ["fmod", "git", "docker"]
      },
      {
        "id": "browser",
        "name": "Navegador",
        "model": "gpt-4o",
        "skills": ["web_search", "web_fetch"]
      }
    ]
  }
}
```

---

## Checklist de Implementación

- [ ] Paso 1: Agregar métodos públicos en `agent/loop.go`
- [ ] Paso 2: Crear archivo `channels/telegram_agent.go`
- [ ] Paso 3: Modificar `channels/telegram_commands.go`
- [ ] Paso 4: Modificar `channels/telegram.go`
- [ ] Paso 5: Actualizar `channels/manager.go` con agentLoop
- [ ] Paso 6: Modificar enrutamiento en `agent/loop.go`
- [ ] Paso 7: Actualizar `main.go` para pasar agentLoop
- [ ] Compilar y probar: `go build ./cmd/picoclaw`
- [ ] Verificar comando `/agent` en Telegram
- [ ] Probar cambio de agente con botones
- [ ] Verificar que los mensajes usan el agente seleccionado

---

## Notas Importantes

1. **Persistencia**: El agente seleccionado se guarda en memoria (`sessionModels` sync.Map). 
   - Si el gateway se reinicia, las sesiones vuelven al agente default.
   - Para persistencia a disco, se necesitaría guardar en el state manager.

2. **Permisos**: Todos los usuarios pueden cambiar de agente en su sesión.
   - No hay restricciones de autorización implementadas.
   - Si se necesita, agregar verificación en `SetSessionAgent`.

3. **Múltiples Chats**: Cada chat (grupo o privado) tiene su propia sesión.
   - El cambio de agente en un chat no afecta otros.

4. **Fallback**: Si un agente es removido de la config pero está en sesión,
   - `GetAgentInstance` retornará `ok=false`
   - El sistema debe manejar esto y volver al default

---

## Posibles Mejoras Futuras

1. Persistencia del agente seleccionado en disco (usar `state.Manager`)
2. Comando `/agent reset` para volver al agente default
3. Mostrar skills disponibles con iconos en la tarjeta
4. Agregar descripción del agente en la config y mostrarla
5. Soporte para grupos: permitir que admins fijen un agente para el grupo