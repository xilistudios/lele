# Implementación de SYSTEM_SPAWN para Cron Jobs

## Resumen
Se ha implementado la opción A: el messageProcessor del agente principal detecta el prefijo `SYSTEM_SPAWN:` y llama a `spawn` para ejecutar tareas programadas como subagentes.

## Cambios Realizados

### 1. CronPayload (pkg/cron/service.go)
Agregado soporte para configuración de spawn:

```go
type SpawnConfig struct {
    Task     string `json:"task"`
    Label    string `json:"label,omitempty"`
    AgentID  string `json:"agent_id,omitempty"`
    Guidance string `json:"guidance,omitempty"`
}

type CronPayload struct {
    // ... campos existentes ...
    Spawn   *SpawnConfig `json:"spawn,omitempty"`
}
```

### 2. CronTool (pkg/tools/cron.go)

#### Actualizaciones:
- **Parámetros**: Añadido parámetro `spawn` al schema de la herramienta
- **addJob()**: Añadido parsing de spawn config desde los argumentos
- **ExecuteJob()**: Añadida lógica para generar mensaje `SYSTEM_SPAWN:` cuando hay spawn config
- **formatSystemSpawnMessage()**: Nueva función para formatear el mensaje

#### Formato del mensaje SYSTEM_SPAWN:
```
SYSTEM_SPAWN:
TASK: Crear backup de la base de datos PostgreSQL y subirlo a S3
LABEL: backup-diario
AGENT_ID: coder
GUIDANCE: Usa pg_dump y aws cli. El bucket es backups-db
CONTEXT: Backup diario de base de datos
```

### 3. MessageProcessor (pkg/agent/message_processor.go)

Agregadas funciones para manejar SYSTEM_SPAWN:

- **ProcessDirectWithChannel()**: Ahora detecta prefijo `SYSTEM_SPAWN:` y redirige a handleSystemSpawn
- **handleSystemSpawn()**: Obtiene el SubagentManager y ejecuta el spawn
- **parseSystemSpawnMessage()**: Parsea el mensaje SYSTEM_SPAWN
- **spawnConfig struct**: Estructura para almacenar la configuración parseada

### Flujo Completo

1. **Usuario crea job cron con spawn**:
```json
{
  "action": "add",
  "message": "Backup diario de base de datos",
  "cron_expr": "0 2 * * *",
  "spawn": {
    "task": "Crear backup de la base de datos PostgreSQL y subirlo a S3",
    "agent_id": "coder",
    "label": "backup-diario"
  }
}
```

2. **CronTool.addJob()** almacena el job con `Payload.Spawn`

3. **Cuando llega la hora**, CronTool.ExecuteJob() detecta `Payload.Spawn` y genera mensaje SYSTEM_SPAWN:
```
SYSTEM_SPAWN:
TASK: Crear backup de la base de datos PostgreSQL y subirlo a S3
LABEL: backup-diario
AGENT_ID: coder
GUIDANCE: Usa pg_dump y aws cli. El bucket es backups-db
```

4. **messageProcessor.ProcessDirectWithChannel()** detecta `SYSTEM_SPAWN:` y llama a `handleSystemSpawn()`

5. **handleSystemSpawn()**:
   - Obtiene el SubagentManager del agente actual
   - Construye un callback para notificar al usuario cuando termine
   - Llama a `subagentManager.Spawn()` para crear el subagente
   - Retorna inmediatamente "Scheduled task spawned"

6. **Subagente ejecuta** la tarea en segundo plano y reporta vía callback

7. **Callback** envía resultado al usuario original

## Ventajas de esta Implementación (Opción A)

✅ **Bajo acoplamiento**: CronTool no depende de la estructura interna de AgentLoop  
✅ **Flexible**: el callback puede hacer validaciones, logging, etc.  
✅ **Fácil de testear**: puedes mockear el callback  
✅ **Sigue el patrón existente**: inyección de dependencias  
✅ **Sin dependencia circular**: CronTool no conoce SubagentManager directamente  

## Tests

Se añadieron tests en `pkg/agent/message_processor_spawn_test.go`:
- `TestParseSystemSpawnMessage`: Verifica el parsing del mensaje
- `TestParseSystemSpawnMessage_Truncation`: Verifica truncamiento de labels largos

Todos los tests existentes continúan pasando.

## Ejemplo de Uso

### Crear un job programado que spawnea un subagente:
```
Usa el tool cron para programar un backup diario a las 2 AM:
{
  "action": "add",
  "message": "Backup diario de base de datos",
  "cron_expr": "0 2 * * *",
  "deliver": false,
  "spawn": {
    "task": "Crear backup de la base de datos PostgreSQL y subirlo a S3",
    "agent_id": "coder",
    "label": "backup-diario"
  }
}
```

### Resultado:
El LLM llamará a la herramienta `cron` con los parámetros apropiados y el job se creará con spawn config.

Cuando se ejecute, se spawneerá un subagente `coder` que realizará el backup y notificará el resultado.
