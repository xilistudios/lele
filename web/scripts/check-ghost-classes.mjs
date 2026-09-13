#!/usr/bin/env node
/**
 * check-ghost-classes.mjs — Gate §1.1: design-token classes that emit no CSS.
 *
 * Reads  dist/*.css  (built bundle) and src/*.tsx + src/*.ts  (markup).
 * Exit 1  if any token-family class in src has no matching selector in CSS.
 * Exit 0  when dead == 0 (current F0 state).
 *
 * Only audits "design-token" families (background, surface, accent, …).
 * Native Tailwind palette (black/white/blue-500) is OUT OF SCOPE.
 *
 * Port of the Python oracle at workspace-planner/…/check-ghost-classes.py.
 * Both must give the same verdict on the same bundle.
 *
 * Usage:
 *   cd web && node scripts/check-ghost-classes.mjs
 */

import { execSync } from 'node:child_process'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// ---------------------------------------------------------------------------
// Design-token families — derived from tailwind.config.js at runtime.
// (FAMILIES and FAM_PREFIX are built inside async main().)
// ---------------------------------------------------------------------------

// Tailwind color utilities
const UTILITIES = [
  'bg',
  'text',
  'border',
  'ring',
  'shadow',
  'outline',
  'divide',
  'fill',
  'stroke',
  'decoration',
  'from',
  'via',
  'to',
  'placeholder',
  'accent',
  'caret',
]

/** Tailwind built-in palette color names — NOT design-token families. */
const TW_PALETTE = new Set([
  'slate',
  'gray',
  'zinc',
  'neutral',
  'stone',
  'red',
  'orange',
  'amber',
  'yellow',
  'lime',
  'green',
  'emerald',
  'teal',
  'cyan',
  'sky',
  'blue',
  'indigo',
  'violet',
  'purple',
  'fuchsia',
  'rose',
  'black',
  'white',
  'inherit',
  'current',
  'transparent',
])

/**
 * Generic Tailwind unescape: `\:` → `:`, `\/` → `/`, etc.
 *   re.sub(r'\\(.)', r'\1', s)  in Python.
 */
function cssUnescape(s) {
  return s.replace(/\\(.)/g, '$1')
}

