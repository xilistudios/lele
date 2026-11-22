import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import {
  getAgentModelPrimary,
  getDefaultModel,
  getErrorForPath,
  isDirtyPath,
} from '../../../hooks/useSettingsHelpers'
import { thinkingLevelOptions } from '../../../lib/thinkingLevel'
import type { EditableAgentConfig } from '../../../lib/types'
import { SearchableSelect } from '../../molecules/SearchableSelect'
import { SegmentedControl } from '../../molecules/SegmentedControl'
import { SettingsField } from '../../molecules/SettingsField'
import { SettingsSection } from '../../molecules/SettingsSection'
import { StringListEditor } from '../../molecules/StringListEditor'
import { TemperatureSlider } from '../../molecules/TemperatureSlider'

type Props = {
  agent: EditableAgentConfig
  index: number
}

export function AgentModelSection({ agent, index }: Props) {
  const { t } = useTranslation()
  const {
    draftConfig,
    dirtyPaths,
    validationErrors,
    updateField,
    getOptionsForAgent,
    getGroupsForAgent,
    isLoadingModels,
  } = useSettings()

  const defaultsModel = getDefaultModel(draftConfig)

  const path = (field: string) => `agents.list.${index}.${field}`
  const dirty = (field: string) => isDirtyPath(dirtyPaths, path(field))
  const error = (field: string) => getErrorForPath(validationErrors, path(field))

  const primary = getAgentModelPrimary(agent.model)
  const emptyLabel = isLoadingModels ? t('settings.loading') : t('settings.noModels')

  const thinkingSegments = thinkingLevelOptions(t).map((option) => ({
    value: option.value,
    label: option.label,
  }))

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.model')}>
        <SettingsField
          label={t('settings.fields.agentModelPrimary')}
          description={
            defaultsModel
              ? t('settings.agentPage.inheritsFromDefaults', { model: defaultsModel })
              : t('settings.descriptions.agentModelPrimary')
          }
          path={path('model.primary')}
          isDirty={dirty('model.primary')}
          error={error('model.primary')}
        >
          <SearchableSelect
            ariaLabel={t('settings.fields.agentModelPrimary')}
            buttonLabel={primary || t('settings.agentPage.inherit', { defaultValue: 'Inherit' })}
            direction="down"
            emptyLabel={emptyLabel}
            groups={getGroupsForAgent}
            onChange={(value) =>
              updateField(path('model'), { ...agent.model, primary: value || undefined })
            }
            options={getOptionsForAgent}
            placeholder={primary || t('settings.selectModel')}
            searchAriaLabel={`${t('settings.fields.agentModelPrimary')} search`}
            searchPlaceholder={t('settings.fields.agentModelPrimary')}
            value={primary}
          />
        </SettingsField>

        <SettingsField
          label={t('settings.fields.agentModelFallbacks')}
          description={t('settings.descriptions.agentModelFallbacks')}
          path={path('model.fallbacks')}
          isDirty={dirty('model.fallbacks')}
          error={error('model.fallbacks')}
        >
          <StringListEditor
            id={path('model.fallbacks')}
            value={agent.model?.fallbacks ?? []}
            onChange={(values) =>
              updateField(path('model'), { primary: primary || undefined, fallbacks: values })
            }
            options={getOptionsForAgent}
            groups={getGroupsForAgent}
            emptyLabel={emptyLabel}
            loading={isLoadingModels}
          />
        </SettingsField>
      </SettingsSection>

      <SettingsSection
        title={t('settings.agentPage.reasoningSection', { defaultValue: 'Reasoning' })}
      >
        <SettingsField
          label={t('settings.fields.agentThinkingLevel')}
          description={t('settings.descriptions.agentThinkingLevel')}
          path={path('thinking_level')}
          isDirty={dirty('thinking_level')}
          error={error('thinking_level')}
        >
          <SegmentedControl
            id={path('thinking_level')}
            ariaLabel={t('settings.fields.agentThinkingLevel')}
            fullWidth
            options={thinkingSegments}
            value={agent.thinking_level ?? ''}
            onChange={(value) => updateField(path('thinking_level'), value || undefined)}
          />
        </SettingsField>

        <SettingsField
          label={t('settings.fields.agentTemperature')}
          description={t('settings.descriptions.agentTemperature')}
          path={path('temperature')}
          isDirty={dirty('temperature')}
          error={error('temperature')}
        >
          <TemperatureSlider
            id={path('temperature')}
            ariaLabel={t('settings.fields.agentTemperature')}
            value={agent.temperature}
            onChange={(value) => updateField(path('temperature'), value)}
          />
        </SettingsField>
      </SettingsSection>
    </div>
  )
}
