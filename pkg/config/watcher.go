package config

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type ConfigWatcher struct {
	path     string
	debounce time.Duration
	stop     chan struct{}
	stopOnce sync.Once
}

func NewConfigWatcher(path string) *ConfigWatcher {
	return &ConfigWatcher{
		path:     path,
		debounce: 400 * time.Millisecond,
		stop:     make(chan struct{}),
	}
}

func (w *ConfigWatcher) Start(ctx context.Context, onReload func(*Config) error) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

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

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.stop:
			return nil
		case <-watcher.Errors:
		case event := <-watcher.Events:
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
