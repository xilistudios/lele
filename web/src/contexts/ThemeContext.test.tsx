import { describe, expect, test, beforeEach } from 'bun:test'
import { act, cleanup, render } from '@testing-library/react'
import type { ReactNode } from 'react'
import React from 'react'
import { ThemeProvider, useTheme } from './ThemeContext'

// Helper component that exposes theme controls for testing
function ThemeControls({ onReady }: { onReady?: (api: ReturnType<typeof useTheme>) => void }) {
  const api = useTheme()
  React.useEffect(() => {
    onReady?.(api)
  }, [api, onReady])
  return null
}

function renderWithTheme(children: ReactNode) {
  return render(<ThemeProvider>{children}</ThemeProvider>)
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.classList.remove('dark')
})

describe('applyTheme — .dark class sync', () => {
  test('adds class "dark" when theme is dark', () => {
    let themeApi: ReturnType<typeof useTheme> | undefined

    renderWithTheme(
      <ThemeControls onReady={(api) => { themeApi = api }} />,
    )

    act(() => {
      themeApi!.setThemeSetting('dark')
    })

    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
  })

  test('removes class "dark" when theme is light', () => {
    // Start with dark in localStorage so initial theme is dark
    localStorage.setItem('lele-theme', 'dark')

    let themeApi: ReturnType<typeof useTheme> | undefined

    renderWithTheme(
      <ThemeControls onReady={(api) => { themeApi = api }} />,
    )

    // Confirm dark was applied initially
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    act(() => {
      themeApi!.setThemeSetting('light')
    })

    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  test('removes class "dark" when switching from dark to light via toggle', () => {
    localStorage.setItem('lele-theme', 'dark')

    let themeApi: ReturnType<typeof useTheme> | undefined

    renderWithTheme(
      <ThemeControls onReady={(api) => { themeApi = api }} />,
    )

    expect(document.documentElement.classList.contains('dark')).toBe(true)

    act(() => {
      themeApi!.toggleTheme()
    })

    // toggle from dark → light
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })
})
