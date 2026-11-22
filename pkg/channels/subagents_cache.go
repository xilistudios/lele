package channels

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

const (
	subagentsCacheTTL = time.Second

	subagentsCacheMaxKeys = 256
)

type subagentsCache struct {
	now func() time.Time
	ttl time.Duration
	max int

	mu      sync.Mutex
	entries map[string]*subagentsCacheEntry
}

type subagentsCacheEntry struct {
	ready chan struct{}

	tasks     []SubagentTaskInfo
	err       error
	expiresAt time.Time

	done bool
}

func newSubagentsCache(ttl time.Duration, max int, now func() time.Time) *subagentsCache {
	if now == nil {
		now = time.Now
	}
	if max <= 0 {
		max = subagentsCacheMaxKeys
	}
	return &subagentsCache{
		now:     now,
		ttl:     ttl,
		max:     max,
		entries: make(map[string]*subagentsCacheEntry),
	}
}

func (n *NativeChannel) subagentsReadCache() *subagentsCache {
	n.subagentsCacheMu.Lock()
	defer n.subagentsCacheMu.Unlock()
	if n.subagentsCached == nil {
		n.subagentsCached = newSubagentsCache(subagentsCacheTTL, subagentsCacheMaxKeys, time.Now)
	}
	return n.subagentsCached
}

func (n *NativeChannel) invalidateSubagentsCache(sessionKey string) {
	if sessionKey == "" {
		return
	}
	n.subagentsReadCache().invalidate(sessionKey)
}

func (c *subagentsCache) get(ctx context.Context, key string, fetch func() ([]SubagentTaskInfo, error)) ([]SubagentTaskInfo, error) {
	c.mu.Lock()
	e, ok := c.entries[key]

	if ok && e.done {
		if c.now().Before(e.expiresAt) {
			tasks := cloneSubagentTasks(e.tasks)
			c.mu.Unlock()
			return tasks, nil
		}
		delete(c.entries, key)
		ok = false
	}

	if ok {
		c.mu.Unlock()
		select {
		case <-e.ready:
			if e.err != nil {
				return nil, e.err
			}
			return cloneSubagentTasks(e.tasks), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	e = &subagentsCacheEntry{ready: make(chan struct{})}
	c.entries[key] = e
	c.evictLocked()
	c.mu.Unlock()

	return c.fill(key, e, fetch)
}

func (c *subagentsCache) invalidate(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *subagentsCache) fill(key string, e *subagentsCacheEntry, fetch func() ([]SubagentTaskInfo, error)) (tasks []SubagentTaskInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("native", "Subagents provider panicked", map[string]interface{}{
				"session_key": key,
				"panic":       fmt.Sprint(r),
				"stack":       string(debug.Stack()),
			})
			panicErr := fmt.Errorf("subagents provider panicked: %v", r)
			c.publish(key, e, nil, panicErr)
			tasks, err = nil, panicErr
		}
	}()

	fetched, err := fetch()
	if err != nil {
		c.publish(key, e, nil, err)
		return nil, err
	}

	sorted := cloneSubagentTasks(fetched)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Created > sorted[j].Created })

	c.publish(key, e, sorted, nil)

	return cloneSubagentTasks(sorted), nil
}

func (c *subagentsCache) publish(key string, e *subagentsCacheEntry, tasks []SubagentTaskInfo, err error) {
	c.mu.Lock()
	if e.done {
		c.mu.Unlock()
		return
	}
	e.done = true
	e.err = err
	if err != nil {
		if cur, ok := c.entries[key]; ok && cur == e {
			delete(c.entries, key)
		}
	} else {
		e.tasks = tasks
		e.expiresAt = c.now().Add(c.ttl)
	}
	c.mu.Unlock()
	close(e.ready)
}

func (c *subagentsCache) evictLocked() {
	if len(c.entries) <= c.max {
		return
	}

	now := c.now()
	for k, e := range c.entries {
		if e.done && !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}

	for len(c.entries) > c.max {
		var victim string
		var victimExpiry time.Time
		found := false
		for k, e := range c.entries {
			if !e.done {
				continue
			}
			if !found || e.expiresAt.Before(victimExpiry) {
				victim, victimExpiry, found = k, e.expiresAt, true
			}
		}
		if !found {
			return
		}
		delete(c.entries, victim)
	}
}

func cloneSubagentTasks(tasks []SubagentTaskInfo) []SubagentTaskInfo {
	if tasks == nil {
		return nil
	}
	return append([]SubagentTaskInfo(nil), tasks...)
}
