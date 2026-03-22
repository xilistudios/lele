package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// MaxBasicMessageSize is the maximum character limit for verbose basic mode messages
const MaxBasicMessageSize = 80

// formatBasicToolMessage genera un mensaje simplificado para verbose basic
// Ejemplo: "🛠️ Exec: push git changes (in ~/.openclaw/workspace/lele)"
func formatBasicToolMessage(toolName string, args map[string]interface{}) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("🛠️ %s", strings.Title(toolName)))

	switch toolName {
	case "exec":
		return formatBasicExec(args)
	case "read":
		return formatBasicFileOp("read", args)
	case "write":
		return formatBasicFileOp("write", args)
	case "edit", "smart_edit":
		return formatBasicFileOp("edit", args)
	case "web_search":
		return formatBasicWebSearch(args)
	case "web_fetch":
		return formatBasicWebFetch(args)
	case "message":
		return formatBasicMessage(args)
	case "spawn":
		return formatBasicSpawn(args)
	case "list_dir":
		return formatBasicListDir(args)
	default:
		// Para otros tools, mostrar nombre y preview de args
		argsJSON, _ := json.Marshal(args)
		preview := string(argsJSON)
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		builder.WriteString(fmt.Sprintf(": %s", preview))
		result := builder.String()
		// Ensure total message doesn't exceed limit
		if len(result) > MaxBasicMessageSize {
			return result[:MaxBasicMessageSize-3] + "..."
		}
		return result
	}
}

func formatBasicExec(args map[string]interface{}) string {
	if cmd, ok := args["command"].(string); ok && cmd != "" {
		desc := extractCommandDescription(cmd)

		var builder strings.Builder
		builder.WriteString(fmt.Sprintf("🛠️ Exec: %s", desc))

		// Si hay cwd, mostrarlo
		if cwd, ok := args["cwd"].(string); ok && cwd != "" {
			// Limit cwd length for basic mode
			displayCwd := cwd
			if len(displayCwd) > 50 {
				displayCwd = displayCwd[:47] + "..."
			}
			builder.WriteString(fmt.Sprintf(" (in %s)", displayCwd))
		}

		// Limit command length for basic mode
		displayCmd := cmd
		if len(displayCmd) > 200 {
			displayCmd = displayCmd[:197] + "..."
		}
		builder.WriteString(fmt.Sprintf("\n%s", displayCmd))

		result := builder.String()
		// Ensure total message doesn't exceed limit
		if len(result) > MaxBasicMessageSize {
			return result[:MaxBasicMessageSize-3] + "..."
		}
		return result
	}
	return "🛠️ Exec: [no command]"
}

func formatBasicFileOp(action string, args map[string]interface{}) string {
	if path, ok := args["path"].(string); ok && path != "" {
		baseName := filepath.Base(path)
		dir := filepath.Dir(path)
		if dir == "." || dir == "" {
			dir = "workspace"
		}

		var builder strings.Builder
		builder.WriteString(fmt.Sprintf("🛠️ %s: %s (in %s)", strings.Title(action), baseName, dir))

		// Para write/edit, mostrar preview del contenido si existe
		if action == "write" || action == "edit" {
			if content, ok := args["content"].(string); ok && content != "" {
				lines := strings.Split(content, "\n")
				if len(lines) > 0 && len(lines[0]) > 0 {
					preview := lines[0]
					if len(preview) > 50 {
						preview = preview[:47] + "..."
					}
					builder.WriteString(fmt.Sprintf("\n→ %s", preview))
				}
			}
		}

		result := builder.String()
		// Ensure total message doesn't exceed limit
		if len(result) > MaxBasicMessageSize {
			return result[:MaxBasicMessageSize-3] + "..."
		}
		return result
	}
	return fmt.Sprintf("🛠️ %s: [no path]", strings.Title(action))
}

func formatBasicWebSearch(args map[string]interface{}) string {
	if query, ok := args["query"].(string); ok && query != "" {
		// Limit query length for basic mode
		displayQuery := query
		if len(displayQuery) > 100 {
			displayQuery = displayQuery[:97] + "..."
		}
		return fmt.Sprintf("🛠️ Search: \"%s\"", displayQuery)
	}
	return "🛠️ Search: [no query]"
}

func formatBasicWebFetch(args map[string]interface{}) string {
	if url, ok := args["url"].(string); ok && url != "" {
		// Truncar URL largas
		display := url
		if len(display) > 60 {
			display = display[:57] + "..."
		}
		return fmt.Sprintf("🛠️ Fetch: %s", display)
	}
	return "🛠️ Fetch: [no url]"
}

func formatBasicMessage(args map[string]interface{}) string {
	channel := "unknown"
	if c, ok := args["channel"].(string); ok && c != "" {
		channel = c
	}

	chatID := ""
	if c, ok := args["chat_id"].(string); ok && c != "" {
		chatID = c
	}

	target := channel
	if chatID != "" {
		target = fmt.Sprintf("%s:%s", channel, chatID)
	}

	if content, ok := args["content"].(string); ok && content != "" {
		preview := content
		if len(preview) > 50 {
			preview = preview[:47] + "..."
		}
		return fmt.Sprintf("🛠️ Message to %s: \"%s\"", target, preview)
	}
	return fmt.Sprintf("🛠️ Message to %s", target)
}

