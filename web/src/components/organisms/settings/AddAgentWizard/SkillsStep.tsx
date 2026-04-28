import { useSettings } from '../../../../contexts/SettingsContext'
import { StringListEditor } from '../../../molecules'

type Props = {
  skills: string[]
  setSkills: (value: string[]) => void
}

const POPULAR_SKILLS_KEYS = [
  { id: 'github', icon: '🐙' },
  { id: 'weather', icon: '🌤️' },
  { id: 'summarize', icon: '📝' },
  { id: 'web_search', icon: '🔍' },
  { id: 'file_manager', icon: '📁' },
]

export function SkillsStep({ skills, setSkills }: Props) {
  const { t } = useSettings()

  const toggleSkill = (skillId: string) => {
    if (skills.includes(skillId)) {
      setSkills(skills.filter((s) => s !== skillId))
    } else {
      setSkills([...skills, skillId])
    }
  }

  return (
    <div className="space-y-5">
      <div className="text-center pb-2">
        <h3 className="text-lg font-medium text-text-primary">
          {t('settings.addAgentModal.stepSkillsTitle')}
        </h3>
        <p className="text-sm text-text-secondary mt-1">
          {t('settings.addAgentModal.stepSkillsDesc')}
        </p>
      </div>

      {/* Popular skills grid */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-text-secondary uppercase tracking-wider">
          {t('settings.addAgentModal.popularSkills')}
        </p>
        <div className="grid grid-cols-2 gap-2">
          {POPULAR_SKILLS_KEYS.map((skill) => {
            const isSelected = skills.includes(skill.id)
            const nameKey = `settings.addAgentModal.skill${skill.id
              .split('_')
              .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
              .join('')}` as const
            const descKey = `${nameKey}Desc` as const
            return (
              <button
                key={skill.id}
                type="button"
                onClick={() => toggleSkill(skill.id)}
                className={`
                  flex items-start gap-3 p-3 rounded-lg border text-left transition-all duration-200
                  ${
                    isSelected
                      ? 'border-blue-500/60 bg-blue-500/10 ring-1 ring-blue-500/20'
                      : 'border-border bg-background-secondary/30 hover:bg-background-secondary'
                  }
                `}
              >
                <span className="text-xl">{skill.icon}</span>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-text-primary">
                      {t(nameKey) || skill.id}
                    </span>
                    {isSelected && (
                      <svg
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2.5"
                        className="text-blue-400 flex-shrink-0"
                      >
                        <polyline points="20 6 9 17 4 12" />
                      </svg>
                    )}
                  </div>
                  <p className="text-xs text-text-tertiary truncate">
                    {t(descKey)}
                  </p>
                </div>
              </button>
            )
          })}
        </div>
      </div>

      {/* Custom skills */}
      <div className="space-y-2">
        <label className="text-sm font-medium text-text-primary">
          {t('settings.fields.agentSkills')}
        </label>
        <div className="rounded-lg border border-border bg-background-secondary/30 p-4">
          <StringListEditor id="wizard-agent-skills" value={skills} onChange={setSkills} />
        </div>
        <p className="text-xs text-text-tertiary">{t('settings.descriptions.agentSkills')}</p>
      </div>

      {/* Selected count */}
      {skills.length > 0 && (
        <div className="flex items-center gap-2 text-sm text-text-secondary">
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            className="text-blue-400"
          >
            <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
            <polyline points="22 4 12 14.01 9 11.01" />
          </svg>
          <span>
            {skills.length} {skills.length === 1 ? t('settings.addAgentModal.skillsSelected') : t('settings.addAgentModal.skillsSelectedPlural')}
          </span>
        </div>
      )}
    </div>
  )
}
