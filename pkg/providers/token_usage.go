// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import "unicode/utf8"

// ResponseTokenCounts converts an LLM response into the (input, output) token
// pair that should be charged to the caller's session.
//
// When the provider reported usage, its numbers are authoritative and are
// returned as-is. When it did not (some providers omit usage entirely), the
// counts are estimated with the same 2.5 chars/token heuristic the session
// manager uses for context-size accounting, so a missing usage block degrades
// to an approximation instead of silently billing zero.
//
// This is the single source of truth for "how many tokens did this response
// cost": the main agent loop (pkg/agent.trackTokenUsage) and subagent tool
// loops (pkg/tools.RunToolLoop) both bill through it, which keeps the parent's
// cumulative total and a subagent's contribution expressed in the same units.
//
// A nil response yields (0, 0); callers may invoke it unconditionally after a
// successful call.
func ResponseTokenCounts(messages []Message, response *LLMResponse) (inputTokens, outputTokens int) {
	if response == nil {
		return 0, 0
	}

	if response.Usage != nil {
		return response.Usage.PromptTokens, response.Usage.CompletionTokens
	}

	// Provider returned no usage data — estimate using the 2.5 chars/token
	// heuristic (integer math: chars * 2 / 5).
	var inputChars int
	for _, msg := range messages {
		inputChars += utf8.RuneCountInString(msg.Content)
	}
	inputEst := inputChars * 2 / 5
	outputEst := utf8.RuneCountInString(response.Content) * 2 / 5
	return inputEst, outputEst
}
