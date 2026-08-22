package config

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// writeConfigFile writes a minimal config file and returns its path.
func writeConfigFile(t *testing.T, dir, name string) string {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"session":{"ephemeral_threshold":560}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestNewConfigWatcher(t *testing.T) {
	w := NewConfigWatcher("/tmp/config.json")
	if w == nil {
		t.Fatal("NewConfigWatcher returned nil")
	}
	if w.path != "/tmp/config.json" {
		t.Errorf("path = %q, want /tmp/config.json", w.path)
	}
	if w.debounce <= 0 {
		t.Errorf("debounce = %v, want > 0", w.debounce)
	}
	if w.stop == nil {
		t.Error("stop channel should be initialized")
	}
}

func TestConfigWatcher_Start_CancelContext(t *testing.T) {
	path := writeConfigFile(t, "", "config.json")
	w := NewConfigWatcher(path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var calls int32
	err := w.Start(ctx, func(c *Config) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Error("onReload should not fire on immediate cancel")
	}
}

func TestConfigWatcher_Start_Stop(t *testing.T) {
	path := writeConfigFile(t, "", "config.json")
	w := NewConfigWatcher(path)
	ctx := context.Background()

	var calls int32
	done := make(chan error, 1)
	// Start is a blocking loop; run it in a goroutine so we can Stop it.
	go func() {
		done <- w.Start(ctx, func(c *Config) error {
			atomic.AddInt32(&calls, 1)
			return nil
		})
	}()

	w.Stop()
	// Stop is idempotent.
	w.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestConfigWatcher_ReloadOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"session":{"ephemeral_threshold":560}}`), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	w := NewConfigWatcher(path)
	w.debounce = 50 * time.Millisecond
	ctx := context.Background()

	var threshold atomic.Int32
	threshold.Store(0)
	done := make(chan struct{})

	startErr := make(chan error, 1)
	go func() {
		startErr <- w.Start(ctx, func(c *Config) error {
			threshold.Store(int32(c.Session.EphemeralThreshold))
			select {
			case <-done:
			default:
				close(done)
			}
			return nil
		})
	}()
	// Start only returns once the watcher is stopped, so do not block on it.
	// Give fsnotify a moment to establish the directory watch before writing.
	time.Sleep(200 * time.Millisecond)

	// Trigger a write event.
	if err := os.WriteFile(path, []byte(`{"session":{"ephemeral_threshold":999}}`), 0600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	select {
	case <-done:
		if threshold.Load() != 999 {
			t.Errorf("threshold = %d, want 999", threshold.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reload")
	}
	w.Stop()
	<-startErr
}

func TestConfigWatcher_ReloadOnCreate(t *testing.T) {
	dir := t.TempDir()
	// Watch a path whose parent dir already exists but file is created later.
	path := filepath.Join(dir, "newconfig.json")

	w := NewConfigWatcher(path)
	w.debounce = 50 * time.Millisecond
	ctx := context.Background()

	done := make(chan struct{})
	startErr := make(chan error, 1)
	go func() {
		startErr <- w.Start(ctx, func(c *Config) error {
			select {
			case <-done:
			default:
				close(done)
			}
			return nil
		})
	}()
	// Start only returns after Stop; do not block on it.
	// Give fsnotify a moment to establish the directory watch before writing.
	time.Sleep(200 * time.Millisecond)

	if err := os.WriteFile(path, []byte(`{"session":{"ephemeral_threshold":1}}`), 0600); err != nil {
		t.Fatalf("create: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for create reload")
	}
	w.Stop()
	<-startErr
}

func TestConfigWatcher_NoReloadOnNilCallback(t *testing.T) {
	path := writeConfigFile(t, "", "config.json")
	w := NewConfigWatcher(path)
	ctx := context.Background()

	started := make(chan struct{})
	go func() {
		close(started)
		// Start with a nil callback; it must not panic and must keep running.
		_ = w.Start(ctx, nil)
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	w.Stop()
}

func TestConfigWatcher_NoReloadOnUnrelatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	w := NewConfigWatcher(path)
	w.debounce = 50 * time.Millisecond
	ctx := context.Background()

	var calls int32
	startErr := make(chan error, 1)
	go func() {
		startErr <- w.Start(ctx, func(c *Config) error {
			atomic.AddInt32(&calls, 1)
			return nil
		})
	}()
	// Start only returns after Stop.

	// Write a different file in the same dir - should not trigger reload.
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("hello"), 0600); err != nil {
		t.Fatalf("write other: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("reload fired %d times for unrelated file", atomic.LoadInt32(&calls))
	}
	w.Stop()
	<-startErr
}
