import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
/**
 * Mobile-viewport gate for the WebUI shell-height fix.
 *
 * WHY the `--app-vh` assertion exists: headless Chromium emulation sets
 * `100vh == innerHeight == viewport.height`, so it cannot reproduce the
 * URL-bar chrome delta that caused the reported bug. A pure geometry
 * assertion would therefore pass even at the unfixed base commit. Asserting
 * the *mechanism* (`--app-vh` resolving to a `dvh` value) is what turns this
 * spec red at the base and green after the fix; the geometry assertions then
 * pin the invariants the fix must keep (shell == visible height, header and
 * composer inside the viewport, document not scrollable, message list with a
 * usable share of the height).
 *
 * The app is booted against a total REST mock and a WebSocket stub, so the
 * gate needs no backend: only the Farm dev server.
 */
import { expect, test } from '@playwright/test'
import type { Page, TestInfo } from '@playwright/test'

const SESSION_KEY = 'native:client-e2e:1'
const API_URL = 'http://localhost:18793'
const AGENT_ID = 'main'
const SECOND_AGENT_ID = 'ui-engineer'

/** Longest model id the strip has to survive (the reported session's model). */
const LONG_MODEL = 'deepseek-v4.1-flash'
/** Longest agent name the strip has to survive. */
const LONG_AGENT_NAME = 'UI Engineer'
/**
 * Session folder whose chip the second tier has to hold and truncate.
 *
 * The basename is deliberately longer than the chip's `max-md:max-w-[200px]`
 * can show: with 26 characters the label fits (122 px of the ~151 px available
 * inside the chip) and a truncation assertion would be vacuous. 37 characters
 * overflow it, so the cap and the `truncate` are both exercised.
 */
const SESSION_FOLDER = '/home/alfredo/projects/lele-mobile-viewport-height-regression'

/**
 * Screenshots are attached to the report AND written here, because the list
 * reporter keeps attachments in memory only: the before/after comparison needs
 * real files on disk.
 */
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

/**
 * `model_groups` is what makes the thinking selector appear: ChatComposer only
 * renders it when the SELECTED model carries `reasoning.enable` in the grouped
 * model list (flat `models` strings carry no reasoning metadata). The first
 * model of the first group is the default selection, so the reasoning model
 * must be listed first.
 */
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

/**
 * Path of a request with the session key decoded, so the mock matches whether
 * the client percent-encodes the key or not (the history call sends it raw,
 * the session subresource calls encode it).
 */
const normalizePath = (url: string): string => {
  const withoutQuery = url.split('?')[0] ?? url
  const marker = '/api/v1/'
  const index = withoutQuery.indexOf(marker)
  const path = index >= 0 ? withoutQuery.slice(index) : withoutQuery
  return decodeURIComponent(path)
}

/**
 * Both secondary selectors are opt-in, because each one is a tier of its own on
 * mobile and the two scenarios assert different things:
 *
 * - `multipleAgents` adds a second agent (`agentsOptions.length > 1` is what
 *   renders the agent selector).
 * - `reasoning` serves the GROUPED model list with `reasoning.enable`, which is
 *   the only thing that renders the thinking selector (flat `models` strings
 *   carry no reasoning metadata).
 *
 * The default scenario therefore has ONE selector (model) and must stay on a
 * single line; the three-selector scenario is the one that needs the second
 * tier.
 *
 * `folder` sets the session folder. It is opt-in because a folder adds a chip to
 * the second tier, and the scenarios that measure the primary line without one
 * would otherwise be measuring a different strip.
 */
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

/**
 * WebSocket stub that actually CONNECTS.
 *
 * The app only loads the model list (and therefore only learns which models
 * are reasoning models) after the socket reaches `connected` — the model load
 * is gated on `wsStatus === 'connected'` in useAppLogic. A stub that stays in
 * CONNECTING leaves the composer on its single fallback selector, which is
 * exactly the shape the three-selector test must NOT silently fall back to.
 * The stub therefore fires `open` on the next task and records every command
 * the app sends, so the test can drive server-side events.
 */
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

/**
 * Boots the chat page against the REST mock.
 *
 * The model trigger is the readiness signal: it renders `Default` until the
 * model list arrives, and the model list only loads once the socket is
 * `connected`, so waiting for a real model name covers bootstrap, socket and
 * model load in one assertion.
 */
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

