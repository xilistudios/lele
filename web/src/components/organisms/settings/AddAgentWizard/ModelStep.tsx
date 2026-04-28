import { useSettings } from '../../../../contexts/SettingsContext'
import { SearchableSelect, StringListEditor } from '../../../molecules'

type Props = {
  primaryModel: string
  setPrimaryModel: (value: string) => void
  fallbacks: string[]
  setFallbacks: (value: string[]) => void
}

export function ModelStep({ primaryModel, setPrimaryModel, fallbacks, setFallbacks }: Props) {
  const { t, getOptionsForAgent, getGroupsForAgent, isLoadingModels } = useSettings()

  return (
    <div className="space-y-5">
      <div className="text-center pb-2">
        <h3 className="text-lg font-medium text-text-primary">
          {t('settings.addAgentModal.stepModelTitle')}
        </h3>
        <p className="text-sm text-text-secondary mt-1">
          {t('settings.addAgentModal.stepModelDesc')}
        </p>
      </div>

      {/* Primary Model */}
      <div className="space-y-2">
        <label className="text-sm font-medium text-text-primary">
          {t('settings.fields.agentModelPrimary')}
        </label>
        <SearchableSelect
          ariaLabel={t('settings.fields.agentModelPrimary')}
          buttonLabel={t('settings.fields.agentModelPrimary')}
          direction="down"
          emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
          groups={getGroupsForAgent}
          onChange={setPrimaryModel}
          options={getOptionsForAgent}
          placeholder={t('settings.selectModel')}
          searchAriaLabel={`${t('settings.fields.agentModelPrimary')} search`}
          searchPlaceholder={t('settings.fields.agentModelPrimary')}
          value={primaryModel}
        />
        {primaryModel && (
          <div className="flex items-center gap-2 text-xs text-state-success">
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <polyline points="20 6 9 17 4 12" />
            </svg>
            {t('settings.addAgentModal.modelConfigured')}
          </div>
        )}
      </div>

      {/* Fallback Models */}
      <div className="space-y-2">
        <label className="text-sm font-medium text-text-primary">
          {t('settings.fields.agentModelFallbacks')}
        </label>
        <div className="rounded-lg border border-border bg-background-secondary/30 p-4">
          <StringListEditor
            id="wizard-agent-fallbacks"
            value={fallbacks}
            onChange={setFallbacks}
            options={getOptionsForAgent}
            groups={getGroupsForAgent}
            emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
            loading={isLoadingModels}
          />
        </div>
        <p className="text-xs text-text-tertiary">
          {t('settings.descriptions.agentModelFallbacks')}
        </p>
      </div>

      {/* Model info cards */}
      <div className="grid grid-cols-2 gap-3 pt-2">
        <div className="rounded-lg border border-border bg-background-secondary/30 p-3">
          <div className="flex items-center gap-2 mb-1">
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              className="text-blue-400"
            >
              <circle cx="12" cy="12" r="10" />
              <line x1="12" y1="16" x2="12" y2="12" />
              <line x1="12" y1="8" x2="12.01" y2="8" />
            </svg>
            <span className="text-xs font-medium text-text-primary">
              {t('settings.addAgentModal.modelPrimaryLabel')}
            </span>
          </div>
          <p className="text-xs text-text-tertiary">
            {t('settings.addAgentModal.modelPrimaryDesc')}
          </p>
        </div>
        <div className="rounded-lg border border-border bg-background-secondary/30 p-3">
          <div className="flex items-center gap-2 mb-1">
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              className="text-purple-400"
            >
              <path d="M12 2L2 7l10 5 10-5-10-5z" />
              <path d="M2 17l10 5 10-5" />
              <path d="M2 12l10 5 10-5" />
            </svg>
            <span className="text-xs font-medium text-text-primary">
              {t('settings.addAgentModal.modelFallbacksLabel')}
            </span>
          </div>
          <p className="text-xs text-text-tertiary">
            {t('settings.addAgentModal.modelFallbacksDesc')}
          </p>
        </div>
      </div>
    </div>
  )
}
