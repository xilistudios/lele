#!/usr/bin/env node
/**
 * check-ghost-classes.mjs — Gate §1.1: design-token classes that emit no CSS.
 *
 * Reads  dist/*.css  (built bundle) and src/*.{ts,tsx} + index.html (markup).
 * Exit 1  if any token-family class in src has no matching selector in CSS.
 * Exit 0  when dead == 0 (current F0 state).
 * Exit 2  on infrastructure error (rg crash, missing dist, etc.).
 *
 * Only audits "design-token" families (background, surface, accent, …).
 * Native Tailwind palette (black/white/blue-500) is OUT OF SCOPE.
 *
 * Port of the Python oracle at workspace-planner/…/check-ghost-classes.py.
 * Both must give the same verdict on the same bundle.
 *
 * Hardening (T0.9 — Option A):
 *   FN-1: Ambiguous-prefix classes (text-, accent-, from-, to-, via-) and
 *         unknown-family classes are only flagged when found inside a
 *         className attribute context.  Full-file scan is preserved for
 *         known token-family classes (no false-positive risk).
 *   FN-2: ripgrep exit-code discrimination — exit 1 (no matches) → valid
 *         empty; any other failure → stderr + process.exit(2).
 *   FN-3: index.html is now included in the scanned files (HTML pure,
 *         same className-context logic as ambiguous classes).
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

/** Utility prefixes that commonly overlap with English text or CSS properties. */
const AMBIGUOUS = new Set(['to', 'from', 'via', 'text', 'accent'])

/**
 * Generic Tailwind unescape: `\:` → `:`, `\/` → `/`, etc.
 *   re.sub(r'\\(.)', r'\1', s)  in Python.
 */
function cssUnescape(s) {
  return s.replace(/\\(.)/g, '$1')
}

// ---------------------------------------------------------------------------
// Class scanning helpers
// ---------------------------------------------------------------------------

/**
 * Regex to match Tailwind color utility classes in source text.
 * Captures: (optional-variants)(utility)(-family-rest)(optional/opacity)
 * Lookbehind `(?<!color-)` prevents matching CSS variable substrings
 * like `--color-bg-primary` (load-bearing: without it, 2 false positives).
 */
const classRe =
  /(?<!color-)(?:(?::[a-z-]+)*(?:bg|text|border|ring|shadow|outline|divide|fill|stroke|decoration|from|via|to|placeholder|accent|caret)-[a-z0-9-]+(?:\/[0-9]+)?)/g

/**
 * Regex to match className attribute values in source text.
 * Handles: className="...", className={'...'}, className={`...`}
 * Also matches plain class="..." for HTML (index.html).
 */
const attrRe = /(?:className|class)\s*=\s*(?:"([^"]*)"|'([^']*)'|`([^`]*)`)/g

/**
 * Extract all Tailwind color classes that appear inside a className/class
 * attribute context from the given source string.
 */
function classContextStrings(src) {
  const out = new Set()
  for (const a of src.matchAll(attrRe)) {
    const val = a[1] ?? a[2] ?? a[3] ?? ''
    for (const mm of val.matchAll(classRe)) {
      out.add(mm[0].replace(/^:+/, ''))
    }
  }
  return out
}

/**
 * Scan a source file: extract all Tailwind color utility classes (full-file
 * scan) AND track which ones appear inside className contexts.
 *
 * @returns {{ cnt: Map<string,number>, inClassCtx: Set<string> }}
 */
function scanFile(filePath) {
  const src = readFileSync(filePath, 'utf8')
  const inClassCtx = classContextStrings(src)
  const cnt = new Map()

  for (const mm of src.matchAll(classRe)) {
    const c = mm[0].replace(/^:+/, '')
    cnt.set(c, (cnt.get(c) || 0) + 1)
  }

  return { cnt, inClassCtx }
}

/**
 * Run ripgrep to find all Tailwind color utility classes in src/ files.
 * Returns the raw match list, or throws on infrastructure failure.
 * Uses rg exit code to distinguish "no matches" (exit 1) from real errors.
 */
function rgScan(web, pattern) {
  try {
    const out = execSync(
      `grep -roPh '${pattern}' src --include='*.tsx' --include='*.ts'`,
      {
        cwd: web,
        encoding: 'utf8',
        maxBuffer: 20 * 1024 * 1024,
      },
    )
    return out.split('\n').filter(Boolean)
  } catch (err) {
    // grep exits 1 when no matches found — that's valid, return empty.
    if (err.status === 1) return []
    // Any other failure (rg not installed, permission error, signal, etc.)
    // is an infrastructure error — fail closed.
    throw err
  }
}

/**
 * Fallback regex scan when rg/grep is not available.
 * Reads src/ files directly and runs the class regex.
 */
function fallbackScan(web) {
  const srcDir = join(web, 'src')
  if (!existsSync(srcDir)) {
    throw new Error(`src/ directory not found at ${srcDir}`)
  }
  const files = execSync(`find src -name '*.tsx' -o -name '*.ts'`, {
    cwd: web,
    encoding: 'utf8',
  })
    .trim()
    .split('\n')
    .filter(Boolean)

  const matches = []
  for (const f of files) {
    const src = readFileSync(join(web, f), 'utf8')
    for (const mm of src.matchAll(classRe)) {
      matches.push(mm[0])
    }
  }
  return matches
}

// ---------------------------------------------------------------------------
// Classify
// ---------------------------------------------------------------------------

