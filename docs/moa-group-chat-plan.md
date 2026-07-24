# Mixture of Agents (MoA) / Chat Grupal Multi-Agente — Plan de Diseño

> Estado: PROPUESTA · Autor: lele (orchestrator) · Fecha: 2026-07-24
> Objetivo: permitir que 2+ agentes colaboren en un espacio conversacional compartido,
> viéndose entre sí y construyendo sobre las respuestas de los otros.

---

## 1. Concepto y alcance

"Mixture of Agents" (MoA, paper de Together AI) es específicamente el patrón
*propose → aggregate* por capas: varios agentes proponen en paralelo y un agregador
sintetiza, iterando por capas. El usuario lo describe como "un chat grupal donde 2+
agentes colaboran". Para cubrir ambos, este plan diseña un **framework general de
colaboración multi-agente** donde MoA es **una estrategia más** entre varias:

| Estrategia    | Descripción                                                               | Uso típico                          |
|---------------|---------------------------------------------------------------------------|-------------------------------------|
| `round_robin` | Cada agente habla por turnos, todos ven lo anterior                       | Debate, review cruzado              |
| `moa`         | Capas: N proponentes en paralelo → agregador sintetiza → se repite L capas | Calidad de respuesta (paper MoA)    |
| `moderator`   | Un coordinador LLM decide dinámicamente quién habla y cuándo converger    | Paneles expertos, brainstorm        |
| `pipeline`    | Salida de A alimenta a B (relay secuencial especializado)                 | Cadena de especialización           |

### Diferencia con subagentes actuales
- **Subagentes (`spawn`)**: padre→hijo, aislados, 1 resultado al padre, sin contexto compartido entre hermanos. Session key `subagent:<id>`.
- **Grupo MoA**: N participantes, transcripción **compartida**, múltiples turnos, output intermedio visible. Session key `group:<groupId>`.

No se reemplaza `spawn`; conviven. Un agente orquestador podrá incluso abrir un grupo
como herramienta (ver §5.2).

---

## 2. Mapeo sobre la arquitectura existente

Piezas que se **reutilizan** (no se reinventan):

| Existente                                                    | Se usa para                                              |
|--------------------------------------------------------------|----------------------------------------------------------|
| `AgentRegistry` / `AgentInstance` (`pkg/agent`)              | Resolver participantes (provider, model, persona, tools) |
| `ContextBuilder.BuildMessages / GetInitialContext`           | Ensamblar system prompt + contexto por agente            |
| `buildSubagentSystemPrompt` (`subagent_prompt.go`)           | Plantilla de persona; extender a "persona de grupo"      |
| `MessageBus` (`OutboundMessage.Event/IsIntermediate/Metadata`)| Stream de turnos al canal/UI                             |
| `session.SessionManager` / `Session`                         | Persistencia de la transcripción del grupo               |
| Semáforo `sessionProcessing` en `AgentLoop.runAgentLoop`     | Evitar pisar otras sesiones del mismo agente             |
| `loop_detector.go`                                            | Detección de convergencia / anti-loop del grupo          |
| `commandHandler.handleCommand`                                | Comando `/group`                                         |
| `config.AgentConfig` / `SubagentsConfig` / `AgentBinding`    | Base para el schema de grupos                            |

Paquete nuevo: **`pkg/group`** (orquestación) + un turno ligero en `pkg/agent`.

---

## 3. Modelo de datos (tipos nuevos en `pkg/group`)

