#!/usr/bin/env node
/**
 * check-active-pill.mjs — Gate for issue #443: selected-state recipes that fail AA.
 *
 * The contrast matrix (check-contrast.mjs) proves that a token PAIR is legible;
 * it cannot see which pair a component actually paints. `bg-surface-selected`
 * combined with `text-accent-primary` measures 2.40:1 in the dark theme
 * (AA normal text needs 4.5:1), so any element that takes the selected
 * background must use `text-accent-text` (6.60:1 dark / 5.66:1 light on the
 * page background, 4.51:1 dark / 5.20:1 light on the selected background).
 *
 * Scope: every className/class attribute under src/ (tests excluded). Tokens are
 * compared variant- and opacity-insensitive (`hover:text-accent-primary/70`
 * normalises to `text-accent-primary`), and only classes of the SAME element are
 * compared, so a text link and an unrelated selected row in the same file never
 * trip the gate.
 *
 * Usage:
 *   cd web && node scripts/check-active-pill.mjs
 *   cd web && node scripts/check-active-pill.mjs --self-test
 *
 * Exit 0 = clean, 1 = banned recipe found (or self-test failure), 2 = infra error.
 */

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

export const BANNED_RECIPES = [
  {
    id: 'selected-accent-primary',
    classes: ['bg-surface-selected', 'text-accent-primary'],
    replacement: 'text-accent-text',
    why: 'accent-primary on surface-selected is 2.40:1 in the dark theme (AA normal text needs 4.5:1)',
  },
]

const SOURCE_EXTENSIONS = ['.tsx', '.ts']
const SKIPPED_SOURCE = [/\.test\.tsx?$/]
const CLASS_ATTRIBUTE = /(?:className|class)\s*=\s*/g
const CLASS_TOKEN = /[a-z][a-z0-9-]*(?:\/[0-9]+)?/gi

export function normalizeClassToken(token) {
  const [base = token] = token.split('/')
  const parts = base.split(':')
  return (parts[parts.length - 1] ?? base).toLowerCase()
}

export function classTokensOf(attributeSource) {
  const tokens = new Set()
  for (const match of attributeSource.matchAll(CLASS_TOKEN)) {
    tokens.add(normalizeClassToken(match[0]))
  }
  return tokens
}

export function bannedRecipeOf(attributeSource, recipes = BANNED_RECIPES) {
  const tokens = classTokensOf(attributeSource)
  return recipes.find((recipe) => recipe.classes.every((cls) => tokens.has(cls)))
}

function lineOf(source, index) {
  let line = 1
  for (let i = 0; i < index && i < source.length; i++) {
    if (source[i] === '\n') line++
  }
  return line
}

/**
 * Read the value of a className attribute starting right after its `=`.
 * Handles "…" / '…' / `…` literals and {…} expressions (nested braces and
 * strings inside the expression are respected).
 */
export function readAttributeExpression(source, start) {
  let i = start
  while (i < source.length && /\s/.test(source[i])) i++
  const opener = source[i]
  if (opener === '"' || opener === "'" || opener === '`') {
    const end = source.indexOf(opener, i + 1)
    const stop = end === -1 ? source.length : end
    return { text: source.slice(i, stop + 1), end: stop + 1 }
  }
  if (opener !== '{') {
    return { text: '', end: i }
  }
  let depth = 0
  let quote = null
  for (let j = i; j < source.length; j++) {
    const ch = source[j]
    if (quote) {
      if (ch === '\\') {
        j++
        continue
      }
      if (ch === quote) quote = null
      continue
    }
    if (ch === '"' || ch === "'" || ch === '`') {
      quote = ch
      continue
    }
    if (ch === '{') depth++
    if (ch === '}') {
      depth--
      if (depth === 0) return { text: source.slice(i, j + 1), end: j + 1 }
    }
  }
  return { text: source.slice(i), end: source.length }
}

