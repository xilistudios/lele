import '../../test/setup'
import '../../test/i18n'

import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import i18n from '../../i18n'
import { SEND_ERROR_I18N_KEYS, sendErrorMessage } from '../../lib/sendErrors'
import { dispatchMessageEvent } from './index'
import type { MessageEventContext } from './types'

const WEBSOCKET_GO = join(import.meta.dir, '../../../../pkg/channels/websocket.go')

function makeCtx() {
  const errors: string[] = []
  const ctx = {
    currentSessionKeyRef: { current: 'native:s1' },
    parentSessionKeyRef: { current: null },
    streamingMessagesRef: { current: [] },
    setStreamingMessages: () => {},
    setPendingAttachments: () => {},
    setError: (message: string) => errors.push(message),
    setTypingIndicator: () => {},
  } as unknown as MessageEventContext
  return { ctx, errors }
}

describe('dispatchMessageEvent — server `error` event', () => {
  test('M20: the event is dispatched and reaches setError (the banner)', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, {
      event: 'error',
      data: { code: 'content_missing', message: 'message content or an attachment is required' },
    })

    expect(errors.length).toBe(1)
    expect(errors[0]).toBe(i18n.t('chat.errorContentMissing'))
    expect(errors[0]).not.toContain('message content or an attachment')
  })

  test('the post-processing refusal (attachments_rejected) is surfaced too', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, {
      event: 'error',
      data: {
        code: 'attachments_rejected',
        message: 'attachments could not be read; add text or re-upload',
      },
    })

    expect(errors).toEqual([i18n.t('chat.errorAttachmentsRejected')])
  })

  test('the session refusal (`forbidden`, the code the WS really sends) is translated', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, {
      event: 'error',
      data: { code: 'forbidden', message: 'access denied to this session' },
    })

    expect(errors).toEqual([i18n.t('chat.errorAccessDenied')])
    expect(errors[0]).not.toContain('access denied')
  })

  test('an unrelated event still does not touch the error banner', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, { event: 'typing.indicator', data: { session_key: 'native:s1' } })

    expect(errors).toEqual([])
  })

  test('an event with no data at all does not throw', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, { event: 'error', data: undefined })

    expect(errors).toEqual([i18n.t('chat.failedToSend')])
  })
})

describe('sendErrorMessage — code → text', () => {
  test('every mapped code has a real translation in all three locales', () => {
    const codes = Object.keys(SEND_ERROR_I18N_KEYS)
    expect(codes.sort()).toEqual(['attachments_rejected', 'content_missing', 'forbidden'])

    for (const locale of ['en', 'es', 'pt']) {
      for (const code of codes) {
        const text = i18n.getFixedT(locale)(SEND_ERROR_I18N_KEYS[code])
        expect(text).not.toBe(SEND_ERROR_I18N_KEYS[code])
        expect((text as string).length).toBeGreaterThan(0)
      }
    }
  })

  test('each code maps to its own key (no copy-paste collisions)', () => {
    const keys = Object.values(SEND_ERROR_I18N_KEYS)
    expect(new Set(keys).size).toBe(keys.length)
  })

  test('an unknown code falls back to the server message, then to the generic key', () => {
    expect(sendErrorMessage('brand_new_code', 'The server says no')).toBe('The server says no')
    expect(sendErrorMessage('', '')).toBe(i18n.t('chat.failedToSend'))
    expect(sendErrorMessage('brand_new_code')).toBe(i18n.t('chat.failedToSend'))
  })

  test('the table only maps codes the WS producer really sends (source pin)', () => {
    const source = readFileSync(WEBSOCKET_GO, 'utf8')
    const produced = new Set(
      [...source.matchAll(/sendError\(client,\s*"([a-z_]+)"/g)].map((match) => match[1]),
    )

    expect(produced.has('forbidden')).toBe(true)
    expect(produced.has('content_missing')).toBe(true)
    expect(produced.has('attachments_rejected')).toBe(true)

    for (const code of Object.keys(SEND_ERROR_I18N_KEYS)) {
      expect(produced.has(code)).toBe(true)
    }
  })
})
