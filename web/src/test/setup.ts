import { afterEach } from 'bun:test'
import { JSDOM } from 'jsdom'

import '@testing-library/jest-dom'

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

// Mock scrollIntoView for jsdom
dom.window.Element.prototype.scrollIntoView = () => undefined

afterEach(() => {
  document.body.innerHTML = ''
})
