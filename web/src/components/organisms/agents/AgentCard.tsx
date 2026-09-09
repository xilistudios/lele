import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate } from 'react-router-dom'
import { getAgentModelPrimary } from '../../../hooks/useSettingsHelpers'
import { shortModelName } from '../../../lib/modelName'
import type { EditableAgentConfig } from '../../../lib/types'
import { AgentAvatar } from '../../atoms/AgentAvatar'
import { Button } from '../../atoms/Button'
import { IconButton } from '../../atoms/IconButton'
import { FolderIcon, SettingsIcon } from '../../atoms/Icons'
import { RemoveButton } from '../../atoms/RemoveButton'

/**
 * Master-list card for one agent (design spec §3.4–§3.5, §3.9).
 *
 * Anatomy: avatar (deterministic gradient) + id row with badges (default /
 * modified / inherits-model) + display name (omitted when equal to the id) +
 * a one-line meta summary (`model · skills · tools · subagents`) + divider +
 * an actions row (Configure / Files / Remove).
 *
 * The card is NOT a global link: the id anchors a stretched hit-area `Link`
 * to the agent config page while the actions sit above it (`relative z-10`).
 * Removal uses an INLINE confirmation that replaces the actions row (§3.5):
 * no modal, the card keeps its height, and the confirmation auto-cancels
 * after 6 s, on Escape, or on click-outside — preserving the confirm-before-
 * destroy semantics `NamedItemCard` had.
 *
 */

/** Auto-cancel window for the inline remove confirmation (§3.5). */
const REMOVE_CONFIRM_TIMEOUT_MS = 6000

type Props = {
  agent: EditableAgentConfig
  /** Any dirty path starts with `agents.list.{index}.` (spec §5.3) — the
   *  page computes this from the agent's position in the UNFILTERED list. */
  isModified: boolean
  /** `agents.defaults.model` — shown as inherited when the agent has none. */
  defaultsModel: string
  /** Remove this agent (the page filters it out of `agents.list`). */
  onRemove: () => void
  /** Override the auto-cancel window of the inline confirmation (tests). */
  confirmTimeoutMs?: number
}

/** One ` · ` separator between meta segments (decorative for a11y). */
function MetaDot() {
  return (
    <span aria-hidden="true" className="mx-1.5">
      ·
    </span>
  )
}

