package tools

import (
	"context"
	"fmt"
	"time"
)

// SleepTool allows the agent to wait for a specified number of seconds.
// It respects context cancellation so that /stop or session cancellation
// can interrupt the sleep early.
type SleepTool struct{}

func NewSleepTool() *SleepTool {
	return &SleepTool{}
}

func (t *SleepTool) Name() string {
	return "sleep"
}

func (t *SleepTool) Description() string {
	return "Sleep for a specified number of seconds. Useful for waiting between operations, polling intervals, rate limiting, or giving background tasks time to complete. Maximum 300 seconds (5 minutes). Can be interrupted by context cancellation."
}

func (t *SleepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"seconds": map[string]interface{}{
				"type":        "number",
				"description": "Number of seconds to sleep (0.1 to 300)",
				"minimum":     0.1,
				"maximum":     300,
			},
		},
		"required": []string{"seconds"},
	}
}

func (t *SleepTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	seconds, ok := args["seconds"].(float64)
	if !ok {
		// Some providers may send integers as float64; if the key is missing entirely, error.
		return ErrorResult("seconds is required")
	}

	if seconds < 0.1 {
		return ErrorResult("seconds must be at least 0.1")
	}
	if seconds > 300 {
		return ErrorResult("seconds must not exceed 300 (5 minutes)")
	}

	duration := time.Duration(seconds * float64(time.Second))

	select {
	case <-time.After(duration):
		return SilentResult(fmt.Sprintf("Slept for %.1f seconds", seconds))
	case <-ctx.Done():
		return ErrorResult(fmt.Sprintf("Sleep interrupted (was waiting %.1f seconds)", seconds))
	}
}
