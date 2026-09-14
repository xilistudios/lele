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

// Alpha-fija vars (§7.4): su valor CSS ya incluye alfa — NO van envueltas en rgb().
// Actualizada tras F1c/F1e/F1f: border-*/surface-selected/state-*-light ahora son
// tripletes (van en rgb()); glass eliminado (F1f). Solo quedan estos dos casos.
const ALPHA_FIJA = new Set(["color-accent-subtle", "color-overlay"]);

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
