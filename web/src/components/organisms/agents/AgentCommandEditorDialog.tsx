import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import { useAgentCommandDetail } from '../../../hooks/useAgentCommands'
import { useAvailableModels } from '../../../hooks/useAvailableModels'
import {
  COMMAND_NAME_MAX_LEN,
  type CommandFields,
  commandNameError,
  parseCommandMarkdown,
  serializeCommandMarkdown,
} from '../../../lib/commandMarkdown'
import type {
  AgentCommandInfo,
  AgentCommandSource,
  AgentCommandWriteRequest,
} from '../../../lib/types'
import { Button } from '../../atoms/Button'
import { Modal } from '../../atoms/Modal'
import { SegmentedControl } from '../../molecules/SegmentedControl'
import { SettingsField } from '../../molecules/SettingsField'

type TriStateOption = 'inherit' | 'yes' | 'no'

export type CommandEditorTarget = {
  mode: 'create' | 'edit' | 'view'
  name: string
  source?: AgentCommandSource
  row?: AgentCommandInfo
}

type Props = {
  agentId: string
  target: CommandEditorTarget | null
  onClose: () => void
  workspaceCommandsDir: string
  agentOptions: string[]
  onCreate: (body: AgentCommandWriteRequest) => Promise<unknown>
  onUpdate: (name: string, content: string) => Promise<unknown>
  onSaved: (name: string) => void
}

type FormState = {
  name: string
  description: string
  agent: string
  model: string
  allowShell: TriStateOption
  allowAbsolute: TriStateOption
  template: string
}

function triState(value: boolean | null | undefined): TriStateOption {
  if (value === true) return 'yes'
  if (value === false) return 'no'
  return 'inherit'
}

function initialState(target: CommandEditorTarget | null): FormState {
  const row = target?.row
  if (target && target.mode !== 'create' && row) {
    return {
      description: row.description ?? '',
      agent: row.agent ?? '',
      model: row.model ?? '',
      allowShell: row.allow_shell ? 'yes' : 'no',
      allowAbsolute: triState(row.allow_absolute_files),
      name: target.name,
      template: '',
    }
  }
  return {
    name: '',
    description: '',
    agent: '',
    model: '',
    allowShell: 'no',
    allowAbsolute: 'inherit',
    template: '',
  }
}

function ServerError({ message }: { message: string }) {
  const { t } = useTranslation()
  return (
    <p
      role="alert"
      data-testid="command-editor-error"
      className="rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
    >
      {t('settings.agentPage.commands.editor.serverError')}
      <span className="mt-0.5 block font-mono text-xs break-all">{message}</span>
    </p>
  )
}

