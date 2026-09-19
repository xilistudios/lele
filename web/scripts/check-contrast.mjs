#!/usr/bin/env node
/**
 * check-contrast.mjs — WCAG 2.1 contrast matrix (spec §2.7)
 *
 * Reads color token values from src/styles/index.css and verifies
 * every mandatory foreground/background pair defined in the matrix.
 *
 * Usage:
 *   node scripts/check-contrast.mjs              # report mode (exit 0)
 *   node scripts/check-contrast.mjs --strict      # exit 1 on failures
 *   node scripts/check-contrast.mjs --self-test   # verify checker logic
 *   node scripts/check-contrast.mjs --css <path>  # custom CSS path
 */

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// ── CLI args ───────────────────────────────────────────────────────
const args = process.argv.slice(2)
let cssPath = resolve('src/styles/index.css')
let strict = false
let selfTest = false

for (let i = 0; i < args.length; i++) {
  if (args[i] === '--css' && args[i + 1]) {
    cssPath = resolve(args[++i])
  } else if (args[i] === '--strict') {
    strict = true
  } else if (args[i] === '--self-test') {
    selfTest = true
  }
}

// ── Token parser ───────────────────────────────────────────────────
/**
 * @typedef {{ r: number, g: number, b: number, a: number }} ParsedColor
 */

const reDecl = /--color-([a-zA-Z0-9-]+)\s*:\s*([^;]+);/g
const reRgb = /rgb\(\s*(\d{1,3})\s+(\d{1,3})\s+(\d{1,3})\s*(?:\/\s*([\d.]+))?\s*\)/

/**
 * Strip comments from raw CSS text.
 */
function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

/**
 * Parse a single CSS value into { r, g, b, a }.
 * Handles "R G B" (alpha=1) and "rgb(R G B)" / "rgb(R G B / A)" forms.
 * @returns {ParsedColor|null}
 */
function parseColorValue(val) {
  const v = val.trim()
  const rgbMatch = v.match(reRgb)
  if (rgbMatch) {
    return {
      r: Number.parseInt(rgbMatch[1], 10),
      g: Number.parseInt(rgbMatch[2], 10),
      b: Number.parseInt(rgbMatch[3], 10),
      a: rgbMatch[4] !== undefined ? Number.parseFloat(rgbMatch[4]) : 1,
    }
  }
  const parts = v.split(/\s+/)
  if (parts.length === 3) {
    const nums = parts.map(Number)
    if (nums.every((n) => !Number.isNaN(n) && n >= 0 && n <= 255)) {
      return { r: nums[0], g: nums[1], b: nums[2], a: 1 }
    }
  }
  return null
}

/**
 * Extract a theme block by regex, returning the matched content.
 */
function extractBlock(css, pattern) {
  const re = new RegExp(pattern.source, pattern.flags)
  const match = re.exec(css)
  if (!match) return ''
  return match[1] ?? match[0]
}

/**
 * Extract all color tokens from a CSS block string.
 * Returns Map<string, { r, g, b, a }>.
 */
function extractTokens(block) {
  const map = new Map()
  const stripped = stripComments(block)
  reDecl.lastIndex = 0
  for (;;) {
    const m = reDecl.exec(stripped)
    if (m === null) break
    const parsed = parseColorValue(m[2])
    if (parsed) {
      map.set(m[1], parsed)
    }
  }
  return map
}

/**
 * Parse the full CSS and return { dark, light } token maps.
 *
 * Block detection:
 *   dark  = first :root { ... } block
 *   light = union of [data-theme="light"] { ... } and
 *           @media (prefers-color-scheme: light) { :root:not([data-theme="dark"]) { ... } }
 *
 * If a token differs between the two light sub-blocks, an error is pushed
 * to the `errors` array.
 *
 * @returns {{ dark: Map, light: Map, errors: string[] }}
 */
