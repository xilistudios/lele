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
	"strconv"
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
	keyringSvc          *keyring.Service
	secretSubstitution  bool

	whitelistMu   sync.RWMutex
	whitelistCmds map[string]struct{}
}

func NormalizeWhitelistKey(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(command), " "))
}

const (
	secretPlaceholderPrefix = "{{SECRET:"
	secretEnvPrefix         = "LELE_SECRET_"
)

var secretPlaceholderInlineRegex = regexp.MustCompile(`\{\{SECRET:([^}]+)\}\}`)

var secretValueShellMeta = regexp.MustCompile("[`;$|&<>\n\r\\\\\"']")

var secretEnvReferenceGuardRegex = regexp.MustCompile(`(?i)\$\{(?:env:)?` + secretEnvPrefix + `[0-9]+\}`)

var execBaseEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_PAGER=cat",
	"PAGER=cat",
	"DEBIAN_FRONTEND=noninteractive",
}

type shellQuoteState int

const (
	shellUnquoted shellQuoteState = iota
	shellSingleQuoted
	shellDoubleQuoted
)

type heredocSpec struct {
	delimiter string
	quoted    bool
	stripTabs bool
}

type injectedSecretCommand struct {
	script   string
	expanded string
	env      []string
}

type secretExpander struct {
	powershell  bool
	resolve     func(name string) (string, error)
	script      strings.Builder
	expanded    strings.Builder
	env         []string
	values      []string
	indexByName map[string]int
}

func secretEnvVariable(index int) string {
	return secretEnvPrefix + strconv.Itoa(index)
}

func secretEnvReference(index int, powershell bool) string {
	if powershell {
		return "${env:" + secretEnvVariable(index) + "}"
	}
	return "${" + secretEnvVariable(index) + "}"
}

func maskSecretEnvReferences(command string) string {
	return secretEnvReferenceGuardRegex.ReplaceAllString(command, "LELE_SECRET_REFERENCE")
}

func buildExecEnv(secretEnv []string) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+len(execBaseEnv)+len(secretEnv))
	for _, entry := range base {
		if name, _, ok := strings.Cut(entry, "="); ok {
			if strings.HasPrefix(strings.ToUpper(name), secretEnvPrefix) {
				continue
			}
		}
		env = append(env, entry)
	}
	env = append(env, execBaseEnv...)
	return append(env, secretEnv...)
}

func nextQuoteState(state shellQuoteState, c byte) shellQuoteState {
	switch {
	case c == '\'' && state == shellUnquoted:
		return shellSingleQuoted
	case c == '\'' && state == shellSingleQuoted:
		return shellUnquoted
	case c == '"' && state == shellUnquoted:
		return shellDoubleQuoted
	case c == '"' && state == shellDoubleQuoted:
		return shellUnquoted
	}
	return state
}

func (e *secretExpander) writeLiteral(text string) {
	e.script.WriteString(text)
	e.expanded.WriteString(text)
}

func (e *secretExpander) indexFor(name string) (int, error) {
	index, seen := e.indexByName[name]
	if seen {
		return index, nil
	}
	value, err := e.resolve(name)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve secret %q: %w", name, err)
	}
	if secretValueShellMeta.MatchString(value) {
		return 0, fmt.Errorf("secret %q contains shell metacharacters/substitution syntax and cannot be passed to a command environment; store the value without shell syntax", name)
	}
	index = len(e.env)
	e.indexByName[name] = index
	e.values = append(e.values, value)
	e.env = append(e.env, secretEnvVariable(index)+"="+value)
	return index, nil
}

func (e *secretExpander) writePlaceholder(name string) error {
	index, err := e.indexFor(name)
	if err != nil {
		return err
	}
	e.script.WriteString(secretEnvReference(index, e.powershell))
	e.expanded.WriteString(e.values[index])
	return nil
}

func (e *secretExpander) result() injectedSecretCommand {
	return injectedSecretCommand{
		script:   e.script.String(),
		expanded: e.expanded.String(),
		env:      e.env,
	}
}

func placeholderAt(text string, i int) (string, string, int) {
	if !strings.HasPrefix(text[i:], secretPlaceholderPrefix) {
		return "", "", i
	}
	loc := secretPlaceholderInlineRegex.FindStringIndex(text[i:])
	if loc == nil || loc[0] != 0 {
		return "", "", i
	}
	placeholder := text[i : i+loc[1]]
	name := strings.TrimSpace(secretPlaceholderInlineRegex.FindStringSubmatch(placeholder)[1])
	return placeholder, name, i + loc[1]
}

func isShellWordBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ';', '|', '&', '(', ')', '<', '>':
		return true
	}
	return false
}

