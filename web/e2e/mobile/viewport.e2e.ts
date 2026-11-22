import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import type { Page, TestInfo } from '@playwright/test'

const SESSION_KEY = 'native:client-e2e:1'
const API_URL = 'http://localhost:18793'
const AGENT_ID = 'main'
const SECOND_AGENT_ID = 'ui-engineer'

const LONG_MODEL = 'deepseek-v4.1-flash'
const LONG_AGENT_NAME = 'UI Engineer'
const SESSION_FOLDER = '/home/alfredo/projects/lele-mobile-viewport-height-regression'

const SCREENSHOT_DIR = join(process.cwd(), 'test-results', 'e2e-mobile', 'screenshots')

const AGENT = {
  id: AGENT_ID,
  name: 'Main Agent',
  workspace: '~/.lele',
  model: LONG_MODEL,
  default: true,
}

const SECOND_AGENT = {
  id: SECOND_AGENT_ID,
  name: LONG_AGENT_NAME,
  workspace: '~/.lele',
  model: LONG_MODEL,
}

const MODEL_GROUPS = [
  {
    provider: 'deepseek',
    models: [
      { value: LONG_MODEL, label: LONG_MODEL, reasoning: { enable: true } },
      { value: 'deepseek-v4.1', label: 'deepseek-v4.1' },
    ],
  },
]

const SESSION = {
  key: SESSION_KEY,
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:01:00Z',
}

const HISTORY = {
  session_key: SESSION_KEY,
  has_more: false,
  messages: [
    { id: 'm1', role: 'user', content: 'How tall is the mobile viewport?' },
    {
      id: 'm2',
      role: 'assistant',
      content:
        'The shell now sizes itself to the dynamic viewport height, so the header stays pinned at the top and the composer sits flush above the bottom edge.',
    },
    { id: 'm3', role: 'user', content: 'And the message list keeps the remaining space?' },
    {
      id: 'm4',
      role: 'assistant',
      content: 'Yes: header and composer are fixed-size rows and the list takes whatever is left.',
    },
  ],
}

const CONFIG = {
  config: {
    agents: {
      list: [AGENT, SECOND_AGENT],
      defaults: {
        workspace: '~/.lele',
        restrict_to_workspace: false,
        provider: 'openai',
        model: LONG_MODEL,
        max_tokens: 4096,
        max_tool_iterations: 30,
      },
    },
    session: { ephemeral: false, ephemeral_threshold: 3600 },
    channels: {},
  },
}

const normalizePath = (url: string): string => {
  const withoutQuery = url.split('?')[0] ?? url
  const marker = '/api/v1/'
  const index = withoutQuery.indexOf(marker)
  const path = index >= 0 ? withoutQuery.slice(index) : withoutQuery
  return decodeURIComponent(path)
}

type MockOptions = { multipleAgents: boolean; reasoning?: boolean; folder?: string }

