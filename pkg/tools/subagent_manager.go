package tools

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

type SubagentManager struct {
	tasks                      map[string]*SubagentTask
	cancels                    map[string]context.CancelFunc
	mu                         sync.RWMutex
	provider                   providers.LLMProvider
	defaultModel               string
	bus                        *bus.MessageBus
	workspace                  string
	tools                      *ToolRegistry
	getAgentContext            func(agentID string) AgentContextInfo
	modelOverrideResolver      func(model string) (providers.LLMProvider, string, int, error)
	visionChecker              func(model string) bool
	videoChecker               func(model string) bool
	maxIterations              int
	maxTokens                  int
	temperature                float64
	hasMaxTokens               bool
	hasTemperature             bool
	thinkingLevel              string
	timeout                    time.Duration
	retentionPeriod            time.Duration
	retentionMu                sync.Mutex
	retentionExited            chan struct{}
	retentionStopCh            chan struct{}
	retentionStopOnce          *sync.Once
	retentionStopFn            func()
	retentionDetached          atomic.Bool
	nextID                     int
	sessionRecorder            SessionRecorder
	sessionKeyCallback         func(sessionKey, agentID string)
	registerSessionCancel      func(sessionKey string, cancel context.CancelFunc) func()
	sessionEvictCallback       func(sessionKey string)
	sessionStatusCallback      func(sessionKey, status string)
	sessionExists              func(sessionKey string) bool
	persistedLister            func(parentPrefixes []string) []PersistedSubagentTask
	persistedDetail            func(keys []string) map[string]PersistedSubagentDetail
	persistedExecutor          func(sessionKey string) string
	maxConcurrent              int
	defaultMaxRetries          int
	compactionThresholdPercent int
	defaultAgentID             string
	redactor                   *keyring.Redactor
	sessionCompactor           SessionCompactor
	compactionModel            string
	evictExcludedFromMemory    bool
	tokenUsageReporter         func(sessionKey string, inputTokens, outputTokens int)
}

type SpawnOptions struct {
	Dependencies  []string
	MaxRetries    int
	ModelOverride string
}

func NewSubagentManager(provider providers.LLMProvider, defaultModel, workspace string, bus *bus.MessageBus, maxIterations int) *SubagentManager {
	return &SubagentManager{
		tasks:           make(map[string]*SubagentTask),
		cancels:         make(map[string]context.CancelFunc),
		provider:        provider,
		defaultModel:    defaultModel,
		bus:             bus,
		workspace:       workspace,
		tools:           NewToolRegistry(),
		maxIterations:   maxIterations,
		nextID:          1,
		retentionPeriod: 5 * time.Minute,
	}
}

func (sm *SubagentManager) SetDefaults(provider providers.LLMProvider, defaultModel, workspace string, maxIterations int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.provider = provider
	sm.defaultModel = defaultModel
	sm.workspace = workspace
	sm.maxIterations = maxIterations
}

func (sm *SubagentManager) SetLLMOptions(maxTokens int, temperature float64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxTokens = maxTokens
	sm.hasMaxTokens = true
	sm.temperature = temperature
	sm.hasTemperature = true
}

func (sm *SubagentManager) SetThinkingLevel(level string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.thinkingLevel = level
}

func (sm *SubagentManager) SetMaxIterations(maxIterations int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxIterations = maxIterations
}

func (sm *SubagentManager) SetCompactionThresholdPercent(p int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.compactionThresholdPercent = p
}

func (sm *SubagentManager) getCompactionThresholdPercent() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.compactionThresholdPercent
}

func (sm *SubagentManager) SetTimeout(timeout time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.timeout = timeout
}

func (sm *SubagentManager) SetRetentionPeriod(period time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.retentionPeriod = period
}

const DefaultSubagentRetentionSweepInterval = time.Minute

