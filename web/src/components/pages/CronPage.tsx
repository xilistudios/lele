import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useAvailableModels } from '../../hooks/useAvailableModels'
import { useCronJobs } from '../../hooks/useCronJobs'
import type { Agent, CronJob, CronJobInput, CronSchedule } from '../../lib/types'
import { Sidebar } from '../organisms/Sidebar'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatTimestamp(ms?: number | null): string {
  if (!ms) return '—'
  const d = new Date(ms)
  return d.toLocaleString()
}

function formatRelative(ms?: number | null): string {
  if (!ms) return '—'
  const diff = ms - Date.now()
  const abs = Math.abs(diff)
  const sec = Math.round(abs / 1000)
  const min = Math.round(sec / 60)
  const hr = Math.round(min / 60)
  const day = Math.round(hr / 24)
  let label: string
  if (sec < 60) label = `${sec}s`
  else if (min < 60) label = `${min}m`
  else if (hr < 24) label = `${hr}h`
  else label = `${day}d`
  return diff >= 0 ? `in ${label}` : `${label} ago`
}

function scheduleLabel(schedule: CronSchedule): string {
  switch (schedule.kind) {
    case 'at':
      return `once @ ${formatTimestamp(schedule.atMs)}`
    case 'every':
      return `every ${Math.round((schedule.everyMs ?? 0) / 1000)}s`
    case 'cron':
      return schedule.expr || 'cron'
    default:
      return schedule.kind || 'unknown'
  }
}

