package heartbeat

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/tools"
)

func TestExecuteHeartbeat_Async(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{}) // Enable for testing

	asyncCalled := false
	asyncResult := &tools.ToolResult{
		ForLLM:  "Background task started",
		ForUser: "Task started in background",
		Silent:  false,
		IsError: false,
		Async:   true,
	}

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		asyncCalled = true
		if prompt == "" {
			t.Error("Expected non-empty prompt")
		}
		return asyncResult
	})

	// Create HEARTBEAT.md
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

	// Execute heartbeat directly (internal method for testing)
	hs.executeHeartbeat()

	if !asyncCalled {
		t.Error("Expected handler to be called")
	}
}

func TestExecuteHeartbeat_Error(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{}) // Enable for testing

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{
			ForLLM:  "Heartbeat failed: connection error",
			ForUser: "",
			Silent:  false,
			IsError: true,
			Async:   false,
		}
	})

	// Create HEARTBEAT.md
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

	hs.executeHeartbeat()

	// Check log file for error message
	logFile := filepath.Join(tmpDir, "heartbeat.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(data)
	if logContent == "" {
		t.Error("Expected log file to contain error message")
	}
}

func TestExecuteHeartbeat_Silent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{}) // Enable for testing

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{
			ForLLM:  "Heartbeat completed successfully",
			ForUser: "",
			Silent:  true,
			IsError: false,
			Async:   false,
		}
	})

	// Create HEARTBEAT.md
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

	hs.executeHeartbeat()

	// Check log file for completion message
	logFile := filepath.Join(tmpDir, "heartbeat.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(data)
	if logContent == "" {
		t.Error("Expected log file to contain completion message")
	}
}

func TestHeartbeatService_StartStop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 1, true)

	err = hs.Start()
	if err != nil {
		t.Fatalf("Failed to start heartbeat service: %v", err)
	}

	hs.Stop()

	time.Sleep(100 * time.Millisecond)
}

func TestHeartbeatService_Disabled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 1, false)

	if hs.enabled != false {
		t.Error("Expected service to be disabled")
	}

	err = hs.Start()
	defer hs.Stop() // the loop recreates HEARTBEAT.md on every tick; leaving it alive races t.TempDir() cleanup
	_ = err         // Disabled service returns nil
}

func TestExecuteHeartbeat_NilResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{}) // Enable for testing

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return nil
	})

	// Create HEARTBEAT.md
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

	// Should not panic with nil result
	hs.executeHeartbeat()
}

// TestLogPath verifies heartbeat log is written to workspace directory
func TestLogPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)

	// Write a log entry
	hs.log("INFO", "Test log entry")

	// Verify log file exists at workspace root
	expectedLogPath := filepath.Join(tmpDir, "heartbeat.log")
	if _, err := os.Stat(expectedLogPath); os.IsNotExist(err) {
		t.Errorf("Expected log file at %s, but it doesn't exist", expectedLogPath)
	}
}

// TestHeartbeatFilePath verifies HEARTBEAT.md is at workspace root
func TestHeartbeatFilePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)

	// Trigger default template creation
	hs.buildPrompt()

	// Verify HEARTBEAT.md exists at workspace root
	expectedPath := filepath.Join(tmpDir, "HEARTBEAT.md")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Expected HEARTBEAT.md at %s, but it doesn't exist", expectedPath)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// UpdateConfig lifecycle tests (CORE-01)
// ──────────────────────────────────────────────────────────────────────────────

// makeCountingHandler returns a HeartbeatHandler that increments counter on
// each invocation and optionally signals beat (non-nil channel).
func makeCountingHandler(t *testing.T, counter *int, mu *sync.Mutex, beat chan<- struct{}, once *sync.Once, first chan struct{}) HeartbeatHandler {
	t.Helper()
	return func(prompt, channel, chatID string) *tools.ToolResult {
		mu.Lock()
		*counter++
		mu.Unlock()
		if beat != nil {
			select {
			case beat <- struct{}{}:
			default:
			}
		}
		if once != nil && first != nil {
			once.Do(func() { close(first) })
		}
		return &tools.ToolResult{ForLLM: "ok", Silent: true}
	}
}