func (sm *SubagentManager) StartRetentionCleanup(interval time.Duration) func() {
	sm.retentionMu.Lock()
	defer sm.retentionMu.Unlock()

	if sm.retentionStopFn != nil {
		return sm.retentionStopFn
	}
	if interval <= 0 {
		interval = DefaultSubagentRetentionSweepInterval
	}

	stop := make(chan struct{})
	exited := make(chan struct{})
	var stopOnce sync.Once

	sm.retentionExited = exited
	sm.retentionStopCh = stop
	sm.retentionStopOnce = &stopOnce
	sm.retentionStopFn = func() {
		_ = sm.StopRetentionCleanupWithin(DefaultRetentionStopGrace)
	}

	go func() {
		defer close(exited)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if sm.retentionDetached.Load() {
					return
				}
				sm.CleanupTerminalTasks()
			}
		}
	}()

	return sm.retentionStopFn
}

const DefaultRetentionStopGrace = 3 * time.Second

func (sm *SubagentManager) StopRetentionCleanup() {
	_ = sm.StopRetentionCleanupWithin(DefaultRetentionStopGrace)
}

func (sm *SubagentManager) StopRetentionCleanupWithin(grace time.Duration) error {
	sm.retentionMu.Lock()
	exited := sm.retentionExited
	stopCh := sm.retentionStopCh
	stopOnce := sm.retentionStopOnce
	sm.retentionMu.Unlock()

	if exited == nil {
		return nil
	}
	if sm.retentionDetached.Load() {
		return nil
	}
	if stopOnce != nil {
		stopOnce.Do(func() { close(stopCh) })
	}

	if grace <= 0 {
		<-exited
		return nil
	}

	select {
	case <-exited:
		return nil
	case <-time.After(grace):
	}

	sm.retentionDetached.Store(true)
	logger.WarnCF("subagent", "Retention sweeper did not exit within the grace; abandoning it (it is detached and will not sweep again)",
		map[string]interface{}{"grace": grace.String()})
	return fmt.Errorf("subagent retention sweeper did not exit within %s", grace)
}

func (sm *SubagentManager) SetMaxConcurrent(max int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxConcurrent = max
}

func (sm *SubagentManager) SetDefaultMaxRetries(maxRetries int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.defaultMaxRetries = maxRetries
}

func (sm *SubagentManager) SetTools(tools *ToolRegistry) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tools = tools
}

func (sm *SubagentManager) SetAgentContextCallback(callback func(agentID string) AgentContextInfo) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.getAgentContext = callback
}

func (sm *SubagentManager) SetDefaultAgentID(agentID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.defaultAgentID = strings.TrimSpace(agentID)
}

func (sm *SubagentManager) getDefaultAgentID() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.defaultAgentID
}

func (sm *SubagentManager) SetModelOverrideResolver(resolver func(model string) (providers.LLMProvider, string, int, error)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.modelOverrideResolver = resolver
}

func (sm *SubagentManager) SetVisionChecker(checker func(model string) bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.visionChecker = checker
}

func (sm *SubagentManager) SetVideoChecker(checker func(model string) bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.videoChecker = checker
}

func (sm *SubagentManager) getVideoChecker() func(model string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.videoChecker
}

func (sm *SubagentManager) SetSessionRecorder(rec SessionRecorder) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionRecorder = rec
}

func (sm *SubagentManager) SetTokenUsageReporter(report func(sessionKey string, inputTokens, outputTokens int)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tokenUsageReporter = report
}

func (sm *SubagentManager) SetSessionCompactor(sc SessionCompactor) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionCompactor = sc
}

func (sm *SubagentManager) SetCompactionConfig(thresholdPercent int, compactionModel string, evictExcluded bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.compactionThresholdPercent = thresholdPercent
	sm.compactionModel = compactionModel
	sm.evictExcludedFromMemory = evictExcluded
}

func (sm *SubagentManager) SetSessionKeyCallback(callback func(sessionKey, agentID string)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionKeyCallback = callback
}

func (sm *SubagentManager) SetRegisterSessionCancelCallback(callback func(sessionKey string, cancel context.CancelFunc) func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.registerSessionCancel = callback
}

func (sm *SubagentManager) SetSessionEvictCallback(callback func(sessionKey string)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionEvictCallback = callback
}