const respond = (url: string, method: string, options: MockOptions): unknown => {
  const path = normalizePath(url)
  const agents = options.multipleAgents ? [AGENT, SECOND_AGENT] : [AGENT]
  const modelGroups = options.reasoning ? MODEL_GROUPS : undefined
  const models = options.reasoning
    ? MODEL_GROUPS.flatMap((group) => group.models.map((model) => model.value))
    : [LONG_MODEL, 'deepseek-v4.1']

  if (path.endsWith('/api/v1/auth/status')) {
    return {
      valid: true,
      client_id: 'client-e2e',
      device_name: 'Pixel',
      expires: '2030-01-01T00:00:00Z',
    }
  }
  if (path.endsWith('/api/v1/agents')) {
    return { agents }
  }
  if (path.endsWith(`/api/v1/agents/${AGENT_ID}/status`)) {
    return { id: AGENT_ID, status: 'running', active_sessions: 1 }
  }
  if (path.endsWith(`/api/v1/agents/${AGENT_ID}`)) {
    return AGENT
  }
  if (path.endsWith(`/api/v1/agents/${SECOND_AGENT_ID}`)) {
    return SECOND_AGENT
  }
  if (path.endsWith('/api/v1/status')) {
    return { status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }
  }
  if (path.endsWith('/api/v1/channels')) {
    return { channels: [{ name: 'native', enabled: true, running: true }] }
  }
  if (path.endsWith('/api/v1/tools')) {
    return { tools: [] }
  }
  if (path.endsWith('/api/v1/config')) {
    return CONFIG
  }
  if (path.endsWith('/api/v1/models')) {
    return { agent_id: AGENT_ID, model: LONG_MODEL, models, model_groups: modelGroups }
  }
  if (path.endsWith('/api/v1/chat/commands')) {
    return { commands: [] }
  }
  if (path.endsWith('/api/v1/chat/sessions/meta')) {
    return { sessions: [SESSION], has_more: false }
  }
  if (path.endsWith('/api/v1/chat/sessions')) {
    return { sessions: [SESSION], has_more: false }
  }

  const sessionBase = `/api/v1/chat/sessions/${SESSION_KEY}`
  if (path === `${sessionBase}/history`) {
    return HISTORY
  }
  if (path === `${sessionBase}/subagents`) {
    return { session_key: SESSION_KEY, subagents: [] }
  }
  if (path === `${sessionBase}/agent`) {
    return { session_key: SESSION_KEY, agent_id: AGENT_ID }
  }
  if (path === `${sessionBase}/thinking`) {
    return { session_key: SESSION_KEY, level: 'default' }
  }
  if (path === `${sessionBase}/folder`) {
    return { session_key: SESSION_KEY, folder: options.folder ?? '' }
  }
  if (path === `${sessionBase}/model`) {
    return { session_key: SESSION_KEY, model: LONG_MODEL, models, model_groups: modelGroups }
  }
  if (path === `${sessionBase}/context`) {
    return {
      session_key: SESSION_KEY,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      cumulative_input_tokens: 0,
      cumulative_output_tokens: 0,
      cumulative_total_tokens: 0,
      context_window: 128000,
    }
  }
  if (path === sessionBase) {
    return { session_key: SESSION_KEY, model: LONG_MODEL, models, model_groups: modelGroups }
  }

  if (method === 'POST' && path.endsWith('/api/v1/chat/send')) {
    return { ok: true }
  }

  return {}
}

const STUB_WS_INIT = ({ key, apiUrl }: { key: string; apiUrl: string }) => {
  localStorage.setItem(
    'lele.session',
    JSON.stringify({
      token: 'e2e-token',
      refresh_token: 'e2e-refresh',
      expires: '2030-01-01T00:00:00Z',
      client_id: 'client-e2e',
      device_name: 'Pixel',
    }),
  )
  localStorage.setItem('lele.apiUrl', apiUrl)
  localStorage.setItem('lele.currentSessionKey', key)

  type Listener = (event: unknown) => void

  class StubWebSocket {
    static readonly CONNECTING = 0
    static readonly OPEN = 1
    static readonly CLOSING = 2
    static readonly CLOSED = 3
    static readonly sent: string[] = []
    readyState = StubWebSocket.CONNECTING
    private readonly listeners: Record<string, Listener[]> = {}

    constructor() {
      setTimeout(() => {
        this.readyState = StubWebSocket.OPEN
        this.emit('open', {})
      }, 0)
    }

    addEventListener(type: string, listener: Listener) {
      this.listeners[type] = [...(this.listeners[type] ?? []), listener]
    }

    removeEventListener(type: string, listener: Listener) {
      this.listeners[type] = (this.listeners[type] ?? []).filter((item) => item !== listener)
    }

    send(payload: string) {
      StubWebSocket.sent.push(payload)
    }

    close() {
      this.readyState = StubWebSocket.CLOSED
    }

    private emit(type: string, event: unknown) {
      for (const listener of this.listeners[type] ?? []) listener(event)
    }
  }

  Object.defineProperty(window, 'WebSocket', { value: StubWebSocket, configurable: true })
  ;(window as unknown as { __stubSocket: typeof StubWebSocket }).__stubSocket = StubWebSocket
}

const stubSocketSent = (page: Page): Promise<unknown[]> =>
  page.evaluate(() => {
    const stub = (window as unknown as { __stubSocket?: { sent: string[] } }).__stubSocket
    return (stub?.sent ?? []).map((raw) => JSON.parse(raw))
  })

const openChat = async (page: Page, options: MockOptions = { multipleAgents: false }) => {
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    await route.fulfill({ json: respond(request.url(), request.method(), options) })
  })
  await page.goto(`/chat/${encodeURIComponent(SESSION_KEY)}`)
  await expect(page.getByTestId('message-list')).toBeVisible({ timeout: 30_000 })
  await expect(page.locator('main form button[aria-label="Model"]')).not.toContainText('Default', {
    timeout: 15_000,
  })
}

