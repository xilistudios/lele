// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"strings"
	"testing"
)

func TestResponseTokenCounts(t *testing.T) {
	tests := []struct {
		name       string
		messages   []Message
		response   *LLMResponse
		wantInput  int
		wantOutput int
	}{
		{
			name:     "nil response bills nothing",
			messages: []Message{{Role: "user", Content: "hi"}},
			response: nil,
		},
		{
			name:     "usage block is authoritative",
			messages: []Message{{Role: "user", Content: "hi"}},
			response: &LLMResponse{
				Content: "hello there",
				Usage: &UsageInfo{
					PromptTokens:     123,
					CompletionTokens: 45,
					TotalTokens:      168,
				},
			},
			wantInput:  123,
			wantOutput: 45,
		},
		{
			name:     "usage beats heuristic even when absurdly small",
			messages: []Message{{Role: "user", Content: strings.Repeat("x", 1000)}},
			response: &LLMResponse{
				Content: "ok",
				Usage:   &UsageInfo{PromptTokens: 7, CompletionTokens: 1},
			},
			wantInput:  7,
			wantOutput: 1,
		},
		{
			name:     "no usage falls back to 2.5 chars per token heuristic",
			messages: []Message{{Role: "user", Content: strings.Repeat("a", 100)}},
			response: &LLMResponse{Content: strings.Repeat("b", 50)},
			// 100*2/5 = 40 input, 50*2/5 = 20 output
			wantInput:  40,
			wantOutput: 20,
		},
		{
			name:     "no usage and empty everything bills zero",
			messages: nil,
			response: &LLMResponse{Content: ""},
		},
		{
			name: "no usage sums input across all messages",
			messages: []Message{
				{Role: "system", Content: strings.Repeat("a", 50)},
				{Role: "user", Content: strings.Repeat("b", 50)},
			},
			response:   &LLMResponse{Content: strings.Repeat("c", 25)},
			wantInput:  40, // (50+50)*2/5
			wantOutput: 10, // 25*2/5
		},
		{
			name:     "multi-byte content counted in runes, not bytes",
			messages: []Message{{Role: "user", Content: "éééé"}}, // 4 runes, 8 bytes
			response: &LLMResponse{Content: "ok"},
			// Rune-based: 4*2/5 = 1. Byte-based would give 8*2/5 = 3,
			// so this pins utf8.RuneCountInString over len().
			wantInput:  1,
			wantOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, out := ResponseTokenCounts(tt.messages, tt.response)
			if in != tt.wantInput || out != tt.wantOutput {
				t.Errorf("ResponseTokenCounts() = (%d, %d), want (%d, %d)",
					in, out, tt.wantInput, tt.wantOutput)
			}
		})
	}
}
