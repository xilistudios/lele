/**
 * Mobile gate for the Secrets view.
 *
 * The desktop layout puts the identity block, the tab switcher and the
 * New/Refresh actions on one row, and the per-secret actions in a trailing
 * column. At phone widths that combination is wider than the viewport, so the
 * header and the cards used to bleed off-screen (see the 2026-11-24 report).
 * These specs pin the mobile contract: everything the user must reach stays
 * inside the viewport, and the page never scrolls sideways.
 */
import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import type { Page, TestInfo } from '@playwright/test'

const API_URL = 'http://localhost:18793'
const SCREENSHOT_DIR = join(process.cwd(), 'test-results', 'e2e-mobile', 'screenshots')

const LONG_NAME = 'CITLI_01_VM_PASSWORD'
const MEDIUM_NAME = 'BINANCE_SECRET_KEY'

const AGENT = {
  id: 'main',
  name: 'Main Agent',
  workspace: '~/.lele',
  model: 'deepseek-v4.1-flash',
  default: true,
}

const SECRETS = [
  {
    name: 'AGENTHANSAS_KEY',
    description: '',
    tags: [],
    scope: [],
    created_at: '2026-08-03T22:58:51Z',
    updated_at: '2026-08-03T22:58:51Z',
    created_by: 'webui',
  },
  {
    name: LONG_NAME,
    description: 'Password for the vps vm citli-01 administrator@93.127.129.140',
    tags: ['vps', 'infra'],
    scope: ['main', 'ui-engineer'],
    created_at: '2026-09-28T13:35:15Z',
    updated_at: '2026-09-28T13:35:15Z',
    created_by: 'webui',
  },
  {
    name: MEDIUM_NAME,
    description: '',
    tags: [],
    scope: [],
    created_at: '2026-08-02T16:56:32Z',
    updated_at: '2026-08-02T16:56:32Z',
    created_by: 'webui',
  },
]

const AUDIT = [
  {
    secret_name: LONG_NAME,
    agent_id: 'main',
    session_key: 'native:client-e2e:1',
    action: 'read',
    timestamp: '2026-11-24T09:10:00Z',
    granted: true,
  },
  {
    secret_name: MEDIUM_NAME,
    agent_id: 'ui-engineer',
    session_key: 'native:client-e2e:1',
    action: 'read',
    timestamp: '2026-11-24T09:05:00Z',
    granted: false,
  },
]

const STATUS = { enabled: true, backend: 'file', count: SECRETS.length }

const normalizePath = (url: string): string => {
  const withoutQuery = url.split('?')[0] ?? url
  const marker = '/api/v1/'
  const index = withoutQuery.indexOf(marker)
  const path = index >= 0 ? withoutQuery.slice(index) : withoutQuery
  return decodeURIComponent(path)
}

