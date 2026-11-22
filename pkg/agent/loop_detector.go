package agent

import (
	"encoding/json"
	"fmt"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

const (
	MaxLoopRepetitions = 3
)

type loopDetector struct {
	lastMessage *messageSignature
}

func newLoopDetector() *loopDetector {
	return &loopDetector{}
}

func (ld *loopDetector) buildSignature(toolCalls []providers.ToolCall) messageSignature {
	sig := messageSignature{
		toolCalls: make([]toolCallSignature, 0, len(toolCalls)),
	}
	for _, tc := range toolCalls {
		argsJSON, _ := json.Marshal(tc.Arguments)
		sig.toolCalls = append(sig.toolCalls, toolCallSignature{
			name:      tc.Name,
			arguments: string(argsJSON),
		})
	}
	return sig
}

func (ld *loopDetector) Check(toolCalls []providers.ToolCall, agentID string, iteration int) *providers.Message {
	currentSig := ld.buildSignature(toolCalls)

	if ld.lastMessage != nil && messageSignaturesEqual(ld.lastMessage.toolCalls, currentSig.toolCalls) {
		ld.lastMessage.count++
		if ld.lastMessage.count >= MaxLoopRepetitions {
			toolNames := make([]string, 0, len(toolCalls))
			for _, tc := range toolCalls {
				toolNames = append(toolNames, tc.Name)
			}
			logger.WarnCF("agent", "Detected repeated message loop, injecting guidance",
				map[string]interface{}{
					"agent_id":    agentID,
					"repetitions": ld.lastMessage.count,
					"iteration":   iteration,
					"tools":       toolNames,
				})

			ld.lastMessage.count = 0

			return &providers.Message{
				Role: "user",
				Content: fmt.Sprintf("⚠️ GUIDANCE: You have sent the same tool calls multiple times consecutively. " +
					"This appears to be a loop. The previous tool calls have already been executed and their results are in the conversation history. " +
					"Please STOP repeating the same tool calls and either:\n" +
					"1. Analyze the results you've already received, or\n" +
					"2. Try a different approach, or\n" +
					"3. Provide a final response based on the information gathered."),
			}
		}
	} else {
		ld.lastMessage = &currentSig
		ld.lastMessage.count = 1
	}

	return nil
}

func (ld *loopDetector) Reset() {
	ld.lastMessage = nil
}
