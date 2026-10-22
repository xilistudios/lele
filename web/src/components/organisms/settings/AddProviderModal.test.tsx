import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { SettingsConfigState } from '../../../hooks/useSettingsConfig'
import type { ApiClient } from '../../../services/http/client'
import { AddProviderModal, DEFAULT_API_BASE, PROVIDER_TYPES } from './AddProviderModal'

function makeSettingsState(): SettingsConfigState {
  return {
    remoteConfig: null,
    draftConfig: null,
    metadata: null,
    dirtyPaths: new Set(),
    validationErrors: [],
    saveState: 'idle',
    saveError: null,
    updateField: () => {},
    updateSecretField: () => {},
    replaceDraft: () => {},
    reset: () => {},
    validate: async () => true,
    save: async () => true,
    isDirty: false,
    isLoading: false,
    isReloading: false,
    hasErrors: false,
  }
}

function renderModal() {
  const api = { models: async () => ({ models: [] }) } as unknown as ApiClient
  return render(
    <SettingsProvider settingsState={makeSettingsState()} api={api}>
      <AddProviderModal isOpen onClose={() => {}} />
    </SettingsProvider>,
  )
}

describe('AddProviderModal OpenCode entries', () => {
  test('offers OpenCode Zen and OpenCode Go in PROVIDER_TYPES', () => {
    const values = PROVIDER_TYPES.map((pt) => pt.value)
    expect(values).toContain('opencode')
    expect(values).toContain('opencode_go')
  })

  test('pre-fills the OpenCode api bases', () => {
    expect(DEFAULT_API_BASE.opencode).toBe('https://opencode.ai/zen/v1')
    expect(DEFAULT_API_BASE.opencode_go).toBe('https://opencode.ai/zen/go/v1')
  })

  test('renders both options without duplicates', () => {
    const utils = renderModal()

    const select = utils.container.querySelector('select') as HTMLSelectElement
    const options = Array.from(select.querySelectorAll('option')).map((o) => o.value)
    expect(options).toContain('opencode')
    expect(options).toContain('opencode_go')
    expect(new Set(options).size).toBe(options.length)
  })

  // Walk the wizard to step 3 for each new entry and check the api_base
  // pre-fill. Buttons live in the footer div; the last one is Next
  // ("Siguiente" under the test i18n locale) — locate it structurally to
  // stay locale-proof. Step 2 requires a provider name before Next enables.
  const newEntries = [
    { value: 'opencode', base: 'https://opencode.ai/zen/v1' },
    { value: 'opencode_go', base: 'https://opencode.ai/zen/go/v1' },
  ]

  for (const { value, base } of newEntries) {
    test(`walks select → name → Next and pre-fills step 3 for ${value}`, () => {
      const utils = renderModal()

      const select = utils.container.querySelector('select') as HTMLSelectElement
      const nextButton = () =>
        utils.container.querySelector('div.mt-6 button:last-child') as HTMLButtonElement

      fireEvent.change(select, { target: { value } })
      fireEvent.click(nextButton())

      const nameInput = utils.container.querySelector(
        'input[id="new-provider-name"]',
      ) as HTMLInputElement
      fireEvent.change(nameInput, { target: { value: `my-${value}` } })
      fireEvent.click(nextButton())

      const apiBase = utils.container.querySelector(
        'input[id="new-provider-api-base"]',
      ) as HTMLInputElement
      expect(apiBase.value).toBe(base)
    })
  }
})