func startsShellComment(line string, i int) bool {
	if i == 0 {
		return true
	}
	return isShellWordBreak(line[i-1])
}

func parseHeredocOperator(line string, start int) (*heredocSpec, int) {
	j := start + 2
	spec := &heredocSpec{}
	if j < len(line) && line[j] == '-' {
		spec.stripTabs = true
		j++
	}
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}

	var delimiter strings.Builder
	quoted := false
	for j < len(line) && !isShellWordBreak(line[j]) {
		c := line[j]
		if c == '\\' {
			quoted = true
			if j+1 < len(line) {
				delimiter.WriteByte(line[j+1])
			}
			j += 2
			continue
		}
		if c == '\'' || c == '"' {
			quoted = true
			j++
			for j < len(line) && line[j] != c {
				delimiter.WriteByte(line[j])
				j++
			}
			if j < len(line) {
				j++
			}
			continue
		}
		delimiter.WriteByte(c)
		j++
	}
	if delimiter.Len() == 0 {
		return nil, start
	}
	spec.delimiter = delimiter.String()
	spec.quoted = quoted
	if j > len(line) {
		j = len(line)
	}
	return spec, j
}

func isHeredocTerminator(line string, spec heredocSpec) bool {
	if spec.stripTabs {
		line = strings.TrimLeft(line, "\t")
	}
	return line == spec.delimiter
}

func quotedHeredocError(placeholder string, spec heredocSpec) error {
	return fmt.Errorf("secret %s is inside a heredoc whose delimiter is quoted (%q), where shells do not expand variables, so it cannot be injected via the environment; use an unquoted delimiter or move the placeholder outside the heredoc", placeholder, spec.delimiter)
}

func scanShellLine(line string, state *shellQuoteState, specs *[]heredocSpec, powershell bool, exp *secretExpander) error {
	inComment := false
	for i := 0; i < len(line); {
		c := line[i]

		if !inComment {
			if c == '\\' && !powershell && *state != shellSingleQuoted {
				end := min(i+2, len(line))
				exp.writeLiteral(line[i:end])
				i = end
				continue
			}

			if c == '#' && *state == shellUnquoted && startsShellComment(line, i) {
				inComment = true
				exp.writeLiteral(line[i : i+1])
				i++
				continue
			}

			if c == '\'' || c == '"' {
				*state = nextQuoteState(*state, c)
				exp.writeLiteral(line[i : i+1])
				i++
				continue
			}

			if c == '<' && *state == shellUnquoted && i+1 < len(line) && line[i+1] == '<' && (i+2 >= len(line) || line[i+2] != '<') {
				if spec, end := parseHeredocOperator(line, i); spec != nil {
					*specs = append(*specs, *spec)
					exp.writeLiteral(line[i:end])
					i = end
					continue
				}
			}
		}

		placeholder, name, next := placeholderAt(line, i)
		if placeholder == "" {
			exp.writeLiteral(line[i : i+1])
			i++
			continue
		}
		if *state == shellSingleQuoted {
			return fmt.Errorf("secret %s is inside single quotes, where shells do not expand variables, so it cannot be injected via the environment; move the placeholder outside single quotes or wrap it in double quotes", placeholder)
		}
		if err := exp.writePlaceholder(name); err != nil {
			return err
		}
		i = next
	}
	return nil
}

func expandHeredocBodyLine(line string, spec heredocSpec, exp *secretExpander) error {
	for i := 0; i < len(line); {
		placeholder, name, next := placeholderAt(line, i)
		if placeholder == "" {
			exp.writeLiteral(line[i : i+1])
			i++
			continue
		}
		if spec.quoted {
			return quotedHeredocError(placeholder, spec)
		}
		if err := exp.writePlaceholder(name); err != nil {
			return err
		}
		i = next
	}
	return nil
}

