/**
 * Scope tabs for the `/skills` page — the only place this feature's SEMANTICS
 * live. Deliberately pure: no React, no DOM, no i18n import. It returns i18n
 * KEYS and the caller translates them, so it can be tested without jsdom.
 *
 * Two decisions are encoded here. Read them before "fixing" anything:
 *
 *   D2 — the Global view lists `source === 'global'` ONLY; built-ins are NOT
 *   included. The tab is not only a filter: it selects the install destination
 *   (`scope:"global"` ⇒ `<leleDir>/skills`), while built-ins live in
 *   `<leleDir>/lele/skills` and can never be installed to, or removed from,
 *   the global directory. Built-ins also render read-only on this page, so
 *   including them would be inert noise under a tab a card badged "Built-in"
 *   we would contradict. The predicate is isolated in `GLOBAL_VIEW_SOURCES`,
 *   so flipping the decision is a one-line change plus the test that pins it.
 *
 *   D11 — which agent the "By agent" view starts on. See `resolveAgentId`:
 *   the requested `?agent=` wins when it exists, else the agent of the chat
 *   the user came from, else the default agent, else the first one, else null.
 */
import type { Agent, AgentSkillCatalogEntry, SkillInfo, SkillSource } from './types'

/** The three filter tabs on `/skills`. */
export type SkillsView = 'all' | 'global' | 'agent'

/** Tab order as rendered (All is the default — D3). */
export const SKILLS_VIEWS: readonly SkillsView[] = ['all', 'global', 'agent']

/**
 * URL query params (D1). Named `view`, not `scope`, because `scope` already
 * means install destination everywhere else (`types.ts` `SkillInstallScope`).
 */
export const VIEW_PARAM = 'view'
export const AGENT_PARAM = 'agent'

/** Narrow a raw query-string value to a known view. */
export function isSkillsView(raw: string | null | undefined): raw is SkillsView {
  return raw === 'all' || raw === 'global' || raw === 'agent'
}

/** Parse a raw `?view=` value; anything unknown (or absent) normalises to `all`. */
export function parseSkillsView(raw: string | null | undefined): SkillsView {
  return isSkillsView(raw) ? raw : 'all'
}

/**
 * D2 — the one place the contested predicate lives. The Global tab means
 * "installed in the shared directory", not "shared across agents": built-ins
 * are excluded on purpose (see the module header). Flip this single line to
 * change the taxonomy — `isInGlobalView` and `filterSkillsForView` follow.
 */
export const GLOBAL_VIEW_SOURCES: readonly SkillSource[] = ['global']

/** True when a skill belongs in the Global view (D2). */
export function isInGlobalView(skill: Pick<SkillInfo, 'source'>): boolean {
  return skill.source != null && GLOBAL_VIEW_SOURCES.includes(skill.source)
}

/**
 * Client-side filter for the two views that share `GET /api/v1/skills`.
 * `all` returns the input array as-is (no copy); `global` keeps only the
 * sources listed in `GLOBAL_VIEW_SOURCES`.
 */
export function filterSkillsForView(skills: SkillInfo[], view: 'all' | 'global'): SkillInfo[] {
  return view === 'all' ? skills : skills.filter(isInGlobalView)
}

/**
 * Adapter from the per-agent catalog to what `SkillsList` renders. The catalog
 * only lists resolved skills (see `types.ts`), so `installed` is always true
 * and the id is the name. `deletable` is intentionally NOT leaked onto
 * `SkillInfo` — the type mirrors the wire format of `GET /api/v1/skills`.
 */
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

/**
 * D11 — agent resolution order for the By-agent view:
 *   1. `requested`, if it exists in `agents`   (deep link wins)
 *   2. `currentAgent?.id`                       (the chat you came from)
 *   3. `agents.find(a => a.default)?.id`
 *   4. `agents[0]?.id`
 *   5. `null`                                   (no agents configured)
 */
export function resolveAgentId(
  requested: string | null,
  agents: Agent[],
  currentAgent: Agent | null,
): string | null {
  if (requested && agents.some((a) => a.id === requested)) return requested
  if (currentAgent) return currentAgent.id
  return agents.find((a) => a.default)?.id ?? agents[0]?.id ?? null
}

/** The i18n key of the hint line rendered under the tabs. */
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
