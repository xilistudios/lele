import '../../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import '../../../test/i18n'
import { AuthContext, type AuthContextValue } from '../../../contexts/AuthContext'
import type { SystemVersionInfo, UpdateCheckInfo } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import {
  ACCESSIBLE_ACCENT_CLASS,
  INADEQUATE_ACCENT_CLASS,
  classTokens,
} from '../../../test/accentRecipe'
import { tr } from '../../../test/agentsHarness'
import { UpdatesSettings } from './UpdatesSettings'

afterEach(cleanup)

const VERSION: SystemVersionInfo = {
  version: '1.0.0',
  git_commit: 'abc1234',
  build_time: '2026-01-01T00:00:00Z',
  go_version: 'go1.25.5',
  os: 'linux',
  arch: 'arm64',
  binary: '/usr/local/bin/lele',
  supervisor: 'systemd',
  dev_build: false,
  has_backup: false,
}

const CHECK: UpdateCheckInfo = {
  current: '1.0.0',
  latest: '1.1.0',
  update_available: true,
  changelog: '- fix: something',
  published_at: '2026-02-01T00:00:00Z',
}

const API_URL = 'http://127.0.0.1:18793'

function setup() {
  const api = {
    systemVersion: async () => VERSION,
    updatesCheck: async () => CHECK,
  } as unknown as ApiClient

  const authValue: AuthContextValue = {
    api,
    apiUrl: API_URL,
    session: null,
    setApiUrl: () => {},
    persistSession: () => {},
    handleAuth: async () => {
      throw new Error('unused')
    },
    ensureSession: async () => null,
    isLoading: false,
  }

  return render(
    <AuthContext.Provider value={authValue}>
      <UpdatesSettings />
    </AuthContext.Provider>,
  )
}

const openChangelogToggle = async (container: HTMLElement) => {
  const label = tr('settings.updates.checkForUpdates')
  fireEvent.click(
    Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === label,
    ) as HTMLButtonElement,
  )
  const findToggle = () =>
    Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent === tr('settings.updates.showChangelog'),
    )
  await waitFor(() => expect(findToggle()).toBeTruthy())
  return findToggle() as HTMLButtonElement
}

describe('UpdatesSettings — changelog link recipe', () => {
  test('the changelog link uses accent-text, the AA token on the page background', async () => {
    const { container } = setup()
    const toggle = await openChangelogToggle(container)
    expect(classTokens(toggle)).toContain(ACCESSIBLE_ACCENT_CLASS)
    expect(classTokens(toggle)).not.toContain(INADEQUATE_ACCENT_CLASS)
  })

  test('the changelog body only renders after the link toggles it', async () => {
    const { container } = setup()
    const toggle = await openChangelogToggle(container)
    expect(container.textContent).not.toContain(CHECK.changelog as string)
    fireEvent.click(toggle)
    await waitFor(() => expect(container.textContent).toContain(CHECK.changelog as string))
  })
})
