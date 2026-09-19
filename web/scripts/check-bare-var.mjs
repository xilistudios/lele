#!/usr/bin/env node
/**
 * check-bare-var.mjs — Gate F2: no bare var(--color-*) as raw color values.
 *
 * Scans every web/src ts/tsx file and flags occurrences of
 * var(--color-<name>) used as a raw CSS/SVG color value WITHOUT being
 * wrapped in a color function (rgb, rgba, hsl, hsla, color).
 *
 * Background: design-system color tokens are channel triplets
 * (e.g. "255 255 255"). They are only valid when consumed as
 * rgb(var(--color-X)). Using the bare var(--color-X) yields an invalid
 * CSS color string; the browser falls back to the initial value (none
 * for stroke), so the element silently paints nothing.
 *
 * This defect shipped in 3bbf58b (design-system migration F1):
 * PlusCircleIcon got stroke="var(--color-text-on-accent)" and the
 * "New chat" icon rendered as a blank filled disc in production.
 *
 * Strategy (inverted default):
 *   FLAG every var(--color-*) unless it is:
 *     (a) wrapped in a color function — rgb/rgba/hsl/hsla/lab/lch/color()
 *     (b) inside a comment            — // or block-comment (slash-star … star-slash)
 *     (c) inside a test-file assertion — .toBe(), .toEqual(), etc. (test files only)
 *     (d) a non-triplet token          — declared as a full color in CSS, not a channel triplet
 *
 * This makes it impossible for new defect shapes to sneak through
 * (whack-a-mole replaced by deny-by-default).
 *
 * Usage:
 *   cd web && node scripts/check-bare-var.mjs              # scan src/
 *   cd web && node scripts/check-bare-var.mjs --self-test   # verify detector
 */

import { readFileSync } from 'node:fs'
import { execSync } from 'node:child_process'
import { join, relative } from 'node:path'

// ---------------------------------------------------------------------------
// CLI args
// ---------------------------------------------------------------------------
const args = process.argv.slice(2)
const selfTest = args.includes('--self-test')

// ---------------------------------------------------------------------------
// Non-triplet token allowlist
// ---------------------------------------------------------------------------
// These tokens are declared as FULL CSS colors (rgb(...)) in index.css,
// NOT as bare channel triplets.  Bare var(--color-X) is therefore a
// valid <color> for them — no rgb() wrapper is needed.
//
// Proof: in tailwind.config.js these two are registered WITHOUT the
// a() wrapper (a = (v) => `rgb(var(${v}) / <alpha-value>)`) that every
// other token uses, confirming they are already valid colors.
//   – accent-subtle: tailwind.config.js line 30  → 'var(--color-accent-subtle)'
//   – overlay:       tailwind.config.js line 68  → 'var(--color-overlay)'
//
// index.css declarations:
//   --color-accent-subtle: rgb(232 62 140 / 0.08);   (line 35, light)
//   --color-overlay:       rgb(15 17 21 / 0.75);     (line 75, light)
// Both use rgb() with alpha — they are full colors, not triplets.

/** @type {Set<string>} */
const NON_TRIPLET_TOKENS = new Set([
  'accent-subtle', // index.css:35 — already rgb(…), not a triplet
  'overlay',       // index.css:75 — already rgb(…), not a triplet
])

// ---------------------------------------------------------------------------
// Comment stripping (line-number-safe: replaces with spaces, not empty)
// ---------------------------------------------------------------------------

/**
 * Strip C-style comments from a string, replacing with spaces so that
 * line numbers (from indexOf \\n) stay correct.
 *
 * @param {string} src - Source text.
 * @returns {string} Source with comments replaced by spaces.
 */
function stripComments(src) {
  // /* … */ (non-greedy, newline-safe)
  let out = src.replace(/\/\*[\s\S]*?\*\//g, (m) =>
    m.replace(/[^\n]/g, ' '),
  )
  // // … to end of line (preserve the trailing \n)
  out = out.replace(/\/\/[^\n]*/g, (m) => m.replace(/[^\n]/g, ' '))
  return out
}

// ---------------------------------------------------------------------------
// Line number from character offset
// ---------------------------------------------------------------------------

/**
 * Find the 1-based line number for a given character index.
 *
 * @param {string} src  - Full source text.
 * @param {number} idx  - Character index.
 * @returns {number}    - 1-based line number.
 */
function lineOf(src, idx) {
  let line = 1
  for (let i = 0; i < idx && i < src.length; i++) {
    if (src[i] === '\n') line++
  }
  return line
}

