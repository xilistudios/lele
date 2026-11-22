package session

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

const maxStoredMessages = 10000

type Session struct {
	Key                string              `json:"key"`
	Name               string              `json:"name,omitempty"`
	Mode               string              `json:"mode,omitempty"`
	Messages           []providers.Message `json:"messages"`
	Summary            string              `json:"summary,omitempty"`
	VerboseMode        bool                `json:"verbose_mode,omitempty"`
	VerboseLevel       string              `json:"verbose_level,omitempty"`
	Model              string              `json:"model,omitempty"`
	ThinkingLevel      string              `json:"thinking_level,omitempty"`
	Folder             string              `json:"folder,omitempty"`
	AgentID            string              `json:"agent_id,omitempty"`
	SubagentStatus     string              `json:"subagent_status,omitempty"`
	Created            time.Time           `json:"created"`
	Updated            time.Time           `json:"updated"`
	lastStreamFlush    time.Time
	hadStreamedContent bool
	lastPersistedSeq   int
	metaDirty          bool
	msgsAppended       int
	modifiedFrom       int
	excludedRange      [2]int
	lastMsgDeleted     bool
	deleteFromSeq      int
	firstInMemorySeq   int
	evictedTotal       int
	excludeBoundary    int
	saveEpoch          uint64
	InputTokens        int `json:"input_tokens,omitempty"`
	OutputTokens       int `json:"output_tokens,omitempty"`
	CompactionCount    int `json:"compaction_count,omitempty"`

	viewSnapshot atomic.Pointer[messageSnapshot]
}

type sessionMetadata struct {
	Key            string    `json:"key"`
	Name           string    `json:"name"`
	Mode           string    `json:"mode,omitempty"`
	Folder         string    `json:"folder,omitempty"`
	AgentID        string    `json:"agent_id,omitempty"`
	SubagentStatus string    `json:"subagent_status,omitempty"`
	Created        time.Time `json:"created"`
	Updated        time.Time `json:"updated"`
}

func generateSessionName(content string) string {
	maxLen := 50
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")
	content = strings.ReplaceAll(content, "\t", " ")

	for _, r := range []string{".", ",", "!", "?", ";", ":", "'", "\"", "`"} {
		content = strings.ReplaceAll(content, r, "")
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return "New Chat"
	}

	result := strings.Join(words, " ")
	if len(result) <= maxLen {
		return result
	}

	result = result[:maxLen]
	lastSpace := strings.LastIndex(result, " ")
	if lastSpace > 0 && lastSpace > maxLen-20 {
		result = result[:lastSpace]
	}

	return strings.TrimSpace(result)
}

func (s *Session) clearDirtyFlags() {
	s.metaDirty = false
	s.msgsAppended = 0
	s.modifiedFrom = 0
	s.excludedRange = [2]int{}
	s.lastMsgDeleted = false
	s.deleteFromSeq = 0
}

func (s *Session) markModified(idx int) {
	if idx < 0 {
		return
	}
	if s.modifiedFrom == 0 || idx+1 < s.modifiedFrom {
		s.modifiedFrom = idx + 1
	}
	s.bumpEpoch()
}

func (s *Session) bumpEpoch() {
	s.saveEpoch++
}

func (s *Session) seqForIndex(i int) int {
	return s.firstInMemorySeq + i
}
