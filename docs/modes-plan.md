# Plan: 3 Modos (Chat / Agent / Group) — TUI + WebUI

## 1. Overview

Agregar 3 modos de operación a la TUI y WebUI de Lele:

| Modo | Tools | System Prompt | Sesiones |
|------|-------|---------------|----------|
| **Chat** | Solo `web_search` + `web_fetch` | Mínimo (sin AGENT.md, SOUL.md, skills, memory) | Tag `mode=chat` |
| **Agent** | Todos (implementación actual) | Completo (AGENT.md, SOUL.md, USER.md, IDENTITY.md, MEMORY.md, skills, harness) | Tag `mode=agent` (default) |
| **Group** | N/A (usa group runner) | N/A (cada agente usa su propio prompt) | Sesiones de grupo (`group:<id>`), tag `mode=group` |

**Switching:**
- TUI: `TAB` cicla entre modos (cuando NO hay autocomplete activo)
- WebUI: Tab selector en el sidebar, debajo del icono de search

**Historial:** Cada modo muestra solo las sesiones de ese modo.

---

## 2. Arquitectura de Sesiones con Modo

### 2.1 Campo `Mode` en `session.Session`

```go
// pkg/session/manager.go
type Session struct {
    // ... campos existentes ...
    Mode string `json:"mode,omitempty"` // "chat", "agent", "group" (default: "agent")
}
```

- Default vacío = `"agent"` (backward compatible con sesiones existentes)
- Se persiste en el JSON de la sesión

### 2.2 Métodos nuevos en `SessionManager`

```go
func (sm *SessionManager) GetMode(key string) string
func (sm *SessionManager) SetMode(key string, mode string) error
func (sm *SessionManager) ListSessionsByMode(mode string) []*Session
```

`ListSessionsByMode` filtra por modo. Modo vacío matchea `"agent"` (backward compat).

### 2.3 `sessionMetadata` también lleva `Mode`

```go
type sessionMetadata struct {
    Key     string    `json:"key"`
    Name    string    `json:"name"`
    Mode    string    `json:"mode,omitempty"`
    Created time.Time `json:"created"`
    Updated time.Time `json:"updated"`
}
```

Esto permite filtrar sin cargar mensajes del disco.

---

## 3. Backend: Chat Mode (Tool Filtering + Minimal Prompt)

### 3.1 Tool Filtering en `llm_runner.go`

En `runLLMIteration()`, después de `agent.Tools.ToProviderDefs()`, filtrar según el modo de la sesión:

```go
// Después de providerToolDefs := agent.Tools.ToProviderDefs()
sessionMode := agent.Sessions.GetMode(opts.SessionKey)
if sessionMode == "chat" {
    chatToolSet := map[string]bool{"web_search": true, "web_fetch": true}
    filtered := make([]providers.ToolDefinition, 0, 2)
    for _, def := range providerToolDefs {
        if chatToolSet[def.Function.Name] {
            filtered = append(filtered, def)
        }
    }
    providerToolDefs = filtered
}
```

**Ubicación exacta:** `pkg/agent/llm_runner.go`, dentro del loop `for iteration < agent.MaxIterations`, justo después de la línea `providerToolDefs := agent.Tools.ToProviderDefs()` (~línea 225) y antes del filtro de `read_image`.

### 3.2 Minimal System Prompt en `ContextBuilder`

Nuevo método en `pkg/agent/context.go`:

```go
func (cb *ContextBuilder) BuildMinimalSystemPrompt() string {
    return `You are a helpful AI assistant. You can search the web and fetch web pages to answer questions.

## Available Tools

- ` + "`web_search`" + ` - Search the web for current information
- ` + "`web_fetch`" + ` - Fetch a URL and extract readable content

## Rules

1. Use tools when you need current information or to read a specific URL.
2. Be helpful, accurate, and concise.
3. Cite sources when using web search results.`
}
```

### 3.3 Integración en `BuildMessages`

En `ContextBuilder.BuildMessages()`, el system prompt se construye con `buildSystemPromptForTurn()`. Necesitamos que el modo influya:

**Opción elegida:** Pasar el modo como parámetro a `BuildMessages` y `buildSystemPromptForTurn`.

