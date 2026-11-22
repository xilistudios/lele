/**
 * Single source of truth for the Settings tab ids.
 *
 * Shared by the tab strip (`molecules/SettingsTabs`), the route layout
 * (`pages/SettingsPage`) and the tab content switch (`pages/SettingsTabContent`)
 * so a tab cannot exist in one place and be unknown in the others.
 */

export const SETTINGS_TABS = [
  'general',
  'agents',
  'providers',
  'session',
  'channels',
  'native',
  'groups',
  'tools',
  'system',
  'updates',
  'advanced',
  'diagnostics',
  'logs',
] as const

export type SettingsTab = (typeof SETTINGS_TABS)[number]

/** Type guard for the `:tab` URL segment. */
export function isSettingsTab(value: string | undefined): value is SettingsTab {
  return !!value && (SETTINGS_TABS as readonly string[]).includes(value)
}
