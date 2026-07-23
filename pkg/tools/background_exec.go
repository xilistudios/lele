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

// ---------------------------------------------------------------------------
// threadSafeBuffer – a bytes.Buffer protected by a mutex for concurrent use
// ---------------------------------------------------------------------------

type threadSafeBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	maxSize int // 0 = unlimited
}

func newThreadSafeBuffer(maxSize int) *threadSafeBuffer {
	return &threadSafeBuffer{maxSize: maxSize}
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.maxSize > 0 && b.buf.Len() > b.maxSize {
		// Keep only the last maxSize bytes
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

// ---------------------------------------------------------------------------
// BackgroundProcess – represents a single backgrounded shell process
// ---------------------------------------------------------------------------

const (
	BgExecStatusRunning   = "running"
	BgExecStatusCompleted = "completed"
	BgExecStatusStopped   = "stopped"
	BgExecStatusFailed    = "failed"
)

type BackgroundProcess struct {
	ID         string
	Command    string
	WorkingDir string
	Status     string
	StartTime  time.Time
	EndTime    *time.Time
	ExitCode   int
	stdout     *threadSafeBuffer
	stderr     *threadSafeBuffer
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	mu         sync.RWMutex
}

// Output returns combined stdout + stderr (stderr prefixed with "\nSTDERR:\n").
// If both are empty it returns "(no output)".
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

// OutputTail returns the last N characters of combined stdout+stderr.
// If tail <= 0 or the output is shorter than tail, it returns the full output.
func (p *BackgroundProcess) OutputTail(tail int) string {
	out := p.Output()
	if tail > 0 && len(out) > tail {
		return out[len(out)-tail:]
	}
	return out
}

// Elapsed returns the duration the process has been (or was) running.
func (p *BackgroundProcess) Elapsed() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.EndTime != nil {
		return p.EndTime.Sub(p.StartTime)
	}
	return time.Since(p.StartTime)
}

// ExitInfo returns a formatted exit-code suffix, or "" when exit code is 0.
func (p *BackgroundProcess) ExitInfo() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ExitCode != 0 {
		return fmt.Sprintf("\nExit code: %d", p.ExitCode)
	}
	return ""
}

// ---------------------------------------------------------------------------
// BackgroundProcessManager – manages background processes (thread-safe)
// ---------------------------------------------------------------------------

type BackgroundProcessManager struct {
	mu              sync.RWMutex
	processes       map[string]*BackgroundProcess
	nextID          int
	retentionPeriod time.Duration
	maxProcesses    int // 0 = unlimited
}

func NewBackgroundProcessManager() *BackgroundProcessManager {
	return &BackgroundProcessManager{
		processes:       make(map[string]*BackgroundProcess),
		nextID:          1,
		retentionPeriod: 10 * time.Minute,
		
	}
}

// SetRetentionPeriod sets how long terminal processes are kept before cleanup.
func (m *BackgroundProcessManager) SetRetentionPeriod(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retentionPeriod = d
}

// SetMaxProcesses sets the maximum number of tracked processes.
// 0 means unlimited.
func (m *BackgroundProcessManager) SetMaxProcesses(max int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxProcesses = max
}

// Register creates a new BackgroundProcess with an auto-incremented ID
// (format "bg-{N}"), stores it, and returns it.
// IMPORTANT: cmd.Stdout / cmd.Stderr are assumed to already be set by the
// caller; this method only stores references to the provided buffers.
func (m *BackgroundProcessManager) Register(cmd *exec.Cmd, command, workingDir string, stdout, stderr *threadSafeBuffer, cancel context.CancelFunc) *BackgroundProcess {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Enforce max processes limit by cleaning up terminal processes first
	if m.maxProcesses > 0 && len(m.processes) >= m.maxProcesses {
		m.cleanupTerminalLocked()
	}

	id := fmt.Sprintf("bg-%d", m.nextID)
	m.nextID++

	p := &BackgroundProcess{
		ID:         id,
		Command:    command,
		WorkingDir: workingDir,
		Status:     BgExecStatusRunning,
		StartTime:  time.Now(),
		stdout:     stdout,
		stderr:     stderr,
		cmd:        cmd,
		cancel:     cancel,
	}

	m.processes[id] = p
	return p
}

// Get returns a process by ID.
func (m *BackgroundProcessManager) Get(id string) (*BackgroundProcess, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.processes[id]
	return p, ok
}

// List returns all processes sorted by ID.
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

// ListRunning returns only running processes.
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

// Stop stops a running process: calls cancel(), kills the process if possible,
// and sets status to stopped. Returns false if not found or not running.
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
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}

	now := time.Now()
	p.mu.Lock()
	p.Status = BgExecStatusStopped
	p.EndTime = &now
	p.mu.Unlock()

	return true
}

// StopAll stops all running processes and returns the count.
func (m *BackgroundProcessManager) StopAll() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, p := range m.processes {
		if p.Status == BgExecStatusRunning {
			if p.cancel != nil {
				p.cancel()
			}
			if p.cmd != nil && p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}

			now := time.Now()
			p.mu.Lock()
			p.Status = BgExecStatusStopped
			p.EndTime = &now
			p.mu.Unlock()

			count++
		}
	}
	return count
}

// MarkCompleted sets the status and exit code for a process.
// No-op if the process ID is not found.
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
}

// CleanupTerminal removes terminal (non-running) processes older than the
// retention period. No-op if retentionPeriod <= 0.
func (m *BackgroundProcessManager) CleanupTerminal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupTerminalLocked()
}

// cleanupTerminalLocked removes terminal processes. Caller must hold the lock.
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

// Count returns the number of tracked processes.
func (m *BackgroundProcessManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.processes)
}

// ---------------------------------------------------------------------------
// ListBackgroundExecsTool – lists all background processes
// ---------------------------------------------------------------------------

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

	var procs []*BackgroundProcess
	if includeCompleted {
		procs = t.manager.List()
	} else {
		procs = t.manager.ListRunning()
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

// ---------------------------------------------------------------------------
// GetBackgroundExecOutputTool – gets stdout+stderr of a background process
// ---------------------------------------------------------------------------

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

	p, ok := t.manager.Get(id)
	if !ok {
		return ErrorResult(fmt.Sprintf("background process not found: %s", id))
	}

	output := p.Output()

	// Apply tail if requested
	if tailVal, ok := args["tail"]; ok {
		if tail, ok := toInt(tailVal); ok && tail > 0 && len(output) > tail {
			output = output[len(output)-tail:]
		}
	}

	// Truncate to 10,000 chars max
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

// toInt converts a numeric interface{} to (int, bool). Handles float64 (JSON default) and int.
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

// ---------------------------------------------------------------------------
// StopBackgroundExecTool – stops a running background process
// ---------------------------------------------------------------------------

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

	p, exists := t.manager.Get(id)
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
