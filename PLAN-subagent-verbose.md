# Plan: Fix verbose basic mode for subagent logs

## Problem
When verbose mode is set to "basic", subagent tool execution logs are sent completely (full) instead of being formatted with simplified messages.

## Root Cause
The `publishSubagentAsyncResult` function in `pkg/agent/subagent_helpers.go` sends the full `ForLLM` content of tool results without applying the basic verbose formatting.

## Solution
Modify `publishSubagentAsyncResult` to:
1. Check the verbose level for the session
2. When in basic mode, format tool execution logs using `formatBasicToolMessage` patterns
3. Only send simplified summaries instead of full logs

## Files to Modify
1. `pkg/agent/subagent_helpers.go` - Add verbose level check and formatting logic

## Implementation Steps
1. Import `session` package for `VerboseLevel` constants
2. Access `verboseManager` from `AgentLoop`
3. Check verbose level before formatting content
4. Apply simplified formatting when in basic mode
5. Parse tool execution patterns from ForLLM content and format them
