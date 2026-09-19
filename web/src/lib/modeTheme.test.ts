/**
 * modeTheme.test.ts — contrato §2.4 (excepción group-claro)
 *
 * Regla: el color de identidad `mode-group` (#C2410C claro) NO alcanza 4.5:1
 * sobre sus propios tintes (tinte/10 = 4.48; tinte doble = 3.92 ❌). Por eso los
 * CINCO roles de texto de group (text, chip, selectedItem, iconCircle, tabActive)
 * deben llevar la excepción explícita: `text-text-primary dark:text-mode-group`.
 *
 * tabActive (§2.4 MAJOR-1): chat/group ahora llevan `dark:text-mode-X` en tabActive
 * porque pasan AA en oscuro (chat 6.16, group 4.96) pero fallan en claro
 * (chat 4.20, group 3.95). Agent falla en ambos (4.39 D / 4.33 L) → sin dark:.
 * Esta asimetría es intencional y está medida; el gate de contraste (check-contrast.mjs)
 * la protege con filas `only:['dark']` para mode-chat y mode-group sobre tint10-L2.
 *
 * Este test es la red que falta al gate de contraste: la matriz mide el token
 * (mode-X) contra el fondo, pero no puede saber QUÉ texto pinta el markup
 * real en cada tema. Un revert de N1 (text pelado) deja lint:design verde —
 * aquí falla. Chat/group usan `text-text-primary dark:text-mode-X` (el color
 * queda en oscuro); agent usa `text-text-primary` pelado (ambos temas fallan AA).
 * La asimetría es intencional y está medida en check-contrast.mjs.
 */
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
    // `text-mode-group` solo puede aparecer prefijado con `dark:`.
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
    // Must NOT have a bare text-mode-chat (would fail light AA at 4.20)
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