```go
func (cb *ContextBuilder) BuildMessages(history []providers.Message, summary string,
    currentMessage string, attachments []bus.FileAttachment,
    channel, chatID, sessionKey string, mode string) []providers.Message {

    // ...
    if mode == "chat" {
        systemPrompt = cb.BuildMinimalSystemPrompt()
    } else {
        systemPrompt = cb.buildSystemPromptForTurn(currentMessage, channel, chatID)
    }
    // ...
}
```

**Nota:** El cache de system prompt por sesión ya existe (`cachedSystemPrompt`), así que el modo se respeta automáticamente una vez cacheado.

### 3.4 Caller chain update

`BuildMessages` es llamado desde `llm_runner.go:runAgentLoop()`. Ahí se obtiene el modo:

```go
sessionMode := agent.Sessions.GetMode(opts.SessionKey)
messages := agent.ContextBuilder.BuildMessages(
    history, summary, opts.UserMessage, persistedAttachments,
    opts.Channel, opts.ChatID, opts.SessionKey, sessionMode,
)
```

### 3.5 Tool Execution Guard

Además de filtrar las definiciones de tools (que evita que el LLM las invoque), agregar un guard en `tool_executor.go` para rechazar tools no permitidas en modo chat:

```go
// En tool_executor.go, antes de ejecutar:
if sessionMode == "chat" && toolName != "web_search" && toolName != "web_fetch" {
    return tools.NewErrorResult("Tool not available in chat mode")
}
```

Esto es defense-in-depth por si el LLM alucina un tool call.

---

## 4. Backend: API Changes

### 4.1 `CreateSessionRequest` acepta `mode`

```go
// pkg/channels/types.go
type CreateSessionRequest struct {
    SessionKey string `json:"session_key"`
    Mode       string `json:"mode,omitempty"` // "chat", "agent", "group"
}
```

### 4.2 `handleCreateSession` setea el modo

```go
// pkg/channels/rest_chat.go
func (n *NativeChannel) handleCreateSession(w http.ResponseWriter, r *http.Request) {
    // ... existing code ...
    if req.Mode != "" {
        n.agentLoop.GetProvidable().SetSessionMode(req.SessionKey, req.Mode)
    }
    // ...
}
```

### 4.3 `ChatSession` response incluye `mode`

```go
type ChatSession struct {
    Key          string    `json:"key"`
    Name         string    `json:"name,omitempty"`
    Mode         string    `json:"mode,omitempty"`
    Created      time.Time `json:"created"`
    Updated      time.Time `json:"updated"`
    MessageCount int       `json:"message_count"`
}
```

### 4.4 `handleChatSessions` soporta filtro por modo

Query param opcional: `GET /api/v1/chat/sessions?mode=chat`

```go
func (n *NativeChannel) handleChatSessions(w http.ResponseWriter, r *http.Request) {
    modeFilter := r.URL.Query().Get("mode") // "", "chat", "agent", "group"
    // ... al construir la lista, filtrar por modo si modeFilter != ""
}
```

### 4.5 `AgentProvidable` interface extension

```go
// pkg/channels/types.go (AgentProvidable interface)
SetSessionMode(sessionKey, mode string)
GetSessionMode(sessionKey string) string
```

Implementado en `agent_providable.go` delegando a `SessionManager`.

### 4.6 Group sessions en la API

Las sesiones de grupo ya usan el patrón `group:<groupId>`. Para el modo Group:

- `GET /api/v1/chat/sessions?mode=group` → retorna sesiones con key `group:*` o taggeadas `mode=group`
- Taggear sesiones de grupo con `mode=group` en `group.Manager.Start()` cuando crea la sesión

---

## 5. TUI: Mode Switching + Session Filtering

### 5.1 Nuevo tipo y campo en `Model`

```go
// pkg/tui/types.go
type chatMode int

const (
    ModeAgent chatMode = iota  // default
    ModeChat
    ModeGroup
)

func (m chatMode) String() string {
    switch m {
    case ModeChat: return "chat"
    case ModeGroup: return "group"
    default: return "agent"
    }
}

// En Model struct:
type Model struct {
    // ... existing fields ...
    currentMode chatMode // modo activo (default: ModeAgent)
}
```

### 5.2 TAB handler en `handlers.go`

