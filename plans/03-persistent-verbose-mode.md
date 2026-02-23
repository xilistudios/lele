# Plan: Persistencia del Modo Verbose

## Objetivo
Hacer que la preferencia del modo verbose (activado/desactivado) persista a través de reinicios del gateway, almacenándola junto con la sesión del usuario en lugar de mantenerla solo en memoria.

## Estado Actual del Problema

### Problema Identificado
El modo verbose se almacena actualmente en `session.VerboseManager` que mantiene un mapa en memoria:

```go
// pkg/session/verbose.go (actual)
type VerboseManager struct {
    states map[string]bool  // Se pierde al reiniciar
    mu     sync.RWMutex
}
```

Al reiniciar el gateway:
1. Se crea nueva instancia de AgentLoop
2. Se crea nuevo VerboseManager con mapa vacío
3. Todas las preferencias de verbose se pierden
4. Usuario debe volver a activar `/verbose` manualmente

### Impacto
- Mala experiencia de usuario tras reinicios
- Pérdida de configuración personalizada por sesión
- Inconsistencia: el historial sí persiste, pero verbose no

## Análisis de Soluciones

### Opción 1: Extender el sistema de sesiones (Recomendada)
**Pros:**
- Usa infraestructura existente de persistencia
- Naturalmente vinculado a sessionKey
- Sincronizado con lifecycle de sesiones

**Contras:**
- Requiere modificar estructura de SessionStore

### Opción 2: Archivo de configuración separado
**Pros:**
- Independiente de sesiones
- Podría ser global por agente

**Contras:**
- Más complejo de mantener
- Desincronizado con sesiones específicas

### Opción 3: Base de datos/State manager
**Pros:**
- Persistencia robusta

**Contras:**
- Sobre-ingeniería para un flag booleano
- Overhead adicional

**Decisión:** Implementar Opción 1 - extender SessionStore

## Fase 1: Extensión del Sistema de Sesiones

### 1.1 Modificar SessionData
**Archivo:** `pkg/session/store.go`

```go
// SessionData - estructura actual extendida
type SessionData struct {
    Messages    []ConversationMessage `json:"messages"`
    Summary     string                `json:"summary,omitempty"`
    LastAccess  int64                 `json:"last_access"`
    // NUEVO:
    VerboseMode bool                  `json:"verbose_mode,omitempty"`
}
```

### 1.2 Modificar SessionStore Interface
**Archivo:** `pkg/session/store.go`

```go
// SessionStore - interface extendida
type SessionStore interface {
    Load(sessionKey string) (*SessionData, error)
    Save(sessionKey string, data *SessionData) error
    Delete(sessionKey string) error
    List() ([]string, error)
    // NUEVO:
    SetVerboseMode(sessionKey string, enabled bool) error
    GetVerboseMode(sessionKey string) bool
}
```

### 1.3 Implementar en FileSessionStore
**Archivo:** `pkg/session/file_store.go`

```go
func (fs *FileSessionStore) SetVerboseMode(sessionKey string, enabled bool) error {
    fs.mu.Lock()
    defer fs.mu.Unlock()
    
    session, ok := fs.sessions[sessionKey]
    if !ok {
        session = &SessionData{
            Messages:   []ConversationMessage{},
            LastAccess: time.Now().Unix(),
        }
        fs.sessions[sessionKey] = session
    }
    
    session.VerboseMode = enabled
    session.LastAccess = time.Now().Unix()
    
    return fs.persist(sessionKey)
}

func (fs *FileSessionStore) GetVerboseMode(sessionKey string) bool {
    fs.mu.RLock()
    defer fs.mu.RUnlock()
    
    if session, ok := fs.sessions[sessionKey]; ok {
        return session.VerboseMode
    }
    return false  // Default: verbose desactivado
}
```

## Fase 2: Refactorizar VerboseManager

### 2.1 Nuevo Diseño Integrado
**Archivo:** `pkg/session/verbose.go` (modificado)

