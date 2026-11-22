package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type threadSafeBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	maxSize int
}

func newThreadSafeBuffer(maxSize int) *threadSafeBuffer {
	return &threadSafeBuffer{maxSize: maxSize}
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.maxSize > 0 && b.buf.Len() > b.maxSize {
		data := b.buf.Bytes()
		excess := b.buf.Len() - b.maxSize
		b.buf.Reset()
		b.buf.Write(data[excess:])
	}
	return n, err
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *threadSafeBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

const (
	BgExecStatusRunning   = "running"
	BgExecStatusCompleted = "completed"
	BgExecStatusStopped   = "stopped"
	BgExecStatusFailed    = "failed"
)

type BackgroundProcess struct {
	ID              string
	Command         string
	WorkingDir      string
	Status          string
	StartTime       time.Time
	EndTime         *time.Time
	ExitCode        int
	OwnerSessionKey string
	stdout          *threadSafeBuffer
	stderr          *threadSafeBuffer
	cmd             *exec.Cmd
	cancel          context.CancelFunc
	mu              sync.RWMutex
}

func (p *BackgroundProcess) VisibleTo(callerSessionKey string) bool {
	p.mu.RLock()
	owner := p.OwnerSessionKey
	p.mu.RUnlock()

	if owner == "" || callerSessionKey == "" {
		return true
	}
	return SessionKeysRelated(owner, callerSessionKey)
}

func (p *BackgroundProcess) Output() string {
	out := p.stdout.String()
	errOut := p.stderr.String()

	if errOut != "" {
		out += "\nSTDERR:\n" + errOut
	}

	if out == "" {
		return "(no output)"
	}
	return out
}

func (p *BackgroundProcess) OutputTail(tail int) string {
	out := p.Output()
	if tail > 0 && len(out) > tail {
		return out[len(out)-tail:]
	}
	return out
}

func (p *BackgroundProcess) Elapsed() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.EndTime != nil {
		return p.EndTime.Sub(p.StartTime)
	}
	return time.Since(p.StartTime)
}

func (p *BackgroundProcess) ExitInfo() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ExitCode != 0 {
		return fmt.Sprintf("\nExit code: %d", p.ExitCode)
	}
	return ""
}

func (p *BackgroundProcess) releaseBuffers() {
	const maxRetainedOutput = 64 * 1024

	if p.stdout != nil {
		out := p.stdout.String()
		if len(out) > maxRetainedOutput {
			out = out[len(out)-maxRetainedOutput:]
		}
		p.stdout = newThreadSafeBuffer(0)
		p.stdout.buf.WriteString(out)
	}

	if p.stderr != nil {
		errOut := p.stderr.String()
		if len(errOut) > maxRetainedOutput {
			errOut = errOut[len(errOut)-maxRetainedOutput:]
		}
		p.stderr = newThreadSafeBuffer(0)
		p.stderr.buf.WriteString(errOut)
	}
}

type BackgroundProcessManager struct {
	mu              sync.RWMutex
	processes       map[string]*BackgroundProcess
	nextID          int
	retentionPeriod time.Duration
	maxProcesses    int
}

func NewBackgroundProcessManager() *BackgroundProcessManager {
	return &BackgroundProcessManager{
		processes:       make(map[string]*BackgroundProcess),
		nextID:          1,
		retentionPeriod: 5 * time.Minute,
	}
}

func (m *BackgroundProcessManager) SetRetentionPeriod(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retentionPeriod = d
}

func (m *BackgroundProcessManager) SetMaxProcesses(max int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxProcesses = max
}

func (m *BackgroundProcessManager) Register(cmd *exec.Cmd, command, workingDir string, stdout, stderr *threadSafeBuffer, cancel context.CancelFunc, ownerSessionKey string) *BackgroundProcess {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.maxProcesses > 0 && len(m.processes) >= m.maxProcesses {
		m.cleanupTerminalLocked()
	}

	id := fmt.Sprintf("bg-%d", m.nextID)
	m.nextID++

	p := &BackgroundProcess{
		ID:              id,
		Command:         command,
		WorkingDir:      workingDir,
		Status:          BgExecStatusRunning,
		StartTime:       time.Now(),
		OwnerSessionKey: ownerSessionKey,
		stdout:          stdout,
		stderr:          stderr,
		cmd:             cmd,
		cancel:          cancel,
	}

	m.processes[id] = p
	return p
}

