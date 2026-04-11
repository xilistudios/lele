<div align="center">
  <img src="assets/logo.png" alt="Lele" width="320">

  <h1>Lele</h1>

  <p>Asistente personal de IA ligero y eficiente en Go.</p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="Licencia">
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | **Español** | [English](README.md)
</div>

---

Lele es un proyecto independiente centrado en ofrecer un asistente de IA práctico con una huella reducida, un inicio rápido y un modelo de despliegue sencillo.

Hoy en día, el proyecto es mucho más que un bot CLI mínimo. Incluye un runtime de agente configurable, una puerta de enlace multicanal, interfaz web, API de cliente nativa, tareas programadas, subagentes y un modelo de automatización centrado en el espacio de trabajo.

## Por Qué Lele

- Implementación ligera en Go con una huella operativa reducida
- Lo suficientemente eficiente para ejecutarse cómodamente en máquinas Linux modestas y placas de bajo consumo
- Un solo proyecto para CLI, canales de chat, interfaz web e integraciones con clientes locales
- Enrutamiento configurable de proveedores con soporte para backends directos y compatibles con OpenAI
- Diseño centrado en el espacio de trabajo con habilidades, memoria, tareas programadas y controles de aislamiento

## Capacidades Actuales

### Runtime del Agente

- Chat por CLI con `lele agent`
- Bucle de agente con uso de herramientas y límites de iteración configurables
- Archivos adjuntos en flujos nativos/web
- Persistencia de sesiones y sesiones efímeras opcionales
- Agentes con nombre, vinculaciones y modelos de respaldo

### Interfaces

- Uso por terminal a través del CLI
- Modo puerta de enlace para canales de chat
- Interfaz web integrada
- Canal de cliente nativo con API REST + WebSocket y emparejamiento por PIN

### Automatización

- Tareas programadas con `lele cron`
- Tareas periódicas basadas en latido desde `HEARTBEAT.md`
- Subagentes asíncronos para trabajo delegado
- Sistema de habilidades para flujos de trabajo reutilizables

### Seguridad y Operaciones

- Restricción al espacio de trabajo
- Patrones de bloqueo de comandos peligrosos para herramientas exec
- Flujo de aprobación para acciones sensibles
- Registros, comandos de estado y gestión de configuración

## Estado del Proyecto

Lele es un proyecto independiente en evolución activa.

La base de código actual ya soporta:

- flujos de puerta de enlace tipo producción
- una ruta de cliente web/nativo
- enrutamiento configurable a múltiples proveedores
- múltiples canales de mensajería
- habilidades, subagentes y automatización programada

La principal brecha de documentación era que el antiguo README aún describía una identidad de fork anterior y no coincidía con el conjunto actual de funcionalidades. Este README refleja el proyecto tal como existe ahora.

## Inicio Rápido

### Instalar Desde el Código Fuente

```bash
git clone https://github.com/xilistudios/lele.git
cd lele
make deps
make build
```

El binario se escribe en `build/lele`.

### Configuración Inicial

```bash
lele onboard
```

`onboard` crea la configuración base, las plantillas del espacio de trabajo y puede habilitar opcionalmente la interfaz web y generar un PIN de emparejamiento para el flujo de cliente nativo/web.

### Uso Mínimo por CLI

```bash
lele agent -m "¿Qué puedes hacer?"
```

## Interfaz Web y Flujo de Cliente Nativo

Lele incluye ahora una interfaz web local además de un canal de cliente nativo.

Flujo típico:

1. Ejecutar `lele onboard`
2. Habilitar la interfaz web cuando se solicite
3. Generar un PIN de emparejamiento
4. Iniciar los servicios con `lele gateway` y `lele web start`
5. Abrir la aplicación web en tu navegador y emparejar con el PIN

El canal nativo expone endpoints REST y WebSocket para clientes de escritorio e integraciones locales.

Consulta `docs/client-api.md` para la API completa.

## Configuración

Archivo de configuración principal:

```text
~/.lele/config.json
```

Plantilla de configuración de ejemplo:

```text
config/config.example.json
```

Áreas principales que puedes configurar:

- `agents.defaults`: espacio de trabajo, proveedor, modelo, límites de tokens y herramientas
- `session`: comportamiento de sesiones efímeras y enlaces de identidad
- `channels`: integraciones de puerta de enlace y mensajería
- `providers`: proveedores directos y backends compatibles con OpenAI con nombre
- `tools`: búsqueda web, cron, configuración de seguridad de exec
- `heartbeat`: ejecución de tareas periódicas
- `gateway`, `logs`, `devices`

