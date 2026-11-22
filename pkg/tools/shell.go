package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
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
	approvalMode        bool
	approvalCallback    func(cmd string) (bool, error)
	channel             string
	chatID              string
	feedbackCallback    func(channel, chatID, message string)
	verboseLevel        session.VerboseLevel
	backgroundManager   *BackgroundProcessManager
	backgroundThreshold time.Duration
	keyringSvc          *keyring.Service
	secretSubstitution  bool

	whitelistMu   sync.RWMutex
	whitelistCmds map[string]struct{}
}

func NormalizeWhitelistKey(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(command), " "))
}

var secretPlaceholderInlineRegex = regexp.MustCompile(`\{\{SECRET:([^}]+)\}\}`)

var secretValueShellMeta = regexp.MustCompile("[`;$|&<>\n\r\\\\\"']")

func (t *ExecTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

func (t *ExecTool) SetFeedbackCallback(callback func(channel, chatID, message string)) {
	t.feedbackCallback = callback
}

func (t *ExecTool) SetVerbose(level session.VerboseLevel) {
	t.verboseLevel = level
}

var defaultDenyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
	regexp.MustCompile(`\bdel\s+/[fq]\b`),
	regexp.MustCompile(`\brmdir\s+/s\b`),
	regexp.MustCompile(`\b(format|mkfs|diskpart)\b\s`),
	regexp.MustCompile(`\bdd\s+if=`),
	regexp.MustCompile(`>\s*/dev/sd[a-z]\b`),
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
			denyPatterns = append(denyPatterns, defaultDenyPatterns...)
			if len(execConfig.CustomDenyPatterns) > 0 {
				logger.DebugCF("tools", "adding custom deny patterns", map[string]interface{}{"patterns": execConfig.CustomDenyPatterns})
				for _, pattern := range execConfig.CustomDenyPatterns {
					re, err := regexp.Compile(pattern)
					if err != nil {
						logger.WarnCF("tools", "invalid custom deny pattern", map[string]interface{}{"pattern": pattern, "error": err.Error()})
						continue
					}
					denyPatterns = append(denyPatterns, re)
				}
			}
		} else {
			logger.WarnCF("tools", "deny patterns are disabled. All commands will be allowed.", nil)
		}
	} else {
		denyPatterns = append(denyPatterns, defaultDenyPatterns...)
	}

	timeout := 60 * time.Second
	if config != nil {
		timeout = time.Duration(config.Tools.Exec.TimeoutSeconds) * time.Second
	}

	t := &ExecTool{
		workingDir:          workingDir,
		timeout:             timeout,
		denyPatterns:        denyPatterns,
		allowPatterns:       nil,
		restrictToWorkspace: restrict,
		backgroundThreshold: 60 * time.Second,
		secretSubstitution:  true,
		whitelistCmds:       make(map[string]struct{}),
	}
	if config != nil {
		t.SetWhitelist(config.Tools.Exec.WhitelistCommands)
	}
	return t
}

func (t *ExecTool) SetWhitelist(commands []string) {
	next := make(map[string]struct{}, len(commands))
	for _, c := range commands {
		key := NormalizeWhitelistKey(c)
		if key == "" {
			continue
		}
		next[key] = struct{}{}
	}
	t.whitelistMu.Lock()
	t.whitelistCmds = next
	t.whitelistMu.Unlock()
}

func (t *ExecTool) AddWhitelistCommand(command string) bool {
	key := NormalizeWhitelistKey(command)
	if key == "" {
		return false
	}
	t.whitelistMu.Lock()
	defer t.whitelistMu.Unlock()
	if t.whitelistCmds == nil {
		t.whitelistCmds = make(map[string]struct{})
	}
	if _, ok := t.whitelistCmds[key]; ok {
		return false
	}
	t.whitelistCmds[key] = struct{}{}
	return true
}

