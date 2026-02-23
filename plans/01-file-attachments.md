# Plan: Implementar Envío de Archivos a Telegram

## Objetivo
Extender la funcionalidad de picoclaw para permitir el envío de archivos adjuntos a Telegram.

## Fase 1: Análisis y Diseño

### 1.1 Requerimientos
- [ ] Soporte para enviar archivos existentes en el workspace
- [ ] Soporte para múltiples tipos de archivos (imágenes, documentos, audio, video)
- [ ] Validación de tamaño máximo (Telegram limita a 50MB para bots)
- [ ] Seguridad: restricción a archivos dentro del workspace
- [ ] Compatibilidad backward con mensajes de texto existentes

### 1.2 API de Telegram
El Bot API de Telegram soporta varios métodos para enviar archivos:

| Método | Descripción | Límite |
|--------|-------------|--------|
| `sendDocument` | Documentos genéricos | 50MB |
| `sendPhoto` | Imágenes JPG/PNG | 10MB |
| `sendAudio` | Archivos de audio MP3 | 50MB |
| `sendVideo` | Videos MP4 | 50MB |
| `sendVoice` | Mensajes de voz OGG | 50MB |

Formato de la API:
```
POST https://api.telegram.org/bot<token>/sendDocument
Content-Type: multipart/form-data

Form fields:
- chat_id: string
- document: file (multipart)
- caption: string (opcional, máx 1024 chars)
- parse_mode: string (opcional: Markdown, HTML)
```

## Fase 2: Modificaciones al Core

### 2.1 Extender la Interfaz de Mensajería

**Archivo a modificar:** `pkg/messaging/interface.go`

```go
// MessageAttachment representa un archivo adjunto
type MessageAttachment struct {
    Path        string            // Ruta al archivo
    Name        string            // Nombre del archivo para mostrar
    ContentType string            // MIME type (opcional, auto-detectar)
    Caption     string            // Descripción/caption del archivo
}

// ExtendedMessageRequest extiende MessageRequest con adjuntos
type ExtendedMessageRequest struct {
    Channel     string
    ChatID      string
    Content     string
    Attachments []MessageAttachment // Nuevo campo
}

// Extender MessagingService
type MessagingService interface {
    SendMessage(req MessageRequest) error
    SendMessageWithAttachments(req ExtendedMessageRequest) error // Nuevo método
}
```

### 2.2 Implementar Soporte en Telegram Provider

**Archivo a modificar:** `pkg/messaging/telegram.go`

```go
func (t *TelegramProvider) SendMessageWithAttachments(req ExtendedMessageRequest) error {
    if len(req.Attachments) == 0 {
        // Fallback a mensaje de texto simple
        return t.SendMessage(MessageRequest{
            Channel: req.Channel,
            ChatID:  req.ChatID,
            Content: req.Content,
        })
    }

    // Para múltiples adjuntos, usar sendMediaGroup
    // Para un solo adjunto, detectar tipo y usar método apropiado
    
    // Validar que los archivos estén dentro del workspace
    // Verificar tamaño máximo (50MB)
    // Detectar MIME type si no está especificado
    // Enviar usando multipart/form-data
}
```

### 2.3 Agregar Cliente HTTP Multipart

**Nuevo archivo:** `pkg/messaging/multipart.go`

```go
// Funciones para construir requests multipart
// - buildMultipartRequest(body io.Reader, files []FileUpload)
// - detectMIMEType(filename string) string
// - getTelegramMethodForMIME(mimeType string) string
```

## Fase 3: Actualizar el Tool de Mensajería

### 3.1 Extender Message Tool

**Archivo a modificar:** `pkg/tools/message.go`

```go
type MessageTool struct {
    // ... campos existentes ...
    allowedDir string // Para validar que archivos estén en workspace
}

type messageParams struct {
    Content     string   `json:"content"`
    Channel     string   `json:"channel,omitempty"`
    ChatID      string   `json:"chat_id,omitempty"`
    Attachments []string `json:"attachments,omitempty"` // NUEVO: rutas a archivos
}

func (t *MessageTool) Description() string {
    return `Sends a message to the user.

Can include text content and optional file attachments.
Attachments must be paths to files within the workspace.
Maximum file size: 50MB per file.`
}

func (t *MessageTool) Parameters() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "content":     map[string]interface{}{"type": "string", "description": "Message text content"},
            "channel":     map[string]interface{}{"type": "string", "description": "Target channel"},
            "chat_id":     map[string]interface{}{"type": "string", "description": "Target chat ID"},
            "attachments": map[string]interface{}{
                "type": "array",
                "description": "Optional file paths to attach",
                "items": map[string]interface{}{"type": "string"},
            },
        },
        "required": []string{"content"},
    }
}
```

