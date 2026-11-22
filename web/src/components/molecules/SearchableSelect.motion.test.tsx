import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render } from '@testing-library/react'
import { SearchableSelect } from './SearchableSelect'

const OPTIONS = [
  { value: 'gpt-4', label: 'gpt-4' },
  { value: 'gpt-4o', label: 'gpt-4o' },
]

async function waitUntil(predicate: () => boolean, timeoutMs = 1000): Promise<void> {
  const deadline = Date.now() + timeoutMs

  while (Date.now() < deadline) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }

  throw new Error('condition not met before timeout')
}

function renderSelect(onChange: (value: string) => void = () => undefined) {
  const view = render(
    <SearchableSelect
      ariaLabel="Modelo"
      buttonLabel="Modelo"
      emptyLabel="Sin resultados"
      onChange={onChange}
      options={OPTIONS}
      placeholder="Elegí"
      searchAriaLabel="Buscar modelos"
      searchPlaceholder="Buscar"
      value="gpt-4"
    />,
  )
  const trigger = view.container.querySelector('button[aria-label="Modelo"]') as HTMLButtonElement

  return { container: view.container, trigger }
}

function panelOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector('[data-state]')
}

function panelState(container: HTMLElement): string | null {
  return panelOf(container)?.getAttribute('data-state') ?? null
}

function classListOf(container: HTMLElement): string[] {
  return (panelOf(container)?.getAttribute('class') ?? '').split(/\s+/)
}

async function openPanel(container: HTMLElement, trigger: HTMLButtonElement): Promise<void> {
  fireEvent.click(trigger)
  await waitUntil(() => panelState(container) === 'open')
}

afterEach(cleanup)

describe('SearchableSelect — open and exit motion', () => {
  test('the panel mounts hidden and reaches the open state on a later frame', async () => {
    const { container, trigger } = renderSelect()

    fireEvent.click(trigger)

    expect(panelState(container)).toBe('closed')

    await waitUntil(() => panelState(container) === 'open')
  })

  test('the panel enters rising from below', async () => {
    const { container, trigger } = renderSelect()

    fireEvent.click(trigger)
    expect(classListOf(container)).toContain('translate-y-2')

    await waitUntil(() => panelState(container) === 'open')
    expect(classListOf(container)).toContain('translate-y-0')
  })

  test('dismissing outside unmounts the panel only after it has animated out', async () => {
    const { container, trigger } = renderSelect()

    await openPanel(container, trigger)
    fireEvent.mouseDown(document.body)

    expect(panelState(container)).toBe('closed')
    expect(panelOf(container)).not.toBeNull()

    await waitUntil(() => panelOf(container) === null)
  })

  test('Escape also plays the exit animation before unmounting', async () => {
    const { container, trigger } = renderSelect()

    await openPanel(container, trigger)
    fireEvent.keyDown(document, { key: 'Escape' })

    expect(panelState(container)).toBe('closed')
    expect(panelOf(container)).not.toBeNull()

    await waitUntil(() => panelOf(container) === null)
  })

  test('the trigger re-opens a panel that is still animating out', async () => {
    const { container, trigger } = renderSelect()

    await openPanel(container, trigger)
    fireEvent.mouseDown(document.body)
    fireEvent.click(trigger)

    await waitUntil(() => panelState(container) === 'open')
  })

  test('picking an option reports the value and animates the panel out', async () => {
    const picked: string[] = []
    const { container, trigger } = renderSelect((value) => picked.push(value))

    await openPanel(container, trigger)
    const option = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === 'gpt-4o',
    ) as HTMLButtonElement
    fireEvent.click(option)

    expect(picked).toEqual(['gpt-4o'])
    expect(panelState(container)).toBe('closed')

    await waitUntil(() => panelOf(container) === null)
  })
})
describe('SearchableSelect — animated list height', () => {
  const observers: Array<() => void> = []

  class StubResizeObserver {
    constructor(callback: () => void) {
      observers.push(callback)
    }

    observe() {}

    disconnect() {}
  }

  afterEach(() => {
    observers.length = 0
    Reflect.deleteProperty(globalThis, 'ResizeObserver')
  })

  test('the list wrapper follows the measured content height', async () => {
    Object.defineProperty(globalThis, 'ResizeObserver', {
      value: StubResizeObserver,
      configurable: true,
    })
    const { container, trigger } = renderSelect()

    await openPanel(container, trigger)

    const list = container.querySelector('.max-h-64') as HTMLElement
    Object.defineProperty(list, 'offsetHeight', { value: 120, configurable: true })
    act(() => {
      for (const notify of observers) notify()
    })

    expect((list.parentElement as HTMLElement).style.height).toBe('120px')
  })
})
