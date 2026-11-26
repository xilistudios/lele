/**
 * Desktop-height gate for the settings rail (issue #444).
 *
 * The desktop rail is a vertical column of tabs inside a `flex-1 overflow-hidden`
 * settings row. Below ~771px of viewport height the 15 tabs (Groups enabled) are
 * taller than the row, and because the rail had no vertical overflow the clipped
 * bottom tabs were mouse-unreachable — there was no scrollbar anywhere. This spec
 * pins the contract on the heights the issue measured (1280x720, 1366x768) plus
 * the 1440x900 height where the rail has nothing to scroll:
 *
 *   1. the bottom tab is reachable: the rail scrolls, `logs` is fully visible,
 *      the click lands, the panel switches and the footer stays flush;
 *   2. the fix is rail-only: the settings row never scrolls and the document
 *      never scrolls sideways/downwards (that was the footer-detachment symptom);
 *   3. a deep link to a bottom tab reveals it by itself — no wheel, no click —
 *      and leaves the rail's own padding visible below the pill;
 *   4. when every tab fits, the rail is inert: nothing scrolls, nothing shifts.
 */
import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'

const API_URL = 'http://localhost:18793'
const AGENT_ID = 'main'
const RAIL = 'settings-tabs'
const TAB_PREFIX = 'settings-tab-'
const LOGS_TAB = `${TAB_PREFIX}logs`
const SCROLL_AREA = 'settings-scroll-area'
const TAB_COUNT_WITH_GROUPS = 15
const LOGS_EMPTY = 'No log entries found for this date.'
const PADDING_TOLERANCE = 1

const AGENT = {
  id: AGENT_ID,
  name: 'Main Agent',
  workspace: '~/.lele',
  model: 'deepseek-v4.1-flash',
  default: true,
}

