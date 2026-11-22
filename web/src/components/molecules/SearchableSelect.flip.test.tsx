import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { SearchableSelect } from './SearchableSelect'

const originalInnerHeight = Object.getOwnPropertyDescriptor(window, 'innerHeight')

function stubInnerHeight(height: number): void {
  Object.defineProperty(window, 'innerHeight', { value: height, configurable: true })
}

function stubVisualViewport(height: number): void {
  const visualViewport = {
    height,
    scale: 1,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  }
  Object.defineProperty(window, 'visualViewport', { value: visualViewport, configurable: true })
}

function stubTriggerRect(container: HTMLElement, bottom: number): void {
  const root = container.querySelector('div.relative')
  if (!root) throw new Error('select root not rendered')
  root.getBoundingClientRect = () =>
    ({ top: bottom - 40, bottom, left: 0, right: 200, height: 40, width: 200 }) as DOMRect
}

function renderSelect() {
  return render(
    <SearchableSelect
      ariaLabel="Modelo"
      buttonLabel="Modelo"
      emptyLabel="Sin resultados"
      onChange={() => undefined}
      options={[{ value: 'gpt-4', label: 'gpt-4' }]}
      placeholder="Elegí"
      searchAriaLabel="Buscar"
      searchPlaceholder="Buscar"
      value="gpt-4"
    />,
  )
}

function panelOf(container: HTMLElement): Element | null {
  return container.querySelector('div.absolute')
}

afterEach(() => {
  cleanup()
  Reflect.deleteProperty(window, 'visualViewport')
  if (originalInnerHeight) Object.defineProperty(window, 'innerHeight', originalInnerHeight)
})

describe('SearchableSelect — visual viewport flip', () => {
  test('a short visual viewport opens the panel upwards', async () => {
    stubInnerHeight(800)
    stubVisualViewport(400)

    const { container } = renderSelect()
    stubTriggerRect(container, 400)

    fireEvent.click(container.querySelector('button') as HTMLButtonElement)

    await waitFor(() => expect(panelOf(container)).not.toBeNull())
    expect((panelOf(container)?.getAttribute('class') ?? '').split(/\s+/)).toContain('bottom-full')
  })

  test('without a visual viewport the layout viewport decides', async () => {
    stubInnerHeight(800)

    const { container } = renderSelect()
    stubTriggerRect(container, 300)

    fireEvent.click(container.querySelector('button') as HTMLButtonElement)

    await waitFor(() => expect(panelOf(container)).not.toBeNull())
    expect((panelOf(container)?.getAttribute('class') ?? '').split(/\s+/)).toContain('top-full')
  })
})
