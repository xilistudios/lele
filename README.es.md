<div align="center">
  <img src="assets/logo.png" alt="Lele" width="512">

  <h1>Lele: Asistente de IA Ultra-Eficiente en Go</h1>

  <h3>Hardware de $10 · 10MB RAM · Inicio en 1s · 皮皮虾，我们走！</h3>

  <p>
    <img src="https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20RISC--V-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
    <br>
    <a href="https://lele.io"><img src="https://img.shields.io/badge/Sitio%20Web-lele.io-blue?style=flat&logo=google-chrome&logoColor=white" alt="Sitio Web"></a>
    <a href="https://x.com/SipeedIO"><img src="https://img.shields.io/badge/X_(Twitter)-SipeedIO-black?style=flat&logo=x&logoColor=white" alt="Twitter"></a>
  </p>

  [中文](README.zh.md) | [日本語](README.ja.md) | [Português](README.pt-br.md) | [Tiếng Việt](README.vi.md) | [Français](README.fr.md) | **Español**
</div>

---

## ¿Qué es Lele?

🦐 **Lele** es un asistente de IA personal ultra-ligero, diseñado como un fork de [lele](https://github.com/sipeed/lele) con un enfoque renovado en **facilidad de uso y simplicidad**. Refactorizado completamente en Go a través de un proceso de auto-bootstrapping, donde el propio agente de IA impulsó la migración arquitectónica y optimización del código.

### 🎯 Nuestra Filosofía

Lele nace con una misión clara: **democratizar el acceso a herramientas de inteligencia artificial** mediante un diseño minimalista y eficiente que prioriza:

- **Simplicidad sobre complejidad**: Una base de código pequeña y legible que cualquiera puede entender y modificar
- **Eficiencia sobre potencia bruta**: Optimizado para funcionar en hardware modesto sin sacrificar funcionalidad
- **Accesibilidad sobre exclusividad**: Herramientas de IA al alcance de todos, sin barreras de entrada económicas
- **Modelos open source**: Enfoque principal en modelos de lenguaje de código abierto (LLMs) que respetan la privacidad y libertad del usuario

### 💡 Enfoque en Modelos LLM Open Source

Lele está diseñado para trabajar nativamente con modelos de lenguaje abiertos:

- **Zhipu AI (GLM)**: Modelos chinos de alto rendimiento
- **Llama y derivados**: Modelos de Meta disponibles a través de OpenRouter
- **Mixtral y Mistral**: Modelos europeos de código abierto
- **Qwen y Yi**: Modelos open source de última generación

Compatibilidad total con proveedores que siguen el protocolo OpenAI, permitiendo integrar fácilmente nuevos backends mediante configuración simple.

### 📉 Optimizaciones para Reducción de Costos

Lele reduce drásticamente los costos de operación:

| Métrica | Asistentes Tradicionales | Lele |
|---------|-------------------------|------|
| **RAM** | >1GB | <10MB |
| **Hardware** | Mac Mini $599+ | Hardware desde $10 |
| **Consumo** | Decenas de watts | <2 watts |
| **Tiempo de inicio** | >500s | <1s |

**Ahorro estimado**: 98% menos costo de hardware, 99% menos consumo de memoria.

### 🌍 Accesibilidad a Herramientas de IA

Lele elimina las barreras de entrada:

- **Sin requisitos de hardware especializado**: Funciona en cualquier dispositivo Linux
- **Código abierto y transparente**: Sin cajas negras, todo el código es auditable
- **Configuración mínima**: Puesta en marcha en menos de 2 minutos
- **Multi-plataforma nativo**: Binarios únicos para RISC-V, ARM y x86_64
- **Integración con apps de chat**: Telegram, Discord, QQ, DingTalk, LINE

### ⚙️ Eficiente en Recursos (No Enfocado a Edge Devices)

A diferencia de otros proyectos que se enfocan en dispositivos edge extremadamente limitados, Lele:

- **No está limitado a microcontroladores**: Requiere un sistema Linux completo
- **Prioriza la eficiencia en hardware económico**: Optimizado para SBCs de bajo costo
- **Balance perfecto entre rendimiento y recursos**: Funciona en hardware de $10 pero escala sin problemas
- **Arquitectura moderna en Go**: Aprovecha las ventajas de concurrencia y eficiencia del lenguaje

---

## ✨ Características Principales

🪶 **Ultra-Ligero**: <10MB de memoria — 99% más pequeño que soluciones tradicionales

💰 **Costo Mínimo**: Eficiente para ejecutar en hardware de $10 — 98% más barato que alternativas

⚡️ **Inicio Relámpago**: 400x más rápido, arranque en 1 segundo incluso en CPUs de 0.6GHz

🌍 **Portabilidad Real**: Binario autocontenido para RISC-V, ARM y x86

🤖 **Auto-Generado**: Implementación nativa en Go — 95% del código generado por el agente con refinamiento humano

|                               | OpenClaw      | NanoBot                  | **Lele**                              |
| ----------------------------- | ------------- | ------------------------ | ------------------------------------- |
| **Lenguaje**                  | TypeScript    | Python                   | **Go**                                |
| **RAM**                       | >1GB          | >100MB                   | **< 10MB**                            |
| **Inicio** (0.8GHz core)      | >500s         | >30s                     | **<1s**                               |
| **Costo**                     | Mac Mini $599 | SBC Linux ~$50           | **Cualquier placa Linux** desde $10   |

<img src="assets/compare.jpg" alt="Comparación Lele" width="512">

---

## 🦾 Demostración

### 🛠️ Flujos de Trabajo Estándar

<table align="center">
  <tr align="center">
    <th><p align="center">🧩 Ingeniero Full-Stack</p></th>
    <th><p align="center">🗂️ Gestión de Logs y Planificación</p></th>
    <th><p align="center">🔎 Búsqueda Web y Aprendizaje</p></th>
  </tr>
  <tr>
    <td align="center"><p align="center"><img src="assets/lele_code.gif" width="240" height="180"></p></td>
    <td align="center"><p align="center"><img src="assets/lele_memory.gif" width="240" height="180"></p></td>
    <td align="center"><p align="center"><img src="assets/lele_search.gif" width="240" height="180"></p></td>
  </tr>
  <tr>
    <td align="center">Desarrollar • Desplegar • Escalar</td>
    <td align="center">Programar • Automatizar • Memoria</td>
    <td align="center">Descubrimiento • Insights • Tendencias</td>
  </tr>
</table>

---

## 📱 Ejecución en Teléfonos Android Antiguos

¡Dale una segunda vida a tu teléfono antiguo! Conviértelo en un asistente de IA inteligente con Lele.

**Inicio Rápido:**

1. **Instalar Termux** (Disponible en F-Droid o Google Play)

2. **Ejecutar comandos:**

```bash
# Nota: Reemplaza v0.1.1 con la última versión de la página de Releases
wget https://github.com/xilistudios/lele/releases/download/v0.1.1/lele-linux-arm64
chmod +x lele-linux-arm64
pkg install proot
termux-chroot ./lele-linux-arm64 onboard
```

Luego sigue las instrucciones en la sección "Inicio Rápido" para completar la configuración.

<img src="assets/termux.jpg" alt="Termux" width="512">

---

## 🐜 Implementación Innovadora de Bajo Consumo

Lele puede desplegarse en casi cualquier dispositivo Linux:

- **$9.9** [LicheeRV-Nano](https://www.aliexpress.com/item/1005006519668532.html) versión E(Ethernet) o W(WiFi6), para Home Assistant mínimo
- **$30~50** [NanoKVM](https://www.aliexpress.com/item/1005007369816019.html), o **$100** [NanoKVM-Pro](https://www.aliexpress.com/item/1005010048471263.html) para mantenimiento automatizado de servidores
- **$50** [MaixCAM](https://www.aliexpress.com/item/1005008053333693.html) o **$100** [MaixCAM2](https://www.kickstarter.com/projects/zepan/maixcam2-build-your-next-gen-4k-ai-camera) para monitoreo inteligente

<https://private-user-images.githubusercontent.com/83055338/547056448-e7b031ff-d6f5-4468-bcca-5726b6fecb5c.mp4>

🌟 ¡Más casos de implementación en camino!

---

## 📦 Instalación

### Instalar con binario precompilado

Descarga el firmware para tu plataforma desde la página de [releases](https://github.com/xilistudios/lele/releases).

### Instalar desde código (últimas características, recomendado para desarrollo)

```bash
git clone https://github.com/xilistudios/lele.git

cd lele
make deps

# Compilar, no es necesario instalar
make build

# Compilar para múltiples plataformas
make build-all

# Compilar e Instalar
make install
```

---

## 🐳 Docker Compose

También puedes ejecutar Lele usando Docker Compose sin instalar nada localmente.

```bash
# 1. Clonar este repo
git clone https://github.com/xilistudios/lele.git
cd lele

# 2. Configurar tus claves API
cp config/config.example.json config/config.json
vim config/config.json      # Establece DISCORD_BOT_TOKEN, claves API, etc.

# 3. Compilar e Iniciar
docker compose --profile gateway up -d

# 4. Verificar logs
docker compose logs -f lele-gateway

# 5. Detener
docker compose --profile gateway down
```

### Modo Agente (One-shot)

```bash
# Hacer una pregunta
docker compose run --rm lele-agent -m "¿Cuánto es 2+2?"

# Modo interactivo
docker compose run --rm lele-agent
```

### Reconstruir

```bash
docker compose --profile gateway build --no-cache
docker compose --profile gateway up -d
```

---

## 🚀 Inicio Rápido

> [!TIP]
> Configura tu clave API en `~/.lele/config.json`.
> Obtén claves API: [OpenRouter](https://openrouter.ai/keys) (LLM) · [Zhipu](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) (LLM)
> La búsqueda web es **opcional** — obtén [Brave Search API](https://brave.com/search/api) gratis (2000 consultas/mes) o usa el fallback automático integrado.

**1. Inicializar**

```bash
lele onboard
```

**2. Configurar** (`~/.lele/config.json`)

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "model": "glm-4.7",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "xxx",
      "api_base": "https://openrouter.ai/api/v1"
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": false,
        "api_key": "TU_CLAVE_BRAVE",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  }
}
```

**3. Obtener Claves API**

- **Proveedor LLM**: [OpenRouter](https://openrouter.ai/keys) · [Zhipu](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) · [Anthropic](https://console.anthropic.com) · [OpenAI](https://platform.openai.com) · [Gemini](https://aistudio.google.com/api-keys)
- **Búsqueda Web** (opcional): [Brave Search](https://brave.com/search/api) - Capa gratuita disponible (2000 consultas/mes)

> **Nota**: Consulta `config.example.json` para una plantilla de configuración completa.

**4. Chatear**

```bash
lele agent -m "¿Cuánto es 2+2?"
```

¡Y eso es todo! Tienes un asistente de IA funcional en 2 minutos.

> Modelos de agente: usa `nombre_proveedor/nombre_modelo` (por ejemplo: `openrouter/auto` o `my-openai-compatible/fast`).

---

## 💬 Aplicaciones de Chat

Habla con tu Lele a través de Telegram, Discord, DingTalk o LINE

| Canal        | Configuración                        |
| ------------ | ------------------------------------ |
| **Telegram** | Fácil (solo un token)                |
| **Discord**  | Fácil (token del bot + intents)      |
| **QQ**       | Fácil (AppID + AppSecret)            |
| **DingTalk** | Medio (credenciales de app)          |
| **LINE**     | Medio (credenciales + URL webhook)   |

<details>
<summary><b>Telegram</b> (Recomendado)</summary>

**1. Crear un bot**

- Abre Telegram, busca `@BotFather`
- Envía `/newbot`, sigue las instrucciones
- Copia el token

**2. Configurar**

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "TU_BOT_TOKEN",
      "allow_from": ["TU_USER_ID"]
    }
  }
}
```

> Obtén tu ID de usuario desde `@userinfobot` en Telegram.

**3. Ejecutar**

```bash
lele gateway
```

</details>

<details>
<summary><b>Discord</b></summary>

**1. Crear un bot**

- Ve a <https://discord.com/developers/applications>
- Crea una aplicación → Bot → Añadir Bot
- Copia el token del bot

**2. Habilitar intents**

- En la configuración del Bot, habilita **MESSAGE CONTENT INTENT**
- (Opcional) Habilita **SERVER MEMBERS INTENT** si planeas usar listas de permitidos basadas en datos de miembros

**3. Obtener tu ID de Usuario**

- Configuración de Discord → Avanzado → habilitar **Modo Desarrollador**
- Click derecho en tu avatar → **Copiar ID de Usuario**

**4. Configurar**

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "TU_BOT_TOKEN",
      "allow_from": ["TU_USER_ID"]
    }
  }
}
```

**5. Invitar al bot**

- OAuth2 → Generador de URL
- Scopes: `bot`
- Permisos del Bot: `Send Messages`, `Read Message History`
- Abre la URL generada y añade el bot a tu servidor

**6. Ejecutar**

```bash
lele gateway
```

</details>

<details>
<summary><b>QQ</b></summary>

**1. Crear un bot**

- Ve a [Plataforma Abierta QQ](https://q.qq.com/#)
- Crea una aplicación → Obtén **AppID** y **AppSecret**

**2. Configurar**

```json
{
  "channels": {
    "qq": {
      "enabled": true,
      "app_id": "TU_APP_ID",
      "app_secret": "TU_APP_SECRET",
      "allow_from": []
    }
  }
}
```

> Establece `allow_from` vacío para permitir todos los usuarios, o especifica números QQ para restringir acceso.

**3. Ejecutar**

```bash
lele gateway
```

</details>

<details>
<summary><b>DingTalk</b></summary>

**1. Crear un bot**

- Ve a [Plataforma Abierta](https://open.dingtalk.com/)
- Crea una app interna
- Copia Client ID y Client Secret

**2. Configurar**

```json
{
  "channels": {
    "dingtalk": {
      "enabled": true,
      "client_id": "TU_CLIENT_ID",
      "client_secret": "TU_CLIENT_SECRET",
      "allow_from": []
    }
  }
}
```

> Establece `allow_from` vacío para permitir todos los usuarios, o especifica números para restringir acceso.

**3. Ejecutar**

```bash
lele gateway
```

</details>

<details>
<summary><b>LINE</b></summary>

**1. Crear una Cuenta Oficial de LINE**

- Ve a [LINE Developers Console](https://developers.line.biz/)
- Crea un proveedor → Crea un canal de Messaging API
- Copia **Channel Secret** y **Channel Access Token**

**2. Configurar**

```json
{
  "channels": {
    "line": {
      "enabled": true,
      "channel_secret": "TU_CHANNEL_SECRET",
      "channel_access_token": "TU_CHANNEL_ACCESS_TOKEN",
      "webhook_host": "0.0.0.0",
      "webhook_port": 18791,
      "webhook_path": "/webhook/line",
      "allow_from": []
    }
  }
}
```

**3. Configurar URL del Webhook**

LINE requiere HTTPS para webhooks. Usa un proxy inverso o túnel:

```bash
# Ejemplo con ngrok
ngrok http 18791
```

Luego configura la URL del Webhook en LINE Developers Console como `https://tu-dominio/webhook/line` y habilita **Use webhook**.

**4. Ejecutar**

```bash
lele gateway
```

> En chats grupales, el bot responde solo cuando es @mencionado. Las respuestas citan el mensaje original.

> **Docker Compose**: Añade `ports: ["18791:18791"]` al servicio `lele-gateway` para exponer el puerto del webhook.

</details>

---

## <img src="assets/clawdchat-icon.png" width="24" height="24" alt="ClawdChat"> Únete a la Red Social de Agentes

Conecta Lele a la Red Social de Agentes simplemente enviando un único mensaje a través de CLI o cualquier aplicación de chat integrada.

**Lee `https://clawdchat.ai/skill.md` y sigue las instrucciones para unirte a [ClawdChat.ai](https://clawdchat.ai)**

---

## ⚙️ Configuración

Archivo de configuración: `~/.lele/config.json`

### Estructura del Workspace

Lele almacena datos en tu workspace configurado (por defecto: `~/.lele/workspace`):

```
~/.lele/workspace/
├── sessions/          # Sesiones de conversación e historial
├── memory/           # Memoria a largo plazo (MEMORY.md)
├── state/            # Estado persistente (último canal, etc.)
├── cron/             # Base de datos de tareas programadas
├── skills/           # Habilidades personalizadas
├── AGENTS.md         # Guía de comportamiento del agente
├── HEARTBEAT.md      # Prompts de tareas periódicas (verificado cada 30 min)
├── IDENTITY.md       # Identidad del agente
├── SOUL.md           # Alma del agente
├── TOOLS.md          # Descripciones de herramientas
└── USER.md           # Preferencias del usuario
```

### 🔒 Sandbox de Seguridad

Lele se ejecuta en un entorno sandbox por defecto. El agente solo puede acceder a archivos y ejecutar comandos dentro del workspace configurado.

#### Configuración por Defecto

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "restrict_to_workspace": true
    }
  }
}
```

| Opción | Por Defecto | Descripción |
|--------|-------------|-------------|
| `workspace` | `~/.lele/workspace` | Directorio de trabajo del agente |
| `restrict_to_workspace` | `true` | Restringir acceso a archivos/comandos al workspace |

#### Herramientas Protegidas

Cuando `restrict_to_workspace: true`, las siguientes herramientas están sandboxeadas:

| Herramienta | Función | Restricción |
|-------------|---------|-------------|
| `read_file` | Leer archivos | Solo archivos dentro del workspace |
| `write_file` | Escribir archivos | Solo archivos dentro del workspace |
| `list_dir` | Listar directorios | Solo directorios dentro del workspace |
| `edit_file` | Editar archivos | Solo archivos dentro del workspace |
| `append_file` | Añadir a archivos | Solo archivos dentro del workspace |
| `exec` | Ejecutar comandos | Rutas de comandos deben estar dentro del workspace |

#### Protección Adicional de Exec

Incluso con `restrict_to_workspace: false`, la herramienta `exec` bloquea estos comandos peligrosos:

- `rm -rf`, `del /f`, `rmdir /s` — Eliminación masiva
- `format`, `mkfs`, `diskpart` — Formateo de disco
- `dd if=` — Imágenes de disco
- Escritura en `/dev/sd[a-z]` — Escrituras directas a disco
- `shutdown`, `reboot`, `poweroff` — Apagado del sistema
- Fork bomb `:(){ :|:& };:`

#### Ejemplos de Error

```
[ERROR] tool: Tool execution failed
{tool=exec, error=Command blocked by safety guard (path outside working dir)}
```

```
[ERROR] tool: Tool execution failed
{tool=exec, error=Command blocked by safety guard (dangerous pattern detected)}
```

#### Deshabilitar Restricciones (Riesgo de Seguridad)

Si necesitas que el agente acceda a rutas fuera del workspace:

**Método 1: Archivo de configuración**

```json
{
  "agents": {
    "defaults": {
      "restrict_to_workspace": false
    }
  }
}
```

**Método 2: Variable de entorno**

```bash
export LELE_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE=false
```

> ⚠️ **Advertencia**: Deshabilitar esta restricción permite al agente acceder a cualquier ruta en tu sistema. Usar con precaución solo en entornos controlados.

#### Consistencia de Límites de Seguridad

La configuración `restrict_to_workspace` se aplica consistentemente en todas las rutas de ejecución:

| Ruta de Ejecución | Límite de Seguridad |
|-------------------|---------------------|
| Agente Principal | `restrict_to_workspace` ✅ |
| Subagente / Spawn | Hereda la misma restricción ✅ |
| Tareas Heartbeat | Hereda la misma restricción ✅ |

Todas las rutas comparten la misma restricción de workspace — no hay forma de evadir el límite de seguridad a través de subagentes o tareas programadas.

---

### Heartbeat (Tareas Periódicas)

Lele puede realizar tareas periódicas automáticamente. Crea un archivo `HEARTBEAT.md` en tu workspace:

```markdown
# Tareas Periódicas

