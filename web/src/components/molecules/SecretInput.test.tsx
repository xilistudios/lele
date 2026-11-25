import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import type { SecretValue } from '../../lib/types'
import i18n from '../../test/i18n'
import { SecretInput } from './SecretInput'

const STORED_LABEL = i18n.t('settings.storedSecret')
const REPLACE_LABEL = i18n.t('settings.replaceSecret')
const KEYRING_LABEL = i18n.t('settings.keyringSecret')

const EMPTY_SECRET: SecretValue = { mode: 'empty', has_env_var: false }
const STORED_LITERAL: SecretValue = {
  mode: 'literal',
  value: undefined,
  has_env_var: false,
  has_value: true,
}

function setup(value: SecretValue = EMPTY_SECRET) {
  const onChange = mock((_value: SecretValue) => {})
  const utils = render(<SecretInput id="secret" value={value} onChange={onChange} />)
  return { ...utils, onChange, input: () => utils.container.querySelector('input') }
}

describe('SecretInput', () => {
  test('TestSecretInput_KeyringMode renders the stored secret name and edits it', () => {
    const { container, onChange, input } = setup({
      mode: 'keyring',
      secret_name: 'openai.api_key',
      has_env_var: false,
    })

    const field = input()
    expect(field).not.toBeNull()
    expect(field?.value).toBe('openai.api_key')

    if (!field) return
    fireEvent.change(field, { target: { value: 'anthropic.api_key' } })

    expect(onChange).toHaveBeenCalledTimes(1)
    const payload = onChange.mock.calls[0][0]
    expect(payload.mode).toBe('keyring')
    expect(payload.secret_name).toBe('anthropic.api_key')
    expect(payload.value).toBeUndefined()

    const keyringButton = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === KEYRING_LABEL,
    )
    expect(keyringButton).not.toBeUndefined()
  })

  test('TestSecretInput_KeyringMode switches from empty to the keyring reference', () => {
    const { container, onChange } = setup()

    const keyringButton = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === KEYRING_LABEL,
    )
    expect(keyringButton).not.toBeUndefined()
    if (!keyringButton) return

    fireEvent.click(keyringButton)

    expect(onChange).toHaveBeenCalledTimes(1)
    const payload = onChange.mock.calls[0][0]
    expect(payload.mode).toBe('keyring')
    expect(payload.secret_name).toBeUndefined()
    expect(payload.value).toBeUndefined()
  })

  test('TestSecretInput_ReplaceAffordance shows the stored marker instead of the value', () => {
    const { container, onChange, input } = setup(STORED_LITERAL)

    expect(input()).toBeNull()
    expect(container.textContent).toContain(STORED_LABEL)

    const replaceButton = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === REPLACE_LABEL,
    )
    expect(replaceButton).not.toBeUndefined()
    if (!replaceButton) return

    fireEvent.click(replaceButton)

    const field = input()
    expect(field).not.toBeNull()
    expect(field?.value).toBe('')
    expect(field?.getAttribute('type')).toBe('password')
    expect(onChange).not.toHaveBeenCalled()

    if (!field) return
    fireEvent.change(field, { target: { value: 'rotated-secret' } })

    const payload = onChange.mock.calls[onChange.mock.calls.length - 1][0]
    expect(payload.mode).toBe('literal')
    expect(payload.value).toBe('rotated-secret')
    expect(payload.has_value).toBe(false)
  })

  test('TestSecretInput_ReplaceAffordance keeps the empty literal as a keep signal', () => {
    const { container, onChange, input } = setup(STORED_LITERAL)

    const replaceButton = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === REPLACE_LABEL,
    )
    if (!replaceButton) throw new Error('replace button missing')

    fireEvent.click(replaceButton)
    const field = input()
    if (!field) throw new Error('literal input missing')
    fireEvent.change(field, { target: { value: 'x' } })
    fireEvent.change(field, { target: { value: '' } })

    const payload = onChange.mock.calls[onChange.mock.calls.length - 1][0]
    expect(payload.mode).toBe('literal')
    expect(payload.value).toBeUndefined()
    expect(payload.has_value).toBe(true)
  })

  test('a literal value without a stored flag renders the editable field', () => {
    const { container, input } = setup({ mode: 'literal', value: 'sk-live', has_env_var: false })

    const field = input()
    expect(field).not.toBeNull()
    expect(field?.value).toBe('sk-live')
    expect(container.textContent).not.toContain(STORED_LABEL)
  })

  test('the empty mode reports no value set', () => {
    const { container } = setup()
    expect(container.textContent).toContain(i18n.t('settings.emptySecretDescription'))
  })
})
