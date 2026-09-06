// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"fmt"
	"strings"
)

// HealToolCallPairs repairs broken tool-call/result pairing in a message
// history, returning the healed history and whether anything changed.
//
// A conversation is only valid when every assistant tool call is answered by
// exactly one tool result carrying its ID, and every tool result answers a
// call that is actually present. A turn that dies mid-flight - user interrupt,
// provider error, crash, or a record that never landed - breaks that pairing.
// Strict providers then reject the whole request with a 400 ("messages with
// role 'tool' must be a response to a preceeding message with 'tool_calls'"),
// and because the broken message is replayed on every subsequent turn, the
// session stays bricked permanently.
//
// The rule applied here is the one the OpenAI Chat Completions API enforces: a
// tool result is only valid as a reply to the tool_calls block that precedes
// it. Healing therefore walks the history block by block:
//
//   - a call with no result gets a synthetic one, so the block is complete;
//   - a result that answers nothing - its call was compacted away, it arrives
//     before the call, it duplicates an answered call, or it carries no ID -
//     is dropped, since no provider accepts it and it cannot be repaired
//     without inventing the call that produced it;
//   - real results are emitted directly after their assistant block, keeping
//     the assistant/tool sequence contiguous even when an unrelated message
//     was recorded in between.
//
// Nothing is edited or invented except the synthetic results, so the model
// still sees the conversation it actually had. A history that is already valid
// is returned as the same slice, which keeps callers from rewriting (and
// bumping the epoch of) every session they load.
func HealToolCallPairs(messages []Message) ([]Message, bool) {
	if len(messages) == 0 {
		return messages, false
	}

	healed := make([]Message, 0, len(messages)+4)

	var (
		open       bool      // an assistant tool-call block awaits results
		unanswered []string  // ids of that block still without a result
		pending    []Message // its real results, held until the block closes
	)

	// closeBlock emits the block's results: the real ones first (they carry
	// actual tool output), then synthetic ones for whatever stayed unanswered.
	closeBlock := func() {
		healed = append(healed, pending...)
		pending = nil
		for _, id := range unanswered {
			healed = append(healed, missingResultMessage(id))
		}
		unanswered = nil
		open = false
	}

	for _, m := range messages {
		switch m.Role {
		case "assistant":
			if open {
				closeBlock()
			}
			// Drop calls the provider would reject outright: one with no ID can
			// never be answered by any result, and one with no name is the
			// 400 "missing a function name" some gateways raise. Both exist in
			// histories written before CanonicalToolCalls; keeping them would
			// only fail the request they belong to.
			if kept, dropped := answerableCalls(m.ToolCalls); dropped > 0 {
				m.ToolCalls = kept
				// Stripping the last call can leave a message with neither tool
				// calls nor text - the exact blank turn the empty-response guard
				// refuses to persist, which the model then imitates.
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
			// Only the open block may be answered. This single test rejects
			// orphans, out-of-order and duplicate results at once.
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
				closeBlock()
			}
			healed = append(healed, m)
		}
	}
	if open {
		closeBlock()
	}

	// A valid history comes back identical, element for element: same length,
	// same order, same content. Anything else means something was dropped,
	// moved or synthesised.
	if len(healed) == len(messages) && sameMessages(healed, messages) {
		return messages, false
	}
	return healed, true
}

// sameMessages reports whether two histories are equal for the purpose of
// detecting healing work: role, tool-call id and text content. Tool calls are
// compared by id only because HealToolCallPairs never rewrites them.
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

// answerableCalls returns the calls that can appear in a valid request - each
// with an ID to match results against and a name to execute - and how many were
// dropped. Returns the input unchanged when nothing has to go, so a healthy
// history keeps its exact values.
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

// answerCall marks one occurrence of callID as answered, reporting whether it
// was still waiting. Duplicate results for the same call are rejected, while a
// block that announced the same id twice can still have both answered.
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

// missingResultMessage is the synthetic reply a dangling call needs. The text
// tells the model the call was never answered, so it may retry rather than
// assume the work was done.
func missingResultMessage(callID string) Message {
	return Message{
		Role:       "tool",
		ToolCallID: callID,
		Content: fmt.Sprintf(
			"No recorded result for this tool call (interrupted or lost). "+
				"Re-run the tool if the outcome is still needed. [call_id=%s]", callID),
	}
}