/**
 * Waits until a real message bubble is visible AND painted.
 *
 * Virtuoso renders its first window asynchronously, so a screenshot taken right
 * after `message-list` appears can capture an empty list (that is how the first
 * capture of the 390 px chat came out blank). Waiting for a bubble plus two
 * animation frames guarantees the paint that the screenshot will read.
 */
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

/**
 * Selector used for the three triggers, and why it is this one.
 *
 * `SearchableSelect` renders `<div class="relative max-md:min-w-0 …">
 * <button aria-label="Model|Thinking|Agent">…</button></div>`: the wrapper is
 * the flex item the strip lays out, and the button inside is the trigger a
 * finger has to hit. `main form button[aria-label]` would also match the icon
 * buttons (Add folder / Attach files / Send), so the selectors are pinned to the
 * three translated labels the composer passes as `ariaLabel`.
 */
const SELECTOR_LABELS = ['Model', 'Thinking', 'Agent'] as const

/**
 * Horizontal chrome of a `SearchableSelect` trigger that is NOT text:
 * `px-2` (8+8) + `gap-1.5` (6) + the 12 px chevron. The trigger's own
 * `scrollWidth` cannot report overflow — its only in-flow child is a
 * `flex-1 min-w-0` span, so the button's content box never exceeds its padding
 * box even when the text is squeezed to 0 px. The intrinsic text width comes
 * from a Range over the value span instead, and the text is clipped exactly
 * when `textWidth + chrome > buttonWidth`.
 */
const TRIGGER_CHROME_PX = 8 + 8 + 6 + 12

type Box = {
  top: number
  bottom: number
  left: number
  right: number
  width: number
  height: number
}

type ControlMeasurement = {
  /** `aria-label` of the control's trigger (or of the control itself). */
  label: string
  /** Box of the flex item — what the strip lays out. */
  box: Box
  /** Box of the actual trigger button (different from `box` for a select). */
  trigger: Box | null
  text: string | null
  textWidth: number
  /** Width of the value span itself — what is actually visible of the label. */
  spanWidth: number
  neededWidth: number
  truncated: boolean
  /** True when the trigger sticks out of the flex item that is supposed to hold it. */
  clipped: boolean
}

type ChipMeasurement = {
  box: Box
  label: { text: string | null; box: Box | null; truncated: boolean }
}

type StripMeasurement = {
  viewport: number
  innerHeight: number
  row: Box
  group: Box
  nowrap: boolean
  secondTier: Box | null
  controls: ControlMeasurement[]
  /** Session-folder chip, wherever it is rendered (mobile chip only: it is `md:hidden`). */
  chip: ChipMeasurement | null
  /** Every pair of laid-out boxes that intersect — the strip may never do that. */
  overlaps: string[]
  send: Box | null
  scrollWidth: number
  list: { height: number } | null
}

/**
 * Measures the control strip.
 *
 * `row` is the flex row that also holds the send button; `group` is the
 * wrapping group inside it (`flex-wrap`) and `secondTier` the `max-md:basis-full
 * md:contents` wrapper that holds the folder chip and the secondary selectors
 * below `md`. The wrapper is flattened away so `controls` is always the list of
 * things a finger can actually see, in DOM order.
 *
 * The folder chip is excluded twice over — the chip itself and the `basis-full`
 * cell that holds it on its own tier line — because neither is a control: the
 * cell would otherwise be described by the chip's clear button and report a
 * bogus "Remove folder × Folder chip" overlap with the chip it contains.
 *
 * `flatten` keys on the `md:contents` class TOKEN, not on the computed display:
 * below `md` the wrapper is a real flex box (`max-md:flex`), and the controls
 * inside it are what has to be measured — while at `md:`+ the wrapper is
 * `display: contents` and its children are direct flex items of the group. Both
 * regimes yield the same flat list, which is what keeps the line maths valid on
 * either side of the 768 px boundary (`secondTier` is only consulted below it).
 * The token is `md:contents`, i.e. the same breakpoint as every `max-md:`
 * utility: a `sm:contents` wrapper would collapse at 640 px while the `max-md:`
 * utilities still apply, and 640–767 px would be measured as a tiered layout
 * that no longer exists there.
 */
