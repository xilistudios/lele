/**
 * AgentSkillsSection — "Skills" tab panel of /agents/:agentId (spec §4.5).
 *
 * Two kinds of state meet on this panel and must not be confused:
 *
 *  - ALLOWLIST (config, `agents.list.{i}.skills`): which skills this agent may
 *    use. Written through `updateField` and saved with the rest of the config —
 *    the card checkbox, the None/All buttons.
 *  - WORKSPACE (disk, this agent's `<workspace>/skills` + its
 *    `.lele/workspace.json`): which skills are INSTALLED and ENABLED for it.
 *    Written through the per-agent REST endpoints immediately — the power and
 *    trash buttons and "Add skill" — with no Save involved.
 *
 * Both read from the same endpoint, `GET /api/v1/agents/{agentId}/catalog`,
 * which the backend resolves against the agent's live skills loader, so the
 * grid can never show a different skill set than the one the agent's system
 * prompt advertises.
 *
 * (react-query; the app-level QueryClientProvider lives in main.tsx and
 * AgentConfigPage already mounts SettingsProvider.)
 *
 * Contract with the parent (AgentConfigPage):
 * - `agentId` is passed explicitly as a prop: this component must NOT re-derive
 *   it from the route — the page owns `useParams` and the agent lookup.
 * - `index` is the agent's ORIGINAL position in `agents.list` (never a filtered
 *   index): every write goes through
 *   `updateField('agents.list.{index}.skills', nextArray)`.
 * - Reads `{ t, updateField, dirtyPaths, api }` from `useSettings()`.
 *
 * The stored value's ABSENCE is meaningful: no selection = "all installed
 * skills"; a non-empty array = allowlist. So the banner is not decoration —
 * it is the field's description (§4.5.1) and the batch buttons switch modes.
 *
 * States implemented: info/allowlist banner, search + counter + batch
 * toolbar, "disabled globally" warning cards (still selectable), orphan block
 * above the grid (existing values are NEVER hidden, §4.5.4), 4-card loading
 * skeleton (§5.1), dashed empty state linking to /skills (§5.5), retryable
 * load error.
 */
import { useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { useSettings } from '../../../contexts/SettingsContext'
import { agentCatalogQueryKey, useAgentSkills } from '../../../hooks/useAgentSkills'
import { getErrorForPath } from '../../../hooks/useSettingsHelpers'
import { isSectionDirty } from '../../../lib/agentDirty'
import { sourceBadgeClassNames, sourceBadgeLabel } from '../../../lib/skillSource'
import type { EditableAgentConfig, SkillSource } from '../../../lib/types'
import { Badge } from '../../atoms/Badge'
import { Button } from '../../atoms/Button'
import { IconButton } from '../../atoms/IconButton'
import {
  CloseIcon,
  EyeIcon,
  EyeOffIcon,
  LockIcon,
  PlusIcon,
  SearchIcon,
  TrashIcon,
} from '../../atoms/Icons'
import { RemoveButton } from '../../atoms/RemoveButton'
import { ToggleCard } from '../../molecules/ToggleCard'
import { AgentSkillInstallDialog } from './AgentSkillInstallDialog'

type Props = {
  /** The agent being edited (draft copy from the settings context). */
  agent: EditableAgentConfig
  /** ORIGINAL index of `agent` inside `agents.list` — basis of every write path. */
  index: number
  /** Agent id used to fetch the catalog. Passed by AgentConfigPage (see header). */
  agentId: string
}

/** One catalog skill as the grid needs it. */
type CatalogSkill = {
  name: string
  description: string
  source: SkillSource
  enabled: boolean
  /** Server-decided: the skill lives in THIS agent's workspace dir, so it may
   * be installed/removed through the agent. Global/built-in skills are shared
   * with every other agent and must not be removable from here. */
  deletable: boolean
}

/** Alphabetical by name (§4.5.5: selected entries do NOT float up — no
 *  surprise reorder on click). The backend already sorts; we sort anyway so
 *  the order never depends on the transport. */
function byName(a: { name: string }, b: { name: string }): number {
  return a.name.localeCompare(b.name)
}

/** Same order for plain name arrays (allowlist, orphans). */
function byString(a: string, b: string): number {
  return a.localeCompare(b)
}

/** Info circle, 16px — same stroke chrome as atoms/Icons. */
function InfoIcon({ className }: { className?: string }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className={className}
    >
      <circle cx="12" cy="12" r="10" />
      <path d="M12 16v-4" />
      <path d="M12 8h.01" />
    </svg>
  )
}

