package agent

import "sync"

const (
	midTurnNoticeSender    = "subagent"
	midTurnNoticePrefix    = "[System: " + midTurnNoticeSender + "] "
	midTurnNoticeSeparator = "\n\n"
)

type midTurnNotice struct {
	content string
	taskID  string
}

func (n midTurnNotice) messageContent() string {
	return midTurnNoticePrefix + n.content
}

type midTurnNoticeQueue struct {
	mu     sync.Mutex
	active bool
	items  []midTurnNotice
}

func (al *AgentLoop) midTurnNoticeQueueFor(sessionKey string) *midTurnNoticeQueue {
	if al == nil || sessionKey == "" {
		return nil
	}
	key := al.ResolveSessionKey(sessionKey)
	if key == "" {
		return nil
	}
	if q, ok := al.midTurnNotices.Load(key); ok {
		if queue, ok := q.(*midTurnNoticeQueue); ok {
			return queue
		}
	}
	queue := &midTurnNoticeQueue{}
	actual, _ := al.midTurnNotices.LoadOrStore(key, queue)
	if existing, ok := actual.(*midTurnNoticeQueue); ok {
		return existing
	}
	return queue
}

func (al *AgentLoop) activateMidTurnNotices(sessionKey string) *midTurnNoticeQueue {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil {
		return nil
	}
	queue.mu.Lock()
	queue.active = true
	queue.mu.Unlock()
	return queue
}

func (al *AgentLoop) enqueueMidTurnNotice(sessionKey, content, taskID string) bool {
	queue := al.midTurnNoticeQueueFor(sessionKey)
	if queue == nil || content == "" {
		return false
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if !queue.active {
		return false
	}
	queue.items = append(queue.items, midTurnNotice{content: content, taskID: taskID})
	return true
}

func (q *midTurnNoticeQueue) drain() []midTurnNotice {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

func (q *midTurnNoticeQueue) drainOrDeactivate() []midTurnNotice {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > 0 {
		items := q.items
		q.items = nil
		return items
	}
	q.active = false
	return nil
}

func (q *midTurnNoticeQueue) deactivate() []midTurnNotice {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active = false
	items := q.items
	q.items = nil
	return items
}
