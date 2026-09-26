/**
 * useBackendStatus tests — polling cadence and visibility gating.
 *
 * The sidecar status poll (Tauri IPC `backend_status`) ran every 3 s in every
 * desktop window, background or not. These tests pin the new contract:
 *   1. visible: one poll per 3 s window (unchanged),
 *   2. hidden: no interval, no IPC call,
 *   3. becoming visible: exactly one immediate poll, then the same cadence,
 *   4. `enabled === false` still clears the reported status and polls nothing.
 *
 * The Tauri bridge is faked through `window.__TAURI_INTERNALS__`, which is the
 * only thing `invokeDesktop` looks at.
 */

import { afterEach, beforeEach, describe, expect, jest, mock, test } from 'bun:test'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { BackendStatus } from './useBackendStatus'
import { useBackendStatus } from './useBackendStatus'

const RUNNING_STATUS: BackendStatus = {
  running: true,
  pid: 4242,
  port: 18793,
  uptime_secs: 12,
  url: 'http://127.0.0.1:18793',
}

type TauriWindow = Window & { __TAURI_INTERNALS__?: { invoke: (...args: unknown[]) => unknown } }

beforeEach(() => {
  ;(window as TauriWindow).__TAURI_INTERNALS__ = {
    invoke: mock(async (command: unknown) =>
      command === 'backend_status' ? { ...RUNNING_STATUS } : null,
    ),
  }
})

afterEach(() => {
  cleanup()
  Reflect.deleteProperty(window as TauriWindow, '__TAURI_INTERNALS__')
  jest.useRealTimers()
  setVisibility('visible')
})

/** jsdom never fires `visibilitychange`; `visibilityState` is a prototype
 *  getter, so both are simulated the way a browser does (see
 *  `services/ws/client.test.ts`). */
function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

function invokeMock(): ReturnType<typeof mock> {
  return (window as TauriWindow).__TAURI_INTERNALS__?.invoke as ReturnType<typeof mock>
}

/** Number of `backend_status` IPC calls made so far. */
function statusCallCount(): number {
  return invokeMock().mock.calls.filter((call) => call[0] === 'backend_status').length
}

async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 10; i += 1) await Promise.resolve()
  })
}

describe('useBackendStatus polling', () => {
  test('polls once per 3 s window while the page is visible', async () => {
    jest.useFakeTimers()

    const { result } = renderHook(() => useBackendStatus(true))
    await flushEffects()
    expect(statusCallCount()).toBe(1)
    expect(result.current.status?.running).toBe(true)

    act(() => {
      jest.advanceTimersByTime(2999)
    })
    await flushEffects()
    expect(statusCallCount()).toBe(1)

    act(() => {
      jest.advanceTimersByTime(1)
    })
    await flushEffects()
    expect(statusCallCount()).toBe(2)
  })

  test('does not poll while hidden and polls once when shown again', async () => {
    jest.useFakeTimers()

    renderHook(() => useBackendStatus(true))
    await flushEffects()
    expect(statusCallCount()).toBe(1)

    act(() => setVisibility('hidden'))
    await flushEffects()

    // A minute hidden: 20 ticks would have been ~20 IPC calls per window.
    act(() => {
      jest.advanceTimersByTime(60_000)
    })
    await flushEffects()
    expect(statusCallCount()).toBe(1)

    act(() => setVisibility('visible'))
    await flushEffects()
    expect(statusCallCount()).toBe(2)

    act(() => {
      jest.advanceTimersByTime(3000)
    })
    await flushEffects()
    expect(statusCallCount()).toBe(3)
  })

  test('a window mounted while hidden makes no call until it is shown', async () => {
    jest.useFakeTimers()

    setVisibility('hidden')
    const { result } = renderHook(() => useBackendStatus(true))
    await flushEffects()

    // Unlike the list pollers (whose mount fetch lives in its own effect), the
    // status poll and its interval share one effect, so a hidden window is
    // completely silent — status simply stays null.
    expect(statusCallCount()).toBe(0)
    expect(result.current.status).toBeNull()

    act(() => {
      jest.advanceTimersByTime(30_000)
    })
    await flushEffects()
    expect(statusCallCount()).toBe(0)

    // Shown: exactly one immediate poll, then the 3 s cadence.
    act(() => setVisibility('visible'))
    await flushEffects()
    expect(statusCallCount()).toBe(1)
    expect(result.current.status?.running).toBe(true)

    act(() => {
      jest.advanceTimersByTime(3000)
    })
    await flushEffects()
    expect(statusCallCount()).toBe(2)
  })

  test('enabled = false neither polls nor keeps a previous status', async () => {
    jest.useFakeTimers()

    const { result, rerender } = renderHook(({ enabled }) => useBackendStatus(enabled), {
      initialProps: { enabled: true },
    })
    await flushEffects()
    expect(result.current.status?.running).toBe(true)

    rerender({ enabled: false })
    await flushEffects()
    expect(result.current.status).toBeNull()

    const callsWhenDisabled = statusCallCount()
    act(() => {
      jest.advanceTimersByTime(30_000)
    })
    await flushEffects()
    expect(statusCallCount()).toBe(callsWhenDisabled)
  })
})
