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
	hs.stopChan = make(chan struct{})

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

	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

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
	hs.stopChan = make(chan struct{})

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{
			ForLLM:  "Heartbeat failed: connection error",
			ForUser: "",
			Silent:  false,
			IsError: true,
			Async:   false,
		}
	})

	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

	hs.executeHeartbeat()

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
	hs.stopChan = make(chan struct{})

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return &tools.ToolResult{
			ForLLM:  "Heartbeat completed successfully",
			ForUser: "",
			Silent:  true,
			IsError: false,
			Async:   false,
		}
	})

	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

	hs.executeHeartbeat()

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
	defer hs.Stop()
	_ = err
}

func TestExecuteHeartbeat_NilResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{})

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return nil
	})

	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("Test task"), 0644)

	hs.executeHeartbeat()
}

func TestLogPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)

	hs.log("INFO", "Test log entry")

	expectedLogPath := filepath.Join(tmpDir, "heartbeat.log")
	if _, err := os.Stat(expectedLogPath); os.IsNotExist(err) {
		t.Errorf("Expected log file at %s, but it doesn't exist", expectedLogPath)
	}
}

func TestHeartbeatFilePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "heartbeat-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hs := NewHeartbeatService(tmpDir, 30, true)

	hs.buildPrompt()

	expectedPath := filepath.Join(tmpDir, "HEARTBEAT.md")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Expected HEARTBEAT.md at %s, but it doesn't exist", expectedPath)
	}
}

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
	hs.interval = 100 * time.Millisecond

	var mu sync.Mutex
	beats := 0
	beatCh := make(chan struct{}, 1)
	var once sync.Once
	firstBeat := make(chan struct{})

	hs.SetHandler(makeCountingHandler(t, &beats, &mu, beatCh, &once, firstBeat))
	hs.Start()
	defer hs.Stop()

	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

	hs.UpdateConfig(1, false)
	if hs.IsRunning() {
		t.Fatal("expected IsRunning()==false after disable")
	}

	hs.UpdateConfig(1, true)
	if !hs.IsRunning() {
		t.Fatal("expected IsRunning()==true after re-enable")
	}

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

	hs.Start()
	defer hs.Stop()
	if hs.IsRunning() {
		t.Fatal("disabled Start() should not launch the loop")
	}

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
	defer hs.Stop()

	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

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

	drainChan(beatCh)
	time.Sleep(100 * time.Millisecond)
	drainChan(beatCh)

	mu.Lock()
	finalBeats := beats
	mu.Unlock()

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
	defer hs.Stop()

	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

	hs.UpdateConfig(1, true)

	mu.Lock()
	before := beats
	mu.Unlock()

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	after := beats
	mu.Unlock()

	delta := after - before
	if delta > 8 {
		t.Errorf("excessive beats in 500ms window: %d (expected ≤8); possible duplicate loop", delta)
	}
}

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
	defer hs.Stop()

	select {
	case <-firstBeat:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first beat")
	}

	drainChan(beatCh)
	time.Sleep(500 * time.Millisecond)
	n1 := drainCount(beatCh)

	if n1 < 6 {
		t.Fatalf("baseline count too low: %d (expected ≥6 at 50ms interval)", n1)
	}

	hs.mu.Lock()
	hs.interval = 400 * time.Millisecond
	hs.mu.Unlock()
	hs.UpdateConfig(1, true)

	time.Sleep(600 * time.Millisecond)

	drainChan(beatCh)
	time.Sleep(600 * time.Millisecond)
	n2 := drainCount(beatCh)

	if n2 > 3 {
		t.Errorf("too many beats after slow-down: %d (expected ≤3 at 400ms interval over 600ms)", n2)
	}

	if n2 > 0 && n1/n2 < 2 {
		t.Errorf("interval change not reflected: fast=%d slow=%d (ratio should be ≥2)", n1, n2)
	}
}

func drainChan(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

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
		}
	}
}

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
		<-release
		return &tools.ToolResult{ForLLM: "ok", Silent: true}
	})

	if err := hs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-inBeat:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("timed out waiting for the loop to enter a beat")
	}

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

	select {
	case <-stopped:
		close(release)
		t.Fatal("Stop returned before the disabled loop goroutine exited; " +
			"the goroutine can still write into the workspace after Stop")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned after the in-flight beat finished")
	}

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
	hs.Stop()

	os.RemoveAll(path)
	time.Sleep(400 * time.Millisecond)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("HEARTBEAT.md was recreated after Stop returned (err=%v); "+
			"a loop goroutine outlived Stop", err)
	}
}
