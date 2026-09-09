import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../contexts/SettingsContext'
import { isAgentDirty } from '../../lib/agentDirty'
import { AgentAvatar } from '../atoms/AgentAvatar'
import { Button } from '../atoms/Button'
import { IconButton } from '../atoms/IconButton'
import { CloseIcon, PlusIcon, SearchIcon } from '../atoms/Icons'
import { AgentCard } from '../organisms/agents/AgentCard'
import { AddAgentModal } from '../organisms/settings/AddAgentWizard'

/**
 * Agents master list — design spec §3.
 *
 * Route: `/agents` (index child of `AgentEntityLayout`, which owns the
 * shared `useSettingsConfig` draft via `SettingsProvider`). Everything here
 * reads and writes that draft: the cards reflect unsaved edits live, and the
 * layout's `SettingsFooter` is what actually persists them.
 *
 * Layout: toolbar (search + count + "Add agent") above a responsive card
 * grid — 1 column on mobile, 2 at `lg`, 3 at `2xl` — with three states:
 * skeleton while loading, empty CTA when there are no agents, and a "no
 * matches" message when the search filters everything out.
 *
 * Search matches `id`, `name` and `description` (case-insensitive substring).
 * Filtering never renumbers the agents: `isModified` is computed against the
 * ORIGINAL position in `agents.list`, because dirty paths are positional
 * (`agents.list.{index}.…`, see `lib/agentDirty.ts`).
 */

/** Placeholder card matching AgentCard's footprint (avatar row + meta + actions). */
function AgentCardSkeleton() {
  return (
    <div
      data-testid="agent-card-skeleton"
      className="animate-pulse rounded-lg border border-border bg-background-secondary p-4"
      aria-hidden="true"
    >
      <div className="flex gap-3">
        <div className="h-10 w-10 flex-none rounded-full bg-surface-muted" />
        <div className="min-w-0 flex-1 space-y-2">
          <div className="h-3.5 w-1/3 rounded bg-surface-muted" />
          <div className="h-3 w-1/2 rounded bg-surface-muted" />
        </div>
      </div>
      <div className="mt-3 h-3 w-3/4 rounded bg-surface-muted" />
      <div className="mt-3 flex gap-2 border-t border-border-light pt-3">
        <div className="h-7 w-24 rounded-lg bg-surface-muted" />
        <div className="ml-auto h-7 w-16 rounded-lg bg-surface-muted" />
      </div>
    </div>
  )
}

/** Row shown while the config draft loads (keeps the grid shape stable). */
function LoadingGrid() {
  const { t } = useTranslation()
  return (
    <div aria-busy="true" className="grid grid-cols-1 gap-4 lg:grid-cols-2 2xl:grid-cols-3">
      <span className="sr-only">{t('settings.loading')}</span>
      {[0, 1, 2, 3, 4, 5].map((i) => (
        <AgentCardSkeleton key={i} />
      ))}
    </div>
  )
}

export function AgentsListPage() {
  const { t } = useTranslation()
  const { draftConfig, dirtyPaths, isLoading, updateField } = useSettings()

  const [query, setQuery] = useState('')
  const [showWizard, setShowWizard] = useState(false)

  const agents = useMemo(() => draftConfig?.agents?.list ?? [], [draftConfig])
  const defaultsModel = draftConfig?.agents?.defaults?.model ?? ''

  const normalizedQuery = query.trim().toLowerCase()
  const filtered = useMemo(() => {
    if (!normalizedQuery) return agents
    return agents.filter((a) => {
      return (
        a.id.toLowerCase().includes(normalizedQuery) ||
        (a.name ?? '').toLowerCase().includes(normalizedQuery) ||
        (a.description ?? '').toLowerCase().includes(normalizedQuery)
      )
    })
  }, [agents, normalizedQuery])

  const removeAgent = (index: number) => {
    // Same semantics as AgentsSettings: rewrite `agents.list` without the
    // removed position (positional dirty paths shift — accepted, saving is
    // what persists the list).
    updateField(
      'agents.list',
      agents.filter((_a, i) => i !== index),
    )
  }

  const countLabel =
    normalizedQuery && filtered.length !== agents.length
      ? t('settings.agentPage.countFiltered', { shown: filtered.length, total: agents.length })
      : t('settings.agentPage.count', { count: agents.length })

  return (
    <div className="mx-auto w-full max-w-6xl">
      {/* Toolbar */}
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="relative min-w-0 flex-1 basis-56">
          <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-muted">
            <SearchIcon size={16} />
          </span>
          <input
            type="search"
            data-testid="agents-search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t('settings.agentPage.searchPlaceholder')}
            aria-label={t('settings.agentPage.searchPlaceholder')}
            className="h-9 w-full rounded-lg border border-border bg-background-secondary pl-9 pr-9 text-sm text-text-primary placeholder:text-text-muted focus:border-border-focus focus:outline-none"
          />
          {query && (
            <span className="absolute right-1.5 top-1/2 -translate-y-1/2">
              <IconButton
                variant="ghost"
                ariaLabel={t('settings.agentPage.clearSearch')}
                onClick={() => setQuery('')}
                className="flex h-7 w-7 items-center justify-center"
              >
                <CloseIcon size={14} />
              </IconButton>
            </span>
          )}
        </div>

        <span data-testid="agents-count" className="text-sm text-text-tertiary">
          {countLabel}
        </span>

        <Button variant="primary" onClick={() => setShowWizard(true)}>
          <PlusIcon size={16} />
          {t('settings.addAgent')}
        </Button>
      </div>

      {/* States */}
      {isLoading ? (
        <LoadingGrid />
      ) : agents.length === 0 ? (
        <div
          data-testid="agents-empty"
          className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border px-6 py-12 text-center"
        >
          <AgentAvatar id="empty" size="xl" />
          <h2 className="mt-4 text-base font-medium text-text-primary">
            {t('settings.agentPage.emptyTitle')}
          </h2>
          <p className="mt-1 max-w-sm text-sm text-text-secondary">
            {t('settings.agentPage.emptyDesc')}
          </p>
          <Button variant="primary" className="mt-4" onClick={() => setShowWizard(true)}>
            <PlusIcon size={16} />
            {t('settings.addAgent')}
          </Button>
        </div>
      ) : filtered.length === 0 ? (
        <div
          data-testid="agents-no-matches"
          className="rounded-lg border border-dashed border-border px-6 py-10 text-center text-sm text-text-secondary"
        >
          {t('settings.agentPage.noMatches', { query: query.trim() })}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 2xl:grid-cols-3">
          {filtered.map((agent) => {
            // Positional dirty lookup against the UNFILTERED list (§3.3).
            const originalIndex = agents.indexOf(agent)
            return (
              <AgentCard
                key={agent.id}
                agent={agent}
                isModified={isAgentDirty(dirtyPaths, originalIndex)}
                defaultsModel={defaultsModel}
                onRemove={() => removeAgent(originalIndex)}
              />
            )
          })}
        </div>
      )}

      <AddAgentModal isOpen={showWizard} onClose={() => setShowWizard(false)} />
    </div>
  )
}