const CONFIG = {
  config: {
    agents: {
      list: [AGENT],
      defaults: {
        workspace: '~/.lele',
        restrict_to_workspace: false,
        provider: 'openai',
        model: 'deepseek-v4.1-flash',
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
  return decodeURIComponent(index >= 0 ? withoutQuery.slice(index) : withoutQuery)
}

const respond = (url: string): unknown => {
  const path = normalizePath(url)
  if (path.endsWith('/api/v1/auth/status')) {
    return {
      valid: true,
      client_id: 'client-e2e',
      device_name: 'Desktop',
      expires: '2030-01-01T00:00:00Z',
    }
  }
  if (path.endsWith('/api/v1/agents')) return { agents: [AGENT] }
  if (path.endsWith(`/api/v1/agents/${AGENT_ID}/status`)) {
    return { id: AGENT_ID, status: 'running', active_sessions: 1 }
  }
  if (path.endsWith(`/api/v1/agents/${AGENT_ID}`)) return AGENT
  if (path.endsWith('/api/v1/status')) {
    return { status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }
  }
  if (path.endsWith('/api/v1/channels')) return { channels: [] }
  if (path.endsWith('/api/v1/tools')) return { tools: [] }
  if (path.endsWith('/api/v1/config')) return CONFIG
  if (path.endsWith('/api/v1/models')) {
    return { agent_id: AGENT_ID, model: 'deepseek-v4.1-flash', models: ['deepseek-v4.1-flash'] }
  }
  if (path.endsWith('/api/v1/chat/commands')) return { commands: [] }
  if (path.endsWith('/api/v1/chat/sessions/meta') || path.endsWith('/api/v1/chat/sessions')) {
    return { sessions: [], has_more: false }
  }
  if (path.endsWith('/api/v1/logs/dates')) return { dates: ['2026-01-01'] }
  if (path.endsWith('/api/v1/logs')) {
    return { entries: [], total_lines: 0, returned_lines: 0, file: '/tmp/lele.log' }
  }
  return {}
}

/**
 * Seed the session and stub the socket. The `groups` tab only exists when the
 * welcome event says so, and 15 tabs is the configuration the issue measured.
 * The welcome is emitted asynchronously on purpose: it is the late arrival of
 * that tab which a stale reveal cannot see.
 */
const STUB_INIT = ({ apiUrl }: { apiUrl: string }) => {
  localStorage.setItem(
    'lele.session',
    JSON.stringify({
      token: 'e2e-token',
      refresh_token: 'e2e-refresh',
      expires: '2030-01-01T00:00:00Z',
      client_id: 'client-e2e',
      device_name: 'Desktop',
    }),
  )
  localStorage.setItem('lele.apiUrl', apiUrl)
  localStorage.setItem('i18nextLng', 'en')
  localStorage.setItem('lele-theme', 'dark')

  type Listener = (event: unknown) => void

  class StubWebSocket {
    static readonly CONNECTING = 0
    static readonly OPEN = 1
    static readonly CLOSING = 2
    static readonly CLOSED = 3
    readyState = StubWebSocket.CONNECTING
    private readonly listeners: Record<string, Listener[]> = {}

    constructor() {
      setTimeout(() => {
        this.readyState = StubWebSocket.OPEN
        this.emit('open', {})
        this.emit('message', {
          data: JSON.stringify({
            event: 'welcome',
            data: {
              client_id: 'client-e2e',
              device_name: 'Desktop',
              session_key: 'native:client-e2e:1',
              status: 'ok',
              agents: [],
              server_time: '2026-01-01T00:00:00Z',
              groups_enabled: true,
            },
          }),
        })
      }, 0)
    }

    addEventListener(type: string, listener: Listener) {
      this.listeners[type] = [...(this.listeners[type] ?? []), listener]
    }

    removeEventListener(type: string, listener: Listener) {
      this.listeners[type] = (this.listeners[type] ?? []).filter((item) => item !== listener)
    }

    send() {}

    close() {
      this.readyState = StubWebSocket.CLOSED
    }

    private emit(type: string, event: unknown) {
      for (const listener of this.listeners[type] ?? []) listener(event)
    }
  }

  Object.defineProperty(window, 'WebSocket', { value: StubWebSocket, configurable: true })
}

const openSettings = async (page: Page, path: string) => {
  await page.addInitScript(STUB_INIT, { apiUrl: API_URL })
  await page.route('**/api/**', async (route) => {
    await route.fulfill({ json: respond(route.request().url()) })
  })
  await page.goto(path)
  await expect(page.getByTestId(RAIL)).toBeVisible({ timeout: 30_000 })
  await expect(page.locator(`[data-testid^="${TAB_PREFIX}"]`)).toHaveCount(TAB_COUNT_WITH_GROUPS, {
    timeout: 30_000,
  })
}

type Geometry = {
  rail: {
    scrollTop: number
    scrollHeight: number
    clientHeight: number
    overflowX: number
    paddingBottom: number
    overflows: boolean
    logsInView: boolean
    bottomGap: number
  }
  rowOverflowY: string
  rowScrollTop: number
  rowScrollLeft: number
  documentScrollTop: number
  documentScrollLeft: number
  scrollAreaBottom: number
  footerBottom: number
  footerTop: number
  viewportHeight: number
}

const measure = (page: Page): Promise<Geometry> =>
  page.evaluate(
    ({ railId, logsId, scrollAreaId }) => {
      const rail = document.querySelector(`[data-testid="${railId}"]`)
      const logs = document.querySelector(`[data-testid="${logsId}"]`)
      const scrollArea = document.querySelector(`[data-testid="${scrollAreaId}"]`)
      if (!rail || !logs || !scrollArea) throw new Error('settings rail not mounted')
      const row = rail.parentElement
      const column = scrollArea.parentElement
      const footer = column?.lastElementChild
      if (!row || !footer) throw new Error('settings layout not mounted')

      const viewRect = rail.getBoundingClientRect()
      const logsRect = logs.getBoundingClientRect()
      const root = document.documentElement

      return {
        rail: {
          scrollTop: rail.scrollTop,
          scrollHeight: rail.scrollHeight,
          clientHeight: rail.clientHeight,
          overflowX: rail.scrollWidth - rail.clientWidth,
          paddingBottom: Number.parseFloat(getComputedStyle(rail).paddingBottom) || 0,
          overflows: rail.scrollHeight > rail.clientHeight,
          logsInView:
            logsRect.top >= viewRect.top - 0.5 && logsRect.bottom <= viewRect.bottom + 0.5,
          bottomGap: viewRect.bottom - logsRect.bottom,
        },
        rowOverflowY: getComputedStyle(row).overflowY,
        rowScrollTop: row.scrollTop,
        rowScrollLeft: row.scrollLeft,
        documentScrollTop: root.scrollTop,
        documentScrollLeft: root.scrollLeft,
        scrollAreaBottom: scrollArea.getBoundingClientRect().bottom,
        footerTop: footer.getBoundingClientRect().top,
        footerBottom: footer.getBoundingClientRect().bottom,
        viewportHeight: window.innerHeight,
      }
    },
    { railId: RAIL, logsId: LOGS_TAB, scrollAreaId: SCROLL_AREA },
  )

const scrollRailToBottom = async (page: Page) => {
  await page.getByTestId(RAIL).hover()
  await page.mouse.wheel(0, 600)
  await expect.poll(async () => (await measure(page)).rail.scrollTop).toBeGreaterThan(0)
}

test('bottom settings tab is reachable on a desktop viewport', async ({ page }) => {
  await openSettings(page, '/settings/general')

  const before = await measure(page)
  expect(before.rail.scrollHeight).toBeGreaterThanOrEqual(before.rail.clientHeight)
  if (before.rail.overflows) {
    expect(before.rail.logsInView).toBe(false)
    await scrollRailToBottom(page)
    expect((await measure(page)).rail.logsInView).toBe(true)
  }

  await page.getByTestId(LOGS_TAB).click()

  await expect(page).toHaveURL(/\/settings\/logs$/)
  await expect(page.getByTestId(LOGS_TAB)).toHaveAttribute('aria-current', 'page')
  await expect(page.getByTestId(SCROLL_AREA)).toContainText(LOGS_EMPTY, { timeout: 15_000 })

  const settled = await measure(page)
  expect(settled.rail.logsInView).toBe(true)
  expect(Math.abs(settled.footerBottom - settled.viewportHeight)).toBeLessThanOrEqual(1)
  expect(Math.abs(settled.footerTop - settled.scrollAreaBottom)).toBeLessThanOrEqual(1)
})

test('focusing the clipped tab reveals it without moving the row or footer', async ({ page }) => {
  await openSettings(page, '/settings/general')

  const before = await measure(page)
  test.skip(!before.rail.overflows, 'rail has nothing to scroll at this height')
  expect(before.rail.logsInView).toBe(false)
  expect(before.rowOverflowY).toBe('hidden')
  expect(before.rowScrollTop).toBe(0)
  expect(Math.abs(before.footerBottom - before.viewportHeight)).toBeLessThanOrEqual(1)

  await page.getByTestId(LOGS_TAB).focus()

  const after = await measure(page)
  expect(after.rail.scrollTop).toBeGreaterThan(0)
  expect(after.rail.logsInView).toBe(true)
  expect(after.rail.overflowX).toBeLessThanOrEqual(0)
  expect(after.rowOverflowY).toBe('hidden')
  expect(after.rowScrollTop).toBe(0)
  expect(after.rowScrollLeft).toBe(0)
  expect(after.documentScrollTop).toBe(0)
  expect(after.documentScrollLeft).toBe(0)
  expect(after.footerBottom).toBe(before.footerBottom)
  expect(Math.abs(after.footerBottom - after.viewportHeight)).toBeLessThanOrEqual(1)
})

test('deep link to a bottom tab reveals it without any user scrolling', async ({ page }) => {
  await openSettings(page, '/settings/logs')

  const revealed = await measure(page)
  expect(revealed.rail.logsInView).toBe(true)
  expect(revealed.rail.bottomGap).toBeGreaterThanOrEqual(
    revealed.rail.paddingBottom - PADDING_TOLERANCE,
  )
  if (revealed.rail.overflows) expect(revealed.rail.scrollTop).toBeGreaterThan(0)

  await expect(page.getByTestId(LOGS_TAB)).toHaveAttribute('aria-current', 'page')
  await expect(page.getByTestId(SCROLL_AREA)).toContainText(LOGS_EMPTY, { timeout: 15_000 })
  expect(revealed.rowScrollTop).toBe(0)
  expect(revealed.documentScrollTop).toBe(0)
  expect(Math.abs(revealed.footerBottom - revealed.viewportHeight)).toBeLessThanOrEqual(1)
})

test('rail is inert on a viewport where every tab fits', async ({ page }) => {
  await openSettings(page, '/settings/general')

  const idle = await measure(page)
  test.skip(idle.rail.overflows, 'rail overflows at this height')
  expect(idle.rail.scrollHeight).toBe(idle.rail.clientHeight)
  expect(idle.rail.scrollTop).toBe(0)
  expect(idle.rail.logsInView).toBe(true)
  expect(idle.rail.overflowX).toBeLessThanOrEqual(0)
  expect(idle.rowScrollTop).toBe(0)
  expect(Math.abs(idle.footerBottom - idle.viewportHeight)).toBeLessThanOrEqual(1)
})
