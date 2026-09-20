import '../../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent } from '@testing-library/react'
import type { EditableChannelsConfig } from '../../../../lib/types'
import { autoCleanup, renderWithSettings, tr } from '../../../../test/agentsHarness'
import { WebChannelSettings } from './WebChannelSettings'

/**
 * Web UI serving toggle (`channels.web`).
 *
 * The contract worth pinning is the default: the backend prunes values that
 * equal their default on save, so an ABSENT `channels.web` block means enabled
 * and must render as "on" — reading the missing key as `false` would show the
 * opposite of what the gateway does. The other half is that turning it off
 * writes the explicit `false` the pruner needs to keep the key.
 */

const PATH = 'channels.web.enabled'

/** Only the fields the card reads; the rest of the document is irrelevant. */
type ChannelsFixture = {
  native: { enabled: boolean }
  web?: { enabled: boolean }
}

function setup(channels: ChannelsFixture) {
  return renderWithSettings(<WebChannelSettings />, [], {
    extraConfig: { channels: channels as unknown as EditableChannelsConfig },
  })
}

/** The checkbox carries the id; the visible label is the section's "Enabled". */
function checkbox(utils: ReturnType<typeof setup>) {
  return utils.container.querySelector(`input[id="${PATH}"]`) as HTMLInputElement
}

autoCleanup()

describe('WebChannelSettings', () => {
  test('renders the toggle bound to channels.web.enabled', () => {
    const utils = setup({ native: { enabled: true }, web: { enabled: true } })
    expect(utils.getByText(tr('settings.sections.webChannel'))).toBeTruthy()
    expect(utils.getByLabelText(tr('settings.fields.webEnabled'))).toBeTruthy()
    expect(checkbox(utils).checked).toBe(true)
  })

  test('an absent channels.web block renders as enabled (pruned default)', () => {
    const utils = setup({ native: { enabled: true } })
    expect(checkbox(utils).checked).toBe(true)
  })

  test('an explicit channels.web.enabled=false renders as disabled', () => {
    const utils = setup({ native: { enabled: true }, web: { enabled: false } })
    expect(checkbox(utils).checked).toBe(false)
  })

  test('toggling off writes channels.web.enabled=false', () => {
    const utils = setup({ native: { enabled: true } })
    fireEvent.click(checkbox(utils))
    expect(utils.lastWrite(PATH)?.[1]).toBe(false)
  })

  test('toggling back on writes channels.web.enabled=true', () => {
    const utils = setup({ native: { enabled: true }, web: { enabled: false } })
    fireEvent.click(checkbox(utils))
    expect(utils.lastWrite(PATH)?.[1]).toBe(true)
  })

  test('the hint is always shown', () => {
    const utils = setup({ native: { enabled: true } })
    expect(utils.getByText(tr('settings.descriptions.webEnabled'))).toBeTruthy()
  })

  test('warns when the Web UI is served but the native channel is off', () => {
    const utils = setup({ native: { enabled: false } })
    expect(utils.getByText(tr('settings.descriptions.webNeedsNative'))).toBeTruthy()
  })

  test('no native warning when the Web UI is off too', () => {
    const utils = setup({ native: { enabled: false }, web: { enabled: false } })
    expect(utils.queryByText(tr('settings.descriptions.webNeedsNative'))).toBeNull()
  })
})