### 3.2 Implementar Validación de Archivos

```go
func (t *MessageTool) validateAttachment(path string) error {
    // 1. Verificar que el archivo existe
    // 2. Verificar que está dentro del allowedDir (seguridad)
    // 3. Verificar tamaño <= 50MB
    // 4. Verificar que es un archivo regular (no symlink fuera)
}
```

## Fase 4: Integración con Fmod (Contexto Actual)

### 4.1 Casos de Uso para Archivos

| Escenario | Acción |
|-----------|--------|
| Usuario pide "envíame este archivo" | Leer archivo → adjuntar → enviar |
| Usuario pide "muestra el diff" | Generar diff → guardar temp → enviar como archivo |
| Usuario pide "genera reporte" | Crear reporte → enviar como PDF/TXT |
| Múltiples archivos | Usar sendMediaGroup de Telegram |

### 4.2 Extensión Opcional: Preview de Archivos

Agregar comando `send` o modificar `preview` para permitir:

```json
{
    "path": "/workspace/file.txt",
    "send_to_telegram": true
}
```

## Fase 5: Implementación Paso a Paso

### Tarea 1: Estructuras de Datos (30 min)
- [ ] Crear `MessageAttachment` struct
- [ ] Crear `ExtendedMessageRequest` struct
- [ ] Extender interface `MessagingService`

### Tarea 2: Cliente Multipart (1 hora)
- [ ] Implementar builder de requests multipart
- [ ] Implementar detección de MIME types
- [ ] Crear función de selección de método Telegram

### Tarea 3: Telegram Provider (1.5 horas)
- [ ] Implementar `SendMessageWithAttachments`
- [ ] Implementar `sendSingleFile`
- [ ] Implementar `sendMediaGroup` (múltiples archivos)
- [ ] Manejo de errores y reintentos

### Tarea 4: Message Tool (1 hora)
- [ ] Extender parámetros del tool
- [ ] Implementar validación de archivos
- [ ] Integrar con provider

### Tarea 5: Tests (1 hora)
- [ ] Tests unitarios para validación de archivos
- [ ] Tests de integración con mock de Telegram API
- [ ] Tests de seguridad (escapes de directorio)

### Tarea 6: Documentación (30 min)
- [ ] Actualizar SKILL.md de messaging
- [ ] Ejemplos de uso
- [ ] Limitaciones conocidas

**Tiempo Total Estimado:** ~5 horas

## Fase 6: Consideraciones de Seguridad

### 6.1 Validaciones Críticas
1. **Path Traversal**: Rechazar rutas con `..` o que resuelvan fuera del workspace
2. **Symlinks**: Verificar que el target final esté dentro del workspace
3. **Tamaño**: Rechazar archivos > 50MB antes de leer
4. **Tipos de archivo**: Opcionalmente, lista blanca de extensiones permitidas

### 6.2 Implementación de Seguridad
```go
func (t *MessageTool) securePath(path string) (string, error) {
    // Resolver path absoluto
    // Verificar que está bajo allowedDir
    // Verificar que no es symlink o que symlink target está permitido
    // Verificar tamaño del archivo
}
```

## Fase 7: Mejoras Futuras

- [ ] Soporte para otros canales (WhatsApp, Discord)
- [ ] Compresión automática de imágenes grandes
- [ ] Generación de thumbnails para videos
- [ ] Colas de envío para múltiples archivos grandes
- [ ] Caching de archivos enviados (usar file_id de Telegram)

## Ejemplos de Uso Post-Implementación

### Ejemplo 1: Enviar un archivo
```json
{
    "content": "Aquí está tu archivo:",
    "attachments": ["/workspace/report.pdf"]
}
```

### Ejemplo 2: Enviar múltiples archivos
```json
{
    "content": "Aquí están los logs:",
    "attachments": [
        "/workspace/app.log",
        "/workspace/error.log"
    ]
}
```

### Ejemplo 3: Enviar captura de pantalla
```json
{
    "content": "Vista previa del cambio:",
    "attachments": ["/workspace/screenshot.png"]
}
```

---

**Nota:** Este plan asume que el usuario quiere la funcionalidad básica de envío. Se puede implementar de forma incremental, empezando solo con `sendDocument` y agregando soporte para tipos específicos después.
