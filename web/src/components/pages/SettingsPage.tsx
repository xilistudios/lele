import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useAvailableModels } from '../../hooks/useAvailableModels'
import { useSettingsConfig } from '../../hooks/useSettingsConfig'
import type { EditableConfig, SecretValue } from '../../lib/types'
import { SearchableSelect } from '../molecules/SearchableSelect'
import { DiagnosticsPanel } from '../organisms/DiagnosticsPanel'
import { Sidebar } from '../organisms/Sidebar'
import {
  BooleanInput,
  KeyValueEditor,
  NumberInput,
  SecretInput,
  SelectInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../settings'

type Props = {
  onLogout: () => void
}

type SettingsTab =
  | 'general'
  | 'agents'
  | 'session'
  | 'providers'
  | 'channels'
  | 'tools'
  | 'system'
  | 'advanced'
  | 'diagnostics'

function isDirtyPath(dirtyPaths: Set<string>, path: string): boolean {
  return dirtyPaths.has(path) || dirtyPaths.has(path.split('.').slice(0, -1).join('.'))
}

function getErrorForPath(
  errors: Array<{ path: string; message: string }>,
  path: string,
): string | undefined {
  const error = errors.find((e) => e.path === path || e.path.startsWith(`${path}.`))
  return error?.message
}

const INPUT_CLS =
  'w-full rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 text-xs text-[#e0e0e0] placeholder-[#555] focus:border-blue-500 focus:outline-none disabled:opacity-50'
const CARD_CLS = 'rounded border border-[#2e2e2e] bg-[#1a1a1a] p-4'
const REMOVE_BTN_CLS = 'text-rose-400 transition-colors hover:text-rose-300 disabled:opacity-50'
const ADD_BTN_CLS =
  'rounded bg-blue-600 px-3 py-2 text-xs text-white transition-colors hover:bg-blue-500 disabled:opacity-50'

function getAgentModelPrimary(
  agentModel: string | { primary?: string; fallbacks?: string[] } | undefined,
): string {
  if (!agentModel) return ''
  if (typeof agentModel === 'string') return agentModel
  return agentModel.primary || ''
}

export function SettingsPage({ onLogout }: Props) {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { sidebarOpen, onToggleSidebar } = useAppLogicContext()
  const [activeTab, setActiveTab] = useState<SettingsTab>('general')
  const [showRawJson, setShowRawJson] = useState(false)
  const [newProviderName, setNewProviderName] = useState('')
  const [newAgentId, setNewAgentId] = useState('')

  const {
    draftConfig,
    metadata,
    dirtyPaths,
    validationErrors,
    saveState,
    saveError,
    updateField,
    updateSecretField,
    replaceDraft,
    reset,
    validate,
    save,
    isDirty,
    isLoading,
    hasErrors,
  } = useSettingsConfig(api)

  const { available, groups, isLoading: isLoadingModels } = useAvailableModels(api)

  const modelOptions = useMemo(() => {
    return available.map((model) => ({ value: model, label: model }))
  }, [available])

  const modelGroups = useMemo(() => {
    return groups.map((group) => ({
      label: group.provider,
      options: group.models,
    }))
  }, [groups])

  const getDefaultModel = (): string => {
    return draftConfig?.agents?.defaults?.model || ''
  }

  const getDefaultImageModel = (): string => {
    return draftConfig?.agents?.defaults?.image_model || ''
  }

  const getOptionsForAgent = useMemo(() => {
    const list = draftConfig?.agents?.list || []
    const allAgentModels = list.map((a) => getAgentModelPrimary(a.model)).filter(Boolean)
    const existingValues = new Set(modelOptions.map((o) => o.value))
    const opts = [...modelOptions]
    for (const m of allAgentModels) {
      if (!existingValues.has(m)) {
        opts.push({ value: m, label: m })
      }
    }
    return opts
  }, [modelOptions, draftConfig])

  const getGroupsForAgent = useMemo(() => {
    const list = draftConfig?.agents?.list || []
    const allAgentModels = list.map((a) => getAgentModelPrimary(a.model)).filter(Boolean)
    const existingValues = new Set(modelGroups.flatMap((g) => g.options.map((o) => o.value)))
    const groups = [...modelGroups]
    const newModels = allAgentModels.filter((m) => !existingValues.has(m))
    if (newModels.length > 0) {
      groups.push({
        label: 'Configured Models',
        options: newModels.map((m) => ({ value: m, label: m })),
      })
    }
    return groups
  }, [modelGroups, draftConfig])

  const handleSave = async () => {
    const isValid = await validate()
    if (isValid) {
      await save()
    }
  }

  const isRestartRequired = (section: string): boolean => {
    return (
      metadata?.restart_required_sections?.some(
        (item) =>
          item === section || item.startsWith(`${section}.`) || section.startsWith(`${item}.`),
      ) ?? false
    )
  }

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: 'general', label: t('settings.tabs.general') },
    { id: 'agents', label: t('settings.tabs.agents') },
    { id: 'session', label: t('settings.tabs.session') },
    { id: 'providers', label: t('settings.tabs.providers') },
    { id: 'channels', label: t('settings.tabs.channels') },
    { id: 'tools', label: t('settings.tabs.tools') },
    { id: 'system', label: t('settings.tabs.system') },
    { id: 'advanced', label: t('settings.tabs.advanced') },
    { id: 'diagnostics', label: t('settings.tabs.diagnostics') },
  ]

  const renderGeneralSettings = () => {
    if (!draftConfig) return null
    const config = draftConfig

    return (
      <div className="space-y-6">
        <SettingsSection
          title={t('settings.sections.gateway')}
          isRestartRequired={isRestartRequired('gateway')}
        >
          <SettingsField
            label={t('settings.fields.gatewayHost')}
            path="gateway.host"
            isDirty={isDirtyPath(dirtyPaths, 'gateway.host')}
            error={getErrorForPath(validationErrors, 'gateway.host')}
          >
            <TextInput
              id="gateway.host"
              value={config.gateway.host}
              onChange={(v) => updateField('gateway.host', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.gatewayPort')}
            path="gateway.port"
            isDirty={isDirtyPath(dirtyPaths, 'gateway.port')}
            error={getErrorForPath(validationErrors, 'gateway.port')}
          >
            <NumberInput
              id="gateway.port"
              value={config.gateway.port}
              onChange={(v) => updateField('gateway.port', v)}
              min={1}
              max={65535}
            />
          </SettingsField>
        </SettingsSection>

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
        </SettingsSection>
      </div>
    )
  }

  const renderAgentsSettings = () => {
    if (!draftConfig) return null
    const list = draftConfig.agents.list || []

    const addAgent = () => {
      const id = newAgentId.trim()
      if (!id) return
      updateField('agents.list', [...list, { id, default: false, skills: [] }])
      setNewAgentId('')
    }

    const removeAgent = (index: number) => {
      updateField(
        'agents.list',
        list.filter((_, i) => i !== index),
      )
    }

    return (
      <div className="space-y-6">
        <SettingsSection title={t('settings.sections.agentsList')}>
          <div className="mb-4 flex gap-2">
            <input
              type="text"
              value={newAgentId}
              onChange={(e) => setNewAgentId(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  addAgent()
                }
              }}
              placeholder={t('settings.agentIdPlaceholder')}
              className={INPUT_CLS}
            />
            <button
              type="button"
              onClick={addAgent}
              disabled={!newAgentId.trim()}
              className={ADD_BTN_CLS}
            >
              {t('settings.addAgent')}
            </button>
          </div>

          {list.length === 0 && <p className="text-xs text-[#666]">{t('settings.noAgents')}</p>}

          {list.map((agent, index) => (
            <div key={agent.id} className={CARD_CLS}>
              <div className="mb-3 flex items-center justify-between">
                <span className="font-mono text-xs font-medium text-white">{agent.id}</span>
                <button
                  type="button"
                  onClick={() => removeAgent(index)}
                  className={REMOVE_BTN_CLS}
                  aria-label={t('settings.removeAgent')}
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    aria-hidden="true"
                  >
                    <path d="M18 6L6 18M6 6l12 12" />
                  </svg>
                </button>
              </div>
              <div className="space-y-3">
                <SettingsField
                  label={t('settings.fields.agentName')}
                  path={`agents.list.${index}.name`}
                  isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.name`)}
                >
                  <TextInput
                    id={`agents.list.${index}.name`}
                    value={agent.name || ''}
                    onChange={(v) => updateField(`agents.list.${index}.name`, v || undefined)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.agentDefault')}
                  path={`agents.list.${index}.default`}
                  isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.default`)}
                >
                  <BooleanInput
                    id={`agents.list.${index}.default`}
                    value={agent.default || false}
                    onChange={(v) => updateField(`agents.list.${index}.default`, v)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.agentWorkspace')}
                  path={`agents.list.${index}.workspace`}
                  isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.workspace`)}
                >
                  <TextInput
                    id={`agents.list.${index}.workspace`}
                    value={agent.workspace || ''}
                    onChange={(v) => updateField(`agents.list.${index}.workspace`, v || undefined)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.agentModelPrimary')}
                  path={`agents.list.${index}.model.primary`}
                  isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.model.primary`)}
                >
                  <SearchableSelect
                    ariaLabel={t('settings.fields.agentModelPrimary')}
                    buttonLabel={t('settings.fields.agentModelPrimary')}
                    direction="down"
                    emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
                    groups={getGroupsForAgent}
                    onChange={(v) =>
                      updateField(`agents.list.${index}.model`, { ...agent.model, primary: v })
                    }
                    options={getOptionsForAgent}
                    placeholder={getAgentModelPrimary(agent.model) || t('settings.selectModel')}
                    searchAriaLabel={`${t('settings.fields.agentModelPrimary')} buscar`}
                    searchPlaceholder={t('settings.fields.agentModelPrimary')}
                    value={getAgentModelPrimary(agent.model)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.agentModelFallbacks')}
                  path={`agents.list.${index}.model.fallbacks`}
                  isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.model.fallbacks`)}
                >
                  <StringListEditor
                    id={`agents.list.${index}.model.fallbacks`}
                    value={agent.model?.fallbacks || []}
                    onChange={(v) =>
                      updateField(`agents.list.${index}.model`, {
                        primary: agent.model?.primary || '',
                        fallbacks: v,
                      })
                    }
                    options={getOptionsForAgent}
                    groups={getGroupsForAgent}
                    emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
                    loading={isLoadingModels}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.agentSkills')}
                  path={`agents.list.${index}.skills`}
                  isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.skills`)}
                >
                  <StringListEditor
                    id={`agents.list.${index}.skills`}
                    value={agent.skills || []}
                    onChange={(v) => updateField(`agents.list.${index}.skills`, v)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.agentTemperature')}
                  path={`agents.list.${index}.temperature`}
                  isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.temperature`)}
                >
                  <NumberInput
                    id={`agents.list.${index}.temperature`}
                    value={agent.temperature ?? 0}
                    min={0}
                    max={2}
                    step={0.1}
                    onChange={(v) =>
                      updateField(`agents.list.${index}.temperature`, v === 0 ? undefined : v)
                    }
                  />
                </SettingsField>
              </div>
            </div>
          ))}
        </SettingsSection>
      </div>
    )
  }

  const renderSessionSettings = () => {
    if (!draftConfig?.session) return null
    const session = draftConfig.session

    return (
      <div className="space-y-6">
        <SettingsSection title={t('settings.sections.session')}>
          <SettingsField
            label={t('settings.fields.ephemeral')}
            path="session.ephemeral"
            isDirty={isDirtyPath(dirtyPaths, 'session.ephemeral')}
          >
            <BooleanInput
              id="session.ephemeral"
              value={session.ephemeral}
              onChange={(v) => updateField('session.ephemeral', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.ephemeralThreshold')}
            path="session.ephemeral_threshold"
            description={t('settings.descriptions.ephemeralThreshold')}
            isDirty={isDirtyPath(dirtyPaths, 'session.ephemeral_threshold')}
          >
            <NumberInput
              id="session.ephemeral_threshold"
              value={session.ephemeral_threshold}
              onChange={(v) => updateField('session.ephemeral_threshold', v)}
              min={60}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.dmScope')}
            path="session.dm_scope"
            isDirty={isDirtyPath(dirtyPaths, 'session.dm_scope')}
            description={t('settings.descriptions.dmScope')}
          >
            <TextInput
              id="session.dm_scope"
              value={session.dm_scope || ''}
              onChange={(v) => updateField('session.dm_scope', v || undefined)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.identityLinks')}
            path="session.identity_links"
            isDirty={isDirtyPath(dirtyPaths, 'session.identity_links')}
            description={t('settings.descriptions.identityLinks')}
          >
            <KeyValueEditor
              id="session.identity_links"
              value={session.identity_links || {}}
              onChange={(v) => updateField('session.identity_links', v)}
              keyPlaceholder={t('settings.identityKeyPlaceholder')}
            />
          </SettingsField>
        </SettingsSection>
      </div>
    )
  }

  const renderProvidersSettings = () => {
    if (!draftConfig) return null
    const providers = draftConfig.providers || {}
    const names = Object.keys(providers)

    const addProvider = () => {
      const name = newProviderName.trim()
      if (!name) return
      updateField('providers', {
        ...providers,
        [name]: { api_key: { mode: 'empty', has_env_var: false }, api_base: '' },
      })
      setNewProviderName('')
    }

    const removeProvider = (name: string) => {
      const updated = { ...providers }
      delete updated[name]
      updateField('providers', updated)
    }

    return (
      <div className="space-y-6">
        <SettingsSection title={t('settings.sections.namedProviders')}>
          <div className="mb-4 flex gap-2">
            <input
              type="text"
              value={newProviderName}
              onChange={(e) => setNewProviderName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  addProvider()
                }
              }}
              placeholder={t('settings.providerNamePlaceholder')}
              className={INPUT_CLS}
            />
            <button
              type="button"
              onClick={addProvider}
              disabled={!newProviderName.trim()}
              className={ADD_BTN_CLS}
            >
              {t('settings.addProvider')}
            </button>
          </div>

          {names.length === 0 && <p className="text-xs text-[#666]">{t('settings.noProviders')}</p>}

          {names.map((name) => {
            const prov = providers[name]
            return (
              <div key={name} className={CARD_CLS}>
                <div className="mb-3 flex items-center justify-between">
                  <span className="font-mono text-xs font-medium text-white">{name}</span>
                  <button
                    type="button"
                    onClick={() => removeProvider(name)}
                    className={REMOVE_BTN_CLS}
                    aria-label={t('settings.removeProvider')}
                  >
                    <svg
                      width="14"
                      height="14"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      aria-hidden="true"
                    >
                      <path d="M18 6L6 18M6 6l12 12" />
                    </svg>
                  </button>
                </div>
                <div className="space-y-3">
                  <SettingsField
                    label={t('settings.fields.providerType')}
                    path={`providers.${name}.type`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.type`)}
                  >
                    <TextInput
                      id={`providers.${name}.type`}
                      value={prov.type || ''}
                      onChange={(v) => updateField(`providers.${name}.type`, v || undefined)}
                    />
                  </SettingsField>
                  <SettingsField
                    label={t('settings.fields.providerApiBase')}
                    path={`providers.${name}.api_base`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.api_base`)}
                  >
                    <TextInput
                      id={`providers.${name}.api_base`}
                      value={prov.api_base}
                      onChange={(v) => updateField(`providers.${name}.api_base`, v)}
                    />
                  </SettingsField>
                  <SettingsField
                    label={t('settings.fields.providerApiKey')}
                    path={`providers.${name}.api_key`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.api_key`)}
                  >
                    <SecretInput
                      id={`providers.${name}.api_key`}
                      value={prov.api_key}
                      onChange={(v: SecretValue) =>
                        updateSecretField(`providers.${name}.api_key`, v.mode, v.value, v.env_name)
                      }
                    />
                  </SettingsField>
                  <SettingsField
                    label={t('settings.fields.providerProxy')}
                    path={`providers.${name}.proxy`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.proxy`)}
                  >
                    <TextInput
                      id={`providers.${name}.proxy`}
                      value={prov.proxy || ''}
                      onChange={(v) => updateField(`providers.${name}.proxy`, v || undefined)}
                    />
                  </SettingsField>
                  <SettingsField
                    label={t('settings.fields.providerAuthMethod')}
                    path={`providers.${name}.auth_method`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.auth_method`)}
                  >
                    <TextInput
                      id={`providers.${name}.auth_method`}
                      value={prov.auth_method || ''}
                      onChange={(v) => updateField(`providers.${name}.auth_method`, v || undefined)}
                    />
                  </SettingsField>
                  <SettingsField
                    label={t('settings.fields.providerConnectMode')}
                    path={`providers.${name}.connect_mode`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.connect_mode`)}
                  >
                    <TextInput
                      id={`providers.${name}.connect_mode`}
                      value={prov.connect_mode || ''}
                      onChange={(v) =>
                        updateField(`providers.${name}.connect_mode`, v || undefined)
                      }
                    />
                  </SettingsField>
                  <SettingsField
                    label={t('settings.fields.providerWebSearch')}
                    path={`providers.${name}.web_search`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.web_search`)}
                  >
                    <BooleanInput
                      id={`providers.${name}.web_search`}
                      value={prov.web_search || false}
                      onChange={(v) => updateField(`providers.${name}.web_search`, v)}
                    />
                  </SettingsField>
                  <SettingsField
                    label={t('settings.fields.providerModels')}
                    path={`providers.${name}.models`}
                    isDirty={isDirtyPath(dirtyPaths, `providers.${name}.models`)}
                  >
                    <ProviderModelsEditor
                      name={name}
                      models={prov.models || {}}
                      onChange={(v) => updateField(`providers.${name}.models`, v)}
                    />
                  </SettingsField>
                </div>
              </div>
            )
          })}
        </SettingsSection>
      </div>
    )
  }

  const renderChannelSettings = () => {
    if (!draftConfig) return null
    const ch = draftConfig.channels

    return (
      <div className="space-y-6">
        <SettingsSection
          title={t('settings.sections.nativeChannel')}
          isRestartRequired={isRestartRequired('channels.native')}
        >
          <SettingsField
            label={t('settings.fields.nativeEnabled')}
            path="channels.native.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.enabled')}
          >
            <BooleanInput
              id="channels.native.enabled"
              value={ch.native.enabled}
              onChange={(v) => updateField('channels.native.enabled', v)}
            />
          </SettingsField>
          {ch.native.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.nativeHost')}
                path="channels.native.host"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.host')}
              >
                <TextInput
                  id="channels.native.host"
                  value={ch.native.host}
                  onChange={(v) => updateField('channels.native.host', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativePort')}
                path="channels.native.port"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.port')}
              >
                <NumberInput
                  id="channels.native.port"
                  value={ch.native.port}
                  onChange={(v) => updateField('channels.native.port', v)}
                  min={1}
                  max={65535}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.maxClients')}
                path="channels.native.max_clients"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.max_clients')}
              >
                <NumberInput
                  id="channels.native.max_clients"
                  value={ch.native.max_clients}
                  onChange={(v) => updateField('channels.native.max_clients', v)}
                  min={1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeTokenExpiryDays')}
                path="channels.native.token_expiry_days"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.token_expiry_days')}
              >
                <NumberInput
                  id="channels.native.token_expiry_days"
                  value={ch.native.token_expiry_days}
                  onChange={(v) => updateField('channels.native.token_expiry_days', v)}
                  min={1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativePinExpiryMinutes')}
                path="channels.native.pin_expiry_minutes"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.pin_expiry_minutes')}
              >
                <NumberInput
                  id="channels.native.pin_expiry_minutes"
                  value={ch.native.pin_expiry_minutes}
                  onChange={(v) => updateField('channels.native.pin_expiry_minutes', v)}
                  min={1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeSessionExpiryDays')}
                path="channels.native.session_expiry_days"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.session_expiry_days')}
              >
                <NumberInput
                  id="channels.native.session_expiry_days"
                  value={ch.native.session_expiry_days}
                  onChange={(v) => updateField('channels.native.session_expiry_days', v)}
                  min={1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeMaxUploadSizeMb')}
                path="channels.native.max_upload_size_mb"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.max_upload_size_mb')}
              >
                <NumberInput
                  id="channels.native.max_upload_size_mb"
                  value={ch.native.max_upload_size_mb}
                  onChange={(v) => updateField('channels.native.max_upload_size_mb', v)}
                  min={1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeUploadTtlHours')}
                path="channels.native.upload_ttl_hours"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.upload_ttl_hours')}
              >
                <NumberInput
                  id="channels.native.upload_ttl_hours"
                  value={ch.native.upload_ttl_hours}
                  onChange={(v) => updateField('channels.native.upload_ttl_hours', v)}
                  min={1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeCorsOrigins')}
                path="channels.native.cors_origins"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.cors_origins')}
              >
                <StringListEditor
                  id="channels.native.cors_origins"
                  value={ch.native.cors_origins || []}
                  onChange={(v) => updateField('channels.native.cors_origins', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.telegramChannel')}
          isRestartRequired={isRestartRequired('channels.telegram')}
        >
          <SettingsField
            label={t('settings.fields.telegramEnabled')}
            path="channels.telegram.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.enabled')}
          >
            <BooleanInput
              id="channels.telegram.enabled"
              value={ch.telegram.enabled}
              onChange={(v) => updateField('channels.telegram.enabled', v)}
            />
          </SettingsField>
          {ch.telegram.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.telegramToken')}
                path="channels.telegram.token"
                isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.token')}
              >
                <SecretInput
                  id="channels.telegram.token"
                  value={ch.telegram.token}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.telegram.token', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.telegramProxy')}
                path="channels.telegram.proxy"
                isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.proxy')}
              >
                <TextInput
                  id="channels.telegram.proxy"
                  value={ch.telegram.proxy || ''}
                  onChange={(v) => updateField('channels.telegram.proxy', v || undefined)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.telegramAllowFrom')}
                path="channels.telegram.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.allow_from')}
              >
                <StringListEditor
                  id="channels.telegram.allow_from"
                  value={ch.telegram.allow_from || []}
                  onChange={(v) => updateField('channels.telegram.allow_from', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.telegramVerbose')}
                path="channels.telegram.verbose"
                isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.verbose')}
              >
                <SelectInput
                  id="channels.telegram.verbose"
                  value={ch.telegram.verbose || 'off'}
                  onChange={(v) => updateField('channels.telegram.verbose', v)}
                  options={[
                    { value: 'off', label: t('settings.options.off') },
                    { value: 'basic', label: t('settings.options.basic') },
                    { value: 'full', label: t('settings.options.full') },
                  ]}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.discordChannel')}
          isRestartRequired={isRestartRequired('channels.discord')}
        >
          <SettingsField
            label={t('settings.fields.discordEnabled')}
            path="channels.discord.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.discord.enabled')}
          >
            <BooleanInput
              id="channels.discord.enabled"
              value={ch.discord.enabled}
              onChange={(v) => updateField('channels.discord.enabled', v)}
            />
          </SettingsField>
          {ch.discord.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.discordToken')}
                path="channels.discord.token"
                isDirty={isDirtyPath(dirtyPaths, 'channels.discord.token')}
              >
                <SecretInput
                  id="channels.discord.token"
                  value={ch.discord.token}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.discord.token', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.discordAllowFrom')}
                path="channels.discord.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.discord.allow_from')}
              >
                <StringListEditor
                  id="channels.discord.allow_from"
                  value={ch.discord.allow_from || []}
                  onChange={(v) => updateField('channels.discord.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.whatsappChannel')}
          isRestartRequired={isRestartRequired('channels.whatsapp')}
        >
          <SettingsField
            label={t('settings.fields.whatsappEnabled')}
            path="channels.whatsapp.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.whatsapp.enabled')}
          >
            <BooleanInput
              id="channels.whatsapp.enabled"
              value={ch.whatsapp.enabled}
              onChange={(v) => updateField('channels.whatsapp.enabled', v)}
            />
          </SettingsField>
          {ch.whatsapp.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.whatsappBridgeUrl')}
                path="channels.whatsapp.bridge_url"
                isDirty={isDirtyPath(dirtyPaths, 'channels.whatsapp.bridge_url')}
              >
                <TextInput
                  id="channels.whatsapp.bridge_url"
                  value={ch.whatsapp.bridge_url}
                  onChange={(v) => updateField('channels.whatsapp.bridge_url', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.whatsappAllowFrom')}
                path="channels.whatsapp.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.whatsapp.allow_from')}
              >
                <StringListEditor
                  id="channels.whatsapp.allow_from"
                  value={ch.whatsapp.allow_from || []}
                  onChange={(v) => updateField('channels.whatsapp.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.feishuChannel')}
          isRestartRequired={isRestartRequired('channels.feishu')}
        >
          <SettingsField
            label={t('settings.fields.feishuEnabled')}
            path="channels.feishu.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.enabled')}
          >
            <BooleanInput
              id="channels.feishu.enabled"
              value={ch.feishu.enabled}
              onChange={(v) => updateField('channels.feishu.enabled', v)}
            />
          </SettingsField>
          {ch.feishu.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.feishuAppId')}
                path="channels.feishu.app_id"
                isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.app_id')}
              >
                <SecretInput
                  id="channels.feishu.app_id"
                  value={ch.feishu.app_id}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.feishu.app_id', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.feishuAppSecret')}
                path="channels.feishu.app_secret"
                isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.app_secret')}
              >
                <SecretInput
                  id="channels.feishu.app_secret"
                  value={ch.feishu.app_secret}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.feishu.app_secret', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.feishuEncryptKey')}
                path="channels.feishu.encrypt_key"
                isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.encrypt_key')}
              >
                <SecretInput
                  id="channels.feishu.encrypt_key"
                  value={ch.feishu.encrypt_key}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.feishu.encrypt_key', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.feishuVerificationToken')}
                path="channels.feishu.verification_token"
                isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.verification_token')}
              >
                <SecretInput
                  id="channels.feishu.verification_token"
                  value={ch.feishu.verification_token}
                  onChange={(v: SecretValue) =>
                    updateSecretField(
                      'channels.feishu.verification_token',
                      v.mode,
                      v.value,
                      v.env_name,
                    )
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.feishuAllowFrom')}
                path="channels.feishu.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.allow_from')}
              >
                <StringListEditor
                  id="channels.feishu.allow_from"
                  value={ch.feishu.allow_from || []}
                  onChange={(v) => updateField('channels.feishu.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.slackChannel')}
          isRestartRequired={isRestartRequired('channels.slack')}
        >
          <SettingsField
            label={t('settings.fields.slackEnabled')}
            path="channels.slack.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.slack.enabled')}
          >
            <BooleanInput
              id="channels.slack.enabled"
              value={ch.slack.enabled}
              onChange={(v) => updateField('channels.slack.enabled', v)}
            />
          </SettingsField>
          {ch.slack.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.slackBotToken')}
                path="channels.slack.bot_token"
                isDirty={isDirtyPath(dirtyPaths, 'channels.slack.bot_token')}
              >
                <SecretInput
                  id="channels.slack.bot_token"
                  value={ch.slack.bot_token}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.slack.bot_token', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.slackAppToken')}
                path="channels.slack.app_token"
                isDirty={isDirtyPath(dirtyPaths, 'channels.slack.app_token')}
              >
                <SecretInput
                  id="channels.slack.app_token"
                  value={ch.slack.app_token}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.slack.app_token', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.slackAllowFrom')}
                path="channels.slack.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.slack.allow_from')}
              >
                <StringListEditor
                  id="channels.slack.allow_from"
                  value={ch.slack.allow_from || []}
                  onChange={(v) => updateField('channels.slack.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.lineChannel')}
          isRestartRequired={isRestartRequired('channels.line')}
        >
          <SettingsField
            label={t('settings.fields.lineEnabled')}
            path="channels.line.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.line.enabled')}
          >
            <BooleanInput
              id="channels.line.enabled"
              value={ch.line.enabled}
              onChange={(v) => updateField('channels.line.enabled', v)}
            />
          </SettingsField>
          {ch.line.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.lineChannelSecret')}
                path="channels.line.channel_secret"
                isDirty={isDirtyPath(dirtyPaths, 'channels.line.channel_secret')}
              >
                <SecretInput
                  id="channels.line.channel_secret"
                  value={ch.line.channel_secret}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.line.channel_secret', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.lineChannelAccessToken')}
                path="channels.line.channel_access_token"
                isDirty={isDirtyPath(dirtyPaths, 'channels.line.channel_access_token')}
              >
                <SecretInput
                  id="channels.line.channel_access_token"
                  value={ch.line.channel_access_token}
                  onChange={(v: SecretValue) =>
                    updateSecretField(
                      'channels.line.channel_access_token',
                      v.mode,
                      v.value,
                      v.env_name,
                    )
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.lineWebhookHost')}
                path="channels.line.webhook_host"
                isDirty={isDirtyPath(dirtyPaths, 'channels.line.webhook_host')}
              >
                <TextInput
                  id="channels.line.webhook_host"
                  value={ch.line.webhook_host}
                  onChange={(v) => updateField('channels.line.webhook_host', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.lineWebhookPort')}
                path="channels.line.webhook_port"
                isDirty={isDirtyPath(dirtyPaths, 'channels.line.webhook_port')}
              >
                <NumberInput
                  id="channels.line.webhook_port"
                  value={ch.line.webhook_port}
                  onChange={(v) => updateField('channels.line.webhook_port', v)}
                  min={1}
                  max={65535}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.lineWebhookPath')}
                path="channels.line.webhook_path"
                isDirty={isDirtyPath(dirtyPaths, 'channels.line.webhook_path')}
              >
                <TextInput
                  id="channels.line.webhook_path"
                  value={ch.line.webhook_path}
                  onChange={(v) => updateField('channels.line.webhook_path', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.lineAllowFrom')}
                path="channels.line.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.line.allow_from')}
              >
                <StringListEditor
                  id="channels.line.allow_from"
                  value={ch.line.allow_from || []}
                  onChange={(v) => updateField('channels.line.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.onebotChannel')}
          isRestartRequired={isRestartRequired('channels.onebot')}
        >
          <SettingsField
            label={t('settings.fields.onebotEnabled')}
            path="channels.onebot.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.enabled')}
          >
            <BooleanInput
              id="channels.onebot.enabled"
              value={ch.onebot.enabled}
              onChange={(v) => updateField('channels.onebot.enabled', v)}
            />
          </SettingsField>
          {ch.onebot.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.onebotWsUrl')}
                path="channels.onebot.ws_url"
                isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.ws_url')}
              >
                <TextInput
                  id="channels.onebot.ws_url"
                  value={ch.onebot.ws_url}
                  onChange={(v) => updateField('channels.onebot.ws_url', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.onebotAccessToken')}
                path="channels.onebot.access_token"
                isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.access_token')}
              >
                <SecretInput
                  id="channels.onebot.access_token"
                  value={ch.onebot.access_token}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.onebot.access_token', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.onebotReconnectInterval')}
                path="channels.onebot.reconnect_interval"
                isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.reconnect_interval')}
              >
                <NumberInput
                  id="channels.onebot.reconnect_interval"
                  value={ch.onebot.reconnect_interval}
                  onChange={(v) => updateField('channels.onebot.reconnect_interval', v)}
                  min={1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.onebotGroupTriggerPrefix')}
                path="channels.onebot.group_trigger_prefix"
                isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.group_trigger_prefix')}
              >
                <StringListEditor
                  id="channels.onebot.group_trigger_prefix"
                  value={ch.onebot.group_trigger_prefix || []}
                  onChange={(v) => updateField('channels.onebot.group_trigger_prefix', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.onebotAllowFrom')}
                path="channels.onebot.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.allow_from')}
              >
                <StringListEditor
                  id="channels.onebot.allow_from"
                  value={ch.onebot.allow_from || []}
                  onChange={(v) => updateField('channels.onebot.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.maixcamChannel')}
          isRestartRequired={isRestartRequired('channels.maixcam')}
        >
          <SettingsField
            label={t('settings.fields.maixcamEnabled')}
            path="channels.maixcam.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.enabled')}
          >
            <BooleanInput
              id="channels.maixcam.enabled"
              value={ch.maixcam.enabled}
              onChange={(v) => updateField('channels.maixcam.enabled', v)}
            />
          </SettingsField>
          {ch.maixcam.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.maixcamHost')}
                path="channels.maixcam.host"
                isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.host')}
              >
                <TextInput
                  id="channels.maixcam.host"
                  value={ch.maixcam.host}
                  onChange={(v) => updateField('channels.maixcam.host', v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.maixcamPort')}
                path="channels.maixcam.port"
                isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.port')}
              >
                <NumberInput
                  id="channels.maixcam.port"
                  value={ch.maixcam.port}
                  onChange={(v) => updateField('channels.maixcam.port', v)}
                  min={1}
                  max={65535}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.maixcamAllowFrom')}
                path="channels.maixcam.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.allow_from')}
              >
                <StringListEditor
                  id="channels.maixcam.allow_from"
                  value={ch.maixcam.allow_from || []}
                  onChange={(v) => updateField('channels.maixcam.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.qqChannel')}
          isRestartRequired={isRestartRequired('channels.qq')}
        >
          <SettingsField
            label={t('settings.fields.qqEnabled')}
            path="channels.qq.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.qq.enabled')}
          >
            <BooleanInput
              id="channels.qq.enabled"
              value={ch.qq.enabled}
              onChange={(v) => updateField('channels.qq.enabled', v)}
            />
          </SettingsField>
          {ch.qq.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.qqAppId')}
                path="channels.qq.app_id"
                isDirty={isDirtyPath(dirtyPaths, 'channels.qq.app_id')}
              >
                <SecretInput
                  id="channels.qq.app_id"
                  value={ch.qq.app_id}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.qq.app_id', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.qqAppSecret')}
                path="channels.qq.app_secret"
                isDirty={isDirtyPath(dirtyPaths, 'channels.qq.app_secret')}
              >
                <SecretInput
                  id="channels.qq.app_secret"
                  value={ch.qq.app_secret}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.qq.app_secret', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.qqAllowFrom')}
                path="channels.qq.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.qq.allow_from')}
              >
                <StringListEditor
                  id="channels.qq.allow_from"
                  value={ch.qq.allow_from || []}
                  onChange={(v) => updateField('channels.qq.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.dingtalkChannel')}
          isRestartRequired={isRestartRequired('channels.dingtalk')}
        >
          <SettingsField
            label={t('settings.fields.dingtalkEnabled')}
            path="channels.dingtalk.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.enabled')}
          >
            <BooleanInput
              id="channels.dingtalk.enabled"
              value={ch.dingtalk.enabled}
              onChange={(v) => updateField('channels.dingtalk.enabled', v)}
            />
          </SettingsField>
          {ch.dingtalk.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.dingtalkClientId')}
                path="channels.dingtalk.client_id"
                isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.client_id')}
              >
                <SecretInput
                  id="channels.dingtalk.client_id"
                  value={ch.dingtalk.client_id}
                  onChange={(v: SecretValue) =>
                    updateSecretField('channels.dingtalk.client_id', v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.dingtalkClientSecret')}
                path="channels.dingtalk.client_secret"
                isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.client_secret')}
              >
                <SecretInput
                  id="channels.dingtalk.client_secret"
                  value={ch.dingtalk.client_secret}
                  onChange={(v: SecretValue) =>
                    updateSecretField(
                      'channels.dingtalk.client_secret',
                      v.mode,
                      v.value,
                      v.env_name,
                    )
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.dingtalkAllowFrom')}
                path="channels.dingtalk.allow_from"
                isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.allow_from')}
              >
                <StringListEditor
                  id="channels.dingtalk.allow_from"
                  value={ch.dingtalk.allow_from || []}
                  onChange={(v) => updateField('channels.dingtalk.allow_from', v)}
                />
              </SettingsField>
            </>
          )}
        </SettingsSection>
      </div>
    )
  }

  const renderToolsSettings = () => {
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

  const renderSystemSettings = () => {
    if (!draftConfig) return null
    const config = draftConfig

    return (
      <div className="space-y-6">
        <SettingsSection
          title={t('settings.sections.heartbeat')}
          isRestartRequired={isRestartRequired('heartbeat')}
        >
          <SettingsField
            label={t('settings.fields.heartbeatEnabled')}
            path="heartbeat.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'heartbeat.enabled')}
          >
            <BooleanInput
              id="heartbeat.enabled"
              value={config.heartbeat.enabled}
              onChange={(v) => updateField('heartbeat.enabled', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.heartbeatInterval')}
            path="heartbeat.interval"
            description={t('settings.descriptions.heartbeatInterval')}
            isDirty={isDirtyPath(dirtyPaths, 'heartbeat.interval')}
          >
            <NumberInput
              id="heartbeat.interval"
              value={config.heartbeat.interval}
              onChange={(v) => updateField('heartbeat.interval', v)}
              min={5}
            />
          </SettingsField>
        </SettingsSection>

        <SettingsSection
          title={t('settings.sections.devices')}
          isRestartRequired={isRestartRequired('devices')}
        >
          <SettingsField
            label={t('settings.fields.devicesEnabled')}
            path="devices.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'devices.enabled')}
          >
            <BooleanInput
              id="devices.enabled"
              value={config.devices.enabled}
              onChange={(v) => updateField('devices.enabled', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.monitorUsb')}
            path="devices.monitor_usb"
            isDirty={isDirtyPath(dirtyPaths, 'devices.monitor_usb')}
          >
            <BooleanInput
              id="devices.monitor_usb"
              value={config.devices.monitor_usb}
              onChange={(v) => updateField('devices.monitor_usb', v)}
            />
          </SettingsField>
        </SettingsSection>

        <SettingsSection title={t('settings.sections.logs')}>
          <SettingsField
            label={t('settings.fields.logsEnabled')}
            path="logs.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'logs.enabled')}
          >
            <BooleanInput
              id="logs.enabled"
              value={config.logs.enabled}
              onChange={(v) => updateField('logs.enabled', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.logsPath')}
            path="logs.path"
            isDirty={isDirtyPath(dirtyPaths, 'logs.path')}
          >
            <TextInput
              id="logs.path"
              value={config.logs.path || ''}
              onChange={(v) => updateField('logs.path', v || undefined)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.logsMaxDays')}
            path="logs.max_days"
            isDirty={isDirtyPath(dirtyPaths, 'logs.max_days')}
          >
            <NumberInput
              id="logs.max_days"
              value={config.logs.max_days || 7}
              onChange={(v) => updateField('logs.max_days', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.logsRotation')}
            path="logs.rotation"
            isDirty={isDirtyPath(dirtyPaths, 'logs.rotation')}
          >
            <SelectInput
              id="logs.rotation"
              value={config.logs.rotation || 'daily'}
              onChange={(v) => updateField('logs.rotation', v)}
              options={[
                { value: 'daily', label: t('settings.options.daily') },
                { value: 'weekly', label: t('settings.options.weekly') },
              ]}
            />
          </SettingsField>
        </SettingsSection>
      </div>
    )
  }

  const renderAdvancedSettings = () => {
    if (!draftConfig) return null
    const bindings = draftConfig.bindings || []

    const addBinding = () => {
      updateField('bindings', [...bindings, { agent_id: '', match: { channel: '' } }])
    }

    const removeBinding = (index: number) => {
      updateField(
        'bindings',
        bindings.filter((_, i) => i !== index),
      )
    }

    return (
      <div className="space-y-6">
        <SettingsSection title={t('settings.sections.bindings')}>
          <div className="mb-4">
            <button type="button" onClick={addBinding} className={ADD_BTN_CLS}>
              {t('settings.addBinding')}
            </button>
          </div>

          {bindings.length === 0 && (
            <p className="text-xs text-[#666]">{t('settings.noBindings')}</p>
          )}

          {bindings.map((binding, index) => (
            <div key={`${binding.agent_id}-${binding.match.channel}-${index}`} className={CARD_CLS}>
              <div className="mb-3 flex items-center justify-between">
                <span className="text-xs font-medium text-[#ccc]">
                  {t('settings.sections.bindings')} #{index + 1}
                </span>
                <button
                  type="button"
                  onClick={() => removeBinding(index)}
                  className={REMOVE_BTN_CLS}
                  aria-label={t('settings.removeBinding')}
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    aria-hidden="true"
                  >
                    <path d="M18 6L6 18M6 6l12 12" />
                  </svg>
                </button>
              </div>
              <div className="space-y-3">
                <SettingsField
                  label={t('settings.fields.bindingAgentId')}
                  path={`bindings.${index}.agent_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.agent_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.agent_id`}
                    value={binding.agent_id}
                    onChange={(v) => updateField(`bindings.${index}.agent_id`, v)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingChannel')}
                  path={`bindings.${index}.match.channel`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.channel`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.channel`}
                    value={binding.match.channel}
                    onChange={(v) => updateField(`bindings.${index}.match.channel`, v)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingAccountId')}
                  path={`bindings.${index}.match.account_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.account_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.account_id`}
                    value={binding.match.account_id || ''}
                    onChange={(v) =>
                      updateField(`bindings.${index}.match.account_id`, v || undefined)
                    }
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingGuildId')}
                  path={`bindings.${index}.match.guild_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.guild_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.guild_id`}
                    value={binding.match.guild_id || ''}
                    onChange={(v) =>
                      updateField(`bindings.${index}.match.guild_id`, v || undefined)
                    }
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingTeamId')}
                  path={`bindings.${index}.match.team_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.team_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.team_id`}
                    value={binding.match.team_id || ''}
                    onChange={(v) => updateField(`bindings.${index}.match.team_id`, v || undefined)}
                  />
                </SettingsField>
                {binding.match.peer && (
                  <>
                    <SettingsField
                      label={t('settings.fields.bindingPeerKind')}
                      path={`bindings.${index}.match.peer.kind`}
                      isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.peer.kind`)}
                    >
                      <TextInput
                        id={`bindings.${index}.match.peer.kind`}
                        value={binding.match.peer.kind}
                        onChange={(v) => updateField(`bindings.${index}.match.peer.kind`, v)}
                      />
                    </SettingsField>
                    <SettingsField
                      label={t('settings.fields.bindingPeerId')}
                      path={`bindings.${index}.match.peer.id`}
                      isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.peer.id`)}
                    >
                      <TextInput
                        id={`bindings.${index}.match.peer.id`}
                        value={binding.match.peer.id}
                        onChange={(v) => updateField(`bindings.${index}.match.peer.id`, v)}
                      />
                    </SettingsField>
                  </>
                )}
              </div>
            </div>
          ))}
        </SettingsSection>

        <SettingsSection title={t('settings.sections.rawJson')}>
          <div className="mb-3">
            <button
              type="button"
              onClick={() => setShowRawJson(!showRawJson)}
              className="rounded border border-[#3a3a3a] px-3 py-2 text-xs text-[#888] transition-colors hover:bg-[#2a2a2a]"
            >
              {showRawJson ? t('settings.hideRawJson') : t('settings.showRawJson')}
            </button>
          </div>
          {showRawJson && (
            <textarea
              value={JSON.stringify(draftConfig, null, 2)}
              onChange={(e) => {
                try {
                  const parsed = JSON.parse(e.target.value) as EditableConfig
                  replaceDraft(parsed)
                } catch {
                  // ignore invalid JSON while typing
                }
              }}
              className="h-[500px] w-full rounded border border-[#3a3a3a] bg-[#1a1a1a] p-3 font-mono text-xs text-[#e0e0e0] focus:border-blue-500 focus:outline-none"
              spellCheck={false}
            />
          )}
        </SettingsSection>
      </div>
    )
  }

  const renderTabContent = () => {
    if (isLoading) {
      return (
        <div className="flex h-64 items-center justify-center">
          <div className="text-sm text-[#888]">{t('common.loading')}</div>
        </div>
      )
    }

    switch (activeTab) {
      case 'general':
        return renderGeneralSettings()
      case 'agents':
        return renderAgentsSettings()
      case 'session':
        return renderSessionSettings()
      case 'providers':
        return renderProvidersSettings()
      case 'channels':
        return renderChannelSettings()
      case 'tools':
        return renderToolsSettings()
      case 'system':
        return renderSystemSettings()
      case 'advanced':
        return renderAdvancedSettings()
      case 'diagnostics':
        return <DiagnosticsPanel />
      default:
        return null
    }
  }

  return (
    <div className="flex h-screen overflow-hidden bg-[#1a1a1a] text-[#e0e0e0]">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        <div className="flex items-center justify-between border-b border-[#2e2e2e] px-6 py-4">
          <div className="flex items-center gap-4">
            <button
              type="button"
              onClick={onToggleSidebar}
              className="text-[#888] transition-colors hover:text-white"
              aria-label="Toggle sidebar"
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
              >
                <line x1="3" y1="6" x2="21" y2="6" />
                <line x1="3" y1="12" x2="21" y2="12" />
                <line x1="3" y1="18" x2="21" y2="18" />
              </svg>
            </button>
            <h1 className="text-xl font-semibold text-white">{t('chat.settings')}</h1>
            {metadata && <span className="text-xs text-[#666]">{metadata.config_path}</span>}
          </div>
          <button
            onClick={onLogout}
            type="button"
            className="rounded-md bg-rose-600 px-4 py-2 text-sm text-white transition-colors hover:bg-rose-500"
          >
            {t('chat.logout')}
          </button>
        </div>

        <div className="flex flex-1 overflow-hidden">
          <nav className="w-[200px] flex-shrink-0 border-r border-[#2e2e2e] bg-[#222] p-4">
            <div className="space-y-1">
              {tabs.map((tab) => (
                <button
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  type="button"
                  className={`w-full rounded px-3 py-2 text-left text-xs transition-colors ${
                    activeTab === tab.id
                      ? 'bg-blue-600 text-white'
                      : 'text-[#bbb] hover:bg-[#2a2a2a]'
                  }`}
                >
                  {tab.label}
                </button>
              ))}
            </div>
          </nav>

          <div className="flex flex-1 flex-col overflow-hidden">
            <div className="flex-1 overflow-y-auto p-6">{renderTabContent()}</div>

            <div className="flex items-center justify-between border-t border-[#2e2e2e] bg-[#222] px-6 py-4">
              <div className="flex items-center gap-2">
                {saveState === 'saved' && (
                  <span className="text-xs text-green-400">{t('settings.saved')}</span>
                )}
                {saveState === 'error' && saveError && (
                  <span className="text-xs text-rose-400">{saveError}</span>
                )}
                {hasErrors && (
                  <span className="text-xs text-amber-400">
                    {t('settings.validationErrors', { count: validationErrors.length })}
                  </span>
                )}
                {isDirty && (
                  <span className="text-xs text-blue-400">{t('settings.unsavedChanges')}</span>
                )}
              </div>

              <div className="flex items-center gap-2">
                <button
                  onClick={() => reset()}
                  disabled={!isDirty || saveState === 'saving'}
                  type="button"
                  className="rounded border border-[#3a3a3a] bg-transparent px-4 py-2 text-xs text-[#ccc] transition-colors hover:bg-[#2a2a2a] disabled:opacity-50"
                >
                  {t('common.reset')}
                </button>
                <button
                  onClick={handleSave}
                  disabled={!isDirty || saveState === 'saving'}
                  type="button"
                  className="rounded bg-blue-600 px-4 py-2 text-xs text-white transition-colors hover:bg-blue-500 disabled:opacity-50"
                >
                  {saveState === 'saving' ? t('common.saving') : t('common.save')}
                </button>
              </div>
            </div>
          </div>
        </div>
      </main>
    </div>
  )
}

type ProviderModels = Record<string, import('../../lib/types').ProviderModelConfig>

function ProviderModelsEditor({
  name,
  models,
  onChange,
}: { name: string; models: ProviderModels; onChange: (v: ProviderModels) => void }) {
  const { t } = useTranslation()
  const [newModelName, setNewModelName] = useState('')
  const modelNames = Object.keys(models)

  const addModel = () => {
    const key = newModelName.trim()
    if (!key) return
    onChange({ ...models, [key]: {} })
    setNewModelName('')
  }

  const removeModel = (key: string) => {
    const updated = { ...models }
    delete updated[key]
    onChange(updated)
  }

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <input
          type="text"
          value={newModelName}
          onChange={(e) => setNewModelName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              addModel()
            }
          }}
          placeholder={t('settings.modelNamePlaceholder')}
          className={INPUT_CLS}
        />
        <button
          type="button"
          onClick={addModel}
          disabled={!newModelName.trim()}
          className={ADD_BTN_CLS}
        >
          {t('common.add')}
        </button>
      </div>

      {modelNames.length === 0 && <p className="text-xs text-[#666]">{t('settings.noModels')}</p>}

      {modelNames.map((key) => {
        const m = models[key]
        return (
          <div key={key} className="rounded border border-[#2e2e2e] bg-[#222] p-3">
            <div className="mb-2 flex items-center justify-between">
              <span className="font-mono text-xs font-medium text-white">{key}</span>
              <button
                type="button"
                onClick={() => removeModel(key)}
                className={REMOVE_BTN_CLS}
                aria-label={t('common.remove')}
              >
                <svg
                  width="12"
                  height="12"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  aria-hidden="true"
                >
                  <path d="M18 6L6 18M6 6l12 12" />
                </svg>
              </button>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <SettingsField
                label={t('settings.fields.modelContextWindow')}
                path={`providers.${name}.models.${key}.context_window`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.context_window`}
                  value={m.context_window || 0}
                  onChange={(v) =>
                    onChange({ ...models, [key]: { ...m, context_window: v || undefined } })
                  }
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelMaxTokens')}
                path={`providers.${name}.models.${key}.max_tokens`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.max_tokens`}
                  value={m.max_tokens || 0}
                  onChange={(v) =>
                    onChange({ ...models, [key]: { ...m, max_tokens: v || undefined } })
                  }
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelTemperature')}
                path={`providers.${name}.models.${key}.temperature`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.temperature`}
                  value={m.temperature || 0}
                  onChange={(v) =>
                    onChange({ ...models, [key]: { ...m, temperature: v || undefined } })
                  }
                  min={0}
                  max={2}
                  step={0.1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelVision')}
                path={`providers.${name}.models.${key}.vision`}
              >
                <BooleanInput
                  id={`providers.${name}.models.${key}.vision`}
                  value={m.vision || false}
                  onChange={(v) => onChange({ ...models, [key]: { ...m, vision: v } })}
                />
              </SettingsField>
            </div>
          </div>
        )
      })}
    </div>
  )
}