```go
// Participante: un agente con un rol dentro del grupo.
type Participant struct {
    AgentID string // resuelto contra AgentRegistry
    Role    string // "proposer" | "aggregator" | "moderator" | "critic" | ""
    Label   string // nombre mostrable en la transcripción
}

// Turn: una intervención en la transcripción compartida.
type Turn struct {
    Index     int
    Layer     int       // capa MoA (0..L); 0 para round_robin/pipeline
    Speaker   string    // AgentID
    Label     string
    Content   string
    CreatedAt time.Time
    Tokens    int
}

// GroupState: estado vivo + persistible de un grupo.
type GroupState struct {
    ID           string        // group:<id>
    ProfileID    string
    Task         string        // consigna/objetivo del grupo
    Participants []Participant
    Strategy     string
    Transcript   []Turn        // transcripción compartida (ordenada)
    Status       string        // running | done | stopped | error
    CreatedAt    time.Time
    UpdatedAt    time.Time
    TotalTokens  int
}

// GroupProfile: configuración declarativa (viene de config.Groups).
type GroupProfile struct {
    ID               string   `json:"id"`
    Participants     []string `json:"participants"`               // agent IDs
    Strategy         string   `json:"strategy"`                   // round_robin|moa|moderator|pipeline
    Rounds           int      `json:"rounds,omitempty"`           // capas MoA / vueltas round_robin
    Moderator        string   `json:"moderator,omitempty"`        // agente moderador/agregador
    MaxTurns         int      `json:"max_turns,omitempty"`
    MaxTokensPerTurn int      `json:"max_tokens_per_turn,omitempty"`
    TotalTokenBudget int      `json:"total_token_budget,omitempty"`
    StopKeywords     []string `json:"stop_keywords,omitempty"`    // ej. ["CONSENSUS","FINAL"]
    Parallel         bool     `json:"parallel,omitempty"`         // turnos paralelos dentro de una capa
}
```

### Interfaz de estrategia (pluggable)
```go
type Strategy interface {
    Name() string
    // Next devuelve los próximos hablantes (puede ser >1 si Parallel) y si terminó.
    Next(state *GroupState) (speakers []string, done bool, err error)
}
```
Implementaciones: `RoundRobinStrategy`, `MoAStrategy`, `ModeratorStrategy`, `PipelineStrategy`.

---

## 4. Ejecución de un turno (turno ligero)

`runAgentLoop` actual es pesado (tool loop completo + persistencia de sesión propia).
Para un turno de grupo necesitamos controlar el contexto con precisión (transcripción
compartida como contexto, persona propia del agente). Se propone un método nuevo en
`llmRunner`:

```go
type GroupTurnOptions struct {
    Group       *GroupState
    Speaker     *AgentInstance
    Transcript  []Turn     // transcripción compartida renderizada
    Instruction string     // consigna de este turno (varía por estrategia)
    EnableTools bool       // si el agente puede usar tools (ej. coder necesita exec)
    MaxTokens   int
}
func (lr *llmRunnerImpl) runGroupTurn(ctx context.Context, opts GroupTurnOptions) (string, int, error)
```

Construcción de mensajes por turno:
1. System prompt = persona del agente (`GetInitialContext`/`buildSubagentSystemPrompt`)
   + **anexo de rol de grupo** ("Estás en un panel con X, Y. Ves sus aportes. Aporta...").
2. Contexto = transcripción compartida renderizada con etiquetas de hablante:
   ```
   [arquitecto]: ...
   [coder]: ...
   ```
   inyectada como bloque de contexto (no como historial propio del agente).
3. Instruction = la consigna de la estrategia para este turno.
4. Se **adquiere el semáforo** `sessionProcessing` del agente con una session key
   derivada `group:<groupId>:<agentId>` para no bloquear sus otras sesiones, y se
   respeta `context.Cancel` propagado.

> Decisión abierta: si `EnableTools=true`, reutilizar el tool loop existente acotado;
> si es false, llamada LLM directa (más barato y predecible para MoA puro).

---

## 5. Puntos de entrada

### 5.1 Comando `/group`
En `commandHandler`:
- `/group start <profile> <task...>` — arranca un grupo desde un `GroupProfile`.
- `/group start --agents a,b,c --strategy moa <task...>` — ad-hoc.
- `/group status [id]` / `/group stop [id]` / `/group list`.

