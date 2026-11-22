import { afterEach } from 'bun:test'
import { act, cleanup, render } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { SettingsProvider } from '../contexts/SettingsContext'
import type { SettingsConfigState } from '../hooks/useSettingsConfig'
import i18n from '../i18n'
import type { EditableAgentConfig, EditableAgentDefaults, EditableConfig } from '../lib/types'
import type { ApiClient } from '../services/http/client'

export function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options) as string
}

let servedModels: string[] = []

export function withModels(models: string[]): void {
  servedModels = models
}

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
  lastWrite: (path: string) => Write | undefined
}

export function makeState(
  agents: EditableAgentConfig[],
  options: {
    defaultsModel?: string
    defaults?: Partial<EditableAgentDefaults>
    remoteList?: EditableAgentConfig[]
    dirtyPaths?: string[]
    validationErrors?: SettingsConfigState['validationErrors']
    writes?: Write[]
    extraConfig?: Partial<EditableConfig>
    isLoading?: boolean
  } = {},
): SettingsConfigState {
  const draft = {
    agents: {
      defaults: { model: options.defaultsModel ?? '', ...options.defaults },
      list: agents,
    },
    ...options.extraConfig,
  } as unknown as EditableConfig

  const remoteConfig = options.remoteList
    ? ({
        ...draft,
        agents: { ...draft.agents, list: options.remoteList },
      } as unknown as EditableConfig)
    : draft

  const state: SettingsConfigState = {
    remoteConfig,
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
    isReloading: false,
    hasErrors: false,
  }
  return state
}

export function renderWithSettings(
  ui: ReactNode,
  agents: EditableAgentConfig[],
  options: Parameters<typeof makeState>[1] = {},
) {
  const writes: Write[] = []
  const state = makeState(agents, { ...options, writes })
  const utils = render(
    <SettingsProvider settingsState={state} api={api}>
      <MemoryRouter initialEntries={['/settings/agents/coder']}>{ui}</MemoryRouter>
    </SettingsProvider>,
  )
  return {
    ...utils,
    writes,
    state,
    lastWrite: (path: string) => [...writes].reverse().find((write) => write[0] === path),
  }
}

export function autoCleanup() {
  afterEach(cleanup)
}

export async function settle(ms = 50): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms))
  })
}
