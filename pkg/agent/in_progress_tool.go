package agent

import (
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/session"
)

func (al *AgentLoop) publishToolLifecycle(msg bus.OutboundMessage) {
	if msg.Channel == channels.ChannelName {
		al.trackInProgressTool(msg)
	}
	al.bus.PublishOutbound(msg)
}

func (al *AgentLoop) trackInProgressTool(msg bus.OutboundMessage) {
	key := al.ResolveSessionKey(msg.ChatID)
	if key == "" {
		return
	}

	switch msg.Event {
	case "tool.executing":
		tool := msg.Metadata["tool"]
		if tool == "" {
			return
		}
		al.inProgressTools.Store(key, &session.InProgressTool{
			Tool:               tool,
			Action:             msg.Metadata["action"],
			Arguments:          msg.Metadata["arguments"],
			ToolCallID:         msg.Metadata["tool_call_id"],
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
			StartedAt:          time.Now(),
		})
	case "tool.result":
		raw, ok := al.inProgressTools.Load(key)
		if !ok {
			return
		}
		inFlight, _ := raw.(*session.InProgressTool)
		if inFlight == nil {
			return
		}
		if id := msg.Metadata["tool_call_id"]; id != "" && inFlight.ToolCallID != "" && id != inFlight.ToolCallID {
			return
		}
		if name := msg.Metadata["tool"]; name != "" && name != inFlight.Tool {
			return
		}
		al.inProgressTools.Delete(key)
	}
}

func (al *AgentLoop) GetInProgressTool(sessionKey string) *session.InProgressTool {
	key := al.ResolveSessionKey(sessionKey)
	if key == "" {
		return nil
	}
	raw, ok := al.inProgressTools.Load(key)
	if !ok {
		return nil
	}
	inFlight, _ := raw.(*session.InProgressTool)
	return inFlight.Clone()
}

func (al *AgentLoop) clearInProgressTool(sessionKey string) {
	if sessionKey == "" {
		return
	}
	al.inProgressTools.Delete(al.ResolveSessionKey(sessionKey))
}
