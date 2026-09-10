import { useCallback, useState } from 'react'
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

/**
 * `tab=commands` — the slash commands THIS agent sees (brief decision 9).
 *
 * One source of truth: `GET /api/v1/agents/{id}/commands`. The backend
 * flattens the four harness discovery levels (config → global → workspace →
 * directory), decides `deletable`, and tags the losers of a name collision
 * with `shadowed_by` — the client never recomputes any of that.
 *
 * Layout, top to bottom:
 * 1. header — the commands dir (`<code>` + copy) with a chip while the folder
 *    does not exist yet, plus the "New command" CTA;
 * 2. shared-workspace banner (`shared_by > 1`): writes here affect N agents;
 * 3. the table: name | description | source | agent | model | shell | actions;
 * 4. the global harness permissions (informative — they live in config.json,
 *    there is no per-agent field; plan D7/L2);
 * 5. built-in gateway commands, collapsed.
 *
 * Editor: `AgentCommandEditorDialog` (create/edit/view). Delete is confirmed
 * inline in the row (same pattern as the skills cards): a delete is the one
 * action here that a discarded form cannot undo.
 *
 * Like `files`, this section never writes config → it can never be dirty
 * (agentDirty: SECTION_PATHS.commands = []).
 */

type Props = {
  /** Unused by the reads; kept for the page contract (and future defaults). */
  agent: EditableAgentConfig
  /** Real id from the draft (not the URL param), so casing cannot drift. */
  agentId: string
}

/** How long the copy button and the delete toast show their confirmation. */
const FEEDBACK_MS = 1500

/** §5.1 geometry: one card skeleton, same shape as the files tab. */
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

