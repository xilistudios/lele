import type { Agent, AgentSkillCatalogEntry, SkillInfo, SkillSource } from './types'

export type SkillsView = 'all' | 'global' | 'agent'

export const SKILLS_VIEWS: readonly SkillsView[] = ['all', 'global', 'agent']

export const VIEW_PARAM = 'view'
export const AGENT_PARAM = 'agent'

export function isSkillsView(raw: string | null | undefined): raw is SkillsView {
  return raw === 'all' || raw === 'global' || raw === 'agent'
}

export function parseSkillsView(raw: string | null | undefined): SkillsView {
  return isSkillsView(raw) ? raw : 'all'
}

export const GLOBAL_VIEW_SOURCES: readonly SkillSource[] = ['global']

export function isInGlobalView(skill: Pick<SkillInfo, 'source'>): boolean {
  return skill.source != null && GLOBAL_VIEW_SOURCES.includes(skill.source)
}

export function filterSkillsForView(skills: SkillInfo[], view: 'all' | 'global'): SkillInfo[] {
  return view === 'all' ? skills : skills.filter(isInGlobalView)
}

export function catalogEntryToSkillInfo(entry: AgentSkillCatalogEntry): SkillInfo {
  return {
    id: entry.name,
    name: entry.name,
    description: entry.description,
    installed: true,
    enabled: entry.enabled,
    source: entry.source,
  }
}

export function resolveAgentId(
  requested: string | null,
  agents: Agent[],
  currentAgent: Agent | null,
): string | null {
  if (requested && agents.some((a) => a.id === requested)) return requested
  if (currentAgent) return currentAgent.id
  return agents.find((a) => a.default)?.id ?? agents[0]?.id ?? null
}

export function viewHintKey(
  view: SkillsView,
): 'skills.viewAllHint' | 'skills.viewGlobalHint' | 'skills.viewAgentHint' {
  switch (view) {
    case 'global':
      return 'skills.viewGlobalHint'
    case 'agent':
      return 'skills.viewAgentHint'
    default:
      return 'skills.viewAllHint'
  }
}
