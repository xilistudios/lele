import type { Agent } from './types'
import type { SubagentTaskInfo } from './types'

/**
 * Find the subagent task entry that matches the given session key.
 *
 * Returns `null` when `sessionKey` is falsy, when no entry matches, or when
 * the subagents list is empty.  Pure — no side effects, no API calls.
 */
export function selectSubagentForSession(
  subagents: SubagentTaskInfo[],
  sessionKey: string | null,
): SubagentTaskInfo | null {
  if (!sessionKey) return null
  return subagents.find((s) => s.session_key === sessionKey) ?? null
}

/**
 * Resolve the display name shown in the chat header.
 *
 * When a matching subagent entry exists, its `agent_id` is looked up in the
 * agents list; if found the agent's `name` is used, otherwise the raw
 * `agent_id` is returned as a best-effort fallback.  When there is no
 * matching entry the caller-supplied `fallback` (typically the current
 * agent's name) is returned unchanged.
 */
export function resolveHeaderAgentName(
  entry: SubagentTaskInfo | null,
  agents: Agent[],
  fallback: string,
): string {
  if (!entry) return fallback
  const agent = agents.find((a) => a.id === entry.agent_id)
  return agent?.name ?? entry.agent_id
}

/**
 * Resolve the display title shown in the chat header.
 *
 * When a matching subagent entry exists and has a non-empty `label`, that
 * label is returned (trimmed).  Otherwise `fallback` (typically produced by
 * `formatSessionTitle`) is returned unchanged.
 */
export function resolveHeaderTitle(entry: SubagentTaskInfo | null, fallback: string): string {
  if (!entry) return fallback
  const trimmed = entry.label.trim()
  return trimmed || fallback
}