### 5.2 Tool `group_chat` (para orquestadores)
Análogo a `spawn`: un agente (ej. el main/orquestador) delega un problema difícil a un
panel y recibe la síntesis. Registra en `tools/` como `group_chat.go`. Devuelve el
resultado final agregado al `ToolResult.ForLLM`.

### 5.3 Binding de config
Extender `AgentBinding`/config para que un *peer grupal* (ej. un grupo de Telegram)
dispare colaboración multi-agente según un `GroupProfile`, de modo que los mensajes a
ese chat se procesen como grupo.

---

## 6. Schema de configuración

Nuevo bloque `groups` en `config.Config`:
```json
{
  "groups": {
    "list": [
      {
        "id": "review-panel",
        "participants": ["architect", "coder", "security-auditor"],
        "strategy": "moa",
        "rounds": 2,
        "moderator": "architect",
        "max_turns": 12,
        "max_tokens_per_turn": 4096,
        "total_token_budget": 60000,
        "stop_keywords": ["CONSENSUS", "FINAL"],
        "parallel": true
      }
    ]
  }
}
```
Validación en `ReloadAgents`/`config`: participantes deben existir en `agents.list`,
`moderator` debe ser participante, `strategy` válido.

---

## 7. Capa de presentación (TUI + WebUI)

Un turno de grupo viaja por el **mismo pipeline que los eventos existentes**
(`message.stream`, `tool.executing`, `subagent.result`). Hay 3 capas y en cada una
hay un punto de inserción concreto.

### 7.1 Eventos (contrato compartido)

| Evento           | Cuándo                          | Metadata                                                            |
|------------------|---------------------------------|---------------------------------------------------------------------|
| `group.status`   | inicio/fin/cancel del grupo     | `group_id`, `status` (started/done/stopped/error), `participants`   |
| `group.turn`     | cada intervención de un agente  | `group_id`, `speaker`, `label`, `role`, `layer`, `turn_index`       |
| `group.complete` | síntesis final agregada         | `group_id`, `strategy`, `layers`, `total_tokens`                    |

- `group.turn` se publica con `IsIntermediate: true` (no detiene el typing indicator).
- La síntesis final se envía **además** como mensaje assistant normal (para que quede en
  el historial de la sesión y se renderice como respuesta canonical).

Publicación desde `pkg/group/runner.go`:
```go
bus.OutboundMessage{
    Channel: originChannel, ChatID: originChatID,
    Event: "group.turn",
    Content: turn.Content,
    IsIntermediate: true,
    Metadata: map[string]string{
        "group_id": g.ID, "speaker": turn.Speaker, "label": turn.Label,
        "role": p.Role, "layer": strconv.Itoa(turn.Layer),
        "turn_index": strconv.Itoa(turn.Index),
    },
}
```

### 7.2 Puente canal nativo → WebSocket (lo que consume la WebUI)

Archivo: `pkg/channels/native.go`, func `dispatchOutboundMessage` (switch sobre `msg.Event`).
- **T7.2.1** Añadir tipos `WSGroupTurnPayload`, `WSGroupStatusPayload`, `WSGroupCompletePayload`
  (junto a `WSStreamPayload`/`WSToolResultPayload`, ~línea 61+).
- **T7.2.2** Añadir `case "group.status" / "group.turn" / "group.complete":` que llamen a
  `n.emitNativeEvent(sessionKey, "group.turn", WSGroupTurnPayload{...}, "")`.
 Patrón idéntico al `case "subagent.result"` existente (~línea 483).

### 7.3 WebUI (frontend React/TS, `web/src`)

Sigue el patrón de subagents (`useSubagents.ts` + `SubagentsSidebar.tsx`).
- **T7.3.1** `web/src/lib/types.ts`: añadir variantes `group.status`/`group.turn`/`group.complete`
  a la unión `ClientEvent` (~línea 700-760) + tipo `GroupTurn`/`GroupInfo`.
