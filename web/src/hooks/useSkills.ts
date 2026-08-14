import { useCallback, useEffect, useState } from 'react'
import type { AvailableSkill, ScannedSkill, SkillInfo } from '../lib/types'
import type { ApiClient } from '../services/http/client'

type SkillsState = {
  skills: SkillInfo[]
  availableSkills: AvailableSkill[]
  isLoading: boolean
  isAvailableLoading: boolean
  isInstalling: boolean
  isRemoving: string | null
  isScanning: boolean
  scanResults: ScannedSkill[] | null
  scanRepo: string | null
  error: string | null
}

export function useSkills(api: ApiClient) {
  const [state, setState] = useState<SkillsState>({
    skills: [],
    availableSkills: [],
    isLoading: true,
    isAvailableLoading: false,
    isInstalling: false,
    isRemoving: null,
    isScanning: false,
    scanResults: null,
    scanRepo: null,
    error: null,
  })

  const fetchSkills = useCallback(async () => {
    setState((prev) => ({ ...prev, isLoading: true, error: null }))
    try {
      const response = await api.skills()
      setState((prev) => ({ ...prev, skills: response.skills, isLoading: false }))
    } catch (err) {
      setState((prev) => ({
        ...prev,
        error: (err as Error).message,
        isLoading: false,
      }))
    }
  }, [api])

  const fetchAvailableSkills = useCallback(async () => {
    setState((prev) => ({ ...prev, isAvailableLoading: true, error: null }))
    try {
      const response = await api.availableSkills()
      setState((prev) => ({
        ...prev,
        availableSkills: response.skills,
        isAvailableLoading: false,
      }))
    } catch (err) {
      setState((prev) => ({
        ...prev,
        error: (err as Error).message,
        isAvailableLoading: false,
      }))
    }
  }, [api])

  const installSkill = useCallback(
    async (url: string) => {
      setState((prev) => ({ ...prev, isInstalling: true, error: null }))
      try {
        await api.installSkill(url)
        await fetchSkills()
        setState((prev) => ({ ...prev, isInstalling: false }))
      } catch (err) {
        setState((prev) => ({
          ...prev,
          error: (err as Error).message,
          isInstalling: false,
        }))
      }
    },
    [api, fetchSkills],
  )

  const removeSkill = useCallback(
    async (name: string) => {
      setState((prev) => ({ ...prev, isRemoving: name, error: null }))
      try {
        await api.removeSkill(name)
        await fetchSkills()
        setState((prev) => ({ ...prev, isRemoving: null }))
      } catch (err) {
        setState((prev) => ({
          ...prev,
          error: (err as Error).message,
          isRemoving: null,
        }))
      }
    },
    [api, fetchSkills],
  )

  const scanRepo = useCallback(
    async (repo: string) => {
      setState((prev) => ({
        ...prev,
        isScanning: true,
        error: null,
        scanResults: null,
        scanRepo: null,
      }))
      try {
        const response = await api.scanSkills(repo)
        setState((prev) => ({
          ...prev,
          scanResults: response.skills,
          scanRepo: response.repo,
          isScanning: false,
        }))
        return response.skills
      } catch (err) {
        setState((prev) => ({
          ...prev,
          error: (err as Error).message,
          isScanning: false,
        }))
        return null
      }
    },
    [api],
  )

  const installBatch = useCallback(
    async (repo: string, skills: string[]) => {
      setState((prev) => ({ ...prev, isInstalling: true, error: null }))
      try {
        await api.installSkillsBatch(repo, skills)
        await fetchSkills()
        setState((prev) => ({ ...prev, isInstalling: false, scanResults: null, scanRepo: null }))
      } catch (err) {
        setState((prev) => ({
          ...prev,
          error: (err as Error).message,
          isInstalling: false,
        }))
      }
    },
    [api, fetchSkills],
  )

  const toggleSkill = useCallback(
    async (name: string, enabled: boolean) => {
      try {
        await api.toggleSkill(name, enabled)
        await fetchSkills()
      } catch (err) {
        setState((prev) => ({
          ...prev,
          error: (err as Error).message,
        }))
      }
    },
    [api, fetchSkills],
  )

  const clearScanResults = useCallback(() => {
    setState((prev) => ({ ...prev, scanResults: null, scanRepo: null }))
  }, [])

  useEffect(() => {
    fetchSkills()
  }, [fetchSkills])

  return {
    ...state,
    fetchSkills,
    fetchAvailableSkills,
    installSkill,
    removeSkill,
    scanRepo: scanRepo,
    installBatch,
    toggleSkill,
    clearScanResults,
  }
}
