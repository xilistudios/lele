import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SkillInfo } from '../../lib/types'
import { IconButton } from '../atoms/IconButton'

type Props = {
  skills: SkillInfo[]
  isLoading: boolean
  isRemoving: string | null
  onRemove: (name: string) => void
  onToggle?: (name: string, enabled: boolean) => void
}

const SOURCE_COLORS: Record<string, string> = {
  workspace: 'bg-state-info-light text-state-info border-state-info/30',
  global: 'bg-state-success-light text-state-success border-state-success/30',
  builtin: 'bg-surface-muted text-text-tertiary border-border/50',
}

const SOURCE_LABELS: Record<string, string> = {
  workspace: 'Workspace',
  global: 'Global',
  builtin: 'Built-in',
}

export function SkillsList({ skills, isLoading, isRemoving, onRemove, onToggle }: Props) {
  const { t } = useTranslation()
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null)

  const handleRemoveClick = (name: string, source?: string) => {
    if (source === 'builtin') return // Built-in skills cannot be removed
    setConfirmRemove(name)
  }

  const handleConfirmRemove = (name: string) => {
    onRemove(name)
    setConfirmRemove(null)
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-brand-rosa border-t-transparent" />
        <span className="ml-3 text-sm text-text-secondary">{t('common.loading')}</span>
      </div>
    )
  }

  if (skills.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="mb-4 rounded-full bg-background-tertiary p-4">
          <svg
            width="32"
            height="32"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            className="text-text-tertiary"
          >
            <title>Code icon</title>
            <path d="M20.91 8.84 8.56 2.23a1.93 1.93 0 0 0-1.81 0L3.1 4.13a1.95 1.95 0 0 0-.97 1.68v4.8a2 2 0 0 0 .5 1.33l7.09 8.38a1 1 0 0 0 1.5.07l9.72-9.72a1 1 0 0 0-.03-1.83Z" />
            <path d="M17 5v.01" />
          </svg>
        </div>
        <h3 className="text-sm font-medium text-text-primary">
          {t('skills.noSkills', 'No skills installed')}
        </h3>
        <p className="mt-1 text-xs text-text-secondary max-w-sm">
          {t('skills.noSkillsDesc', "Install skills to extend your agent's capabilities")}
        </p>
      </div>
    )
  }

  return (
    <div className="grid gap-3 sm:grid-cols-1 md:grid-cols-2 lg:grid-cols-3">
      {skills.map((skill) => (
        <div
          key={skill.name}
          className="group flex flex-col rounded-xl border border-border bg-background-secondary/50 p-4 hover:border-brand-rosa/30 transition-all duration-200"
        >
          <div className="flex items-start justify-between">
            <div className="flex items-center gap-2.5 min-w-0">
              <div className="flex-shrink-0 rounded-lg bg-brand-rosa/10 p-2">
                <svg
                  width="18"
                  height="18"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  className="text-brand-rosa"
                >
                  <title>Skill icon</title>
                  <path d="M20.91 8.84 8.56 2.23a1.93 1.93 0 0 0-1.81 0L3.1 4.13a1.95 1.95 0 0 0-.97 1.68v4.8a2 2 0 0 0 .5 1.33l7.09 8.38a1 1 0 0 0 1.5.07l9.72-9.72a1 1 0 0 0-.03-1.83Z" />
                  <path d="M17 5v.01" />
                </svg>
              </div>
              <h4 className="text-sm font-medium text-text-primary truncate">{skill.name}</h4>
            </div>

            <div className="flex-shrink-0 flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
              {/* Enable/disable toggle */}
              {onToggle && skill.source !== 'builtin' && (
                <button
                  type="button"
                  onClick={() => onToggle(skill.name, !skill.enabled)}
                  className={`rounded px-2 py-1 text-[11px] font-medium transition-colors ${
                    skill.enabled
                      ? 'text-state-success hover:bg-state-success/10'
                      : 'text-text-tertiary hover:bg-text-tertiary/10'
                  }`}
                  title={
                    skill.enabled ? t('skills.disable', 'Disable') : t('skills.enable', 'Enable')
                  }
                >
                  {skill.enabled ? '●' : '○'}
                </button>
              )}

              {/* Remove button */}
              {skill.source !== 'builtin' && (
                <>
                  {confirmRemove === skill.name ? (
                    <div className="flex items-center gap-1">
                      <button
                        type="button"
                        onClick={() => handleConfirmRemove(skill.name)}
                        disabled={isRemoving === skill.name}
                        className="rounded px-2 py-1 text-[11px] font-medium text-red-400 hover:bg-red-500/10 transition-colors"
                      >
                        {isRemoving === skill.name ? (
                          <div className="h-3 w-3 animate-spin rounded-full border border-state-error border-t-transparent" />
                        ) : (
                          t('skills.removeConfirmYes', 'Yes, remove')
                        )}
                      </button>
                      <button
                        type="button"
                        onClick={() => setConfirmRemove(null)}
                        className="rounded px-2 py-1 text-[11px] text-text-tertiary hover:text-text-primary transition-colors"
                      >
                        {t('skills.removeConfirmNo', 'No')}
                      </button>
                    </div>
                  ) : (
                    <IconButton
                      onClick={() => handleRemoveClick(skill.name, skill.source)}
                      disabled={isRemoving === skill.name}
                      variant="danger"
                      title={t('skills.removeSkill', 'Remove')}
                    >
                      <svg
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                      >
                        <title>Delete</title>
                        <path d="M3 6h18" />
                        <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                      </svg>
                    </IconButton>
                  )}
                </>
              )}
            </div>
          </div>

          {skill.description && (
            <p className="mt-2 text-xs text-text-secondary line-clamp-2">{skill.description}</p>
          )}

          <div className="mt-auto pt-3 flex items-center gap-2">
            {skill.source && (
              <span
                className={`inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium border ${SOURCE_COLORS[skill.source] || 'bg-surface-muted text-text-tertiary border-border/50'}`}
              >
                {SOURCE_LABELS[skill.source] || skill.source}
              </span>
            )}
            <span className="inline-flex items-center gap-1 text-[10px] text-text-tertiary">
              <span
                className={`h-1.5 w-1.5 rounded-full ${skill.enabled ? 'bg-state-success' : 'bg-text-tertiary'}`}
              />
              {skill.enabled ? t('common.enabled') : t('common.disabled')}
            </span>
          </div>
        </div>
      ))}
    </div>
  )
}