func (sm *SubagentManager) SetSessionStatusCallback(callback func(sessionKey, status string)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionStatusCallback = callback
}

func (sm *SubagentManager) persistTerminalStatus(task *SubagentTask) {
	if sm.sessionStatusCallback == nil {
		return
	}
	sessionKey := task.OriginSessionKey + ":" + task.ID
	if sessionKey != "" && task.Status != "" {
		sm.sessionStatusCallback(sessionKey, task.Status)
	}
}

func (sm *SubagentManager) SetRedactor(r *keyring.Redactor) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.redactor = r
}

func (sm *SubagentManager) getRedactor() *keyring.Redactor {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.redactor
}

func (sm *SubagentManager) RegisterTool(tool Tool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.tools.Register(tool)
}

func (sm *SubagentManager) countRunning() int {
	count := 0
	for _, task := range sm.tasks {
		if task.Status == SubagentStatusRunning {
			count++
		}
	}
	return count
}

func (sm *SubagentManager) checkDependencies(task *SubagentTask) bool {
	for _, depID := range task.Dependencies {
		dep, ok := sm.tasks[depID]
		if !ok {
			continue
		}
		if !dep.IsTerminal() {
			return false
		}
	}
	return true
}

func (sm *SubagentManager) Spawn(ctx context.Context, task, label, agentID, originChannel, originChatID string, callback AsyncCallback) (string, error) {
	return sm.SpawnWithDeps(ctx, task, label, agentID, originChannel, originChatID, callback, nil, 0)
}

func (sm *SubagentManager) SpawnWithDeps(ctx context.Context, task, label, agentID, originChannel, originChatID string, callback AsyncCallback, dependencies []string, maxRetries int) (string, error) {
	return sm.SpawnWithOptions(ctx, task, label, agentID, originChannel, originChatID, callback, SpawnOptions{
		Dependencies: dependencies,
		MaxRetries:   maxRetries,
	})
}

func (sm *SubagentManager) claimTaskID(originSessionKey string) string {
	for {
		sm.mu.Lock()
		taskID := fmt.Sprintf("subagent-%d", sm.nextID)
		sm.nextID++
		exists := sm.sessionExists
		sm.mu.Unlock()

		if exists == nil || !exists(originSessionKey+":"+taskID) {
			return taskID
		}
	}
}

func BuildOriginSessionKey(channel, chatID string) string {
	switch {
	case chatID == "" && channel == "":
		return ""
	case channel == "":
		return chatID
	case chatID == "":
		return channel
	case strings.HasPrefix(chatID, channel+":"):
		return chatID
	default:
		return channel + ":" + chatID
	}
}

func (sm *SubagentManager) safeGoroutine(task *SubagentTask, fn func()) {
	go func() {
		defer task.SignalDone()
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("subagent %s: panic in goroutine: %v\n%s", task.ID, r, stack)
				locked := false
				for attempt := 0; attempt < 3; attempt++ {
					if sm.mu.TryLock() {
						locked = true
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
				if locked {
					task.Status = SubagentStatusFailed
					task.Summary = fmt.Sprintf("Subagent panic: %v", r)
					task.Result = fmt.Sprintf("Subagent panic: %v\n%s", r, stack)
					task.Updated = time.Now().UnixMilli()
					sm.mu.Unlock()
				} else {
					log.Printf("subagent %s: WARNING — could not acquire sm.mu during recover; writing task state unsafely", task.ID)
					task.Status = SubagentStatusFailed
					task.Summary = fmt.Sprintf("Subagent panic: %v", r)
					task.Result = fmt.Sprintf("Subagent panic: %v\n%s", r, stack)
					task.Updated = time.Now().UnixMilli()
				}
				sm.reportTerminalStatus(task)
			}
		}()
		fn()
	}()
}

