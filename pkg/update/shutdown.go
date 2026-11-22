package update

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

var ErrShutdownBudgetExceeded = errors.New("shutdown budget exceeded")

type hook struct {
	name     string
	timeout  time.Duration
	fn       func(context.Context) error
	critical bool
}

type ShutdownCoordinator struct {
	mu    sync.Mutex
	hooks []hook
	total time.Duration

	started bool
	done    chan struct{}
	results map[string]error
}

func NewShutdownCoordinator(total time.Duration) *ShutdownCoordinator {
	if total <= 0 {
		total = DefaultShutdownBudget
	}
	return &ShutdownCoordinator{total: total, done: make(chan struct{})}
}

const DefaultShutdownBudget = 15 * time.Second

func (c *ShutdownCoordinator) Register(name string, timeout time.Duration, fn func(context.Context) error) {
	if fn == nil {
		logger.WarnCF("shutdown", "Ignoring shutdown hook with nil function", map[string]interface{}{"hook": name})
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		logger.WarnCF("shutdown", "Ignoring shutdown hook registered after shutdown started", map[string]interface{}{
			"hook": name,
		})
		return
	}
	c.hooks = append(c.hooks, hook{name: name, timeout: timeout, fn: fn})
}

func (c *ShutdownCoordinator) RegisterCritical(name string, timeout time.Duration, fn func(context.Context) error) {
	if fn == nil {
		logger.WarnCF("shutdown", "Ignoring critical shutdown hook with nil function", map[string]interface{}{"hook": name})
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		logger.WarnCF("shutdown", "Ignoring critical shutdown hook registered after shutdown started", map[string]interface{}{
			"hook": name,
		})
		return
	}
	c.hooks = append(c.hooks, hook{name: name, timeout: timeout, fn: fn, critical: true})
}

func (c *ShutdownCoordinator) RunAll(ctx context.Context) map[string]error {
	c.mu.Lock()
	if c.started {
		done, results := c.done, c.results
		c.mu.Unlock()
		if results != nil {
			return results
		}
		<-done
		c.mu.Lock()
		results = c.results
		c.mu.Unlock()
		return results
	}
	c.started = true
	hooks := make([]hook, len(c.hooks))
	copy(hooks, c.hooks)
	total := c.total
	c.mu.Unlock()

	deadline := time.Now().Add(total)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	results := make(map[string]error, len(hooks))
	logger.InfoCF("shutdown", "Running shutdown hooks", map[string]interface{}{
		"hooks":  len(hooks),
		"budget": total.String(),
	})

	for i := len(hooks) - 1; i >= 0; i-- {
		h := hooks[i]
		if h.critical {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			results[h.name] = ErrShutdownBudgetExceeded
			logger.WarnCF("shutdown", "Skipped shutdown hook: budget exceeded", map[string]interface{}{
				"hook": h.name,
			})
			continue
		}

		timeout := h.timeout
		if timeout <= 0 || timeout > remaining {
			timeout = remaining
		}
		results[h.name] = runHook(h, timeout)
	}

	for i := len(hooks) - 1; i >= 0; i-- {
		h := hooks[i]
		if !h.critical {
			continue
		}
		timeout := h.timeout
		if timeout <= 0 {
			timeout = total
		}
		results[h.name] = runHook(h, timeout)
	}

	c.mu.Lock()
	c.results = results
	close(c.done)
	c.mu.Unlock()

	logger.InfoCF("shutdown", "Shutdown hooks finished", map[string]interface{}{
		"hooks": len(results),
	})
	return results
}

func runHook(h hook, timeout time.Duration) error {
	logger.InfoCF("shutdown", "Shutdown hook started", map[string]interface{}{
		"hook":    h.name,
		"timeout": timeout.String(),
	})

	hookCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				errCh <- fmt.Errorf("panic in shutdown hook %q: %v", h.name, p)
			}
		}()
		errCh <- h.fn(hookCtx)
	}()

	started := time.Now()
	select {
	case err := <-errCh:
		logHookDone(h.name, started, err)
		return err
	case <-hookCtx.Done():
		err := fmt.Errorf("shutdown hook %q timed out after %s: %w", h.name, timeout, context.DeadlineExceeded)
		logger.WarnCF("shutdown", "Shutdown hook timed out", map[string]interface{}{
			"hook":    h.name,
			"timeout": timeout.String(),
		})
		return err
	}
}

func logHookDone(name string, started time.Time, err error) {
	fields := map[string]interface{}{
		"hook":     name,
		"duration": time.Since(started).String(),
	}
	if err != nil {
		fields["error"] = err.Error()
		logger.WarnCF("shutdown", "Shutdown hook finished with error", fields)
		return
	}
	logger.InfoCF("shutdown", "Shutdown hook finished", fields)
}

func (c *ShutdownCoordinator) HasRun() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

func (c *ShutdownCoordinator) Total() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}
