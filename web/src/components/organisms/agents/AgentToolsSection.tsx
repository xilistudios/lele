import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useSettings } from '../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../hooks/useSettingsHelpers'
import {
  CATEGORY_LABEL_KEYS,
  CATEGORY_ORDER,
  ESSENTIAL_TOOLS,
  type ToolCategory,
  categorize,
} from '../../../lib/toolCatalog'
import type { EditableAgentConfig } from '../../../lib/types'
import { SearchIcon } from '../../atoms/Icons'
import { CategoryGroup } from '../../molecules/CategoryGroup'
import { SegmentedControl } from '../../molecules/SegmentedControl'
import { SettingsSection } from '../../molecules/SettingsSection'
import { ToggleCard } from '../../molecules/ToggleCard'

/**
 * `tab=tools` — per-agent tool allowlist (spec §4.6).
 *
 * Two access modes over a single config value (`agents.list.{index}.tools`):
 *
 * - `all`    → the value is absent/undefined: the agent keeps its full default
 *   registry. The grid is still shown, with every card checked, as a live
 *   preview of what "all" grants. Unchecking a card here is the natural entry
 *   point to restriction: it writes the catalog minus that tool, and the mode
 *   (derived from the value, §4.6.1) flips to `custom`.
 * - `custom` → the value is a NON-empty array: only the listed tools stay
 *   registered. Switching through the segmented control pre-selects the whole
 *   catalog (a no-op restriction the user then trims down); switching back
 *   writes `undefined`. Clearing every selection also writes `undefined` —
 *   "no tools at all" is not a state the backend can represent (an empty
 *   allowlist keeps everything), so the UI never pretends otherwise.
 *
 * The tool list comes from `GET /agents/{id}/catalog` — the agent's LIVE tool
 * registry, not the static category map. Allowlisted names that are no longer
 * in the catalog are still rendered (unknown names fall into `other` via
 * `categorize`), so a restricted agent can always be un-restricted from the
 * UI: tools are never lost.
 *
 * Writes always store the array sorted by name (byte order, like Go's
 * `sort.Strings`) so toggling back and forth produces a stable diff.
 */

type ToolsMode = 'all' | 'custom'

type Props = {
  agent: EditableAgentConfig
  /** Position in `agents.list` — dirty/write paths are positional. */
  index: number
  /** Agent id; used for the catalog query and the subagents-tab link. */
  agentId: string
}

/** Deduped, name-sorted array — the shape persisted to config. */
function sortTools(names: Iterable<string>): string[] {
  return [...new Set(names)].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
}

type CatalogEntry = { name: string; description: string }

const BATCH_BUTTON_CLS =
  'h-8 rounded-lg border border-border bg-background-secondary px-2.5 text-xs font-medium text-text-secondary transition-colors hover:border-border-strong hover:text-text-primary'