func formatBasicSpawn(args map[string]interface{}) string {
	if task, ok := args["task"].(string); ok && task != "" {
		desc := task
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		return fmt.Sprintf("🛠️ Spawn: %s", desc)
	}
	return "🛠️ Spawn: [no task]"
}

func formatBasicListDir(args map[string]interface{}) string {
	path := "current"
	if p, ok := args["path"].(string); ok && p != "" {
		path = p
		// Limit path length for basic mode
		if len(path) > 100 {
			path = path[:97] + "..."
		}
	}
	return fmt.Sprintf("🛠️ List: %s", path)
}

// extractCommandDescription extrae una descripción legible de un comando
// Ej: "cd ~/.openclaw/workspace/lele && git push" → "push git changes"
func extractCommandDescription(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	lower := strings.ToLower(cmd)

	// Detectar patrones comunes de git
	switch {
	case strings.Contains(lower, "git commit"):
		return "commit git changes"
	case strings.Contains(lower, "git push"):
		return "push git changes"
	case strings.Contains(lower, "git pull"):
		return "pull git changes"
	case strings.Contains(lower, "git status"):
		return "check git status"
	case strings.Contains(lower, "git add"):
		return "stage git changes"
	case strings.Contains(lower, "git checkout"):
		return "switch git branch"
	case strings.Contains(lower, "git log"):
		return "view git history"
	case strings.Contains(lower, "git diff"):
		return "view git diff"
	case strings.Contains(lower, "git clone"):
		return "clone repository"
	}

	// Detectar patrones de build
	switch {
	case strings.Contains(lower, "go build"):
		return "build Go project"
	case strings.Contains(lower, "go run"):
		return "run Go program"
	case strings.Contains(lower, "go test"):
		return "run Go tests"
	case strings.Contains(lower, "go mod"):
		return "manage Go modules"
	case strings.Contains(lower, "npm install") || strings.Contains(lower, "npm i"):
		return "install npm packages"
	case strings.Contains(lower, "npm run"):
		return "run npm script"
	case strings.Contains(lower, "npm build"):
		return "build npm project"
	case strings.Contains(lower, "make"):
		return "run make"
	case strings.Contains(lower, "docker build"):
		return "build Docker image"
	case strings.Contains(lower, "docker run"):
		return "run Docker container"
	case strings.Contains(lower, "docker compose"):
		return "run Docker compose"
	case strings.Contains(lower, "cargo build"):
		return "build Rust project"
	case strings.Contains(lower, "cargo run"):
		return "run Rust project"
	}

	// Detectar operaciones de archivos
	switch {
	case strings.Contains(lower, "mkdir"):
		return "create directory"
	case strings.Contains(lower, "rm -rf") || strings.Contains(lower, "rm -r"):
		return "remove directory"
	case strings.Contains(lower, "rm "):
		return "remove files"
	case strings.Contains(lower, "cp -r"):
		return "copy directory"
	case strings.Contains(lower, "cp "):
		return "copy files"
	case strings.Contains(lower, "mv "):
		return "move files"
	case strings.Contains(lower, "cat "):
		return "display file"
	case strings.Contains(lower, "head "):
		return "view file start"
	case strings.Contains(lower, "tail "):
		return "view file end"
	case strings.Contains(lower, "less "):
		return "view file"
	case strings.Contains(lower, "ls "):
		return "list directory"
	case strings.Contains(lower, "find "):
		return "find files"
	case strings.Contains(lower, "grep "):
		return "search in files"
	case strings.Contains(lower, "chmod "):
		return "change permissions"
	case strings.Contains(lower, "chown "):
		return "change ownership"
	case strings.Contains(lower, "tar ") || strings.Contains(lower, "zip") || strings.Contains(lower, "gzip"):
		return "archive files"
	}

	// Detectar procesos del sistema
	switch {
	case strings.Contains(lower, "ps "):
		return "list processes"
	case strings.Contains(lower, "kill "):
		return "kill process"
	case strings.Contains(lower, "top") || strings.Contains(lower, "htop"):
		return "monitor processes"
	case strings.Contains(lower, "systemctl"):
		return "manage service"
	case strings.Contains(lower, "journalctl"):
		return "view logs"
	}

	// Detectar red
	switch {
	case strings.Contains(lower, "curl "):
		return "HTTP request"
	case strings.Contains(lower, "wget "):
		return "download file"
	case strings.Contains(lower, "ping "):
		return "ping host"
	case strings.Contains(lower, "ssh "):
		return "SSH connection"
	case strings.Contains(lower, "scp "):
		return "secure copy"
	}

	// Default: usar primer comando después de cd y &&
	parts := strings.Fields(cmd)
	if len(parts) > 0 {
		// Saltar cd inicial
		start := 0
		for start < len(parts) && (parts[start] == "cd" || strings.HasPrefix(parts[start], "~")) {
			start++
			if start < len(parts) && !strings.HasPrefix(parts[start], "~") && parts[start] != "&&" {
				start++ // saltar también el argumento de cd
			}
		}

		// Saltar &&
		if start < len(parts) && parts[start] == "&&" {
			start++
		}

		// Encontrar comando real
		for start < len(parts) {
			cmd := parts[start]
			// Ignorar sudo
			if cmd == "sudo" {
				start++
				if start < len(parts) {
					return parts[start] + " (with sudo)"
				}
				continue
			}
			if cmd != "" {
				return cmd + " command"
			}
			start++
		}
	}

	return "execute command"
}
