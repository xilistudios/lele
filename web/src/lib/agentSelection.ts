import type { EditableAgentConfig } from './types'

export type SelectableAgent = {
  agent: EditableAgentConfig
  id: string
  index: number
}

export function selectableAgents(list: EditableAgentConfig[]): SelectableAgent[] {
  return list
    .map((agent, index) => ({ agent, id: agent.id.trim(), index }))
    .filter((entry) => entry.id !== '')
}

export function defaultAgentId(selectable: SelectableAgent[]): string | null {
  return selectable.find((entry) => entry.agent.default)?.id ?? selectable[0]?.id ?? null
}
