package tools

// truncateString truncates a string to maxLen characters, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// retryConfigPtr returns a pointer to a default RetryConfig.
// Used to enable automatic retry for subagent LLM calls.
func retryConfigPtr() *RetryConfig {
	c := DefaultRetryConfig()
	return &c
}
