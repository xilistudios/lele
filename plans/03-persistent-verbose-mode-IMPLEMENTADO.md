# Plan 03: Persistencia del Modo Verbose - IMPLEMENTADO

## Estado: ✅ COMPLETADO

## Resumen de Cambios

### 1. Session Manager (`pkg/session/manager.go`)

- **Campo agregado a `Session`:** `VerboseMode bool`
- **Métodos agregados a `SessionManager`:**
  - `GetVerboseMode(key string) bool` - Obtiene el estado desde memoria
  - `SetVerboseMode(key string, enabled bool) error` - Guarda y persiste el estado
  - `saveUnlocked(key string) error` - Helper para guardar sin lock

- **Refactorización:** `Save()` ahora usa `saveUnlocked()` para evitar duplicación

### 2. Verbose Manager (`pkg/session/verbose.go`)

Cambio completo de implementación:
- Antes: Usaba solo `sync.Map` en memoria (se perdía al reiniciar)
- Ahora: Usa cache en memoria + `SessionManager` para persistencia

**Nuevos métodos:**
- `NewVerboseManager(sessions ...*SessionManager)` - Acepta SessionManager opcional
- `SetSessionManager(sm *SessionManager)` - Para configurar después de crear
- `InitializeFromSession(sessionKey string)` - Carga el estado desde persistencia

**Métodos actualizados:**
- `IsVerbose()` - Primero revisa cache, luego carga desde persistencia si es necesario
- `SetVerbose()` - Actualiza cache Y persiste en SessionManager
- `Toggle()` - Usa el nuevo `SetVerbose()` que persiste automáticamente

### 3. Agent Loop (`pkg/agent/loop.go`)

- **Modificado `NewAgentLoop()`:** Crea VerboseManager conectado al SessionManager del agente default
- **Modificado `runAgentLoop()`:** Llama a `InitializeFromSession()` al cargar el historial

## Flujo de Persistencia

```
Usuario: /verbose
    │
    ▼
AgentLoop.handleVerboseCommand()
    │
    ▼
VerboseManager.Toggle(sessionKey)
    │
    ▼
VerboseManager.SetVerbose(sessionKey, true)
    │
    ├──► Cache en memoria: vm.cache[sessionKey] = true
    │
    └──► Persistencia: sessions.SetVerboseMode(sessionKey, true)
              │
              ▼
         SessionManager guarda en archivo JSON
              │
              ▼
         workspace/sessions/telegram_1779224049.json
         {"verbose_mode": true, ...}

[Reinicio del gateway]

Usuario: [cualquier mensaje]
    │
    ▼
AgentLoop.runAgentLoop()
    │
    ▼
VerboseManager.InitializeFromSession(sessionKey)
    │
    ▼
Carga desde: sessions.GetVerboseMode(sessionKey)
    │
    └──► Lee del archivo JSON y actualiza cache
```

## Archivos Modificados

| Archivo | Cambios |
|---------|---------|
| `pkg/session/manager.go` | +130 líneas (nuevos métodos y refactorización) |
| `pkg/session/verbose.go` | Reescrito completo (~140 líneas) |
| `pkg/agent/loop.go` | +15 líneas (integración) |

## Backward Compatibility

- ✅ Sesiones antiguas sin `verbose_mode` funcionan (default: `false`)
- ✅ Campo `omitempty` en JSON no rompe unmarshaling
- ✅ Si no hay SessionManager, funciona como antes (solo en memoria)

## Ejemplo de Archivo de Sesión

```json
{
  "key": "telegram:1779224049",
  "messages": [...],
  "summary": "...",
  "verbose_mode": true,
  "created": "2026-02-23T02:00:00Z",
  "updated": "2026-02-23T02:30:00Z"
}
```

## Tiempo de Implementación

- Fase 1 (SessionStore): 30 min - ✅
- Fase 2 (VerboseManager): 45 min - ✅
- Fase 3 (AgentLoop): 20 min - ✅
- **Total: ~1.5 horas** (menos del estimado de 2.5h)