func (sm *SubagentManager) SpawnWithOptions(ctx context.Context, task, label, agentID, originChannel, originChatID string, callback AsyncCallback, opts SpawnOptions) (string, error) {
	sm.CleanupTerminalTasks()

	originSessionKey := BuildOriginSessionKey(originChannel, originChatID)

	spawnerAgentID, spawnerSessionKey := AgentToolContextFromCtx(ctx)

	if agentID == "" {
		agentID = spawnerAgentID
	}

	taskID := sm.claimTaskID(originSessionKey)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.maxConcurrent > 0 && sm.countRunning() >= sm.maxConcurrent {
		return "", fmt.Errorf("maximum concurrent subagents reached (%d running, limit %d)", sm.countRunning(), sm.maxConcurrent)
	}

	initialStatus := SubagentStatusRunning
	if len(opts.Dependencies) > 0 && !sm.checkDependencies(&SubagentTask{Dependencies: opts.Dependencies}) {
		initialStatus = SubagentStatusPending
	}

	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = sm.defaultMaxRetries
	}

	subagentTask := &SubagentTask{
		ID:                taskID,
		Task:              task,
		Label:             label,
		AgentID:           agentID,
		ModelOverride:     strings.TrimSpace(opts.ModelOverride),
		OriginChannel:     originChannel,
		OriginChatID:      originChatID,
		OriginSessionKey:  originSessionKey,
		SpawnerSessionKey: spawnerSessionKey,
		Status:            initialStatus,
		Created:           time.Now().UnixMilli(),
		Updated:           time.Now().UnixMilli(),
		Dependencies:      opts.Dependencies,
		MaxRetries:        maxRetries,
	}
	subagentTask.mu = &sync.Mutex{}
	subagentTask.InitDoneChannel()
	sm.tasks[taskID] = subagentTask

	taskCtx, cancel := context.WithCancel(context.Background())
	sm.cancels[taskID] = cancel

	if initialStatus == SubagentStatusPending {
		sm.safeGoroutine(subagentTask, func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-taskCtx.Done():
					cancelledWhilePending := func() bool {
						sm.mu.Lock()
						defer sm.mu.Unlock()
						if subagentTask.Status == SubagentStatusPending {
							subagentTask.Status = SubagentStatusCancelled
							subagentTask.Summary = "Task cancelled while waiting for dependencies"
							subagentTask.Result = "Task cancelled while waiting for dependencies"
							subagentTask.Updated = time.Now().UnixMilli()
							return true
						}
						return false
					}()
					if cancelledWhilePending {
						sm.reportTerminalStatus(subagentTask)
					}
					return
				case <-ticker.C:
					allMet := func() bool {
						sm.mu.Lock()
						defer sm.mu.Unlock()
						return sm.checkDependencies(subagentTask)
					}()
					if allMet {
						func() {
							sm.mu.Lock()
							defer sm.mu.Unlock()
							subagentTask.Status = SubagentStatusRunning
							subagentTask.Updated = time.Now().UnixMilli()
						}()
						sm.runTask(taskCtx, subagentTask, callback)
						return
					}
				}
			}
		})
	} else {
		sm.safeGoroutine(subagentTask, func() {
			sm.runTask(taskCtx, subagentTask, callback)
		})
	}

	if label != "" {
		return fmt.Sprintf("Spawned subagent task %s ('%s') for task: %s", taskID, label, task), nil
	}
	return fmt.Sprintf("Spawned subagent task %s for task: %s", taskID, task), nil
}

func (sm *SubagentManager) ContinueTask(ctx context.Context, taskID, guidance string, callback AsyncCallback) (string, error) {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return "", fmt.Errorf("guidance is required")
	}

	sm.CleanupTerminalTasks()
	sm.mu.Lock()
	task, ok := sm.tasks[taskID]
	if !ok {
		sm.mu.Unlock()
		return "", fmt.Errorf("subagent task not found: %s", taskID)
	}
	if task.Status != SubagentStatusNeedsContext {
		status := task.Status
		sm.mu.Unlock()
		return "", fmt.Errorf("subagent task %s is not waiting for context (status: %s)", taskID, status)
	}

	task.Guidance = append(task.Guidance, guidance)
	task.Status = SubagentStatusRunning
	task.lastErr = nil
	task.Updated = time.Now().UnixMilli()
	if task.mu == nil {
		task.mu = &sync.Mutex{}
	}
	task.mu.Lock()
	task.doneCh = make(chan struct{})
	task.resultClaimed = false
	task.resultWaiters = 0
	task.mu.Unlock()
	taskCtx, cancel := context.WithCancel(context.Background())
	sm.cancels[taskID] = cancel
	sm.mu.Unlock()

	sm.safeGoroutine(task, func() {
		sm.runTask(taskCtx, task, callback)
	})

	return fmt.Sprintf("Continuing subagent task %s with new guidance.", taskID), nil
}