func scanAndExpand(command string, powershell bool, exp *secretExpander) error {
	state := shellUnquoted
	var bodies []heredocSpec
	for i := 0; i < len(command); {
		end := strings.IndexByte(command[i:], '\n')
		hasNewline := end >= 0
		if hasNewline {
			end += i
		} else {
			end = len(command)
		}
		line := command[i:end]

		switch {
		case len(bodies) == 0:
			var specs []heredocSpec
			if err := scanShellLine(line, &state, &specs, powershell, exp); err != nil {
				return err
			}
			bodies = specs
		case isHeredocTerminator(line, bodies[0]):
			exp.writeLiteral(line)
			bodies = bodies[1:]
			if len(bodies) == 0 {
				state = shellUnquoted
			}
		default:
			if err := expandHeredocBodyLine(line, bodies[0], exp); err != nil {
				return err
			}
		}

		if hasNewline {
			exp.writeLiteral("\n")
			i = end + 1
		} else {
			i = end
		}
	}
	return nil
}

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
	return "Execute a shell command and return its output. Use with caution. Supports {{SECRET:name}} placeholders that are resolved at run time: the value is passed to the child process through its environment (LELE_SECRET_0, LELE_SECRET_1, ...) and the command only carries a ${LELE_SECRET_<n>} reference (${env:LELE_SECRET_<n>} on PowerShell), so the raw value does not appear in process argv. The value is inherited by the whole child process tree, and any command that prints its environment (env, printenv, set -x) writes it into the tool output and therefore into the session: do not dump the environment in a command that references a secret. Placeholders inside single quotes or inside a heredoc with a quoted delimiter are rejected because neither expands variables: use double quotes (recommended, it also preserves values containing whitespace), no quotes, or an unquoted heredoc delimiter. Secret values containing shell metacharacters are rejected."
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
				"description": "If true, run the command in the background immediately. Returns a process ID that can be used with list_background_execs, get_background_exec_output, and stop_background_exec. Default: false (a still-running command auto-backgrounds 5 seconds before its deadline and at most 60 seconds after it starts; auto-backgrounding is disabled when the deadline is 5 seconds away or less).",
			},
		},
		"required": []string{"command"},
	}
}

const (
	autoBackgroundMargin = 5 * time.Second
	autoBackgroundCap    = 60 * time.Second
)

func autoBackgroundDelay(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return autoBackgroundCap
	}
	if timeout <= autoBackgroundMargin {
		return 0
	}
	return min(autoBackgroundCap, timeout-autoBackgroundMargin)
}

func autoBackgroundDelayFor(cmdCtx context.Context) time.Duration {
	deadline, ok := cmdCtx.Deadline()
	if !ok {
		return autoBackgroundDelay(0)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	return autoBackgroundDelay(remaining)
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

	resolved, err := t.substituteSecrets(ctx, command)
	if err != nil {
		return ErrorResult(err.Error())
	}

	if resolved.script != command {
		if origMsg, _ := t.guardCommandWithStatus(command, cwd); origMsg == "" {
			if guardMsg, _ := t.guardCommandWithStatus(resolved.expanded, cwd); guardMsg != "" {
				return ErrorResult("post-substitution safety guard: " + guardMsg)
			}
		}
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(bgCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", resolved.script)
	} else {
		cmd = exec.CommandContext(bgCtx, "sh", "-c", resolved.script)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	cmd.Env = buildExecEnv(resolved.env)

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

	autoBgDelay := autoBackgroundDelayFor(cmdCtx)
	var autoBgC <-chan time.Time
	if t.backgroundManager != nil && autoBgDelay > 0 {
		autoBgTimer := time.NewTimer(autoBgDelay)
		defer autoBgTimer.Stop()
		autoBgC = autoBgTimer.C
	}

	for {
		select {
		case execErr = <-done:
			bgCancel()
		case <-cmdCtx.Done():
			bgCancel()
			execErr = cmdCtx.Err()
		case <-autoBgC:
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
			msg := fmt.Sprintf("Command is still running after %v. Moved to background.\nProcess ID: %s\nUse list_background_execs to check status, get_background_exec_output to view output, stop_background_exec to stop it.", autoBgDelay.Round(time.Second), proc.ID)
			return &ToolResult{
				ForLLM:  msg,
				ForUser: msg,
				IsError: false,
			}
		case <-time.After(heartbeatInterval):
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
	lower := maskSecretEnvReferences(strings.ToLower(cmd))

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

func (t *ExecTool) substituteSecrets(ctx context.Context, command string) (injectedSecretCommand, error) {
	return t.substituteSecretsForShell(ctx, command, runtime.GOOS == "windows")
}

func (t *ExecTool) substituteSecretsForShell(ctx context.Context, command string, powershell bool) (injectedSecretCommand, error) {
	unchanged := injectedSecretCommand{script: command, expanded: command}
	if !t.secretSubstitution || t.keyringSvc == nil || !strings.Contains(command, secretPlaceholderPrefix) {
		return unchanged, nil
	}

	agentID, sessionKey := AgentToolContextFromCtx(ctx)
	if agentID == "" {
		agentID = "unknown"
	}

	svc := t.keyringSvc
	exp := &secretExpander{
		powershell:  powershell,
		indexByName: make(map[string]int),
		resolve: func(name string) (string, error) {
			return svc.GetForAgent(name, agentID, sessionKey)
		},
	}
	exp.script.Grow(len(command))
	exp.expanded.Grow(len(command))

	if err := scanAndExpand(command, powershell, exp); err != nil {
		return unchanged, err
	}
	return exp.result(), nil
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
