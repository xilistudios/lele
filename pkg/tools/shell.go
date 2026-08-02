package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/utils"
)

type ExecTool struct {
	workingDir          string
	timeout             time.Duration
	denyPatterns        []*regexp.Regexp
	allowPatterns       []*regexp.Regexp
	restrictToWorkspace bool
	approvalMode        bool                                  // Activa modo aprobación
	approvalCallback    func(cmd string) (bool, error)        // Callback para solicitar aprobación
	bypassGuard         bool                                  // Bypass all safety guards when command is approved
	channel             string                                // Channel for feedback messages
	chatID              string                                // ChatID for feedback messages
	feedbackCallback    func(channel, chatID, message string) // Callback to send feedback messages
	verboseLevel        session.VerboseLevel                  // Verbose level for feedback messages
	backgroundManager   *BackgroundProcessManager             // Manager for background processes
	backgroundThreshold time.Duration                         // Duration threshold for auto-backgrounding
	keyringSvc          *keyring.Service                      // Optional keyring for {{SECRET:name}} substitution
	secretSubstitution  bool                                  // Enable {{SECRET:name}} substitution (default true)
}

// secretPlaceholderInlineRegex matches {{SECRET:name}} placeholders anywhere in
// a command string (not just whole-string, unlike the config resolver).
var secretPlaceholderInlineRegex = regexp.MustCompile(`\{\{SECRET:([^}]+)\}\}`)

// SetContext implements ContextualTool interface
func (t *ExecTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

// SetFeedbackCallback sets the callback for sending feedback messages
func (t *ExecTool) SetFeedbackCallback(callback func(channel, chatID, message string)) {
	t.feedbackCallback = callback
}

// SetVerbose sets the verbose level for feedback messages
func (t *ExecTool) SetVerbose(level session.VerboseLevel) {
	t.verboseLevel = level
}

var defaultDenyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
	regexp.MustCompile(`\bdel\s+/[fq]\b`),
	regexp.MustCompile(`\brmdir\s+/s\b`),
	regexp.MustCompile(`\b(format|mkfs|diskpart)\b\s`), // Match disk wiping commands (must be followed by space/args)
	regexp.MustCompile(`\bdd\s+if=`),
	regexp.MustCompile(`>\s*/dev/sd[a-z]\b`), // Block writes to disk devices (but allow /dev/null)
	regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
	regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),
	regexp.MustCompile(`\$\([^)]+\)`),
	regexp.MustCompile(`\$\{[^}]+\}`),
	regexp.MustCompile("`[^`]+`"),
	regexp.MustCompile(`\|\s*sh\b`),
	regexp.MustCompile(`\|\s*bash\b`),
	regexp.MustCompile(`;\s*rm\s+-[rf]`),
	regexp.MustCompile(`&&\s*rm\s+-[rf]`),
	regexp.MustCompile(`\|\|\s*rm\s+-[rf]`),
	regexp.MustCompile(`>\s*/dev/null\s*>&?\s*\d?`),
	regexp.MustCompile(`<<\s*EOF`),
	regexp.MustCompile(`\$\(\s*cat\s+`),
	regexp.MustCompile(`\$\(\s*curl\s+`),
	regexp.MustCompile(`\$\(\s*wget\s+`),
	regexp.MustCompile(`\$\(\s*which\s+`),
	regexp.MustCompile(`\bsudo\b`),
	regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\b`),
	regexp.MustCompile(`\bchown\b`),
	regexp.MustCompile(`\bpkill\b`),
	regexp.MustCompile(`\bkillall\b`),
	regexp.MustCompile(`\bkill\s+-[9]\b`),
	regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)`),
	regexp.MustCompile(`\bwget\b.*\|\s*(sh|bash)`),
	regexp.MustCompile(`\bnpm\s+install\s+-g\b`),
	regexp.MustCompile(`\bpip\s+install\s+--user\b`),
	regexp.MustCompile(`\bapt\s+(install|remove|purge)\b`),
	regexp.MustCompile(`\byum\s+(install|remove)\b`),
	regexp.MustCompile(`\bdnf\s+(install|remove)\b`),
	regexp.MustCompile(`\bdocker\s+run\b`),
	regexp.MustCompile(`\bdocker\s+exec\b`),
	regexp.MustCompile(`\bgit\s+push\b`),
	regexp.MustCompile(`\bgit\s+force\b`),
	regexp.MustCompile(`\bssh\b.*@`),
	regexp.MustCompile(`\beval\b`),
	regexp.MustCompile(`\bsource\s+.*\.sh\b`),
}

func NewExecTool(workingDir string, restrict bool) *ExecTool {
	return NewExecToolWithConfig(workingDir, restrict, nil)
}