export function AgentCommandEditorDialog({
  agentId,
  target,
  onClose,
  workspaceCommandsDir,
  agentOptions,
  onCreate,
  onUpdate,
  onSaved,
}: Props) {
  const { t, api } = useSettings()
  const isOpen = target !== null
  const mode = target?.mode ?? 'create'
  const readOnly = mode === 'view'

  const [form, setForm] = useState<FormState>(() => initialState(target))
  const [serverError, setServerError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const appliedKeyRef = useRef<string | null>(null)

  const { available: models } = useAvailableModels(api)

  const openKey = isOpen ? `${target?.mode}:${target?.name}` : ''
  useEffect(() => {
    if (!target) return
    setForm(initialState(target))
    setServerError(null)
    appliedKeyRef.current = null
  }, [target])

  const detailName = isOpen && mode === 'edit' ? (target?.name ?? null) : null
  const detail = useAgentCommandDetail(api, agentId, detailName)

  useEffect(() => {
    if (!detail.data || !detailName || !openKey) return
    if (appliedKeyRef.current === openKey) return
    appliedKeyRef.current = openKey
    const parsed = parseCommandMarkdown(detail.data.content)
    setForm((previous) => ({
      ...previous,
      description: parsed.fields.description,
      agent: parsed.fields.agent,
      model: parsed.fields.model,
      allowShell: parsed.fields.allow_shell ? 'yes' : 'no',
      allowAbsolute: triState(parsed.fields.allow_absolute_files),
      template: parsed.body,
    }))
  }, [detail.data, detailName, openKey])

  useEffect(() => {
    if (detail.isError && detailName && appliedKeyRef.current !== openKey) {
      setServerError(t('settings.agentPage.commands.editor.loadError'))
    }
  }, [detail.isError, detailName, openKey, t])

  const nameError = (() => {
    if (mode !== 'create' || form.name.trim().length === 0) return null
    switch (commandNameError(form.name)) {
      case 'tooLong':
        return t('settings.agentPage.commands.editor.nameTooLong', { max: COMMAND_NAME_MAX_LEN })
      case 'reservedExt':
        return t('settings.agentPage.commands.editor.nameReservedExt')
      case 'invalid':
        return t('settings.agentPage.commands.editor.nameInvalid')
      default:
        return null
    }
  })()

  const canSave =
    !saving &&
    !readOnly &&
    !detail.isLoading &&
    (mode !== 'create' || (form.name.trim().length > 0 && commandNameError(form.name) === null)) &&
    form.description.trim().length > 0 &&
    form.template.trim().length > 0

  const fields: CommandFields = {
    description: form.description.trim(),
    agent: form.agent.trim(),
    model: form.model.trim(),
    allow_shell: form.allowShell === 'yes',
    allow_absolute_files: form.allowAbsolute === 'inherit' ? null : form.allowAbsolute === 'yes',
  }
  const content = serializeCommandMarkdown(fields, form.template)

  const handleSave = async () => {
    if (!target || !canSave) return
    setSaving(true)
    setServerError(null)
    try {
      if (mode === 'create') {
        await onCreate({ name: form.name, content, scope: 'workspace' })
      } else {
        await onUpdate(target.name, content)
      }
      onSaved(mode === 'create' ? form.name : target.name)
      onClose()
    } catch (error) {
      setServerError((error as Error).message || String(error))
    } finally {
      setSaving(false)
    }
  }

  const title =
    mode === 'create'
      ? t('settings.agentPage.commands.new')
      : `/${target?.name ?? ''}${readOnly ? ` · ${t('settings.agentPage.commands.editor.readOnly')}` : ''}`

  return (
    <Modal isOpen={isOpen} onClose={onClose} title={title} size="lg">
      {!target ? null : (
        <div className="flex flex-col gap-4 p-6">
          {readOnly && (
            <p
              data-testid="command-editor-readonly"
              className="rounded-lg border border-state-warning/40 bg-state-warning/10 px-3 py-2 text-xs text-text-secondary"
            >
              {t('settings.agentPage.commands.editor.readOnlyHint', {
                source: target.source ?? '',
              })}
            </p>
          )}

          {detail.isLoading && (
            <p data-testid="command-editor-loading" className="text-xs text-text-tertiary">
              {t('settings.agentPage.commands.loading')}
            </p>
          )}

          {serverError && <ServerError message={serverError} />}

          {mode === 'create' && (
            <SettingsField
              label={t('settings.agentPage.commands.editor.name')}
              path="command-name"
              required
              description={t('settings.agentPage.commands.editor.nameHint')}
              error={nameError ?? undefined}
            >
              <input
                id="command-name"
                data-testid="command-editor-name"
                type="text"
                value={form.name}
                onChange={(event) => setForm((p) => ({ ...p, name: event.target.value }))}
                placeholder="review"
                spellCheck={false}
                autoComplete="off"
                className="w-full rounded-md border border-border-strong bg-background-secondary px-3 py-2 font-mono text-sm text-text-primary placeholder:text-text-muted focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20"
              />
            </SettingsField>
          )}

          <SettingsField
            label={t('settings.agentPage.commands.editor.description')}
            path="command-description"
            required
          >
            <input
              id="command-description"
              data-testid="command-editor-description"
              type="text"
              value={form.description}
              disabled={readOnly}
              onChange={(event) => setForm((p) => ({ ...p, description: event.target.value }))}
              className="w-full rounded-md border border-border-strong bg-background-secondary px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20 disabled:opacity-40"
            />
          </SettingsField>

          <SettingsField
            label={t('settings.agentPage.commands.editor.template')}
            path="command-template"
            required
            description={t('settings.agentPage.commands.editor.templateHint')}
          >
            <textarea
              id="command-template"
              data-testid="command-editor-template"
              value={form.template}
              readOnly={readOnly}
              rows={8}
              spellCheck={false}
              onChange={(event) => setForm((p) => ({ ...p, template: event.target.value }))}
              className="w-full resize-y rounded-md border border-border-strong bg-background-secondary px-3 py-2 font-mono text-xs leading-5 text-text-primary placeholder:text-text-muted focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20 read-only:opacity-60"
            />
          </SettingsField>

          <div
            data-testid="command-editor-help"
            className="rounded-lg border border-border bg-background-secondary/60 p-3 text-xs text-text-secondary"
          >
            <p className="mb-2 font-medium text-text-primary">
              {t('settings.agentPage.commands.editor.helpTitle')}
            </p>
            <ul className="space-y-1">
              {[
                ['$ARGUMENTS', t('settings.agentPage.commands.editor.helpArgs')],
                ['$1…$9', t('settings.agentPage.commands.editor.helpPositional')],
                ['@ruta', t('settings.agentPage.commands.editor.helpFile')],
                ['!`cmd`', t('settings.agentPage.commands.editor.helpShell')],
              ].map(([token, meaning]) => (
                <li key={token} className="flex items-baseline gap-2">
                  <code className="rounded bg-background-tertiary px-1.5 py-0.5 font-mono text-xs text-text-primary">
                    {token}
                  </code>
                  <span>{meaning}</span>
                </li>
              ))}
            </ul>
            <p className="mt-2 text-xs text-text-tertiary">
              {t('settings.agentPage.commands.editor.helpUnknownKeys')}
            </p>
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <SettingsField
              label={t('settings.agentPage.commands.editor.agent')}
              path="command-agent"
              description={t('settings.agentPage.commands.editor.optionalHint')}
            >
              <select
                id="command-agent"
                data-testid="command-editor-agent"
                value={form.agent}
                disabled={readOnly}
                onChange={(event) => setForm((p) => ({ ...p, agent: event.target.value }))}
                className="w-full rounded-md border border-border-strong bg-background-secondary px-3 py-2 text-sm text-text-primary focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20 disabled:opacity-40"
              >
                <option value="">{t('settings.agentPage.commands.editor.agentNone')}</option>
                {agentOptions.map((id) => (
                  <option key={id} value={id}>
                    {id}
                  </option>
                ))}
                {form.agent && !agentOptions.includes(form.agent) && (
                  <option value={form.agent}>{form.agent}</option>
                )}
              </select>
            </SettingsField>

            <SettingsField
              label={t('settings.agentPage.commands.editor.model')}
              path="command-model"
              description={t('settings.agentPage.commands.editor.optionalHint')}
            >
              <input
                id="command-model"
                data-testid="command-editor-model"
                type="text"
                list="command-model-options"
                value={form.model}
                disabled={readOnly}
                placeholder={t('settings.agentPage.commands.editor.modelPlaceholder')}
                onChange={(event) => setForm((p) => ({ ...p, model: event.target.value }))}
                className="w-full rounded-md border border-border-strong bg-background-secondary px-3 py-2 font-mono text-sm text-text-primary placeholder:text-text-muted focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20 disabled:opacity-40"
              />
              <datalist id="command-model-options">
                {models.map((model) => (
                  <option key={model} value={model} />
                ))}
              </datalist>
            </SettingsField>
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <SettingsField
              label={t('settings.agentPage.commands.editor.allowShell')}
              path="command-allow-shell"
              description={t('settings.agentPage.commands.editor.allowShellHint')}
            >
              <SegmentedControl<'yes' | 'no'>
                id="command-allow-shell"
                size="sm"
                value={form.allowShell === 'yes' ? 'yes' : 'no'}
                disabled={readOnly}
                options={[
                  { value: 'no', label: t('common.no') },
                  { value: 'yes', label: t('common.yes') },
                ]}
                onChange={(value) => setForm((p) => ({ ...p, allowShell: value }))}
              />
            </SettingsField>

            <SettingsField
              label={t('settings.agentPage.commands.editor.allowAbsoluteFiles')}
              path="command-allow-absolute"
              description={t('settings.agentPage.commands.editor.allowAbsoluteHint')}
            >
              <SegmentedControl<TriStateOption>
                id="command-allow-absolute"
                size="sm"
                value={form.allowAbsolute}
                disabled={readOnly}
                options={[
                  { value: 'inherit', label: t('settings.agentPage.commands.editor.inherit') },
                  { value: 'yes', label: t('common.yes') },
                  { value: 'no', label: t('common.no') },
                ]}
                onChange={(value) => setForm((p) => ({ ...p, allowAbsolute: value }))}
              />
            </SettingsField>
          </div>

          {mode === 'create' && (
            <div
              data-testid="command-editor-destination"
              className="flex flex-col gap-1 rounded-lg border border-border bg-background-secondary/60 p-3"
            >
              <span className="text-xs font-medium text-text-secondary">
                {t('settings.agentPage.commands.editor.destinationTitle')}
              </span>
              <span className="truncate font-mono text-xs text-text-tertiary">
                {workspaceCommandsDir || t('settings.agentPage.commands.editor.destinationUnknown')}
              </span>
              <span className="text-xs text-text-tertiary">
                {t('settings.agentPage.commands.editor.destinationHint')}
              </span>
            </div>
          )}

          <div className="flex items-center justify-end gap-2 pt-2">
            <Button variant="ghost" size="md" onClick={onClose}>
              {readOnly ? t('common.close') : t('common.cancel', { defaultValue: 'Cancel' })}
            </Button>
            {!readOnly && (
              <Button
                variant="primary"
                size="md"
                type="button"
                data-testid="command-editor-save"
                disabled={!canSave}
                loading={saving}
                onClick={() => void handleSave()}
              >
                {mode === 'create'
                  ? t('settings.agentPage.commands.editor.create')
                  : t('common.save')}
              </Button>
            )}
          </div>
        </div>
      )}
    </Modal>
  )
}
