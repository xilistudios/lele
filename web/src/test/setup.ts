import { afterEach, mock } from 'bun:test'
import { JSDOM } from 'jsdom'
import React from 'react'

import '@testing-library/jest-dom'

// react-virtuoso relies on container measurements (getBoundingClientRect /
// ResizeObserver) that are always 0 in jsdom, so the real component renders
// no items and integration tests cannot find message text. Replace it with a
// simple non-virtualized list that renders every item through the same
// props (data, itemContent, computeItemKey, components.Header/Footer) so
// behavior is preserved without depending on layout measurements.
mock.module('react-virtuoso', () => {
  const Virtuoso = React.forwardRef<unknown, Record<string, unknown>>(
    function Virtuoso(props, ref) {
      const { data, itemContent, computeItemKey, components } = props as {
        data: unknown[]
        itemContent: (index: number, item: unknown) => React.ReactNode
        computeItemKey?: (index: number, item: unknown) => string
        components?: {
          Header?: React.ComponentType
          Footer?: React.ComponentType
        }
      }

      if (typeof ref === 'function') {
        ref({ scrollToIndex: () => undefined, scrollTo: () => undefined })
      } else if (ref && typeof ref === 'object') {
        ;(ref as { current?: unknown }).current = {
          scrollToIndex: () => undefined,
          scrollTo: () => undefined,
        }
      }

      const items = (data ?? []).map((item, index) => {
        const key = computeItemKey ? computeItemKey(index, item) : index
        return React.createElement('div', { key }, itemContent(index, item))
      })

      return React.createElement(
        'div',
        { 'data-testid': 'virtuoso-mock' },
        components?.Header ? React.createElement(components.Header) : null,
        items,
        components?.Footer ? React.createElement(components.Footer) : null,
      )
    },
  )
  Virtuoso.displayName = 'Virtuoso'

  return {
    Virtuoso,
    TableVirtuoso: Virtuoso,
    GroupedVirtuoso: Virtuoso,
    VirtuosoHandle: {},
    VirtuosoGrid: Virtuoso,
    VirtuosoGridHandle: {},
    VirtuosoHandleMethods: {},
    default: Virtuoso,
  }
})

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' })

Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  navigator: dom.window.navigator,
  localStorage: dom.window.localStorage,
  sessionStorage: dom.window.sessionStorage,
})

dom.window.requestAnimationFrame = (cb: FrameRequestCallback) => setTimeout(cb, 16)
dom.window.cancelAnimationFrame = (id: number) => clearTimeout(id)

if (!globalThis.matchMedia) {
  globalThis.matchMedia = (() => ({
    matches: false,
    media: '',
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  })) as never
}

if (!dom.window.matchMedia) {
  dom.window.matchMedia = globalThis.matchMedia
}

// Mock scrollIntoView for jsdom
dom.window.Element.prototype.scrollIntoView = () => undefined

// Mock scrollTo for jsdom
dom.window.Element.prototype.scrollTo = () => undefined
dom.window.scrollTo = () => undefined

afterEach(() => {
  document.body.innerHTML = ''
})
