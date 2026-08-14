import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../contexts/AuthContext'
import type { SecretAuditRecord, SecretInput, SecretMeta, SecretStatus } from '../lib/types'

export function useSecrets() {
  const { api } = useAuthContext()
  const [secrets, setSecrets] = useState<SecretMeta[]>([])
  const [status, setStatus] = useState<SecretStatus | null>(null)
  const [audit, setAudit] = useState<SecretAuditRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)

  const fetchSecrets = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await api.secrets.list()
      if (!mountedRef.current) return
      setSecrets(data?.secrets ?? [])
      setStatus(data?.status ?? null)
    } catch (err) {
      console.warn('[useSecrets] Failed to fetch:', err)
      if (mountedRef.current) {
        setError((err as Error).message)
      }
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }, [api])

  const fetchAudit = useCallback(async () => {
    try {
      const data = await api.secrets.audit()
      if (!mountedRef.current) return
      setAudit(data?.audit ?? [])
    } catch (err) {
      console.warn('[useSecrets] Failed to fetch audit:', err)
    }
  }, [api])

  // Initial load
  useEffect(() => {
    mountedRef.current = true
    fetchSecrets()
    fetchAudit()
    return () => {
      mountedRef.current = false
    }
  }, [fetchSecrets, fetchAudit])

  const reveal = useCallback(
    async (name: string): Promise<string> => {
      const data = await api.secrets.get(name)
      return data?.value ?? ''
    },
    [api],
  )

  const createSecret = useCallback(
    async (input: SecretInput) => {
      const resp = await api.secrets.create(input)
      await fetchSecrets()
      await fetchAudit()
      return resp.secret
    },
    [api, fetchSecrets, fetchAudit],
  )

  const removeSecret = useCallback(
    async (name: string) => {
      try {
        await api.secrets.remove(name)
        await fetchSecrets()
      } catch (err) {
        console.warn('[useSecrets] Failed to remove:', err)
        setError((err as Error).message)
      }
    },
    [api, fetchSecrets],
  )

  return {
    secrets,
    status,
    audit,
    loading,
    error,
    refresh: fetchSecrets,
    refreshAudit: fetchAudit,
    reveal,
    createSecret,
    removeSecret,
  }
}