```go
// VerboseManager - ahora integrado con SessionStore
type VerboseManager struct {
    store SessionStore
    mu    sync.RWMutex
    cache map[string]bool  // Cache en memoria para performance
}

func NewVerboseManager(store SessionStore) *VerboseManager {
    return &VerboseManager{
        store: store,
        cache: make(map[string]bool),
    }
}

func (vm *VerboseManager) IsVerbose(sessionKey string) bool {
    vm.mu.RLock()
    if state, ok := vm.cache[sessionKey]; ok {
        vm.mu.RUnlock()
        return state
    }
    vm.mu.RUnlock()
    
    // Cargar desde store persistente
    state := vm.store.GetVerboseMode(sessionKey)
    
    vm.mu.Lock()
    vm.cache[sessionKey] = state
    vm.mu.Unlock()
    
    return state
}

func (vm *VerboseManager) Toggle(sessionKey string) bool {
    current := vm.IsVerbose(sessionKey)
    newState := !current
    
    // Persistir inmediatamente
    if err := vm.store.SetVerboseMode(sessionKey, newState); err != nil {
        // Fallback: solo actualizar cache si falla persistencia
        log.Printf("[WARN] Failed to persist verbose mode: %v", err)
    }
    
    vm.mu.Lock()
    vm.cache[sessionKey] = newState
    vm.mu.Unlock()
    
    return newState
}

func (vm *VerboseManager) Set(sessionKey string, enabled bool) {
    // Persistir inmediatamente
    if err := vm.store.SetVerboseMode(sessionKey, enabled); err != nil {
        log.Printf("[WARN] Failed to persist verbose mode: %v", err)
    }
    
    vm.mu.Lock()
    vm.cache[sessionKey] = enabled
    vm.mu.Unlock()
}
```

## Fase 3: Integración con AgentLoop

### 3.1 Modificar Inicialización
**Archivo:** `pkg/agent/loop.go`

```go
func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus, provider providers.LLMProvider) *AgentLoop {
    // ... código existente ...
    
    // Crear state manager usando default agent's workspace
    defaultAgent := registry.GetDefaultAgent()
    var stateManager *state.Manager
    if defaultAgent != nil {
        stateManager = state.NewManager(defaultAgent.Workspace)
    }
    
    // CAMBIO: Pasar stateManager a VerboseManager
    // Esto permite que verbose persista con las sesiones
    verboseManager := session.NewVerboseManagerWithStore(stateManager.GetSessionStore())
    
    return &AgentLoop{
        // ... campos existentes ...
        verboseManager: verboseManager,
        // ...
    }
}
```

### 3.2 Modificar handleVerboseCommand
**Archivo:** `pkg/agent/loop.go`

```go
func (al *AgentLoop) handleVerboseCommand(sessionKey string) string {
    if sessionKey == "" {
        return "Verbose mode requires a session context. Please start a conversation first."
    }
    
    newState := al.verboseManager.Toggle(sessionKey)
    
    // Guardar sesión inmediatamente para persistir el cambio
    if agent := al.registry.GetDefaultAgent(); agent != nil {
        agent.Sessions.Save(sessionKey)
    }
    
    if newState {
        return "🔊 Verbose mode **ENABLED**\nYou will now see real-time tool execution notifications."
    }
    return "🔇 Verbose mode **DISABLED**\nTool execution notifications are now hidden."
}
```

## Fase 4: Carga al Iniciar Sesión

### 4.1 Restaurar Estado al Cargar Historial
**Archivo:** `pkg/agent/loop.go` - en `runAgentLoop`

```go
func (al *AgentLoop) runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (string, error) {
    // ... código existente ...
    
    // Cargar historial y summary
    if !opts.NoHistory {
        history = agent.Sessions.GetHistory(opts.SessionKey)
        summary = agent.Sessions.GetSummary(opts.SessionKey)
        
        // NUEVO: Cargar preferencia de verbose al iniciar sesión
        if agent.Sessions.HasVerboseMode(opts.SessionKey) {
            verboseState := agent.Sessions.GetVerboseMode(opts.SessionKey)
            al.verboseManager.Set(opts.SessionKey, verboseState)
        }
    }
    
    // ... resto del código ...
}
```

## Fase 5: Consideraciones Técnicas

### 5.1 Migraión de Datos Existentes
Las sesiones existentes no tienen el campo `verbose_mode`, por lo que:

```go
// En GetVerboseMode - manejar campo ausente
func (fs *FileSessionStore) GetVerboseMode(sessionKey string) bool {
    fs.mu.RLock()
    defer fs.mu.RUnlock()
    
    if session, ok := fs.sessions[sessionKey]; ok {
        // Si no existe el campo, retorna false (default)
        return session.VerboseMode 
    }
    return false
}
```

### 5.2 Backward Compatibility
El cambio es backward compatible porque:
- Campo `omitempty` en JSON no rompe unmarshaling
- Valor por defecto `false` mantiene comportamiento actual
- Estructura extendida, no modificada

### 5.3 Performance
- Cache en memoria evita I/O constante
- Persistencia solo en cambios (toggle)
- Carga lazy al acceder

