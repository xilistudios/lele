import { describe, expect, test } from 'bun:test'
import { getModeTheme } from './modeTheme'

const EXCEPTION = 'text-text-primary dark:text-mode-group'

describe('§2.4 excepción group-claro (todos los roles de texto)', () => {
  const g = getModeTheme('group')

  test('text usa la excepción (N1: MessageList badge status = tinte doble)', () => {
    expect(g.text).toBe(EXCEPTION)
  })

  test('chip/selectedItem/iconCircle usan la excepción', () => {
    expect(g.chip).toContain(EXCEPTION)
    expect(g.selectedItem).toContain(EXCEPTION)
    expect(g.iconCircle).toContain(EXCEPTION)
  })

  test('tabActive usa la excepción (MAJOR-1: group 4.96 ✅ dark / 3.95 ❌ light)', () => {
    expect(g.tabActive).toContain('text-text-primary')
    expect(g.tabActive).toContain('dark:text-mode-group')
  })

  test('ningún rol de texto de group pinta mode-group pelado en claro', () => {
    for (const role of ['text', 'chip', 'selectedItem', 'iconCircle', 'tabActive'] as const) {
      const bare = g[role].replace(/dark:text-mode-group/g, '')
      expect(bare).not.toContain('text-mode-group')
    }
  })

  test('el color de identidad sobrevive en oscuro y en dot/borde', () => {
    expect(g.dot).toBe('bg-mode-group')
    expect(g.border).toContain('border-mode-group')
    expect(g.text).toContain('dark:text-mode-group')
  })
})

describe('chat: tabActive carries dark: variant (6.16 ✅ dark / 4.20 ❌ light)', () => {
  test('tabActive uses text-text-primary with dark:text-mode-chat', () => {
    const t = getModeTheme('chat')
    expect(t.tabActive).toContain('text-text-primary')
    expect(t.tabActive).toContain('dark:text-mode-chat')
    const bare = t.tabActive.replace(/dark:text-mode-chat/g, '')
    expect(bare).not.toContain('text-mode-chat')
  })
})

describe('chat/agent: color pelado permitido en roles no-tabActive (medido ≥4.5 sobre sus tintes)', () => {
  test('text y chip no requieren excepción', () => {
    for (const mode of ['chat', 'agent'] as const) {
      const t = getModeTheme(mode)
      expect(t.text).toBe(`text-mode-${mode}`)
      expect(t.chip).toContain(`text-mode-${mode}`)
    }
  })
})

describe('agent: tabActive has NO dark: variant (4.39 dark / 4.33 light both fail)', () => {
  test('tabActive uses text-text-primary only, no dark:text-mode-agent', () => {
    const t = getModeTheme('agent')
    expect(t.tabActive).toContain('text-text-primary')
    expect(t.tabActive).not.toContain('dark:text-mode-agent')
  })
})
