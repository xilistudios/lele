import { useSettings } from '../../../../contexts/SettingsContext'

const DEFAULT_AVATARS = ['🤖', '🔧', '💻', '🎨', '⚡', '🔍', '📊', '⚙️']

type Props = {
  agentId: string
  agentName: string
  isDefault: boolean
  primaryModel: string
  fallbacks: string[]
  temperature: number
  skills: string[]
  enableThinking: boolean
  supportsImages: boolean
}

export function AgentPreview({
  agentId,
  agentName,
  isDefault,
  primaryModel,
  fallbacks,
  temperature,
  skills,
  enableThinking,
  supportsImages,
}: Props) {
  const { t } = useSettings()

  const displayName = agentName.trim() || agentId.trim() || 'New Agent'
  const avatar = DEFAULT_AVATARS[displayName.length % DEFAULT_AVATARS.length]

  const hasContent = agentId || agentName || primaryModel || skills.length > 0

  if (!hasContent) {
    return (
      <div className="rounded-lg border border-dashed border-border bg-background-secondary/50 p-6 text-center">
        <div className="text-3xl mb-3">👻</div>
        <p className="text-sm text-text-secondary">{t('settings.addAgentModal.previewEmpty')}</p>
        <p className="text-xs text-text-tertiary mt-1">
          {t('settings.addAgentModal.previewEmptyDesc')}
        </p>
      </div>
    )
  }

  return (
    <div className="rounded-lg border border-border bg-background-secondary/50 overflow-hidden">
      {/* Header with avatar and name */}
      <div className="p-4 bg-gradient-to-br from-background-secondary to-background-tertiary border-b border-border">
        <div className="flex items-center gap-3">
          <div className="w-12 h-12 rounded-xl bg-gradient-to-br from-blue-500 to-purple-500 flex items-center justify-center text-2xl shadow-lg">
            {avatar}
          </div>
          <div className="flex-1 min-w-0">
            <h4 className="font-medium text-text-primary truncate">{displayName}</h4>
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-xs text-text-secondary font-mono">@{agentId || '...'}</span>
              {isDefault && (
                <span className="text-[10px] px-2 py-0.5 rounded-full bg-blue-500/20 text-blue-400 font-medium">
                  {t('settings.defaultBadge').toUpperCase()}
                </span>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Specs */}
      <div className="p-4 space-y-3">
        {/* Model */}
        {primaryModel && (
          <div className="flex items-center justify-between">
            <span className="text-xs text-text-tertiary">
              {t('settings.addAgentModal.modelPrimaryLabel')}
            </span>
            <span className="text-xs text-text-primary font-medium truncate max-w-[60%]">
              {primaryModel.split('/').pop() || primaryModel}
            </span>
          </div>
        )}

        {/* Fallbacks */}
        {fallbacks.length > 0 && (
          <div className="flex items-start justify-between gap-2">
            <span className="text-xs text-text-tertiary shrink-0">
              {t('settings.addAgentModal.modelFallbacksLabel')}
            </span>
            <span className="text-xs text-text-secondary text-right">
              {fallbacks.length} model{fallbacks.length !== 1 ? 's' : ''}
            </span>
          </div>
        )}

        {/* Temperature */}
        <div className="flex items-center justify-between">
          <span className="text-xs text-text-tertiary">
            {t('settings.fields.agentTemperature')}
          </span>
          <div className="flex items-center gap-2">
            <div className="w-16 h-1.5 rounded-full bg-border overflow-hidden">
              <div
                className="h-full bg-gradient-to-r from-blue-500 to-purple-500 transition-all duration-300"
                style={{ width: `${(temperature / 2) * 100}%` }}
              />
            </div>
            <span className="text-xs text-text-primary font-mono w-8 text-right">
              {temperature}
            </span>
          </div>
        </div>

        {/* Features badges */}
        <div className="flex flex-wrap gap-1.5 pt-1">
          {enableThinking && (
            <span className="text-[10px] px-2 py-1 rounded-full bg-purple-500/20 text-purple-400">
              🧠 {t('settings.fields.agentThinking')}
            </span>
          )}
          {supportsImages && (
            <span className="text-[10px] px-2 py-1 rounded-full bg-green-500/20 text-green-400">
              📷 {t('settings.fields.agentSupportsImages')}
            </span>
          )}
          {skills.map((skill) => (
            <span
              key={skill}
              className="text-[10px] px-2 py-1 rounded-full bg-surface-muted text-text-secondary"
            >
              {skill}
            </span>
          ))}
        </div>
      </div>
    </div>
  )
}