function parseThemes(raw) {
  const errors = []
  const stripped = stripComments(raw)

  // 1. Dark: first :root block
  const darkMatch = raw.match(/^:root\s*\{([\s\S]*?)\n\}/m)
  const dark = darkMatch ? extractTokens(darkMatch[1]) : new Map()

  // 2a. [data-theme="light"] block
  const dtMatch = raw.match(/\[data-theme\s*=\s*"light"\]\s*\{([\s\S]*?)\n\}/)
  const dtTokens = dtMatch ? extractTokens(dtMatch[1]) : new Map()

  // 2b. @media (prefers-color-scheme: light) { :root:not([data-theme="dark"]) { ... } }
  const mqMatch = raw.match(
    /@media\s*\(prefers-color-scheme:\s*light\)\s*\{\s*:root:not\(\[data-theme="dark"\]\)\s*\{([\s\S]*?)\n\s*\}/,
  )
  const mqTokens = mqMatch ? extractTokens(mqMatch[1]) : new Map()

  // 2c. Merge: check for divergence between the two light sub-blocks
  const light = new Map([...dtTokens])
  for (const [name, color] of mqTokens) {
    const existing = light.get(name)
    if (existing) {
      if (
        existing.r !== color.r ||
        existing.g !== color.g ||
        existing.b !== color.b ||
        Math.abs(existing.a - color.a) > 0.001
      ) {
        errors.push(
          `DIVERGENT light token --color-${name}: ` +
            `data-theme=${existing.r} ${existing.g} ${existing.b} a=${existing.a} vs ` +
            `prefers-color-scheme=${color.r} ${color.g} ${color.b} a=${color.a}`,
        )
      }
    } else {
      light.set(name, color)
    }
  }

  return { dark, light, errors }
}

// ── WCAG 2.1 contrast ─────────────────────────────────────────────
function linearize(c) {
  const s = c / 255
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

function luminance({ r, g, b }) {
  return 0.2126 * linearize(r) + 0.7152 * linearize(g) + 0.0722 * linearize(b)
}

/**
 * Contrast ratio between two opaque colors.
 * @returns {number} ratio ≥ 1
 */
function contrastRatio(fg, bg) {
  const l1 = luminance(fg)
  const l2 = luminance(bg)
  const lighter = Math.max(l1, l2)
  const darker = Math.min(l1, l2)
  return (lighter + 0.05) / (darker + 0.05)
}

/**
 * Composite a semi-transparent foreground over an opaque background.
 * Formula: result = fg * alpha + bg * (1 - alpha)
 */
function blend(fg, bg) {
  const a = fg.a
  return {
    r: Math.round(fg.r * a + bg.r * (1 - a)),
    g: Math.round(fg.g * a + bg.g * (1 - a)),
    b: Math.round(fg.b * a + bg.b * (1 - a)),
    a: 1,
  }
}

/**
 * Effective contrast: if fg has alpha < 1, composite it over the
 * cell's own background before measuring.
 *
 * @param {ParsedColor} fg        foreground token (may have alpha)
 * @param {ParsedColor} bg        background token to measure against (and to blend into)
 * @returns {number} contrast ratio
 */
function effectiveRatio(fg, bg) {
  const effFg = fg.a < 1 ? blend(fg, bg) : fg
  return contrastRatio(effFg, bg)
}

// ── Matrix (spec §2.7) ────────────────────────────────────────────
const BG_TOKENS = {
  L0: 'bg-primary',
  L1: 'bg-secondary',
  L2: 'bg-tertiary',
  L3: 'bg-elevated',
  hover: 'surface-hover',
  sel: 'surface-selected',
  tint: 'accent-tint',
}

/**
 * Spec §2.7 matrix: background references use BG_TOKENS keys.
 * `blendBg` specifies which BG_TOKENS entry to use for alpha compositing.
 */
function buildMatrix() {
  const L0L2 = ['L0', 'L1', 'L2']
  const L3HovSelTint = ['L3', 'hover', 'sel', 'tint']
  const allBg = ['L0', 'L1', 'L2', 'L3', 'hover', 'sel', 'tint']
  const L0L1L2L3 = ['L0', 'L1', 'L2', 'L3']

  const rows = []

  // Helper: add rows for a set of foregrounds against a set of backgrounds
  const addRows = (fgs, bgs, min) => {
    for (const fg of fgs) {
      rows.push({ fg, bgs, min })
    }
  }

  // Normal text (min 4.5) over L0..L2
  addRows(
    [
      'text-primary',
      'text-secondary',
      'state-success',
      'state-warning',
      'state-error',
      'state-info',
    ],
    L0L2,
    4.5,
  )

  // text-tertiary: min 4.5 over L0..L2
  addRows(['text-tertiary'], L0L2, 4.5)

  // Large text (min 3.0) over L3/hover/sel/tint
  addRows(['text-tertiary', 'text-muted'], L3HovSelTint, 3.0)
  addRows(['accent-text'], L3HovSelTint, 3.0)

  // on-accent (min 4.5): over accent-primary and state-error-fill
  rows.push({
    fg: 'text-on-accent',
    bgs: ['accent-primary', 'state-error-fill'],
    min: 4.5,
  })

  // ── §2.4 mode identity, roles reales de uso ──
  // chip: text-mode-X sobre bg-mode-X/10 en L1 (header/sidebar)
  // selectedItem: text-mode-X sobre bg-mode-X/14 en L1 (lista de sesiones)
  // tabActive: text-mode-X + tinte/10 sobre L2 (track bg-background-tertiary)
  // exceptions: la regla §2.4 (group-claro) extendida por F1 — cuando el texto de
  // color no alcanza 4.5 sobre su propio tinte, el claro usa text-primary y el color
  // queda solo en dot/borde. Se verifica text-primary sobre ese fondo en su lugar.
  rows.push({ fg: 'mode-chat', bgs: ['L1', 'chat-tint10-L1'], min: 4.5 })
  rows.push({ fg: 'mode-agent', bgs: ['L1', 'agent-tint10-L1', 'agent-tint14-L1'], min: 4.5 })
  // tabActive on L2: tint/10 over track layer; §2.4 exception — mode color fails AA on
  // its own tint in light (chat 4.20, agent 4.33, group 3.95) and agent in dark (4.39),
  // so text-primary unificado (10.10–12.95 all pass). Border-underline carries color.
  rows.push({
    fg: 'text-primary',
    bgs: ['chat-tint10-L2', 'agent-tint10-L2', 'group-tint10-L2'],
    min: 4.5, // §2.4: text-primary unificado sobre tinte/10 en L2
  })
  // Gate for the actual identity-color paint (MAJOR-1): chat/group pass in dark but
  // fail in light → only:['dark']. agent fails BOTH themes (4.39 D / 4.33 L) so no
  // passing row can be added; that failure is the reason agent has no dark: variant.
  rows.push({ fg: 'mode-chat', bgs: ['chat-tint10-L2'], min: 4.5, only: ['dark'] })
  rows.push({ fg: 'mode-group', bgs: ['group-tint10-L2'], min: 4.5, only: ['dark'] })
  // group: chip/selectedItem usan texto de color solo en oscuro; claro usa text-primary
  // N1 fix: L1 plano se verifica en AMBOS temas (claro 5.178 ✅). Antes estaba empaquetado
  // con los tintes bajo only:['dark'] y la celda L1 del claro nunca se medía.
  rows.push({ fg: 'mode-group', bgs: ['L1'], min: 4.5 })
  rows.push({ fg: 'mode-group', bgs: ['group-tint10-L1', 'group-tint14-L1'], min: 4.5, only: ['dark'] })
  // N1: MessageList pinta el badge de estado (softBg/10 + texto) DENTRO de la card de
  // grupo, que ya pinta softBg/10 → tinte doble. Peor caso real; sin esta fila el gate
  // daba verde con 3.91 ❌ en claro.
  rows.push({
    fg: 'mode-group',
    bgs: ['group-tint10-on-group-tint10-L1'],
    min: 4.5,
    only: ['dark'], // oscuro: color de identidad sobre tinte doble (4.92 ✅)
  })
  rows.push({
    fg: 'text-primary',
    bgs: ['group-tint10-on-group-tint10-L1', 'group-tint10-on-group-tint14-L1'],
    min: 4.5, // claro: excepción §2.4 sobre tinte doble (12.78 ✅)
  })
  // chat selectedItem: color solo en oscuro (claro 4.498 → text-primary)
  rows.push({ fg: 'mode-chat', bgs: ['chat-tint14-L1'], min: 4.5, only: ['dark'] })
  // excepciones claras (texto primary sobre tinte de modo)
  rows.push({ fg: 'text-primary', bgs: ['chat-tint14-L1', 'group-tint10-L1', 'group-tint14-L1'], min: 4.5 })

  // focus indicator (min 3.0): over all backgrounds
  rows.push({
    fg: 'focus',
    bgs: allBg,
    min: 3.0,
  })

  // border-strong (min 3.0): over L0..L3
  rows.push({
    fg: 'border-strong',
    bgs: L0L1L2L3,
    min: 3.0,
  })

  return rows
}

// ── Formatting helpers ─────────────────────────────────────────────
function fmtCell(ratio, min) {
  if (ratio >= min) return `  ${ratio.toFixed(2)} ✅`
  if (min <= 3.0 && ratio >= 3.0) return `  ${ratio.toFixed(2)} ⚠️Lg`
  return ` ${ratio.toFixed(2)} ❌ `
}

function fmtRatio(ratio) {
  return ratio.toFixed(2)
}

function fmtColor(c) {
  return `${c.r} ${c.g} ${c.b}${c.a < 1 ? ` a=${c.a}` : ''}`
}

/**
 * Resolve a color token by name. Missing tokens must be declared in CSS;
 * silent aliasing is forbidden (exit 2 on absence).
 * @returns {ParsedColor|undefined}
 */
function resolveColor(tokens, name) {
  return tokens.get(name)
}

// ── Self-test ──────────────────────────────────────────────────────
/**
 * Validate that the checker correctly detects known-good, known-bad,
 * and alpha-blending behavior. Does NOT modify global state; works on
 * cloned token maps.
 *
 * @returns {boolean} true if all checks pass
 */
function selfTestCheck(raw, matrix) {
  console.log('── self-test ──────────────────────────────────────')
  let passed = 0
  let total = 0
  let allOk = true

  const { dark, light } = parseThemes(raw)

  // We'll mutate copies for each test, then restore.
  const origMuted = dark.get('text-muted')

  // ── Test 1: Corrupt text-muted dark → must be ❌ on L3 and hover ──
  total++
  dark.set('text-muted', { r: 110, g: 116, b: 124, a: 1 })
  const l0Color = dark.get('bg-primary')
  const l3Color = dark.get('bg-elevated')
  const hoverColor = dark.get('surface-hover')
  const mutedNew = dark.get('text-muted')
  const rMutedL3 = effectiveRatio(mutedNew, l3Color)
  const rMutedHover = effectiveRatio(mutedNew, hoverColor)
  const failsL3 = rMutedL3 < 3.0
  const failsHover = rMutedHover < 3.0
  const t1ok = failsL3 && failsHover
  console.log(
    `  [1] corrupt text-muted(110 116 124) dark: L3=${rMutedL3.toFixed(2)} ${failsL3 ? '❌' : '✅'} hover=${rMutedHover.toFixed(2)} ${failsHover ? '❌' : '✅'} → ${t1ok ? 'PASS' : 'FAIL'}`,
  )
  if (t1ok) passed++
  else allOk = false

  // Restore
  if (origMuted) dark.set('text-muted', origMuted)

  // ── Test 2: overlay dark alpha-compositing vs raw triplet ─────────
  total++
  const ov = dark.get('overlay')
  const bgElev = dark.get('bg-elevated')
  if (ov && ov.a < 1) {
    const ratioComposite = effectiveRatio(ov, bgElev)
    const ratioRaw = contrastRatio(ov, bgElev)
    const differs = Math.abs(ratioComposite - ratioRaw) > 0.01
    const t2ok = differs
    console.log(
      `  [2] overlay dark composite=${ratioComposite.toFixed(2)} vs raw=${ratioRaw.toFixed(2)} (differs=${differs}) → ${t2ok ? 'PASS' : 'FAIL'}`,
    )
    if (t2ok) passed++
    else allOk = false
  } else {
    console.log('  [2] SKIP: overlay missing or has no alpha (a == 1)')
    allOk = false
  }

  // ── Test 3: text-primary dark on L0 must pass (ratio > 10) ─────
  total++
  const tpDark = dark.get('text-primary')
  const bgDark = dark.get('bg-primary')
  const rTp = contrastRatio(tpDark, bgDark)
  const t3ok = rTp > 10
  console.log(
    `  [3] text-primary dark / L0 = ${rTp.toFixed(2)} (expected >10) → ${t3ok ? 'PASS' : 'FAIL'}`,
  )
  if (t3ok) passed++
  else allOk = false

  // ── Test 4: on-accent / accent-primary dark must PASS post-F1j ──
  total++
  const onAccent = dark.get('text-on-accent')
  const accentBg = dark.get('accent-primary')
  if (onAccent && accentBg) {
    const rOnAccent = contrastRatio(onAccent, accentBg)
    const t4ok = rOnAccent >= 4.5
    console.log(
      `  [4] on-accent / accent-primary dark = ${rOnAccent.toFixed(2)} (must be >=4.5 post-F1j) → ${t4ok ? 'PASS' : 'FAIL'}`,
    )
    if (t4ok) passed++
    else allOk = false
  } else {
    console.log('  [4] SKIP: missing text-on-accent or accent-primary')
    allOk = false
  }

  console.log(`\nself-test: ${total} checks, ${passed} passed`)
  return allOk
}

// ── Main ───────────────────────────────────────────────────────────
function main() {
  let raw
  try {
    raw = readFileSync(cssPath, 'utf-8')
  } catch (err) {
    console.error(`ERROR: Cannot read CSS file: ${cssPath}`)
    console.error(`  ${err.message}`)
    process.exit(2)
  }

  const { dark, light, errors: parseErrors } = parseThemes(raw)
  const matrix = buildMatrix()

  // Self-test mode: run checks before the main report
  if (selfTest) {
    const ok = selfTestCheck(raw, matrix)
    if (!ok) {
      console.error('\nself-test FAILED — exiting 1')
      process.exit(1)
    }
  }

  let totalPairs = 0
  let totalFail = 0
  let totalExempt = 0
  let hasMissing = false
  const allFailures = []

  for (const [themeName, tokens] of [
    ['DARK', dark],
    ['LIGHT', light],
  ]) {
    // Resolve background token colors
    const bg = {}
    const bgMissing = []
    for (const [label, name] of Object.entries(BG_TOKENS)) {
      const c = tokens.get(name)
      if (c) {
        bg[label] = c
      } else {
        bgMissing.push(name)
      }
    }
    // Defect 3: resolve direct token names used as row bgs (e.g. accent-primary, state-error-fill)
    for (const row of matrix) {
      for (const bgLabel of row.bgs) {
        if (!bg[bgLabel]) {
          // §2.4 synthetic: "<mode>-tint<alpha>-<level>" = mode color at alpha over level bg
          const mt = bgLabel.match(/^(chat|agent|group)-tint(\d+)-(L[0-3])$/)
          // §2.4 synthetic (N1): "<mode>-tint<alpha>-on-<mode>-tint<alpha>-<level>" =
          // tinte anidado dentro de tinte (MessageList: badge softBg/10 dentro de una
          // card que ya pinta softBg/10). Sin esta forma el gate no veía el peor caso.
          const mn = bgLabel.match(/^(chat|agent|group)-tint(\d+)-on-(chat|agent|group)-tint(\d+)-(L[0-3])$/)
          if (mn) {
            const outer = tokens.get(`mode-${mn[1]}`)
            const inner = tokens.get(`mode-${mn[3]}`)
            const levelTok = tokens.get(BG_TOKENS[mn[5]])
            if (outer && inner && levelTok) {
              const innerBg = blend({ ...inner, a: Number(mn[4]) / 100 }, levelTok)
              bg[bgLabel] = blend({ ...outer, a: Number(mn[2]) / 100 }, innerBg)
            } else {
              bgMissing.push(`mode tint tokens for ${bgLabel}`)
            }
          } else if (mt) {
            const modeTok = tokens.get(`mode-${mt[1]}`)
            const levelTok = tokens.get(BG_TOKENS[mt[3]])
            if (modeTok && levelTok) {
              bg[bgLabel] = blend({ ...modeTok, a: Number(mt[2]) / 100 }, levelTok)
            } else {
              bgMissing.push(`--color-mode-${mt[1]} or ${BG_TOKENS[mt[3]]} (for ${bgLabel})`)
            }
          } else {
            const direct = tokens.get(bgLabel)
            if (direct) {
              bg[bgLabel] = direct
            } else {
              bgMissing.push(`--color-${bgLabel} (used as bg for --color-${row.fg})`)
            }
          }
        }
      }
    }
    if (bgMissing.length > 0) {
      console.error(`ERROR [${themeName}]: missing background tokens: ${bgMissing.join(', ')}`)
      hasMissing = true
      continue
    }

    // Check all foreground tokens exist — missing = exit 2 (no silent aliases)
    const fgMissing = []
    for (const row of matrix) {
      if (!tokens.has(row.fg)) {
        fgMissing.push(row.fg)
      }
    }
    if (fgMissing.length > 0) {
      console.error(
        `ERROR [${themeName}]: missing foreground tokens: ${fgMissing.map((n) => `--color-${n}`).join(', ')}`,
      )
      hasMissing = true
      continue
    }

    // ── Header ────────────────────────────────────────────────────
    const baseBgLabels = ['L0', 'L1', 'L2', 'L3', 'hover', 'sel', 'tint']
    // Extend with any extra bg labels used by matrix rows (e.g. accent-primary, state-error-fill)
    const extraBgLabels = []
    for (const row of matrix) {
      for (const bgLabel of row.bgs) {
        if (!baseBgLabels.includes(bgLabel) && !extraBgLabels.includes(bgLabel)) {
          extraBgLabels.push(bgLabel)
        }
      }
    }
    const bgLabels = [...baseBgLabels, ...extraBgLabels]
    const bgHeader = bgLabels.map((l) => l.padStart(8)).join(' │')
    console.log(`\n═══ ${themeName} ═══`)
    console.log(`foreground       │${bgHeader}`)
    console.log(
      `${'─'.repeat(17)}─┼${'─'
        .repeat(9)
        .repeat(bgLabels.length)
        .replace(/(.{9})/g, '$1┼')
        .slice(0, -1)}`,
    )

    // ── Build quick-lookup for this theme's requirements ──────────
    // fg → { bgLabel → { min, exempt } }
    // Rows may carry `only: ['dark'|'light']` to scope an exception per theme.
    const req = new Map()
    for (const row of matrix) {
      if (row.only && !row.only.includes(themeName.toLowerCase())) continue
      for (const bgLabel of row.bgs) {
        if (!req.has(row.fg)) req.set(row.fg, new Map())
        req.get(row.fg).set(bgLabel, { min: row.min })
      }
    }

    // border-default: exempt
    req.set('border-default', new Map())

    // ── Report ────────────────────────────────────────────────────
    const allFg = [
      'text-primary',
      'text-secondary',
      'text-tertiary',
      'text-muted',
      'accent-text',
      'accent-primary',
      'state-success',
      'state-warning',
      'state-error',
      'state-info',
      'text-on-accent',
      'focus',
      'border-strong',
      'border-default',
      'mode-chat',
      'mode-agent',
      'mode-group',
    ]

    for (const fgName of allFg) {
      const fg = resolveColor(tokens, fgName)
      if (!fg) continue

      const cells = []
      for (const bgLabel of bgLabels) {
        const bgc = bg[bgLabel]

        // Check if this fg/bg pair is in the matrix
        const fgReqs = req.get(fgName)
        const pairReq = fgReqs?.get(bgLabel)

        if (!pairReq) {
          // Not in matrix — skip (don't count)
          cells.push('      ·  ')
          continue
        }

        if (fgName === 'border-default') {
          // Exempt
          const r = effectiveRatio(fg, bgc)
          cells.push(`  ${r.toFixed(2)} 📋`)
          totalExempt++
          continue
        }

        // Defect 1 fix: alpha-composite fg against the cell's OWN background
        const ratio = effectiveRatio(fg, bgc)
        totalPairs++
        const pass = ratio >= pairReq.min
        if (!pass) {
          totalFail++
          allFailures.push({
            theme: themeName,
            fg: fgName,
            bg: bgLabel,
            ratio,
            min: pairReq.min,
          })
        }
        cells.push(fmtCell(ratio, pairReq.min))
      }

      // Pad foreground name
      const label = `--${fgName}`.padEnd(17)
      // Color value
      const colorStr = fmtColor(fg).padEnd(20)
      console.log(`${label} ${colorStr}│${cells.join('│')}`)
    }
  }

  // ── border-default info line ────────────────────────────────────
  for (const [themeName, tokens] of [
    ['DARK', dark],
    ['LIGHT', light],
  ]) {
    const bd = tokens.get('border-default')
    if (bd) {
      const bgL0 = tokens.get('bg-primary')
      if (bgL0) {
        const ratio = effectiveRatio(bd, bgL0)
        console.log(
          `\n[${themeName}] border-default / L0 = ${ratio.toFixed(2)} (exempt — decorative)`,
        )
      }
    }
  }

  // ── Summary ─────────────────────────────────────────────────────
  console.log(`\npairs verified: ${totalPairs} · failures: ${totalFail} · exempt: ${totalExempt}`)

  if (parseErrors.length > 0) {
    console.error(`\nPARSE ERRORS (${parseErrors.length}):`)
    for (const e of parseErrors) console.error(`  ❌ ${e}`)
    process.exit(2)
  }

  if (hasMissing) {
    console.error('\nMissing tokens — cannot complete matrix.')
    process.exit(2)
  }

  if (allFailures.length > 0) {
    console.log(`\nFAILURES (${allFailures.length}):`)
    for (const f of allFailures) {
      console.log(
        `  ❌ ${f.theme} — --color-${f.fg} / ${f.bg}: ${f.ratio.toFixed(2)} (min ${f.min})`,
      )
    }
  }

  // B2 fix (spec §7.2 #6): reportear un fallo y salir a 0 no es un verificador.
  // El gate sale 1 por defecto cuando hay fallos; --strict queda como alias
  // histórico. Parse/missing errors ya salen 2 arriba.
  if (totalFail > 0) {
    process.exit(1)
  }
}

main()
