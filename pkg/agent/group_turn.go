// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

// runGroupTurn executes a single group turn: builds the messages (persona+role
// system prompt, shared transcript as context, and the strategy instruction),
// acquires the session semaphore with a key derived as group:<groupID>:<speaker>
// so that different speakers in the same group do not block each other, and
// makes a direct LLM call (no tools in this first version).
// Returns the produced content and tokens used.
func (lr *llmRunnerImpl) runGroupTurn(ctx context.Context, req group.TurnRequest) (string, int, error) {
	// a. Resolve the speaking agent.
	agent, ok := lr.al.registry.GetAgent(req.Speaker)
	if !ok {
		return "", 0, fmt.Errorf("group turn: agent %q not found in registry", req.Speaker)
	}

	// b. Session key for the semaphore — unique per group+speaker.
	sessionKey := "group:" + req.GroupID + ":" + req.Speaker

	// c. Acquire semaphore (same pattern as runAgentLoop).
	sem, _ := lr.al.sessionProcessing.LoadOrStore(sessionKey, make(chan struct{}, 1))
	semCh := sem.(chan struct{})
	select {
	case semCh <- struct{}{}:
		defer func() { <-semCh }()
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	// d. Respect per-turn MaxTokens without mutating the shared AgentInstance.
	if req.MaxTokens > 0 {
		agentCopy := *agent
		agentCopy.MaxTokens = req.MaxTokens
		agent = &agentCopy
	}

	// e. Build messages.
	system := req.SystemPrompt
	userContent := req.Instruction
	if req.Transcript != "" {
		userContent = "Panel context (shared transcript):\n" + req.Transcript + "\n\n---\n\n" + req.Instruction
	}
	messages := []providers.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: userContent},
	}

	// f. Direct LLM call — no tools, no streaming.
	// TODO(T2.4): support EnableTools=true reusing the bounded tool loop
	caller := newLLMCaller(lr.al)
	callOpts := llmCallOptions{
		ctx:        ctx,
		agent:      agent,
		messages:   messages,
		toolDefs:   nil,
		model:      agent.Model,
		candidates: agent.Candidates,
		sessionKey: sessionKey,
		iteration:  1,
		// stream handlers nil — no streaming for group turns for now
	}
	response, _, err := caller.executeWithRetry(callOpts, messages)
	if err != nil {
		return "", 0, fmt.Errorf("group turn LLM call failed: %w", err)
	}

	// g. Extract token usage.
	tokens := 0
	if response != nil && response.Usage != nil {
		tokens = response.Usage.TotalTokens
	}

	// h. Extract content.
	content := ""
	if response != nil {
		content = response.Content
	}

	return content, tokens, nil
}