/**
 * Classify a class base as "token", "ambiguous-needs-ctx", or "skip".
 *
 * "token"               → family is in config-derived FAMILIES (design token).
 * "ambiguous-needs-ctx" → utility prefix is ambiguous (text-, accent-, from-,
 *                          to-, via-) OR family is unknown (not in FAMILIES
 *                          and not in TW_PALETTE).  These classes are only
 *                          flagged if they appear inside a className context.
 * "skip"                → known Tailwind palette colors, non-color utilities.
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
  return 'ambiguous-needs-ctx'
}

/** Escape a string for use inside a PCRE/grep regex. */
function escapeRegex(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// ---------------------------------------------------------------------------
// Main
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
  const FAM_PREFIX = new Set(
    UTILITIES.flatMap((u) => [...FAMILIES].map((f) => `${u}-${f}`)),
  )

  // ── 1. Load dist/*.css ──────────────────────────────────────────────
  const cssFiles = readdirSync(join(web, 'dist'))
    .filter((f) => f.endsWith('.css'))
    .sort()
    .map((f) => join(web, 'dist', f))
  if (!cssFiles.length) {
    console.error('ERROR: No dist/*.css found. Build first.')
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
  //   Two paths:
  //   a) rg/grep for fast full-file scan (produces rawMatches).
  //   b) fs-based scan for className-context tracking (per file).
  //
  // Token-family classes use the full-file scan (a) — no false-positive risk.
  // Ambiguous-prefix + unknown-family classes require className context (b).

  // Path (a): rg/grep full-file scan
  let rawMatches
  try {
    rawMatches = rgScan(web, classRe.source)
  } catch (rgErr) {
    // rg not installed or crashed — try fs fallback
    console.warn(
      `WARN: ripgrep/grep failed (${rgErr.message}); falling back to fs scan.`,
    )
    try {
      rawMatches = fallbackScan(web)
    } catch (fbErr) {
      console.error(
        `ERROR: fallback scan also failed: ${fbErr.message}`,
      )
      process.exit(2)
    }
  }

  // Path (b): fs-based scan for className-context tracking
  const srcFiles = execSync(`find src -name '*.tsx' -o -name '*.ts'`, {
    cwd: web,
    encoding: 'utf8',
  })
    .trim()
    .split('\n')
    .filter(Boolean)

  const inClassCtx = new Set()
  const fsCnt = new Map()

  for (const f of srcFiles) {
    const { cnt: fileCnt, inClassCtx: fileCtx } = scanFile(join(web, f))
    for (const c of fileCtx) inClassCtx.add(c)
    for (const [c, n] of fileCnt) {
      fsCnt.set(c, (fsCnt.get(c) || 0) + n)
    }
  }

  // ── 2b. Scan index.html (FN-3) ─────────────────────────────────────
  //   HTML is pure markup — all classes must be in className/class context.
  const indexHtml = join(web, 'index.html')
  if (existsSync(indexHtml)) {
    const { cnt: htmlCnt, inClassCtx: htmlCtx } = scanFile(indexHtml)
    for (const c of htmlCtx) inClassCtx.add(c)
    for (const [c, n] of htmlCnt) {
      fsCnt.set(c, (fsCnt.get(c) || 0) + n)
    }
  }

  // Merge rg matches into cnt (rg may find classes in comments/strings
  // that fs scan also finds — dedup via Map).
  const cnt = new Map()
  for (const raw of rawMatches) {
    const c = raw.replace(/^:+/, '')
    cnt.set(c, (cnt.get(c) || 0) + 1)
  }
  // Ensure fsCnt entries are also in cnt (index.html may have classes
  // not in .tsx/.ts files).
  for (const [c, n] of fsCnt) {
    cnt.set(c, (cnt.get(c) || 0) + n)
  }

  // ── 3. Classify and detect dead classes ─────────────────────────────
  const used = new Map()
  const dead = new Map()
  const unknownFamily = new Set()

  for (const [c, n] of cnt) {
    const base = c.split(':').pop()
    const cl = classify(base, FAM_PREFIX, FAMILIES)
    if (cl === 'skip') continue

    // Token families: flag always (full-file scan — no false-positive risk)
    // Ambiguous/unknown: only flag if in className context
    if (cl === 'ambiguous-needs-ctx' && !inClassCtx.has(c)) continue

    used.set(c, (used.get(c) || 0) + n)
    if (!present.has(c) && !presentBase.has(base)) {
      dead.set(c, (dead.get(c) || 0) + n)
      if (cl === 'ambiguous-needs-ctx') {
        unknownFamily.add(c)
      }
    }
  }

  // Count affected source files
  let nFiles = '0'
  if (dead.size > 0) {
    try {
      const pattern = [...dead.keys()].map(escapeRegex).join('|')
      nFiles = execSync(
        `grep -rlP '${pattern}' src --include='*.tsx' --include='*.ts' | wc -l`,
        { cwd: web, encoding: 'utf8' },
      ).trim()
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
    const sorted = [...dead.entries()].sort(
      (a, b) => b[1] - a[1] || a[0].localeCompare(b[0]),
    )
    for (const [d, n] of sorted) {
      const label = unknownFamily.has(d) ? ' (unknown-family)' : ''
      console.log(`  ${(d + label).padEnd(56)} ${n}`)
    }
  }

  console.log(
    '\nGate §1.1 del spec: dead debe ser 0 tras aplicar <alpha-value>.',
  )
  process.exit(dead.size > 0 ? 1 : 0)
}

main().catch((e) => {
  console.error(e)
  process.exit(2)
})
