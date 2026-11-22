import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import { useAgentCommands } from '../../../hooks/useAgentCommands'
import { sourceBadgeClasses, sourceBadgeLabel } from '../../../lib/skillSource'
import type {
  AgentCommandInfo,
  AgentCommandWriteRequest,
  AgentCommandsResponse,
  EditableAgentConfig,
} from '../../../lib/types'
import { Badge } from '../../atoms/Badge'
import { Button } from '../../atoms/Button'
import { IconButton } from '../../atoms/IconButton'
import { CheckIcon, CopyIcon, EditIcon, PlusIcon, TrashIcon } from '../../atoms/Icons'
import { SettingsSection } from '../../molecules/SettingsSection'
import { AgentCommandEditorDialog, type CommandEditorTarget } from './AgentCommandEditorDialog'

type Props = {
  agent: EditableAgentConfig
  agentId: string
}

const FEEDBACK_MS = 1500

function CommandsSkeleton() {
  const { t } = useTranslation()
  return (
    // biome-ignore lint/a11y/useSemanticElements: spec §5.1 requires a status live region around the skeleton; <output> is form-only and would not announce a load
    <div role="status" aria-busy="true" data-testid="commands-loading" className="space-y-4">
      <span className="sr-only">{t('common.loading')}</span>
      <div className="rounded-lg border border-border bg-background-primary p-4 md:p-6">
        <div className="animate-pulse">
          <div className="h-4 w-1/4 rounded-md bg-background-tertiary" />
          <div className="mt-4 h-9 w-full rounded-md bg-background-tertiary" />
          <div className="mt-4 h-9 w-full rounded-md bg-background-tertiary" />
        </div>
      </div>
    </div>
  )
}

function PathCopy({ value, testId }: { value: string; testId: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const copiedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current)
    }
  }, [])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
    } catch {}
    setCopied(true)
    if (copiedTimeoutRef.current) clearTimeout(copiedTimeoutRef.current)
    copiedTimeoutRef.current = setTimeout(() => setCopied(false), FEEDBACK_MS)
  }
  return (
    <IconButton
      variant="ghost"
      dataTestId={testId}
      ariaLabel={copied ? t('settings.agentPage.copied') : t('settings.agentPage.commands.copyDir')}
      title={t('settings.agentPage.commands.copyDir')}
      onClick={() => void copy()}
    >
      {copied ? <CheckIcon size={14} /> : <CopyIcon size={14} />}
    </IconButton>
  )
}