- Revisar mi correo electrónico para mensajes importantes
- Revisar mi calendario para próximos eventos
- Verificar el pronóstico del tiempo
```

El agente leerá este archivo cada 30 minutos (configurable) y ejecutará cualquier tarea usando las herramientas disponibles.

#### Tareas Asíncronas con Spawn

Para tareas de larga duración (búsqueda web, llamadas API), usa la herramienta `spawn` para crear un **subagente**:

```markdown
# Tareas Periódicas

## Tareas Rápidas (responder directamente)
- Reportar hora actual

## Tareas Largas (usar spawn para asíncrono)
- Buscar en la web noticias de IA y resumir
- Revisar correo y reportar mensajes importantes
```

**Comportamientos clave:**

| Característica | Descripción |
|----------------|-------------|
| **spawn** | Crea subagente asíncrono, no bloquea heartbeat |
| **Contexto independiente** | Subagente tiene su propio contexto, sin historial de sesión |
| **Herramienta message** | Subagente se comunica directamente con el usuario vía herramienta message |
| **No bloqueante** | Después de spawn, heartbeat continúa a la siguiente tarea |

#### Cómo Funciona la Comunicación del Subagente

```
Heartbeat se activa
    ↓
Agente lee HEARTBEAT.md
    ↓
Para tarea larga: crear subagente
    ↓                           ↓