En el `switch msg.String()` principal (cuando `modalMode == ModalNone` y `!showAutocomplete`):

```go
case "tab":
    // Ciclar modo: agent → chat → group → agent
    switch m.currentMode {
    case ModeAgent:
        m.currentMode = ModeChat
    case ModeChat:
        m.currentMode = ModeGroup
    case ModeGroup:
        m.currentMode = ModeAgent
    }
    m.reloadSessions()
    m.updateViewport()
    return m, nil
```

**Importante:** TAB solo cicla modos cuando NO hay autocomplete activo. Cuando `showAutocomplete == true`, TAB completa el comando (comportamiento existente, ya implementado).

### 5.3 `reloadSessions()` filtra por modo

```go
func (m *Model) reloadSessions() {
    m.renderedBaseKey = ""
    m.visibleSessions = nil
    all := m.sessionMgr.ListSessions()

    modeStr := m.currentMode.String()

    for _, s := range all {
        if isSubagentSessionKey(s.Key) {
            continue
        }
        // Filtrar por modo
        sessionMode := s.Mode
        if sessionMode == "" {
            sessionMode = "agent" // backward compat
        }
        if sessionMode != modeStr {
            continue
        }
        if len(s.Messages) > 0 || s.Key == m.currentKey {
            m.visibleSessions = append(m.visibleSessions, s)
        }
    }
    // ... resto igual ...
}
```

### 5.4 `createNewChat()` setea modo

```go
func (m *Model) createNewChat() {
    newKey := fmt.Sprintf("tui:chat:%s", uuid.New().String())
    m.sessionMgr.GetOrCreate(newKey)
    m.sessionMgr.SetMode(newKey, m.currentMode.String())
    // ... resto igual ...
}
```

### 5.5 Indicador de modo en la UI

En `view.go`, agregar un indicador de modo:

**Welcome screen:** Mostrar tabs de modo junto al model/agent selector:
```
  Chat  [Agent]  Group
```
El modo activo se resalta con color (usar `brand-rosa` / estilo existente).

**Chat view:** En la barra de estado inferior o en el header del viewport, mostrar un badge compacto:
```
[AGENT]  glm-4.7  ·  main  ·  thinking: off
```

### 5.6 Group Mode en TUI

Cuando `currentMode == ModeGroup`:

**Welcome screen:** En lugar del input normal, mostrar:
1. Lista de perfiles de grupo disponibles (de `config.Groups.List`)
2. Input para el task
3. Al enviar, ejecuta `/group start <profileID> <task>`

**Chat view:** Las sesiones de grupo se muestran con sus turns (ya implementado via `groupTranscripts`, `groupStatus`, `groupMeta`).

**Input en Group mode:**
- Si hay perfiles configurados: mostrar selector de perfil + input de task
- Si no hay perfiles: mostrar input para comando ad-hoc (`/group start --agents a,b --strategy moa <task>`)

### 5.7 `/sessions` command filtra por modo

```go
case "/sessions":
    m.resetModal(ModalSessions)
    allSessions := m.sessionMgr.ListSessions()
    modeStr := m.currentMode.String()
    for _, s := range allSessions {
        if isSubagentSessionKey(s.Key) {
            continue
        }
        sessionMode := s.Mode
        if sessionMode == "" {
            sessionMode = "agent"
        }
        if sessionMode != modeStr {
            continue
        }
        // ... resto igual ...
    }
```

### 5.8 i18n keys

Agregar en `pkg/tui/i18n/` para es/en/pt:

```
tui.modeChat = "Chat"
tui.modeAgent = "Agent"
tui.modeGroup = "Group"
tui.modeIndicator = "Mode: %s"
tui.tabHint = "TAB: switch mode"
tui.groupSelectProfile = "Select group profile:"
tui.groupTaskPlaceholder = "Describe the task for the group..."
tui.noGroupProfiles = "No group profiles configured"
```

---

## 6. WebUI: Mode Selector + Session Filtering

### 6.1 Mode state en `useAppLogic`