const measure = async (page: Page) =>
  page.evaluate(() => {
    const rect = (el: Element | null) => {
      if (!el) return null
      const r = el.getBoundingClientRect()
      return {
        top: r.top,
        bottom: r.bottom,
        left: r.left,
        right: r.right,
        width: r.width,
        height: r.height,
      }
    }
    return {
      appVh: getComputedStyle(document.documentElement).getPropertyValue('--app-vh').trim(),
      innerHeight: window.innerHeight,
      innerWidth: window.innerWidth,
      shell: rect(document.querySelector('.app-shell')),
      header: rect(document.querySelector('main > div.border-b')),
      composer: rect(document.querySelector('main form')),
      list: rect(document.querySelector('[data-testid="message-list"]')),
      scrollHeight: document.documentElement.scrollHeight,
    }
  })

const waitForPaintedMessages = async (page: Page) => {
  await page.getByTestId('message').first().waitFor({ state: 'visible', timeout: 15_000 })
  await page.evaluate(
    () =>
      new Promise<void>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
      }),
  )
}

const attach = async (page: Page, testInfo: TestInfo, name: string) => {
  await waitForPaintedMessages(page)
  const body = await page.screenshot()
  await testInfo.attach(name, { body, contentType: 'image/png' })
  await mkdir(SCREENSHOT_DIR, { recursive: true })
  await writeFile(join(SCREENSHOT_DIR, `${name}.png`), body)
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(STUB_WS_INIT, { key: SESSION_KEY, apiUrl: API_URL })
})