/**
 * Skeleton of one md ToggleCard (§5.1: preserve final geometry, no spinners).
 * Mirrors ToggleCard's DOM so the swap to real cards causes no reflow: title
 * row (60% wide) + two clamped description lines + the 18px check box.
 */
function ToggleCardSkeleton() {
  return (
    <div className="flex gap-2.5 rounded-lg border border-border bg-background-secondary p-3.5">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="h-3.5 w-3/5 animate-pulse rounded-md bg-background-tertiary" />
        <div className="h-3 w-4/5 animate-pulse rounded-md bg-background-tertiary" />
        <div className="h-3 w-2/3 animate-pulse rounded-md bg-background-tertiary" />
      </div>
      <div className="h-[18px] w-[18px] flex-none animate-pulse rounded-md bg-background-tertiary" />
    </div>
  )
}

/**
 * Enable/disable and delete, rendered inside a skill card.
 *
 * Both act on the agent's WORKSPACE (disk + its workspace.json), not on the
 * config allowlist, and take effect immediately — which is why each shows its
 * own pending state instead of waiting for the page's Save.
 *
 * Delete is offered only when the server said `deletable` (the skill lives in
 * this agent's own workspace). Global and built-in skills are shared with every
 * other agent, so removing them from here would be a surprising side effect;
 * the Skills page owns that.
 *
 * Confirmation is inline (same pattern as `organisms/SkillsList`) because a
 * delete is the one action here that cannot be undone by discarding the form.
 */
function SkillCardActions({
  name,
  enabled,
  deletable,
  pendingToggle,
  pendingRemove,
  confirmingRemove,
  onAskRemove,
  onCancelRemove,
  onConfirmRemove,
  onToggle,
}: {
  name: string
  enabled: boolean
  deletable: boolean
  pendingToggle: boolean
  pendingRemove: boolean
  confirmingRemove: boolean
  onAskRemove: (name: string) => void
  onCancelRemove: () => void
  onConfirmRemove: (name: string) => void
  onToggle: (enabled: boolean) => void
}) {
  const { t } = useTranslation()

  if (confirmingRemove) {
    return (
      <span
        data-testid={`skill-remove-confirm-${name}`}
        className="flex items-center gap-1.5 text-[11px]"
      >
        <span className="text-text-secondary">
          {t('settings.agentPage.skillsConfirmRemove', { defaultValue: 'Remove?' })}
        </span>
        <Button
          variant="danger"
          size="sm"
          data-testid={`skill-remove-yes-${name}`}
          disabled={pendingRemove}
          onClick={() => onConfirmRemove(name)}
        >
          {t('common.delete', { defaultValue: 'Delete' })}
        </Button>
        <Button variant="ghost" size="sm" onClick={onCancelRemove}>
          {t('common.cancel', { defaultValue: 'Cancel' })}
        </Button>
      </span>
    )
  }

  return (
    <>
      {/* Enabled/disabled is the AGENT's own workspace.json, so it applies to
          any skill the agent sees — disabling a shared skill for THIS agent is
          exactly what a per-agent config is for. */}
      <IconButton
        dataTestId={`skill-toggle-${name}`}
        title={
          enabled
            ? t('settings.agentPage.skillsDisable', { defaultValue: 'Disable in workspace' })
            : t('settings.agentPage.skillsEnable', { defaultValue: 'Enable in workspace' })
        }
        ariaLabel={
          enabled
            ? t('settings.agentPage.skillsDisableAria', {
                defaultValue: 'Disable {{name}} in this workspace',
                name,
              })
            : t('settings.agentPage.skillsEnableAria', {
                defaultValue: 'Enable {{name}} in this workspace',
                name,
              })
        }
        disabled={pendingToggle}
        onClick={() => onToggle(!enabled)}
      >
        {enabled ? <EyeIcon size={14} /> : <EyeOffIcon size={14} />}
      </IconButton>
      {deletable && (
        <IconButton
          dataTestId={`skill-remove-${name}`}
          variant="danger"
          title={t('settings.agentPage.skillsRemove', { defaultValue: 'Remove from workspace' })}
          ariaLabel={t('settings.agentPage.skillsRemoveAria', {
            defaultValue: 'Remove {{name}} from this workspace',
            name,
          })}
          disabled={pendingRemove}
          onClick={() => onAskRemove(name)}
        >
          <TrashIcon size={14} />
        </IconButton>
      )}
    </>
  )
}

