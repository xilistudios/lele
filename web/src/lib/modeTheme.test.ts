/**
 * modeTheme.test.ts — contrato §2.4 (excepción group-claro)
 *
 * Regla: el color de identidad `mode-group` (#C2410C claro) NO alcanza 4.5:1
 * sobre sus propios tintes (tinte/10 = 4.48; tinte doble = 3.92 ❌). Por eso los
 * CUATRO roles de texto de group (text, chip, selectedItem, iconCircle) deben
 * llevar la excepción explícita: `text-text-primary dark:text-mode-group`.
 *
 * Este test es la red que falta al gate de contraste: la matriz mide el token
 * (mode-group) contra el fondo, pero no puede saber QUÉ texto pinta el markup
 * real en cada tema. Un revert de N1 (text pelado) deja lint:design verde —
 * aquí falla. Chat/agent sí usan el color pelado (pasan 4.5 sobre sus tintes);
 * la asimetría es intencional y está medida en check-contrast.mjs.
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

  test('ningún rol de texto de group pinta mode-group pelado en claro', () => {
    // `text-mode-group` solo puede aparecer prefijado con `dark:`.
    for (const role of ['text', 'chip', 'selectedItem', 'iconCircle'] as const) {
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

describe('chat/agent: color pelado permitido (medido ≥4.5 sobre sus tintes)', () => {
  test('roles de texto no requieren excepción', () => {
    for (const mode of ['chat', 'agent'] as const) {
      const t = getModeTheme(mode)
      expect(t.text).toBe(`text-mode-${mode}`)
      expect(t.chip).toContain(`text-mode-${mode}`)
    }
  })
})
