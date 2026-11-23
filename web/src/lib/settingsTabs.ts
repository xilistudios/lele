export const SETTINGS_TABS = [
  'general',
  'agents',
  'subagents',
  'providers',
  'session',
  'channels',
  'native',
  'groups',
  'tools',
  'mcp',
  'system',
  'updates',
  'advanced',
  'diagnostics',
  'logs',
] as const

export type SettingsTab = (typeof SETTINGS_TABS)[number]

export function isSettingsTab(value: string | undefined): value is SettingsTab {
  return !!value && (SETTINGS_TABS as readonly string[]).includes(value)
}
