import { useSettings } from '../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../hooks/useSettingsHelpers'
import { getErrorForPath } from '../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  NumberInput,
  SearchableSelect,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../molecules'

export function GeneralSettings() {
  const {
    draftConfig,
    dirtyPaths,
    validationErrors,
    updateField,
    t,
    getOptionsForAgent,
    getGroupsForAgent,
    modelGroups,
    modelOptions,
    isLoadingModels,
  } = useSettings()

  if (!draftConfig) return null
  const config = draftConfig

  const getDefaultModel = (): string => {
    return config.agents?.defaults?.model || ''
  }

  const getDefaultImageModel = (): string => {
    return config.agents?.defaults?.image_model || ''
  }

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.agentsDefaults')}>
        <SettingsField
          label={t('settings.fields.workspace')}
          path="agents.defaults.workspace"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.workspace')}
          error={getErrorForPath(validationErrors, 'agents.defaults.workspace')}
          required
        >
          <TextInput
            id="agents.defaults.workspace"
            value={config.agents.defaults.workspace}
            onChange={(v) => updateField('agents.defaults.workspace', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.restrictToWorkspace')}
          path="agents.defaults.restrict_to_workspace"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.restrict_to_workspace')}
        >
          <BooleanInput
            id="agents.defaults.restrict_to_workspace"
            value={config.agents.defaults.restrict_to_workspace}
            onChange={(v) => updateField('agents.defaults.restrict_to_workspace', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.provider')}
          path="agents.defaults.provider"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.provider')}
          error={getErrorForPath(validationErrors, 'agents.defaults.provider')}
          required
        >
          <TextInput
            id="agents.defaults.provider"
            value={config.agents.defaults.provider}
            onChange={(v) => updateField('agents.defaults.provider', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.model')}
          path="agents.defaults.model"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.model')}
          error={getErrorForPath(validationErrors, 'agents.defaults.model')}
          required
        >
          <SearchableSelect
            ariaLabel={t('settings.fields.model')}
            buttonLabel={t('settings.fields.model')}
            direction="down"
            emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
            groups={getGroupsForAgent}
            onChange={(v) => updateField('agents.defaults.model', v)}
            options={getOptionsForAgent}
            placeholder={getDefaultModel() || t('settings.selectModel')}
            searchAriaLabel={`${t('settings.fields.model')} buscar`}
            searchPlaceholder={t('settings.fields.model')}
            value={getDefaultModel()}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.modelFallbacks')}
          path="agents.defaults.model_fallbacks"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.model_fallbacks')}
          description={t('settings.descriptions.modelFallbacks')}
        >
          <StringListEditor
            id="agents.defaults.model_fallbacks"
            value={config.agents.defaults.model_fallbacks || []}
            onChange={(v) => updateField('agents.defaults.model_fallbacks', v)}
            options={getOptionsForAgent}
            groups={getGroupsForAgent}
            emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
            loading={isLoadingModels}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.imageModel')}
          path="agents.defaults.image_model"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.image_model')}
          description={t('settings.descriptions.imageModel')}
        >
          <SearchableSelect
            ariaLabel={t('settings.fields.imageModel')}
            buttonLabel={t('settings.fields.imageModel')}
            direction="down"
            emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
            groups={modelGroups}
            onChange={(v) => updateField('agents.defaults.image_model', v || undefined)}
            options={modelOptions}
            placeholder={getDefaultImageModel() || t('settings.selectModel')}
            searchAriaLabel={`${t('settings.fields.imageModel')} buscar`}
            searchPlaceholder={t('settings.fields.imageModel')}
            value={getDefaultImageModel()}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.imageModelFallbacks')}
          path="agents.defaults.image_model_fallbacks"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.image_model_fallbacks')}
          description={t('settings.descriptions.imageModelFallbacks')}
        >
          <StringListEditor
            id="agents.defaults.image_model_fallbacks"
            value={config.agents.defaults.image_model_fallbacks || []}
            onChange={(v) => updateField('agents.defaults.image_model_fallbacks', v)}
            options={modelOptions}
            groups={modelGroups}
            emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
            loading={isLoadingModels}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.maxTokens')}
          path="agents.defaults.max_tokens"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.max_tokens')}
          error={getErrorForPath(validationErrors, 'agents.defaults.max_tokens')}
        >
          <NumberInput
            id="agents.defaults.max_tokens"
            value={config.agents.defaults.max_tokens}
            onChange={(v) => updateField('agents.defaults.max_tokens', v)}
            min={1}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.temperature')}
          path="agents.defaults.temperature"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.temperature')}
          description={t('settings.descriptions.temperature')}
        >
          <NumberInput
            id="agents.defaults.temperature"
            value={config.agents.defaults.temperature ?? 0}
            onChange={(v) => updateField('agents.defaults.temperature', v === 0 ? undefined : v)}
            min={0}
            max={2}
            step={0.1}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.maxToolIterations')}
          path="agents.defaults.max_tool_iterations"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.max_tool_iterations')}
        >
          <NumberInput
            id="agents.defaults.max_tool_iterations"
            value={config.agents.defaults.max_tool_iterations}
            onChange={(v) => updateField('agents.defaults.max_tool_iterations', v)}
            min={1}
            max={100}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.maxReadLines')}
          path="agents.defaults.max_read_lines"
          isDirty={isDirtyPath(dirtyPaths, 'agents.defaults.max_read_lines')}
          description={t('settings.descriptions.maxReadLines')}
        >
          <NumberInput
            id="agents.defaults.max_read_lines"
            value={config.agents.defaults.max_read_lines}
            onChange={(v) => updateField('agents.defaults.max_read_lines', v)}
            min={1}
            max={5000}
          />
        </SettingsField>
      </SettingsSection>
    </div>
  )
}
