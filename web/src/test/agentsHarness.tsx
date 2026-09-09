import { afterEach } from 'bun:test'
import { act, cleanup, render } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { SettingsProvider } from '../contexts/SettingsContext'
import type { SettingsConfigState } from '../hooks/useSettingsConfig'
import i18n from '../i18n'
import type { EditableAgentConfig, EditableConfig } from '../lib/types'
import type { ApiClient } from '../services/http/client'

/**
 * Shared harness for the agent config pages/sections (spec §4).
 *
 * Every section reads and writes the settings draft through `SettingsContext`,
 * so they can only be rendered inside a `SettingsProvider`. This builds a
 * `SettingsConfigState` by hand — no network — and records the `updateField`
 * calls, which is exactly what the sections must get right (the paths they
 * write and the values they store).
 *
 * `screen` is unusable in this environment (its global-document binding is
 * captured before the JSDOM setup runs), so queries come from the helpers
 * returned here.
 */

/** The test env boots in Spanish; resolve labels through i18n (house pattern). */
export function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options) as string
}

/**
 * Fake API. `models()` feeds `useAvailableModels`, which `SettingsProvider`
 * turns into `getOptionsForAgent` / `getGroupsForAgent`; tests that need a
 * populated model list call `withModels()` before rendering.
 */
let servedModels: string[] = []

export function withModels(models: string[]): void {
  servedModels = models
}

/**
 * Skills and tools the fake API serves. Set before rendering: the sections
 * fetch them on mount (`useSkills` / `api.system.tools()`).
 */
let servedSkills: unknown[] = []
let servedTools: unknown[] = []
let toolsShouldFail = false

export function withSkills(skills: unknown[]): void {
  servedSkills = skills
}

export function withTools(tools: unknown[], shouldFail = false): void {
  servedTools = tools
  toolsShouldFail = shouldFail
}

const api = {
  // `model_groups` must be non-empty when there are models: SearchableSelect
  // prefers `groups` over `options`, and SettingsProvider maps the API shape
  // ({provider, models}) to ({label, options}).
  models: async () => ({
    models: servedModels,
    model_groups: servedModels.length
      ? [{ provider: 'Test', models: servedModels.map((m) => ({ value: m, label: m })) }]
      : [],
  }),
  skills: async () => ({ skills: servedSkills }),
  tools: async () => {
    if (toolsShouldFail) throw new Error('tools endpoint down')
    return { tools: servedTools }
  },
  agentFiles: async (agentId: string) => {
    if (filesShouldFail) throw new Error('files endpoint down')
    return { files: servedFiles[agentId] ?? [] }
  },
} as unknown as ApiClient

/** Files the fake API serves per agent id (Files tab). */
let servedFiles: Record<string, unknown[]> = {}
let filesShouldFail = false

export function withFiles(agentId: string, files: unknown[], shouldFail = false): void {
  servedFiles = { ...servedFiles, [agentId]: files }
  filesShouldFail = shouldFail
}

export function resetFileStore(): void {
  servedFiles = {}
  filesShouldFail = false
}

export type Write = [path: string, value: unknown]

export type Harness = {
  state: SettingsConfigState
  writes: Write[]
  /** Last `updateField(path, …)` call for `path`, or undefined. */
  lastWrite: (path: string) => Write | undefined
}

export function makeState(
  agents: EditableAgentConfig[],
  options: {
    defaultsModel?: string
    dirtyPaths?: string[]
    validationErrors?: SettingsConfigState['validationErrors']
    writes?: Write[]
    /** Agents the config exposes as defaults (used by the Skills/Tools tabs). */
    extraConfig?: Partial<EditableConfig>
    /** Initial config load still in flight → the page shows skeletons, not a 404 (§5.2). */
    isLoading?: boolean
  } = {},
): SettingsConfigState {
  const draft = {
    agents: {
      defaults: { model: options.defaultsModel ?? '' },
      list: agents,
    },
    ...options.extraConfig,
  } as unknown as EditableConfig

  const state: SettingsConfigState = {
    remoteConfig: draft,
    draftConfig: draft,
    metadata: null,
    dirtyPaths: new Set(options.dirtyPaths ?? []),
    validationErrors: options.validationErrors ?? [],
    saveState: 'idle',
    saveError: null,
    updateField: (path: string, value: unknown) => options.writes?.push([path, value]),
    updateSecretField: () => undefined,
    replaceDraft: () => undefined,
    reset: () => undefined,
    validate: async () => true,
    save: async () => true,
    isDirty: false,
    isLoading: options.isLoading ?? false,
    hasErrors: false,
  }
  return state
}

/** Render `ui` inside SettingsProvider + MemoryRouter. */
export function renderWithSettings(
  ui: ReactNode,
  agents: EditableAgentConfig[],
  options: Parameters<typeof makeState>[1] = {},
) {
  const writes: Write[] = []
  const state = makeState(agents, { ...options, writes })
  const utils = render(
    <SettingsProvider settingsState={state} api={api}>
      <MemoryRouter initialEntries={['/agents/coder']}>{ui}</MemoryRouter>
    </SettingsProvider>,
  )
  return {
    ...utils,
    writes,
    state,
    lastWrite: (path: string) => [...writes].reverse().find((write) => write[0] === path),
  }
}

/** Unmount between tests: `@testing-library/react` does not auto-cleanup under bun. */
export function autoCleanup() {
  afterEach(cleanup)
}

/**
 * Let pending effects settle (model list fetch, dropdown animation frames).
 * `SettingsProvider` loads models asynchronously, so anything that opens a
 * `SearchableSelect` must wait first or the popup renders empty.
 */
export async function settle(ms = 50): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms))
  })
}