const respond = (url: string, method: string): unknown => {
  const path = normalizePath(url)

  if (path.endsWith('/api/v1/auth/status')) {
    return {
      valid: true,
      client_id: 'client-e2e',
      device_name: 'Pixel',
      expires: '2030-01-01T00:00:00Z',
    }
  }
  if (path.endsWith('/api/v1/secrets/audit')) return { audit: AUDIT }
  if (path.endsWith('/api/v1/secrets/status')) return STATUS
  if (path.endsWith('/api/v1/secrets')) {
    if (method === 'POST') return { secret: SECRETS[0] }
    return { secrets: SECRETS, status: STATUS }
  }
  if (path.includes('/api/v1/secrets/')) {
    // reveal
    return { secret: SECRETS[0], value: 'super-secret-value-0123456789abcdef' }
  }
  if (path.endsWith('/api/v1/agents')) return { agents: [AGENT] }
  if (path.endsWith('/api/v1/status')) {
    return { status: 'ok', uptime: '1h', agents: [], channels: [], version: 'dev' }
  }
  if (path.endsWith('/api/v1/config')) {
    return {
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
  }
  if (path.endsWith('/api/v1/chat/sessions/meta') || path.endsWith('/api/v1/chat/sessions')) {
    return { sessions: [], has_more: false }
  }
  if (path.endsWith('/api/v1/models')) {
    return { agent_id: 'main', model: 'deepseek-v4.1-flash', models: ['deepseek-v4.1-flash'] }
  }
  if (path.endsWith('/api/v1/chat/commands')) return { commands: [] }
  if (path.endsWith('/api/v1/tools')) return { tools: [] }
  if (path.endsWith('/api/v1/channels')) return { channels: [] }
  return {}
}

const STUB_INIT = ({ apiUrl }: { apiUrl: string }) => {
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

type Box = {
  top: number
  bottom: number
  left: number
  right: number
  width: number
  height: number
}

const headerSignature = (m: Awaited<ReturnType<typeof measureSecrets>>): string =>
  JSON.stringify([
    m.viewport,
    m.docScrollWidth,
    m.headerSections.map((s) => [Math.round(s.left), Math.round(s.right)]),
    m.tabs.map((t) => [t.text, Math.round(t.left), Math.round(t.right)]),
  ])

/**
 * A viewport change lands in two steps: the layout viewport, then React's
 * re-render of the shell. Measuring after the first step reads stale geometry,
 * so poll until two consecutive frames agree.
 */
const measureStable = async (page: Page) => {
  let previous = await measureSecrets(page)
  let signature = headerSignature(previous)
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    )
    previous = await measureSecrets(page)
    const next = headerSignature(previous)
    if (next === signature) return previous
    signature = next
  }
  return previous
}

const openSecrets = async (page: Page) => {
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    await route.fulfill({ json: respond(request.url(), request.method()) })
  })
  await page.goto('/secrets')
  await expect(page.getByText(LONG_NAME)).toBeVisible({ timeout: 30_000 })
}

const measureSecrets = (page: Page) =>
  page.evaluate(() => {
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
    const visible = (el: Element) => {
      const r = el.getBoundingClientRect()
      const style = getComputedStyle(el)
      return r.width > 0 && r.height > 0 && style.visibility !== 'hidden'
    }

    const viewport = window.innerWidth
    const header = document.querySelector('main > header')
    const scroller = main_scroller()
    const cards = Array.from(document.querySelectorAll('main .rounded-xl.border')).filter(
      (el) => el.querySelector('.font-mono') !== null,
    )

    function main_scroller(): HTMLElement | null {
      return document.querySelector('main > div.overflow-y-auto') as HTMLElement | null
    }

    const buttons = (root: Element | null) =>
      root ? Array.from(root.querySelectorAll('button')).map((el) => ({ el, box: box(el) })) : []

    const cardInfo = cards.map((card) => {
      const cardBox = box(card)
      const info = card.querySelector(':scope > div > div') as HTMLElement | null
      const name = card.querySelector('.font-mono') as HTMLElement | null
      const actions = card.querySelector(':scope > div > div:last-child') as HTMLElement | null
      const actionButtons = Array.from(
        card.querySelectorAll(':scope > div > div:last-child button'),
      )
      const actionBoxes = actionButtons.map((el) => ({ text: el.textContent, ...box(el) }))
      const nameRange = name ? document.createRange() : null
      if (name && nameRange) nameRange.selectNodeContents(name)
      return {
        text: (name?.textContent ?? '').trim(),
        box: cardBox,
        name: name ? box(name) : null,
        nameTextWidth: nameRange ? nameRange.getBoundingClientRect().width : 0,
        nameScrollWidth: name?.scrollWidth ?? 0,
        nameClientWidth: name?.clientWidth ?? 0,
        info: info ? box(info) : null,
        actions: actions ? box(actions) : null,
        actionButtons: actionBoxes,
      }
    })

    const headerTabs = Array.from(header?.querySelectorAll('button') ?? []).filter((el) =>
      ['Secrets', 'Audit Log'].includes((el.textContent ?? '').trim()),
    )

    return {
      viewport,
      innerHeight: window.innerHeight,
      docScrollWidth: document.documentElement.scrollWidth,
      bodyScrollWidth: document.body.scrollWidth,
      mainScrollWidth: scroller?.scrollWidth ?? null,
      mainClientWidth: scroller?.clientWidth ?? null,
      header: header ? box(header) : null,
      headerScrollWidth: header?.scrollWidth ?? null,
      headerButtons: buttons(header).map(({ el, box: b }) => ({
        text: (el.getAttribute('aria-label') ?? el.textContent ?? '').replace(/\s+/g, ' ').trim(),
        title: el.getAttribute('title'),
        ...b,
      })),
      tabs: headerTabs.map((el) => {
        const rect = el.getBoundingClientRect()
        const hit = document.elementFromPoint(
          rect.left + rect.width / 2,
          rect.top + rect.height / 2,
        )
        return {
          text: (el.textContent ?? '').trim(),
          ...box(el),
          clipped: el.scrollWidth > el.clientWidth + 1,
          hittable: hit?.closest('button') === el,
        }
      }),
      headerSections: Array.from(header?.children ?? []).map((el) => box(el)),
      statusText: (() => {
        const span = header?.querySelector(':scope > div:last-child > span')
        return span ? { text: (span.textContent ?? '').trim(), ...box(span) } : null
      })(),
      cards: cardInfo,
      auditRows: Array.from(document.querySelectorAll('main table tbody tr')).map((row) => ({
        box: box(row),
        cells: Array.from(row.querySelectorAll('td')).map((td) => ({
          label: (td.querySelector('span')?.textContent ?? '').trim(),
          box: box(td),
        })),
      })),
      auditHeaderVisible: (() => {
        const thead = document.querySelector('main table thead')
        return thead ? visible(thead) : false
      })(),
      visibleText: document.body.innerText,
    }
  })

