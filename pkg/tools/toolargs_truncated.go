package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/providers"
)

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
