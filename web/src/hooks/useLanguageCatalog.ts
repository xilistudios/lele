import { useCallback, useEffect, useState } from 'react'
import {
  fetchLanguageCatalog,
  loadLanguagePack,
  type LanguagePackStatus,
} from '../i18n'

/** Loads the language catalog (builtin + downloadable) and exposes install/switch helpers. */
export function useLanguageCatalog() {
  const [languages, setLanguages] = useState<LanguagePackStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [switching, setSwitching] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const list = await fetchLanguageCatalog()
      setLanguages(list)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'catalog_failed')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const switchLanguage = useCallback(async (code: string) => {
    setSwitching(code)
    setError(null)
    try {
      const ok = await loadLanguagePack(code)
      if (!ok) setError('load_failed')
      return ok
    } finally {
      setSwitching(null)
    }
  }, [])

  return { languages, loading, switching, error, refresh, switchLanguage }
}