- **T7.3.2** `web/src/hooks/messageEventHandlers.ts`: añadir `handleGroupTurn`,
  `handleGroupStatus`, `handleGroupComplete` y registrarlos en el mapa `HANDLERS` (~línea 521).
  Deben acumular turnos por `group_id` en el estado de mensajes (agrupados por `layer`).
- **T7.3.3** Nuevo hook `web/src/hooks/useGroups.ts` (espejo de `useSubagents.ts`):
  estado/ciclo de vida del grupo, polling opcional mientras `status==started`.
- **T7.3.4** Nuevo componente `web/src/components/organisms/GroupChatPanel.tsx`
  (o `molecules/GroupTurnBubble.tsx`): renderiza turnos agrupados por capa con etiqueta
  de hablante + badge de capa + role; capas colapsables. Reutiliza `MarkdownText.tsx`.
- **T7.3.5** Panel de participantes (espejo de `SubagentsSidebar.tsx`): lista participantes
  con su role y estado. Cablear en la página de chat (`components/pages/`) y `App.tsx`.
- **T7.3.6** i18n: strings en `web/src/i18n` (etiquetas "Capa N", "Síntesis final", roles).

### 7.4 TUI (BubbleTea, `pkg/tui`)

Sigue el patrón del sidebar de subagents (`subagentClickTargets`, `subagentProgress`).
- **T7.4.1** `pkg/tui/types.go`: struct `groupTurn` + campos en `Model`
  (`groupTranscripts map[string][]groupTurn`, `activeGroupID string`, `groupStatus`).
- **T7.4.2** `pkg/tui/handlers.go`: en `case outboundMsg` (switch sobre `msg.msg.Event`,
  ~línea 671) añadir `case "group.status" / "group.turn" / "group.complete":` que acumulen
  el turno y llamen `m.updateViewport()`. Mantener `m.processing=true` hasta `group.complete`.
- **T7.4.3** `pkg/tui/viewport.go` / `view.go`: render de turnos como bloque etiquetado
  (`┌ [label · capa N · role]` + contenido), distinguible del assistant normal; la síntesis
  final como respuesta canonical. Estilo en `pkg/tui/style.go`.
- **T7.4.4** (Opcional) sección de participantes en el sidebar, espejo de `subagentClickTargets`.
- **T7.4.5** i18n: strings en `pkg/tui/i18n`.

### 7.5 Render en historial (persistencia)

Para que al reabrir una sesión se vea el grupo, los turnos deben persistir. Opción simple:
guardar la transcripción como mensajes `assistant` con prefijo de hablante en la sesión, o
como metadata del `GroupState` persistido (§8) y reconstruir la vista al cargar.
Decisión: persistir `GroupState` completo y renderizar desde ahí (no contaminar el historial
assistant de la sesión individual de cada agente).

---

## 8. Concurrencia, persistencia y seguridad

- **Concurrencia**: turnos paralelos de una capa con `errgroup`; cada turno adquiere el
  semáforo de sesión de su agente. Mutex de grupo para append a `Transcript`.
- **Cancelación**: `GroupManager` (análogo a `SubagentManager`) con `cancelAll()`; se
  engancha en `ReloadRegistry` igual que `toolCoordinator.cancelAll()`.
- **Persistencia**: `GroupState` se persiste como `Session` (reutilizar patrón de
  `session.SessionManager`) bajo el dir global de sesiones, key `group:<id>`.
- **Límites / anti-loop**: `MaxTurns`, `TotalTokenBudget`, `MaxTokensPerTurn`,
  `StopKeywords`, y detección de convergencia (delta decreciente / repeticiones)
  reutilizando ideas de `loop_detector.go`.
- **Permisos**: respetar `CanSpawnSubagent`/allowlists: un agente sólo puede abrir un
  grupo con agentes que tenga permitidos.

---

## 9. Roadmap de implementación (tareas atómicas para `coder`)

> Cada fase es modular, atómica y enfocada. El orden respeta dependencias.
> Regla: cada tarea = UN cambio específico con límites claros.

