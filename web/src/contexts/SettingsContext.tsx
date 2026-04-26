import { type ReactNode, createContext, useContext, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { useAvailableModels } from '../hooks/useAvailableModels'
import type { SettingsConfigState } from '../hooks/useSettingsConfig'
import type { ApiClient } from '../services/http/client'

type Option = {
  value: string
  label: string
}

type OptionGroup = {
  label: string
  options: Option[]
}

type SettingsContextValue = SettingsConfigState & {
  modelOptions: Option[]
  modelGroups: OptionGroup[]
  getOptionsForAgent: Option[]
  getGroupsForAgent: OptionGroup[]
  isLoadingModels: boolean
  isRestartRequired: (section: string) => boolean
  t: (key: string, options?: Record<string, unknown>) => string
  api: ApiClient
}

const SettingsContext = createContext<SettingsContextValue | null>(null)

type Props = {
  children: ReactNode
  settingsState: SettingsConfigState
  api: ApiClient
}

export function SettingsProvider({ children, settingsState, api }: Props) {
  const { t } = useTranslation()
  const { available, groups, isLoading: isLoadingModels } = useAvailableModels(api)

  const modelOptions = useMemo(() => {
    return available.map((model: string) => ({ value: model, label: model }))
  }, [available])

  const modelGroups = useMemo(() => {
    return groups.map((group: { provider: string; models: Option[] }) => ({
      label: group.provider,
      options: group.models,
    }))
  }, [groups])

  const getOptionsForAgent = useMemo(() => {
    const list = settingsState.draftConfig?.agents?.list || []
    const allAgentModels = list
      .map((a: { model?: string | { primary?: string } }) =>
        typeof a.model === 'string' ? a.model : a.model?.primary || '',
      )
      .filter(Boolean)
    const existingValues = new Set(modelOptions.map((o: Option) => o.value))
    const opts: Option[] = [...modelOptions]
    for (const m of allAgentModels) {
      if (!existingValues.has(m)) {
        opts.push({ value: m, label: m })
      }
    }
    return opts
  }, [modelOptions, settingsState.draftConfig])

  const getGroupsForAgent = useMemo(() => {
    const list = settingsState.draftConfig?.agents?.list || []
    const allAgentModels = list
      .map((a: { model?: string | { primary?: string } }) =>
        typeof a.model === 'string' ? a.model : a.model?.primary || '',
      )
      .filter(Boolean)
    const existingValues = new Set(
      modelGroups.flatMap((g: OptionGroup) => g.options.map((o: Option) => o.value)),
    )
    const grp: OptionGroup[] = [...modelGroups]
    const newModels = allAgentModels.filter((m: string) => !existingValues.has(m))
    if (newModels.length > 0) {
      grp.push({
        label: 'Configured Models',
        options: newModels.map((m: string) => ({ value: m, label: m })),
      })
    }
    return grp
  }, [modelGroups, settingsState.draftConfig])

  const isRestartRequired = (section: string): boolean => {
    return (
      settingsState.metadata?.restart_required_sections?.some(
        (item: string) =>
          item === section || item.startsWith(`${section}.`) || section.startsWith(`${item}.`),
      ) ?? false
    )
  }

  const value: SettingsContextValue = {
    ...settingsState,
    modelOptions,
    modelGroups,
    getOptionsForAgent,
    getGroupsForAgent,
    isLoadingModels,
    isRestartRequired,
    t,
    api,
  }

  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>
}

export function useSettings(): SettingsContextValue {
  const context = useContext(SettingsContext)
  if (!context) {
    throw new Error('useSettings must be used within a SettingsProvider')
  }
  return context
}
