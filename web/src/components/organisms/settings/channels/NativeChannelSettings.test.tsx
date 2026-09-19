import '../../../../test/setup'
import '../../../../test/i18n'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { SettingsProvider } from '../../../../contexts/SettingsContext'
import type { SettingsConfigState } from '../../../../hooks/useSettingsConfig'
import i18n from '../../../../i18n'
import type { EditableConfig } from '../../../../lib/types'
import type { ApiClient } from '../../../../services/http/client'
import { NativeChannelSettings } from './NativeChannelSettings'

const tr = (key: string) => i18n.t(key) as string

const baseNative = {
  enabled: true,
  host: '127.0.0.1',
  port: 18793,
  token_expiry_days: 30,
  pin_expiry_minutes: 10,
  max_clients: 5,
  cors_origins: [],
  session_expiry_days: 365,
  max_upload_size_mb: 50,
  upload_ttl_hours: 24,
  rate_limit: {
    enabled: false,
    pin_per_minute: 10,
    pair_per_minute: 5,
    refresh_per_minute: 20,
    api_per_minute: 120,
    ws_messages_per_minute: 120,
  },
}

const fakeApi = {
  models: async () => ({ models: [], model_groups: [] }),
  skills: async () => ({ skills: [] }),
  tools: async () => ({ tools: [] }),
  agentFiles: async () => ({ files: [] }),
} as unknown as ApiClient

type Write = [path: string, value: unknown]

function renderWithSettings(
  nativeOverride?: Partial<typeof baseNative>,
  options?: { dirtyPaths?: string[]; validationErrors?: SettingsConfigState['validationErrors'] },
) {
  const writes: Write[] = []
  const native = { ...baseNative, ...nativeOverride }
  const draft = {
    channels: { native },
  } as unknown as EditableConfig

  const state: SettingsConfigState = {
    remoteConfig: draft,
    draftConfig: draft,
    metadata: null,
    dirtyPaths: new Set(options?.dirtyPaths ?? []),
    validationErrors: options?.validationErrors ?? [],
    saveState: 'idle',
    saveError: null,
    updateField: (path: string, value: unknown) => writes.push([path, value]),
    updateSecretField: () => undefined,
    replaceDraft: () => undefined,
    reset: () => undefined,
    validate: async () => true,
    save: async () => true,
    isDirty: false,
    isLoading: false,
    isReloading: false,
    hasErrors: false,
  }

  const utils = render(
    <SettingsProvider settingsState={state} api={fakeApi}>
      <MemoryRouter>
        <NativeChannelSettings />
      </MemoryRouter>
    </SettingsProvider>,
  )

  return {
    ...utils,
    writes,
    lastWrite: (path: string) => [...writes].reverse().find((w) => w[0] === path),
  }
}

afterEach(cleanup)

describe('NativeChannelSettings — rate_limit block', () => {
  test('checkbox visible, 5 numbers hidden when rate_limit.enabled=false', () => {
    const { container, queryByLabelText } = renderWithSettings()

    // The rate limit checkbox is visible (found via the label text)
    const checkbox = container.querySelector('#channels\\.native\\.rate_limit\\.enabled')
    expect(checkbox).toBeTruthy()
    expect((checkbox as HTMLInputElement).checked).toBe(false)

    // The 5 rate limit number inputs are NOT in the DOM
    expect(queryByLabelText(tr('settings.fields.nativeRateLimitPin'))).toBeNull()
    expect(queryByLabelText(tr('settings.fields.nativeRateLimitPair'))).toBeNull()
    expect(queryByLabelText(tr('settings.fields.nativeRateLimitRefresh'))).toBeNull()
    expect(queryByLabelText(tr('settings.fields.nativeRateLimitApi'))).toBeNull()
    expect(queryByLabelText(tr('settings.fields.nativeRateLimitWs'))).toBeNull()
  })

  test('with rate_limit.enabled=true, all 5 number inputs appear with correct values', () => {
    const { container } = renderWithSettings({
      rate_limit: {
        enabled: true,
        pin_per_minute: 10,
        pair_per_minute: 5,
        refresh_per_minute: 20,
        api_per_minute: 120,
        ws_messages_per_minute: 120,
      },
    })

    // The rate limit checkbox should be checked
    const checkbox = container.querySelector(
      '#channels\\.native\\.rate_limit\\.enabled',
    ) as HTMLInputElement
    expect(checkbox).toBeTruthy()
    expect(checkbox.checked).toBe(true)

    // The 5 number inputs should be visible with their values
    const pin = container.querySelector(
      '#channels\\.native\\.rate_limit\\.pin_per_minute',
    ) as HTMLInputElement
    expect(pin).toBeTruthy()
    expect(pin.value).toBe('10')

    const pair = container.querySelector(
      '#channels\\.native\\.rate_limit\\.pair_per_minute',
    ) as HTMLInputElement
    expect(pair).toBeTruthy()
    expect(pair.value).toBe('5')

    const refresh = container.querySelector(
      '#channels\\.native\\.rate_limit\\.refresh_per_minute',
    ) as HTMLInputElement
    expect(refresh).toBeTruthy()
    expect(refresh.value).toBe('20')

    const api = container.querySelector(
      '#channels\\.native\\.rate_limit\\.api_per_minute',
    ) as HTMLInputElement
    expect(api).toBeTruthy()
    expect(api.value).toBe('120')

    const ws = container.querySelector(
      '#channels\\.native\\.rate_limit\\.ws_messages_per_minute',
    ) as HTMLInputElement
    expect(ws).toBeTruthy()
    expect(ws.value).toBe('120')
  })

  test('changing a rate limit field calls updateField with the correct path', () => {
    const { container, writes } = renderWithSettings({
      rate_limit: {
        enabled: true,
        pin_per_minute: 10,
        pair_per_minute: 5,
        refresh_per_minute: 20,
        api_per_minute: 120,
        ws_messages_per_minute: 120,
      },
    })

    // Change the pin_per_minute field
    const pin = container.querySelector(
      '#channels\\.native\\.rate_limit\\.pin_per_minute',
    ) as HTMLInputElement
    fireEvent.change(pin, { target: { value: '3' } })

    // NumberInput onChange returns a number, not a string
    expect(writes).toContainEqual(['channels.native.rate_limit.pin_per_minute', 3])

    // Change the api_per_minute field
    const api = container.querySelector(
      '#channels\\.native\\.rate_limit\\.api_per_minute',
    ) as HTMLInputElement
    fireEvent.change(api, { target: { value: '200' } })

    expect(writes).toContainEqual(['channels.native.rate_limit.api_per_minute', 200])
  })

  test('clicking the checkbox calls updateField with the correct path', () => {
    const { container, writes } = renderWithSettings()

    const checkbox = container.querySelector('#channels\\.native\\.rate_limit\\.enabled')
    if (!checkbox) throw new Error('checkbox not found')
    fireEvent.click(checkbox)

    expect(writes).toContainEqual(['channels.native.rate_limit.enabled', true])
  })

  test('validation error appears under the correct field', () => {
    const { getByText } = renderWithSettings(
      {
        rate_limit: {
          enabled: true,
          pin_per_minute: -1,
          pair_per_minute: 5,
          refresh_per_minute: 20,
          api_per_minute: 120,
          ws_messages_per_minute: 120,
        },
      },
      {
        validationErrors: [
          {
            path: 'channels.native.rate_limit.pin_per_minute',
            message: 'rate limit must be 0 or greater',
            code: 'invalid_range',
          },
        ],
      },
    )

    expect(getByText('rate limit must be 0 or greater')).toBeTruthy()
  })
})
