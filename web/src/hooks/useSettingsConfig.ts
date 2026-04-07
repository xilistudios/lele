/* eslint-disable @typescript-eslint/no-explicit-any */
/* eslint-disable @typescript-eslint/no-unsafe-member-access */
import { useCallback, useEffect, useState } from 'react'
import type {
  ConfigError,
  ConfigMetadata,
  EditableConfig,
} from '../lib/types'
import type { ApiClient } from '../services/http/client'

export type SaveState = 'idle' | 'validating' | 'saving' | 'saved' | 'error'

export type SettingsConfigState = {
  // Data
  remoteConfig: EditableConfig | null
  draftConfig: EditableConfig | null
  metadata: ConfigMetadata | null

  // UI State
  dirtyPaths: Set<string>
  validationErrors: ConfigError[]
  saveState: SaveState
  saveError: string | null

  // Actions
  updateField: <T>(path: string, value: T) => void
  updateSecretField: (path: string, mode: 'literal' | 'env' | 'empty', value?: string, envName?: string) => void
  replaceDraft: (nextConfig: EditableConfig) => void
  reset: () => void
  validate: () => Promise<boolean>
  save: () => Promise<boolean>
  isDirty: boolean
  isLoading: boolean
  hasErrors: boolean
}

function setValueAtPath(obj: Record<string, unknown>, p: string, v: unknown): Record<string, unknown> {
  if (!p) {
    return v as Record<string, unknown>
  }
  const parts = p.split('.')
  let current: Record<string, unknown> = obj
  for (let i = 0; i < parts.length - 1; i++) {
    const part = parts[i]
    const nextPart = parts[i + 1]
    const existing = current[part]
    if (typeof existing !== 'object' || existing === null) {
      current[part] = /^\d+$/.test(nextPart) ? [] : {}
    }
    current = current[part] as Record<string, unknown>
  }
  current[parts[parts.length - 1]] = v
  return obj
}

export function useSettingsConfig(apiClient: ApiClient): SettingsConfigState {
  const [remoteConfig, setRemoteConfig] = useState<EditableConfig | null>(null)
  const [draftConfig, setDraftConfig] = useState<EditableConfig | null>(null)
  const [metadata, setMetadata] = useState<ConfigMetadata | null>(null)
  const [dirtyPaths, setDirtyPaths] = useState<Set<string>>(new Set())
  const [validationErrors, setValidationErrors] = useState<ConfigError[]>([])
  const [saveState, setSaveState] = useState<SaveState>('idle')
  const [saveError, setSaveError] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(true)

  // Load initial config
  useEffect(() => {
    const loadConfig = async () => {
      setIsLoading(true)
      try {
        const response = await apiClient.config()
        setRemoteConfig(response.config)
        setDraftConfig(deepClone(response.config))
        setMetadata(response.meta)
      } catch (err) {
        setSaveError(err instanceof Error ? err.message : 'Failed to load config')
      } finally {
        setIsLoading(false)
      }
    }
    loadConfig()
  }, [apiClient])

  const updateField = useCallback(<T,>(path: string, value: T) => {
    if (!draftConfig) return

    const newDraft = deepClone(draftConfig)
    const updated = setValueAtPath(newDraft as Record<string, unknown>, path, value)
    setDraftConfig(updated as EditableConfig)
    setDirtyPaths((prev) => new Set(prev).add(path))
    setSaveState('idle')
    setValidationErrors([])
  }, [draftConfig])

  const updateSecretField = useCallback((
    path: string,
    mode: 'literal' | 'env' | 'empty',
    value?: string,
    envName?: string
  ) => {
    if (!draftConfig) return

    const newDraft = deepClone(draftConfig)
    const secretValue = {
      mode,
      value: mode === 'literal' ? value : undefined,
      env_name: mode === 'env' ? envName : undefined,
      env_default: undefined,
      has_env_var: mode === 'env' ? !!envName && envName.length > 0 : false,
    }
    const updated = setValueAtPath(newDraft as Record<string, unknown>, path, secretValue)
    setDraftConfig(updated as EditableConfig)
    setDirtyPaths((prev) => new Set(prev).add(path))
    setSaveState('idle')
    setValidationErrors([])
  }, [draftConfig])

  const replaceDraft = useCallback((nextConfig: EditableConfig) => {
    setDraftConfig(deepClone(nextConfig))
    setDirtyPaths((prev) => new Set(prev).add(''))
    setSaveState('idle')
    setValidationErrors([])
  }, [])

  const reset = useCallback(() => {
    if (remoteConfig) {
      setDraftConfig(deepClone(remoteConfig))
      setDirtyPaths(new Set())
      setValidationErrors([])
      setSaveState('idle')
      setSaveError(null)
    }
  }, [remoteConfig])

  const validate = useCallback(async (): Promise<boolean> => {
    if (!draftConfig) return false

    setSaveState('validating')
    try {
      const response = await apiClient.validateConfig(draftConfig)
      if (!response.valid && response.errors) {
        setValidationErrors(response.errors)
        setSaveState('error')
        return false
      }
      setValidationErrors([])
      setSaveState('idle')
      return true
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Validation failed')
      setSaveState('error')
      return false
    }
  }, [apiClient, draftConfig])

  const save = useCallback(async (): Promise<boolean> => {
    if (!draftConfig) return false

    setSaveState('saving')
    try {
      const response = await apiClient.saveConfig(draftConfig)
      if (response.errors && response.errors.length > 0) {
        setValidationErrors(response.errors)
        setSaveState('error')
        return false
      }
      setRemoteConfig(response.config ?? draftConfig)
      setMetadata(response.meta)
      setDirtyPaths(new Set())
      setValidationErrors([])
      setSaveState('saved')
      return true
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
      setSaveState('error')
      return false
    }
  }, [apiClient, draftConfig])

  const isDirty = dirtyPaths.size > 0
  const hasErrors = validationErrors.length > 0

  return {
    remoteConfig,
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
  }
}

function deepClone<T>(obj: T): T {
  if (obj === null || typeof obj !== 'object') return obj
  if (obj instanceof Date) return new Date(obj.getTime()) as unknown as T
  if (Array.isArray(obj)) return obj.map(deepClone) as unknown as T
  const cloned: Record<string, unknown> = {}
  for (const key in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, key)) {
      cloned[key] = deepClone(obj[key as keyof T])
    }
  }
  return cloned as T
}
