package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

type fakeFileWatcher struct {
	events   chan fsnotify.Event
	errors   chan error
	addErr   error
	added    chan struct{}
	addOnce  sync.Once
	mu       sync.Mutex
	closed   bool
	closeCnt int
}

func newFakeFileWatcher() *fakeFileWatcher {
	return &fakeFileWatcher{
		events: make(chan fsnotify.Event, 8),
		errors: make(chan error, 8),
		added:  make(chan struct{}),
	}
}

func (f *fakeFileWatcher) Add(string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.addOnce.Do(func() { close(f.added) })
	return nil
}

func (f *fakeFileWatcher) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.closeCnt++
	return nil
}

func (f *fakeFileWatcher) Events() <-chan fsnotify.Event { return f.events }

func (f *fakeFileWatcher) Errors() <-chan error { return f.errors }

func (f *fakeFileWatcher) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeFileWatcher) closes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCnt
}

func watcherTestConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"agents":{"defaults":{"model":"m"}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func startFakeWatcher(t *testing.T, path string, fake *fakeFileWatcher) (chan error, *ConfigWatcher) {
	t.Helper()

	w := NewConfigWatcher(path)
	w.newWatcher = func() (configFileWatcher, error) { return fake, nil }

	done := make(chan error, 1)
	go func() { done <- w.Start(context.Background(), nil) }()

	select {
	case <-fake.added:
	case err := <-done:
		t.Fatalf("Start returned before watching: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start never registered the directory")
	}

	return done, w
}

func waitStartReturn(t *testing.T, done chan error) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

func TestWatcher_StopClosesFsnotify(t *testing.T) {
	fake := newFakeFileWatcher()
	done, w := startFakeWatcher(t, watcherTestConfig(t), fake)

	w.Stop()
	waitStartReturn(t, done)

	if !fake.isClosed() {
		t.Fatal("Stop left the fsnotify watcher open: the inotify descriptor leaks for the life of the process")
	}
	if got := fake.closes(); got != 1 {
		t.Fatalf("watcher closed %d times, want exactly 1", got)
	}
}

func TestWatcher_ContextCancelClosesFsnotify(t *testing.T) {
	fake := newFakeFileWatcher()
	path := watcherTestConfig(t)

	w := NewConfigWatcher(path)
	w.newWatcher = func() (configFileWatcher, error) { return fake, nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Start(ctx, nil) }()

	select {
	case <-fake.added:
	case <-time.After(5 * time.Second):
		t.Fatal("Start never registered the directory")
	}

	cancel()
	waitStartReturn(t, done)

	if !fake.isClosed() {
		t.Fatal("a cancelled context left the fsnotify watcher open")
	}
}

func TestWatcher_AddErrorClosesFsnotify(t *testing.T) {
	fake := newFakeFileWatcher()
	fake.addErr = errors.New("add failed")

	w := NewConfigWatcher(watcherTestConfig(t))
	w.newWatcher = func() (configFileWatcher, error) { return fake, nil }

	err := w.Start(context.Background(), nil)
	if err == nil {
		t.Fatal("Start succeeded with a failing Add")
	}
	if !errors.Is(err, fake.addErr) {
		t.Fatalf("Start error = %v, want it to wrap the Add error", err)
	}
	if !fake.isClosed() {
		t.Fatal("a failed Add leaked the fsnotify watcher")
	}
}

func TestWatcher_ClosedChannelsEndTheLoop(t *testing.T) {
	fake := newFakeFileWatcher()
	done, w := startFakeWatcher(t, watcherTestConfig(t), fake)
	defer w.Stop()

	close(fake.events)
	waitStartReturn(t, done)

	if !fake.isClosed() {
		t.Fatal("an exhausted event channel left the fsnotify watcher open")
	}
}

func TestWatcher_RealFsnotifyClosesOnStop(t *testing.T) {
	path := watcherTestConfig(t)
	w := NewConfigWatcher(path)

	done := make(chan error, 1)
	go func() { done <- w.Start(context.Background(), nil) }()

	time.Sleep(50 * time.Millisecond)
	w.Stop()
	waitStartReturn(t, done)
}
