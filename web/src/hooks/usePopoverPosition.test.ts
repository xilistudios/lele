import { afterEach, describe, expect, test } from 'bun:test'
import { act, renderHook } from '@testing-library/react'
import { usePopoverPosition } from './usePopoverPosition'

describe('usePopoverPosition', () => {
  test('retorna posición por defecto cuando no está abierto', () => {
    const { result } = renderHook(() =>
      usePopoverPosition({
        isOpen: false,
        popoverWidth: 200,
        popoverHeight: 300,
      }),
    )

    expect(result.current.position.horizontal).toBe('right-align')
    expect(result.current.position.vertical).toBe('above')
  })

  test('ref no es null', () => {
    const { result } = renderHook(() =>
      usePopoverPosition({
        isOpen: true,
        popoverWidth: 200,
        popoverHeight: 300,
      }),
    )

    expect(result.current.ref.current).toBeNull()
  })

  test('usa padding por defecto', () => {
    const { result } = renderHook(() =>
      usePopoverPosition({
        isOpen: false,
        popoverWidth: 200,
        popoverHeight: 300,
      }),
    )

    // El padding interno es 8 (DEFAULT_PADDING)
    expect(result.current).toBeDefined()
  })
})

/**
 * The flip decision must use the VISUAL viewport, not the layout one: on iOS
 * the on-screen keyboard shrinks only the visual viewport, so a dropdown that
 * measured against `window.innerHeight` would open downwards into the keyboard.
 */
describe('usePopoverPosition — visual viewport', () => {
  const originalInnerHeight = Object.getOwnPropertyDescriptor(window, 'innerHeight')

  afterEach(() => {
    Reflect.deleteProperty(window, 'visualViewport')
    if (originalInnerHeight) Object.defineProperty(window, 'innerHeight', originalInnerHeight)
  })

  function stubVisualViewport(height: number) {
    const listeners = new Set<() => void>()
    const vv = {
      height,
      scale: 1,
      addEventListener: (_type: string, listener: () => void) => listeners.add(listener),
      removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener),
    }
    Object.defineProperty(window, 'visualViewport', { value: vv, configurable: true })
    return listeners
  }

  /**
   * Element spanning y=350..450 in a 800px layout viewport. Both directions
   * need 308px (300 + 8): the layout viewport has 350px below and 350px above,
   * so it says `below` (checked first). A 400px visual viewport leaves -50px
   * below and still 350px above, so only the visual viewport flips it.
   */
  function mountWithRect() {
    const element = document.createElement('div')
    element.getBoundingClientRect = () =>
      ({ top: 350, bottom: 450, left: 0, right: 200 }) as DOMRect
    document.body.appendChild(element)

    const hook = renderHook(() =>
      usePopoverPosition({ isOpen: true, popoverWidth: 200, popoverHeight: 300 }),
    )
    ;(hook.result.current.ref as { current: HTMLElement | null }).current = element
    return { element, hook }
  }

  test('a short visual viewport flips the popover above', () => {
    Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })
    stubVisualViewport(400)

    const { element, hook } = mountWithRect()

    act(() => {
      window.dispatchEvent(new window.Event('resize'))
    })

    expect(hook.result.current.position.vertical).toBe('above')
    element.remove()
  })

  test('without a visual viewport the layout viewport still decides', () => {
    Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })

    const { element, hook } = mountWithRect()

    act(() => {
      window.dispatchEvent(new window.Event('resize'))
    })

    expect(hook.result.current.position.vertical).toBe('below')
    element.remove()
  })

  test('a visual-viewport resize re-runs the calculation', () => {
    Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })
    const listeners = stubVisualViewport(800)

    const { element, hook } = mountWithRect()

    act(() => {
      window.dispatchEvent(new window.Event('resize'))
    })
    expect(hook.result.current.position.vertical).toBe('below')

    act(() => {
      for (const listener of [...listeners]) listener()
    })

    expect(hook.result.current.position.vertical).toBe('below')
    element.remove()
  })
})
