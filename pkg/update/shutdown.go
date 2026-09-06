// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package update

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

// ErrShutdownBudgetExceeded is recorded for hooks that never got a chance to
// run because the overall shutdown budget was already spent.
var ErrShutdownBudgetExceeded = errors.New("shutdown budget exceeded")

// hook is a single registered shutdown step.
type hook struct {
	name    string
	timeout time.Duration
	fn      func(context.Context) error
}

// ShutdownCoordinator runs registered shutdown hooks in LIFO order, each with
// its own timeout, exactly once.
//
// It exists because the gateway used to have two independent, ad-hoc stop
// paths (the SIGTERM handler and the self-restart path) that disagreed about
// ordering and could both leave the process half-torn-down. Registering every
// teardown step once here gives the process a single idempotent, ordered,
// timeout-bounded graceful-stop entry point.
//
// LIFO (last registered, first to run) mirrors defer: you register the things
// that must be released last (the instance lock) first, and the things that
// must stop first (config watcher, schedulers) last.
type ShutdownCoordinator struct {
	mu    sync.Mutex
	hooks []hook
	total time.Duration

	// started is set by the first RunAll call; Register is a no-op afterwards
	// (a hook registered after the teardown began would never run).
	started bool
	// done is closed when RunAll finishes and results is populated. Concurrent
	// RunAll callers block on it so they observe the recorded results instead
	// of an empty map.
	done    chan struct{}
	results map[string]error
}

// NewShutdownCoordinator returns a coordinator with the given overall budget.
// A non-positive total falls back to DefaultShutdownBudget.
func NewShutdownCoordinator(total time.Duration) *ShutdownCoordinator {
	if total <= 0 {
		total = DefaultShutdownBudget
	}
	return &ShutdownCoordinator{total: total, done: make(chan struct{})}
}

// DefaultShutdownBudget is the overall time the gateway allows for a graceful
// stop before it stops starting new hooks.
const DefaultShutdownBudget = 15 * time.Second

// Register adds a hook. Hooks run in reverse registration order (LIFO), so the
// first thing registered is the last thing to run. Registering after RunAll has
// started is a no-op with a warning (late hooks would never run).
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

// RunAll executes all hooks once. It is idempotent: a second call returns the
// recorded results without re-running (and a concurrent second call waits for
// the first to finish). It NEVER inherits a cancelled context — the caller's
// ctx is used only for the overall deadline, and if it is already cancelled
// RunAll still runs the hooks with their own timeouts derived from
// context.Background(). Returns name -> error (nil error means success).
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

	// The caller's context only bounds the overall budget; hooks never receive
	// it, so a cancelled ctx can never abort the teardown halfway through.
	deadline := time.Now().Add(total)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	results := make(map[string]error, len(hooks))
	logger.InfoCF("shutdown", "Running shutdown hooks", map[string]interface{}{
		"hooks":  len(hooks),
		"budget": total.String(),
	})

	// LIFO: the last registered hook runs first.
	for i := len(hooks) - 1; i >= 0; i-- {
		h := hooks[i]
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

	c.mu.Lock()
	c.results = results
	close(c.done)
	c.mu.Unlock()

	logger.InfoCF("shutdown", "Shutdown hooks finished", map[string]interface{}{
		"hooks": len(results),
	})
	return results
}

// runHook executes one hook under its own timeout and reports its outcome.
// The hook runs in a goroutine so a hook that ignores its context cannot block
// the remaining hooks; a panicking hook is recovered and recorded as an error.
func runHook(h hook, timeout time.Duration) error {
	logger.InfoCF("shutdown", "Shutdown hook started", map[string]interface{}{
		"hook":    h.name,
		"timeout": timeout.String(),
	})

	// Deliberately derived from context.Background(): shutdown hooks must run
	// even when the caller's context is already cancelled.
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
		// The hook may have returned in the same instant the timer fired.
		select {
		case err := <-errCh:
			logHookDone(h.name, started, err)
			return err
		default:
		}
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

// HasRun reports whether RunAll has already executed.
func (c *ShutdownCoordinator) HasRun() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

// Total returns the configured overall budget.
func (c *ShutdownCoordinator) Total() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}