func (t *ExecTool) Whitelisted(command string) bool {
	key := NormalizeWhitelistKey(command)
	if key == "" {
		return false
	}
	t.whitelistMu.RLock()
	defer t.whitelistMu.RUnlock()
	_, ok := t.whitelistCmds[key]
	return ok
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

	backgroundMode, _ := args["background"].(bool)

	if t.restrictToWorkspace && cwd != "" {
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			return ErrorResult(fmt.Sprintf("invalid working_dir: %v", err))
		}
		absWorkspace, err := filepath.Abs(t.workingDir)
		if err != nil {
			return ErrorResult(fmt.Sprintf("invalid workspace: %v", err))
		}
		if !isWithinWorkspace(absCwd, absWorkspace) {
			return ErrorResult("access denied: working_dir is outside the workspace")
		}
	}

	if !BypassGuardFor(ctx, command) {
		guardMsg, isBlockable := t.guardCommandWithStatus(command, cwd)
		if guardMsg != "" {
			if t.approvalMode && isBlockable {
				return &ToolResult{
					ForLLM: fmt.Sprintf(
						"Command '%s' was NOT executed: it is blocked by an approval guard (%s) "+
							"and this session has no interactive approver. Do not retry it or "+
							"rephrase it — the guard is not a transient state. Either accomplish "+
							"the goal without this command, or finish with STATUS: not_done and "+
							"explain in DETAILS that the task needs a command the operator must "+
							"run manually.",
						command, guardMsg),
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

	var cmdCtx context.Context
	var cancel context.CancelFunc
	if t.timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, t.timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	resolvedCommand, err := t.substituteSecrets(ctx, command)
	if err != nil {
		return ErrorResult(err.Error())
	}

	if resolvedCommand != command {
		if origMsg, _ := t.guardCommandWithStatus(command, cwd); origMsg == "" {
			if guardMsg, _ := t.guardCommandWithStatus(resolvedCommand, cwd); guardMsg != "" {
				return ErrorResult("post-substitution safety guard: " + guardMsg)
			}
		}
	}
	command = resolvedCommand

	bgCtx, bgCancel := context.WithCancel(context.Background())

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(bgCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(bgCtx, "sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"PAGER=cat",
		"DEBIAN_FRONTEND=noninteractive",
	)

	cmd.Stdin = nil

	detachControllingTerminal(cmd)

	cmd.Cancel = func() error {
		killProcessTree(cmd)
		return nil
	}
	cmd.WaitDelay = 3 * time.Second

	stdout := newThreadSafeBuffer(1024 * 1024)
	stderr := newThreadSafeBuffer(1024 * 1024)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	processName := utils.RandomProcessName()

	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		bgCancel()
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	if backgroundMode && t.backgroundManager != nil {
		_, ownerSessionKey := AgentToolContextFromCtx(ctx)
		proc := t.backgroundManager.Register(cmd, command, cwd, stdout, stderr, bgCancel, ownerSessionKey)
		go func() {
			err := cmd.Wait()
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else if errors.Is(err, exec.ErrWaitDelay) {
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

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var execErr error
	feedbackSent := false
	heartbeatInterval := 5 * time.Second

	for {
		select {
		case execErr = <-done:
			bgCancel()
		case <-cmdCtx.Done():
			bgCancel()
			execErr = cmdCtx.Err()
		case <-time.After(heartbeatInterval):
			if t.backgroundManager != nil && t.backgroundThreshold > 0 && time.Since(startTime) >= t.backgroundThreshold {
				_, ownerSessionKey := AgentToolContextFromCtx(ctx)
				proc := t.backgroundManager.Register(cmd, command, cwd, stdout, stderr, bgCancel, ownerSessionKey)
				go func() {
					err := <-done
					exitCode := 0
					if err != nil {
						if exitErr, ok := err.(*exec.ExitError); ok {
							exitCode = exitErr.ExitCode()
						} else if errors.Is(err, exec.ErrWaitDelay) {
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
			if verboseLevel == session.VerboseFull && feedbackCallback != nil && channel != "" && chatID != "" {
				elapsedSoFar := time.Since(startTime).Round(time.Second)
				feedbackMsg := fmt.Sprintf("🧰 Process: %s (running for %v)", processName, elapsedSoFar)
				feedbackCallback(channel, chatID, feedbackMsg)
				feedbackSent = true
			}
			continue
		}
		break
	}

	elapsed := time.Since(startTime)

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

	ioDrainTimedOut := errors.Is(execErr, exec.ErrWaitDelay)
	if execErr != nil && !ioDrainTimedOut {
		output += fmt.Sprintf("\nExit code: %v", execErr)
	}
	if ioDrainTimedOut {
		output += "\n(note: process exited successfully; a descendant still holding the output pipes was disconnected after the wait delay — trailing output may be truncated)"
	}

	if output == "" {
		output = "(no output)"
	}

	const outputHeadBytes = 48 * 1024
	const outputTailBytes = 16 * 1024
	output = utils.TruncateOutput(output, outputHeadBytes, outputTailBytes)

	if feedbackSent && elapsed > 10*time.Second && t.feedbackCallback != nil {
		completionMsg := fmt.Sprintf("✅ Process %s completed (took %v)", processName, elapsed.Round(time.Second))
		t.feedbackCallback(t.channel, t.chatID, completionMsg)
	}

	if execErr != nil && !ioDrainTimedOut {
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
	msg, _ := t.guardCommandWithStatus(command, cwd)
	return msg
}

func (t *ExecTool) guardCommandWithStatus(command, cwd string) (string, bool) {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	if t.Whitelisted(cmd) {
		return "", false
	}

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

func (t *ExecTool) SetKeyringService(svc *keyring.Service) {
	t.keyringSvc = svc
}

func (t *ExecTool) SetSecretSubstitution(enabled bool) {
	t.secretSubstitution = enabled
}

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
		if secretValueShellMeta.MatchString(value) {
			subErr = fmt.Errorf("secret %q contains shell metacharacters/substitution syntax and cannot be inlined into a command; store the value without shell syntax or pass it via env", name)
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

func (t *ExecTool) SetApprovalMode(enabled bool) {
	t.approvalMode = enabled
}

func (t *ExecTool) SetApprovalCallback(callback func(cmd string) (bool, error)) {
	t.approvalCallback = callback
}

func (t *ExecTool) SetBypassGuard(_ bool) {
}

func (t *ExecTool) SetBackgroundManager(m *BackgroundProcessManager) {
	t.backgroundManager = m
}

func (t *ExecTool) SetBackgroundThreshold(d time.Duration) {
	t.backgroundThreshold = d
}
