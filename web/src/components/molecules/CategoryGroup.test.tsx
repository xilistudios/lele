import { describe, expect, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import '../../test/i18n'
import { CategoryGroup } from './CategoryGroup'

function setup(overrides: { defaultOpen?: boolean; open?: boolean } = {}) {
  const utils = render(
    <CategoryGroup activeCount={6} title="Archivos" totalCount={8} {...overrides}>
      <div data-testid="payload">tool cards</div>
    </CategoryGroup>,
  )
  const header = utils.getByRole('button')
  const panelId = header.getAttribute('aria-controls') as string
  const panel = utils.container.querySelector(`[id="${panelId}"]`) as HTMLElement
  return { ...utils, header, panel }
}

describe('CategoryGroup', () => {
  test('header shows the title and the active/total counter', () => {
    const { header } = setup()
    expect(header.textContent).toContain('Archivos')
    // i18n key categoryCount renders "{{active}} de {{total}}" in the test locale.
    expect(header.textContent).toMatch(/6.*8/)
  })

  test('is expanded by default (§4.6.4)', () => {
    const { header, panel } = setup()
    expect(header.getAttribute('aria-expanded')).toBe('true')
    expect(panel.className).toContain('grid-rows-[1fr]')
    expect(panel.querySelector('[data-testid="payload"]')).toBeTruthy()
  })

  test('clicking the header collapses: aria-expanded false + grid-rows-[0fr]', () => {
    const { header, panel } = setup()
    fireEvent.click(header)
    expect(header.getAttribute('aria-expanded')).toBe('false')
    expect(panel.className).toContain('grid-rows-[0fr]')
    expect(panel.className).not.toContain('grid-rows-[1fr]')
    fireEvent.click(header)
    expect(header.getAttribute('aria-expanded')).toBe('true')
    expect(panel.className).toContain('grid-rows-[1fr]')
  })

  test('countLabel overrides the i18n counter', () => {
    const { getByRole } = render(
      <CategoryGroup activeCount={1} countLabel="1 activa" title="Web" totalCount={2}>
        <span />
      </CategoryGroup>,
    )
    expect(getByRole('button').textContent).toContain('(1 activa)')
  })

  test('defaultOpen=false starts collapsed', () => {
    const { header, panel } = setup({ defaultOpen: false })
    expect(header.getAttribute('aria-expanded')).toBe('false')
    expect(panel.className).toContain('grid-rows-[0fr]')
  })

  test('the controlled `open` prop wins over internal state (search auto-expand)', () => {
    const { header, panel } = setup({ open: false })
    expect(header.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(header)
    // Still closed: `open` is controlled, the click cannot override it.
    expect(header.getAttribute('aria-expanded')).toBe('false')
    expect(panel.className).toContain('grid-rows-[0fr]')
  })

  test('aria-controls points at the panel that holds the children', () => {
    const { header, panel } = setup()
    expect(header.getAttribute('aria-controls')).toBe(panel.id)
    expect(panel).toBeTruthy()
    expect(panel.textContent).toContain('tool cards')
  })

  test('uses the NamedItemCard grid trick: grid + transition-all duration-200', () => {
    const { panel } = setup()
    expect(panel.className).toContain('grid')
    expect(panel.className).toContain('transition-all')
    expect(panel.className).toContain('duration-200')
    expect((panel.firstElementChild as HTMLElement).className).toContain('overflow-hidden')
  })

  test('header geometry + chevron rotation', () => {
    const { container, header } = setup()
    expect(header.className).toContain('h-9')
    expect(header.className).toContain('w-full')
    const chevron = container.querySelector('svg') as SVGSVGElement
    expect(chevron.getAttribute('width')).toBe('12')
    expect(chevron.getAttribute('height')).toBe('12')
    expect(chevron.className.baseVal).toContain('transition-transform')
    expect(chevron.className.baseVal).toContain('rotate-90')
    fireEvent.click(header)
    expect(chevron.className.baseVal).not.toContain('rotate-90')
  })

  test('icon slot is decorative', () => {
    const { container } = render(
      <CategoryGroup activeCount={0} icon={<svg data-testid="icon" />} title="Web" totalCount={2}>
        <span />
      </CategoryGroup>,
    )
    const wrapper = container.querySelector('span[aria-hidden="true"]')
    expect(wrapper?.querySelector('[data-testid="icon"]')).toBeTruthy()
  })
})