```typescript
// web/src/hooks/useAppLogic.ts
type ChatMode = 'chat' | 'agent' | 'group'

const [chatMode, setChatMode] = useState<ChatMode>(() => {
    return (localStorage.getItem('lele_chat_mode') as ChatMode) || 'agent'
})

const selectMode = useCallback((mode: ChatMode) => {
    setChatMode(mode)
    localStorage.setItem('lele_chat_mode', mode)
}, [])
```

### 6.2 `AppLogicContext` expone mode

```typescript
// web/src/contexts/AppLogicContext.tsx
export type AppLogicContextValue = {
    // ... existing ...
    chatMode: ChatMode
    onSelectMode: (mode: ChatMode) => void
}
```

### 6.3 `ModeSelector` component

Nuevo componente: `web/src/components/molecules/ModeSelector.tsx`

```tsx
// 3 tabs: Chat | Agent | Group
// Posición: en el Sidebar, debajo del botón de search
// Estilo: segmented control / pill tabs
export function ModeSelector() {
    const { chatMode, onSelectMode } = useAppLogicContext()
    const { t } = useTranslation()

    const modes = [
        { id: 'chat', label: t('mode.chat'), icon: ChatIcon },
        { id: 'agent', label: t('mode.agent'), icon: AgentIcon },
        { id: 'group', label: t('mode.group'), icon: GroupIcon },
    ] as const

    return (
        <div className="flex rounded-lg bg-surface-hover p-0.5 gap-0.5">
            {modes.map(mode => (
                <button
                    key={mode.id}
                    onClick={() => onSelectMode(mode.id)}
                    className={`flex-1 flex items-center justify-center gap-1.5
                        rounded-md px-2 py-1.5 text-xs font-medium transition-colors
                        ${chatMode === mode.id
                            ? 'bg-background-primary text-brand-rosa shadow-sm'
                            : 'text-text-secondary hover:text-text-primary'}`}
                >
                    <mode.icon size={14} />
                    <span>{mode.label}</span>
                </button>
            ))}
        </div>
    )
}
```

### 6.4 Integración en `Sidebar.tsx`

Insertar `<ModeSelector />` debajo del botón de search, antes de la sección "Recent":

```tsx
// En Sidebar.tsx, después del botón de search:
{!collapsed && (
    <div className="px-3 py-2">
        <ModeSelector />
    </div>
)}
{collapsed && (
    // Versión compacta: 3 iconos en vertical con tooltip
    <div className="flex flex-col items-center gap-1 py-2">
        {modes.map(mode => (
            <IconButton key={mode.id} onClick={() => onSelectMode(mode.id)}
                className={chatMode === mode.id ? 'text-brand-rosa' : ''}>
                <mode.icon size={16} />
            </IconButton>
        ))}
    </div>
)}
```

### 6.5 Session filtering por modo

En `Sidebar.tsx`, filtrar `sortedSessions` por modo:

```typescript
const sortedSessions = useMemo(() => {
    const visible = sessions.filter(s => {
        if (s.key.startsWith('subagent:')) return false
        const sessionMode = s.mode || 'agent'
        if (sessionMode !== chatMode) return false
        return s.message_count > 0 || s.key === selectedKey
    })
    return [...visible].sort(
        (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
    )
}, [sessions, selectedKey, chatMode])
```

### 6.6 `ChatSession` type update

```typescript
// web/src/lib/types.ts
export type ChatSession = {
    key: string
    name?: string
    mode?: string  // "chat" | "agent" | "group"
    created: string
    updated: string
    message_count: number
}
```

### 6.7 Session creation con modo

```typescript
// useChatSessions.ts
const createSession = useCallback(async (mode?: string): Promise<string | null> => {
    const sessionKey = generateUUID()
    // ...
    await api.createSession(sessionKey, mode)
    // ...
}, [clientId, persistCurrentSessionKey, api])
```

En `useAppLogic.ts`, `onCreateSession` pasa el modo actual:

```typescript
const onCreateSession = useCallback(async () => {
    const key = await createSession(chatMode)
    if (key) navigate(`/chat/${key}`)
}, [createSession, chatMode, navigate])
```

### 6.8 API client update

```typescript
// web/src/lib/api.ts
createSession(sessionKey: string, mode?: string) {
    return this.post('/api/v1/chat/sessions', { session_key: sessionKey, mode })
}

sessions(mode?: string) {
    const params = mode ? `?mode=${mode}` : ''
    return this.get(`/api/v1/chat/sessions${params}`)
}
```