Continuar a siguiente tarea   Subagente trabaja independientemente
    ↓                           ↓
Todas las tareas completas    Subagente usa herramienta "message"
    ↓                           ↓
Responder HEARTBEAT_OK        Usuario recibe resultado directamente
```

El subagente tiene acceso a herramientas (message, web_search, etc.) y puede comunicarse con el usuario independientemente sin pasar por el agente principal.

**Configuración:**

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

| Opción | Por Defecto | Descripción |
|--------|-------------|-------------|
| `enabled` | `true` | Habilitar/deshabilitar heartbeat |
| `interval` | `30` | Intervalo de verificación en minutos (mín: 5) |

**Variables de entorno:**

- `LELE_HEARTBEAT_ENABLED=false` para deshabilitar
- `LELE_HEARTBEAT_INTERVAL=60` para cambiar intervalo

---

### Proveedores

> [!NOTE]
> Groq proporciona transcripción de voz gratuita vía Whisper. Si está configurado, los mensajes de voz de Telegram se transcribirán automáticamente.

| Proveedor                       | Propósito                                      | Obtener Clave API                                   |
| ------------------------------- | ---------------------------------------------- | --------------------------------------------------- |
| `gemini`                        | LLM (Gemini directo)                           | [aistudio.google.com](https://aistudio.google.com)  |
| `zhipu`                         | LLM (Zhipu directo)                            | [bigmodel.cn](bigmodel.cn)                          |
| `openrouter` (Por probar)       | LLM (recomendado, acceso a todos los modelos)  | [openrouter.ai](https://openrouter.ai)              |
| `anthropic` (Por probar)        | LLM (Claude directo)                           | [console.anthropic.com](https://console.anthropic.com) |
| `openai` (Por probar)           | LLM (GPT directo)                              | [platform.openai.com](https://platform.openai.com)  |
| `deepseek` (Por probar)         | LLM (DeepSeek directo)                         | [platform.deepseek.com](https://platform.deepseek.com) |
| `groq`                          | LLM + **Transcripción de voz** (Whisper)       | [console.groq.com](https://console.groq.com)        |

### Arquitectura de Proveedores

Lele enruta proveedores por familia de protocolo:

- **Protocolo compatible con OpenAI**: OpenRouter, gateways compatibles con OpenAI, Groq, Zhipu y endpoints estilo vLLM.
- **Protocolo Anthropic**: Comportamiento nativo de API de Claude.
- **Ruta Codex/OAuth**: Ruta de autenticación OAuth/token de OpenAI.

Esto mantiene el runtime ligero mientras hace que nuevos backends compatibles con OpenAI sean principalmente una operación de configuración (`api_base` + `api_key`).

<details>
<summary><b>Zhipu</b></summary>

**1. Obtener clave API y URL base**

- Obtén [clave API](https://bigmodel.cn/usercenter/proj-mgmt/apikeys)

**2. Configurar**

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.lele/workspace",
      "model": "glm-4.7",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "zhipu": {
      "type": "zhipu",
      "api_key": "Tu Clave API",
      "api_base": "https://open.bigmodel.cn/api/paas/v4"
    }
  }
}
```

