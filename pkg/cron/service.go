package cron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adhocore/gronx"
	"github.com/xilistudios/lele/pkg/store"
)

type CronSchedule struct {
	Kind    string `json:"kind"`
	AtMS    *int64 `json:"atMs,omitempty"`
	EveryMS *int64 `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

// SpawnConfig contains configuration for spawning a subagent from a cron job
type SpawnConfig struct {
	Task     string `json:"task"`
	Label    string `json:"label,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	Guidance string `json:"guidance,omitempty"`
	Model    string `json:"model,omitempty"`
}

type CronPayload struct {
	Kind       string       `json:"kind"`
	Message    string       `json:"message"`
	Command    string       `json:"command,omitempty"`
	Deliver    bool         `json:"deliver"`
	Channel    string       `json:"channel,omitempty"`
	To         string       `json:"to,omitempty"`
	Spawn      *SpawnConfig `json:"spawn,omitempty"`
	SessionKey string       `json:"session_key,omitempty"` // Originating session key (for session-scoped jobs)
}

type CronJobState struct {
	NextRunAtMS *int64 `json:"nextRunAtMs,omitempty"`
	LastRunAtMS *int64 `json:"lastRunAtMs,omitempty"`
	LastStatus  string `json:"lastStatus,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type CronJob struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Enabled        bool         `json:"enabled"`
	Scope          string       `json:"scope,omitempty"` // "global" (default) or "session"
	Schedule       CronSchedule `json:"schedule"`
	Payload        CronPayload  `json:"payload"`
	State          CronJobState `json:"state"`
	CreatedAtMS    int64        `json:"createdAtMs"`
	UpdatedAtMS    int64        `json:"updatedAtMs"`
	DeleteAfterRun bool         `json:"deleteAfterRun"`
}

type CronStore struct {
	Version int       `json:"version"`
	Jobs    []CronJob `json:"jobs"`
}

// cronPayloadWithMeta wraps CronPayload to persist DeleteAfterRun inside
// the payload JSON, since cron_jobs has no dedicated column for it.
type cronPayloadWithMeta struct {
	CronPayload
	DeleteAfterRun bool `json:"delete_after_run,omitempty"`
}

type JobHandler func(job *CronJob) (string, error)

type CronService struct {
	storePath string
	store     *CronStore
	repo      *store.CronRepo
	onJob     JobHandler
	mu        sync.RWMutex
	running   bool
	stopChan  chan struct{}
	gronx     *gronx.Gronx
}

func NewCronService(storePath string, onJob JobHandler) *CronService {
	cs := &CronService{
		storePath: storePath,
		onJob:     onJob,
		gronx:     gronx.New(),
	}
	// Initialize and load store on creation
	cs.loadStore()
	return cs
}

func (cs *CronService) Start() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.running {
		return nil
	}

	if err := cs.loadStore(); err != nil {
		return fmt.Errorf("failed to load store: %w", err)
	}

	cs.recomputeNextRuns()
	if err := cs.saveStoreUnsafe(); err != nil {
		return fmt.Errorf("failed to save store: %w", err)
	}

	cs.stopChan = make(chan struct{})
	cs.running = true
	go cs.runLoop(cs.stopChan)

	return nil
}

func (cs *CronService) Stop() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.running {
		return
	}

	cs.running = false
	if cs.stopChan != nil {
		close(cs.stopChan)
		cs.stopChan = nil
	}
}

func (cs *CronService) runLoop(stopChan chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			cs.checkJobs()
		}
	}
}

func (cs *CronService) checkJobs() {
	cs.mu.Lock()

	if !cs.running {
		cs.mu.Unlock()
		return
	}

	now := time.Now().UnixMilli()
	var dueJobIDs []string

	// Collect jobs that are due (we need to copy them to execute outside lock)
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.Enabled && job.State.NextRunAtMS != nil && *job.State.NextRunAtMS <= now {
			dueJobIDs = append(dueJobIDs, job.ID)
		}
	}

	// Reset next run for due jobs before unlocking to avoid duplicate execution.
	dueMap := make(map[string]bool, len(dueJobIDs))
	for _, jobID := range dueJobIDs {
		dueMap[jobID] = true
	}
	for i := range cs.store.Jobs {
		if dueMap[cs.store.Jobs[i].ID] {
			cs.store.Jobs[i].State.NextRunAtMS = nil
		}
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
	}

	cs.mu.Unlock()

	// Execute jobs outside lock.
	for _, jobID := range dueJobIDs {
		cs.executeJobByID(jobID)
	}
}

func (cs *CronService) executeJobByID(jobID string) {
	startTime := time.Now().UnixMilli()

	cs.mu.RLock()
	var callbackJob *CronJob
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			jobCopy := *job
			callbackJob = &jobCopy
			break
		}
	}
	cs.mu.RUnlock()

	if callbackJob == nil {
		return
	}

	var err error
	if cs.onJob != nil {
		_, err = cs.onJob(callbackJob)
	}

	// Now acquire lock to update state
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var job *CronJob
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			job = &cs.store.Jobs[i]
			break
		}
	}
	if job == nil {
		log.Printf("[cron] job %s disappeared before state update", jobID)
		return
	}

	job.State.LastRunAtMS = &startTime
	job.UpdatedAtMS = time.Now().UnixMilli()

	if err != nil {
		job.State.LastStatus = "error"
		job.State.LastError = err.Error()
	} else {
		job.State.LastStatus = "ok"
		job.State.LastError = ""
	}

	// Compute next run time
	if job.Schedule.Kind == "at" {
		if job.DeleteAfterRun {
			cs.removeJobUnsafe(job.ID)
		} else {
			job.Enabled = false
			job.State.NextRunAtMS = nil
		}
	} else {
		nextRun := cs.computeNextRun(&job.Schedule, time.Now().UnixMilli())
		job.State.NextRunAtMS = nextRun
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
	}
}

func (cs *CronService) computeNextRun(schedule *CronSchedule, nowMS int64) *int64 {
	if schedule.Kind == "at" {
		if schedule.AtMS != nil && *schedule.AtMS > nowMS {
			return schedule.AtMS
		}
		return nil
	}

	if schedule.Kind == "every" {
		if schedule.EveryMS == nil || *schedule.EveryMS <= 0 {
			return nil
		}
		next := nowMS + *schedule.EveryMS
		return &next
	}

	if schedule.Kind == "cron" {
		if schedule.Expr == "" {
			return nil
		}

		// Use gronx to calculate next run time
		now := time.UnixMilli(nowMS)
		nextTime, err := gronx.NextTickAfter(schedule.Expr, now, false)
		if err != nil {
			log.Printf("[cron] failed to compute next run for expr '%s': %v", schedule.Expr, err)
			return nil
		}

		nextMS := nextTime.UnixMilli()
		return &nextMS
	}

	return nil
}

func (cs *CronService) recomputeNextRuns() {
	now := time.Now().UnixMilli()
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.Enabled {
			job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, now)
		}
	}
}

func (cs *CronService) getNextWakeMS() *int64 {
	var nextWake *int64
	for _, job := range cs.store.Jobs {
		if job.Enabled && job.State.NextRunAtMS != nil {
			if nextWake == nil || *job.State.NextRunAtMS < *nextWake {
				nextWake = job.State.NextRunAtMS
			}
		}
	}
	return nextWake
}

func (cs *CronService) Load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.loadStore()
}

func (cs *CronService) SetOnJob(handler JobHandler) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.onJob = handler
}

// SetStore switches persistence to the given SQLite repository.
// If the repository is empty and the legacy jobs.json exists, jobs are
// migrated lazily on first load. Pass nil to revert to the JSON backend.
func (cs *CronService) SetStore(repo *store.CronRepo) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.repo = repo
	if err := cs.loadStore(); err != nil {
		log.Printf("[cron] failed to load store after SetStore: %v", err)
	}
}

func (cs *CronService) loadStore() error {
	if cs.repo != nil {
		return cs.loadFromRepo()
	}

	cs.store = &CronStore{
		Version: 1,
		Jobs:    []CronJob{},
	}

	data, err := os.ReadFile(cs.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, cs.store)
}

// loadFromRepo loads jobs from the SQLite repository. If the repo is empty
// and a legacy JSON file exists, it migrates the jobs lazily.
func (cs *CronService) loadFromRepo() error {
	cs.store = &CronStore{
		Version: 1,
		Jobs:    []CronJob{},
	}

	rows, err := cs.repo.ListCronJobs()
	if err != nil {
		return fmt.Errorf("cron: list jobs from store: %w", err)
	}

	if len(rows) == 0 {
		// Attempt lazy migration from legacy JSON.
		data, readErr := os.ReadFile(cs.storePath)
		if os.IsNotExist(readErr) {
			return nil // no legacy file, empty store
		}
		if readErr != nil {
			return readErr
		}
		if unmarshalErr := json.Unmarshal(data, cs.store); unmarshalErr != nil {
			return unmarshalErr
		}
		for _, job := range cs.store.Jobs {
			row, convErr := cronJobToRow(&job)
			if convErr != nil {
				log.Printf("[cron] failed to convert job %s during migration: %v", job.ID, convErr)
				continue
			}
			if upsertErr := cs.repo.UpsertCronJob(row); upsertErr != nil {
				log.Printf("[cron] failed to upsert job %s during migration: %v", job.ID, upsertErr)
				return nil
			}
		}
		return nil
	}

	// Load from SQLite.
	for _, row := range rows {
		job, convErr := rowToCronJob(&row)
		if convErr != nil {
			return convErr
		}
		cs.store.Jobs = append(cs.store.Jobs, *job)
	}
	return nil
}

func (cs *CronService) saveStoreUnsafe() error {
	if cs.repo != nil {
		return cs.saveToRepo()
	}

	dir := filepath.Dir(cs.storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cs.store, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cs.storePath, data, 0600)
}

// saveToRepo persists the in-memory jobs to the SQLite repository.
// Upserts run first, then stale rows are deleted. If a failure occurs
// mid-operation the worst case is a leftover stale row that is cleaned
// up on the next save.
func (cs *CronService) saveToRepo() error {
	existing, err := cs.repo.ListCronJobs()
	if err != nil {
		return fmt.Errorf("cron: list jobs before save: %w", err)
	}

	// Upsert all current jobs.
	for i := range cs.store.Jobs {
		row, convErr := cronJobToRow(&cs.store.Jobs[i])
		if convErr != nil {
			return convErr
		}
		if upsertErr := cs.repo.UpsertCronJob(row); upsertErr != nil {
			return fmt.Errorf("cron: upsert job %s: %w", row.ID, upsertErr)
		}
	}

	// Build set of current IDs for delete sweep.
	currentIDs := make(map[string]bool, len(cs.store.Jobs))
	for i := range cs.store.Jobs {
		currentIDs[cs.store.Jobs[i].ID] = true
	}

	// Delete stale rows.
	for _, row := range existing {
		if !currentIDs[row.ID] {
			if delErr := cs.repo.DeleteCronJob(row.ID); delErr != nil {
				return fmt.Errorf("cron: delete stale job %s: %w", row.ID, delErr)
			}
		}
	}

	return nil
}

func (cs *CronService) AddJob(name string, schedule CronSchedule, message string, deliver bool, channel, to string) (*CronJob, error) {
	return cs.AddJobWithOptions(name, schedule, message, deliver, channel, to, "", "")
}

// AddJobWithOptions adds a job with scope and sessionKey support.
// scope can be "global" (default) or "session".
// sessionKey is the originating session key for session-scoped jobs.
func (cs *CronService) AddJobWithOptions(name string, schedule CronSchedule, message string, deliver bool, channel, to, scope, sessionKey string) (*CronJob, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	now := time.Now().UnixMilli()

	// One-time tasks (at) should be deleted after execution
	deleteAfterRun := (schedule.Kind == "at")

	if scope == "" {
		scope = "global"
	}

	job := CronJob{
		ID:       generateID(),
		Name:     name,
		Enabled:  true,
		Scope:    scope,
		Schedule: schedule,
		Payload: CronPayload{
			Kind:       "agent_turn",
			Message:    message,
			Deliver:    deliver,
			Channel:    channel,
			To:         to,
			SessionKey: sessionKey,
		},
		State: CronJobState{
			NextRunAtMS: cs.computeNextRun(&schedule, now),
		},
		CreatedAtMS:    now,
		UpdatedAtMS:    now,
		DeleteAfterRun: deleteAfterRun,
	}

	cs.store.Jobs = append(cs.store.Jobs, job)
	if err := cs.saveStoreUnsafe(); err != nil {
		return nil, err
	}

	return &job, nil
}

func (cs *CronService) UpdateJob(job *CronJob) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			cs.store.Jobs[i] = *job
			cs.store.Jobs[i].UpdatedAtMS = time.Now().UnixMilli()
			return cs.saveStoreUnsafe()
		}
	}
	return fmt.Errorf("job not found")
}

func (cs *CronService) RemoveJob(jobID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	return cs.removeJobUnsafe(jobID)
}

func (cs *CronService) removeJobUnsafe(jobID string) bool {
	before := len(cs.store.Jobs)
	var jobs []CronJob
	for _, job := range cs.store.Jobs {
		if job.ID != jobID {
			jobs = append(jobs, job)
		}
	}
	cs.store.Jobs = jobs
	removed := len(cs.store.Jobs) < before

	if removed {
		if err := cs.saveStoreUnsafe(); err != nil {
			log.Printf("[cron] failed to save store after remove: %v", err)
		}
	}

	return removed
}

func (cs *CronService) EnableJob(jobID string, enabled bool) *CronJob {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			job.Enabled = enabled
			job.UpdatedAtMS = time.Now().UnixMilli()

			if enabled {
				job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, time.Now().UnixMilli())
			} else {
				job.State.NextRunAtMS = nil
			}

			if err := cs.saveStoreUnsafe(); err != nil {
				log.Printf("[cron] failed to save store after enable: %v", err)
			}
			return job
		}
	}

	return nil
}

func (cs *CronService) ListJobs(includeDisabled bool) []CronJob {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if includeDisabled {
		return cs.store.Jobs
	}

	var enabled []CronJob
	for _, job := range cs.store.Jobs {
		if job.Enabled {
			enabled = append(enabled, job)
		}
	}

	return enabled
}

// GetJob returns a copy of the job with the given ID, or nil if not found.
func (cs *CronService) GetJob(jobID string) *CronJob {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			jobCopy := cs.store.Jobs[i]
			return &jobCopy
		}
	}
	return nil
}

// RunJobNow executes a job immediately without waiting for its schedule and
// without altering its enabled state or next run time. It records the outcome
// in the job's state (last run timestamp, status, and error). Returns an error
// if the job is not found.
func (cs *CronService) RunJobNow(jobID string) error {
	startTime := time.Now().UnixMilli()

	cs.mu.RLock()
	var callbackJob *CronJob
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			jobCopy := cs.store.Jobs[i]
			callbackJob = &jobCopy
			break
		}
	}
	cs.mu.RUnlock()

	if callbackJob == nil {
		return fmt.Errorf("job not found")
	}

	var err error
	if cs.onJob != nil {
		_, err = cs.onJob(callbackJob)
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			job := &cs.store.Jobs[i]
			job.State.LastRunAtMS = &startTime
			job.UpdatedAtMS = time.Now().UnixMilli()
			if err != nil {
				job.State.LastStatus = "error"
				job.State.LastError = err.Error()
			} else {
				job.State.LastStatus = "ok"
				job.State.LastError = ""
			}
			break
		}
	}

	return cs.saveStoreUnsafe()
}

func (cs *CronService) Status() map[string]interface{} {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var enabledCount int
	for _, job := range cs.store.Jobs {
		if job.Enabled {
			enabledCount++
		}
	}

	return map[string]interface{}{
		"enabled":      cs.running,
		"jobs":         len(cs.store.Jobs),
		"nextWakeAtMS": cs.getNextWakeMS(),
	}
}

// cronJobToRow converts a domain CronJob to a store CronJobRow.
func cronJobToRow(job *CronJob) (*store.CronJobRow, error) {
	schedJSON, err := json.Marshal(job.Schedule)
	if err != nil {
		return nil, fmt.Errorf("cron: marshal schedule: %w", err)
	}
	payloadJSON, err := json.Marshal(cronPayloadWithMeta{
		CronPayload:    job.Payload,
		DeleteAfterRun: job.DeleteAfterRun,
	})
	if err != nil {
		return nil, fmt.Errorf("cron: marshal payload: %w", err)
	}
	stateJSON, err := json.Marshal(job.State)
	if err != nil {
		return nil, fmt.Errorf("cron: marshal state: %w", err)
	}
	return &store.CronJobRow{
		ID:          job.ID,
		Name:        job.Name,
		Enabled:     job.Enabled,
		Schedule:    string(schedJSON),
		Payload:     string(payloadJSON),
		State:       string(stateJSON),
		Scope:       job.Scope,
		SessionKey:  job.Payload.SessionKey,
		CreatedAtMS: job.CreatedAtMS,
		UpdatedAtMS: job.UpdatedAtMS,
	}, nil
}

// rowToCronJob converts a store CronJobRow to a domain CronJob.
func rowToCronJob(row *store.CronJobRow) (*CronJob, error) {
	job := &CronJob{
		ID:          row.ID,
		Name:        row.Name,
		Enabled:     row.Enabled,
		Scope:       row.Scope,
		CreatedAtMS: row.CreatedAtMS,
		UpdatedAtMS: row.UpdatedAtMS,
	}
	if err := json.Unmarshal([]byte(row.Schedule), &job.Schedule); err != nil {
		return nil, fmt.Errorf("cron: unmarshal schedule for job %s: %w", row.ID, err)
	}
	var meta cronPayloadWithMeta
	if err := json.Unmarshal([]byte(row.Payload), &meta); err != nil {
		return nil, fmt.Errorf("cron: unmarshal payload for job %s: %w", row.ID, err)
	}
	job.Payload = meta.CronPayload
	job.DeleteAfterRun = meta.DeleteAfterRun
	if err := json.Unmarshal([]byte(row.State), &job.State); err != nil {
		return nil, fmt.Errorf("cron: unmarshal state for job %s: %w", row.ID, err)
	}
	return job, nil
}

func generateID() string {
	// Use crypto/rand for better uniqueness under concurrent access
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
