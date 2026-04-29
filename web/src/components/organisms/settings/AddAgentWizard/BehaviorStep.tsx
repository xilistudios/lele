import { useSettings } from '../../../../contexts/SettingsContext'
import { BooleanInput, NumberInput } from '../../../molecules'

type Props = {
  temperature: number
  setTemperature: (value: number) => void
  maxIterations: number
  setMaxIterations: (value: number) => void
  maxTokens: number
  setMaxTokens: (value: number) => void
  contextWindow: number
  setContextWindow: (value: number) => void
  enableThinking: boolean
  setEnableThinking: (value: boolean) => void
  supportsImages: boolean
  setSupportsImages: (value: boolean) => void
}

export function BehaviorStep({
  temperature,
  setTemperature,
  maxIterations,
  setMaxIterations,
  maxTokens,
  setMaxTokens,
  contextWindow,
  setContextWindow,
  enableThinking,
  setEnableThinking,
  supportsImages,
  setSupportsImages,
}: Props) {
  const { t } = useSettings()

  const getTemperatureInfo = (temp: number) => {
    if (temp <= 0.3)
      return {
        label: t('settings.addAgentModal.tempPrecise'),
        color: 'text-blue-400',
        desc: t('settings.addAgentModal.tempPreciseDesc'),
      }
    if (temp <= 0.7)
      return {
        label: t('settings.addAgentModal.tempBalanced'),
        color: 'text-green-400',
        desc: t('settings.addAgentModal.tempBalancedDesc'),
      }
    if (temp <= 1.0)
      return {
        label: t('settings.addAgentModal.tempCreative'),
        color: 'text-yellow-400',
        desc: t('settings.addAgentModal.tempCreativeDesc'),
      }
    return {
      label: t('settings.addAgentModal.tempVeryCreative'),
      color: 'text-purple-400',
      desc: t('settings.addAgentModal.tempVeryCreativeDesc'),
    }
  }

  const tempInfo = getTemperatureInfo(temperature)

  return (
    <div className="space-y-5">
      <div className="text-center pb-2">
        <h3 className="text-lg font-medium text-text-primary">
          {t('settings.addAgentModal.stepBehaviorTitle')}
        </h3>
        <p className="text-sm text-text-secondary mt-1">
          {t('settings.addAgentModal.stepBehaviorDesc')}
        </p>
      </div>

      {/* Temperature with visual indicator */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-text-primary">
            {t('settings.fields.agentTemperature')}
          </label>
          <span className={`text-xs font-medium ${tempInfo.color}`}>{tempInfo.label}</span>
        </div>

        {/* Temperature gradient bar */}
        <div className="relative">
          <div className="h-2 rounded-full bg-gradient-to-r from-blue-500 via-green-500 via-yellow-500 to-purple-500" />
          <div
            className="absolute top-1/2 -translate-y-1/2 w-4 h-4 rounded-full bg-white shadow-lg border-2 border-border transition-all duration-200 pointer-events-none"
            style={{ left: `${(temperature / 2) * 100}%`, transform: 'translate(-50%, -50%)' }}
          />
        </div>

        <NumberInput
          id="wizard-temperature"
          value={temperature}
          min={0}
          max={2}
          step={0.1}
          onChange={setTemperature}
        />
        <p className="text-xs text-text-tertiary">{tempInfo.desc}</p>
      </div>

      {/* Other numeric settings in a grid */}
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-2">
          <label htmlFor="wizard-max-iterations" className="text-sm font-medium text-text-primary">
            {t('settings.fields.agentMaxIterations')}
          </label>
          <NumberInput
            id="wizard-max-iterations"
            value={maxIterations}
            min={1}
            max={100}
            step={1}
            onChange={setMaxIterations}
          />
          <p className="text-xs text-text-tertiary">
            {t('settings.addAgentModal.maxIterationsDesc')}
          </p>
        </div>
        <div className="space-y-2">
          <label htmlFor="wizard-max-tokens" className="text-sm font-medium text-text-primary">
            {t('settings.fields.agentMaxTokens')}
          </label>
          <NumberInput
            id="wizard-max-tokens"
            value={maxTokens}
            min={256}
            max={128000}
            step={256}
            onChange={setMaxTokens}
          />
          <p className="text-xs text-text-tertiary">
            {t('settings.addAgentModal.maxTokensDesc')}
          </p>
        </div>
      </div>

      {/* Context window */}
      <div className="space-y-2">
        <label htmlFor="wizard-context-window" className="text-sm font-medium text-text-primary">
          {t('settings.fields.agentContextWindow')}
        </label>
        <NumberInput
          id="wizard-context-window"
          value={contextWindow}
          min={4096}
          max={2097152}
          step={4096}
          onChange={setContextWindow}
        />
        <p className="text-xs text-text-tertiary">
          {t('settings.addAgentModal.contextWindowDesc')}
        </p>
      </div>

      {/* Feature toggles */}
      <div className="space-y-3 pt-2">
        <p className="text-xs font-medium text-text-secondary uppercase tracking-wider">
          {t('settings.addAgentModal.featuresLabel')}
        </p>

        <div className="rounded-lg border border-border bg-background-secondary/30 p-4 space-y-4">
          {/* Thinking */}
          <div className="flex items-start gap-3">
            <div className="pt-0.5">
              <BooleanInput
                id="wizard-thinking"
                value={enableThinking}
                onChange={setEnableThinking}
              />
            </div>
            <div className="flex-1">
              <label
                htmlFor="wizard-thinking"
                className="text-sm font-medium text-text-primary cursor-pointer flex items-center gap-2"
              >
                🧠 {t('settings.fields.agentThinking')}
              </label>
              <p className="text-xs text-text-tertiary mt-1">
                {t('settings.descriptions.agentThinking')}
              </p>
            </div>
          </div>

          {/* Vision */}
          <div className="flex items-start gap-3">
            <div className="pt-0.5">
              <BooleanInput
                id="wizard-supports-images"
                value={supportsImages}
                onChange={setSupportsImages}
              />
            </div>
            <div className="flex-1">
              <label
                htmlFor="wizard-supports-images"
                className="text-sm font-medium text-text-primary cursor-pointer flex items-center gap-2"
              >
                📷 {t('settings.fields.agentSupportsImages')}
              </label>
              <p className="text-xs text-text-tertiary mt-1">
                {t('settings.descriptions.agentSupportsImages')}
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
