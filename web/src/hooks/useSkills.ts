import { useCallback, useEffect, useState } from 'react'
import type { AvailableSkill, SkillInfo } from '../lib/types'
import type { ApiClient } from '../services/http/client'

type SkillsState = {
  skills: SkillInfo[]
  availableSkills: AvailableSkill[]
  isLoading: boolean
  isAvailableLoading: boolean
  isInstalling: boolean
  isRemoving: string | null
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

  useEffect(() => {
    fetchSkills()
  }, [fetchSkills])

  return {
    ...state,
    fetchSkills,
    fetchAvailableSkills,
    installSkill,
    removeSkill,
  }
}