### 6.9 Group Mode en WebUI

Cuando `chatMode === 'group'`:

**Sidebar:** Muestra sesiones de grupo (taggeadas `mode=group`).

**ChatPage:** En lugar del `ChatComposer` normal, mostrar un `GroupComposer`:

Nuevo componente: `web/src/components/molecules/GroupComposer.tsx`

```tsx
export function GroupComposer() {
    const { wsSend } = useAppLogicContext()
    const [groupProfiles, setGroupProfiles] = useState<GroupProfile[]>([])
    const [selectedProfile, setSelectedProfile] = useState('')
    const [task, setTask] = useState('')

    // Cargar perfiles de config via API
    useEffect(() => {
        api.getConfig().then(cfg => setGroupProfiles(cfg.groups?.list || []))
    }, [])

    const handleStart = () => {
        if (!task.trim()) return
        wsSend('message', {
            content: `/group start ${selectedProfile} ${task}`,
            session_key: currentSessionKey,
        })
        setTask('')
    }

    return (
        <div className="space-y-3">
            <select value={selectedProfile} onChange={e => setSelectedProfile(e.target.value)}
                className="w-full rounded-md border border-border bg-surface-hover px-3 py-2 text-sm">
                <option value="">Select profile...</option>
                {groupProfiles.map(p => (
                    <option key={p.id} value={p.id}>
                        {p.id} ({p.strategy}, {p.participants.length} agents)
                    </option>
                ))}
            </select>
            <textarea value={task} onChange={e => setTask(e.target.value)}
                placeholder="Describe the task for the group..."
                className="w-full rounded-md border border-border bg-surface-hover px-3 py-2 text-sm"
                rows={3} />
            <button onClick={handleStart} disabled={!task.trim() || !selectedProfile}
                className="w-full rounded-md bg-brand-rosa px-4 py-2 text-sm font-medium text-white">
                Start Group
            </button>
        </div>
    )
}
```

**MessageList:** Ya renderiza group turns via `GroupChatPanel`. Los turns se muestran inline.

### 6.10 Chat Mode en WebUI

Cuando `chatMode === 'chat'`:
- Sesiones filtradas por `mode=chat`
- El `ChatComposer` funciona igual (envía mensajes normalmente)
- El backend se encarga de filtrar tools y usar prompt mínimo
- Opcional: mostrar un badge "Chat Mode" en el `ChatHeader`

### 6.11 i18n keys (WebUI)

Agregar en `web/src/i18n/` para es/en/pt:

```json
{
    "mode.chat": "Chat",
    "mode.agent": "Agent",
    "mode.group": "Group",
    "mode.chatDescription": "Simple chat with web search",
    "mode.agentDescription": "Full agent with all tools",
    "mode.groupDescription": "Multi-agent group chat",
    "group.selectProfile": "Select group profile",
    "group.taskPlaceholder": "Describe the task for the group...",
    "group.start": "Start Group",
    "group.noProfiles": "No group profiles configured"
}
```

---

## 7. Fases de Implementación

### Fase 1: Backend — Session Mode Infrastructure
**Tasks para coder:**

1. **T1.1** — `pkg/session/manager.go`: Agregar campo `Mode` a `Session` struct y `sessionMetadata`. Agregar métodos `GetMode()`, `SetMode()`, `ListSessionsByMode()`. Actualizar `loadSessionMetadata()` y `saveUnlocked()` para persistir el modo.

2. **T1.2** — `pkg/channels/types.go`: Agregar `Mode` a `ChatSession`, `CreateSessionRequest`. Agregar `SetSessionMode`/`GetSessionMode` a la interface `AgentProvidable`.

3. **T1.3** — `pkg/agent/agent_providable.go`: Implementar `SetSessionMode()` y `GetSessionMode()` delegando a `SessionManager`.

4. **T1.4** — `pkg/channels/rest_chat.go`: Actualizar `handleCreateSession` para aceptar y setear modo. Actualizar `handleChatSessions` para soportar query param `?mode=` y retornar el campo `mode` en cada sesión.

### Fase 2: Backend — Chat Mode (Tool Filtering + Minimal Prompt)
**Tasks para coder:**