func NewExecToolWithConfig(workingDir string, restrict bool, config *config.Config) *ExecTool {
	denyPatterns := make([]*regexp.Regexp, 0)

	enableDenyPatterns := config != nil && config.Tools.Exec.EnableDenyPatterns
	if config != nil {
		execConfig := config.Tools.Exec
		if enableDenyPatterns {
			if len(execConfig.CustomDenyPatterns) > 0 {
				fmt.Printf("Using custom deny patterns: %v\n", execConfig.CustomDenyPatterns)
				for _, pattern := range execConfig.CustomDenyPatterns {
					re, err := regexp.Compile(pattern)
					if err != nil {
						fmt.Printf("Invalid custom deny pattern %q: %v\n", pattern, err)
						continue
					}
					denyPatterns = append(denyPatterns, re)
				}
			} else {
				denyPatterns = append(denyPatterns, defaultDenyPatterns...)
			}
		} else {
			// If deny patterns are disabled, we won't add any patterns, allowing all commands.
			logger.WarnCF("tools", "deny patterns are disabled. All commands will be allowed.", nil)
		}
	} else {
		denyPatterns = append(denyPatterns, defaultDenyPatterns...)
	}

	timeout := 60 * time.Second
	if config != nil {
		timeout = time.Duration(config.Tools.Exec.TimeoutSeconds) * time.Second
	}

	return &ExecTool{
		workingDir:          workingDir,
		timeout:             timeout,
		denyPatterns:        denyPatterns,
		allowPatterns:       nil,
		restrictToWorkspace: restrict,
		backgroundThreshold: 60 * time.Second,
		secretSubstitution:  true,
	}
}

func (t *ExecTool) Name() string {
	return "exec"
}

func (t *ExecTool) Description() string {
	return "Execute a shell command and return its output. Use with caution. Supports {{SECRET:name}} placeholders that are substituted with the named keyring secret at run time (the raw value never appears in logs or session history)."
}