const measureStrip = async (page: Page): Promise<StripMeasurement | null> =>
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

      const send = document.querySelector('main form button[aria-label="Send"]')
      const row = send?.parentElement ?? null
      if (!send || !row) return null
      const group = Array.from(row.children).find((el) => classOf(el).includes('flex-wrap')) ?? null
      if (!group) return null
      const secondTier =
        Array.from(row.querySelectorAll('div')).find((el) =>
          classOf(el).includes('max-md:basis-full'),
        ) ?? null

      const flatten = (nodes: Element[]): Element[] =>
        nodes.flatMap((el) =>
          classOf(el).includes('md:contents') ? flatten(Array.from(el.children)) : [el],
        )

      const describe = (el: Element): ControlMeasurement => {
        const trigger = (
          el.tagName === 'BUTTON' ? el : el.querySelector('button')
        ) as HTMLElement | null
        const itemBox = box(el)
        const triggerBox = trigger ? box(trigger) : null
        const { text, textWidth, spanWidth } = textOf(trigger ?? el)
        return {
          label: trigger?.getAttribute('aria-label') ?? el.getAttribute('aria-label') ?? el.tagName,
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
        Array.from(document.querySelectorAll('main form [data-testid="folder-chip"]')).find(
          (el) => el.getBoundingClientRect().width > 0,
        ) ?? null
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

      const isChipCell = (el: Element) =>
        el.getAttribute('data-testid') === 'folder-chip' ||
        el.querySelector('[data-testid="folder-chip"]') !== null

      const controls = flatten(Array.from(group.children))
        .filter((el) => !isChipCell(el))
        .map(describe)

      const boxes = [...controls.map((control) => control.box), ...(chip ? [chip.box] : [])]
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

      return {
        viewport: window.innerWidth,
        innerHeight: window.innerHeight,
        row: box(row),
        group: box(group),
        nowrap: classOf(group).includes('max-md:flex-nowrap'),
        secondTier: secondTier ? box(secondTier) : null,
        controls,
        chip,
        overlaps,
        send: box(send),
        scrollWidth: document.documentElement.scrollWidth,
        list: list ? { height: box(list).height } : null,
      }
    },
    { chrome: TRIGGER_CHROME_PX },
  )

/** `1` for the primary line, `2` for the secondary tier. */
const lineOfBox = (box: Box, secondTier: Box | null): 1 | 2 =>
  secondTier && box.top >= secondTier.top - 1 ? 2 : 1

const lineOf = (control: ControlMeasurement, secondTier: Box | null): 1 | 2 =>
  lineOfBox(control.box, secondTier)