func (sm *SubagentManager) GetTask(taskID string) (*SubagentTask, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	task, ok := sm.tasks[taskID]
	if !ok {
		return nil, false
	}
	return task.Snapshot(), true
}

func (sm *SubagentManager) ClaimResult(taskID string) bool {
	sm.mu.RLock()
	task, ok := sm.tasks[taskID]
	sm.mu.RUnlock()
	if !ok || task == nil {
		return false
	}
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	if task.resultClaimed {
		return true
	}
	task.resultClaimed = true
	return false
}

func (sm *SubagentManager) ClaimResultForInjection(taskID string) bool {
	sm.mu.RLock()
	task, ok := sm.tasks[taskID]
	sm.mu.RUnlock()
	if !ok || task == nil {
		return false
	}
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	if task.resultClaimed || task.resultWaiters > 0 {
		return false
	}
	task.resultClaimed = true
	return true
}

func (sm *SubagentManager) AddResultWaiter(taskID string) {
	sm.mu.RLock()
	task, ok := sm.tasks[taskID]
	sm.mu.RUnlock()
	if !ok || task == nil {
		return
	}
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	task.resultWaiters++
}

func (sm *SubagentManager) ReleaseResultWaiter(taskID string) {
	sm.mu.RLock()
	task, ok := sm.tasks[taskID]
	sm.mu.RUnlock()
	if !ok || task == nil {
		return
	}
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	if task.resultWaiters > 0 {
		task.resultWaiters--
	}
}

func (sm *SubagentManager) ReleaseResultWaiterAndClaim(taskID string) *SubagentTask {
	sm.mu.RLock()
	task, ok := sm.tasks[taskID]
	sm.mu.RUnlock()
	if !ok || task == nil {
		return nil
	}
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	if task.resultWaiters > 0 {
		task.resultWaiters--
	}
	if task.resultWaiters > 0 || task.resultClaimed {
		return nil
	}
	if !isSubagentTerminal(task.Status) {
		return nil
	}
	task.resultClaimed = true
	return task.Snapshot()
}

func (sm *SubagentManager) SealResult(taskID string) {
	sm.mu.RLock()
	task, ok := sm.tasks[taskID]
	sm.mu.RUnlock()
	if !ok || task == nil {
		return
	}
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	task.resultClaimed = true
}

func (sm *SubagentManager) reportTerminalStatus(task *SubagentTask) {
	sm.mu.RLock()
	callback := sm.sessionStatusCallback
	status := task.Status
	sessionKey := task.OriginSessionKey + ":" + task.ID
	sm.mu.RUnlock()

	if callback == nil || status == "" || sessionKey == ":" {
		return
	}
	callback(sessionKey, status)
}