export function AgentSkillsSection({ agent, index, agentId }: Props) {
  const { t, updateField, dirtyPaths, validationErrors, api } = useSettings()
  const [query, setQuery] = useState('')

  // Workspace administration (install / enable / remove) — immediate REST
  // writes; see the header comment for how that differs from the allowlist.
  const skills = useAgentSkills(api, { agentId })
  const [installOpen, setInstallOpen] = useState(false)
  /** Skill awaiting a delete confirmation, mirroring SkillsList's pattern. */
  const [pendingRemove, setPendingRemove] = useState<string | null>(null)

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: agentCatalogQueryKey(agentId),
    queryFn: () => api.getAgentCatalog(agentId),
    staleTime: 10_000,
    retry: 1,
  })

  const path = `agents.list.${index}.skills`
  const fieldError = getErrorForPath(validationErrors, path)

  const allowlist = agent.skills ?? []
  const selected = useMemo(() => new Set(allowlist), [allowlist])

  const catalog: CatalogSkill[] = useMemo(() => [...(data?.skills ?? [])].sort(byName), [data])
  const catalogNames = useMemo(() => new Set(catalog.map((entry) => entry.name)), [catalog])
  /** Directory a workspace-scoped install writes into ("" until loaded). */
  const workspacePath = data?.workspace ?? ''

  /** Names in agent.skills the catalog does not know (uninstalled / removed
   *  globally). Rendered FIRST, above the grid, never hidden (§4.5.4). */
  const orphans = useMemo(
    () => allowlist.filter((name) => !catalogNames.has(name)).sort(byString),
    [allowlist, catalogNames],
  )

  const normalizedQuery = query.trim().toLowerCase()
  const visibleCatalog = normalizedQuery
    ? catalog.filter(
        (entry) =>
          entry.name.toLowerCase().includes(normalizedQuery) ||
          (entry.description ?? '').toLowerCase().includes(normalizedQuery),
      )
    : catalog

  // Counter = allowlist ∩ catalog over total installed ("8 de 12 seleccionadas").
  const selectedInstalled = catalog.reduce((n, e) => (selected.has(e.name) ? n + 1 : n), 0)
  const allowlistActive = allowlist.length > 0

  /** Write the COMPLETE array on every change (§4.5.6). The exact path
   *  `agents.list.{index}.skills` is what lights the skills-tab dot (§5.3);
   *  an emptied allowlist writes `[]` (never undefined — the dot must fire on
   *  the exact registered prefix). */
  const writeSkills = (next: string[]) => {
    updateField(path, next)
  }

  const toggleSkill = (name: string, checked: boolean) => {
    if (checked) {
      if (allowlist.includes(name)) return
      writeSkills([...allowlist, name].sort(byString))
    } else {
      writeSkills(allowlist.filter((skill) => skill !== name))
    }
  }

  const removeOrphan = (name: string) => {
    writeSkills(allowlist.filter((skill) => skill !== name))
  }

  // "Todas" = complete installed list (full catalog, ignoring the search filter).
  const setNone = () => writeSkills([])
  const setAll = () => writeSkills(catalog.map((entry) => entry.name))

  const confirmRemove = (name: string) => {
    setPendingRemove(null)
    void skills.remove(name)
  }

  /** Same rule as the tab dot (§5.3): any dirty path under `…skills`. */
  const sectionDirty = isSectionDirty(dirtyPaths, index, 'skills')

  /**
   * One dialog instance for the whole panel (toolbar button and empty state
   * both open it), defined before the early returns so no branch can render a
   * second copy with a different set of props.
   */
  const installDialog = (
    <AgentSkillInstallDialog
      agentId={agentId}
      workspacePath={workspacePath}
      isOpen={installOpen}
      onClose={() => setInstallOpen(false)}
    />
  )

  // ---------- Loading (§5.1: 4 ToggleCard skeletons) ----------
  if (isLoading) {
    return (
      // Same loading a11y pattern as AgentsListPage: aria-busy + sr-only text.
      <div aria-busy="true">
        <span className="sr-only">{t('settings.loading')}</span>
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          {[0, 1, 2, 3].map((n) => (
            <ToggleCardSkeleton key={n} />
          ))}
        </div>
      </div>
    )
  }

  // ---------- Catalog failed to load ----------
  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-background-secondary/50 px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">
          {t('settings.agentPage.skillsLoadError', {
            defaultValue: 'Could not load the skills catalog.',
          })}
        </p>
        <Button variant="secondary" size="sm" data-testid="skills-retry" onClick={() => refetch()}>
          {t('settings.agentPage.retry', { defaultValue: 'Retry' })}
        </Button>
      </div>
    )
  }

  // ---------- Empty: nothing installed AND nothing orphaned (§4.5 / §5.5) ----------
  if (catalog.length === 0 && orphans.length === 0) {
    return (
      <>
        <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center">
          <p className="text-sm text-text-secondary">
            {t('settings.agentPage.skillsNoneInstalled', {
              defaultValue: 'No skills installed in this workspace yet.',
            })}
          </p>
          {/* Two destinations, because there are two places a skill can live:
              install here (this agent only) or manage the shared catalogue. */}
          <div className="flex items-center gap-3">
            <Button
              variant="secondary"
              size="sm"
              data-testid="skills-empty-add"
              onClick={() => setInstallOpen(true)}
            >
              <PlusIcon size={14} />
              {t('settings.agentPage.skillsAdd', { defaultValue: 'Add skill' })}
            </Button>
            <Link to="/skills" className="text-sm text-interaction-primary hover:underline">
              {t('settings.agentPage.goToSkills', { defaultValue: 'Go to Skills' })}
            </Link>
          </div>
        </div>
        {installDialog}
      </>
    )
  }

  return (
    <div>
      {/* ---------- Status banner (§4.5.1) — it IS the field description ---------- */}
      {allowlistActive ? (
        <div
          data-testid="skills-banner"
          className="mb-4 flex items-start gap-2.5 rounded-lg border border-border bg-background-tertiary px-4 py-3 text-xs"
        >
          <LockIcon size={16} className="mt-px flex-none text-text-tertiary" />
          <span className="text-text-secondary">
            {t('settings.agentPage.skillsCustomBanner', {
              defaultValue:
                'Allowlist active: only the {{count}} selected skills are available. Deselecting all returns to "all".',
              count: allowlist.length,
            })}
          </span>
        </div>
      ) : (
        <div
          data-testid="skills-banner"
          className="mb-4 flex items-start gap-2.5 rounded-lg border border-state-info/30 bg-state-info-light px-4 py-3 text-xs"
        >
          <InfoIcon className="mt-px flex-none text-state-info" />
          <span className="text-text-secondary">
            {t('settings.agentPage.skillsAllBanner', {
              defaultValue:
                'Nothing selected: this agent can use ALL installed skills. Select one to switch to an allowlist.',
            })}
          </span>
        </div>
      )}

      {/* ---------- Toolbar (§4.5.2) ---------- */}
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="relative min-w-0 max-w-[260px] flex-1">
          <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-muted">
            <SearchIcon size={16} />
          </span>
          <input
            type="search"
            data-testid="skills-search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => event.key === 'Escape' && setQuery('')}
            placeholder={t('settings.agentPage.skillsSearch', { defaultValue: 'Search skills' })}
            aria-label={t('settings.agentPage.skillsSearch', { defaultValue: 'Search skills' })}
            className="h-9 w-full rounded-md border border-border-strong bg-surface-tertiary pl-9 pr-8 text-sm text-text-primary placeholder:text-text-muted transition-colors duration-fast hover:border-border-light focus:border-interaction-primary focus:outline-none focus:ring-2 focus:ring-interaction-primary/20"
          />
          {query && (
            <button
              type="button"
              data-testid="skills-search-clear"
              aria-label={t('settings.agentPage.clearSearch', { defaultValue: 'Clear search' })}
              onClick={() => setQuery('')}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-text-tertiary transition-colors hover:text-text-primary"
            >
              <CloseIcon size={14} />
            </button>
          )}
        </div>

        <span
          data-testid="skills-count"
          className="ml-auto whitespace-nowrap text-xs text-text-tertiary"
        >
          {t('settings.agentPage.skillsCount', {
            defaultValue: '{{selected}} of {{total}} selected',
            selected: selectedInstalled,
            total: catalog.length,
          })}
        </span>

        <Button variant="ghost" size="sm" data-testid="skills-batch-none" onClick={setNone}>
          {t('settings.agentPage.skillNoneSelected', { defaultValue: 'None' })}
        </Button>
        <Button variant="ghost" size="sm" data-testid="skills-batch-all" onClick={setAll}>
          {t('settings.agentPage.skillAll', { defaultValue: 'All' })}
        </Button>

        {/* Workspace administration — writes immediately, no Save involved. */}
        <Button
          variant="secondary"
          size="sm"
          data-testid="skills-add"
          onClick={() => setInstallOpen(true)}
        >
          <PlusIcon size={14} />
          {t('settings.agentPage.skillsAdd', { defaultValue: 'Add skill' })}
        </Button>
      </div>

      {/* A failed REST write has nowhere else to surface: the config form's save
          banner is about unsaved edits, not about this panel's own actions. */}
      {skills.error && (
        <p
          data-testid="skills-action-error"
          role="alert"
          className="mb-3 rounded-lg border border-state-error/40 bg-state-error-light px-3 py-2 text-xs text-state-error"
        >
          {skills.error}
        </p>
      )}

      {/* ---------- Orphans: in agent.skills, not in the catalog (§4.5.4) ---------- */}
      {orphans.length > 0 && (
        <div data-testid="skills-orphans" className="mb-3 space-y-2">
          {orphans.map((name) => (
            <div
              key={name}
              data-testid={`skills-orphan-${name}`}
              className="flex items-center gap-2.5 rounded-lg border border-state-warning/40 bg-state-warning-light/30 p-3.5"
            >
              <span className="min-w-0 flex-1">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-mono text-sm font-medium text-text-primary">
                    {name}
                  </span>
                  <Badge variant="warning" size="sm">
                    {t('settings.agentPage.skillNotInstalled', { defaultValue: 'Not installed' })}
                  </Badge>
                </span>
                <span className="mt-0.5 block text-xs text-text-tertiary">
                  {t('settings.agentPage.skillsOrphanHint', {
                    defaultValue:
                      'In the allowlist but not installed — the agent cannot use it until it is installed again.',
                  })}
                </span>
              </span>
              <RemoveButton
                ariaLabel={t('settings.agentPage.skillsRemoveOrphan', {
                  defaultValue: 'Remove {{name}} from the allowlist',
                  name,
                })}
                onClick={() => removeOrphan(name)}
              />
            </div>
          ))}
        </div>
      )}

      {/* ---------- Grid (§4.5.3): md cards, 2 description lines, source badge ---------- */}
      {visibleCatalog.length === 0 ? (
        normalizedQuery ? (
          <p
            data-testid="skills-no-matches"
            className="py-6 text-center text-xs text-text-tertiary"
          >
            {t('settings.agentPage.skillsNoMatches', {
              defaultValue: 'No skills match "{{query}}"',
              query,
            })}
          </p>
        ) : null
      ) : (
        <div data-testid="skills-grid" className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          {visibleCatalog.map((entry) => {
            // Installed but globally disabled: dim + warning badge, STILL
            // selectable — the allowlist is per-agent, the global switch is
            // another page's concern (§4.5.3).
            const disabledGlobally = entry.enabled === false
            return (
              <div key={entry.name} className={disabledGlobally ? 'opacity-60' : undefined}>
                <ToggleCard
                  id={`skill-${entry.name}`}
                  size="md"
                  title={entry.name}
                  description={entry.description}
                  descriptionLines={2}
                  checked={selected.has(entry.name)}
                  onChange={(checked) => toggleSkill(entry.name, checked)}
                  badge={
                    <span className={sourceBadgeClassNames(entry.source)}>
                      {sourceBadgeLabel(entry.source)}
                    </span>
                  }
                  warning={
                    disabledGlobally
                      ? t('settings.agentPage.skillDisabledGlobally', {
                          defaultValue: 'Disabled globally',
                        })
                      : undefined
                  }
                  actions={
                    <SkillCardActions
                      name={entry.name}
                      enabled={entry.enabled}
                      deletable={entry.deletable}
                      pendingToggle={skills.isToggling === entry.name}
                      pendingRemove={skills.isRemoving === entry.name}
                      confirmingRemove={pendingRemove === entry.name}
                      onAskRemove={setPendingRemove}
                      onCancelRemove={() => setPendingRemove(null)}
                      onConfirmRemove={confirmRemove}
                      onToggle={(next) => void skills.toggle(entry.name, next)}
                    />
                  }
                />
              </div>
            )
          })}
        </div>
      )}

      {/* Backend validation errors for the array itself (§5.4). */}
      {fieldError && (
        <p data-testid="skills-error" className="mt-3 text-xs text-state-error">
          {fieldError}
        </p>
      )}

      {/* In-panel counterpart of the tab dot (§5.3). */}
      {sectionDirty && (
        <p data-testid="skills-dirty-hint" className="mt-3 text-[11px] text-text-tertiary">
          {t('settings.agentPage.sectionHasChanges', {
            defaultValue: 'Unsaved changes in this section',
          })}
        </p>
      )}

      {/* Install into this agent's workspace (or globally) — the dialog owns
          the scope choice and invalidates the catalog on success. */}
      {installDialog}
    </div>
  )
}