5. **T2.1** — `pkg/agent/context.go`: Agregar `BuildMinimalSystemPrompt()`. Modificar `BuildMessages()` para aceptar parámetro `mode string` y usar el prompt mínimo cuando `mode == "chat"`.

6. **T2.2** — `pkg/agent/llm_runner.go`: En `runAgentLoop()`, obtener el modo de la sesión y pasarlo a `BuildMessages()`. En `runLLMIteration()`, filtrar `providerToolDefs` para modo chat (solo `web_search` + `web_fetch`).

7. **T2.3** — `pkg/agent/tool_executor.go`: Agregar guard de defense-in-depth para rechazar tools no permitidas en modo chat.

### Fase 3: TUI — Mode Switching + Session Filtering
**Tasks para coder:**

8. **T3.1** — `pkg/tui/types.go`: Agregar tipo `chatMode` con constantes `ModeAgent`, `ModeChat`, `ModeGroup`. Agregar campo `currentMode` a `Model`.

9. **T3.2** — `pkg/tui/handlers.go`: Agregar handler de `TAB` para ciclar modos (solo cuando `!showAutocomplete && modalMode == ModalNone`).

10. **T3.3** — `pkg/tui/model.go`: Modificar `reloadSessions()` para filtrar por `currentMode`. Modificar `createNewChat()` para setear el modo en la nueva sesión.

11. **T3.4** — `pkg/tui/view.go`: Agregar indicador de modo (tabs `Chat | Agent | Group`) en la welcome screen y en la chat view. Resaltar el modo activo.

12. **T3.5** — `pkg/tui/commands.go`: Modificar `/sessions` para filtrar por modo actual.

13. **T3.6** — `pkg/tui/i18n/`: Agregar keys de i18n para los 3 idiomas (es/en/pt).

### Fase 4: WebUI — Mode Selector + Session Filtering
**Tasks para coder:**

14. **T4.1** — `web/src/lib/types.ts`: Agregar `mode` a `ChatSession`. Agregar tipo `ChatMode`.

15. **T4.2** — `web/src/lib/api.ts`: Actualizar `createSession()` y `sessions()` para soportar modo.

16. **T4.3** — `web/src/hooks/useAppLogic.ts`: Agregar state `chatMode` + `selectMode`. Persistir en localStorage. Exponer en el return.

17. **T4.4** — `web/src/contexts/AppLogicContext.tsx`: Agregar `chatMode` y `onSelectMode` al context.

18. **T4.5** — `web/src/components/molecules/ModeSelector.tsx`: Crear componente de segmented control con 3 tabs.

19. **T4.6** — `web/src/components/organisms/Sidebar.tsx`: Integrar `ModeSelector` debajo del search. Filtrar sesiones por modo. Pasar modo a `onCreateSession`.

20. **T4.7** — `web/src/hooks/useChatSessions.ts`: Actualizar `createSession()` para aceptar y enviar modo.

21. **T4.8** — `web/src/i18n/`: Agregar keys de i18n para los 3 idiomas.

### Fase 5: Group Mode UI
**Tasks para coder:**

22. **T5.1** — TUI Group mode: En `view.go`, cuando `currentMode == ModeGroup`, mostrar selector de perfil + task input en welcome screen. En `handlers.go`, manejar submit en group mode (ejecutar `/group start`).

23. **T5.2** — WebUI `GroupComposer.tsx`: Crear componente con selector de perfil, textarea de task, botón start. Integrar en `ChatPage.tsx` cuando `chatMode === 'group'`.

24. **T5.3** — Backend: Taggear sesiones de grupo con `mode=group` en `group.Manager.Start()` o en `agent/group_turn.go`.

### Fase 6: Integration + Polish
**Tasks para coder:**

25. **T6.1** — Tests: Unit tests para `GetMode`/`SetMode`/`ListSessionsByMode`. Test de tool filtering en modo chat. Test de `BuildMinimalSystemPrompt`.

26. **T6.2** — Build verification: `go build ./...`, `golangci-lint run`, `cd web && bun run build`.

27. **T6.3** — Edge cases: Modo chat sin web_search configurado (mostrar warning). Modo group sin perfiles configurados (mostrar mensaje). Switch de modo con sesión activa (no interrumpir processing).