const attach = async (page: Page, testInfo: TestInfo, name: string) => {
  const body = await page.screenshot({ fullPage: false })
  await testInfo.attach(name, { body, contentType: 'image/png' })
  await mkdir(SCREENSHOT_DIR, { recursive: true })
  await writeFile(join(SCREENSHOT_DIR, `${name}.png`), body)
}

const logSecrets = (name: string, m: Awaited<ReturnType<typeof measureSecrets>>) =>
  console.log(
    `[${name}] viewport=${m.viewport} docScrollWidth=${m.docScrollWidth} ` +
      `mainScroll=${m.mainScrollWidth}/${m.mainClientWidth} headerScroll=${m.headerScrollWidth} ` +
      `header=${m.header ? `${Math.round(m.header.height)}px` : 'none'} ` +
      `tabs=${m.tabs.map((t) => `${t.text}:${Math.round(t.width)}x${Math.round(t.height)}`).join(',')} ` +
      `buttons=${m.headerButtons.map((b) => `${b.text}:${Math.round(b.width)}x${Math.round(b.height)}@${Math.round(b.left)}-${Math.round(b.right)}`).join(',')} ` +
      `cards=${m.cards
        .map(
          (c) =>
            `${c.text}:${Math.round(c.box.width)}x${Math.round(c.box.height)} ` +
            `name=${Math.round(c.name?.width ?? -1)}(${c.nameScrollWidth}/${c.nameClientWidth}) ` +
            `actions=${c.actionButtons.map((b) => `${Math.round(b.width)}@${Math.round(b.left)}`).join('|')}`,
        )
        .join(' ; ')}`,
  )

test.beforeEach(async ({ page }) => {
  await page.addInitScript(STUB_INIT, { apiUrl: API_URL })
})

