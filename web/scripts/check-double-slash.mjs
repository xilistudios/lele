#!/usr/bin/env node
/**
 * check-double-slash.mjs — Gate F0: no double-slash alpha regression.
 *
 * Checks two things:
 *  (a) dist/*.css bundle — no  rgb(var(--color-X) ... / ... / ...)  with double slash.
 *  (b) src/styles/index.css — no  rgb(var(--color-X))  where X is an alpha-fija var
 *      (those must be raw  var(--color-X)  so they carry their own alpha).
 *
 * Port of web/scripts/_f0-check-double-slash.py.
 *
 * Usage:
 *   cd web && node scripts/check-double-slash.mjs
 */

import { readFileSync, readdirSync, existsSync } from "node:fs";
import { join } from "node:path";

// The 14 alpha-fija vars whose CSS values already include alpha.
// They must NOT be wrapped in rgb().
const ALPHA_FIJA = new Set([
  "color-surface-selected",
  "color-accent-muted",
  "color-accent-subtle",
  "color-border-default",
  "color-border-light",
  "color-border-strong",
  "color-state-success-light",
  "color-state-warning-light",
  "color-state-error-light",
  "color-state-info-light",
  "color-overlay",
  "color-overlay-light",
  "color-glass",
  "color-glass-border",
]);

const ALPHAFIJA_PAT = new RegExp(
  "rgb(?:a)?\\(var\\(--(" +
    [...ALPHA_FIJA].sort().join("|") +
    ")\\)",
);

function main() {
  const web = process.cwd();
  const errors = [];

  // ── (a) Check index.css ────────────────────────────────────────────
  const indexCssPath = join(web, "src", "styles", "index.css");
  if (existsSync(indexCssPath)) {
    const cssSrc = readFileSync(indexCssPath, "utf8");
    for (const m of cssSrc.matchAll(new RegExp(ALPHAFIJA_PAT, "g"))) {
      errors.push(`  index.css: rgb(var(--${m[1]})) found`);
    }
  }

  // ── (b) Check dist/*.css bundle ────────────────────────────────────
  const distDir = join(web, "dist");
  if (existsSync(distDir)) {
    for (const fname of readdirSync(distDir).filter((f) => f.endsWith(".css"))) {
      const bundle = readFileSync(join(distDir, fname), "utf8");
      for (const m of bundle.matchAll(new RegExp(ALPHAFIJA_PAT, "g"))) {
        errors.push(`  ${fname}: rgb(var(--${m[1]})) found`);
      }
    }
  }

  // ── Report ─────────────────────────────────────────────────────────
  if (errors.length > 0) {
    console.log(`DOUBLE-SLASH RISK: ${errors.length} violations found:`);
    for (const e of errors) console.log(e);
    process.exit(1);
  }

  console.log(
    "OK: 0 double-slash violations (alpha-fija vars are NOT wrapped in rgb())",
  );
  process.exit(0);
}

main();