---

## 8. Consideraciones de Diseño

### 8.1 Backward Compatibility
- Sesiones existentes sin campo `Mode` se tratan como `"agent"` (default)
- El campo `mode` es `omitempty` en JSON, así que sesiones viejas no se rompen
- La API sin query param `?mode=` retorna todas las sesiones (comportamiento actual)

### 8.2 Session Key Patterns
- No cambiamos los patrones de session key existentes
- TUI: `tui:chat:<uuid>` (todos los modos)
- WebUI: `<uuid>` (todos los modos)
- Group: `group:<groupId>` (existente)
- El modo se almacena como metadata de la sesión, no en la key

### 8.3 Mode Persistence
- TUI: El modo actual NO se persiste entre restarts (siempre arranca en Agent)
- WebUI: El modo se persiste en `localStorage` por cliente
- El modo de cada sesión SÍ se persiste en el JSON de la sesión

### 8.4 Tool Filtering Strategy
- Filtrado en 2 niveles:
  1. **Definiciones de tools** (llm_runner.go): El LLM no ve tools que no puede usar
  2. **Execution guard** (tool_executor.go): Defense-in-depth si el LLM alucina un tool call
- El registro de tools en `AgentInstance` NO cambia (todos los tools siguen registrados)

### 8.5 Group Mode Session Lifecycle
- Al iniciar un grupo desde la UI, se crea una sesión con `mode=group`
- Los turns del grupo se almacenan en esa sesión
- La sesión aparece en el historial del modo Group
- El `/group` command sigue funcionando desde cualquier modo (no está restringido)

### 8.6 No Breaking Changes
- El `/group` command existente sigue funcionando igual
- Los subagents no se ven afectados
- Los canales externos (Telegram, Discord) no se ven afectados (siempre usan modo agent)
- La API REST es backward compatible (mode es opcional)

---

## 9. Archivos a Modificar (Resumen)

### Backend (Go)
| Archivo | Cambio |
|---------|--------|
| `pkg/session/manager.go` | Campo `Mode`, métodos Get/Set/ListByMode |
| `pkg/channels/types.go` | `Mode` en ChatSession, CreateSessionRequest, AgentProvidable |
| `pkg/agent/agent_providable.go` | Implementar SetSessionMode/GetSessionMode |
| `pkg/channels/rest_chat.go` | handleCreateSession + handleChatSessions con modo |
| `pkg/agent/context.go` | BuildMinimalSystemPrompt, BuildMessages con mode param |
| `pkg/agent/llm_runner.go` | Tool filtering + mode passing |
| `pkg/agent/tool_executor.go` | Execution guard para modo chat |
| `pkg/group/manager.go` | Taggear sesiones con mode=group |

### TUI (Go)
| Archivo | Cambio |
|---------|--------|
| `pkg/tui/types.go` | Tipo chatMode, campo currentMode |
| `pkg/tui/handlers.go` | TAB handler, group mode submit |
| `pkg/tui/model.go` | reloadSessions con filtro, createNewChat con modo |
| `pkg/tui/view.go` | Indicador de modo, group mode welcome |
| `pkg/tui/commands.go` | /sessions filtrado por modo |
| `pkg/tui/i18n/*.go` | Keys de i18n |

### WebUI (TypeScript/React)
| Archivo | Cambio |
|---------|--------|
| `web/src/lib/types.ts` | ChatMode type, mode en ChatSession |
| `web/src/lib/api.ts` | createSession/sessions con mode |
| `web/src/hooks/useAppLogic.ts` | chatMode state + selectMode |
| `web/src/contexts/AppLogicContext.tsx` | Exponer chatMode |
| `web/src/components/molecules/ModeSelector.tsx` | **NUEVO** — Segmented control |
| `web/src/components/molecules/GroupComposer.tsx` | **NUEVO** — Group start form |
| `web/src/components/organisms/Sidebar.tsx` | Integrar ModeSelector, filtrar sesiones |
| `web/src/hooks/useChatSessions.ts` | createSession con mode |
| `web/src/components/pages/ChatPage.tsx` | GroupComposer cuando mode=group |
| `web/src/i18n/*.ts` | Keys de i18n |
