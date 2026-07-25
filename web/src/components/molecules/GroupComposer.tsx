import { type FormEvent, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useGroups } from '../../hooks/useGroups'
import { getModeTheme } from '../../lib/modeTheme'
import type { GroupProfile } from '../../lib/types'

export function GroupComposer() {
  const { t } = useTranslation()
  const { onSend, isProcessing, onCancel, groups, currentSessionKey } = useAppLogicContext()
  const { api } = useAuthContext()
  const { groups: sessionGroups } = useGroups(groups, currentSessionKey)

  const [groupProfiles, setGroupProfiles] = useState<GroupProfile[]>([])
  const [selectedProfile, setSelectedProfile] = useState('')
  const [task, setTask] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  // When session has existing groups, default to follow-up (chat) mode
  const [isNewGroupMode, setIsNewGroupMode] = useState(false)

  const groupTheme = getModeTheme('group')
  const hasExistingGroups = sessionGroups.length > 0

  useEffect(() => {
    let cancelled = false
    api
      .getConfig()
      .then((cfg) => {
        if (cancelled) return
        setGroupProfiles(cfg.groups?.list ?? [])
      })
      .catch((err: unknown) => {
        console.warn('[GroupComposer] Failed to load group profiles:', err)
      })
    return () => {
      cancelled = true
    }
  }, [api])

  const hasProfiles = groupProfiles.length > 0
  const activeProcessing = isProcessing || isSubmitting

  // In follow-up mode: can submit with just text
  // In new-group mode: need profile + text (if profiles exist)
  const showNewGroupForm = isNewGroupMode || !hasExistingGroups
  const canSubmit = showNewGroupForm
    ? task.trim().length > 0 && (!hasProfiles || selectedProfile !== '') && !activeProcessing
    : task.trim().length > 0 && !activeProcessing

  const handleSubmit = async (e?: FormEvent) => {
    e?.preventDefault()
    if (!canSubmit) return

    let content: string
    if (showNewGroupForm && hasProfiles) {
      content = `/group start ${selectedProfile} ${task.trim()}`
    } else if (showNewGroupForm && !hasProfiles) {
      // No profiles configured — send as plain /group command
      content = task.trim()
    } else {
      // Follow-up mode: send plain message
      content = task.trim()
    }

    setIsSubmitting(true)
    try {
      await onSend(content, [])
      setTask('')
      // After starting a new group, switch back to follow-up mode
      if (showNewGroupForm && hasExistingGroups) {
        setIsNewGroupMode(false)
      }
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit}>
      <div className="rounded-lg border border-border bg-background-secondary transition-colors focus-within:border-border-light">
        <div className={`h-0.5 w-full rounded-t-lg ${groupTheme.accentBar}`} />

        {/* Toggle between follow-up and new-group mode */}
        {hasExistingGroups && (
          <div className="flex items-center gap-2 px-4 pt-2">
            <button
              type="button"
              onClick={() => setIsNewGroupMode(false)}
              className={`px-2.5 py-1 rounded-md text-xs font-medium transition-colors ${
                !isNewGroupMode
                  ? 'bg-brand-naranja/15 text-brand-naranja'
                  : 'text-text-tertiary hover:text-text-secondary hover:bg-surface-hover'
              }`}
            >
              {t('groups.followUp')}
            </button>
            <button
              type="button"
              onClick={() => setIsNewGroupMode(true)}
              className={`px-2.5 py-1 rounded-md text-xs font-medium transition-colors ${
                isNewGroupMode
                  ? 'bg-brand-naranja/15 text-brand-naranja'
                  : 'text-text-tertiary hover:text-text-secondary hover:bg-surface-hover'
              }`}
            >
              {t('groups.newGroup')}
            </button>
          </div>
        )}

        {/* Profile selector — only in new-group mode */}
        {showNewGroupForm && hasProfiles && (
          <div className="px-4 pt-3">
            <select
              className="w-full rounded-md border border-border bg-background-primary px-3 py-2 text-sm text-text-primary outline-none focus:border-border-light disabled:opacity-50"
              value={selectedProfile}
              onChange={(e) => setSelectedProfile(e.target.value)}
            >
              <option value="">{t('groups.selectProfile')}</option>
              {groupProfiles.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.id} ({p.strategy}, {p.participants.length} agents)
                </option>
              ))}
            </select>
          </div>
        )}

        {showNewGroupForm && !hasProfiles && !hasExistingGroups && (
          <div className="px-4 pt-3">
            <p className="text-xs text-text-tertiary">{t('groups.noProfiles')}</p>
          </div>
        )}

        <textarea
          className="min-h-[44px] max-h-[200px] w-full resize-none bg-transparent px-4 pb-2 pt-3 text-sm text-text-primary outline-none placeholder:text-text-tertiary disabled:opacity-50"
          placeholder={
            activeProcessing
              ? t('groups.taskPlaceholderWhileRunning')
              : showNewGroupForm
                ? t('groups.taskPlaceholder')
                : t('groups.followUpPlaceholder')
          }
          value={task}
          onChange={(e) => {
            setTask(e.target.value)
            e.target.style.height = 'auto'
            e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey && !activeProcessing) {
              e.preventDefault()
              handleSubmit()
            }
          }}
          rows={showNewGroupForm ? 3 : 1}
        />
        <div className="flex items-center justify-between px-3 pb-2 pt-1">
          {activeProcessing ? (
            <div className="flex items-center gap-2 text-xs font-medium text-brand-naranja px-1">
              <span className="inline-block h-2 w-2 rounded-full bg-brand-naranja animate-ping" />
              <span>{t('groups.executing')}</span>
            </div>
          ) : (
            <div />
          )}
          <div className="flex items-center gap-2">
            {activeProcessing ? (
              <button
                type="button"
                onClick={onCancel}
                aria-label={t('groups.cancel')}
                className="flex h-7 items-center justify-center rounded-md border border-state-error/30 bg-state-error-light px-3 text-xs font-medium text-state-error transition-colors hover:bg-state-error hover:text-text-on-accent"
              >
                {t('groups.cancel')}
              </button>
            ) : (
              <button
                type="submit"
                disabled={!canSubmit}
                aria-label={showNewGroupForm ? t('groups.start') : t('groups.send')}
                className="flex h-7 items-center justify-center rounded-md bg-cta-primary px-3 text-xs font-medium text-text-on-accent transition-colors hover:bg-cta-hover disabled:opacity-40 disabled:cursor-not-allowed"
              >
                {showNewGroupForm ? t('groups.start') : t('groups.send')}
              </button>
            )}
          </div>
        </div>
      </div>
    </form>
  )
}