const describeControl = (control: ControlMeasurement, secondTier: Box | null): string => {
  const width = control.trigger?.width ?? control.box.width
  const parts = [`w=${Math.round(width)}`, `line=${lineOf(control, secondTier)}`]
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

test('the single-selector control strip keeps every control on one line', async ({
  page,
}, testInfo) => {
  await openChat(page)

  const strip = await measureStrip(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `control-strip-${testInfo.project.name}`)

  console.log(
    `[control-strip ${testInfo.project.name}] viewport=${strip.viewport} ` +
      `row=${Math.round(strip.row.width)}px group=${Math.round(strip.group.width)}x${Math.round(strip.group.height)} ` +
      `nowrap=${strip.nowrap} tier2=${strip.secondTier ? 'yes' : 'none'} ` +
      `controls=${strip.controls.map((control) => describeControl(control, strip.secondTier)).join(' ')} ` +
      `send=${strip.send ? `${Math.round(strip.send.width)}x${Math.round(strip.send.height)}` : 'none'} ` +
      `list=${Math.round(strip.list?.height ?? -1)}px innerHeight=${strip.innerHeight} ` +
      `scrollWidth=${strip.scrollWidth}`,
  )

  expect(strip.nowrap).toBe(false)
  expect(strip.secondTier).toBeNull()
  expect(strip.controls).toHaveLength(3)

  const model = strip.controls.find((control) => control.label === 'Model')
  expect(model).toBeDefined()
  if (!model) return

  expect(strip.group.height).toBeLessThanOrEqual(45)
  for (const control of strip.controls) {
    expect(control.box.top).toBeGreaterThanOrEqual(strip.group.top - 1)
    expect(control.box.bottom).toBeLessThanOrEqual(strip.group.bottom + 1)
    expect(control.box.left).toBeGreaterThanOrEqual(strip.group.left - 1)
    expect(control.box.right).toBeLessThanOrEqual(strip.group.right + 1)
    expect(control.clipped).toBe(false)
  }

  expect(model.trigger?.width ?? 0).toBeGreaterThanOrEqual(110)

  expect(strip.send).not.toBeNull()
  const send = strip.send
  if (!send) return
  expect(send.width).toBeGreaterThanOrEqual(40)
  expect(send.height).toBeGreaterThanOrEqual(40)
  expect(send.right).toBeLessThanOrEqual(strip.row.right + 1)
  expect(send.bottom).toBeLessThanOrEqual(strip.row.bottom + 1)
  expect(Math.max(send.top, model.box.top)).toBeLessThanOrEqual(
    Math.min(send.bottom, model.box.bottom),
  )

  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
})

test('the three-selector strip splits into two tiers with every trigger usable', async ({
  page,
}, testInfo) => {
  await openChat(page, { multipleAgents: true, reasoning: true })

  for (const label of SELECTOR_LABELS) {
    await expect(page.locator(`main form button[aria-label="${label}"]`)).toBeVisible({
      timeout: 15_000,
    })
  }

  const strip = await measureStrip(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `full-strip-${testInfo.project.name}`)

  console.log(
    `[full-strip ${testInfo.project.name}] viewport=${strip.viewport} ` +
      `row=${Math.round(strip.row.width)}px group=${Math.round(strip.group.width)}x${Math.round(strip.group.height)} ` +
      `nowrap=${strip.nowrap} tier2Top=${strip.secondTier ? Math.round(strip.secondTier.top) : 'none'} ` +
      `controls=${strip.controls.map((control) => describeControl(control, strip.secondTier)).join(' ')} ` +
      `send=${strip.send ? `${Math.round(strip.send.width)}x${Math.round(strip.send.height)} line=${lineOfBox(strip.send, strip.secondTier)}` : 'none'} ` +
      `list=${Math.round(strip.list?.height ?? -1)}px innerHeight=${strip.innerHeight} ` +
      `scrollWidth=${strip.scrollWidth}`,
  )

  expect(strip.controls).toHaveLength(5)
  expect(strip.nowrap).toBe(false)
  expect(strip.secondTier).not.toBeNull()
  const secondTier = strip.secondTier
  const send = strip.send
  if (!secondTier || !send) return

  const byLabel = (label: string) => {
    const control = strip.controls.find((item) => item.label === label)
    expect(control).toBeDefined()
    return control as ControlMeasurement
  }
  const model = byLabel('Model')
  const thinking = byLabel('Thinking')
  const agent = byLabel('Agent')

  expect(lineOf(model, secondTier)).toBe(1)
  expect(lineOf(thinking, secondTier)).toBe(2)
  expect(lineOf(agent, secondTier)).toBe(2)
  expect(Math.abs(thinking.box.top - agent.box.top)).toBeLessThanOrEqual(1)
  expect(thinking.box.top - model.box.top).toBeGreaterThanOrEqual(8)

  const primary = strip.controls.filter((control) => lineOf(control, secondTier) === 1)
  const secondary = strip.controls.filter((control) => lineOf(control, secondTier) === 2)
  expect(primary).toHaveLength(3)
  expect(secondary).toHaveLength(2)
  for (const control of primary) {
    expect(control.box.bottom).toBeLessThanOrEqual(secondTier.top)
  }

  expect(model.trigger?.width ?? 0).toBeGreaterThanOrEqual(strip.viewport <= 320 ? 110 : 140)
  for (const control of [thinking, agent]) {
    expect(control.trigger?.width ?? 0).toBeGreaterThanOrEqual(60)
  }

  const primaryTop = Math.min(...primary.map((control) => control.box.top))
  const primaryBottom = Math.max(...primary.map((control) => control.box.bottom))
  expect(send.width).toBeGreaterThanOrEqual(40)
  expect(send.height).toBeGreaterThanOrEqual(40)
  expect(send.top).toBeLessThan(secondTier.top)
  expect(send.bottom).toBeGreaterThan(primaryTop)
  expect(send.top).toBeLessThan(primaryBottom)
  expect(send.right).toBeLessThanOrEqual(strip.row.right + 1)
  expect(send.bottom).toBeLessThanOrEqual(strip.row.bottom + 1)

  for (const control of strip.controls) {
    expect(control.box.left).toBeGreaterThanOrEqual(strip.group.left - 1)
    expect(control.box.right).toBeLessThanOrEqual(strip.group.right + 1)
    expect(control.clipped).toBe(false)
  }

  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
  expect(strip.list?.height ?? -1).toBeGreaterThanOrEqual(0.3 * strip.innerHeight)
})

/**
 * Floors for the two secondary triggers, taken from the NO-folder scenario of
 * "the three-selector strip splits into two tiers…" (70 px and 90 px at every
 * project width). Each trigger sizes itself to its label: `Default` needs 68 px
 * and `Main Agent` 88 px once the 34 px of non-text chrome is added, so these
 * floors are exactly "still readable".
 */
const THINKING_FLOOR_WIDTH = 68
const AGENT_FLOOR_WIDTH = 88

const controlByLabel = (strip: StripMeasurement, label: string): ControlMeasurement => {
  const control = strip.controls.find((item) => item.label === label)
  expect(control).toBeDefined()
  return control as ControlMeasurement
}

/**
 * The pair a session folder must never squeeze: both secondary triggers clear
 * their floor and paint their label in full.
 *
 * `truncated` compares the label's intrinsic width against the trigger box, so
 * it catches "the trigger is too narrow for the text"; `spanWidth` is the width
 * the label is painted at, so it catches "the text has already been ellipsised
 * away". Both, because an 11 px span still reports the full 34 px intrinsic
 * width through the Range.
 */
const expectSecondarySelectorsReadable = (strip: StripMeasurement) => {
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

/**
 * The strip also has to survive a session folder, which is the case every other
 * test here misses: the mock answers `folder: ''` unless a test asks for one.
 *
 * With a session folder set, the chip used to sit on the PRIMARY touch line next
 * to the model trigger. Every other item on that line (`+` add-folder, attach,
 * send) is a fixed 44 px target, so the model trigger — the only `flex-1` item —
 * was the single shrinkable element: measured 26 px wide with a 0 px value span
 * at 360 px and 56 px with a 20 px span at 390, i.e. a blank pill. The chip was
 * then moved onto the second tier, next to thinking and agent — and that only
 * moved the damage sideways: measured 50 px / 64 px with 14 px / 28 px value
 * spans at 320 (both below the 60 px bar of the sibling test, and `Def…` /
 * `Main A…` on screen at 390).
 *
 * The chip therefore owns a `basis-full` line of its own on the tier (the tier
 * wraps), so the two selectors keep exactly the widths they have with no folder
 * set. That is what this test pins, and it is the geometry the previous version
 * of this test failed to assert: the chip is ABOVE the selectors, the selectors
 * clear their floors with untruncated value spans, and nothing overlaps.
 */
test('a session folder never squeezes the primary line', async ({ page }, testInfo) => {
  await openChat(page, { multipleAgents: true, reasoning: true, folder: SESSION_FOLDER })

  for (const label of SELECTOR_LABELS) {
    await expect(page.locator(`main form button[aria-label="${label}"]`)).toBeVisible({
      timeout: 15_000,
    })
  }
  await expect(page.locator('main form [data-testid="folder-chip"]:visible')).toBeVisible({
    timeout: 15_000,
  })

  const strip = await measureStrip(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `folder-strip-${testInfo.project.name}`)

  console.log(
    `[folder-strip ${testInfo.project.name}] viewport=${strip.viewport} ` +
      `row=${Math.round(strip.row.width)}px group=${Math.round(strip.group.width)}x${Math.round(strip.group.height)} ` +
      `tier2Top=${strip.secondTier ? Math.round(strip.secondTier.top) : 'none'} ` +
      `controls=${strip.controls.map((control) => describeControl(control, strip.secondTier)).join(' ')} ` +
      `chip=${Math.round(strip.chip?.box.width ?? -1)}px label=${JSON.stringify(strip.chip?.label.text)} ` +
      `labelTruncated=${strip.chip?.label.truncated} ` +
      `send=${strip.send ? `${Math.round(strip.send.width)}x${Math.round(strip.send.height)} line=${lineOfBox(strip.send, strip.secondTier)}` : 'none'} ` +
      `overlaps=${strip.overlaps.join(',') || 'none'} ` +
      `list=${Math.round(strip.list?.height ?? -1)}px innerHeight=${strip.innerHeight} ` +
      `scrollWidth=${strip.scrollWidth}`,
  )

  expect(strip.overlaps).toEqual([])
  expect(strip.controls).toHaveLength(5)
  expect(strip.secondTier).not.toBeNull()
  const secondTier = strip.secondTier
  const send = strip.send
  const chip = strip.chip
  if (!secondTier || !send || !chip) return

  const model = controlByLabel(strip, 'Model')
  const thinking = controlByLabel(strip, 'Thinking')
  const agent = controlByLabel(strip, 'Agent')

  expect(lineOf(model, secondTier)).toBe(1)
  expect(model.trigger?.width ?? 0).toBeGreaterThanOrEqual(110)
  expect(model.spanWidth).toBeGreaterThan(40)
  expect(model.clipped).toBe(false)

  expect(Math.round(send.width)).toBe(44)
  expect(Math.round(send.height)).toBe(44)
  expect(lineOfBox(send, secondTier)).toBe(1)
  expect(send.right).toBeLessThanOrEqual(strip.row.right + 1)

  /** The pair that actually got crushed, asserted before anything else. */
  expectSecondarySelectorsReadable(strip)

  /** Chip printed above the selectors: the tier wraps, so `basis-full` applies. */
  expect(chip.box.top).toBeGreaterThanOrEqual(secondTier.top - 1)
  expect(Math.abs(chip.box.left - secondTier.left)).toBeLessThanOrEqual(1)
  expect(chip.box.right).toBeLessThanOrEqual(strip.group.right + 1)
  expect(chip.label.box).not.toBeNull()
  expect((chip.label.box?.width ?? 0) > 0).toBe(true)
  expect(chip.label.truncated).toBe(true)
  expect(chip.box.width).toBeLessThanOrEqual(200.5)
  expect(chip.box.bottom).toBeLessThanOrEqual(thinking.box.top - 1)
  expect(chip.box.bottom).toBeLessThanOrEqual(agent.box.top - 1)

  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
  expect(strip.list?.height ?? -1).toBeGreaterThanOrEqual(0.3 * strip.innerHeight)
})

/**
 * The folder chip's own line costs the composer a tier (~28 px), which is only
 * acceptable if the composer still leaves the message list room with the
 * TEXTAREA at its maximum. That combination is what this case pins: a folder
 * set (`basis-full` chip line) plus a 12-line draft, which is the tallest the
 * composer can get below `md` (the textarea is capped at 30dvh).
 *
 * Measured with the fix at 320×568: composer 301.5 px (53.1 % of the viewport),
 * list 152.5 px (26.8 %), no document scroll, send still 44×44 on the primary
 * line, and the two selectors still at their no-folder widths.
 */
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
  const strip = await measureStrip(page)
  const shell = await measure(page)
  expect(strip).not.toBeNull()
  if (!strip) return

  await attach(page, testInfo, `folder-long-draft-${testInfo.project.name}`)

  console.log(
    `[folder-long-draft ${testInfo.project.name}] viewport=${strip.viewport} ` +
      `composer=${Math.round(shell.composer?.height ?? -1)}px ` +
      `(${(((shell.composer?.height ?? 0) / strip.innerHeight) * 100).toFixed(1)}%) ` +
      `textarea=${Math.round(textareaHeight)}px list=${Math.round(strip.list?.height ?? -1)}px ` +
      `chip=${Math.round(strip.chip?.box.width ?? -1)}px ` +
      `chipOwnLine=${(strip.chip?.box.bottom ?? 0) <= controlByLabel(strip, 'Thinking').box.top - 1} ` +
      `scrollWidth=${strip.scrollWidth}`,
  )

  expect(textareaHeight).toBeLessThanOrEqual(0.35 * strip.innerHeight)
  expect(shell.composer?.bottom ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(
    strip.innerHeight + 1,
  )
  expect(shell.composer?.top ?? -1).toBeGreaterThanOrEqual(0)

  expect(strip.overlaps).toEqual([])
  expectSecondarySelectorsReadable(strip)

  const chip = strip.chip
  const secondTier = strip.secondTier
  expect(chip).not.toBeNull()
  expect(secondTier).not.toBeNull()
  if (!chip || !secondTier) return

  expect(chip.label.truncated).toBe(true)
  expect(chip.box.width).toBeLessThanOrEqual(200.5)
  expect(chip.box.bottom).toBeLessThanOrEqual(controlByLabel(strip, 'Thinking').box.top - 1)

  expect(strip.scrollWidth).toBeLessThanOrEqual(strip.viewport + 1)
  expect(strip.list?.height ?? -1).toBeGreaterThanOrEqual(0.25 * strip.innerHeight)
})