func (m *BackgroundProcessManager) Get(id string) (*BackgroundProcess, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.processes[id]
	return p, ok
}

func (m *BackgroundProcessManager) List() []*BackgroundProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.processes))
	for id := range m.processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]*BackgroundProcess, 0, len(ids))
	for _, id := range ids {
		result = append(result, m.processes[id])
	}
	return result
}

func (m *BackgroundProcessManager) ListRunning() []*BackgroundProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0)
	for id, p := range m.processes {
		if p.Status == BgExecStatusRunning {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	result := make([]*BackgroundProcess, 0, len(ids))
	for _, id := range ids {
		result = append(result, m.processes[id])
	}
	return result
}

func (m *BackgroundProcessManager) ListForSession(sessionKey string) []*BackgroundProcess {
	all := m.List()
	result := make([]*BackgroundProcess, 0, len(all))
	for _, p := range all {
		if p.VisibleTo(sessionKey) {
			result = append(result, p)
		}
	}
	return result
}

func (m *BackgroundProcessManager) ListRunningForSession(sessionKey string) []*BackgroundProcess {
	all := m.ListRunning()
	result := make([]*BackgroundProcess, 0, len(all))
	for _, p := range all {
		if p.VisibleTo(sessionKey) {
			result = append(result, p)
		}
	}
	return result
}

func (m *BackgroundProcessManager) GetForSession(id, sessionKey string) (*BackgroundProcess, bool) {
	p, ok := m.Get(id)
	if !ok || !p.VisibleTo(sessionKey) {
		return nil, false
	}
	return p, true
}

func (m *BackgroundProcessManager) Stop(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.processes[id]
	if !ok || p.Status != BgExecStatusRunning {
		return false
	}

	if p.cancel != nil {
		p.cancel()
	}
	killProcessTree(p.cmd)

	now := time.Now()
	p.mu.Lock()
	p.Status = BgExecStatusStopped
	p.EndTime = &now
	p.releaseBuffers()
	p.mu.Unlock()

	return true
}

func (m *BackgroundProcessManager) StopAll() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, p := range m.processes {
		if m.stopLocked(p) {
			count++
		}
	}
	return count
}

func (m *BackgroundProcessManager) StopForSession(sessionKey string) int {
	if sessionKey == "" {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, p := range m.processes {
		p.mu.RLock()
		owner := p.OwnerSessionKey
		p.mu.RUnlock()
		if !SessionKeysRelated(owner, sessionKey) {
			continue
		}
		if m.stopLocked(p) {
			count++
		}
	}
	return count
}

func (m *BackgroundProcessManager) stopLocked(p *BackgroundProcess) bool {
	if p == nil || p.Status != BgExecStatusRunning {
		return false
	}

	if p.cancel != nil {
		p.cancel()
	}
	killProcessTree(p.cmd)

	now := time.Now()
	p.mu.Lock()
	p.Status = BgExecStatusStopped
	p.EndTime = &now
	p.releaseBuffers()
	p.mu.Unlock()

	return true
}

func (m *BackgroundProcessManager) MarkCompleted(id string, exitCode int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.processes[id]
	if !ok {
		return
	}

	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	if exitCode == 0 {
		p.Status = BgExecStatusCompleted
	} else {
		p.Status = BgExecStatusFailed
	}
	p.ExitCode = exitCode
	p.EndTime = &now
	p.releaseBuffers()
}

func (m *BackgroundProcessManager) CleanupTerminal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupTerminalLocked()
}

func (m *BackgroundProcessManager) cleanupTerminalLocked() {
	if m.retentionPeriod <= 0 {
		return
	}
	cutoff := time.Now().Add(-m.retentionPeriod)
	for id, p := range m.processes {
		if p.Status == BgExecStatusRunning {
			continue
		}
		endTime := p.StartTime
		if p.EndTime != nil {
			endTime = *p.EndTime
		}
		if endTime.Before(cutoff) {
			delete(m.processes, id)
		}
	}
}

func (m *BackgroundProcessManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.processes)
}

type ListBackgroundExecsTool struct {
	manager *BackgroundProcessManager
}

func NewListBackgroundExecsTool(m *BackgroundProcessManager) *ListBackgroundExecsTool {
	return &ListBackgroundExecsTool{manager: m}
}

func (t *ListBackgroundExecsTool) Name() string {
	return "list_background_execs"
}