test('the secrets header keeps its actions inside the viewport', async ({ page }, testInfo) => {
  await openSecrets(page)
  const m = await measureSecrets(page)
  await attach(page, testInfo, `secrets-header-${testInfo.project.name}`)
  logSecrets(`header ${testInfo.project.name}`, m)

  // no sideways scroll anywhere
  expect(m.docScrollWidth).toBeLessThanOrEqual(m.viewport + 1)
  expect(m.mainScrollWidth ?? 0).toBeLessThanOrEqual((m.mainClientWidth ?? m.viewport) + 1)
  expect(m.headerScrollWidth ?? 0).toBeLessThanOrEqual(m.viewport + 1)

  // the tab switcher keeps both labels on one line and inside the viewport
  expect(m.tabs).toHaveLength(2)
  for (const tab of m.tabs) {
    expect(tab.left).toBeGreaterThanOrEqual(0)
    expect(tab.right).toBeLessThanOrEqual(m.viewport)
    expect(tab.width).toBeGreaterThanOrEqual(70)
    expect(tab.clipped, `${tab.text} label is clipped`).toBe(false)
    expect(tab.hittable, `${tab.text} is covered by another control`).toBe(true)
  }

  // the "N secrets" counter stays on one line next to the tabs
  expect(m.statusText).not.toBeNull()
  if (m.statusText) {
    expect(m.statusText.text).toContain(`${SECRETS.length} secrets`)
    expect(m.statusText.height).toBeLessThanOrEqual(24)
    expect(m.statusText.right).toBeLessThanOrEqual(m.viewport)
  }

  // New + Refresh are reachable without scrolling
  for (const label of ['New', 'Refresh']) {
    const button = m.headerButtons.find((b) => b.text === label || b.text.endsWith(label))
    expect(button, `missing ${label} button`).toBeDefined()
    if (!button) continue
    expect(button.left).toBeGreaterThanOrEqual(0)
    expect(button.right).toBeLessThanOrEqual(m.viewport)
    expect(button.top).toBeGreaterThanOrEqual(0)
    expect(button.bottom).toBeLessThanOrEqual(m.innerHeight)
    expect(button.height).toBeGreaterThanOrEqual(32)
  }

  // header sits above the list and does not eat the screen
  expect(m.header?.height ?? 0).toBeLessThanOrEqual(120)
})

const HEADER_BREAKPOINTS = [768, 900, 1024] as const

/**
 * The desktop header is one row: identity | tabs | New + Refresh. When the
 * sidebar eats the width the three sections negotiate space, and a section
 * that is allowed to shrink below its content lets a neighbour paint on top
 * of it — which is how the New button ended up covering "Audit Log" at 768.
 */
test('the header sections never cover each other at tablet widths', async ({ page }, testInfo) => {
  await openSecrets(page)

  for (const width of HEADER_BREAKPOINTS) {
    await page.setViewportSize({ width, height: 900 })
    const m = await measureStable(page)
    await attach(page, testInfo, `secrets-header-${width}`)

    console.log(
      `[tablet ${width}] innerWidth=${m.viewport} doc=${m.docScrollWidth} ` +
        `sections=${m.headerSections
          .map((sec) => `${Math.round(sec.left)}-${Math.round(sec.right)}`)
          .join(
            ',',
          )} tabs=${m.tabs.map((t) => `${t.text}:${Math.round(t.left)}-${Math.round(t.right)}:hit=${t.hittable}`).join(',')}`,
    )
    const [identity, actions, tabs] = m.headerSections
    expect(m.headerSections).toHaveLength(3)
    for (const section of m.headerSections) {
      expect(section.right).toBeLessThanOrEqual(m.viewport)
      expect(section.left).toBeGreaterThanOrEqual(0)
    }

    // no pairwise overlap between the identity, the actions and the tabs
    const pairs: Array<[string, typeof identity, typeof identity]> = [
      ['identity/actions', identity, actions],
      ['identity/tabs', identity, tabs],
      ['actions/tabs', actions, tabs],
    ]
    for (const [name, a, b] of pairs) {
      const across = Math.min(a.right, b.right) - Math.max(a.left, b.left)
      expect(across, `${name} overlap at ${width}px`).toBeLessThanOrEqual(0.5)
    }

    for (const tab of m.tabs) {
      expect(tab.hittable, `${tab.text} not clickable at ${width}px`).toBe(true)
    }
    expect(m.docScrollWidth).toBeLessThanOrEqual(width + 1)
  }
})