func TestUpdateConfig_DisableThenReEnable(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("check"), 0644)

	hs := NewHeartbeatService(tmpDir, 1, true)
	// Use a very short interval for testing (bypasses minIntervalMinutes).
	hs.interval = 100 * time.Millisecond

	var mu sync.Mutex
	beats := 0
	beatCh := make(chan struct{}, 1)
	var once sync.Once
	firstBeat := make(chan struct{})

	hs.SetHandler(makeCountingHandler(t, &beats, &mu, beatCh, &once, firstBeat))
	hs.Start()
	defer hs.Stop() // the loop recreates HEARTBEAT.md on every tick; leaving it alive races t.TempDir() cleanup

	// Wait for first beat to confirm the loop is alive.
	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

	// Disable → loop stops.
	hs.UpdateConfig(1, false)
	if hs.IsRunning() {
		t.Fatal("expected IsRunning()==false after disable")
	}

	// Re-enable → loop restarts.
	hs.UpdateConfig(1, true)
	if !hs.IsRunning() {
		t.Fatal("expected IsRunning()==true after re-enable")
	}

	// At least one beat must arrive after re-enable.
	select {
	case <-beatCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no beat after re-enable; heartbeat is dead")
	}
}

func TestUpdateConfig_StartDisabledThenEnable(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("check"), 0644)

	hs := NewHeartbeatService(tmpDir, 1, false)
	hs.interval = 100 * time.Millisecond

	var mu sync.Mutex
	beats := 0
	beatCh := make(chan struct{}, 1)
	var once sync.Once
	firstBeat := make(chan struct{})

	hs.SetHandler(makeCountingHandler(t, &beats, &mu, beatCh, &once, firstBeat))

	// Start on a disabled service → no loop.
	hs.Start()
	defer hs.Stop() // the loop recreates HEARTBEAT.md on every tick; leaving it alive races t.TempDir() cleanup
	if hs.IsRunning() {
		t.Fatal("disabled Start() should not launch the loop")
	}

	// Enable at runtime → loop must start.
	hs.UpdateConfig(1, true)
	if !hs.IsRunning() {
		t.Fatal("expected IsRunning()==true after enabling")
	}

	select {
	case <-beatCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no beat after enabling; loop was never started")
	}
}

