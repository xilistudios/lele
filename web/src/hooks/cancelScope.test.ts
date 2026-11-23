import '../test/setup'
import '../test/i18n'

import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup } from '@testing-library/react'
import { renderAppLogic } from '../test/appLogicHarness'

afterEach(() => cleanup())

const SESSION_KEY = 'native:test-session'

const cancelFrames = (sent: { event: string; data: Record<string, unknown> }[]) =>
  sent.filter((e) => e.event === 'cancel')

describe('useAppLogic — cancel scope', () => {
  test('the stop button asks for a session-wide stop', async () => {
    const { result, waitUntilReady, sent } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    await act(async () => {
      result.current.onCancel()
    })

    expect(cancelFrames(sent)).toEqual([
      { event: 'cancel', data: { session_key: SESSION_KEY, scope: 'session' } },
    ])
  })

  test('send now interrupts only the turn and leaves the queue flushed by the turn end', async () => {
    const { result, waitUntilReady, sent } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    await act(async () => {
      result.current.setProcessingSessions(new Set([SESSION_KEY]))
    })

    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.onSend('hola', [])
    })

    expect(accepted).toBe(true)
    expect(sent.filter((e) => e.event === 'message').length).toBe(0)
    expect(result.current.queuedMessages.length).toBe(1)

    const queuedId = result.current.queuedMessages[0].id
    await act(async () => {
      result.current.sendNowQueuedMessage(queuedId)
    })

    expect(cancelFrames(sent)).toEqual([
      { event: 'cancel', data: { session_key: SESSION_KEY, scope: 'turn' } },
    ])
    expect(result.current.queuedMessages.length).toBe(0)
  })
})
