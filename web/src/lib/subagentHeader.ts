import type { Agent } from './types'
import type { SubagentTaskInfo } from './types'

export function selectSubagentForSession(
  subagents: SubagentTaskInfo[],
  sessionKey: string | null,
): SubagentTaskInfo | null {
  if (!sessionKey) return null
  return subagents.find((s) => s.session_key === sessionKey) ?? null
}

export function resolveHeaderAgentName(
  entry: SubagentTaskInfo | null,
  agents: Agent[],
  fallback: string,
): string {
  if (!entry) return fallback
  const agent = agents.find((a) => a.id === entry.agent_id)
  return agent?.name ?? entry.agent_id
}

export function resolveHeaderTitle(entry: SubagentTaskInfo | null, fallback: string): string {
  if (!entry) return fallback
  const trimmed = entry.label.trim()
  return trimmed || fallback
}
