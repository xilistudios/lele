package tools

import "strings"

// SessionKeysRelated reports whether two session keys name the same session or
// one of its descendants. Keys are hierarchical on ":", so a subagent running as
// "telegram:chat-1:subagent-2" is related to "telegram:chat-1" but not to
// "telegram:chat-1:subagent-3" (siblings are separate sessions). The hierarchy is
// intentional: it is what lets a parent see its subagents' work.
func SessionKeysRelated(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return sessionKeyHasSuffix(a, b) || sessionKeyHasSuffix(b, a) ||
		sessionKeyHasPrefix(a, b) || sessionKeyHasPrefix(b, a)
}

func sessionKeyHasSuffix(long, short string) bool {
	if len(long) <= len(short) {
		return false
	}
	if !strings.HasSuffix(long, short) {
		return false
	}
	return long[len(long)-len(short)-1] == ':'
}

func sessionKeyHasPrefix(long, short string) bool {
	if len(long) <= len(short) {
		return false
	}
	if !strings.HasPrefix(long, short) {
		return false
	}
	return long[len(short)] == ':'
}

func taskBelongsToSession(spawnerKey, originKey, taskID string, keys []string) bool {
	childKey := ""
	if originKey != "" {
		childKey = originKey + ":" + taskID
	}
	for _, k := range keys {
		if SessionKeysRelated(spawnerKey, k) ||
			SessionKeysRelated(originKey, k) ||
			SessionKeysRelated(childKey, k) {
			return true
		}
	}
	return false
}

func TaskBelongsToSession(task *SubagentTask, keys []string) bool {
	if task == nil {
		return false
	}
	return taskBelongsToSession(task.SpawnerSessionKey, task.OriginSessionKey, task.ID, keys)
}

func taskOwnershipKey(task *SubagentTask) string {
	if task == nil {
		return ""
	}
	if task.SpawnerSessionKey != "" {
		return task.SpawnerSessionKey
	}
	return task.OriginSessionKey
}

func subagentLoopOwner(task *SubagentTask, childSessionKey string) string {
	if owner := taskOwnershipKey(task); owner != "" {
		return owner
	}
	return childSessionKey
}
