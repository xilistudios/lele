/**
 * Per-section dirty detection for the agent config tabs (spec §5.3).
 *
 * The settings draft reports changed fields as dotted paths in a
 * `Set<string>` (`agents.list.2.model.primary`, …). The agent detail page
 * groups those fields into tabs and needs one boolean per tab to paint the
 * blue dot / red error dot in the tab strip.
 *
 * Matching is by PREFIX, not equality: a dirty `…model.primary` must light up
 * the `model` tab, whose registered prefix is the shorter `…model`.
 */

/** Tabs of `/agents/:agentId/:tab` (spec §1.3, same order as the tab strip). */
export const AGENT_TABS = ['general', 'model', 'skills', 'tools', 'subagents', 'files'] as const

export type AgentTab = (typeof AGENT_TABS)[number]

export function isAgentTab(value: string | undefined): value is AgentTab {
  return value !== undefined && (AGENT_TABS as readonly string[]).includes(value)
}

/** Root path of the agents list; every agent field hangs off `agents.list.{index}.` */
export const AGENT_LIST_PREFIX = 'agents.list'

/** Build `agents.list.{index}` (the base of every field path of one agent). */
export function agentPathPrefix(index: number): string {
  return `${AGENT_LIST_PREFIX}.${index}`
}

/**
 * Tab -> path suffixes that belong to it, relative to `agents.list.{index}`.
 * Table §5.3. `files` has none: that tab never writes config.
 */
export const SECTION_PATHS: Record<AgentTab, string[]> = {
  general: ['name', 'description', 'default', 'workspace'],
  model: ['model', 'temperature', 'thinking_level'],
  skills: ['skills'],
  tools: ['tools'],
  subagents: ['subagents'],
  files: [],
}

/** Every dirty-path prefix of a section for one agent (absolute, dotted). */
export function sectionPrefixes(index: number, tab: AgentTab): string[] {
  return SECTION_PATHS[tab].map((suffix) => `${agentPathPrefix(index)}.${suffix}`)
}

/**
 * Does `path` belong to `prefix`? True for the exact path and for anything
 * nested below it (`agents.list.2.model.primary` belongs to
 * `agents.list.2.model`), including array-segment children (`…model[0]`).
 */
export function pathInPrefix(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(`${prefix}.`) || path.startsWith(`${prefix}[`)
}

/**
 * Does `dirtyPaths` contain any change belonging to `tab` of agent `index`?
 *
 * A prefix matches itself and anything nested below it (`…model.primary`
 * counts for the `model` tab). `files` is always false.
 */
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

/**
 * Is any field of the whole agent dirty? Used by the "modified" badge on the
 * list cards — same literal rule the current `AgentsSettings` uses.
 */
export function isAgentDirty(dirtyPaths: Set<string>, index: number): boolean {
  if (!dirtyPaths || dirtyPaths.size === 0) return false
  const prefix = `${agentPathPrefix(index)}.`
  for (const path of dirtyPaths) {
    if (path.startsWith(prefix)) return true
  }
  return false
}