// ---------------------------------------------------------------------------
// Core scanner
// ---------------------------------------------------------------------------

/** CSS color functions that wrap channel-triplet tokens correctly. */
const COLOR_FUNCTIONS = ['rgb(', 'rgba(', 'hsl(', 'hsla(', 'lab(', 'lch(', 'color(']

/** Matches var(--color-<name>) with an optional fallback value. */
const VAR_COLOR_RE = /var\(--color-([a-zA-Z0-9-]+)(?:\s*,[^)]*)?\)/g

/**
 * Check if a var(--color-) match is inside a string argument to a test
 * assertion method (e.g. .toBe('var(--color-...)')).  These are legitimate
 * string comparisons in tests, NOT raw CSS color values.
 *
 * Precise exclusion: matches the pattern .<method>('...var(--color-...)')
 * where <method> is toBe, toEqual, toContain, toMatch, toHaveProperty,
 * or not followed by one of those.
 */
function isInsideAssertionString(stripped, matchIndex) {
  const before = stripped.substring(0, matchIndex)
  // Look for .<method>('<quote> immediately before the match
  // Pattern: optional ".not". ".<method>(" then a quote character
  const assertionRe = /\.((?:not\.)?(?:toBe|toEqual|toContain|toMatch|toHaveProperty))\s*\(\s*['"`]$/
  return assertionRe.test(before)
}

/**
 * Scan source text for bare var(--color-*) usages.
 *
 * Inverted-default strategy: flag every occurrence UNLESS it is in a
 * known-safe context (color-function wrapper, comment, test assertion,
 * or non-triplet allowlist).
 *
 * @param {string} src     - Raw source text.
 * @param {string} relPath - Relative file path for reporting.
 * @param {object} [opts]
 * @param {boolean} [opts.isTestFile=false] - Whether relPath is a test file
 *   (*.test.ts / *.test.tsx).  Assertion exclusion is scoped to test files
 *   only, so source code can never hide a real violation behind a .toBe().
 * @returns {Array<{relPath: string, line: number, raw: string}>}
 */
function findBareVars(src, relPath, opts = {}) {
  const stripped = stripComments(src)
  const violations = []
  const isTestFile = opts.isTestFile ?? false

  for (const m of stripped.matchAll(VAR_COLOR_RE)) {
    const tokenName = m[1] // capture group: the part after --color-

    // (d) Non-triplet token allowlist: bare usage is valid because the
    //     token is declared as a full CSS color, not a channel triplet.
    if (NON_TRIPLET_TOKENS.has(tokenName)) continue

    // (a) Wrapped in a color function (rgb, rgba, hsl, hsla, lab, lch, color)
    const before = stripped.substring(0, m.index).trimEnd()
    const wrapped = COLOR_FUNCTIONS.some((fn) => before.endsWith(fn))
    if (wrapped) continue

    // (c) String argument to a test assertion method — scoped to test files
    //     only.  Scoping by file kind is safe because:
    //       - Test files never define production CSS/SVG color values.
    //       - Source files should never contain .toBe('var(--color-…)') 
    //         unless they are doing something deeply wrong with the design
    //         token API.  If they do, we WANT to flag it.
    if (isTestFile && isInsideAssertionString(stripped, m.index)) continue

    const line = lineOf(src, m.index)
    const lineText = src.split('\n')[line - 1]?.trim() ?? ''
    violations.push({ relPath, line, raw: lineText })
  }
  return violations
}

// ---------------------------------------------------------------------------
// Self-test: verify the detector catches bare vars and allows wrapped ones
// ---------------------------------------------------------------------------
function runSelfTest() {
  let ok = true
  let passed = 0
  let total = 0

  // ── Test 1: bare var MUST be detected ──
  total++
  const bare = 'stroke="var(--color-text-on-accent)"'
  const bareHits = findBareVars(bare, 'test.tsx')
  if (bareHits.length === 1) {
    console.log(`  ✅ [1] bare var detected → ${bareHits[0].raw}`)
    passed++
  } else {
    console.error(
      `  ❌ [1] expected 1 violation for bare var, got ${bareHits.length}`,
    )
    ok = false
  }

  // ── Test 2: rgb()-wrapped var MUST pass ──
  total++
  const rgbWrapped = 'stroke="rgb(var(--color-text-on-accent))"'
  const rgbHits = findBareVars(rgbWrapped, 'test.tsx')
  if (rgbHits.length === 0) {
    console.log('  ✅ [2] rgb()-wrapped var correctly allowed')
    passed++
  } else {
    console.error(
      `  ❌ [2] expected 0 violations for rgb()-wrapped var, got ${rgbHits.length}`,
    )
    ok = false
  }

  // ── Test 3: rgba()-wrapped var MUST pass ──
  total++
  const rgbaSrc =
    'background: `rgba(var(--color-bg-primary) / 0.5)`'
  const rgbaHits = findBareVars(rgbaSrc, 'test.tsx')
  if (rgbaHits.length === 0) {
    console.log('  ✅ [3] rgba()-wrapped var correctly allowed')
    passed++
  } else {
    console.error(
      `  ❌ [3] expected 0 violations for rgba()-wrapped var, got ${rgbaHits.length}`,
    )
    ok = false
  }

  // ── Test 4: multiline template literal with rgb() MUST pass ──
  total++
  const multiSrc =
    'background: `linear-gradient(\n  rgb(var(--color-focus)) 0%\n)`'
  const multiHits = findBareVars(multiSrc, 'test.tsx')
  if (multiHits.length === 0) {
    console.log('  ✅ [4] multiline rgb()-wrapped var correctly allowed')
    passed++
  } else {
    console.error(
      `  ❌ [4] expected 0 violations for multiline rgb()-wrapped var, got ${multiHits.length}`,
    )
    ok = false
  }

  // ── Test 5: bare var inside template literal MUST be detected ──
  total++
  const bareTpl = 'style={{ color: `var(--color-text-primary)` }}'
  const bareTplHits = findBareVars(bareTpl, 'test.tsx')
  if (bareTplHits.length === 1) {
    console.log(
      `  ✅ [5] bare var in template literal detected → ${bareTplHits[0].raw}`,
    )
    passed++
  } else {
    console.error(
      `  ❌ [5] expected 1 violation for bare var in template, got ${bareTplHits.length}`,
    )
    ok = false
  }

  // ── Test 6: var inside a comment MUST be ignored ──
  total++
  const commentSrc =
    '// stroke="var(--color-text-on-accent)"\nconst x = 1'
  const commentHits = findBareVars(commentSrc, 'test.tsx')
  if (commentHits.length === 0) {
    console.log('  ✅ [6] bare var inside // comment correctly ignored')
    passed++
  } else {
    console.error(
      `  ❌ [6] expected 0 violations for var in comment, got ${commentHits.length}`,
    )
    ok = false
  }

  // ── Test 7: var inside block comment MUST be ignored ──
  total++
  const blockCommentSrc =
    '/* stroke="var(--color-text-on-accent)" */\nconst x = 1'
  const blockCommentHits = findBareVars(blockCommentSrc, 'test.tsx')
  if (blockCommentHits.length === 0) {
    console.log('  ✅ [7] bare var inside /* */ comment correctly ignored')
    passed++
  } else {
    console.error(
      `  ❌ [7] expected 0 violations for var in block comment, got ${blockCommentHits.length}`,
    )
    ok = false
  }

  // ── Test 8: string argument to .toBe() MUST be excluded (test file only) ──
  total++
  const toBeSrc =
    "expect(stroke).not.toBe('var(--color-text-on-accent)')"
  const toBeHits = findBareVars(toBeSrc, 'Icons.test.tsx', { isTestFile: true })
  if (toBeHits.length === 0) {
    console.log('  ✅ [8] bare var in .toBe() assertion correctly ignored (test file)')
    passed++
  } else {
    console.error(
      `  ❌ [8] expected 0 violations for var in .toBe(), got ${toBeHits.length}`,
    )
    ok = false
  }

  // ── Test 9: .toBe() in SOURCE file MUST still be flagged ──
  //    Assertion exclusion scoped to test files only — a .toBe() in a
  //    source file means something is wrong with the token API usage.
  total++
  const toBeInSrc =
    "expect(stroke).not.toBe('var(--color-text-on-accent)')"
  const toBeSrcHits = findBareVars(toBeInSrc, 'Icons.tsx', { isTestFile: false })
  if (toBeSrcHits.length === 1) {
    console.log('  ✅ [9] .toBe() in source file correctly flagged')
    passed++
  } else {
    console.error(
      `  ❌ [9] expected 1 violation for .toBe() in source file, got ${toBeSrcHits.length}`,
    )
    ok = false
  }

  // ── Test 10: quoted string in JS style object MUST be detected ──
  //    This is the ToggleSwitch shape that was a false negative before.
  total++
  const quotedStyle =
    "style={{ backgroundColor: checked ? 'var(--color-accent-primary)' : 'var(--color-bg-elevated)' }}"
  const quotedHits = findBareVars(quotedStyle, 'ToggleSwitch.tsx')
  if (quotedHits.length === 2) {
    console.log(`  ✅ [10] quoted string in JS style object detected (${quotedHits.length} violations)`)
    passed++
  } else {
    console.error(
      `  ❌ [10] expected 2 violations for quoted string in style object, got ${quotedHits.length}`,
    )
    ok = false
  }

  // ── Test 11: var(--color-x, #fff) with fallback MUST be detected ──
  //    A CSS fallback does not make a bare channel-triplet valid.
  //    The triplet still produces an invalid <color>; the fallback is
  //    only used when the var() itself resolves to nothing (invalid
  //    initial value), NOT when the resolved value is a non-color string.
  total++
  const withFallback = "color: var(--color-text-primary, #ffffff)"
  const fallbackHits = findBareVars(withFallback, 'test.tsx')
  if (fallbackHits.length === 1) {
    console.log(`  ✅ [11] var with CSS fallback correctly detected as violation`)
    passed++
  } else {
    console.error(
      `  ❌ [11] expected 1 violation for var with fallback, got ${fallbackHits.length}`,
    )
    ok = false
  }

  // ── Test 12: non-triplet token (--color-accent-subtle) MUST be allowed ──
  total++
  const subtleSrc = "color: var(--color-accent-subtle)"
  const subtleHits = findBareVars(subtleSrc, 'test.tsx')
  if (subtleHits.length === 0) {
    console.log('  ✅ [12] non-triplet token --color-accent-subtle correctly allowed')
    passed++
  } else {
    console.error(
      `  ❌ [12] expected 0 violations for --color-accent-subtle, got ${subtleHits.length}`,
    )
    ok = false
  }

  // ── Test 13: non-triplet token (--color-overlay) MUST be allowed ──
  total++
  const overlaySrc = "backgroundColor: var(--color-overlay)"
  const overlayHits = findBareVars(overlaySrc, 'test.tsx')
  if (overlayHits.length === 0) {
    console.log('  ✅ [13] non-triplet token --color-overlay correctly allowed')
    passed++
  } else {
    console.error(
      `  ❌ [13] expected 0 violations for --color-overlay, got ${overlayHits.length}`,
    )
    ok = false
  }

  console.log(`\nself-test: ${total} checks, ${passed} passed`)
  if (!ok) {
    console.error('\nself-test FAILED — detector is broken')
    process.exit(1)
  }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
function main() {
  if (selfTest) {
    console.log('── self-test: check-bare-var ──────────────────────')
    runSelfTest()
    return
  }

  const web = process.cwd()

  // Find all .ts/.tsx files under src/
  let files
  try {
    files = execSync(
      `find src \\( -name '*.tsx' -o -name '*.ts' \\) -type f`,
      { cwd: web, encoding: 'utf8' },
    )
      .trim()
      .split('\n')
      .filter(Boolean)
  } catch {
    console.error('ERROR: Failed to list source files. Run from web/.')
    process.exit(2)
  }

  const violations = []
  for (const file of files) {
    const absPath = join(web, file)
    let src
    try {
      src = readFileSync(absPath, 'utf8')
    } catch {
      continue
    }
    const relPath = relative(web, absPath)
    // Assertion exclusion is scoped to test files only (*.test.ts / *.test.tsx)
    // so that source code can never hide a real violation behind a .toBe().
    const isTestFile = /\.test\.(?:ts|tsx)$/.test(file)
    violations.push(...findBareVars(src, relPath, { isTestFile }))
  }

  if (violations.length === 0) {
    console.log(
      `\x1b[32m✓ check-bare-var: 0 violations in ${files.length} files\x1b[0m`,
    )
    process.exit(0)
  }

  // Failure report
  console.error(
    `\nBARE var(--color-*) VIOLATIONS: ${violations.length} found\n`,
  )
  for (const v of violations) {
    console.error(`  ${v.relPath}:${v.line}: ${v.raw}`)
  }
  console.error(
    `\n${violations.length} bare var(--color-*) found — ` +
    `wrap in rgb() or another color function, or add the token to NON_TRIPLET_TOKENS if it is declared as a full color in index.css.`,
  )
  process.exit(1)
}

main()