// ---------------------------------------------------------------------------
// Main (async — config is ESM)
// ---------------------------------------------------------------------------
async function main() {
  const web = process.cwd()
  if (!existsSync(join(web, 'tailwind.config.js'))) {
    console.error('ERROR: run from web/ (expect tailwind.config.js).')
    process.exit(2)
  }

  // ── 0. Derive FAMILIES from tailwind.config.js ──────────────────────
  const scriptDir = dirname(fileURLToPath(import.meta.url))
  const configPath = join(scriptDir, '..', 'tailwind.config.js')
  const { default: cfg } = await import(configPath)
  const FAMILIES = new Set(Object.keys(cfg.theme.extend.colors))

  // Pre-compute "bg-surface", "text-accent", …  (utility + "-" + family)
  const FAM_PREFIX = new Set(UTILITIES.flatMap((u) => [...FAMILIES].map((f) => `${u}-${f}`)))

  // ── 1. Load dist/*.css ──────────────────────────────────────────────
  const cssFiles = readdirSync(join(web, 'dist'))
    .filter((f) => f.endsWith('.css'))
    .sort()
    .map((f) => join(web, 'dist', f))
  if (!cssFiles.length) {
    console.error('No dist/*.css found. Build first.')
    process.exit(2)
  }

  const css = cssFiles.map((p) => readFileSync(p, 'utf8')).join('\n')
  console.log(
    `CSS auditado: ${cssFiles.map((p) => p.split('/').pop()).join(', ')} (${css.length} bytes)`,
  )

  // Set of selectors present in CSS, unescaped, and their bases.
  const rawSet = new Set()
  const selectorRe = /\.((?:[a-zA-Z0-9_-]|\\.)+)/g
  let m = selectorRe.exec(css)
  while (m !== null) {
    rawSet.add(m[1])
    m = selectorRe.exec(css)
  }
  const present = new Set([...rawSet].map(cssUnescape))
  const presentBase = new Set([...present].map((c) => c.split(':').pop()))

  // ── 2. Scan src/*.{ts,tsx} for color utility classes ────────────────
  // Variant prefixes: hover:, group-hover:, data-[...]:, lg:, etc.
  // Captures: (optional-variants)(utility)(-family-rest)(optional/opacity)
  const classRe =
    /(?<!color-)(?:(?::[a-z-]+)*(?:bg|text|border|ring|shadow|outline|divide|fill|stroke|decoration|from|via|to|placeholder|accent|caret)-[a-z0-9-]+(?:\/[0-9]+)?)/g

  let rawMatches
  try {
    rawMatches = execSync(`grep -roPh '${classRe.source}' src --include='*.tsx' --include='*.ts'`, {
      cwd: web,
      encoding: 'utf8',
      maxBuffer: 20 * 1024 * 1024,
    })
      .split('\n')
      .filter(Boolean)
  } catch {
    rawMatches = []
  }

  // Normalise ":bg-x" (variant glued to class) → "bg-x"
  const cnt = new Map()
  for (const raw of rawMatches) {
    const c = raw.replace(/^:+/, '')
    cnt.set(c, (cnt.get(c) || 0) + 1)
  }

  // ── 3. Classify and detect dead classes ─────────────────────────────
  const used = new Map()
  const dead = new Map() // class → ocurrences
  const unknownFamily = new Set() // classes flagged as unknown-family
  for (const [c, n] of cnt) {
    const base = c.split(':').pop()
    const classification = classify(base, FAM_PREFIX, FAMILIES)
    if (classification === 'skip') continue
    used.set(c, (used.get(c) || 0) + n)
    if (!present.has(c) && !presentBase.has(base)) {
      dead.set(c, (dead.get(c) || 0) + n)
      if (classification === 'unknown-family') {
        unknownFamily.add(c)
      }
    }
  }

  // Count affected source files
  let nFiles = '0'
  if (dead.size > 0) {
    try {
      const pattern = [...dead.keys()].map(escapeRegex).join('|')
      nFiles = execSync(`grep -rlP '${pattern}' src --include='*.tsx' --include='*.ts' | wc -l`, {
        cwd: web,
        encoding: 'utf8',
      }).trim()
    } catch {
      nFiles = '0'
    }
  }

  const totalUsed = [...used.values()].reduce((a, b) => a + b, 0)
  const totalDead = [...dead.values()].reduce((a, b) => a + b, 0)
  const pct = totalUsed > 0 ? (100 * totalDead) / totalUsed : 0

  console.log()
  console.log(`clases de token distinct en src : ${used.size}`)
  console.log(`ocurrencias totales             : ${totalUsed}`)
  console.log(`CLASES MUERTAS (distinct)       : ${dead.size}`)
  console.log(`ocurrencias muertas             : ${totalDead}`)
  console.log(`archivos afectados              : ${nFiles}`)
  console.log(`→ ${pct.toFixed(1)}% del markup de color no emite CSS`)

  if (dead.size > 0) {
    console.log('\n--- detalle (por ocurrencias) ---')
    const sorted = [...dead.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    for (const [d, n] of sorted) {
      const label = unknownFamily.has(d) ? ' (unknown-family)' : ''
      console.log(`  ${(d + label).padEnd(56)} ${n}`)
    }
  }

  console.log('\nGate §1.1 del spec: dead debe ser 0 tras aplicar <alpha-value>.')
  process.exit(dead.size > 0 ? 1 : 0)
}

/**
 * Classify a class base as "token", "unknown-family", or "skip".
 *
 * "token"          → family is in config-derived FAMILIES (design token).
 * "unknown-family" → family is NOT in FAMILIES AND NOT in TW_PALETTE,
 *                    AND the utility prefix is NOT ambiguous with English
 *                    text (e.g. "to-flush" in comments, "text-block" as CSS
 *                    property).  Catches mistyped families like bg-bg-tertiary
 *                    (family=bg), bg-doesnotexist-tertiary, etc.
 *                    CSS variable substrings are already excluded by the
 *                    grep lookbehind (?<!color-).
 * "skip"          → everything else: known Tailwind palette colors,
 *                   non-color utilities, or ambiguous utility prefixes
 *                   whose unknown families are likely English text.
 */
function classify(base, famPrefix, families) {
  const noOpacity = base.split('/')[0]
  const segs = noOpacity.split('-')
  if (segs.length < 2) return 'skip'
  const prefix = `${segs[0]}-${segs[1]}`
  if (famPrefix.has(prefix)) return 'token'
  const family = segs[1]
  if (families.has(family)) return 'token'
  if (TW_PALETTE.has(family)) return 'skip'

  // Utility prefixes that commonly overlap with English text or CSS property
  // names.  "to"/"from"/"via" appear in comments ("auto-flush", "from the
  // end"), "text"/"accent" overlap with CSS properties (text-block, etc.).
  // Skip unknown families for these to avoid false positives on clean tree.
  const ambiguous = new Set(['to', 'from', 'via', 'text', 'accent'])
  if (ambiguous.has(segs[0])) return 'skip'

  return 'unknown-family'
}

/** Escape a string for use inside a PCRE/grep regex. */
function escapeRegex(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

main().catch((e) => {
  console.error(e)
  process.exit(2)
})
