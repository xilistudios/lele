import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import { getErrorForPath, isDirtyPath } from '../../../hooks/useSettingsHelpers'
import { expandHomeDisplay } from '../../../lib/paths'
import type { EditableAgentConfig } from '../../../lib/types'
import { IconButton } from '../../atoms/IconButton'
import { CheckIcon, CopyIcon } from '../../atoms/Icons'
import { BooleanInput } from '../../molecules/BooleanInput'
import { SettingsField } from '../../molecules/SettingsField'
import { SettingsSection } from '../../molecules/SettingsSection'
import { TextInput } from '../../molecules/TextInput'

/**
 * `tab=general` — identity fields (spec §4.3).
 *
 * Writes go straight to the shared draft owned by `AgentEntityLayout`, under
 * `agents.list.{index}.…`; the global footer is what persists them. `id` is
 * read-only on purpose (§4.3): renaming an agent means editing config.json by
 * hand, so the UI only offers "select" and "copy".
 */

/** How long the copy button shows its "Copied" confirmation. */
const COPIED_FEEDBACK_MS = 1500

type Props = {
  agent: EditableAgentConfig
  /** Position in `agents.list` — dirty/validation paths are positional. */
  index: number
  /** The whole list, only to warn about several defaults (§4.3). */
  agents: EditableAgentConfig[]
}

export function AgentGeneralSection({ agent, index, agents }: Props) {
  const { t } = useTranslation()
  const { dirtyPaths, validationErrors, updateField } = useSettings()
  const [copied, setCopied] = useState(false)
  const copiedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Clear the "Copied" feedback timer if the component unmounts mid-toast.
  useEffect(() => {
    return () => {
      if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current)
    }
  }, [])

  const path = (field: string) => `agents.list.${index}.${field}`
  const dirty = (field: string) => isDirtyPath(dirtyPaths, path(field))
  const error = (field: string) => getErrorForPath(validationErrors, path(field))

  const copyId = async () => {
    try {
      await navigator.clipboard.writeText(agent.id)
    } catch {
      // Clipboard is unavailable over plain http / without focus. The field is
      // `select-all`, so the user can still copy by hand: never throw here.
    }
    setCopied(true)
    if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current)
    copiedTimeoutRef.current = setTimeout(() => setCopied(false), COPIED_FEEDBACK_MS)
  }

  const moreThanOneDefault = agents.filter((candidate) => candidate.default).length > 1

  return (
    <SettingsSection title={t('settings.agentPage.tab.general')}>
      {/* ID — read-only, select-all, copy button */}
      <SettingsField
        label="ID"
        path={path('id')}
        description={t('settings.agentPage.idLocked')}
        error={error('id')}
      >
        <div className="flex items-center gap-2">
          <div
            role="textbox"
            aria-readonly="true"
            aria-label="ID"
            data-testid="agent-id-field"
            className="h-9 flex-1 select-all overflow-hidden rounded-md border border-border bg-background-tertiary px-3 font-mono text-sm leading-9 text-text-secondary"
            title={agent.id}
          >
            {agent.id}
          </div>
          <IconButton
            variant="ghost"
            ariaLabel={copied ? t('settings.agentPage.copied') : t('settings.agentPage.copyId')}
            title={t('settings.agentPage.copyId')}
            onClick={copyId}
          >
            {copied ? <CheckIcon size={14} /> : <CopyIcon size={14} />}
          </IconButton>
        </div>
      </SettingsField>

      <SettingsField
        label={t('settings.fields.agentName')}
        path={path('name')}
        isDirty={dirty('name')}
        error={error('name')}
      >
        <TextInput
          id={path('name')}
          value={agent.name ?? ''}
          onChange={(value) => updateField(path('name'), value || undefined)}
          placeholder={agent.id}
        />
      </SettingsField>

      <SettingsField
        label={t('settings.fields.agentDescription')}
        path={path('description')}
        isDirty={dirty('description')}
        error={error('description')}
      >
        <TextInput
          id={path('description')}
          value={agent.description ?? ''}
          onChange={(value) => updateField(path('description'), value || undefined)}
          placeholder={t('settings.agentPage.descriptionPlaceholder', {
            defaultValue: 'What is this agent for?',
          })}
        />
      </SettingsField>

      <SettingsField
        label={t('settings.fields.agentDefault')}
        path={path('default')}
        isDirty={dirty('default')}
        error={error('default')}
      >
        <BooleanInput
          id={path('default')}
          value={agent.default ?? false}
          onChange={(value) => updateField(path('default'), value || undefined)}
        />
        {/* The backend resolves duplicates by order, so the UI only warns
            instead of silently flipping the others off (§4.3). */}
        {moreThanOneDefault && (
          <p className="mt-1 text-xs text-state-warning" data-testid="multiple-defaults-warning">
            {t('settings.agentPage.multipleDefaults')}
          </p>
        )}
      </SettingsField>

      <SettingsField
        label={t('settings.fields.agentWorkspace')}
        path={path('workspace')}
        description={t('settings.agentPage.workspaceHint')}
        isDirty={dirty('workspace')}
        error={error('workspace')}
      >
        <TextInput
          id={path('workspace')}
          value={agent.workspace ?? ''}
          onChange={(value) => updateField(path('workspace'), value || undefined)}
          placeholder="~/.lele/workspace"
        />
        {/* Presentation only: the real expansion happens server-side. */}
        {agent.workspace?.includes('~') && (
          <p data-testid="workspace-preview" className="mt-1 font-mono text-[11px] text-text-muted">
            {expandHomeDisplay(agent.workspace)}
          </p>
        )}
      </SettingsField>
    </SettingsSection>
  )
}
