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
  agent: EditableAgentConfig
  index: number
  agentId: string
}

type CatalogSkill = {
  name: string
  description: string
  source: SkillSource
  enabled: boolean
  deletable: boolean
}

function byName(a: { name: string }, b: { name: string }): number {
  return a.name.localeCompare(b.name)
}

function byString(a: string, b: string): number {
  return a.localeCompare(b)
}

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
        className="flex items-center gap-1.5 text-xs"
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

  const skills = useAgentSkills(api, { agentId })
  const [installOpen, setInstallOpen] = useState(false)
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
  const workspacePath = data?.workspace ?? ''

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

  const selectedInstalled = catalog.reduce((n, e) => (selected.has(e.name) ? n + 1 : n), 0)
  const allowlistActive = allowlist.length > 0

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

  const setNone = () => writeSkills([])
  const setAll = () => writeSkills(catalog.map((entry) => entry.name))

  const confirmRemove = (name: string) => {
    setPendingRemove(null)
    void skills.remove(name)
  }

  const sectionDirty = isSectionDirty(dirtyPaths, index, 'skills')

  const installDialog = (
    <AgentSkillInstallDialog
      agentId={agentId}
      workspacePath={workspacePath}
      isOpen={installOpen}
      onClose={() => setInstallOpen(false)}
    />
  )

  if (isLoading) {
    return (
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

  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-surface-sunken px-4 py-8 text-center">
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

  if (catalog.length === 0 && orphans.length === 0) {
    return (
      <>
        <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center">
          <p className="text-sm text-text-secondary">
            {t('settings.agentPage.skillsNoneInstalled', {
              defaultValue: 'No skills installed in this workspace yet.',
            })}
          </p>
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
            <Link
              to={`/skills?view=agent&agent=${agentId}`}
              className="text-sm text-focus hover:underline"
            >
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
          className="mb-4 flex items-start gap-2.5 rounded-lg border border-state-info/30 bg-state-info/10 px-4 py-3 text-xs"
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
            className="h-9 w-full rounded-md border border-border-strong bg-background-secondary pl-9 pr-8 text-sm text-text-primary placeholder:text-text-muted transition-colors duration-fast hover:border-border focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20"
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

      {skills.error && (
        <p
          data-testid="skills-action-error"
          role="alert"
          className="mb-3 rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
        >
          {skills.error}
        </p>
      )}

      {orphans.length > 0 && (
        <div data-testid="skills-orphans" className="mb-3 space-y-2">
          {orphans.map((name) => (
            <div
              key={name}
              data-testid={`skills-orphan-${name}`}
              className="flex items-center gap-2.5 rounded-lg border border-state-warning/40 bg-state-warning/10 p-3.5"
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

      {fieldError && (
        <p data-testid="skills-error" className="mt-3 text-xs text-state-error">
          {fieldError}
        </p>
      )}

      {sectionDirty && (
        <p data-testid="skills-dirty-hint" className="mt-3 text-xs text-text-tertiary">
          {t('settings.agentPage.sectionHasChanges', {
            defaultValue: 'Unsaved changes in this section',
          })}
        </p>
      )}

      {installDialog}
    </div>
  )
}