export function AgentCard({
  agent,
  isModified,
  defaultsModel,
  onRemove,
  confirmTimeoutMs = REMOVE_CONFIRM_TIMEOUT_MS,
}: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [confirmingRemove, setConfirmingRemove] = useState(false)

  const cardRef = useRef<HTMLDivElement>(null)
  const removeBtnRef = useRef<HTMLSpanElement>(null)
  const confirmBtnRef = useRef<HTMLButtonElement>(null)

  const cancelRemove = useCallback(() => {
    setConfirmingRemove(false)
    // Return focus to the ✕ that opened the confirmation (§6.6).
    removeBtnRef.current?.querySelector('button')?.focus()
  }, [])

  // Auto-cancel: an old click must never silently arm a delete (§3.5).
  useEffect(() => {
    if (!confirmingRemove) return
    const timer = setTimeout(() => setConfirmingRemove(false), confirmTimeoutMs)
    return () => clearTimeout(timer)
  }, [confirmingRemove, confirmTimeoutMs])

  // Escape or click-outside cancel the confirmation (§3.5).
  useEffect(() => {
    if (!confirmingRemove) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') cancelRemove()
    }
    const onPointerDown = (e: PointerEvent) => {
      if (!cardRef.current?.contains(e.target as Node)) cancelRemove()
    }
    document.addEventListener('keydown', onKeyDown)
    document.addEventListener('pointerdown', onPointerDown)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.removeEventListener('pointerdown', onPointerDown)
    }
  }, [confirmingRemove, cancelRemove])

  // When the confirmation opens, focus moves to "Remove" (§6.6).
  useEffect(() => {
    if (confirmingRemove) confirmBtnRef.current?.focus()
  }, [confirmingRemove])

  const primary = getAgentModelPrimary(agent.model)
  const inherits = !primary && !!defaultsModel
  // Inherited models are prefixed with `≈` so the summary never reads as an
  // explicit choice (§3.4.4).
  const modelLabel = primary
    ? shortModelName(primary)
    : inherits
      ? `≈ ${shortModelName(defaultsModel)}`
      : ''

  const skillCount = agent.skills?.length ?? 0
  const toolCount = agent.tools?.length ?? 0
  const allowedSubagents = agent.subagents?.allow_agents
  const subagentsOn = allowedSubagents !== undefined && allowedSubagents !== null

  const displayName = agent.name && agent.name !== agent.id ? agent.name : ''
  const confirmName = agent.name?.trim() || agent.id

  const openConfig = () => navigate(`/agents/${encodeURIComponent(agent.id)}`)
  const openFiles = () => navigate(`/settings/agent/${encodeURIComponent(agent.id)}`)

  return (
    <div
      ref={cardRef}
      data-testid="agent-card"
      className="relative rounded-lg border border-border bg-background-secondary p-4 shadow-card transition-colors duration-fast hover:border-border-strong"
    >
      <div className="flex gap-3">
        <AgentAvatar id={agent.id} name={agent.name} size="md" className="max-md:h-8 max-md:w-8" />

        <div className="min-w-0 flex-1">
          {/* Row 1 — id (stretched link) + badges */}
          <div className="flex items-center gap-1.5">
            <Link
              to={`/agents/${encodeURIComponent(agent.id)}`}
              aria-label={agent.id}
              title={agent.description || agent.id}
              className="truncate font-mono text-sm font-medium text-text-primary after:absolute after:inset-0 after:content-[''] hover:text-accent-primary"
            >
              {agent.id}
            </Link>

            <span className="ml-2 inline-flex flex-none items-center gap-1.5">
              {agent.default && (
                <span className="rounded-md bg-accent-subtle px-1.5 py-0.5 text-[10px] font-medium text-accent-primary">
                  {t('settings.defaultBadge')}
                </span>
              )}
              {isModified && (
                <span className="rounded-md bg-state-info-light px-1.5 py-0.5 text-[10px] font-medium text-state-info">
                  {t('settings.modifiedBadge')}
                </span>
              )}
              {inherits && (
                <span className="hidden rounded-md border border-border bg-surface-muted px-1.5 py-0.5 text-[10px] font-medium text-text-tertiary sm:inline-flex">
                  {t('settings.agentPage.inheritsModel', { model: shortModelName(defaultsModel) })}
                </span>
              )}
            </span>
          </div>

          {/* Row 2 — display name (omitted when it duplicates the id) */}
          {displayName && (
            <div className="truncate text-base font-medium text-text-primary">{displayName}</div>
          )}
        </div>
      </div>

      {/* Row 3 — meta summary: model · skills · tools · subagents */}
      <div
        data-testid="agent-card-meta"
        className="mt-2 truncate text-xs text-text-tertiary"
        title={`${modelLabel} · ${skillCount} skills · ${toolCount} tools`}
      >
        {modelLabel && <span>{modelLabel}</span>}
        {modelLabel && <MetaDot />}
        <span>
          {skillCount > 0
            ? t('settings.agentPage.metaSkills', { count: skillCount })
            : t('settings.agentPage.metaAllSkills')}
        </span>
        <MetaDot />
        <span>
          {toolCount > 0
            ? t('settings.agentPage.metaTools', { count: toolCount })
            : t('settings.agentPage.metaAllTools')}
        </span>
        <MetaDot />
        <span>
          {subagentsOn
            ? `${t('settings.agentPage.metaSubagentsOn')}${
                allowedSubagents.length > 0 ? ` (${allowedSubagents.length})` : ''
              }`
            : t('settings.agentPage.metaSubagentsOff')}
        </span>
      </div>

      {/* Divider + actions (or the inline remove confirmation) */}
      <div className="relative z-10 mt-2.5 flex items-center gap-2 border-t border-border-light pt-2.5">
        {confirmingRemove ? (
          <>
            {/* biome-ignore lint/a11y/useSemanticElements: <fieldset> fits form
                controls; this is an action row, so role="group" is correct. */}
            <div
              role="group"
              aria-label={t('settings.agentPage.removeConfirm', { name: confirmName })}
              aria-live="polite"
              className="flex w-full items-center gap-2"
            >
              <span className="flex-1 truncate text-xs text-text-secondary">
                {t('settings.agentPage.removeConfirm', { name: confirmName })}
                <span className="ml-1.5 text-[10px] text-text-muted">
                  {t('settings.agentPage.removeConfirmDesc')}
                </span>
              </span>
              <Button
                ref={confirmBtnRef}
                data-testid="agent-confirm-remove"
                variant="danger"
                size="sm"
                className="h-7"
                onClick={() => {
                  setConfirmingRemove(false)
                  onRemove()
                }}
              >
                {t('settings.agentPage.remove')}
              </Button>
              <Button
                data-testid="agent-cancel-remove"
                variant="ghost"
                size="sm"
                className="h-7"
                onClick={cancelRemove}
              >
                {t('common.cancel')}
              </Button>
            </div>
          </>
        ) : (
          <>
            <Button variant="secondary" size="sm" onClick={openConfig}>
              <SettingsIcon size={14} />
              {t('settings.agentPage.configure')}
            </Button>
            <span className="ml-auto flex items-center gap-2">
              <IconButton
                variant="ghost"
                ariaLabel={t('settings.agentPage.filesAria')}
                title={t('settings.agentPage.files')}
                onClick={openFiles}
                className="flex h-8 w-8 items-center justify-center max-md:h-9 max-md:w-9"
              >
                <FolderIcon size={16} />
              </IconButton>
              <span
                data-testid="agent-remove-btn"
                ref={removeBtnRef}
                className="flex h-8 w-8 items-center justify-center rounded transition-colors hover:bg-surface-hover max-md:h-9 max-md:w-9"
              >
                <RemoveButton
                  ariaLabel={t('settings.removeAgent')}
                  onClick={() => setConfirmingRemove(true)}
                />
              </span>
            </span>
          </>
        )}
      </div>
    </div>
  )
}