/** Copy button next to a path, mirroring the agent-ID field (§ general tab). */
function PathCopy({ value, testId }: { value: string; testId: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      // Clipboard is unavailable over plain http: never throw, the <code> is
      // select-all so copying by hand still works.
    }
    setCopied(true)
    setTimeout(() => setCopied(false), FEEDBACK_MS)
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

/** One row of the table. Actions live in the row itself (not a subcomponent)
 *  so the test ids stay flat: command-row-<name> wraps everything. */
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
      // A shadowed file has no effect for THIS agent: dim it, keep it readable.
      className={shadowed ? 'opacity-60' : undefined}
    >
      <td className="px-3 py-2 align-top">
        <span className="font-mono text-xs text-text-primary">/{command.name}</span>
        {shadowed && (
          <span className="ml-2 whitespace-nowrap text-[10px] text-text-tertiary">
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
      <td className="px-3 py-2 align-top font-mono text-[11px] text-text-secondary">
        {command.agent || '—'}
      </td>
      <td className="px-3 py-2 align-top font-mono text-[11px] text-text-secondary">
        {command.model || '—'}
      </td>
      <td className="px-3 py-2 align-top text-center">
        {/* allow_shell is a plain bool (OR-merged with the global default);
            absolute files is tri-state and belongs to the editor, not to a
            narrow column. */}
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
            /* Reads and writes are addressed BY NAME and the server resolves
               the name to the level that wins precedence: acting from a
               shadowed row would edit or delete a DIFFERENT file than the one
               this row describes. So a hidden command has no actions — the
               winner row is the only place to touch that name. */
            <span
              data-testid={`command-shadowed-${command.name}`}
              className="text-[11px] text-text-tertiary"
            >
              {t('settings.agentPage.commands.shadowedNoActions')}
            </span>
          ) : confirming ? (
            <span
              data-testid={`command-remove-confirm-${command.name}`}
              className="flex items-center gap-1.5 text-[11px]"
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
              {/* Edit ALWAYS opens the dialog; the dialog decides (from
                  source) whether it is editable or read-only — a config or
                  directory command cannot be fetched at all. */}
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

  /** null = closed. One dialog instance for the whole panel. */
  const [target, setTarget] = useState<CommandEditorTarget | null>(null)
  /** Row awaiting a delete confirmation (inline, like the skills cards). */
  const [pendingRemove, setPendingRemove] = useState<string | null>(null)
  /** Transient success notice after a delete (creates/edits close the dialog). */
  const [notice, setNotice] = useState<string | null>(null)

  // The dialog owns its create/update errors (its own banner); the section
  // only passes the mutations through so the whole commands API stays in one
  // place. Delete happens inline in the rows → its error is the section's.
  const submitCreate = useCallback(
    (body: AgentCommandWriteRequest) => create.mutateAsync(body),
    [create],
  )
  const submitUpdate = useCallback(
    (name: string, content: string) => update.mutateAsync({ name, content }),
    [update],
  )

  // A failed REST write has nowhere else to surface: this tab never touches
  // the config draft, so the page's save banner is about something else. Only
  // the delete happens inline in the row — create/update errors belong to the
  // dialog's own banner, which is open while they can occur.
  const actionError = remove.error

  const showNotice = (message: string) => {
    setNotice(message)
    setTimeout(() => setNotice(null), FEEDBACK_MS)
  }

  const confirmRemove = (name: string) => {
    setPendingRemove(null)
    remove.mutate(name, {
      onSuccess: () => showNotice(t('settings.agentPage.commands.removed', { name })),
    })
  }

  // Error FIRST (files-tab rule): retry must not swap the banner for a fake
  // skeleton while the query refetches.
  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-background-secondary/50 px-4 py-8 text-center">
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
    // config/directory files are neither readable nor writable through this
    // API (403 not_editable) → open in view mode WITHOUT fetching.
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

  // ---------- Empty: no command at any level (still offer creation) ----------
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
        {/* ---------- header: destination dir + chip + CTA ---------- */}
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

        {/* ---------- shared workspace: writes here affect several agents ---------- */}
        {response && response.shared_by > 1 && (
          <div
            data-testid="commands-shared-banner"
            className="flex items-start gap-2.5 rounded-lg border border-state-info/30 bg-state-info-light px-4 py-3 text-xs"
          >
            <span className="text-text-secondary">
              {t('settings.agentPage.commands.sharedWorkspace', { count: response.shared_by })}
            </span>
            <code className="select-all truncate font-mono text-[11px] text-text-tertiary">
              {response.workspace}
            </code>
          </div>
        )}

        {actionError && (
          <p
            data-testid="commands-action-error"
            role="alert"
            className="rounded-lg border border-state-error/40 bg-state-error-light px-3 py-2 text-xs text-state-error"
          >
            {(actionError as Error).message}
          </p>
        )}

        {notice && (
          /* aria-live (not role="status"): the same polite announcement for a
             transient toast, without the semantic-element lint fighting the
             multi-attribute JSX the rule flags. */
          <p
            data-testid="commands-notice"
            aria-live="polite"
            className="rounded-lg border border-state-success/40 bg-state-success-light px-3 py-2 text-xs text-state-success"
          >
            {notice}
          </p>
        )}

        {/* ---------- the table ---------- */}
        <div className="overflow-x-auto" data-testid="commands-list">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-[11px] uppercase tracking-wide text-text-tertiary">
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

        {/* ---------- global harness permissions (informative) ---------- */}
        {response && (
          <div
            data-testid="commands-harness"
            className="rounded-lg border border-border bg-background-secondary/60 p-3 text-xs text-text-secondary"
          >
            <p className="mb-1 font-medium text-text-primary">
              {t('settings.agentPage.commands.harnessTitle')}
            </p>
            <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <code className="rounded bg-background-tertiary px-1.5 py-0.5 font-mono text-[11px] text-text-primary">
                harness.allow_shell
              </code>
              <span>
                {response.harness.allow_shell ? t('common.enabled') : t('common.disabled')}
              </span>
              <span aria-hidden="true">·</span>
              <code className="rounded bg-background-tertiary px-1.5 py-0.5 font-mono text-[11px] text-text-primary">
                harness.allow_absolute_files
              </code>
              <span>
                {response.harness.allow_absolute_files ? t('common.enabled') : t('common.disabled')}
              </span>
            </p>
            <p className="mt-1 text-[11px] text-text-tertiary">
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

/**
 * Built-in gateway commands (`/new`, `/model`, …), collapsed by default: they
 * belong to the dispatcher, not to this agent, and cannot be edited here.
 */
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
            <code className="rounded bg-background-tertiary px-1.5 py-0.5 font-mono text-[11px] text-text-primary">
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