### Ejemplo Mínimo

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true,
      "model": "glm-4.7",
      "max_tokens": 8192,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "YOUR_API_KEY"
    }
  }
}
```

## Proveedores

Lele soporta tanto proveedores integrados como definiciones de proveedores con nombre.

Las familias de proveedores integrados representados actualmente en configuración/runtime incluyen:

- `anthropic`
- `openai`
- `openrouter`
- `groq`
- `zhipu`
- `gemini`
- `vllm`
- `nvidia`
- `ollama`
- `moonshot`
- `deepseek`
- `github_copilot`

El proyecto también soporta entradas de proveedores compatibles con OpenAI con nombre y configuraciones por modelo como:

- `model`
- `context_window`
- `max_tokens`
- `temperature`
- `vision`
- `reasoning`

## Canales

La puerta de enlace incluye actualmente configuración para:

- `telegram`
- `discord`
- `whatsapp`
- `feishu`
- `slack`
- `line`
- `onebot`
- `qq`
- `dingtalk`
- `maixcam`
- `native`
- `web`

Algunos canales son integraciones simples basadas en token, mientras que otros requieren configuración de webhook o puente.

## Estructura del Espacio de Trabajo

Espacio de trabajo por defecto:

```text
~/.lele/workspace/
```

Contenido típico:

```text
~/.lele/workspace/
├── sessions/
├── memory/
├── state/
├── cron/
├── skills/
├── AGENT.md
├── HEARTBEAT.md
├── IDENTITY.md
├── SOUL.md
└── USER.md
```

Esta estructura centrada en el espacio de trabajo es parte de lo que mantiene a Lele práctico y eficiente: estado, prompts, habilidades y automatización viven en un lugar predecible.

## Programación, Habilidades y Subagentes

### Tareas Programadas

Usa `lele cron` para crear trabajos puntuales o recurrentes.

Ejemplos:

```bash
lele cron list
lele cron add --name reminder --message "Verificar respaldos" --every "2h"
```

### Latido (Heartbeat)

Lele puede leer periódicamente `HEARTBEAT.md` del espacio de trabajo y ejecutar tareas de forma automática.

### Habilidades (Skills)

Las habilidades integradas y personalizadas se pueden gestionar con:

```bash
lele skills list
lele skills search
lele skills install <skill>
```

### Subagentes

Lele soporta trabajo delegado asíncrono a través de subagentes. Esto es útil para tareas de larga duración o paralelizables.

Consulta `docs/SKILL_SUBAGENTS.md` para más detalles.

## Modelo de Seguridad

Lele puede restringir el acceso a archivos y comandos del agente al espacio de trabajo configurado.

Los controles clave incluyen:

- `restrict_to_workspace`
- patrones de bloqueo para exec
- flujo de aprobación para acciones sensibles
- autenticación por token para clientes nativos
- límites de subida y TTL para cargas de archivos nativas

Consulta `docs/tools_configuration.md` y `docs/client-api.md` para detalles operativos.

## Referencia de CLI

| Command | Descripción |
| --- | --- |
| `lele onboard` | Inicializar configuración y espacio de trabajo |
| `lele agent` | Iniciar sesión interactiva de agente |
| `lele agent -m "..."` | Ejecutar un prompt único |
| `lele gateway` | Iniciar puerta de enlace de mensajería |
| `lele web start` | Iniciar la interfaz web integrada |
| `lele web status` | Mostrar estado de la interfaz web |
| `lele auth login` | Autenticar proveedores soportados |
| `lele status` | Mostrar estado del runtime |
| `lele cron list` | Listar tareas programadas |
| `lele cron add ...` | Añadir una tarea programada |
| `lele skills list` | Listar habilidades instaladas |
| `lele client pin` | Generar un PIN de emparejamiento |
| `lele client list` | Listar clientes nativos emparejados |
| `lele version` | Mostrar información de versión |

## Documentación Adicional

- `docs/agents-models-providers.md`
- `docs/architecture.md`
- `docs/channel-setup.md`
- `docs/cli-reference.md`
- `docs/config-reference.md`
- `docs/client-api.md`
- `docs/deployment.md`
- `docs/examples.md`
- `docs/installation-and-onboarding.md`
- `docs/logging-and-observability.md`
- `docs/model-routing.md`
- `docs/security-and-sandbox.md`
- `docs/session-and-workspace.md`
- `docs/skills-authoring.md`
- `docs/tools_configuration.md`
- `docs/troubleshooting.md`
- `docs/web-ui.md`
- `docs/SKILL_SUBAGENTS.md`
- `docs/SYSTEM_SPAWN_IMPLEMENTATION.md`

## Desarrollo

Comandos útiles:

```bash
make build
make test
make fmt
make vet
make check
```

## Licencia

MIT