import { useSettings } from '../../../../contexts/SettingsContext'
import { BooleanInput } from '../../../molecules'

type Props = {
  agentId: string
  setAgentId: (value: string) => void
  agentName: string
  setAgentName: (value: string) => void
  isDefault: boolean
  setIsDefault: (value: boolean) => void
  isValid: boolean
  isDuplicate: boolean
}

export function BasicInfoStep({
  agentId,
  setAgentId,
  agentName,
  setAgentName,
  isDefault,
  setIsDefault,
  isValid,
  isDuplicate,
}: Props) {
  const { t } = useSettings()

  return (
    <div className="space-y-5">
      <div className="text-center pb-2">
        <h3 className="text-lg font-medium text-text-primary">
          {t('settings.addAgentModal.stepBasicInfoTitle')}
        </h3>
        <p className="text-sm text-text-secondary mt-1">
          {t('settings.addAgentModal.stepBasicInfoDesc')}
        </p>
      </div>

      {/* Agent ID */}
      <div className="space-y-2">
        <label className="flex items-center gap-1 text-sm font-medium text-text-primary">
          {t('settings.fields.agentId')}
          <span className="text-red-400">*</span>
        </label>
        <div className="relative">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary text-sm pointer-events-none">
            @
          </span>
          <input
            type="text"
            value={agentId}
            onChange={(e) =>
              setAgentId(e.target.value.replace(/[^a-zA-Z0-9_-]/g, '').toLowerCase())
            }
            placeholder="my-awesome-agent"
            className={`
              w-full rounded-lg border bg-background-primary pl-8 pr-10 py-2.5 text-sm text-text-primary
              placeholder:text-text-tertiary focus:outline-none transition-all duration-200
              ${
                isDuplicate
                  ? 'border-red-500/60 focus:border-red-500 focus:ring-2 focus:ring-red-500/20'
                  : agentId
                    ? isValid
                      ? 'border-border focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20'
                      : 'border-border focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20'
                    : 'border-border focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20'
              }
            `}
          />
          {isValid && agentId && !isDuplicate && (
            <svg
              className="absolute right-3 top-1/2 -translate-y-1/2 text-state-success"
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <polyline points="20 6 9 17 4 12" />
            </svg>
          )}
          {isDuplicate && (
            <svg
              className="absolute right-3 top-1/2 -translate-y-1/2 text-red-400"
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <circle cx="12" cy="12" r="10" />
              <line x1="15" y1="9" x2="9" y2="15" />
              <line x1="9" y1="9" x2="15" y2="15" />
            </svg>
          )}
        </div>
        {isDuplicate && (
          <p className="text-xs text-red-400">An agent with this ID already exists</p>
        )}
        <p className={`text-xs text-text-tertiary ${isDuplicate ? 'hidden' : ''}`}>
          {t('settings.addAgentModal.agentIdHint')}
        </p>
      </div>

      {/* Agent Name */}
      <div className="space-y-2">
        <label className="text-sm font-medium text-text-primary">
          {t('settings.fields.agentName')}
        </label>
        <input
          type="text"
          value={agentName}
          onChange={(e) => setAgentName(e.target.value)}
          placeholder="My Awesome Agent"
          className="w-full rounded-lg border border-border bg-background-primary px-3 py-2.5 text-sm text-text-primary placeholder:text-text-tertiary focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/20 transition-all duration-200"
        />
        <p className="text-xs text-text-tertiary">{t('settings.descriptions.agentName')}</p>
      </div>

      {/* Default checkbox */}
      <div className="rounded-lg border border-border bg-background-secondary/50 p-4">
        <div className="flex items-start gap-3">
          <div className="pt-0.5">
            <BooleanInput id="wizard-agent-default" value={isDefault} onChange={setIsDefault} />
          </div>
          <div>
            <label
              htmlFor="wizard-agent-default"
              className="text-sm font-medium text-text-primary cursor-pointer"
            >
              {t('settings.fields.agentDefault')}
            </label>
            <p className="text-xs text-text-tertiary mt-1">
              {t('settings.descriptions.agentDefault')}
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