test('secret cards stack their actions under the content on mobile', async ({ page }, testInfo) => {
  await openSecrets(page)
  const m = await measureSecrets(page)
  await attach(page, testInfo, `secrets-cards-${testInfo.project.name}`)
  logSecrets(`cards ${testInfo.project.name}`, m)

  const contentRight = m.mainClientWidth ?? m.viewport
  expect(m.cards).toHaveLength(SECRETS.length)

  for (const card of m.cards) {
    expect(card.box.left).toBeGreaterThanOrEqual(0)
    expect(card.box.right).toBeLessThanOrEqual(contentRight + 1)
    expect(card.nameClientWidth).toBeLessThanOrEqual(card.box.width)

    // the actions live on their own row: below the text block, never beside it
    expect(card.actions).not.toBeNull()
    expect(card.info).not.toBeNull()
    if (!card.actions || !card.info) continue
    expect(card.actions.top).toBeGreaterThanOrEqual(card.info.bottom - 1)

    for (const button of card.actionButtons) {
      expect(button.left).toBeGreaterThanOrEqual(card.box.left)
      expect(button.right).toBeLessThanOrEqual(card.box.right)
      expect(button.height).toBeGreaterThanOrEqual(32)
    }

    // side by side tap targets share the row instead of spilling out
    const rows = new Set(card.actionButtons.map((b) => Math.round(b.top)))
    expect(rows.size).toBe(1)
  }

  // the actions are reachable, not merely present
  await expect(page.getByRole('button', { name: 'Delete' }).first()).toBeVisible()
  await expect(page.getByRole('button', { name: 'Reveal' }).first()).toBeVisible()
})

test('the audit log turns into readable rows instead of a wide table', async ({
  page,
}, testInfo) => {
  await openSecrets(page)
  await page.getByRole('button', { name: 'Audit Log' }).click()
  await expect(page.getByText('read').first()).toBeVisible({ timeout: 15_000 })

  const m = await measureSecrets(page)
  await attach(page, testInfo, `secrets-audit-${testInfo.project.name}`)
  console.log(
    `[audit ${testInfo.project.name}] viewport=${m.viewport} rows=${m.auditRows.length} ` +
      `theadVisible=${m.auditHeaderVisible} docScrollWidth=${m.docScrollWidth}`,
  )

  expect(m.docScrollWidth).toBeLessThanOrEqual(m.viewport + 1)
  expect(m.mainScrollWidth ?? 0).toBeLessThanOrEqual((m.mainClientWidth ?? m.viewport) + 1)
  expect(m.auditRows).toHaveLength(AUDIT.length)

  // the column header row is gone; each cell carries its own label
  expect(m.auditHeaderVisible).toBe(false)
  for (const row of m.auditRows) {
    expect(row.cells).toHaveLength(5)
    for (const cell of row.cells) {
      expect(cell.label).not.toBe('')
      expect(cell.box.left).toBeGreaterThanOrEqual(row.box.left)
      expect(cell.box.right).toBeLessThanOrEqual(m.viewport)
    }
  }
})

test('the new-secret dialog fits a phone screen', async ({ page }, testInfo) => {
  await openSecrets(page)
  await page.locator('main > header').getByRole('button', { name: 'Add New' }).click()

  const dialog = page.locator('dialog')
  await expect(dialog).toBeVisible({ timeout: 15_000 })
  const box = await dialog.boundingBox()
  const m = await measureSecrets(page)
  await attach(page, testInfo, `secrets-new-dialog-${testInfo.project.name}`)

  if (!box) throw new Error('dialog has no bounding box')
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(m.viewport)
  expect(box.y).toBeGreaterThanOrEqual(0)
  expect(box.height).toBeLessThanOrEqual(m.innerHeight)

  // the create action stays reachable inside the dialog
  await expect(dialog.getByRole('button', { name: /Create|Save/ })).toBeVisible()
  const createBox = await dialog.getByRole('button', { name: /Create|Save/ }).boundingBox()
  if (!createBox) throw new Error('create button has no bounding box')
  expect(createBox.x + createBox.width).toBeLessThanOrEqual(m.viewport)
  expect(createBox.y + createBox.height).toBeLessThanOrEqual(m.innerHeight)
  // pinned footer: the action is not clipped by the dialog's own edge
  expect(createBox.y + createBox.height).toBeLessThanOrEqual(box.y + box.height + 1)
  await expect(dialog.getByRole('button', { name: /Create|Save/ })).toBeInViewport()
})
