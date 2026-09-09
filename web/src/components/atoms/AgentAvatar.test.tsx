import { describe, expect, test } from 'bun:test'
import { render } from '@testing-library/react'
import { AgentAvatar, agentIdHash, gradientForId } from './AgentAvatar'

// The seven gradients of spec §3.6, in order.
const GRADIENTS = [
  'from-interaction-primary to-brand-morado',
  'from-brand-morado to-brand-rosa',
  'from-brand-turquesa to-secondary-azul',
  'from-brand-naranja to-brand-rosa',
  'from-secondary-verde to-brand-turquesa',
  'from-brand-rosa to-brand-morado',
  'from-secondary-azul to-brand-morado',
]

function box(size: 'sm' | 'md' | 'xl') {
  const { container } = render(<AgentAvatar id="coder" size={size} />)
  return container.firstElementChild as HTMLElement
}

describe('agentIdHash / gradientForId', () => {
  test('hash matches the literal spec algorithm (seed 7, *31, >>>0)', () => {
    expect(agentIdHash('coder')).toBe(
      [...'coder'].reduce((a, c) => (a * 31 + c.charCodeAt(0)) >>> 0, 7),
    )
    expect(agentIdHash('')).toBe(7)
    // A hash used to pick a class must never be negative.
    for (const id of ['coder', 'lele', 'default', 'x'.repeat(40), 'ünïcødé']) {
      expect(agentIdHash(id)).toBeGreaterThanOrEqual(0)
      expect(Number.isInteger(agentIdHash(id))).toBe(true)
    }
  })

  test('gradient is the palette entry at hash % 7', () => {
    for (const id of ['coder', 'lele', 'default', 'research']) {
      expect(GRADIENTS).toContain(gradientForId(id))
      expect(gradientForId(id)).toBe(GRADIENTS[agentIdHash(id) % GRADIENTS.length])
    }
  })

  test('different ids can land on different gradients (no constant palette index)', () => {
    const distinct = new Set(['br', 'ba', 'b', 'lele'].map((id) => gradientForId(id)))
    expect(distinct.size).toBeGreaterThan(1)
  })
})

describe('AgentAvatar', () => {
  test('is decorative: aria-hidden always', () => {
    const { container } = render(<AgentAvatar id="coder" name="Coder" />)
    const el = container.firstElementChild as HTMLElement
    expect(el.getAttribute('aria-hidden')).toBe('true')
  })

  test('uses the name initial when present, otherwise the id initial, uppercased', () => {
    expect(render(<AgentAvatar id="coder" name="Senior Coder" />).container.textContent).toBe('S')
    expect(render(<AgentAvatar id="coder" />).container.textContent).toBe('C')
    expect(render(<AgentAvatar id="coder" name="   " />).container.textContent).toBe('C')
    expect(render(<AgentAvatar id="ñu" />).container.textContent).toBe('Ñ')
  })

  test('sizes: sm 20px · md 40px · xl 48px, each with its text + radius', () => {
    const sm = box('sm')
    expect(sm.className).toContain('h-5')
    expect(sm.className).toContain('w-5')
    expect(sm.className).toContain('text-[10px]')
    expect(sm.className).toContain('rounded-md')

    const md = box('md')
    expect(md.className).toContain('h-10')
    expect(md.className).toContain('text-sm')
    expect(md.className).toContain('rounded-lg')

    const xl = box('xl')
    expect(xl.className).toContain('h-12')
    expect(xl.className).toContain('text-lg')
    expect(xl.className).toContain('rounded-xl')
  })

  test('defaults to md', () => {
    const { container } = render(<AgentAvatar id="coder" />)
    expect((container.firstElementChild as HTMLElement).className).toContain('h-10')
  })

  test('renders a gradient from tokens only, never a raw hex', () => {
    const el = box('md')
    expect(el.className).toContain('bg-gradient-to-br')
    expect(el.className).toMatch(/from-\S+ to-\S+/)
    expect(el.className).not.toMatch(/#[0-9a-f]{3,6}/i)
  })

  test('stable across renders and re-mounts (same id => same classes)', () => {
    const first = box('md').className
    const second = box('md').className
    const third = render(<AgentAvatar id="coder" size="md" />).container
      .firstElementChild as HTMLElement
    expect(second).toBe(first)
    expect(third.className).toBe(first)
  })

  test('extra className is appended, tokens preserved', () => {
    const { container } = render(<AgentAvatar className="max-md:h-8 max-md:w-8" id="coder" />)
    const el = container.firstElementChild as HTMLElement
    expect(el.className).toContain('max-md:h-8')
    expect(el.className).toContain('h-10')
  })
})
