import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'
import en from './locales/en.json'
import es from './locales/es.json'
import pt from './locales/pt.json'

export const BUILTIN_LANGUAGES = ['es', 'en', 'pt'] as const

const resources = {
  en: { translation: en },
  es: { translation: es },
  pt: { translation: pt },
}

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'es',
    interpolation: {
      escapeValue: false,
    },
    detection: {
      order: ['localStorage', 'navigator'],
      caches: ['localStorage'],
    },
  })

export type LanguagePackStatus = {
  code: string
  name: string
  native_name: string
  builtin: boolean
  rtl?: boolean
  installed: boolean
}

export type LocalesListResponse = {
  languages: LanguagePackStatus[]
  current?: string
  cache_dir?: string
}

function authHeaders(): HeadersInit {
  let token = ''
  try {
    const raw = localStorage.getItem('lele.session')
    if (raw) {
      const session = JSON.parse(raw) as { token?: string }
      token = session?.token || ''
    }
  } catch {}
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (token) headers.Authorization = `Bearer ${token}`
  return headers
}

function apiBase(): string {
  const stored = localStorage.getItem('lele.apiUrl')
  if (stored) return stored.replace(/\/$/, '')
  return ''
}

export async function fetchLanguageCatalog(): Promise<LanguagePackStatus[]> {
  try {
    const res = await fetch(`${apiBase()}/api/v1/locales`, {
      headers: authHeaders(),
      credentials: 'include',
    })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = (await res.json()) as LocalesListResponse
    if (data?.languages?.length) return data.languages
  } catch {}
  return BUILTIN_LANGUAGES.map((code) => ({
    code,
    name: code,
    native_name: nativeName(code),
    builtin: true,
    installed: true,
  }))
}

function nativeName(code: string): string {
  const map: Record<string, string> = {
    es: 'Español',
    en: 'English',
    pt: 'Português',
    fr: 'Français',
    de: 'Deutsch',
    it: 'Italiano',
    ja: '日本語',
    ko: '한국어',
    zh: '中文',
    ru: 'Русский',
    vi: 'Tiếng Việt',
  }
  return map[code] || code.toUpperCase()
}

export async function loadLanguagePack(code: string): Promise<boolean> {
  const lang = code.toLowerCase().split(/[-_]/)[0]
  if ((BUILTIN_LANGUAGES as readonly string[]).includes(lang)) {
    if (i18n.language !== lang) await i18n.changeLanguage(lang)
    return true
  }
  if (i18n.hasResourceBundle(lang, 'translation')) {
    if (i18n.language !== lang) await i18n.changeLanguage(lang)
    return true
  }

  try {
    await fetch(`${apiBase()}/api/v1/locales/${encodeURIComponent(lang)}/install`, {
      method: 'POST',
      headers: authHeaders(),
      credentials: 'include',
    }).catch(() => undefined)

    const res = await fetch(`${apiBase()}/api/v1/locales/${encodeURIComponent(lang)}`, {
      headers: authHeaders(),
      credentials: 'include',
    })
    if (!res.ok) return false
    const data = await res.json()
    const web = data?.web
    if (!web || typeof web !== 'object' || Object.keys(web).length === 0) {
      if (data?.source === 'builtin') {
        await i18n.changeLanguage(lang)
        return true
      }
      return false
    }
    i18n.addResourceBundle(lang, 'translation', web, true, true)
    await i18n.changeLanguage(lang)
    return true
  } catch {
    return false
  }
}

export async function ensureActiveLanguagePack(): Promise<void> {
  const lang = (i18n.language || 'es').toLowerCase().split(/[-_]/)[0]
  if ((BUILTIN_LANGUAGES as readonly string[]).includes(lang)) return
  await loadLanguagePack(lang)
}

void ensureActiveLanguagePack()

export default i18n
