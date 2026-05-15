import { useSettings } from '../../../../contexts/SettingsContext'
import { NumberInput } from '../../../molecules'

type Props = {
  temperature: number
  setTemperature: (value: number) => void
}

export function BehaviorStep({ temperature, setTemperature }: Props) {
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
          <label htmlFor="wizard-temperature" className="text-sm font-medium text-text-primary">
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
    </div>
  )
}