test('chat shell fits the mobile viewport', async ({ page }, testInfo) => {
  await openChat(page)
  const m = await measure(page)
  await attach(page, testInfo, `chat-${testInfo.project.name}`)

  expect(m.appVh).toContain('dvh')
  expect(Math.abs((m.shell?.height ?? -1) - m.innerHeight)).toBeLessThanOrEqual(1)
  expect(m.header?.top ?? -1).toBeGreaterThanOrEqual(0)
  expect(m.header?.bottom ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(m.innerHeight)
  expect(m.composer?.bottom ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(m.innerHeight + 1)
  expect(m.scrollHeight).toBeLessThanOrEqual(m.innerHeight + 1)
  expect(m.list?.height ?? -1).toBeGreaterThan(50)
  expect(m.list?.height ?? -1).toBeGreaterThanOrEqual(0.45 * m.innerHeight)
})

test('a long draft never pushes the composer off screen', async ({ page }, testInfo) => {
  await openChat(page)

  const draft = Array.from({ length: 12 }, (_, i) => `line ${i + 1} of a long mobile draft`).join(
    '\n',
  )
  await page.locator('main form textarea').fill(draft)

  const textareaHeight = await page
    .locator('main form textarea')
    .evaluate((el) => el.getBoundingClientRect().height)
  const m = await measure(page)
  await attach(page, testInfo, `long-draft-${testInfo.project.name}`)

  expect(textareaHeight).toBeLessThanOrEqual(0.35 * m.innerHeight)
  expect(m.composer?.bottom ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(m.innerHeight + 1)
  expect(m.composer?.top ?? -1).toBeGreaterThanOrEqual(0)
  expect(m.list?.height ?? -1).toBeGreaterThanOrEqual(0.3 * m.innerHeight)
})

const SELECTOR_LABELS = ['Model', 'Thinking', 'Agent'] as const

const TRIGGER_CHROME_PX = 8 + 8 + 6 + 12
const FOLDER_CHIP_MAX_WIDTH = 200
const THINKING_FLOOR_WIDTH = 68
const AGENT_FLOOR_WIDTH = 88

type Box = {
  top: number
  bottom: number
  left: number
  right: number
  width: number
  height: number
}

type ControlMeasurement = {
  label: string
  box: Box
  trigger: Box | null
  text: string | null
  textWidth: number
  spanWidth: number
  neededWidth: number
  truncated: boolean
  clipped: boolean
}

type ChipMeasurement = {
  box: Box
  label: { text: string | null; box: Box | null; truncated: boolean }
}

type ComposerMeasurement = {
  viewport: number
  innerHeight: number
  capsule: Box
  capsuleWraps: boolean
  sendOverlapsTextarea: boolean
  chips: Box | null
  chipsWraps: boolean
  controls: ControlMeasurement[]
  chip: ChipMeasurement | null
  overlaps: string[]
  send: Box | null
  scrollWidth: number
  list: { height: number } | null
}

const measureComposer = async (page: Page): Promise<ComposerMeasurement | null> =>
  page.evaluate(
    ({ chrome }: { chrome: number }) => {
      const box = (el: Element): Box => {
        const r = el.getBoundingClientRect()
        return {
          top: r.top,
          bottom: r.bottom,
          left: r.left,
          right: r.right,
          width: r.width,
          height: r.height,
        }
      }
      const classOf = (el: Element) => el.getAttribute('class') ?? ''
      const textOf = (el: Element) => {
        const value = el.querySelector('span.font-medium') as HTMLElement | null
        if (!value) return { text: null, textWidth: 0, spanWidth: 0 }
        const range = document.createRange()
        range.selectNodeContents(value)
        return {
          text: value.textContent,
          textWidth: range.getBoundingClientRect().width,
          spanWidth: value.getBoundingClientRect().width,
        }
      }

      const capsule = document.querySelector('[data-testid="composer-capsule"]')
      const chips = document.querySelector('[data-testid="composer-chips"]')
      const textarea = document.querySelector('main form textarea')
      const send = document.querySelector('main form button[aria-label="Send"]')
      if (!capsule || !chips || !textarea || !send) return null

      const describe = (el: Element): ControlMeasurement => {
        const trigger = (el.tagName === 'BUTTON' ? el : el.querySelector('button')) as HTMLElement
        const itemBox = box(el)
        const triggerBox = trigger ? box(trigger) : null
        const { text, textWidth, spanWidth } = textOf(trigger ?? el)
        return {
          label: trigger?.getAttribute('aria-label') ?? el.tagName,
          box: itemBox,
          trigger: triggerBox,
          text,
          textWidth,
          spanWidth,
          neededWidth: textWidth + chrome,
          truncated: triggerBox ? textWidth + chrome > triggerBox.width + 0.5 : false,
          clipped: triggerBox ? triggerBox.right > itemBox.right + 1 : false,
        }
      }

      const chipEl =
        (chips.querySelector('[data-testid="folder-chip"]') as HTMLElement | null) ?? null
      const chipLabel = chipEl?.querySelector('span.truncate') as HTMLElement | null
      const chip = chipEl
        ? {
            box: box(chipEl),
            label: {
              text: chipLabel?.textContent ?? null,
              box: chipLabel ? box(chipLabel) : null,
              truncated: chipLabel ? chipLabel.scrollWidth > chipLabel.clientWidth + 1 : false,
            },
          }
        : null

      const controls = Array.from(chips.children)
        .filter((el) => el.querySelector('button') && el !== chipEl)
        .map(describe)

      const boxes = [
        ...controls.map((control) => control.box),
        ...(chip ? [chip.box] : []),
      ]
      const labels = [...controls.map((control) => control.label), 'Folder chip']
      const overlaps: string[] = []
      for (let i = 0; i < boxes.length; i += 1) {
        for (let j = i + 1; j < boxes.length; j += 1) {
          const a = boxes[i]
          const b = boxes[j]
          const across = Math.min(a.right, b.right) - Math.max(a.left, b.left)
          const down = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top)
          if (across > 0.5 && down > 0.5) overlaps.push(`${labels[i]}×${labels[j]}`)
        }
      }

      const list = document.querySelector('[data-testid="message-list"]')
      const textareaBox = box(textarea)
      const sendBox = box(send)

      return {
        viewport: window.innerWidth,
        innerHeight: window.innerHeight,
        capsule: box(capsule),
        capsuleWraps: classOf(capsule).includes('flex-wrap'),
        sendOverlapsTextarea:
          Math.min(sendBox.right, textareaBox.right) - Math.max(sendBox.left, textareaBox.left) > 1,
        chips: box(chips),
        chipsWraps: classOf(chips).includes('flex-wrap'),
        controls,
        chip,
        overlaps,
        send: sendBox,
        scrollWidth: document.documentElement.scrollWidth,
        list: list ? { height: box(list).height } : null,
      }
    },
    { chrome: TRIGGER_CHROME_PX },
  )

const describeControl = (control: ControlMeasurement): string => {
  const width = control.trigger?.width ?? control.box.width
  const parts = [`w=${Math.round(width)}`]
  if (control.text !== null) {
    parts.push(
      `text=${JSON.stringify(control.text)}`,
      `span=${Math.round(control.spanWidth)}`,
      `needed=${Math.round(control.neededWidth)}`,
      `truncated=${control.truncated}`,
      `clipped=${control.clipped}`,
    )
  }
  return `${control.label}{${parts.join(' ')}}`
}

const controlByLabel = (strip: ComposerMeasurement, label: string): ControlMeasurement => {
  const control = strip.controls.find((item) => item.label === label)
  expect(control).toBeDefined()
  return control as ControlMeasurement
}

const expectSelectorsReadable = (strip: ComposerMeasurement) => {
  for (const [label, floor, spanFloor] of [
    ['Thinking', THINKING_FLOOR_WIDTH, 34],
    ['Agent', AGENT_FLOOR_WIDTH, 53.5],
  ] as const) {
    const control = controlByLabel(strip, label)
    expect(control.trigger?.width ?? 0).toBeGreaterThanOrEqual(floor)
    expect(control.truncated).toBe(false)
    expect(control.spanWidth).toBeGreaterThanOrEqual(spanFloor)
  }
}

const logComposer = (name: string, strip: ComposerMeasurement) =>
  console.log(
    `[${name}] viewport=${strip.viewport} ` +
      `capsule=${Math.round(strip.capsule.width)}x${Math.round(strip.capsule.height)} ` +
      `chips=${strip.chips ? `${Math.round(strip.chips.width)}x${Math.round(strip.chips.height)}` : 'none'} ` +
      `controls=${strip.controls.map(describeControl).join(' ')} ` +
      `chip=${Math.round(strip.chip?.box.width ?? -1)}px label=${JSON.stringify(strip.chip?.label.text)} ` +
      `labelTruncated=${strip.chip?.label.truncated} ` +
      `send=${strip.send ? `${Math.round(strip.send.width)}x${Math.round(strip.send.height)}` : 'none'} ` +
      `overlaps=${strip.overlaps.join(',') || 'none'} ` +
      `list=${Math.round(strip.list?.height ?? -1)}px innerHeight=${strip.innerHeight} ` +
      `scrollWidth=${strip.scrollWidth}`,
  )

test('the input capsule keeps the textarea and the send action on one row', async ({
  page,
}, testInfo) => {
  await openChat(page)

  const strip = await measureComposer(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `capsule-${testInfo.project.name}`)
  logComposer(`capsule ${testInfo.project.name}`, strip)

  const send = strip.send
  expect(send).not.toBeNull()
  if (!send) return

  expect(strip.capsuleWraps).toBe(false)
  expect(send.height).toBeLessThanOrEqual(strip.capsule.height - 8)
  expect(strip.capsule.height).toBeLessThanOrEqual(50)
  expect(send.width).toBeGreaterThanOrEqual(36)
  expect(send.height).toBeGreaterThanOrEqual(36)
  expect(send.left).toBeGreaterThanOrEqual(strip.capsule.left - 1)
  expect(send.right).toBeLessThanOrEqual(strip.capsule.right + 1)
  expect(send.bottom).toBeLessThanOrEqual(strip.capsule.bottom + 1)
  expect(strip.sendOverlapsTextarea).toBe(false)
  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
})

test('the selector chips stay readable pills below the capsule', async ({ page }, testInfo) => {
  await openChat(page, { multipleAgents: true, reasoning: true })

  for (const label of SELECTOR_LABELS) {
    await expect(page.locator(`main form button[aria-label="${label}"]`)).toBeVisible({
      timeout: 15_000,
    })
  }

  const strip = await measureComposer(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `chips-${testInfo.project.name}`)
  logComposer(`chips ${testInfo.project.name}`, strip)

  const chips = strip.chips
  expect(chips).not.toBeNull()
  if (!chips) return

  expect(strip.chipsWraps).toBe(true)
  expect(chips.top).toBeGreaterThanOrEqual(strip.capsule.bottom)
  expect(strip.controls).toHaveLength(3)
  expect(strip.overlaps).toEqual([])

  for (const control of strip.controls) {
    expect(control.clipped).toBe(false)
    expect(control.box.left).toBeGreaterThanOrEqual(chips.left - 1)
    expect(control.box.right).toBeLessThanOrEqual(chips.right + 1)
    expect(control.box.bottom).toBeLessThanOrEqual(chips.bottom + 1)
  }

  const model = controlByLabel(strip, 'Model')
  expect(model.trigger?.width ?? 0).toBeGreaterThanOrEqual(strip.viewport <= 320 ? 110 : 130)
  expect(model.truncated).toBe(false)
  expectSelectorsReadable(strip)

  expect(chips.height).toBeLessThanOrEqual(92)
  expect(strip.list?.height ?? -1).toBeGreaterThanOrEqual(0.3 * strip.innerHeight)
  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
})

test('a session folder never squeezes the model chip', async ({ page }, testInfo) => {
  await openChat(page, { multipleAgents: true, reasoning: true, folder: SESSION_FOLDER })

  for (const label of SELECTOR_LABELS) {
    await expect(page.locator(`main form button[aria-label="${label}"]`)).toBeVisible({
      timeout: 15_000,
    })
  }
  await expect(page.locator('main form [data-testid="folder-chip"]:visible')).toBeVisible({
    timeout: 15_000,
  })

  const strip = await measureComposer(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `folder-chips-${testInfo.project.name}`)
  logComposer(`folder-chips ${testInfo.project.name}`, strip)

  expect(strip.overlaps).toEqual([])
  const chip = strip.chip
  const send = strip.send
  if (!chip || !send) return

  const model = controlByLabel(strip, 'Model')
  if (strip.viewport <= 360) {
    expect(chip.box.bottom).toBeLessThanOrEqual(model.box.top - 1)
  }
  expect(chip.box.width).toBeLessThanOrEqual(FOLDER_CHIP_MAX_WIDTH + 0.5)
  expect(chip.label.truncated).toBe(true)
  expect(model.trigger?.width ?? 0).toBeGreaterThanOrEqual(110)
  expect(model.clipped).toBe(false)
  expectSelectorsReadable(strip)

  expect(Math.round(send.width)).toBe(36)
  expect(Math.round(send.height)).toBe(36)
  expect(send.right).toBeLessThanOrEqual(strip.capsule.right + 1)

  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
  expect(strip.list?.height ?? -1).toBeGreaterThanOrEqual(0.3 * strip.innerHeight)
})

test('a session folder and a long draft still leave the list room', async ({ page }, testInfo) => {
  await openChat(page, { multipleAgents: true, reasoning: true, folder: SESSION_FOLDER })

  for (const label of SELECTOR_LABELS) {
    await expect(page.locator(`main form button[aria-label="${label}"]`)).toBeVisible({
      timeout: 15_000,
    })
  }

  const draft = Array.from({ length: 12 }, (_, i) => `line ${i + 1} of a long mobile draft`).join(
    '\n',
  )
  await page.locator('main form textarea').fill(draft)

  const textareaHeight = await page
    .locator('main form textarea')
    .evaluate((el) => el.getBoundingClientRect().height)
  const strip = await measureComposer(page)
  const shell = await measure(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `folder-long-draft-${testInfo.project.name}`)
  logComposer(`folder-long-draft ${testInfo.project.name}`, strip)

  console.log(
    `[folder-long-draft ${testInfo.project.name}] composer=${Math.round(shell.composer?.height ?? -1)}px ` +
      `(${(((shell.composer?.height ?? 0) / strip.innerHeight) * 100).toFixed(1)}%) ` +
      `textarea=${Math.round(textareaHeight)}px ` +
      `chipOwnLine=${(strip.chip?.box.bottom ?? 0) <= controlByLabel(strip, 'Model').box.top - 1}`,
  )

  expect(textareaHeight).toBeLessThanOrEqual(0.35 * strip.innerHeight)
  expect(shell.composer?.bottom ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(
    strip.innerHeight + 1,
  )
  expect(shell.composer?.top ?? -1).toBeGreaterThanOrEqual(0)

  expect(strip.overlaps).toEqual([])
  expect(strip.sendOverlapsTextarea).toBe(false)
  expectSelectorsReadable(strip)

  const chip = strip.chip
  expect(chip).not.toBeNull()
  if (!chip) return

  expect(chip.label.truncated).toBe(true)
  expect(chip.box.width).toBeLessThanOrEqual(FOLDER_CHIP_MAX_WIDTH + 0.5)
  expect(controlByLabel(strip, 'Model').trigger?.width ?? 0).toBeGreaterThanOrEqual(110)

  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
  expect(strip.list?.height ?? -1).toBeGreaterThanOrEqual(0.25 * strip.innerHeight)
})
