package providers

import (
	"fmt"
	"strings"
)

func HealToolCallPairs(messages []Message) ([]Message, bool) {
	if len(messages) == 0 {
		return messages, false
	}

	healed := make([]Message, 0, len(messages)+4)

	var (
		open        bool
		unanswered  []string
		pending     []Message
		interleaved []Message
	)

	closeBlock := func() {
		healed = append(healed, pending...)
		pending = nil
		for _, id := range unanswered {
			healed = append(healed, missingResultMessage(id))
		}
		unanswered = nil
		healed = append(healed, interleaved...)
		interleaved = nil
		open = false
	}

	for _, m := range messages {
		switch m.Role {
		case "assistant":
			if open {
				closeBlock()
			}
			if kept, dropped := answerableCalls(m.ToolCalls); dropped > 0 {
				m.ToolCalls = kept
				if len(m.ToolCalls) == 0 && strings.TrimSpace(m.Content) == "" &&
					m.ReasoningContent == "" {
					continue
				}
			}
			healed = append(healed, m)
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					unanswered = append(unanswered, tc.ID)
				}
			}
			open = len(unanswered) > 0

		case "tool":
			if open {
				if answerCall(&unanswered, m.ToolCallID) {
					pending = append(pending, m)
				}
				if len(unanswered) == 0 {
					closeBlock()
				}
			}

		default:
			if open {
				interleaved = append(interleaved, m)
				continue
			}
			healed = append(healed, m)
		}
	}
	if open {
		closeBlock()
	}
	healed = append(healed, interleaved...)

	if len(healed) == len(messages) && sameMessages(healed, messages) {
		return messages, false
	}
	return healed, true
}

func sameMessages(a, b []Message) bool {
	for i := range a {
		if a[i].Role != b[i].Role || a[i].ToolCallID != b[i].ToolCallID ||
			a[i].Content != b[i].Content || len(a[i].ToolCalls) != len(b[i].ToolCalls) {
			return false
		}
		for j := range a[i].ToolCalls {
			if a[i].ToolCalls[j].ID != b[i].ToolCalls[j].ID {
				return false
			}
		}
	}
	return true
}

func answerableCalls(toolCalls []ToolCall) ([]ToolCall, int) {
	droppable := 0
	for i := range toolCalls {
		if toolCalls[i].ID == "" || strings.TrimSpace(toolCalls[i].FunctionName()) == "" {
			droppable++
		}
	}
	if droppable == 0 {
		return toolCalls, 0
	}
	kept := make([]ToolCall, 0, len(toolCalls)-droppable)
	for i := range toolCalls {
		if toolCalls[i].ID == "" || strings.TrimSpace(toolCalls[i].FunctionName()) == "" {
			continue
		}
		kept = append(kept, toolCalls[i])
	}
	return kept, droppable
}

func answerCall(unanswered *[]string, callID string) bool {
	if callID == "" {
		return false
	}
	for i, id := range *unanswered {
		if id == callID {
			*unanswered = append((*unanswered)[:i], (*unanswered)[i+1:]...)
			return true
		}
	}
	return false
}

func missingResultMessage(callID string) Message {
	return Message{
		Role:       "tool",
		ToolCallID: callID,
		Content: fmt.Sprintf(
			"No recorded result for this tool call (interrupted or lost). "+
				"Re-run the tool if the outcome is still needed. [call_id=%s]", callID),
	}
}