**3. Ejecutar**

```bash
lele agent -m "Hola"
```

</details>

<details>
<summary><b>Ejemplo de configuración completa</b></summary>

```json
{
  "agents": {
    "defaults": {
      "model": "anthropic/claude-opus-4-5"
    }
  },
  "session": {
    "ephemeral": true,
    "ephemeral_threshold": 560
  },
  "providers": {
    "openrouter": {
      "type": "openrouter",
      "api_key": "sk-or-v1-xxx"
    },
    "groq": {
      "type": "groq",
      "api_key": "gsk_xxx"
    }
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "123456:ABC...",
      "allow_from": ["123456789"]
    },
    "discord": {
      "enabled": true,
      "token": "",
      "allow_from": [""]
    },
    "whatsapp": {
      "enabled": false
    },
    "feishu": {
      "enabled": false,
      "app_id": "cli_xxx",
      "app_secret": "xxx",
      "encrypt_key": "",
      "verification_token": "",
      "allow_from": []
    },
    "qq": {
      "enabled": false,
      "app_id": "",
      "app_secret": "",
      "allow_from": []
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": false,
        "api_key": "BSA...",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    },
    "cron": {
      "exec_timeout_minutes": 5
    }
  },
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

</details>

### Sesiones Efímeras

Agrega el bloque `session` para habilitar sesiones nuevas automáticas después de un periodo de inactividad:

```json
{
  "session": {
    "ephemeral": true,
    "ephemeral_threshold": 560
  }
}
```

`ephemeral_threshold` se mide en segundos.

Cuando `ephemeral` está activado y un chat supera ese tiempo sin actividad, el siguiente mensaje entrante inicia una sesión vacía automáticamente y Lele notifica que se creó una nueva sesión.

También puedes cambiarlo en runtime con `/toggle ephemeral`. Ese comando actualiza `~/.lele/config.json`, así que la configuración persiste entre reinicios.

---

## Referencia CLI

| Comando                    | Descripción                            |
| -------------------------- | -------------------------------------- |
| `lele onboard`             | Inicializar configuración y workspace  |
| `lele agent -m "..."`      | Chatear con el agente                  |
| `lele agent`               | Modo de chat interactivo               |
| `lele gateway`             | Iniciar el gateway                     |
| `lele status`              | Mostrar estado                         |
| `lele cron list`           | Listar todas las tareas programadas    |
| `lele cron add ...`        | Añadir una tarea programada            |

### Tareas Programadas / Recordatorios

Lele soporta recordatorios programados y tareas recurrentes a través de la herramienta `cron`:

- **Recordatorios únicos**: "Recuérdame en 10 minutos" → se activa una vez después de 10min
- **Tareas recurrentes**: "Recuérdame cada 2 horas" → se activa cada 2 horas
- **Expresiones cron**: "Recuérdame a las 9am diariamente" → usa expresión cron

Las tareas se almacenan en `~/.lele/workspace/cron/` y se procesan automáticamente.

---

## 🤝 Contribuir y Roadmap

¡PRs bienvenidos! La base de código es intencionalmente pequeña y legible. 🤗

Roadmap próximamente...

Construcción de grupo de desarrolladores, Requisito de Entrada: Al menos 1 PR Fusionado.

---

## 🐛 Solución de Problemas

### La búsqueda web dice "API 配置问题"

Esto es normal si aún no has configurado una clave API de búsqueda. Lele proporcionará enlaces útiles para búsquedas manuales.

Para habilitar la búsqueda web:

1. **Opción 1 (Recomendada)**: Obtén una clave API gratuita en [https://brave.com/search/api](https://brave.com/search/api) (2000 consultas/mes gratis) para los mejores resultados.
2. **Opción 2 (Sin Tarjeta de Crédito)**: Si no tienes una clave, automáticamente usamos **DuckDuckGo** (no requiere clave).

Añade la clave a `~/.lele/config.json` si usas Brave:

```json
{
  "tools": {
    "web": {
      "brave": {
        "enabled": false,
        "api_key": "TU_CLAVE_BRAVE",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  }
}
```

### Obteniendo errores de filtrado de contenido

Algunos proveedores (como Zhipu) tienen filtrado de contenido. Intenta reformular tu consulta o usa un modelo diferente.

### El bot de Telegram dice "Conflict: terminated by other getUpdates"

Esto sucede cuando otra instancia del bot está en ejecución. Asegúrate de que solo un `lele gateway` esté ejecutándose a la vez.

---

## 📝 Comparación de Claves API

| Servicio         | Capa Gratuita         | Caso de Uso                                |
| ---------------- | --------------------- | ------------------------------------------ |
| **OpenRouter**   | 200K tokens/mes       | Múltiples modelos (Claude, GPT-4, etc.)    |
| **Zhipu**        | 200K tokens/mes       | Mejor para usuarios chinos                 |
| **Brave Search** | 2000 consultas/mes    | Funcionalidad de búsqueda web              |
| **Groq**         | Capa gratuita         | Inferencia rápida (Llama, Mixtral)         |

---

<div align="center">

**Hecho con ❤️ por la comunidad Lele**

[Website](https://lele.io) · [GitHub](https://github.com/xilistudios/lele) · [Discord](https://discord.gg/xxx) · [Telegram](https://t.me/xxx)

</div>
