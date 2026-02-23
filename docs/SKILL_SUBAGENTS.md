# Skill: Subagentes en picoclaw

## Descripción

Los subagentes son instancias independientes del agente que pueden ejecutar tareas en paralelo al agente principal. Desde la versión actual, los subagentes heredan automáticamente todas las herramientas del sistema del agente padre, permitiéndoles realizar operaciones complejas de forma autónoma.

## Capacidades de Subagentes

Los subagentes tienen acceso completo a las siguientes categorías de herramientas:

### 📁 Operaciones de Archivo
- `read_file` - Leer archivos del workspace
- `write_file` - Crear/escribir archivos
- `edit_file` - Editar archivos (reemplazo exacto)
- `append_file` - Añadir contenido al final
- `list_dir` - Listar directorios

### 🔧 Fmod - Edición Inteligente
- `smart_edit` - Edición con estrategias de fallback
- `preview` - Previsualizar cambios en archivos temporales
- `apply` - Aplicar cambios desde archivo temporal
- `patch` - Aplicar unified diffs
- `sequential_replace` - Múltiples reemplazos simultáneos

### 🌐 Web
- `web_fetch` - Descargar contenido de URLs
- `web_search` - Búsqueda web (requiere configuración API)

### ⚡ Sistema
- `exec` - Ejecutar comandos del sistema
- `message` - Enviar mensajes al usuario

### 🔌 Hardware (Linux)
- `i2c` - Comunicación I2C
- `spi` - Comunicación SPI

### 🔄 Subagentes Anidados
- `spawn` - Crear subagentes asíncronos
- `subagent` - Ejecutar subagente síncrono

## Uso Básico

### Crear un Subagente Asíncrono

```json
{
  "tool": "spawn",
  "args": {
    "task": "Analiza el archivo main.go y genera un resumen de las funciones principales",
    "label": "Análisis de main.go"
  }
}
```

El subagente heredará automáticamente el registro de herramientas completo del agente padre, permitiéndole:
1. Leer archivos con `read_file`
2. Realizar búsquedas web si está configurado
3. Enviar resultados con `message`

### Ejecutar Subagente Síncrono

```json
{
  "tool": "subagent",
  "args": {
    "task": "Refactoriza la función calculate() en utils.go para usar tipos más específicos",
    "label": "Refactor utils"
  }
}
```

## Ejemplos de Casos de Uso

### Ejemplo 1: Análisis de Código Independiente

El usuario pide: "Analiza todos los archivos Go del proyecto y encuentra funciones sin documentar"

El agente principal puede spawnear múltiples subagentes:
- Subagente 1: Analiza `pkg/agent/` y sus archivos
- Subagente 2: Analiza `pkg/tools/` y sus archivos
- Subagente 3: Analiza `pkg/providers/` y sus archivos

Cada subagente opera independientemente usando `list_dir`, `read_file`, y luego reporta resultados.

### Ejemplo 2: Búsqueda y Síntesis

El usuario pide: "Investiga las mejores prácticas de logging en Go 2024 y actualiza nuestro código"

Flujo del subagente:
1. `web_search` - Buscar "Go logging best practices 2024"
2. `web_fetch` - Descargar artículos relevantes
3. `read_file` - Leer implementación actual
4. `smart_edit` - Aplicar mejoras sugeridas

### Ejemplo 3: Generación de Documentación

El usuario pide: "Genera documentación para todos los tools en formato markdown"

El subagente puede:
1. `list_dir` - Encontrar archivos en `pkg/tools/`
2. `read_file` - Leer cada archivo
3. `write_file` - Crear `TOOLS_DOCUMENTATION.md`

## Seguridad

Los subagentes heredan las mismas restricciones de seguridad que el agente padre:

- **Path Traversal**: No pueden escribir fuera del workspace configurado
- **Comandos peligrosos**: El tool `exec` mantiene su lista negra
- **Tamaño de archivos**: Límites de 50MB para operaciones de archivo
- **Workspace Bound**: El workspace del subagente es el mismo que el padre

## Configuración de LLM

Los subagentes heredan la configuración de LLM del agente padre:
- Modelo por defecto
- Max tokens
- Temperature
- Proveedor de LLM

## Mejores Prácticas

### 1. Usar Labels Descriptivos

```json
{
  "label": "Procesar logs de error",
  "task": "Analiza los logs y extrae patrones de error"
}
```

Los labels aparecen en `/subagents` y facilitan el monitoreo.

### 2. Tareas Atómicas

Dividir trabajo complejo en subagentes específicos:
- ❌ "Arregla todo el proyecto"
- ✅ "Encuentra funciones sin test"
- ✅ "Genera tests unitarios para utils.go"

### 3. Manejo de Errores

Los subagentes reportan errores a través del canal `system`. El agente padre recibe notificaciones cuando:
- Un subagente completa exitosamente
- Un subagente falla
- Un subagente es cancelado

### 4. Recursión Controlada

Los subagentes pueden crear otros subagentes, pero:
- Cada nivel tiene `maxIterations` (default: 10)
- Los contextos se anidan: nivel 1 → nivel 2 → nivel 3
- En caso de loops, el límite de iteraciones prevenirá ciclos infinitos

## Limitaciones Conocidas

1. **Contexto de Sesión**: Los subagentes no comparten historial de mensajes con el padre
2. **Estado**: Cada subagente es stateless respecto al estado del padre
3. **Announces**: Los resultados se envían como mensajes de sistema, no se integran automáticamente en la conversación

## Tips Avanzados

### Inspeccionar Subagentes Activos

```
/subagents
```

Muestra todos los subagentes en ejecución con sus labels.

### Detener un Subagente Específico

```
/subagents stop subagent-123
```

### Monitoreo en Tiempo Real

Activar modo verbose antes de spawnear:
```
/verbose
```

Esto mostrará cada llamada a tool que hace el subagente.

## Trabajando con Resultados de Subagentes

Cuando un subagente completa, el sistema envía:

```
[Channel: system]
From: subagent:subagent-1
Task 'Análisis de código' completed.

Result:
- 5 funciones encontradas
- 2 sin documentar
- 1 con complejidad alta
```

El agente padre puede procesar estos resultados y:
- Solicitar acciones adicionales
- Sintetizar información de múltiples subagentes
- Presentar resumen final al usuario

---

## Referencia Técnica

### Implementación

La herencia de herramientas se implementa en `pkg/agent/loop.go`:

```go
subagentManager := tools.NewSubagentManager(provider, agent.Model, agent.Workspace, msgBus)
subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)
subagentManager.SetTools(agent.Tools) // ← Herencia completa
```

### Estructura de Datos

```go
type SubagentManager struct {
    tasks          map[string]*SubagentTask
    tools          *ToolRegistry  // ← Heredado del padre
    provider       providers.LLMProvider
    maxIterations  int
    maxTokens      int
    temperature    float64
    // ...
}
```

## Ejemplo Completo: Flujo de Trabajo

**Usuario**: "Mejora la documentación del proyecto"

**Agente Principal**:

1. Analiza la estructura del proyecto
2. Spawnea subagentes:
   - Subagente A: Documentar `pkg/agent/`
   - Subagente B: Documentar `pkg/tools/`
   - Subagente C: Documentar `pkg/channels/`

**Subagente A** (opera independientemente):

```
→ list_dir("pkg/agent")
→ read_file("loop.go")
→ web_search("Go agent patterns documentation")
→ write_file("docs/AGENT_PACKAGE.md", ...)
→ message("Documentación de agent completada")
```

**Resultado**:

Los 3 subagentes trabajan en paralelo, cada uno con acceso completo a herramientas, generando documentación simultáneamente.