function StatusBadge({ job }: { job: CronJob }) {
  const { t } = useTranslation()
  if (!job.enabled) {
    return (
      <span className="inline-flex items-center rounded-full border border-gray-500/30 bg-gray-500/20 px-2 py-0.5 text-xs font-medium text-gray-400">
        {t('cron.disabled', 'disabled')}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center rounded-full border border-emerald-500/30 bg-emerald-500/20 px-2 py-0.5 text-xs font-medium text-emerald-400">
      <span className="mr-1.5 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-400" />
      {t('cron.enabled', 'enabled')}
    </span>
  )
}

function LastRunBadge({ job }: { job: CronJob }) {
  const status = job.state.lastStatus
  if (!status) return null
  const color =
    status === 'ok'
      ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400'
      : 'border-red-500/30 bg-red-500/10 text-red-400'
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${color}`}
      title={job.state.lastError || undefined}
    >
      {status === 'ok' ? '✓' : '✗'} {status}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Job card
// ---------------------------------------------------------------------------

function JobCard({
  job,
  expanded,
  onToggle,
  onEnableToggle,
  onRun,
  onDelete,
  onEdit,
  busy,
}: {
  job: CronJob
  expanded: boolean
  onToggle: () => void
  onEnableToggle: () => void
  onRun: () => void
  onDelete: () => void
  onEdit: () => void
  busy: boolean
}) {
  const { t } = useTranslation()
  const [confirmDelete, setConfirmDelete] = useState(false)

  return (
    <div
      className={`rounded-xl border transition-colors ${
        expanded
          ? 'border-interaction-primary/40 bg-background-secondary'
          : 'border-border bg-background-secondary/50 hover:bg-background-secondary'
      } ${job.enabled ? '' : 'opacity-70'}`}
    >
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center gap-4 px-4 py-3 text-left"
      >
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-medium text-text-primary">
              {job.name || job.id}
            </span>
            <StatusBadge job={job} />
            <LastRunBadge job={job} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-text-tertiary">
            <span className="font-mono">{scheduleLabel(job.schedule)}</span>
            <span>
              {t('cron.nextRun', 'next')}: {formatRelative(job.state.nextRunAtMs)}
            </span>
            <span className="font-mono text-text-tertiary/60">{job.id.slice(0, 8)}</span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              onRun()
            }}
            disabled={busy}
            title={t('cron.runNow', 'Run now')}
            className="rounded-lg border border-interaction-primary/30 bg-interaction-primary/10 px-3 py-1 text-xs font-medium text-interaction-primary transition-colors hover:bg-interaction-primary/20 disabled:opacity-50"
          >
            {t('cron.run', 'Run')}
          </button>
          <svg
            className={`h-4 w-4 text-text-tertiary transition-transform ${expanded ? 'rotate-180' : ''}`}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <title>{expanded ? t('cron.collapse', 'Collapse') : t('cron.expand', 'Expand')}</title>
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </div>
      </button>

      {expanded && (
        <div className="space-y-3 border-t border-border px-4 pb-4 pt-3">
          <div className="grid grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
            <Detail label={t('cron.message', 'Message')} value={job.payload.message} />
            {job.payload.command && (
              <Detail label={t('cron.command', 'Command')} value={job.payload.command} mono />
            )}
            <Detail
              label={t('cron.deliver', 'Deliver')}
              value={job.payload.deliver ? 'yes' : 'no'}
            />
            <Detail
              label={t('cron.channel', 'Channel')}
              value={job.payload.channel ? `${job.payload.channel} → ${job.payload.to}` : '—'}
            />
            <Detail label={t('cron.created', 'Created')} value={formatTimestamp(job.createdAtMs)} />
            <Detail label={t('cron.updated', 'Updated')} value={formatTimestamp(job.updatedAtMs)} />
            <Detail
              label={t('cron.lastRun', 'Last run')}
              value={formatTimestamp(job.state.lastRunAtMs)}
            />
            <Detail
              label={t('cron.nextRun', 'Next run')}
              value={formatTimestamp(job.state.nextRunAtMs)}
            />
            {job.payload.spawn && (
              <>
                <Detail
                  label={t('cron.spawn', 'Spawn')}
                  value={job.payload.spawn.label || job.payload.spawn.task}
                />
                <Detail
                  label={t('cron.spawnAgent', 'Agent')}
                  value={job.payload.spawn.agent_id || t('cron.spawnAgentDefault', 'Default agent')}
                />
                {job.payload.spawn.model && (
                  <Detail label={t('cron.spawnModel', 'Model')} value={job.payload.spawn.model} />
                )}
                <Detail
                  label={t('cron.spawnTask', 'Task instructions')}
                  value={job.payload.spawn.task}
                />
                {job.payload.spawn.guidance && (
                  <Detail
                    label={t('cron.spawnGuidance', 'Additional guidance')}
                    value={job.payload.spawn.guidance}
                  />
                )}
              </>
            )}
          </div>
          {job.state.lastError && (
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-2 text-xs text-red-400">
              {job.state.lastError}
            </div>
          )}

          <div className="flex flex-wrap items-center gap-2 pt-1">
            <button
              type="button"
              onClick={onEnableToggle}
              disabled={busy}
              className="rounded-lg border border-border bg-background-secondary px-3 py-1.5 text-xs font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-50"
            >
              {job.enabled ? t('cron.disable', 'Disable') : t('cron.enable', 'Enable')}
            </button>
            <button
              type="button"
              onClick={onEdit}
              disabled={busy}
              className="rounded-lg border border-border bg-background-secondary px-3 py-1.5 text-xs font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-50"
            >
              {t('common.edit', 'Edit')}
            </button>
            {confirmDelete ? (
              <span className="flex items-center gap-2">
                <span className="text-xs text-red-400">{t('cron.confirmDelete', 'Delete?')}</span>
                <button
                  type="button"
                  onClick={onDelete}
                  disabled={busy}
                  className="rounded-lg border border-red-500/40 bg-red-500/20 px-3 py-1.5 text-xs font-medium text-red-300 transition-colors hover:bg-red-500/30 disabled:opacity-50"
                >
                  {t('common.yes', 'Yes')}
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmDelete(false)}
                  className="rounded-lg border border-border bg-background-secondary px-3 py-1.5 text-xs font-medium text-text-secondary transition-colors hover:bg-surface-hover"
                >
                  {t('common.no', 'No')}
                </button>
              </span>
            ) : (
              <button
                type="button"
                onClick={() => setConfirmDelete(true)}
                className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1.5 text-xs font-medium text-red-400 transition-colors hover:bg-red-500/20"
              >
                {t('common.delete', 'Delete')}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function Detail({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-text-tertiary">{label}</span>
      <span className={`break-all text-text-secondary ${mono ? 'font-mono' : ''}`}>
        {value || '—'}
      </span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Create / edit form
// ---------------------------------------------------------------------------

type ScheduleKind = 'at' | 'every' | 'cron'
type ActionKind = 'message' | 'command' | 'spawn'

function JobFormModal({
  initial,
  agents,
  onClose,
  onSubmit,
  busy,
}: {
  initial: CronJob | null
  agents: Agent[]
  onClose: () => void
  onSubmit: (input: CronJobInput) => Promise<void>
  busy: boolean
}) {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { groups: modelGroups, isLoading: isLoadingModels } = useAvailableModels(api)
  const [form, setForm] = useState(() => {
    const actionType: ActionKind = initial?.payload.spawn
      ? 'spawn'
      : initial?.payload.command
        ? 'command'
        : 'message'
    if (initial) {
      const kind = (initial.schedule.kind as ScheduleKind) || 'every'
      return {
        name: initial.name,
        actionType,
        message: initial.payload.message,
        command: initial.payload.command ?? '',
        spawnTask: initial.payload.spawn?.task ?? '',
        spawnAgent: initial.payload.spawn?.agent_id ?? '',
        spawnModel: initial.payload.spawn?.model ?? '',
        spawnLabel: initial.payload.spawn?.label ?? '',
        spawnGuidance: initial.payload.spawn?.guidance ?? '',
        deliver: initial.payload.deliver,
        channel: initial.payload.channel ?? '',
        to: initial.payload.to ?? '',
        scheduleKind: kind,
        atDatetime:
          kind === 'at' && initial.schedule.atMs
            ? new Date(initial.schedule.atMs - new Date().getTimezoneOffset() * 60000)
                .toISOString()
                .slice(0, 16)
            : '',
        everySeconds:
          kind === 'every' && initial.schedule.everyMs
            ? String(Math.round(initial.schedule.everyMs / 1000))
            : '3600',
        cronExpr: kind === 'cron' ? (initial.schedule.expr ?? '') : '',
      }
    }
    return {
      name: '',
      actionType: 'message' as ActionKind,
      message: '',
      command: '',
      spawnTask: '',
      spawnAgent: '',
      spawnModel: '',
      spawnLabel: '',
      spawnGuidance: '',
      deliver: true,
      channel: '',
      to: '',
      scheduleKind: 'every' as ScheduleKind,
      atDatetime: '',
      everySeconds: '3600',
      cronExpr: '',
    }
  })
  const [error, setError] = useState<string | null>(null)

  const buildInput = (): CronJobInput | null => {
    const schedule: CronSchedule = { kind: form.scheduleKind }
    if (form.scheduleKind === 'at') {
      if (!form.atDatetime) {
        setError(t('cron.errorDatetime', 'Select a date and time'))
        return null
      }
      schedule.atMs = new Date(form.atDatetime).getTime()
    } else if (form.scheduleKind === 'every') {
      const secs = Number.parseInt(form.everySeconds, 10)
      if (!secs || secs <= 0) {
        setError(t('cron.errorInterval', 'Interval must be a positive number of seconds'))
        return null
      }
      schedule.everyMs = secs * 1000
    } else if (form.scheduleKind === 'cron') {
      if (!form.cronExpr.trim()) {
        setError(t('cron.errorCron', 'Enter a cron expression'))
        return null
      }
      schedule.expr = form.cronExpr.trim()
    }

    const base: CronJobInput = {
      name: form.name.trim() || undefined,
      channel: form.channel.trim() || undefined,
      to: form.to.trim() || undefined,
      schedule,
    }

    if (form.actionType === 'spawn') {
      const task = form.spawnTask.trim()
      if (!task) {
        setError(t('cron.errorSpawnTask', 'Task instructions are required'))
        return null
      }
      return {
        ...base,
        message: null,
        command: null,
        deliver: false,
        spawn: {
          task,
          agent_id: form.spawnAgent || undefined,
          model: form.spawnModel.trim() || undefined,
          label: form.spawnLabel.trim() || undefined,
          guidance: form.spawnGuidance.trim() || undefined,
        },
      }
    }

    if (form.actionType === 'command') {
      const command = form.command.trim()
      if (!command) {
        setError(t('cron.errorCommand', 'Command is required'))
        return null
      }
      return {
        ...base,
        message: null,
        command,
        deliver: false,
        spawn: null,
      }
    }

    const message = form.message.trim()
    if (!message) {
      setError(t('cron.errorMessage', 'Message is required'))
      return null
    }
    return {
      ...base,
      message,
      command: null,
      deliver: form.deliver,
      spawn: null,
    }
  }

  const handleSubmit = async () => {
    setError(null)
    const input = buildInput()
    if (!input) return
    try {
      await onSubmit(input)
      onClose()
    } catch (err) {
      setError((err as Error).message)
    }
  }

  const inputCls =
    'w-full rounded-lg border border-border bg-background-primary px-3 py-2 text-sm text-text-primary outline-none focus:border-interaction-primary/50'
  const labelCls = 'mb-1 block text-xs font-medium text-text-secondary'

  const actionButtons: { kind: ActionKind; label: string }[] = [
    { kind: 'message', label: t('cron.actionMessage', 'Message') },
    { kind: 'command', label: t('cron.actionCommand', 'Command') },
    { kind: 'spawn', label: t('cron.actionSpawn', 'Spawn agent') },
  ]

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onClick={onClose}
      onKeyDown={(e) => e.key === 'Escape' && onClose()}
      aria-hidden
    >
      <dialog
        open
        className="max-h-[90vh] w-full max-w-lg overflow-y-auto rounded-2xl border border-border bg-background-secondary p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
        aria-modal="true"
        aria-label={initial ? t('cron.editJob', 'Edit Job') : t('cron.newJob', 'New Job')}
      >
        <h2 className="mb-4 text-base font-semibold text-text-primary">
          {initial ? t('cron.editJob', 'Edit Job') : t('cron.newJob', 'New Job')}
        </h2>

        <div className="space-y-4">
          <div>
            <label className={labelCls} htmlFor="cron-name">
              {t('cron.name', 'Name')}
            </label>
            <input
              id="cron-name"
              className={inputCls}
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder={t('cron.namePlaceholder', 'Optional label')}
            />
          </div>

          <div>
            <span className={labelCls}>{t('cron.action', 'Action')}</span>
            <div className="mb-2 flex gap-2">
              {actionButtons.map(({ kind, label }) => (
                <button
                  key={kind}
                  type="button"
                  onClick={() => setForm((f) => ({ ...f, actionType: kind }))}
                  className={`rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors ${
                    form.actionType === kind
                      ? 'border-interaction-primary/50 bg-interaction-primary/20 text-interaction-primary'
                      : 'border-border bg-background-primary text-text-secondary hover:bg-surface-hover'
                  }`}
                >
                  {label}
                </button>
              ))}
            </div>

            {form.actionType === 'message' && (
              <div>
                <label className={labelCls} htmlFor="cron-message">
                  {t('cron.message', 'Message')}
                </label>
                <textarea
                  id="cron-message"
                  className={`${inputCls} min-h-[70px] resize-y`}
                  value={form.message}
                  onChange={(e) => setForm((f) => ({ ...f, message: e.target.value }))}
                  placeholder={t('cron.messagePlaceholder', 'What should happen when this runs?')}
                />
              </div>
            )}

            {form.actionType === 'command' && (
              <div>
                <label className={labelCls} htmlFor="cron-command">
                  {t('cron.command', 'Command')}
                </label>
                <input
                  id="cron-command"
                  className={`${inputCls} font-mono`}
                  value={form.command}
                  onChange={(e) => setForm((f) => ({ ...f, command: e.target.value }))}
                  placeholder="df -h"
                />
              </div>
            )}

            {form.actionType === 'spawn' && (
              <div className="space-y-3">
                <div>
                  <label className={labelCls} htmlFor="cron-spawn-agent">
                    {t('cron.spawnAgent', 'Agent')}
                  </label>
                  <select
                    id="cron-spawn-agent"
                    className={inputCls}
                    value={form.spawnAgent}
                    onChange={(e) => setForm((f) => ({ ...f, spawnAgent: e.target.value }))}
                  >
                    <option value="">{t('cron.spawnAgentDefault', 'Default agent')}</option>
                    {agents.map((agent) => (
                      <option key={agent.id} value={agent.id}>
                        {agent.name || agent.id}
                        {agent.default ? ` (${t('cron.default', 'default')})` : ''}
                      </option>
                    ))}
                  </select>
                </div>
                <div>
                  <label className={labelCls} htmlFor="cron-spawn-model">
                    {t('cron.spawnModel', 'Model')} ({t('common.optional', 'optional')})
                  </label>
                  <select
                    id="cron-spawn-model"
                    className={inputCls}
                    value={form.spawnModel}
                    onChange={(e) => setForm((f) => ({ ...f, spawnModel: e.target.value }))}
                    disabled={isLoadingModels}
                  >
                    <option value="">
                      {isLoadingModels
                        ? t('cron.spawnModelLoading', 'Loading models…')
                        : t('cron.spawnModelDefault', "Agent's default model")}
                    </option>
                    {form.spawnModel &&
                      !modelGroups.some((g) =>
                        g.models.some((m) => m.value === form.spawnModel),
                      ) && <option value={form.spawnModel}>{form.spawnModel}</option>}
                    {modelGroups.map((group) => (
                      <optgroup key={group.provider} label={group.provider}>
                        {group.models.map((m) => (
                          <option key={m.value} value={m.value}>
                            {m.label}
                          </option>
                        ))}
                      </optgroup>
                    ))}
                  </select>
                </div>
                <div>
                  <label className={labelCls} htmlFor="cron-spawn-task">
                    {t('cron.spawnTask', 'Task instructions')}
                  </label>
                  <textarea
                    id="cron-spawn-task"
                    className={`${inputCls} min-h-[70px] resize-y`}
                    value={form.spawnTask}
                    onChange={(e) => setForm((f) => ({ ...f, spawnTask: e.target.value }))}
                    placeholder={t(
                      'cron.spawnTaskPlaceholder',
                      'What should the agent do when this runs?',
                    )}
                  />
                </div>
                <div>
                  <label className={labelCls} htmlFor="cron-spawn-label">
                    {t('cron.spawnLabel', 'Label')} ({t('common.optional', 'optional')})
                  </label>
                  <input
                    id="cron-spawn-label"
                    className={inputCls}
                    value={form.spawnLabel}
                    onChange={(e) => setForm((f) => ({ ...f, spawnLabel: e.target.value }))}
                    placeholder={t('cron.spawnLabelPlaceholder', 'Short label for the subagent')}
                  />
                </div>
                <div>
                  <label className={labelCls} htmlFor="cron-spawn-guidance">
                    {t('cron.spawnGuidance', 'Additional guidance')} (
                    {t('common.optional', 'optional')})
                  </label>
                  <textarea
                    id="cron-spawn-guidance"
                    className={`${inputCls} min-h-[50px] resize-y`}
                    value={form.spawnGuidance}
                    onChange={(e) => setForm((f) => ({ ...f, spawnGuidance: e.target.value }))}
                    placeholder={t(
                      'cron.spawnGuidancePlaceholder',
                      'Extra instructions or constraints',
                    )}
                  />
                </div>
              </div>
            )}
          </div>

          <div>
            <span className={labelCls}>{t('cron.schedule', 'Schedule')}</span>
            <div className="mb-2 flex gap-2">
              {(['at', 'every', 'cron'] as ScheduleKind[]).map((k) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => setForm((f) => ({ ...f, scheduleKind: k }))}
                  className={`rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors ${
                    form.scheduleKind === k
                      ? 'border-interaction-primary/50 bg-interaction-primary/20 text-interaction-primary'
                      : 'border-border bg-background-primary text-text-secondary hover:bg-surface-hover'
                  }`}
                >
                  {k === 'at'
                    ? t('cron.once', 'One-time')
                    : k === 'every'
                      ? t('cron.interval', 'Interval')
                      : t('cron.cronExpr', 'Cron')}
                </button>
              ))}
            </div>

            {form.scheduleKind === 'at' && (
              <input
                type="datetime-local"
                className={inputCls}
                value={form.atDatetime}
                onChange={(e) => setForm((f) => ({ ...f, atDatetime: e.target.value }))}
              />
            )}
            {form.scheduleKind === 'every' && (
              <div className="flex items-center gap-2">
                <input
                  type="number"
                  min="1"
                  className={inputCls}
                  value={form.everySeconds}
                  onChange={(e) => setForm((f) => ({ ...f, everySeconds: e.target.value }))}
                />
                <span className="text-xs text-text-tertiary">{t('cron.seconds', 'seconds')}</span>
              </div>
            )}
            {form.scheduleKind === 'cron' && (
              <input
                className={`${inputCls} font-mono`}
                value={form.cronExpr}
                onChange={(e) => setForm((f) => ({ ...f, cronExpr: e.target.value }))}
                placeholder="0 9 * * *"
              />
            )}
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={labelCls} htmlFor="cron-channel">
                {t('cron.channel', 'Channel')} ({t('common.optional', 'optional')})
              </label>
              <input
                id="cron-channel"
                className={inputCls}
                value={form.channel}
                onChange={(e) => setForm((f) => ({ ...f, channel: e.target.value }))}
                placeholder="telegram"
              />
            </div>
            <div>
              <label className={labelCls} htmlFor="cron-to">
                {t('cron.to', 'To')} ({t('common.optional', 'optional')})
              </label>
              <input
                id="cron-to"
                className={inputCls}
                value={form.to}
                onChange={(e) => setForm((f) => ({ ...f, to: e.target.value }))}
                placeholder="chat id"
              />
            </div>
          </div>

          {form.actionType === 'message' && (
            <label className="flex items-center gap-2 text-sm text-text-secondary">
              <input
                type="checkbox"
                checked={form.deliver}
                onChange={(e) => setForm((f) => ({ ...f, deliver: e.target.checked }))}
                className="h-4 w-4 rounded border-border"
              />
              {t('cron.deliverHint', 'Deliver message directly to channel')}
            </label>
          )}

          {error && (
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-2 text-xs text-red-400">
              {error}
            </div>
          )}
        </div>

        <div className="mt-6 flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg border border-border bg-background-secondary px-4 py-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover"
          >
            {t('common.cancel', 'Cancel')}
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            disabled={busy}
            className="rounded-lg bg-interaction-primary px-4 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-50"
          >
            {initial ? t('common.save', 'Save') : t('common.create', 'Create')}
          </button>
        </div>
      </dialog>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export function CronPage() {
  const { t } = useTranslation()
  const { sidebarOpen, onToggleSidebar, agents } = useAppLogicContext()
  const { jobs, status, loading, refresh, toggleEnabled, removeJob, runJob, createJob, updateJob } =
    useCronJobs()
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<CronJob | null>(null)
  const [formBusy, setFormBusy] = useState(false)

  const sortedJobs = useMemo(() => {
    return [...jobs].sort((a, b) => {
      if (a.enabled !== b.enabled) return a.enabled ? -1 : 1
      const an = a.state.nextRunAtMs ?? Number.MAX_SAFE_INTEGER
      const bn = b.state.nextRunAtMs ?? Number.MAX_SAFE_INTEGER
      return an - bn
    })
  }, [jobs])

  const handleToggle = useCallback((id: string) => {
    setExpandedId((prev) => (prev === id ? null : id))
  }, [])

  const withBusy = useCallback(
    (id: string, fn: () => Promise<void>) => async () => {
      setBusyId(id)
      try {
        await fn()
      } finally {
        setBusyId(null)
      }
    },
    [],
  )

  const openCreate = useCallback(() => {
    setEditing(null)
    setFormOpen(true)
  }, [])

  const openEdit = useCallback((job: CronJob) => {
    setEditing(job)
    setFormOpen(true)
  }, [])

  const handleFormSubmit = useCallback(
    async (input: CronJobInput) => {
      setFormBusy(true)
      try {
        if (editing) {
          await updateJob(editing.id, input)
        } else {
          await createJob(input)
        }
      } finally {
        setFormBusy(false)
      }
    },
    [editing, createJob, updateJob],
  )

  return (
    <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        <header className="flex shrink-0 items-center justify-between border-b border-border bg-background-secondary/30 px-6 py-4">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onToggleSidebar()}
              className="flex rounded-lg p-1.5 text-text-tertiary transition-colors hover:bg-background-tertiary hover:text-text-primary md:hidden"
              title={t('chat.toggleSidebar')}
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <title>Toggle sidebar</title>
                <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
                <line x1="9" y1="3" x2="9" y2="21" />
              </svg>
            </button>
            <h1 className="text-base font-semibold text-text-primary">
              {t('cron.title', 'Scheduled Jobs')}
            </h1>
            {status && (
              <span className="text-xs text-text-tertiary">
                {status.jobs} {t('cron.jobs', 'jobs')}
                {status.nextWakeAtMS
                  ? ` · ${t('cron.nextWake', 'next wake')} ${formatRelative(status.nextWakeAtMS)}`
                  : ''}
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={openCreate}
              className="inline-flex items-center gap-1.5 rounded-lg bg-interaction-primary px-3 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90"
            >
              <svg
                className="h-4 w-4"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              >
                <title>Add</title>
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
              {t('cron.new', 'New')}
            </button>
            <button
              type="button"
              onClick={refresh}
              disabled={loading}
              className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-background-secondary px-3 py-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-50"
            >
              <svg
                className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <title>Refresh</title>
                <path d="M21 12a9 9 0 1 1-6.219-8.56" />
              </svg>
              {t('common.refresh', 'Refresh')}
            </button>
          </div>
        </header>

        <div className="flex-1 overflow-y-auto p-6">
          {loading && jobs.length === 0 && (
            <div className="flex items-center justify-center py-20">
              <div className="h-8 w-8 animate-spin rounded-full border-2 border-interaction-primary border-t-transparent" />
            </div>
          )}

          {!loading && jobs.length === 0 && (
            <div className="flex flex-col items-center justify-center py-20 text-center">
              <svg
                className="mb-4 h-12 w-12 text-text-tertiary/40"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <title>Clock</title>
                <circle cx="12" cy="12" r="10" />
                <polyline points="12 6 12 12 16 14" />
              </svg>
              <p className="text-sm text-text-tertiary">{t('cron.empty', 'No scheduled jobs')}</p>
              <button
                type="button"
                onClick={openCreate}
                className="mt-4 rounded-lg border border-border bg-background-secondary px-4 py-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover"
              >
                {t('cron.createFirst', 'Create your first job')}
              </button>
            </div>
          )}

          {sortedJobs.length > 0 && (
            <div className="space-y-2">
              {sortedJobs.map((job) => (
                <JobCard
                  key={job.id}
                  job={job}
                  expanded={expandedId === job.id}
                  busy={busyId === job.id}
                  onToggle={() => handleToggle(job.id)}
                  onEnableToggle={withBusy(job.id, () => toggleEnabled(job))}
                  onRun={withBusy(job.id, () => runJob(job.id))}
                  onDelete={withBusy(job.id, () => removeJob(job.id))}
                  onEdit={() => openEdit(job)}
                />
              ))}
            </div>
          )}
        </div>
      </main>

      {formOpen && (
        <JobFormModal
          initial={editing}
          agents={agents}
          busy={formBusy}
          onClose={() => setFormOpen(false)}
          onSubmit={handleFormSubmit}
        />
      )}
    </div>
  )
}