func (t *ExecTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"working_dir": map[string]interface{}{
				"type":        "string",
				"description": "Optional working directory for the command",
			},
			"background": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, run the command in the background immediately. Returns a process ID that can be used with list_background_execs, get_background_exec_output, and stop_background_exec. Default: false (command auto-backgrounds after 60 seconds if still running).",
			},
		},
		"required": []string{"command"},
	}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	command, ok := args["command"].(string)
	if !ok {
		return ErrorResult("command is required")
	}

	channel, chatID := ToolContextFromCtx(ctx)
	if channel == "" {
		channel = t.channel
	}
	if chatID == "" {
		chatID = t.chatID
	}
	feedbackCallback := t.feedbackCallback
	verboseLevel := t.verboseLevel

	cwd := t.workingDir
	if wd, ok := args["working_dir"].(string); ok && wd != "" {
		cwd = wd
	}

	if cwd == "" {
		wd, err := os.Getwd()
		if err == nil {
			cwd = wd
		}
	}

	// Parse background mode flag
	backgroundMode, _ := args["background"].(bool)

	// Check safety guards unless bypass is enabled (for approved commands)
	if !t.bypassGuard {
		guardMsg, isBlockable := t.guardCommandWithStatus(command, cwd)
		if guardMsg != "" {
			// Si está en modo aprobación y el comando es bloqueable (requiere aprobación)
			if t.approvalMode && isBlockable {
				// Retornar resultado especial indicando que se necesita aprobación
				return &ToolResult{
					ForLLM:  fmt.Sprintf("Command '%s' requires user approval. Reason: %s", command, guardMsg),
					ForUser: "",
					IsError: false,
					ApprovalRequired: &ApprovalInfo{
						Command: command,
						Reason:  guardMsg,
					},
				}
			}
			return ErrorResult(guardMsg)
		}
	}

	// Context for the wait loop (timeout + agent cancellation)
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if t.timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, t.timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// Resolve {{SECRET:name}} placeholders just before execution. The guard
	// checks above ran against the placeholder form, so raw secret values are
	// never exposed to safety guards, logs, or session history.
	resolvedCommand, err := t.substituteSecrets(ctx, command)
	if err != nil {
		return ErrorResult(err.Error())
	}
	command = resolvedCommand

	// Detached context for the command process (survives after we return
	// for backgrounded processes)
	bgCtx, bgCancel := context.WithCancel(context.Background())

	// Create command with bgCtx so it can survive agent lifecycle
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(bgCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(bgCtx, "sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Thread-safe buffers so the background manager can read while the
	// process is still writing.
	stdout := newThreadSafeBuffer(1024 * 1024) // 1MB cap
	stderr := newThreadSafeBuffer(1024 * 1024) // 1MB cap
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Generate process name for feedback
	processName := utils.RandomProcessName()

	// Start the command
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		bgCancel()
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	// ── Explicit background mode ──────────────────────────────────────
	if backgroundMode && t.backgroundManager != nil {
		proc := t.backgroundManager.Register(cmd, command, cwd, stdout, stderr, bgCancel)
		go func() {
			err := cmd.Wait()
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = 1
				}
			}
			t.backgroundManager.MarkCompleted(proc.ID, exitCode)
		}()
		msg := fmt.Sprintf("Command started in background. Process ID: %s\nUse list_background_execs to check status, get_background_exec_output to view output, stop_background_exec to stop it.", proc.ID)
		return &ToolResult{
			ForLLM:  msg,
			ForUser: msg,
			IsError: false,
		}
	}

	// Channel to signal command completion
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Wait for command to complete with periodic heartbeats
	var execErr error
	feedbackSent := false
	heartbeatInterval := 5 * time.Second

	for {
		select {
		case execErr = <-done:
			bgCancel() // clean up background context
		case <-cmdCtx.Done():
			bgCancel() // kill the command
			execErr = cmdCtx.Err()
		case <-time.After(heartbeatInterval):
			// Auto-background: if the process has been running longer than
			// the threshold, move it to the background.
			if t.backgroundManager != nil && t.backgroundThreshold > 0 && time.Since(startTime) >= t.backgroundThreshold {
				proc := t.backgroundManager.Register(cmd, command, cwd, stdout, stderr, bgCancel)
				go func() {
					err := cmd.Wait()
					exitCode := 0
					if err != nil {
						if exitErr, ok := err.(*exec.ExitError); ok {
							exitCode = exitErr.ExitCode()
						} else {
							exitCode = 1
						}
					}
					t.backgroundManager.MarkCompleted(proc.ID, exitCode)
				}()
				msg := fmt.Sprintf("Command is still running after %v. Moved to background.\nProcess ID: %s\nUse list_background_execs to check status, get_background_exec_output to view output, stop_background_exec to stop it.", t.backgroundThreshold.Round(time.Second), proc.ID)
				return &ToolResult{
					ForLLM:  msg,
					ForUser: msg,
					IsError: false,
				}
			}
			// Existing feedback for long-running commands
			if verboseLevel == session.VerboseFull && feedbackCallback != nil && channel != "" && chatID != "" {
				elapsedSoFar := time.Since(startTime).Round(time.Second)
				feedbackMsg := fmt.Sprintf("🧰 Process: %s (running for %v)", processName, elapsedSoFar)
				feedbackCallback(channel, chatID, feedbackMsg)
				feedbackSent = true
			}
			continue
		}
		break // Exit the loop when command completes
	}

	elapsed := time.Since(startTime)

	// Check if context was cancelled due to timeout or user stop
	if cmdCtx.Err() == context.DeadlineExceeded {
		msg := fmt.Sprintf("Command timed out after %v", t.timeout)
		return &ToolResult{
			ForLLM:  msg,
			ForUser: msg,
			IsError: true,
		}
	}
	if cmdCtx.Err() == context.Canceled {
		msg := "Command was stopped by user"
		return &ToolResult{
			ForLLM:  msg,
			ForUser: msg,
			IsError: true,
		}
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if execErr != nil {
		output += fmt.Sprintf("\nExit code: %v", execErr)
	}

	if output == "" {
		output = "(no output)"
	}

	maxLen := 10000
	if len(output) > maxLen {
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, %d more chars)", len(output)-maxLen)
	}

	// If feedback was sent and command completed, optionally send completion message
	// (only if it took significant time)
	if feedbackSent && elapsed > 10*time.Second && t.feedbackCallback != nil {
		completionMsg := fmt.Sprintf("✅ Process %s completed (took %v)", processName, elapsed.Round(time.Second))
		t.feedbackCallback(t.channel, t.chatID, completionMsg)
	}

	if execErr != nil {
		return &ToolResult{
			ForLLM:  output,
			ForUser: output,
			IsError: true,
		}
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: false,
	}
}

func (t *ExecTool) guardCommand(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	for _, pattern := range t.denyPatterns {
		if pattern.MatchString(lower) {
			return "Command blocked by safety guard (dangerous pattern detected)"
		}
	}

	if len(t.allowPatterns) > 0 {
		allowed := false
		for _, pattern := range t.allowPatterns {
			if pattern.MatchString(lower) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "Command blocked by safety guard (not in allowlist)"
		}
	}

	if t.restrictToWorkspace {
		if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
			return "Command blocked by safety guard (path traversal detected)"
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return ""
		}

		pathPattern := regexp.MustCompile(`[A-Za-z]:\\[^\\\"']+|/[^\s\"']+`)
		matches := pathPattern.FindAllString(cmd, -1)

		for _, raw := range matches {
			p, err := filepath.Abs(raw)
			if err != nil {
				continue
			}

			rel, err := filepath.Rel(cwdPath, p)
			if err != nil {
				continue
			}

			if strings.HasPrefix(rel, "..") {
				return "Command blocked by safety guard (path outside working dir)"
			}
		}
	}

	return ""
}

