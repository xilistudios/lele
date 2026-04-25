import { describe, expect, test } from 'bun:test'
import {
  getAgentModelPrimary,
  getDefaultModel,
  getDefaultImageModel,
  getErrorForPath,
  isDirtyPath,
} from './useSettingsHelpers'

describe('isDirtyPath', () => {
  test('detecta path exacto como dirty', () => {
    expect(isDirtyPath(new Set(['agents.defaults.model']), 'agents.defaults.model')).toBe(true)
  })

  test('detecta path anidado como dirty por su padre', () => {
    expect(isDirtyPath(new Set(['agents.defaults']), 'agents.defaults.model')).toBe(true)
  })

  test('retorna false para path no dirty', () => {
    expect(isDirtyPath(new Set(['channels.native']), 'agents.defaults')).toBe(false)
  })
})

describe('getErrorForPath', () => {
  test('encuentra error por path exacto', () => {
    const errors = [
      { path: 'agents.defaults.model', message: 'Model is required' },
    ]
    expect(getErrorForPath(errors, 'agents.defaults.model')).toBe('Model is required')
  })

  test('encuentra error por path anidado', () => {
    const errors = [
      { path: 'agents.defaults.model', message: 'Model is required' },
    ]
    expect(getErrorForPath(errors, 'agents.defaults')).toBe('Model is required')
  })

  test('retorna undefined si no hay error', () => {
    const errors = [{ path: 'channels.native', message: 'Error' }]
    expect(getErrorForPath(errors, 'agents.defaults')).toBeUndefined()
  })

  test('retorna undefined si el array está vacío', () => {
    expect(getErrorForPath([], 'agents.defaults')).toBeUndefined()
  })
})

describe('getAgentModelPrimary', () => {
  test('retorna string directamente', () => {
    expect(getAgentModelPrimary('gpt-4')).toBe('gpt-4')
  })

  test('retorna primary de un objeto', () => {
    expect(getAgentModelPrimary({ primary: 'gpt-4', fallbacks: ['gpt-3.5'] })).toBe('gpt-4')
  })

  test('retorna string vacío para objeto sin primary', () => {
    expect(getAgentModelPrimary({ fallbacks: ['gpt-3.5'] })).toBe('')
  })

  test('retorna string vacío para undefined', () => {
    expect(getAgentModelPrimary(undefined)).toBe('')
  })
})

describe('getDefaultModel / getDefaultImageModel', () => {
  test('getDefaultModel retorna el modelo por defecto', () => {
    const config = { agents: { defaults: { model: 'gpt-4' } } }
    expect(getDefaultModel(config)).toBe('gpt-4')
  })

  test('getDefaultModel retorna vacío si no hay defaults', () => {
    expect(getDefaultModel({})).toBe('')
  })

  test('getDefaultImageModel retorna el image_model por defecto', () => {
    const config = { agents: { defaults: { image_model: 'dall-e-3' } } }
    expect(getDefaultImageModel(config)).toBe('dall-e-3')
  })

  test('getDefaultImageModel retorna vacío si no hay image_model', () => {
    expect(getDefaultImageModel({ agents: { defaults: {} } })).toBe('')
  })
})
