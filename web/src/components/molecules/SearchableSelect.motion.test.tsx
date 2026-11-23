import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render } from '@testing-library/react'
import { waitUntil } from '../../test/waitUntil'
import { SearchableSelect } from './SearchableSelect'

const OPTIONS = [
  { value: 'gpt-4', label: 'gpt-4' },
  { value: 'gpt-4o', label: 'gpt-4o' },
]

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
  const trigger = view.getByRole('button', { name: 'Modelo' }) as HTMLButtonElement

  return { container: view.container, trigger, view }
}

function panelOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector('[data-state]')
}

function panelState(container: HTMLElement): string | null {
  return panelOf(container)?.getAttribute('data-state') ?? null
}

function listWrapperOf(container: HTMLElement): HTMLElement | null {
  return (panelOf(container)?.lastElementChild as HTMLElement | null) ?? null
}

function listOf(container: HTMLElement): HTMLElement | null {
  return (listWrapperOf(container)?.firstElementChild as HTMLElement | null) ?? null
}

function isOpen(container: HTMLElement): boolean {
  return panelState(container) === 'open'
}

async function openPanel(container: HTMLElement, trigger: HTMLButtonElement): Promise<void> {
  fireEvent.click(trigger)
  await waitUntil(() => isOpen(container))
}

afterEach(cleanup)

describe('SearchableSelect — open and exit motion', () => {
  test('the panel mounts dismissed and reaches the open state on a later frame', async () => {
    const { container, trigger } = renderSelect()

    fireEvent.click(trigger)

    expect(panelState(container)).toBe('closed')

    await waitUntil(() => isOpen(container))
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

    await waitUntil(() => isOpen(container))
  })

  test('picking an option reports the value and animates the panel out', async () => {
    const picked: string[] = []
    const { container, trigger, view } = renderSelect((value) => picked.push(value))

    await openPanel(container, trigger)
    fireEvent.click(view.getByRole('button', { name: 'gpt-4o' }))

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

  const notifyResize = () => {
    act(() => {
      for (const notify of observers) notify()
    })
  }

  const measureList = (container: HTMLElement, height: number) => {
    const list = listOf(container) as HTMLElement
    Object.defineProperty(list, 'offsetHeight', { value: height, configurable: true })
    notifyResize()
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
    measureList(container, 120)

    expect(listWrapperOf(container)?.style.height).toBe('120px')
  })

  test('reopening during the exit animation keeps the measured height', async () => {
    Object.defineProperty(globalThis, 'ResizeObserver', {
      value: StubResizeObserver,
      configurable: true,
    })
    const { container, trigger } = renderSelect()

    await openPanel(container, trigger)
    measureList(container, 120)
    expect(listWrapperOf(container)?.style.height).toBe('120px')

    fireEvent.mouseDown(document.body)
    expect(panelState(container)).toBe('closed')

    fireEvent.click(trigger)
    await waitUntil(() => isOpen(container))

    expect(listWrapperOf(container)?.style.height).toBe('120px')
  })
})
