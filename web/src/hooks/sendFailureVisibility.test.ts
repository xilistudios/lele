/**
 * Send/upload failures are visible IN THE APP, not just in the dispatcher.
 *
 * Two ways a failure used to disappear without a word:
 *  1. no token: `handleUploadAttachments` returned `[]` immediately, which the
 *     composer reads as a failure but nothing explained it to the user;
 *  2. a refused send: the server's `error` event was dispatched nowhere (see
 *     event-handlers/serverError.test.ts) so the banner stayed empty.
 *
 * Both are asserted through the REAL `useAppLogic`, so the wiring from the
 * socket/upload path to `error` (-> `ChatPage.tsx` `ErrorBanner`) is what is
 * being tested, not a mock.
 */
import '../test/setup'
import '../test/i18n'

import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup } from '@testing-library/react'
import i18n from '../i18n'
import { renderAppLogic, stubApi } from '../test/appLogicHarness'

afterEach(() => cleanup())

describe('useAppLogic — upload failures reach the error banner', () => {
  test('M21: with no token the upload is refused AND explained', async () => {
    // No token also means no agent list (the agents effect is gated on it), so
    // there is nothing to wait for: flush the mount effects and upload.
    const { result } = renderAppLogic({ token: null })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })

    let paths: string[] | undefined
    await act(async () => {
      paths = await result.current.onUploadAttachments([new File(['x'], 'photo.png')])
    })

    // The signature must not change: AttachmentInput reads [] as "nothing was
    // attached" (and shows its own message only when it sees no paths at all).
    expect(paths).toEqual([])
    // M21: reverting to a bare `return []` leaves this null — a silent failure.
    expect(result.current.error).toBe(i18n.t('chat.notAuthenticated') as string)
  })

  test('a successful upload says nothing (no false alarm)', async () => {
    const { result, waitUntilReady } = renderAppLogic({
      api: stubApi({ uploadFiles: async () => ({ files: [{ path: '/uploads/a.png' }] }) }),
    })
    await waitUntilReady()

    let paths: string[] | undefined
    await act(async () => {
      paths = await result.current.onUploadAttachments([new File(['x'], 'photo.png')])
    })

    expect(paths).toEqual(['/uploads/a.png'])
    expect(result.current.error).toBe(null)
  })

  test('a failing upload surfaces the API message', async () => {
    const { result, waitUntilReady } = renderAppLogic({
      api: stubApi({
        uploadFiles: async () => {
          throw new Error('file too large')
        },
      }),
    })
    await waitUntilReady()

    let paths: string[] | undefined
    await act(async () => {
      paths = await result.current.onUploadAttachments([new File(['x'], 'photo.png')])
    })

    expect(paths).toEqual([])
    expect(result.current.error).toBe('file too large')
  })
})

describe('useAppLogic — a refused send is shown to the user', () => {
  test('the server `error` event lands in the banner (end-to-end wiring)', async () => {
    const { result, waitUntilReady, sessionKey } = renderAppLogic()
    await waitUntilReady()

    await act(async () => {
      result.current.handleEvent({
        event: 'error',
        data: {
          code: 'attachments_rejected',
          message: 'attachments could not be read; add text or re-upload',
        },
      })
    })

    expect(result.current.error).toBe(i18n.t('chat.errorAttachmentsRejected') as string)
    // The refusal is scoped to the send: it must not stop the session.
    expect(result.current.isProcessing).toBe(false)
    expect(sessionKey.length).toBeGreaterThan(0)
  })
})