func (t *ListBackgroundExecsTool) Description() string {
	return "List all background shell processes. Shows their ID, command, status (running/completed/stopped/failed), and elapsed time. Use this to check on commands that were automatically moved to background or explicitly started in background mode."
}

func (t *ListBackgroundExecsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_completed": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, include completed/stopped/failed processes. Default: false (only running).",
			},
		},
	}
}

func (t *ListBackgroundExecsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	includeCompleted, _ := args["include_completed"].(bool)
	_, sessionKey := AgentToolContextFromCtx(ctx)

	var procs []*BackgroundProcess
	if includeCompleted {
		procs = t.manager.ListForSession(sessionKey)
	} else {
		procs = t.manager.ListRunningForSession(sessionKey)
	}

	if len(procs) == 0 {
		return SilentResult("No background processes found.")
	}

	var lines []string
	for _, p := range procs {
		lines = append(lines, fmt.Sprintf("ID: %s | Status: %s | Elapsed: %s | Command: %s",
			p.ID, p.Status, p.Elapsed().Round(time.Second), p.Command))
	}

	return SilentResult(strings.Join(lines, "\n"))
}

type GetBackgroundExecOutputTool struct {
	manager *BackgroundProcessManager
}

func NewGetBackgroundExecOutputTool(m *BackgroundProcessManager) *GetBackgroundExecOutputTool {
	return &GetBackgroundExecOutputTool{manager: m}
}

func (t *GetBackgroundExecOutputTool) Name() string {
	return "get_background_exec_output"
}

func (t *GetBackgroundExecOutputTool) Description() string {
	return "Get the current output (stdout and stderr) of a background shell process. Works for both running and completed processes. Output is truncated to 10,000 characters. Use the process ID from list_background_execs."
}

func (t *GetBackgroundExecOutputTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "The background process ID (e.g., 'bg-1')",
			},
			"tail": map[string]interface{}{
				"type":        "integer",
				"description": "If set, return only the last N characters of output. Useful for long-running processes with lots of output.",
				"minimum":     1,
			},
		},
		"required": []string{"id"},
	}
}

func (t *GetBackgroundExecOutputTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return ErrorResult("id is required")
	}
	_, sessionKey := AgentToolContextFromCtx(ctx)

	p, ok := t.manager.GetForSession(id, sessionKey)
	if !ok {
		return ErrorResult(fmt.Sprintf("background process not found: %s", id))
	}

	output := p.Output()

	if tailVal, ok := args["tail"]; ok {
		if tail, ok := toInt(tailVal); ok && tail > 0 && len(output) > tail {
			output = output[len(output)-tail:]
		}
	}

	const maxLen = 10000
	truncated := ""
	if len(output) > maxLen {
		truncated = fmt.Sprintf("\n... (truncated, %d more chars)", len(output)-maxLen)
		output = output[:maxLen]
	}

	header := fmt.Sprintf("[Status: %s | Elapsed: %s]", p.Status, p.Elapsed().Round(time.Second))
	result := header + "\n" + output + truncated

	return SilentResult(result)
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

type StopBackgroundExecTool struct {
	manager *BackgroundProcessManager
}

func NewStopBackgroundExecTool(m *BackgroundProcessManager) *StopBackgroundExecTool {
	return &StopBackgroundExecTool{manager: m}
}

func (t *StopBackgroundExecTool) Name() string {
	return "stop_background_exec"
}

func (t *StopBackgroundExecTool) Description() string {
	return "Stop a running background shell process. Sends a kill signal to the process. The process status will change to 'stopped'. Use the process ID from list_background_execs."
}

func (t *StopBackgroundExecTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "The background process ID to stop (e.g., 'bg-1')",
			},
		},
		"required": []string{"id"},
	}
}

func (t *StopBackgroundExecTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return ErrorResult("id is required")
	}
	_, sessionKey := AgentToolContextFromCtx(ctx)

	p, exists := t.manager.GetForSession(id, sessionKey)
	if !exists {
		return ErrorResult(fmt.Sprintf("background process not found: %s", id))
	}

	if p.Status != BgExecStatusRunning {
		return ErrorResult(fmt.Sprintf("process %s is not running (status: %s)", id, p.Status))
	}

	elapsed := p.Elapsed()
	if !t.manager.Stop(id) {
		return ErrorResult(fmt.Sprintf("failed to stop process %s", id))
	}

	return SilentResult(fmt.Sprintf("Stopped background process %s (was running for %s).", id, elapsed.Round(time.Second)))
}