func (t *ExecTool) guardCommandWithStatus(command, cwd string) (string, bool) {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	for _, pattern := range t.denyPatterns {
		if pattern.MatchString(lower) {
			return "Command blocked by safety guard (dangerous pattern detected)", true
		}
	}

	if len(t.allowPatterns) > 0 {
		allowed := false
		for _, pattern := range t.allowPatterns {
			if pattern.MatchString(lower) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "Command blocked by safety guard (not in allowlist)", false
		}
	}

	if t.restrictToWorkspace {
		if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
			return "Command blocked by safety guard (path traversal detected)", false
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return "", false
		}

		pathPattern := regexp.MustCompile(`[A-Za-z]:\\[^\\"']+|/[^\s"']+`)
		matches := pathPattern.FindAllString(cmd, -1)

		for _, raw := range matches {
			p, err := filepath.Abs(raw)
			if err != nil {
				continue
			}

			rel, err := filepath.Rel(cwdPath, p)
			if err != nil {
				continue
			}

			if strings.HasPrefix(rel, "..") {
				return "Command blocked by safety guard (path outside working dir)", false
			}
		}
	}

	return "", false
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

// SetKeyringService attaches a keyring service used to resolve {{SECRET:name}}
// placeholders in commands at run time.
func (t *ExecTool) SetKeyringService(svc *keyring.Service) {
	t.keyringSvc = svc
}

// SetSecretSubstitution enables or disables {{SECRET:name}} substitution.
func (t *ExecTool) SetSecretSubstitution(enabled bool) {
	t.secretSubstitution = enabled
}

// substituteSecrets replaces every {{SECRET:name}} placeholder in the command
// with the corresponding keyring value, scoped to the acting agent. The raw
// secret value is injected only here, immediately before execution, so it never
// appears in guard checks, logs, or session history. If no keyring is attached
// or substitution is disabled, the command is returned unchanged. An unknown or
// inaccessible secret yields an error so the command is not run with a literal
// placeholder.
func (t *ExecTool) substituteSecrets(ctx context.Context, command string) (string, error) {
	if !t.secretSubstitution || t.keyringSvc == nil {
		return command, nil
	}
	if !secretPlaceholderInlineRegex.MatchString(command) {
		return command, nil
	}

	agentID, sessionKey := AgentToolContextFromCtx(ctx)
	if agentID == "" {
		agentID = "unknown"
	}

	var subErr error
	resolved := secretPlaceholderInlineRegex.ReplaceAllStringFunc(command, func(match string) string {
		if subErr != nil {
			return match
		}
		name := secretPlaceholderInlineRegex.FindStringSubmatch(match)[1]
		name = strings.TrimSpace(name)
		value, err := t.keyringSvc.GetForAgent(name, agentID, sessionKey)
		if err != nil {
			subErr = fmt.Errorf("failed to resolve secret %q: %w", name, err)
			return match
		}
		return value
	})
	if subErr != nil {
		return "", subErr
	}
	return resolved, nil
}

func (t *ExecTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ExecTool) SetAllowPatterns(patterns []string) error {
	t.allowPatterns = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("invalid allow pattern %q: %w", p, err)
		}
		t.allowPatterns = append(t.allowPatterns, re)
	}
	return nil
}

// SetApprovalMode activa/desactiva el modo de aprobación para comandos peligrosos
func (t *ExecTool) SetApprovalMode(enabled bool) {
	t.approvalMode = enabled
}

// SetApprovalCallback configura la función de callback para solicitar aprobación
func (t *ExecTool) SetApprovalCallback(callback func(cmd string) (bool, error)) {
	t.approvalCallback = callback
}

// SetBypassGuard activa/desactiva el bypass de seguridad para comandos aprobados
func (t *ExecTool) SetBypassGuard(enabled bool) {
	t.bypassGuard = enabled
}

// SetBackgroundManager sets the background process manager for long-running commands.
func (t *ExecTool) SetBackgroundManager(m *BackgroundProcessManager) {
	t.backgroundManager = m
}

// SetBackgroundThreshold sets the duration threshold after which a command is
// automatically moved to the background.
func (t *ExecTool) SetBackgroundThreshold(d time.Duration) {
	t.backgroundThreshold = d
}
