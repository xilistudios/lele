import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SkillInfo } from '../../lib/types'
import { Badge } from '../atoms/Badge'
import { IconButton } from '../atoms/IconButton'

type Props = {
  skills: SkillInfo[]
  isLoading: boolean
  isRemoving: string | null
  onRemove: (name: string) => void
}

const SOURCE_VARIANTS: Record<string, 'primary' | 'success' | 'default'> = {
  workspace: 'primary',
  global: 'success',
  builtin: 'default',
}

const SOURCE_LABELS: Record<string, string> = {
  workspace: 'Workspace',
  global: 'Global',
  builtin: 'Built-in',
}

export function SkillsList({ skills, isLoading, isRemoving, onRemove }: Props) {
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
            <path d="M20.91 8.84 8.56 2.23a1.93 1.93 0 0 0-1.81 0L3.1 4.13a1.95 1.95 0 0 0-.97 1.68v4.8a2 2 0 0 0 .5 1.33l7.09 8.38a1 1 0 0 0 1.5.07l9.72-9.72a1 1 0 0 0-.03-1.83Z" />
            <path d="M17 5v.01" />
          </svg>
        </div>
        <h3 className="text-sm font-medium text-text-primary">{t('skills.noSkills', 'No skills installed')}</h3>
        <p className="mt-1 text-xs text-text-secondary max-w-sm">
          {t('skills.noSkillsDesc', 'Install skills to extend your agent\'s capabilities')}
        </p>
      </div>
    )
  }

  return (
    <div className="grid gap-3 sm:grid-cols-1 md:grid-cols-2 lg:grid-cols-3">
      {skills.map((skill) => (
        <div
          key={skill.id}
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
                  <path d="M20.91 8.84 8.56 2.23a1.93 1.93 0 0 0-1.81 0L3.1 4.13a1.95 1.95 0 0 0-.97 1.68v4.8a2 2 0 0 0 .5 1.33l7.09 8.38a1 1 0 0 0 1.5.07l9.72-9.72a1 1 0 0 0-.03-1.83Z" />
                  <path d="M17 5v.01" />
                </svg>
              </div>
              <h4 className="text-sm font-medium text-text-primary truncate">{skill.name}</h4>
            </div>

            {skill.source !== 'builtin' && (
              <div className="flex-shrink-0 opacity-0 group-hover:opacity-100 transition-opacity">
                {confirmRemove === skill.name ? (
                  <div className="flex items-center gap-1">
                    <button
                      type="button"
                      onClick={() => handleConfirmRemove(skill.name)}
                      disabled={isRemoving === skill.name}
                      className="rounded px-2 py-1 text-[11px] font-medium text-red-400 hover:bg-red-500/10 transition-colors"
                    >
                      {isRemoving === skill.name ? (
                        <div className="h-3 w-3 animate-spin rounded-full border border-red-400 border-t-transparent" />
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
                      <path d="M3 6h18" />
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                    </svg>
                  </IconButton>
                )}
              </div>
            )}
          </div>

          {skill.description && (
            <p className="mt-2 text-xs text-text-secondary line-clamp-2">{skill.description}</p>
          )}

          <div className="mt-auto pt-3 flex items-center gap-2">
            {skill.source && (
              <Badge variant={SOURCE_VARIANTS[skill.source] || 'default'} bordered>
                {SOURCE_LABELS[skill.source] || skill.source}
              </Badge>
            )}
            <Badge variant={skill.installed ? 'success' : 'default'}>
              <span className={`h-1.5 w-1.5 rounded-full ${skill.installed ? 'bg-emerald-400' : 'bg-slate-400'}`} />
              {skill.installed ? t('common.enabled') : t('common.disabled')}
            </Badge>
          </div>
        </div>
      ))}
    </div>
  )
}