export function scanSource(source, filePath, recipes = BANNED_RECIPES) {
  const violations = []
  for (const match of source.matchAll(CLASS_ATTRIBUTE)) {
    const start = (match.index ?? 0) + match[0].length
    const { text } = readAttributeExpression(source, start)
    const recipe = bannedRecipeOf(text, recipes)
    if (!recipe) continue
    violations.push({
      file: filePath,
      line: lineOf(source, match.index ?? 0),
      recipe: recipe.id,
      replacement: recipe.replacement,
      why: recipe.why,
      snippet: text.replace(/\s+/g, ' ').slice(0, 160),
    })
  }
  return violations
}

export function collectSourceFiles(root) {
  const files = []
  const walk = (dir) => {
    for (const entry of readdirSync(dir).sort()) {
      const full = join(dir, entry)
      if (statSync(full).isDirectory()) {
        walk(full)
        continue
      }
      if (!SOURCE_EXTENSIONS.some((ext) => entry.endsWith(ext))) continue
      if (SKIPPED_SOURCE.some((re) => re.test(entry))) continue
      files.push(full)
    }
  }
  walk(root)
  return files
}

export function scanTree(srcRoot, recipes = BANNED_RECIPES) {
  const files = collectSourceFiles(srcRoot)
  const violations = []
  for (const file of files) {
    violations.push(...scanSource(readFileSync(file, 'utf8'), file, recipes))
  }
  return { files: files.length, violations }
}

function selfTest(recipes = BANNED_RECIPES) {
  const cases = [
    {
      name: 'flags the exact recipe',
      source: '<button className="border-accent-primary/30 bg-surface-selected text-accent-primary" />',
      expected: 1,
    },
    {
      name: 'flags the recipe regardless of class order',
      source: '<button className="text-accent-primary bg-surface-selected" />',
      expected: 1,
    },
    {
      name: 'flags a variant/opacity form of the recipe',
      source: '<button className="bg-surface-selected hover:text-accent-primary/70" />',
      expected: 1,
    },
    {
      name: 'flags a recipe split across a conditional inside one className',
      source:
        "<button className={`flex ${active ? 'bg-surface-selected' : ''} ${active ? 'text-accent-primary' : 'text-text-secondary'}`} />",
      expected: 1,
    },
    {
      name: 'accepts the accessible replacement',
      source: '<button className="border-accent-primary/30 bg-surface-selected text-accent-text" />',
      expected: 0,
    },
    {
      name: 'accepts accent-primary text without the selected background',
      source: '<button className="text-xs text-accent-primary hover:underline" />',
      expected: 0,
    },
    {
      name: 'accepts the two classes on different elements of one file',
      source:
        '<div className="bg-surface-selected">\n  <button className="text-accent-primary" />\n</div>',
      expected: 0,
    },
  ]

  let failed = 0
  console.log('── self-test ──────────────────────────────────────')
  for (const testCase of cases) {
    const found = scanSource(testCase.source, 'fixture.tsx', recipes).length
    const ok = found === testCase.expected
    if (!ok) failed++
    console.log(`  [${ok ? 'PASS' : 'FAIL'}] ${testCase.name}: found ${found}, expected ${testCase.expected}`)
  }
  return failed === 0
}

function main() {
  const args = process.argv.slice(2)
  if (args.includes('--self-test') && !selfTest()) {
    console.error('\nself-test FAILED — exiting 1')
    process.exit(1)
  }

  let result
  try {
    result = scanTree(join(process.cwd(), 'src'))
  } catch (err) {
    console.error(`ERROR: cannot scan src/: ${err.message}`)
    process.exit(2)
  }

  console.log('\n═══ active-pill recipe gate ═══')
  console.log(`files scanned: ${result.files}`)
  console.log(`banned recipes: ${BANNED_RECIPES.map((r) => r.classes.join(' + ')).join(', ')}`)
  console.log(`violations: ${result.violations.length}`)

  if (result.violations.length === 0) {
    console.log('✅ no selected-state pill/chip paints the low-contrast accent-primary token')
    return
  }

  console.error('\nVIOLATIONS:')
  for (const violation of result.violations) {
    console.error(`  ❌ ${violation.file}:${violation.line}`)
    console.error(`     ${violation.snippet}`)
    console.error(`     ${violation.why}`)
    console.error(`     use ${violation.replacement} instead`)
  }
  process.exit(1)
}

main()