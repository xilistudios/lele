import { describe, expect, test } from 'bun:test'
import { SETTINGS_TABS, isSettingsTab } from './settingsTabs'

describe('settings tab registry', () => {
  test('isSettingsTab accepts the two new tabs', () => {
    expect(isSettingsTab('subagents')).toBe(true)
    expect(isSettingsTab('mcp')).toBe(true)
  })

  test('isSettingsTab rejects unknown and undefined values while keeping the existing ids', () => {
    expect(isSettingsTab('nope')).toBe(false)
    expect(isSettingsTab('')).toBe(false)
    expect(isSettingsTab(undefined)).toBe(false)
    expect(isSettingsTab('general')).toBe(true)
    expect(isSettingsTab('logs')).toBe(true)
  })

  test('subagents sits right after agents and mcp right after tools', () => {
    expect(SETTINGS_TABS.indexOf('subagents')).toBe(SETTINGS_TABS.indexOf('agents') + 1)
    expect(SETTINGS_TABS.indexOf('mcp')).toBe(SETTINGS_TABS.indexOf('tools') + 1)
  })
})