func TestUpdateConfig_DisableStopsLoop(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("check"), 0644)

	hs := NewHeartbeatService(tmpDir, 1, true)
	hs.interval = 100 * time.Millisecond

	var mu sync.Mutex
	beats := 0
	beatCh := make(chan struct{}, 100)
	var once sync.Once
	firstBeat := make(chan struct{})

	hs.SetHandler(makeCountingHandler(t, &beats, &mu, beatCh, &once, firstBeat))
	hs.Start()
	defer hs.Stop() // the loop recreates HEARTBEAT.md on every tick; leaving it alive races t.TempDir() cleanup

	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

	// Disable and wait for the loop goroutine to fully exit. A handler
	// can still be executing when stopChan is closed (the loop checks
	// stopChan before each tick, not during handler execution), so we
	// must not record the baseline until the loop has stopped.
	hs.UpdateConfig(1, false)
	deadline := time.After(5 * time.Second)
	for hs.IsRunning() {
		select {
		case <-deadline:
			t.Fatal("loop did not stop after disable")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Now the loop is dead. Drain any beats that arrived during the
	// shutdown window, then record the final count.
	drainChan(beatCh)
	time.Sleep(100 * time.Millisecond) // let any last handler finish
	drainChan(beatCh)

	mu.Lock()
	finalBeats := beats
	mu.Unlock()

	// Wait and verify no new beats arrive.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	afterDisable := beats
	mu.Unlock()

	if afterDisable != finalBeats {
		t.Errorf("beats after disable: final=%d after=%d; expected no new beats", finalBeats, afterDisable)
	}
}

func TestUpdateConfig_DoubleEnableDoesNotDuplicateLoop(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("check"), 0644)

	hs := NewHeartbeatService(tmpDir, 1, true)
	hs.interval = 100 * time.Millisecond

	var mu sync.Mutex
	beats := 0
	beatCh := make(chan struct{}, 1)
	var once sync.Once
	firstBeat := make(chan struct{})

	hs.SetHandler(makeCountingHandler(t, &beats, &mu, beatCh, &once, firstBeat))
	hs.Start()
	defer hs.Stop() // the loop recreates HEARTBEAT.md on every tick; leaving it alive races t.TempDir() cleanup

	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

	// Second UpdateConfig(_, true) while already running must NOT spawn
	// a second loop (the stopChan != nil guard must prevent it).
	hs.UpdateConfig(1, true)

	mu.Lock()
	before := beats
	mu.Unlock()

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	after := beats
	mu.Unlock()

	delta := after - before
	// With a single loop at 100ms interval over 500ms we expect ~5 beats.
	// A duplicate loop would produce ~10. Threshold at 8 keeps the margin
	// wide enough for scheduling jitter while catching a duplicate loop.
	if delta > 8 {
		t.Errorf("excessive beats in 500ms window: %d (expected ≤8); possible duplicate loop", delta)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Interval hot-reload test (CORE-02/03)
// ──────────────────────────────────────────────────────────────────────────────

func TestUpdateConfig_IntervalChangeAppliedLive(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("check"), 0644)

	hs := NewHeartbeatService(tmpDir, 1, true)
	hs.interval = 50 * time.Millisecond

	var mu sync.Mutex
	beats := 0
	beatCh := make(chan struct{}, 100)
	var once sync.Once
	firstBeat := make(chan struct{})

	hs.SetHandler(makeCountingHandler(t, &beats, &mu, beatCh, &once, firstBeat))
	hs.Start()
	defer hs.Stop() // the loop recreates HEARTBEAT.md on every tick; leaving it alive races t.TempDir() cleanup

	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

	// Drain any buffered beats, then count at 50ms interval.
	drainChan(beatCh)
	time.Sleep(500 * time.Millisecond)
	n1 := drainCount(beatCh)

	// At 50ms over 500ms, expect approximately 10 beats (range 6–14
	// for scheduling jitter).
	if n1 < 6 {
		t.Fatalf("baseline count too low: %d (expected ≥6 at 50ms interval)", n1)
	}

	// Switch to a much longer interval (400ms) via UpdateConfig. The
	// internal field is set directly (bypasses minIntervalMinutes).
	hs.mu.Lock()
	hs.interval = 400 * time.Millisecond
	hs.mu.Unlock()
	hs.UpdateConfig(1, true) // triggers read of new interval in runLoop

	// Wait a beat tick at the new cadence to confirm it took effect.
	time.Sleep(600 * time.Millisecond)

	// Drain and count beats in the next 600ms window.
	drainChan(beatCh)
	time.Sleep(600 * time.Millisecond)
	n2 := drainCount(beatCh)

	// At 400ms over 600ms, expect at most 2 beats.
	if n2 > 3 {
		t.Errorf("too many beats after slow-down: %d (expected ≤3 at 400ms interval over 600ms)", n2)
	}

	// The ratio must reflect the interval change (n1/n2 ≥ 2).
	if n2 > 0 && n1/n2 < 2 {
		t.Errorf("interval change not reflected: fast=%d slow=%d (ratio should be ≥2)", n1, n2)
	}
}

// drainChan drains all currently-buffered values from ch (non-blocking).
func drainChan(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// drainCount drains ch for the given duration and returns the count.
func drainCount(ch chan struct{}) int {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	count := 0
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case <-ch:
			count++
		case <-timeout:
			return count
		case <-ticker.C:
			// Keep draining; the timeout above is the real deadline.
		}
	}
}

