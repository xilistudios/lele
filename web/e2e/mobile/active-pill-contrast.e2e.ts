/**
 * Contrast gate for the active settings tab and the active MCP filter chip
 * (issue #443). Both used to paint `text-accent-primary` on
 * `bg-surface-selected`: 2.40:1 in the dark theme, below the 4.5:1 WCAG AA
 * floor for normal text (they render at 13px and 11px).
 *
 * The unit suites pin the token recipe and `lint:design` pins the token pair,
 * but neither sees what the browser actually paints. This spec measures the
 * computed colour of the two real elements in both themes and recomputes the
 * WCAG 2.1 ratio from the rendered pixels.
 */
import { expect, test } from '@playwright/test'
import type { Page, TestInfo } from '@playwright/test'

const API_URL = 'http://localhost:18793'
const AGENT_ID = 'main'
const SETTINGS_TAB = 'settings-tab-general'
const MCP_CHIP = 'mcp-filter-all'

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

const MCP_INVENTORY = {
  agent_id: AGENT_ID,
  layers: [
    { layer: 'global', path: '/home/u/.lele/mcp.json', exists: true },
    { layer: 'agent', path: '/home/u/.lele/workspace/mcp.json', exists: false },
    { layer: 'project', exists: false },
  ],
  servers: [
    {
      name: 'github',
      layer: 'global',
      path: '/home/u/.lele/mcp.json',
      effective: 'enabled',
      defines: true,
      server: {
        command: 'npx mcp-github',
        args: 0,
        auth: { required: false, authenticated: false },
      },
    },
  ],
  warnings: [],
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
      device_name: 'Pixel',
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
  if (path.endsWith('/api/v1/mcp')) return MCP_INVENTORY
  if (path.endsWith('/api/v1/chat/sessions/meta') || path.endsWith('/api/v1/chat/sessions')) {
    return { sessions: [], has_more: false }
  }
  return {}
}

const STUB_INIT = ({ apiUrl, theme }: { apiUrl: string; theme: string }) => {
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
  localStorage.setItem('i18nextLng', 'en')
  localStorage.setItem('lele-theme', theme)

  class StubWebSocket {
    static readonly CONNECTING = 0
    static readonly OPEN = 1
    static readonly CLOSING = 2
    static readonly CLOSED = 3
    readyState = StubWebSocket.CONNECTING
    addEventListener() {}
    removeEventListener() {}
    send() {}
    close() {
      this.readyState = StubWebSocket.CLOSED
    }
  }

  Object.defineProperty(window, 'WebSocket', { value: StubWebSocket, configurable: true })
}

const openRoute = async (page: Page, path: string, theme: 'dark' | 'light') => {
  await page.addInitScript(STUB_INIT, { apiUrl: API_URL, theme })
  await page.route('**/api/**', async (route) => {
    await route.fulfill({ json: respond(route.request().url()) })
  })
  await page.goto(path)
}

type Measured = { ratio: number; foreground: string; background: string; fontSize: string }

/**
 * Recompute the WCAG 2.1 contrast ratio of an element from its computed style.
 * A translucent background is composited onto the first opaque ancestor, so
 * the number reflects what the user sees.
 */
const measureContrast = (page: Page, testId: string): Promise<Measured> =>
  page.evaluate((id) => {
    const element = document.querySelector(`[data-testid="${id}"]`)
    if (!element) throw new Error(`missing element: ${id}`)

    const parse = (value: string): [number, number, number, number] => {
      const match = value.match(/rgba?\(([^)]+)\)/)
      if (!match) throw new Error(`unparsable colour: ${value}`)
      const parts = (match[1] ?? '')
        .split(/[,\s/]+/)
        .filter(Boolean)
        .map(Number)
      return [parts[0] ?? 0, parts[1] ?? 0, parts[2] ?? 0, parts[3] ?? 1]
    }

    const opaqueBackground = (start: Element): [number, number, number] => {
      let node: Element | null = start
      while (node) {
        const [r, g, b, a] = parse(getComputedStyle(node).backgroundColor)
        if (a >= 1) return [r, g, b]
        node = node.parentElement
      }
      return [255, 255, 255]
    }

    const style = getComputedStyle(element)
    const [fr, fg, fb, fa] = parse(style.color)
    const [br, bg, bb, ba] = parse(style.backgroundColor)
    const underneath = ba < 1 ? opaqueBackground(element.parentElement ?? element) : null
    const composite = (channel: number, base: number, alpha: number) =>
      Math.round(channel * alpha + base * (1 - alpha))
    const bgFinal: [number, number, number] =
      ba < 1 && underneath
        ? [
            composite(br, underneath[0], ba),
            composite(bg, underneath[1], ba),
            composite(bb, underneath[2], ba),
          ]
        : [br, bg, bb]
    const fgFinal: [number, number, number] =
      fa < 1
        ? [
            composite(fr, bgFinal[0], fa),
            composite(fg, bgFinal[1], fa),
            composite(fb, bgFinal[2], fa),
          ]
        : [fr, fg, fb]

    const linear = (channel: number) => {
      const s = channel / 255
      return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
    }
    const luminance = ([r, g, b]: [number, number, number]) =>
      0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b)
    const l1 = luminance(fgFinal)
    const l2 = luminance(bgFinal)
    const lighter = Math.max(l1, l2)
    const darker = Math.min(l1, l2)

    return {
      ratio: (lighter + 0.05) / (darker + 0.05),
      foreground: style.color,
      background: `rgb(${bgFinal.join(' ')})`,
      fontSize: style.fontSize,
    }
  }, testId)

const AA_NORMAL_TEXT = 4.5

const annotate = (testInfo: TestInfo, element: string, measured: Measured) => {
  const summary = `${element}: ${measured.ratio.toFixed(2)}:1 (${measured.foreground} on ${measured.background}, ${measured.fontSize})`
  testInfo.annotations.push({ type: 'contrast', description: summary })
  console.log(`[contrast] ${testInfo.project.name} ${summary}`)
}

for (const theme of ['dark', 'light'] as const) {
  test(`active settings tab keeps AA contrast in the ${theme} theme`, async ({
    page,
  }, testInfo) => {
    await openRoute(page, '/settings/general', theme)
    const tab = page.getByTestId(SETTINGS_TAB)
    await expect(tab).toBeVisible({ timeout: 30_000 })
    await expect(tab).toHaveAttribute('aria-current', 'page')

    const measured = await measureContrast(page, SETTINGS_TAB)
    annotate(testInfo, 'active settings tab', measured)
    expect(Number.parseFloat(measured.fontSize)).toBeLessThan(18.66)
    expect(measured.ratio).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })

  test(`active MCP filter chip keeps AA contrast in the ${theme} theme`, async ({
    page,
  }, testInfo) => {
    await openRoute(page, `/settings/agents/${AGENT_ID}/mcp`, theme)
    const chip = page.getByTestId(MCP_CHIP)
    await expect(chip).toBeVisible({ timeout: 30_000 })
    await expect(chip).toHaveAttribute('aria-pressed', 'true')

    const measured = await measureContrast(page, MCP_CHIP)
    annotate(testInfo, 'active MCP chip', measured)
    expect(Number.parseFloat(measured.fontSize)).toBeLessThan(18.66)
    expect(measured.ratio).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })
}