## Fase 6: Tests

### 6.1 Test: Persistencia Básica
```go
func TestVerboseModePersists(t *testing.T) {
    store := NewFileSessionStore(tempDir)
    vm := NewVerboseManager(store)
    
    // Activar verbose
    vm.Set("session-123", true)
    
    // Crear nuevo manager (simula reinicio)
    vm2 := NewVerboseManager(store)
    
    // Verificar que persiste
    assert.True(t, vm2.IsVerbose("session-123"))
}
```

### 6.2 Test: Toggle Persiste
```go
func TestVerboseTogglePersists(t *testing.T) {
    store := NewFileSessionStore(tempDir)
    vm := NewVerboseManager(store)
    
    // Toggle a true
    vm.Toggle("session-abc")
    
    // Nuevo manager
    vm2 := NewVerboseManager(store)
    
    assert.True(t, vm2.IsVerbose("session-abc"))
    
    // Toggle a false
    vm2.Toggle("session-abc")
    
    // Otro manager
    vm3 := NewVerboseManager(store)
    
    assert.False(t, vm3.IsVerbose("session-abc"))
}
```

### 6.3 Test: Múltiples Sesiones Independientes
```go
func TestVerboseModePerSessionIndependent(t *testing.T) {
    store := NewFileSessionStore(tempDir)
    vm := NewVerboseManager(store)
    
    vm.Set("session-1", true)
    vm.Set("session-2", false)
    
    vm2 := NewVerboseManager(store)
    
    assert.True(t, vm2.IsVerbose("session-1"))
    assert.False(t, vm2.IsVerbose("session-2"))
}
```

## Fase 7: Archivos a Modificar

| Archivo | Cambio | Líneas |
|---------|--------|--------|
| `pkg/session/store.go` | Agregar campo VerboseMode a SessionData | +1 |
| `pkg/session/store.go` | Agregar métodos a interface | +2 |
| `pkg/session/file_store.go` | Implementar SetVerboseMode/GetVerboseMode | +30 |
| `pkg/session/verbose.go` | Refactorizar para usar SessionStore | ~ refactor |
| `pkg/agent/loop.go` | Crear VerboseManager con store | +1 |
| `pkg/agent/loop.go` | Guardar al hacer toggle | +5 |
| `pkg/agent/loop.go` | Cargar al iniciar sesión | +5 |
| `pkg/agent/loop_test.go` | Tests de persistencia | +50 |

## Fase 8: Plan de Implementación

### Paso 1: Preparar SessionStore (30 min)
- [ ] Extender SessionData con VerboseMode
- [ ] Modificar SessionStore interface
- [ ] Implementar en FileSessionStore

### Paso 2: Refactorizar VerboseManager (45 min)
- [ ] Modificar estructura para usar SessionStore
- [ ] Actualizar IsVerbose, Toggle, Set
- [ ] Agregar cache para performance

### Paso 3: Integrar con AgentLoop (30 min)
- [ ] Modificar NewAgentLoop para pasar store
- [ ] Actualizar handleVerboseCommand
- [ ] Agregar carga al iniciar sesión

### Paso 4: Tests (45 min)
- [ ] Test de persistencia básica
- [ ] Test de toggle
- [ ] Test de múltiples sesiones
- [ ] Test de migración (campo ausente)

### Paso 5: Validación Manual (15 min)
- [ ] Activar verbose
- [ ] Reiniciar gateway
- [ ] Verificar que persiste
- [ ] Desactivar y verificar persistencia

**Tiempo Total Estimado:** ~2.5 horas

## Ejemplo de Flujo Completo

```
Usuario: /verbose
Agente:  🔊 Verbose mode ENABLED
        [Se guarda en: workspace/sessions/telegram_123.json]

[Reinicio del gateway]

Usuario: [cualquier mensaje]
Agente:  [autocarga verbose=true desde archivo de sesión]
        🔧 **Tool Call (1):** `read_file`
        ...

Usuario: /verbose
Agente:  🔇 Verbose mode DISABLED
        [Actualiza archivo de sesión]

[Otro reinicio]

Usuario: [cualquier mensaje]
Agente:  [verbose=false cargado, modo silencioso]
```

---

## Notas de Implementación

1. **Atomicidad**: Usar el mismo mecanismo de guardado atómico que las sesiones
2. **Cleanup**: Al truncar/borrar sesión, también limpiar flag de verbose
3. **Logging**: Agregar logs de debug al cargar/guardar preferencia
4. **Metrics**: Opcional, contar cuántas sesiones usan modo verbose