// TestStop_WaitsForLoopSignalledByDisable is the regression test for the
// unreapable-goroutine race behind the flaky t.TempDir() cleanup (issue #338).
//
// The symptom: a heartbeat loop still alive at the end of a test recreates
// HEARTBEAT.md inside the temp dir while testing is removing it, so the
// cleanup walk fails with "directory not empty" and the CI job goes red for a
// reason unrelated to the change under review.
//
// The mechanism this pins down: UpdateConfig(_, false) signals the loop to
// exit but must not block on an in-flight beat, so it cannot wait for the
// goroutine itself. It used to close stopChan and drop hs.done on the floor,
// which left the signalled goroutine with no channel anyone could reap: a
// later Stop() saw stopChan == nil, returned immediately, and the goroutine
// outlived it still holding a handle on the workspace.
//
// Disabling the reaping here makes the test fail: Stop returns while the
// handler is still blocked, i.e. before the goroutine has exited.
func TestStop_WaitsForLoopSignalledByDisable(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("check"), 0644)

	hs := NewHeartbeatService(tmpDir, 1, true)
	hs.interval = 50 * time.Millisecond

	inBeat := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		once.Do(func() { close(inBeat) })
		<-release // simulate a beat that outlives the disable signal
		return &tools.ToolResult{ForLLM: "ok", Silent: true}
	})

	if err := hs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Get the loop into a beat and block it there.
	select {
	case <-inBeat:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("timed out waiting for the loop to enter a beat")
	}

	// Disable while the beat is in flight. This must not block: it only
	// signals. The goroutine is still running the handler.
	hs.UpdateConfig(1, false)
	if hs.IsRunning() {
		close(release)
		t.Fatal("expected IsRunning()==false after disable")
	}

	stopped := make(chan struct{})
	go func() {
		hs.Stop()
		close(stopped)
	}()

	// Stop must not report success while the goroutine it is supposed to
	// reap is still inside the handler.
	select {
	case <-stopped:
		close(release)
		t.Fatal("Stop returned before the disabled loop goroutine exited; " +
			"the goroutine can still write into the workspace after Stop")
	case <-time.After(200 * time.Millisecond):
	}

	// Let the beat finish; now Stop must complete.
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned after the in-flight beat finished")
	}

	// Idempotence: a second Stop has nothing left to reap and must not block.
	secondStop := make(chan struct{})
	go func() {
		hs.Stop()
		close(secondStop)
	}()
	select {
	case <-secondStop:
	case <-time.After(2 * time.Second):
		t.Fatal("second Stop() blocked; Stop must be idempotent")
	}
}

// TestStop_NoWorkspaceWritesAfterStop asserts the property the CI failure
// actually violated: once Stop returns, nothing may touch the workspace again.
// HEARTBEAT.md is removed after Stop and the service is left alone for well
// over one interval; the file must stay gone.
func TestStop_NoWorkspaceWritesAfterStop(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "HEARTBEAT.md")
	os.WriteFile(path, []byte("check"), 0644)

	hs := NewHeartbeatService(tmpDir, 1, true)
	hs.interval = 50 * time.Millisecond
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{ForLLM: "ok", Silent: true}
	})

	if err := hs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Stop is expected to have reaped the loop, so this returns only once the
	// goroutine is gone.
	hs.Stop()

	// Same removal t.TempDir() performs at cleanup, then a wait far longer
	// than the interval the loop used to beat on.
	os.RemoveAll(path)
	time.Sleep(400 * time.Millisecond)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("HEARTBEAT.md was recreated after Stop returned (err=%v); "+
			"a loop goroutine outlived Stop", err)
	}
}
