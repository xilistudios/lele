package config

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// configFileWatcher is the slice of fsnotify.ConfigWatcher the reload loop
// needs. Keeping it behind an interface lets a test observe that the watcher is
// closed on every exit path, which fsnotify itself does not expose.
type configFileWatcher interface {
	Add(path string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type fsnotifyFileWatcher struct {
	watcher *fsnotify.Watcher
}

func (f fsnotifyFileWatcher) Add(path string) error { return f.watcher.Add(path) }

func (f fsnotifyFileWatcher) Close() error { return f.watcher.Close() }

func (f fsnotifyFileWatcher) Events() <-chan fsnotify.Event { return f.watcher.Events }

func (f fsnotifyFileWatcher) Errors() <-chan error { return f.watcher.Errors }

type ConfigWatcher struct {
	path       string
	debounce   time.Duration
	stop       chan struct{}
	stopOnce   sync.Once
	newWatcher func() (configFileWatcher, error)
}

func NewConfigWatcher(path string) *ConfigWatcher {
	return &ConfigWatcher{
		path:     path,
		debounce: 400 * time.Millisecond,
		stop:     make(chan struct{}),
		newWatcher: func() (configFileWatcher, error) {
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				return nil, err
			}
			return fsnotifyFileWatcher{watcher: watcher}, nil
		},
	}
}

func (w *ConfigWatcher) Start(ctx context.Context, onReload func(*Config) error) error {
	watcher, err := w.newWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(filepath.Dir(w.path)); err != nil {
		return err
	}

	var timer *time.Timer
	var timerC <-chan time.Time
	trigger := func() {
		if onReload == nil {
			return
		}
		cfg, err := LoadConfig(w.path)
		if err != nil {
			return
		}
		_ = onReload(cfg)
	}

	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerC = nil
	}
	defer stopTimer()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.stop:
			return nil
		case _, ok := <-watcher.Errors():
			if !ok {
				return nil
			}
		case event, ok := <-watcher.Events():
			if !ok {
				return nil
			}
			if filepath.Clean(event.Name) != filepath.Clean(w.path) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			stopTimer()
			if timer == nil {
				timer = time.NewTimer(w.debounce)
			} else {
				timer.Reset(w.debounce)
			}
			timerC = timer.C
		case <-timerC:
			stopTimer()
			trigger()
		}
	}
}

func (w *ConfigWatcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stop)
	})
}
