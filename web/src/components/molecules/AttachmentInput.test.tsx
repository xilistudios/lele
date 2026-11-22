import '../../test/setup'
import '../../test/i18n'

import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render } from '@testing-library/react'
import i18n from '../../i18n'
import { AttachmentInput } from './AttachmentInput'

function fileInput(): HTMLInputElement {
  const input = document.querySelector('input[type="file"]')
  if (!input) throw new Error('file input not found')
  return input as HTMLInputElement
}

function fakeFiles(count: number): File[] {
  return Array.from({ length: count }, (_, i) => new File(['x'], `file-${i}.png`))
}

function alertText(): string | null {
  return document.querySelector('[role="alert"]')?.textContent ?? null
}

async function pickFiles(count: number) {
  await act(async () => {
    fireEvent.change(fileInput(), { target: { files: fakeFiles(count) } })
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

function renderInput(onUpload: () => Promise<string[]>) {
  const attached: string[][] = []
  render(<AttachmentInput onUpload={onUpload} onAttach={(paths) => attached.push(paths)} />)
  return { attached }
}

afterEach(() => cleanup())

describe('AttachmentInput — upload outcomes', () => {
  test('total failure (0 paths for 3 files): the failure message, no chips', async () => {
    const { attached } = renderInput(async () => [])

    await pickFiles(3)

    expect(alertText()).toBe(i18n.t('chat.uploadFailed') as string)
    expect(attached).toEqual([])
  })

  test('M22: partial failure (3 requested, 2 returned) warns AND keeps the 2 chips', async () => {
    const { attached } = renderInput(async () => ['/a/one.png', '/a/two.png'])

    await pickFiles(3)

    expect(alertText()).toBe(i18n.t('chat.uploadPartial', { failed: 1, total: 3 }) as string)
    expect(attached).toEqual([['/a/one.png', '/a/two.png']])
  })

  test('full success (2 requested, 2 returned) stays silent', async () => {
    const { attached } = renderInput(async () => ['/a/one.png', '/a/two.png'])

    await pickFiles(2)

    expect(alertText()).toBe(null)
    expect(attached).toEqual([['/a/one.png', '/a/two.png']])
  })

  test('a rejected upload promise is reported too', async () => {
    const { attached } = renderInput(async () => {
      throw new Error('boom')
    })
    const originalError = console.error
    console.error = () => {}
    try {
      await pickFiles(1)
    } finally {
      console.error = originalError
    }

    expect(alertText()).toBe(i18n.t('chat.uploadFailed') as string)
    expect(attached).toEqual([])
  })
})