export function AgentToolsSection({ agent, index, agentId }: Props) {
  const { t, api, updateField, dirtyPaths, draftConfig } = useSettings()
  const [query, setQuery] = useState('')

  const path = `agents.list.${index}.tools`

  // Read through the shared draft when possible: the parent may hand us the
  // remote snapshot, but every write lands in the draft, and the segmented
  // control must reflect what is about to be saved.
  const draftAgent = draftConfig?.agents?.list?.[index]
  const current: EditableAgentConfig = draftAgent && draftAgent.id === agent.id ? draftAgent : agent

  // §4.6.1: the mode is derived from the value, never stored separately. An
  // EMPTY array is normalized to `all` because "zero tools" is not an
  // expressible state on the backend: applyToolsAllowlist keeps EVERY tool
  // when the allowlist is empty (TestApplyToolsAllowlist_EmptyPreservesAll)
  // and the config field is `omitempty`, so a legacy `tools: []` must render
  // as unrestricted — showing it as custom-with-0 would lie about what the
  // agent can actually do.
  const allowlist =
    Array.isArray(current.tools) && current.tools.length > 0 ? current.tools : undefined
  const mode: ToolsMode = allowlist ? 'custom' : 'all'
  const selected = new Set(allowlist ?? [])

  const { data, isLoading, isError, refetch } = useQuery({
    // Keyed by agent: the catalog is the live registry of THAT agent.
    queryKey: ['agentCatalog', agentId],
    queryFn: () => api.getAgentCatalog(agentId),
    staleTime: 30_000,
    retry: 1,
  })

  const catalog = data?.tools ?? []

  /** What the grid shows: catalog ∪ allowlist (unknown names are never hidden). */
  const entries: CatalogEntry[] = sortTools([
    ...catalog.map((tool) => tool.name),
    ...(allowlist ?? []),
  ]).map((name) => ({
    name,
    description: catalog.find((tool) => tool.name === name)?.description ?? '',
  }))

  const total = entries.length
  const active = mode === 'custom' ? entries.filter((e) => selected.has(e.name)).length : total

  const writeTools = (next: string[]) => {
    // An emptied allowlist is written as `undefined`, not `[]`: the backend
    // treats an empty allowlist as "no restriction" (and `omitempty` would
    // drop `[]` anyway), so normalizing here keeps the saved config, the
    // derived mode and the banner honest. The path is still marked dirty, so
    // the tab dot and Save behave as with any other change.
    updateField(path, next.length > 0 ? sortTools(next) : undefined)
  }

  const handleModeChange = (next: ToolsMode) => {
    if (next === mode) return
    // Entering custom starts from "everything the agent has right now";
    // leaving it drops the key entirely (undefined = default registry).
    if (next === 'custom') writeTools(entries.map((entry) => entry.name))
    else updateField(path, undefined)
  }

  const toggleTool = (name: string, checked: boolean) => {
    const base = allowlist ?? entries.map((entry) => entry.name)
    writeTools(checked ? [...base, name] : base.filter((entry) => entry !== name))
  }

  // ── Search (§4.6.4) ──────────────────────────────────────────────────────
  const trimmed = query.trim().toLowerCase()
  const searching = trimmed.length > 0
  const matches = (entry: CatalogEntry) =>
    !searching ||
    entry.name.toLowerCase().includes(trimmed) ||
    entry.description.toLowerCase().includes(trimmed)

  // Group in CATEGORY_ORDER; unknown tools land in `other` via categorize.
  const buckets = new Map<ToolCategory, CatalogEntry[]>()
  for (const entry of entries) {
    const category = categorize(entry.name)
    const bucket = buckets.get(category)
    if (bucket) bucket.push(entry)
    else buckets.set(category, [entry])
  }
  const groups = CATEGORY_ORDER.filter((category) => buckets.has(category)).map((category) => {
    const all = buckets.get(category) as CatalogEntry[]
    return { category, all, matched: all.filter(matches) }
  })

  // While searching: groups without hits are hidden, groups with hits are
  // force-expanded (the controlled `open` prop beats the user's collapse).
  const visibleGroups = searching ? groups.filter((group) => group.matched.length > 0) : groups

  // ── Spawn warning (§4.6.6) ───────────────────────────────────────────────
  const delegatingTo = current.subagents?.allow_agents ?? []
  const spawnMissing = mode === 'custom' && delegatingTo.length > 0 && !selected.has('spawn')

  const isDirty = isDirtyPath(dirtyPaths, path)

  return (
    <SettingsSection title={t('settings.agentPage.tab.tools')}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <SegmentedControl<ToolsMode>
          id="tools-mode"
          ariaLabel={t('settings.agentPage.toolsModeAria', {
            defaultValue: 'Tools access mode',
          })}
          value={mode}
          onChange={handleModeChange}
          disabled={isLoading}
          options={[
            {
              value: 'all',
              label: t('settings.agentPage.toolsModeAll', { defaultValue: 'All (default)' }),
            },
            {
              value: 'custom',
              label: t('settings.agentPage.toolsModeCustom', { defaultValue: 'Custom' }),
            },
          ]}
        />
        {isDirty && (
          <span className="text-[11px] text-state-info" data-testid="tools-dirty">
            {t('settings.agentPage.sectionHasChanges', {
              defaultValue: 'Unsaved changes in this section',
            })}
          </span>
        )}
      </div>

      {mode === 'all' && !isLoading && (
        <p
          data-testid="tools-all-banner"
          className="rounded-lg border border-state-info/30 bg-accent-subtle px-3 py-2 text-xs text-text-secondary"
        >
          {t('settings.agentPage.toolsAllBanner', {
            total,
            defaultValue: `This agent has access to ALL system tools (${total}). Switch to Custom to restrict them.`,
          })}
        </p>
      )}

      {spawnMissing && (
        <div
          data-testid="tools-spawn-warning"
          className="flex flex-wrap items-start gap-2 rounded-lg border border-state-warning/40 bg-state-warning-light px-3 py-2 text-xs text-state-warning"
        >
          <span>
            {t('settings.agentPage.toolsSpawnWarning', {
              agents: delegatingTo.join(', '),
              defaultValue: `Subagents are enabled for ${delegatingTo.join(', ')} but the spawn tool is disabled. This agent will not be able to delegate.`,
            })}
          </span>
          <Link
            to={`/agents/${agentId}/subagents`}
            data-testid="tools-spawn-warning-link"
            className="font-medium text-interaction-primary underline"
          >
            {t('settings.agentPage.tab.subagents', { defaultValue: 'Subagents' })}
          </Link>
        </div>
      )}

      {isError && (
        <div
          data-testid="tools-load-error"
          className="flex items-center justify-between gap-3 rounded-lg border border-state-error/40 bg-background-secondary px-3 py-2 text-xs text-state-error"
        >
          <span>
            {t('settings.agentPage.toolsLoadError', {
              defaultValue: 'Could not load the tool catalog.',
            })}
          </span>
          <button
            type="button"
            onClick={() => void refetch()}
            className="font-medium text-interaction-primary underline"
          >
            {t('settings.agentPage.retry', { defaultValue: 'Retry' })}
          </button>
        </div>
      )}

      {isLoading && (
        // Skeleton with the shape of the real content: 2 category groups.
        <div data-testid="tools-skeleton" aria-hidden="true" className="space-y-3">
          {[0, 1].map((skeleton) => (
            <div key={skeleton} className="space-y-2">
              <div className="h-9 w-40 animate-pulse rounded-md bg-background-tertiary" />
              <div className="grid grid-cols-1 gap-2.5 md:grid-cols-2 xl:grid-cols-3">
                {[0, 1, 2].map((card) => (
                  <div
                    key={card}
                    className="h-[60px] animate-pulse rounded-lg bg-background-secondary"
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {!isLoading && !isError && total === 0 && (
        <div
          data-testid="tools-empty"
          className="rounded-lg border-2 border-dashed border-border bg-background-secondary/20 px-4 py-6 text-center text-xs text-text-secondary"
        >
          {t('settings.agentPage.toolsEmpty', {
            defaultValue: 'The system tool catalog is empty.',
          })}
        </div>
      )}

      {!isLoading && !isError && total > 0 && (
        <div className="space-y-3">
          {mode === 'custom' && (
            <div className="flex flex-wrap items-center gap-2" data-testid="tools-toolbar">
              <div className="relative min-w-[180px] flex-1">
                <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-muted">
                  <SearchIcon size={16} />
                </span>
                <input
                  type="search"
                  data-testid="tools-search"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder={t('settings.agentPage.toolsSearch', {
                    defaultValue: 'Search tools',
                  })}
                  aria-label={t('settings.agentPage.toolsSearch', {
                    defaultValue: 'Search tools',
                  })}
                  className="h-9 w-full rounded-lg border border-border bg-background-secondary pl-9 pr-3 text-sm text-text-primary placeholder:text-text-muted focus:border-border-focus focus:outline-none"
                />
              </div>
              <span
                data-testid="tools-count"
                className="whitespace-nowrap text-xs text-text-tertiary"
              >
                {t('settings.agentPage.toolsCount', {
                  active,
                  total,
                  defaultValue: `${active} of ${total} active`,
                })}
              </span>
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  data-testid="tools-batch-essential"
                  // Essentials the agent's live registry actually has: naming a
                  // tool the agent never had would be dead config.
                  onClick={() =>
                    writeTools(
                      ESSENTIAL_TOOLS.filter((name) =>
                        entries.some((entry) => entry.name === name),
                      ),
                    )
                  }
                  className={BATCH_BUTTON_CLS}
                >
                  {t('settings.agentPage.toolsBatchEssential', { defaultValue: 'Essentials' })}
                </button>
                <button
                  type="button"
                  data-testid="tools-batch-none"
                  onClick={() => writeTools([])}
                  className={BATCH_BUTTON_CLS}
                >
                  {t('settings.agentPage.toolsBatchNone', { defaultValue: 'None' })}
                </button>
                <button
                  type="button"
                  data-testid="tools-batch-all"
                  onClick={() => writeTools(entries.map((entry) => entry.name))}
                  className={BATCH_BUTTON_CLS}
                >
                  {t('settings.agentPage.toolsBatchAll', { defaultValue: 'All' })}
                </button>
              </div>
            </div>
          )}

          {visibleGroups.length === 0 && (
            <p data-testid="tools-no-matches" className="px-1 py-3 text-xs text-text-muted">
              {t('settings.agentPage.toolsNoMatches', {
                query,
                defaultValue: `No tools match "${query}"`,
              })}
            </p>
          )}

          {visibleGroups.map((group) => (
            <CategoryGroup
              key={group.category}
              title={t(CATEGORY_LABEL_KEYS[group.category], {
                defaultValue: group.category,
              })}
              // In `all` mode every card is checked (full access), so the
              // header counter must agree with the grid.
              activeCount={
                mode === 'all'
                  ? group.all.length
                  : group.all.filter((e) => selected.has(e.name)).length
              }
              totalCount={group.all.length}
              // Controlled only while searching; otherwise the user can
              // collapse groups freely (defaultOpen=true, §4.6.4).
              open={searching ? true : undefined}
              defaultOpen={true}
            >
              <div className="grid grid-cols-1 gap-2.5 md:grid-cols-2 xl:grid-cols-3">
                {group.matched.map((entry) => (
                  <ToggleCard
                    key={entry.name}
                    id={`tool-${entry.name}`}
                    size="sm"
                    descriptionLines={1}
                    title={entry.name}
                    description={entry.description || undefined}
                    checked={mode === 'all' ? true : selected.has(entry.name)}
                    onChange={(checked) => toggleTool(entry.name, checked)}
                  />
                ))}
              </div>
            </CategoryGroup>
          ))}
        </div>
      )}
    </SettingsSection>
  )
}