func (sm *SubagentManager) CleanupTerminalTasks() int {
	sm.mu.Lock()

	if sm.retentionDetached.Load() {
		sm.mu.Unlock()
		return 0
	}

	if sm.retentionPeriod <= 0 {
		sm.mu.Unlock()
		return 0
	}

	now := time.Now().UnixMilli()
	thresholdMs := int64(sm.retentionPeriod / time.Millisecond)
	removed := 0

	var toEvict []string
	var toCancel []context.CancelFunc
	for taskID, task := range sm.tasks {
		if !isSubagentTerminalStatus(task.Status) {
			continue
		}
		if now-task.Updated > thresholdMs {
			toEvict = append(toEvict, task.OriginSessionKey+":"+taskID)
			delete(sm.tasks, taskID)
			if cancel, ok := sm.cancels[taskID]; ok && cancel != nil {
				delete(sm.cancels, taskID)
				toCancel = append(toCancel, cancel)
			}
			removed++
		}
	}
	evictCallback := sm.sessionEvictCallback
	sm.mu.Unlock()

	for _, cancel := range toCancel {
		cancel()
	}

	if evictCallback != nil {
		for _, sessionKey := range toEvict {
			if sm.retentionDetached.Load() {
				break
			}
			evictCallback(sessionKey)
		}
	}

	return removed
}

func (sm *SubagentManager) ListTasks() []*SubagentTask {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	tasks := make([]*SubagentTask, 0, len(sm.tasks))
	for _, task := range sm.tasks {
		tasks = append(tasks, task.Snapshot())
	}
	return tasks
}

func (sm *SubagentManager) StopTask(taskID string) bool {
	sm.mu.Lock()
	task, taskExists := sm.tasks[taskID]
	cancel, ok := sm.cancels[taskID]
	canStop := taskExists && task != nil && (task.Status == SubagentStatusRunning || task.Status == SubagentStatusNeedsContext || task.Status == SubagentStatusPending)
	if ok {
		delete(sm.cancels, taskID)
	}
	if canStop {
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled"
		task.Result = "Task cancelled"
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
		task.SignalDone()
	}
	sm.mu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
	if canStop {
		sm.reportTerminalStatus(task)
	}
	return ok || canStop
}

func (sm *SubagentManager) StopAll() int {
	sm.mu.Lock()

	stoppedCount := 0
	cancelled := make([]*SubagentTask, 0, len(sm.cancels))
	handled := make(map[string]struct{}, len(sm.cancels))
	pendingCancels := make([]context.CancelFunc, 0, len(sm.cancels))
	for taskID, cancel := range sm.cancels {
		if cancel != nil {
			pendingCancels = append(pendingCancels, cancel)
			stoppedCount++
		}
		handled[taskID] = struct{}{}
		if task, ok := sm.tasks[taskID]; ok {
			task.Status = SubagentStatusCancelled
			task.Summary = "Task cancelled"
			task.Result = "Task cancelled"
			task.ContextRequest = ""
			task.Updated = time.Now().UnixMilli()
			task.SignalDone()
			cancelled = append(cancelled, task)
		}
		delete(sm.cancels, taskID)
	}

	for taskID, task := range sm.tasks {
		if _, alreadyHandled := handled[taskID]; alreadyHandled {
			continue
		}
		if task.Status != SubagentStatusNeedsContext && task.Status != SubagentStatusPending {
			continue
		}
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled"
		task.Result = "Task cancelled"
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
		task.SignalDone()
		cancelled = append(cancelled, task)
		stoppedCount++
	}
	sm.mu.Unlock()

	for _, cancel := range pendingCancels {
		cancel()
	}

	for _, task := range cancelled {
		sm.reportTerminalStatus(task)
	}

	return stoppedCount
}

func (sm *SubagentManager) GetToolRegistry() *ToolRegistry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.tools
}

func (sm *SubagentManager) AgentContextForTest(agentID string) AgentContextInfo {
	sm.mu.RLock()
	cb := sm.getAgentContext
	sm.mu.RUnlock()
	if cb == nil {
		return AgentContextInfo{}
	}
	return cb(agentID)
}

func (sm *SubagentManager) ThinkingLevelForTest() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.thinkingLevel
}

func (sm *SubagentManager) HasTool(name string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.tools.Get(name)
	return ok
}

func (sm *SubagentManager) AddTaskForTest(task *SubagentTask, cancel context.CancelFunc) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if task.mu == nil {
		task.mu = &sync.Mutex{}
	}
	task.InitDoneChannel()
	sm.tasks[task.ID] = task
	if cancel != nil {
		sm.cancels[task.ID] = cancel
	}
}
