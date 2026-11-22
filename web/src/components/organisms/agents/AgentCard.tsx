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

const REMOVE_CONFIRM_TIMEOUT_MS = 6000

type Props = {
  agent: EditableAgentConfig
  isModified: boolean
  defaultsModel: string
  onRemove: () => void
  confirmTimeoutMs?: number
}

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
    removeBtnRef.current?.querySelector('button')?.focus()
  }, [])

  useEffect(() => {
    if (!confirmingRemove) return
    const timer = setTimeout(() => setConfirmingRemove(false), confirmTimeoutMs)
    return () => clearTimeout(timer)
  }, [confirmingRemove, confirmTimeoutMs])

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

  useEffect(() => {
    if (confirmingRemove) confirmBtnRef.current?.focus()
  }, [confirmingRemove])

  const primary = getAgentModelPrimary(agent.model)
  const inherits = !primary && !!defaultsModel
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

  const openConfig = () => navigate(`/settings/agents/${encodeURIComponent(agent.id)}`)
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
          <div className="flex items-center gap-1.5">
            <Link
              to={`/settings/agents/${encodeURIComponent(agent.id)}`}
              aria-label={agent.id}
              title={agent.description || agent.id}
              className="truncate font-mono text-sm font-medium text-text-primary after:absolute after:inset-0 after:content-[''] hover:text-accent-primary"
            >
              {agent.id}
            </Link>

            <span className="ml-2 inline-flex flex-none items-center gap-1.5">
              {agent.default && (
                <span className="rounded-md bg-accent-subtle px-1.5 py-0.5 text-2xs font-medium text-accent-primary">
                  {t('settings.defaultBadge')}
                </span>
              )}
              {isModified && (
                <span className="rounded-md bg-state-info/10 px-1.5 py-0.5 text-2xs font-medium text-state-info">
                  {t('settings.modifiedBadge')}
                </span>
              )}
              {inherits && (
                <span className="hidden rounded-md border border-border bg-background-tertiary px-1.5 py-0.5 text-2xs font-medium text-text-tertiary sm:inline-flex">
                  {t('settings.agentPage.inheritsModel', { model: shortModelName(defaultsModel) })}
                </span>
              )}
            </span>
          </div>

          {displayName && (
            <div className="truncate text-base font-medium text-text-primary">{displayName}</div>
          )}
        </div>
      </div>

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

      <div className="relative z-10 mt-2.5 flex items-center gap-2 border-t border-border pt-2.5">
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
                <span className="ml-1.5 text-2xs text-text-muted">
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
