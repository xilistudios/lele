export const AGENT_TABS = [
  'general',
  'model',
  'skills',
  'mcp',
  'commands',
  'tools',
  'subagents',
  'files',
] as const

export type AgentTab = (typeof AGENT_TABS)[number]

export function isAgentTab(value: string | undefined): value is AgentTab {
  return value !== undefined && (AGENT_TABS as readonly string[]).includes(value)
}

export const AGENT_LIST_PREFIX = 'agents.list'

export function agentPathPrefix(index: number): string {
  return `${AGENT_LIST_PREFIX}.${index}`
}

export const SECTION_PATHS: Record<AgentTab, string[]> = {
  general: ['name', 'description', 'default', 'workspace'],
  model: ['model', 'temperature', 'thinking_level'],
  skills: ['skills'],
  mcp: [],
  commands: [],
  tools: ['tools'],
  subagents: ['subagents'],
  files: [],
}

export function sectionPrefixes(index: number, tab: AgentTab): string[] {
  return SECTION_PATHS[tab].map((suffix) => `${agentPathPrefix(index)}.${suffix}`)
}

export function pathInPrefix(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(`${prefix}.`) || path.startsWith(`${prefix}[`)
}

export function isSectionDirty(dirtyPaths: Set<string>, index: number, tab: AgentTab): boolean {
  if (!dirtyPaths || dirtyPaths.size === 0) return false
  const prefixes = sectionPrefixes(index, tab)
  return prefixes.some((prefix) => {
    for (const path of dirtyPaths) {
      if (pathInPrefix(path, prefix)) return true
    }
    return false
  })
}

export function isAgentDirty(dirtyPaths: Set<string>, index: number): boolean {
  if (!dirtyPaths || dirtyPaths.size === 0) return false
  const prefix = `${agentPathPrefix(index)}.`
  for (const path of dirtyPaths) {
    if (path.startsWith(prefix)) return true
  }
  return false
}
