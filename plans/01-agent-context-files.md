# Plan: Incluir archivos de contexto al cambiar de agente

## Problema
Cuando se cambia de agente con el comando `/agent` en lele, no se incluyen las instrucciones iniciales (archivos AGENT.md, SOUL.md, MEMORY.md) del nuevo agente en el mensaje de confirmación ni se asegura que se carguen correctamente.

Cada agente tiene su workspace independiente con sus propios archivos de contexto:
- SOUL.md - Identidad del agente
- AGENT.md - Instrucciones específicas del agente
- MEMORY.md - Memoria a largo plazo
- USER.md - Información del usuario
- IDENTITY.md - Identidad adicional

## Análisis del código actual

### Flujo del comando `/agent`:
1. `handleAgentCommand` en `pkg/agent/command_handler.go`
2. Llama a `resetAgentSession(agent, sessionKey)` para limpiar el historial
3. Guarda el mapeo `sessionAgents[sessionKey] = agentID`
4. Retorna mensaje de confirmación

### Carga de archivos de contexto:
- `ContextBuilder.LoadBootstrapFiles()` carga AGENT.md, SOUL.md, USER.md, IDENTITY.md
- `ContextBuilder.GetInitialContext()` retorna el contexto completo incluyendo bootstrap files
- Cada `AgentInstance` tiene su propio `ContextBuilder` asociado a su workspace

### Problemas identificados:
1. El mensaje de `/agent` no menciona que se cargó el contexto del nuevo agente
2. No hay mensaje informativo sobre los archivos de contexto del nuevo agente
3. El usuario no tiene feedback visual de que el agente cambió completamente su contexto

## Solución propuesta

### Cambios a realizar:

1. **Modificar `handleAgentCommand` en `pkg/agent/command_handler.go`**:
   - Después de cambiar de agente, cargar explícitamente los archivos de contexto del nuevo agente
   - Generar un mensaje informativo que muestre qué archivos de contexto se cargaron
   - Mostrar información sobre el workspace del nuevo agente

2. **Verificar carga de contexto**:
   - Asegurar que `ContextBuilder.GetInitialContext()` se llame correctamente
   - El nuevo agente ya tiene su propio ContextBuilder, solo necesitamos usarlo

## Implementación

### Archivos a modificar:
- `pkg/agent/command_handler.go` - Función `handleAgentCommand`

### Cambios detallados:

En `handleAgentCommand`, después de cambiar el agente:
1. Obtener el contexto inicial del nuevo agente usando `agent.ContextBuilder.GetInitialContext()`
2. Verificar qué archivos existen en el workspace del nuevo agente
3. Actualizar el mensaje de respuesta para incluir:
   - Nombre del agente
   - Modelo
   - Workspace
   - Archivos de contexto cargados (SOUL.md, AGENT.md, etc.)

## Testing

1. Probar `/agent <agent_id>` y verificar que el mensaje incluye info de contexto
2. Verificar que los archivos del workspace correcto se cargan
3. Probar cambio entre agentes con diferentes workspaces

## Criterios de éxito

- [ ] Al cambiar de agente, el mensaje muestra el workspace y archivos de contexto
- [ ] El contexto del nuevo agente se carga correctamente
- [ ] El historial se limpia correctamente al cambiar de agente