### FASE 0 — Esqueleto y tipos (sin lógica)
- **T0.1** Crear `pkg/group/types.go` con `Participant`, `Turn`, `GroupState`, `GroupProfile`, constantes de status/role.
- **T0.2** Crear `pkg/group/strategy.go` con la interfaz `Strategy` y un registry de estrategias (mapa nombre→factory). Sin implementaciones aún.
- **T0.3** Añadir bloque `GroupsConfig`/`GroupProfile` a `pkg/config/config.go` (+ `document_types.go`) con validación básica y tests de parse JSON.

### FASE 1 — Estrategias (lógica pura, testeable en aislamiento)
- **T1.1** `pkg/group/strategy_roundrobin.go` + test: orden cíclico, respeta `Rounds`/`MaxTurns`.
- **T1.2** `pkg/group/strategy_moa.go` + test: capas de proponentes en paralelo → agregador; cuenta capas hasta `Rounds`.
- **T1.3** `pkg/group/strategy_pipeline.go` + test: relay secuencial A→B→C, done al llegar al último.
- **T1.4** `pkg/group/strategy_moderator.go` + test: decisión de próximo hablante vía callback LLM (mock en test); condiciones de stop.
- **T1.5** `pkg/group/convergence.go` + test: detección de stop por keywords, presupuesto y delta decreciente (apoyarse en `loop_detector.go`).

### FASE 2 — Turno ligero de grupo
- **T2.1** Añadir `GroupTurnOptions` y `runGroupTurn` a `pkg/agent/llm_runner.go`: construye messages (persona + transcripción renderizada + instruction), llamada LLM directa (sin tools primero), devuelve contenido + tokens.
- **T2.2** `pkg/group/render.go` + test: render de transcripción compartida con etiquetas `[label]:` y anexo de rol de grupo para el system prompt.
- **T2.3** Adquirir/liberar semáforo `sessionProcessing` con key `group:<id>:<agentId>` dentro de `runGroupTurn` (test de no-bloqueo de otras sesiones).
- **T2.4** (Opcional) Soporte `EnableTools=true` reutilizando el tool loop acotado.

### FASE 3 — Orquestador (GroupManager)
- **T3.1** `pkg/group/manager.go`: `GroupManager` con `Start/Stop/Status/List`, registro de grupos activos, `cancelAll()`, mutex de transcripción.
- **T3.2** `pkg/group/runner.go`: loop principal — pide `Next()` a la estrategia, ejecuta turnos (paralelos con `errgroup` si `Parallel`), append a transcripción, publica `group.turn` al bus, evalúa convergencia, sintetiza respuesta final.
- **T3.3** Cablear `GroupManager` en `AgentLoop` (`loop.go`): instanciación, `cancelAll()` en `ReloadRegistry`, inyección de `AgentRegistry` + `MessageBus` + `llmRunner`.
- **T3.4** Persistencia de `GroupState` reutilizando patrón de `session.SessionManager` (save/load bajo dir global, key `group:<id>`).

### FASE 4 — Puntos de entrada
- **T4.1** Comando `/group` en `commandHandler` (`command_handler.go`): `start|status|stop|list`, parseo de flags `--agents/--strategy`.
- **T4.2** Tool `group_chat` en `pkg/tools/group_chat.go` (análogo a `spawn.go`): schema, validación de permisos vía `CanSpawnSubagent`, retorno de síntesis en `ToolResult.ForLLM`. Registrar en `base.go`.
- **T4.3** Binding de config: ruta de peer grupal → `GroupProfile` en `messageProcessor.processMessage` (detectar y derivar al `GroupManager`).

### FASE 5 — Capa de presentación (TUI + WebUI) y hardening

> Detalle completo y puntos de inserción por archivo en **§7**. El contrato de eventos
> (`group.status`/`group.turn`/`group.complete`) se define en §7.1.