function CommandRow({
  command,
  confirming,
  deleting,
  onEdit,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  command: AgentCommandInfo
  confirming: boolean
  deleting: boolean
  onEdit: () => void
  onAskDelete: () => void
  onCancelDelete: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation()
  const shadowed = command.shadowed_by !== ''
  const rowTitle = shadowed
    ? t('settings.agentPage.commands.shadowedBy', { source: command.shadowed_by })
    : undefined

  return (
    <tr
      data-testid={`command-row-${command.name}`}
      title={rowTitle}
      className={shadowed ? 'opacity-60' : undefined}
    >
      <td className="px-3 py-2 align-top">
        <span className="font-mono text-xs text-text-primary">/{command.name}</span>
        {shadowed && (
          <span className="ml-2 whitespace-nowrap text-2xs text-text-tertiary">
            {t('settings.agentPage.commands.shadowedBy', { source: command.shadowed_by })}
          </span>
        )}
      </td>
      <td className="max-w-72 px-3 py-2 align-top text-xs text-text-secondary">
        <span className="line-clamp-2" title={command.description}>
          {command.description}
        </span>
      </td>
      <td className="px-3 py-2 align-top">
        <Badge
          variant="default"
          size="sm"
          className={`border ${sourceBadgeClasses(command.source)}`}
        >
          {sourceBadgeLabel(command.source)}
        </Badge>
      </td>
      <td className="px-3 py-2 align-top font-mono text-xs text-text-secondary">
        {command.agent || '—'}
      </td>
      <td className="px-3 py-2 align-top font-mono text-xs text-text-secondary">
        {command.model || '—'}
      </td>
      <td className="px-3 py-2 align-top text-center">
        {command.allow_shell ? (
          <CheckIcon size={14} />
        ) : (
          <span className="text-text-muted" aria-hidden="true">
            –
          </span>
        )}
      </td>
      <td className="px-3 py-2 align-top">
        <div className="flex items-center justify-end gap-1">
          {shadowed ? (
            <span
              data-testid={`command-shadowed-${command.name}`}
              className="text-xs text-text-tertiary"
            >
              {t('settings.agentPage.commands.shadowedNoActions')}
            </span>
          ) : confirming ? (
            <span
              data-testid={`command-remove-confirm-${command.name}`}
              className="flex items-center gap-1.5 text-xs"
            >
              <span className="text-text-secondary">
                {t('settings.agentPage.commands.confirmRemove')}
              </span>
              <Button
                variant="danger"
                size="sm"
                data-testid={`command-remove-yes-${command.name}`}
                disabled={deleting}
                onClick={onDelete}
              >
                {t('common.delete')}
              </Button>
              <Button variant="ghost" size="sm" onClick={onCancelDelete}>
                {t('common.cancel')}
              </Button>
            </span>
          ) : (
            <>
              <IconButton
                dataTestId={`command-edit-${command.name}`}
                title={t('settings.agentPage.commands.edit')}
                ariaLabel={t('settings.agentPage.commands.editAria', { name: command.name })}
                onClick={onEdit}
              >
                <EditIcon size={14} />
              </IconButton>
              <IconButton
                dataTestId={`command-remove-${command.name}`}
                variant="danger"
                title={
                  command.deletable
                    ? t('settings.agentPage.commands.remove')
                    : t('settings.agentPage.commands.notDeletable')
                }
                ariaLabel={t('settings.agentPage.commands.removeAria', { name: command.name })}
                disabled={!command.deletable || deleting}
                onClick={onAskDelete}
              >
                <TrashIcon size={14} />
              </IconButton>
            </>
          )}
        </div>
      </td>
    </tr>
  )
}

export function AgentCommandsSection({ agentId }: Props) {
  const { t, api, draftConfig } = useSettings()
  const { data, isLoading, isError, refetch, commands, builtin, create, update, remove } =
    useAgentCommands(api, { agentId })

  const [target, setTarget] = useState<CommandEditorTarget | null>(null)
  const [pendingRemove, setPendingRemove] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const noticeTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (noticeTimeoutRef.current) clearTimeout(noticeTimeoutRef.current)
    }
  }, [])

  const submitCreate = useCallback(
    (body: AgentCommandWriteRequest) => create.mutateAsync(body),
    [create],
  )
  const submitUpdate = useCallback(
    (name: string, content: string) => update.mutateAsync({ name, content }),
    [update],
  )

  const actionError = remove.error

  const showNotice = (message: string) => {
    setNotice(message)
    if (noticeTimeoutRef.current) clearTimeout(noticeTimeoutRef.current)
    noticeTimeoutRef.current = setTimeout(() => setNotice(null), FEEDBACK_MS)
  }

  const confirmRemove = (name: string) => {
    setPendingRemove(null)
    remove.mutate(name, {
      onSuccess: () => showNotice(t('settings.agentPage.commands.removed', { name })),
    })
  }

  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-surface-sunken px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">{t('settings.agentPage.commands.loadError')}</p>
        <Button
          variant="secondary"
          size="sm"
          data-testid="commands-retry"
          onClick={() => refetch()}
        >
          {t('settings.agentPage.commands.retry')}
        </Button>
      </div>
    )
  }

  if (isLoading) return <CommandsSkeleton />

  const response: AgentCommandsResponse | undefined = data
  const agentOptions = (draftConfig?.agents?.list ?? []).map((entry) => entry.id)

  const openEditor = (command: AgentCommandInfo) => {
    const viewOnly = command.source === 'config' || command.source === 'directory'
    setTarget({
      mode: viewOnly ? 'view' : 'edit',
      name: command.name,
      source: command.source,
      row: command,
    })
  }

  const editor = (
    <AgentCommandEditorDialog
      agentId={agentId}
      target={target}
      onClose={() => setTarget(null)}
      workspaceCommandsDir={response?.commands_dir ?? ''}
      agentOptions={agentOptions}
      onCreate={submitCreate}
      onUpdate={submitUpdate}
      onSaved={(name) => showNotice(t('settings.agentPage.commands.saved', { name }))}
    />
  )

  if (commands.length === 0) {
    return (
      <div className="space-y-6">
        <SettingsSection
          title={t('settings.agentPage.commands.title')}
          description={
            response?.commands_dir
              ? t('settings.agentPage.commands.dirHint', { dir: response.commands_dir })
              : undefined
          }
        >
          <div
            className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center"
            data-testid="commands-empty"
          >
            <p className="text-sm text-text-secondary">
              {t('settings.agentPage.commands.emptyTitle')}
            </p>
            <p className="max-w-md text-xs text-text-tertiary">
              {t('settings.agentPage.commands.emptyHint')}
            </p>
            <Button
              variant="secondary"
              size="sm"
              data-testid="commands-new"
              onClick={() => setTarget({ mode: 'create', name: '' })}
            >
              <PlusIcon size={14} />
              {t('settings.agentPage.commands.new')}
            </Button>
          </div>
        </SettingsSection>
        <BuiltinBlock builtin={builtin} />
        {editor}
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <SettingsSection
        title={t('settings.agentPage.commands.title')}
        description={
          response?.commands_dir
            ? t('settings.agentPage.commands.dirHint', { dir: response.commands_dir })
            : undefined
        }
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <code
              data-testid="commands-dir"
              title={response?.commands_dir}
              className="select-all truncate rounded bg-background-tertiary px-2 py-1 font-mono text-xs text-text-primary"
            >
              {response?.commands_dir ?? ''}
            </code>
            {response && !response.commands_dir_exists && (
              <Badge
                variant="warning"
                size="sm"
                data-testid="commands-dir-missing"
                className="whitespace-nowrap"
              >
                {t('settings.agentPage.commands.noDirYet')}
              </Badge>
            )}
            {response?.commands_dir && (
              <PathCopy value={response.commands_dir} testId="commands-copy-dir" />
            )}
          </div>
          <Button
            variant="secondary"
            size="sm"
            data-testid="commands-new"
            onClick={() => setTarget({ mode: 'create', name: '' })}
          >
            <PlusIcon size={14} />
            {t('settings.agentPage.commands.new')}
          </Button>
        </div>

        {response && response.shared_by > 1 && (
          <div
            data-testid="commands-shared-banner"
            className="flex items-start gap-2.5 rounded-lg border border-state-info/30 bg-state-info/10 px-4 py-3 text-xs"
          >
            <span className="text-text-secondary">
              {t('settings.agentPage.commands.sharedWorkspace', { count: response.shared_by })}
            </span>
            <code className="select-all truncate font-mono text-xs text-text-tertiary">
              {response.workspace}
            </code>
          </div>
        )}

        {actionError && (
          <p
            data-testid="commands-action-error"
            role="alert"
            className="rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
          >
            {(actionError as Error).message}
          </p>
        )}

        {notice && (
          <p
            data-testid="commands-notice"
            aria-live="polite"
            className="rounded-lg border border-state-success/40 bg-state-success/10 px-3 py-2 text-xs text-state-success"
          >
            {notice}
          </p>
        )}

        <div className="overflow-x-auto" data-testid="commands-list">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-text-tertiary">
                <th scope="col" className="px-3 py-2 font-medium">
                  {t('settings.agentPage.commands.colName')}
                </th>
                <th scope="col" className="px-3 py-2 font-medium">
                  {t('settings.agentPage.commands.colDescription')}
                </th>
                <th scope="col" className="px-3 py-2 font-medium">
                  {t('settings.agentPage.commands.colSource')}
                </th>
                <th scope="col" className="px-3 py-2 font-medium">
                  {t('settings.agentPage.commands.colAgent')}
                </th>
                <th scope="col" className="px-3 py-2 font-medium">
                  {t('settings.agentPage.commands.colModel')}
                </th>
                <th scope="col" className="px-3 py-2 text-center font-medium">
                  {t('settings.agentPage.commands.colShell')}
                </th>
                <th scope="col" className="px-3 py-2 text-right font-medium">
                  {t('settings.agentPage.commands.colActions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {commands.map((command) => (
                <CommandRow
                  key={`${command.source}:${command.name}`}
                  command={command}
                  confirming={pendingRemove === command.name}
                  deleting={remove.isPending && remove.variables === command.name}
                  onEdit={() => openEditor(command)}
                  onAskDelete={() => setPendingRemove(command.name)}
                  onCancelDelete={() => setPendingRemove(null)}
                  onDelete={() => confirmRemove(command.name)}
                />
              ))}
            </tbody>
          </table>
        </div>

        {response && (
          <div
            data-testid="commands-harness"
            className="rounded-lg border border-border bg-background-secondary/60 p-3 text-xs text-text-secondary"
          >
            <p className="mb-1 font-medium text-text-primary">
              {t('settings.agentPage.commands.harnessTitle')}
            </p>
            <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <code className="rounded bg-background-tertiary px-1.5 py-0.5 font-mono text-xs text-text-primary">
                harness.allow_shell
              </code>
              <span>
                {response.harness.allow_shell ? t('common.enabled') : t('common.disabled')}
              </span>
              <span aria-hidden="true">·</span>
              <code className="rounded bg-background-tertiary px-1.5 py-0.5 font-mono text-xs text-text-primary">
                harness.allow_absolute_files
              </code>
              <span>
                {response.harness.allow_absolute_files ? t('common.enabled') : t('common.disabled')}
              </span>
            </p>
            <p className="mt-1 text-xs text-text-tertiary">
              {t('settings.agentPage.commands.harnessHint')}
            </p>
          </div>
        )}
      </SettingsSection>

      <BuiltinBlock builtin={builtin} />
      {editor}
    </div>
  )
}

function BuiltinBlock({ builtin }: { builtin: AgentCommandsResponse['builtin'] }) {
  const { t } = useTranslation()
  if (builtin.length === 0) return null
  return (
    <details
      data-testid="commands-builtin"
      className="rounded-lg border border-border bg-background-primary p-4"
    >
      <summary className="cursor-pointer text-sm font-medium text-text-primary">
        {t('settings.agentPage.commands.builtinTitle', { count: builtin.length })}
      </summary>
      <p className="mt-2 text-xs text-text-tertiary">
        {t('settings.agentPage.commands.builtinNote')}
      </p>
      <ul className="mt-3 space-y-1.5">
        {builtin.map((command) => (
          <li key={command.name} className="flex flex-wrap items-baseline gap-2 text-xs">
            <code className="rounded bg-background-tertiary px-1.5 py-0.5 font-mono text-xs text-text-primary">
              {command.usage || `/${command.name}`}
            </code>
            <span className="font-medium text-text-primary">{command.name}</span>
            <span className="min-w-0 flex-1 text-text-secondary">{command.description}</span>
          </li>
        ))}
      </ul>
    </details>
  )
}
