package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/providers"
)

// TruncatedArgumentsError builds the result returned in place of executing a
// tool call whose arguments were cut off mid-write.
//
// When a model spends its output budget while streaming a tool call, the
// arguments arrive as a JSON object that never closed. The repair pass keeps
// the members that completed, so the payload becomes valid JSON again and the
// call looks executable - but the value being written when the stream stopped
// is gone. For a write_file that is the whole file body, and the tool then
// fails with "content is required" (or, worse, an empty one) while the model
// is told nothing about what actually happened.
//
// Running such a call cannot succeed, and its error message sends the model off
// to fix the wrong thing (it believes it sent the content). The loop reports the
// real problem instead and tells the model how to work around the budget, so the
// turn recovers on the next iteration.
//
// The surviving keys are listed because they are what the model must keep: it
// already paid for them, and re-sending the same call unchanged just truncates
// again at the same spot.
func TruncatedArgumentsError(tc providers.ToolCall) *ToolResult {
	kept := "none"
	if len(tc.Arguments) > 0 {
		keys := make([]string, 0, len(tc.Arguments))
		for key := range tc.Arguments {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		kept = strings.Join(keys, ", ")
	}

	name := tc.Name
	if name == "" && tc.Function != nil {
		name = tc.Function.Name
	}
	if name == "" {
		name = "unknown"
	}

	message := fmt.Sprintf(
		"Your %s call was not executed: its arguments were cut off before they "+
			"arrived complete (the response hit its output token limit). Only these "+
			"arguments made it through: %s. The value you were writing when the cut "+
			"happened is missing entirely.\n"+
			"Do not re-send the same call unchanged - it will be cut off again in the "+
			"same place. Send the missing content in smaller pieces instead: create or "+
			"append a first chunk, then append the rest with one call per chunk.",
		name, kept,
	)
	return ErrorResult(message)
}
