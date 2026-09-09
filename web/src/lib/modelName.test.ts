import { describe, expect, test } from 'bun:test'
import { shortModelName } from './modelName'

describe('shortModelName', () => {
  test('drops the routing provider prefix (last slash segment wins)', () => {
    expect(shortModelName('openrouter/anthropic/claude-sonnet-4')).toBe('claude-sonnet-4')
    expect(shortModelName('anthropic/claude-sonnet-4')).toBe('claude-sonnet-4')
  })

  test('drops a known dotted provider prefix', () => {
    expect(shortModelName('openai.gpt-4o-mini')).toBe('gpt-4o-mini')
    expect(shortModelName('Google.Gemini-2.0-Flash')).toBe('Gemini-2.0-Flash')
  })

  test('keeps dotted names whose prefix is not a known provider', () => {
    expect(shortModelName('custom.my.model')).toBe('custom.my.model')
  })

  test('leaves bare names and empty values untouched', () => {
    expect(shortModelName('gpt-4o')).toBe('gpt-4o')
    expect(shortModelName('llama3.1:70b')).toBe('llama3.1:70b')
    expect(shortModelName('')).toBe('')
  })
})
