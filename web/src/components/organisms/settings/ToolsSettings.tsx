import { useSettings } from '../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../hooks/useSettingsHelpers'
import type { SecretValue } from '../../../lib/types'
import {
  BooleanInput,
  NumberInput,
  SecretInput,
  SelectInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../molecules'

export function ToolsSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, t } = useSettings()

  if (!draftConfig) return null
  const config = draftConfig

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.webTools')}>
        <SettingsField
          label={t('settings.fields.braveEnabled')}
          path="tools.web.brave.enabled"
          isDirty={isDirtyPath(dirtyPaths, 'tools.web.brave.enabled')}
        >
          <BooleanInput
            id="tools.web.brave.enabled"
            value={config.tools.web.brave.enabled}
            onChange={(v) => updateField('tools.web.brave.enabled', v)}
          />
        </SettingsField>
        {config.tools.web.brave.enabled && (
          <SettingsField
            label={t('settings.fields.braveApiKey')}
            path="tools.web.brave.api_key"
            isDirty={isDirtyPath(dirtyPaths, 'tools.web.brave.api_key')}
          >
            <SecretInput
              id="tools.web.brave.api_key"
              value={config.tools.web.brave.api_key}
              onChange={(v: SecretValue) =>
                updateSecretField('tools.web.brave.api_key', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
        )}
        <SettingsField
          label={t('settings.fields.braveMaxResults')}
          path="tools.web.brave.max_results"
          isDirty={isDirtyPath(dirtyPaths, 'tools.web.brave.max_results')}
        >
          <NumberInput
            id="tools.web.brave.max_results"
            value={config.tools.web.brave.max_results}
            onChange={(v) => updateField('tools.web.brave.max_results', v)}
            min={1}
            max={50}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.duckduckgoEnabled')}
          path="tools.web.duckduckgo.enabled"
          isDirty={isDirtyPath(dirtyPaths, 'tools.web.duckduckgo.enabled')}
        >
          <BooleanInput
            id="tools.web.duckduckgo.enabled"
            value={config.tools.web.duckduckgo.enabled}
            onChange={(v) => updateField('tools.web.duckduckgo.enabled', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.duckduckgoMaxResults')}
          path="tools.web.duckduckgo.max_results"
          isDirty={isDirtyPath(dirtyPaths, 'tools.web.duckduckgo.max_results')}
        >
          <NumberInput
            id="tools.web.duckduckgo.max_results"
            value={config.tools.web.duckduckgo.max_results}
            onChange={(v) => updateField('tools.web.duckduckgo.max_results', v)}
            min={1}
            max={50}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.perplexityEnabled')}
          path="tools.web.perplexity.enabled"
          isDirty={isDirtyPath(dirtyPaths, 'tools.web.perplexity.enabled')}
        >
          <BooleanInput
            id="tools.web.perplexity.enabled"
            value={config.tools.web.perplexity.enabled}
            onChange={(v) => updateField('tools.web.perplexity.enabled', v)}
          />
        </SettingsField>
        {config.tools.web.perplexity.enabled && (
          <SettingsField
            label={t('settings.fields.perplexityApiKey')}
            path="tools.web.perplexity.api_key"
            isDirty={isDirtyPath(dirtyPaths, 'tools.web.perplexity.api_key')}
          >
            <SecretInput
              id="tools.web.perplexity.api_key"
              value={config.tools.web.perplexity.api_key}
              onChange={(v: SecretValue) =>
                updateSecretField('tools.web.perplexity.api_key', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
        )}
      </SettingsSection>

      <SettingsSection title={t('settings.sections.searxng')}>
        <SettingsField
          label={t('settings.fields.searxngEnabled')}
          path="tools.web.searxng.enabled"
          isDirty={isDirtyPath(dirtyPaths, 'tools.web.searxng.enabled')}
        >
          <BooleanInput
            id="tools.web.searxng.enabled"
            value={config.tools.web.searxng.enabled}
            onChange={(v) => updateField('tools.web.searxng.enabled', v)}
          />
        </SettingsField>
        {config.tools.web.searxng.enabled && (
          <>
            <SettingsField
              label={t('settings.fields.searxngInstanceUrl')}
              path="tools.web.searxng.instance_url"
              isDirty={isDirtyPath(dirtyPaths, 'tools.web.searxng.instance_url')}
            >
              <TextInput
                id="tools.web.searxng.instance_url"
                value={config.tools.web.searxng.instance_url}
                onChange={(v) => updateField('tools.web.searxng.instance_url', v)}
                placeholder="https://searxng.example.com"
              />
            </SettingsField>
            <SettingsField
              label={t('settings.fields.searxngCategories')}
              path="tools.web.searxng.categories"
              isDirty={isDirtyPath(dirtyPaths, 'tools.web.searxng.categories')}
            >
              <TextInput
                id="tools.web.searxng.categories"
                value={config.tools.web.searxng.categories}
                onChange={(v) => updateField('tools.web.searxng.categories', v)}
                placeholder="general"
              />
            </SettingsField>
            <SettingsField
              label={t('settings.fields.searxngLanguage')}
              path="tools.web.searxng.language"
              isDirty={isDirtyPath(dirtyPaths, 'tools.web.searxng.language')}
            >
              <SelectInput
                id="tools.web.searxng.language"
                value={config.tools.web.searxng.language}
                onChange={(v) => updateField('tools.web.searxng.language', v)}
                options={[
                  { value: 'auto', label: 'Auto' },
                  { value: 'en', label: 'English' },
                  { value: 'es', label: 'Español' },
                  { value: 'pt', label: 'Português' },
                  { value: 'fr', label: 'Français' },
                  { value: 'de', label: 'Deutsch' },
                  { value: 'zh', label: '中文' },
                  { value: 'ja', label: '日本語' },
                ]}
              />
            </SettingsField>
            <SettingsField
              label={t('settings.fields.searxngSafeSearch')}
              path="tools.web.searxng.safesearch"
              isDirty={isDirtyPath(dirtyPaths, 'tools.web.searxng.safesearch')}
            >
              <SelectInput
                id="tools.web.searxng.safesearch"
                value={String(config.tools.web.searxng.safesearch)}
                onChange={(v) => updateField('tools.web.searxng.safesearch', Number(v))}
                options={[
                  { value: '0', label: 'None' },
                  { value: '1', label: 'Moderate' },
                  { value: '2', label: 'Strict' },
                ]}
              />
            </SettingsField>
            <SettingsField
              label={t('settings.fields.searxngMaxResults')}
              path="tools.web.searxng.max_results"
              isDirty={isDirtyPath(dirtyPaths, 'tools.web.searxng.max_results')}
            >
              <NumberInput
                id="tools.web.searxng.max_results"
                value={config.tools.web.searxng.max_results}
                onChange={(v) => updateField('tools.web.searxng.max_results', v)}
                min={1}
                max={50}
              />
            </SettingsField>
          </>
        )}
      </SettingsSection>

      <SettingsSection title={t('settings.sections.execTools')}>
        <SettingsField
          label={t('settings.fields.enableDenyPatterns')}
          path="tools.exec.enable_deny_patterns"
          isDirty={isDirtyPath(dirtyPaths, 'tools.exec.enable_deny_patterns')}
        >
          <BooleanInput
            id="tools.exec.enable_deny_patterns"
            value={config.tools.exec.enable_deny_patterns}
            onChange={(v) => updateField('tools.exec.enable_deny_patterns', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.customDenyPatterns')}
          path="tools.exec.custom_deny_patterns"
          isDirty={isDirtyPath(dirtyPaths, 'tools.exec.custom_deny_patterns')}
        >
          <StringListEditor
            id="tools.exec.custom_deny_patterns"
            value={config.tools.exec.custom_deny_patterns || []}
            onChange={(v) => updateField('tools.exec.custom_deny_patterns', v)}
          />
        </SettingsField>
      </SettingsSection>

      <SettingsSection title={t('settings.sections.cronTools')}>
        <SettingsField
          label={t('settings.fields.execTimeoutMinutes')}
          path="tools.cron.exec_timeout_minutes"
          description={t('settings.descriptions.execTimeoutMinutes')}
          isDirty={isDirtyPath(dirtyPaths, 'tools.cron.exec_timeout_minutes')}
        >
          <NumberInput
            id="tools.cron.exec_timeout_minutes"
            value={config.tools.cron.exec_timeout_minutes}
            onChange={(v) => updateField('tools.cron.exec_timeout_minutes', v)}
            min={0}
          />
        </SettingsField>
      </SettingsSection>
    </div>
  )
}