Backend → WebSocket (puente):
- **T5.1** (=T7.2.1/T7.2.2) `pkg/channels/native.go`: tipos `WSGroupTurnPayload`/`WSGroupStatusPayload`/`WSGroupCompletePayload` + `case "group.*"` en `dispatchOutboundMessage` (patrón `subagent.result`).

WebUI (frontend React/TS):
- **T5.2** (=T7.3.1) `web/src/lib/types.ts`: variantes `group.*` en la unión `ClientEvent` + tipos `GroupTurn`/`GroupInfo`.
- **T5.3** (=T7.3.2) `web/src/hooks/messageEventHandlers.ts`: handlers `handleGroupTurn/Status/Complete` + registro en `HANDLERS` (acumular turnos por `group_id`/`layer`).
- **T5.4** (=T7.3.3) Nuevo `web/src/hooks/useGroups.ts` (espejo de `useSubagents.ts`).
- **T5.5** (=T7.3.4/T7.3.5) `components/organisms/GroupChatPanel.tsx` (turnos por capa, colapsables) + panel de participantes (espejo `SubagentsSidebar.tsx`); cablear en página de chat y `App.tsx`.
- **T5.6** (=T7.3.6) i18n WebUI (`web/src/i18n`).

TUI (BubbleTea):
- **T5.7** (=T7.4.1/T7.4.2) `pkg/tui/types.go` (struct `groupTurn` + campos en `Model`) y `pkg/tui/handlers.go` (`case "group.*"` en `outboundMsg`, ~línea 671).
- **T5.8** (=T7.4.3/T7.4.4) `pkg/tui/viewport.go`/`view.go`/`style.go`: render de turnos etiquetados por capa + síntesis canonical; (opcional) participantes en sidebar.
- **T5.9** (=T7.4.5) i18n TUI (`pkg/tui/i18n`).

Hardening:
- **T5.10** Tests de integración E2E: grupo round_robin y moa con provider mock (`mock_provider_test.go`), verificando orden de turnos, streaming de `group.turn` y síntesis final.
- **T5.11** Test de persistencia/render en historial: reabrir sesión y reconstruir vista del grupo desde `GroupState` (§7.5).
- **T5.12** Docs: sección en `README.md` + `docs/` con ejemplos de config, comandos y screenshots de TUI/WebUI.

---

## 10. Riesgos y decisiones abiertas

1. **Costo/latencia**: MoA multiplica llamadas LLM (N proponentes × L capas + agregador).
   Mitigar con `TotalTokenBudget`, `MaxTurns`, `parallel` y modelos baratos para proponentes.
2. **Contexto compartido vs ventana**: la transcripción crece y puede exceder el context
   window de agentes con ventanas chicas. Decisión: resumir capas anteriores (usar
   `Summary`/compaction como en sesiones) antes de inyectar.
3. **Tools en turnos de grupo**: permitir `exec`/tools dentro de un turno paralelo puede
   causar carreras en el workspace. Decisión inicial: `EnableTools=false` por defecto;
   habilitar sólo en `pipeline`/`moderator` con agente único por turno.
4. **Convergencia del moderador**: un moderador LLM puede no converger. Tope duro
   `MaxTurns` + presupuesto como red de seguridad.
5. **Persistencia de sesiones propias**: decidir si los agentes además guardan su
   historial individual del grupo (para reanudar) o sólo la transcripción compartida.

---

## 11. Criterios de éxito

- [ ] `/group start review-panel <task>` ejecuta un panel MoA y devuelve síntesis final.
- [ ] Los turnos intermedios se ven en streaming con etiqueta de hablante.
- [ ] `round_robin`, `moa`, `pipeline`, `moderator` funcionan y tienen tests unitarios.
- [ ] `group_chat` tool permite a un orquestador delegar a un panel.
- [ ] Respeto de `MaxTurns`/`TotalTokenBudget` (no hay loops infinitos).
- [ ] `GroupManager.cancelAll()` no deja goroutines al recargar config.
- [ ] Cobertura >80% en `pkg/group`.
