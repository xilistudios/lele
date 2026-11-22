import '../../test/setup'
import { describe, expect, test } from 'bun:test'
import { render } from '@testing-library/react'
import type { ComponentProps } from 'react'
import type { ScannedSkill } from '../../lib/types'
import i18n from '../../test/i18n'
import { InstallSkillModal } from './InstallSkillModal'

const tr = (key: string, options?: Record<string, unknown>) => i18n.t(key, options) as string

const DESTINATION = tr('skills.installIntoTarget', {
  target: tr('skills.targetDefaultWorkspace'),
})

const SCAN_RESULTS: ScannedSkill[] = [
  { name: 'tmux', description: 'Remote-control tmux', path: 'skills/tmux', has_skill: true },
]

type Props = ComponentProps<typeof InstallSkillModal>

function setup(overrides: Partial<Props> = {}) {
  return render(
    <InstallSkillModal
      isOpen
      onClose={() => {}}
      availableSkills={[]}
      isAvailableLoading={false}
      isInstalling={false}
      isScanning={false}
      scanResults={null}
      onInstall={() => {}}
      onScan={async () => null}
      onInstallBatch={() => {}}
      onClearScan={() => {}}
      {...overrides}
    />,
  )
}

const byTestId = (utils: ReturnType<typeof setup>, id: string) =>
  utils.container.querySelector(`[data-testid="${id}"]`)

describe('InstallSkillModal — destination caption', () => {
  test('renders the fixed destination in the browse/URL view', () => {
    const utils = setup({ destinationLabel: DESTINATION })

    const caption = byTestId(utils, 'install-destination') as HTMLElement
    expect(caption).toBeTruthy()
    expect(caption.textContent).toBe(DESTINATION)
    expect(byTestId(utils, 'install-scope')).toBeNull()
  })

  test('renders the fixed destination in the scan-picker view (second branch)', () => {
    const utils = setup({ destinationLabel: DESTINATION, scanResults: SCAN_RESULTS })

    expect(utils.container.querySelectorAll('input[type="checkbox"]')).toHaveLength(1)
    expect(utils.container.querySelector('#github-repo-input')).toBeNull()

    const caption = byTestId(utils, 'install-destination') as HTMLElement
    expect(caption).toBeTruthy()
    expect(caption.textContent).toBe(DESTINATION)
    expect(byTestId(utils, 'install-scope')).toBeNull()
  })

  test('omitted destinationLabel renders no caption at all', () => {
    const utils = setup()
    expect(byTestId(utils, 'install-destination')).toBeNull()
    expect(byTestId(utils, 'install-scope')).toBeNull()
  })

  test('an empty scanResults list falls back to the browse/URL view', () => {
    const utils = setup({ destinationLabel: DESTINATION, scanResults: [] })
    expect(byTestId(utils, 'install-destination')).toBeTruthy()
    const urlTab = Array.from(utils.container.querySelectorAll('button')).find(
      (button) => button.textContent?.trim() === tr('skills.installFromUrl'),
    )
    expect(urlTab).toBeTruthy()
    expect(utils.container.querySelectorAll('input[type="checkbox"]')).toHaveLength(0)
  })
})

describe('InstallSkillModal — render rule (C3 guard)', () => {
  test('no scope props at all: neither picker nor caption', () => {
    const utils = setup()
    expect(byTestId(utils, 'install-scope')).toBeNull()
    expect(byTestId(utils, 'install-destination')).toBeNull()
  })

  test('scope WITHOUT onScopeChange: the picker is not rendered', () => {
    const utils = setup({ scope: 'workspace', destinationLabel: DESTINATION })

    expect(byTestId(utils, 'install-scope')).toBeNull()
    expect(byTestId(utils, 'install-scope-workspace')).toBeNull()
    expect((byTestId(utils, 'install-destination') as HTMLElement).textContent).toBe(DESTINATION)
  })

  test('scope WITHOUT onScopeChange and no caption: the slot stays empty', () => {
    const utils = setup({ scope: 'workspace' })
    expect(byTestId(utils, 'install-scope')).toBeNull()
    expect(byTestId(utils, 'install-destination')).toBeNull()
  })

  test('scope + onScopeChange: the picker renders and NO caption (By-agent shape)', () => {
    const utils = setup({
      scope: 'workspace',
      onScopeChange: () => {},
      destinationLabel: DESTINATION,
    })

    expect(byTestId(utils, 'install-scope')).toBeTruthy()
    expect(byTestId(utils, 'install-scope-workspace')).toBeTruthy()
    expect(byTestId(utils, 'install-destination')).toBeNull()
  })

  test('the scan-picker branch follows the same rule', () => {
    const utils = setup({
      scope: 'workspace',
      onScopeChange: () => {},
      destinationLabel: DESTINATION,
      scanResults: SCAN_RESULTS,
    })

    expect(byTestId(utils, 'install-scope')).toBeTruthy()
    expect(byTestId(utils, 'install-destination')).toBeNull()
    expect(utils.container.querySelectorAll('input[type="checkbox"]')).toHaveLength(1)
  })
})
