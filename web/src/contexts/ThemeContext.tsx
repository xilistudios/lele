import {
  type ReactNode,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'

type ThemeSetting = 'dark' | 'light' | 'auto'
type Theme = 'dark' | 'light'

type ThemeContextValue = {
  theme: Theme
  themeSetting: ThemeSetting
  setThemeSetting: (setting: ThemeSetting) => void
  toggleTheme: () => void
}

const THEME_STORAGE_KEY = 'lele-theme'

export function getStoredThemeSetting(): ThemeSetting {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY)
    if (stored === 'dark' || stored === 'light' || stored === 'auto') return stored
  } catch {
    // localStorage not available
  }
  return 'dark'
}

function resolveTheme(setting: ThemeSetting): Theme {
  if (setting === 'auto') {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  return setting
}

const THEME_META_COLORS: Record<Theme, string> = {
  dark: '#0f1115',
  light: '#f5f6f8',
}

function applyTheme(theme: Theme) {
  document.documentElement.setAttribute('data-theme', theme)
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) {
    meta.setAttribute('content', THEME_META_COLORS[theme])
  }
}

function persistThemeSetting(setting: ThemeSetting) {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, setting)
  } catch {
    // localStorage not available
  }
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [themeSetting, setThemeSettingState] = useState<ThemeSetting>(() =>
    getStoredThemeSetting(),
  )
  const [theme, setThemeState] = useState<Theme>(() => resolveTheme(getStoredThemeSetting()))

  // Track the media query listener ref so we can clean up
  const mediaQueryRef = useRef<MediaQueryList | null>(null)

  // Sync resolved theme when setting changes
  useEffect(() => {
    const resolved = resolveTheme(themeSetting)
    setThemeState(resolved)
    applyTheme(resolved)
    persistThemeSetting(themeSetting)

    // Set up listener for system preference changes when in auto mode
    if (themeSetting === 'auto') {
      const mq = window.matchMedia('(prefers-color-scheme: dark)')
      mediaQueryRef.current = mq
      const handler = () => {
        const newResolved = resolveTheme('auto')
        setThemeState(newResolved)
        applyTheme(newResolved)
      }
      mq.addEventListener('change', handler)
      return () => {
        mq.removeEventListener('change', handler)
        mediaQueryRef.current = null
      }
    }
  }, [themeSetting])

  const setThemeSetting = useCallback((newSetting: ThemeSetting) => {
    setThemeSettingState(newSetting)
  }, [])

  const toggleTheme = useCallback(() => {
    setThemeSettingState((prev) => {
      if (prev === 'auto') return 'dark' // toggle from auto goes to dark
      return prev === 'dark' ? 'light' : 'dark'
    })
  }, [])

  const value = useMemo(
    () => ({ theme, themeSetting, setThemeSetting, toggleTheme }),
    [theme, themeSetting, setThemeSetting, toggleTheme],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext)
  if (!context) {
    throw new Error('useTheme must be used within a ThemeProvider')
  }
  return context
}
