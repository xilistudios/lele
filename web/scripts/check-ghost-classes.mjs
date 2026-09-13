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

import { readdirSync, readFileSync, existsSync } from "node:fs";
import { execSync } from "node:child_process";
import { join } from "node:path";

// ---------------------------------------------------------------------------
// Design-token families (not native Tailwind palette)
// ---------------------------------------------------------------------------
const FAMILIES = [
  "background", "surface", "accent", "cta", "state", "brand", "overlay",
  "glass", "interaction", "mode", "secondary", "text", "border", "focus",
];

// Tailwind color utilities
const UTILITIES = [
  "bg", "text", "border", "ring", "shadow", "outline", "divide", "fill",
  "stroke", "decoration", "from", "via", "to", "placeholder", "accent", "caret",
];

// Pre-compute "bg-surface", "text-accent", …  (utility + "-" + family)
const FAM_PREFIX = new Set(
  UTILITIES.flatMap((u) => FAMILIES.map((f) => `${u}-${f}`)),
);

/**
 * Generic Tailwind unescape: `\:` → `:`, `\/` → `/`, etc.
 *   re.sub(r'\\(.)', r'\1', s)  in Python.
 */
function unescape(s) {
  return s.replace(/\\(.)/g, "$1");
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
function main() {
  const web = process.cwd();
  if (!existsSync(join(web, "tailwind.config.js"))) {
    console.error("ERROR: run from web/ (expect tailwind.config.js).");
    process.exit(2);
  }

  // ── 1. Load dist/*.css ──────────────────────────────────────────────
  const cssFiles = readdirSync(join(web, "dist"))
    .filter((f) => f.endsWith(".css"))
    .sort()
    .map((f) => join(web, "dist", f));
  if (!cssFiles.length) {
    console.error("No dist/*.css found. Build first.");
    process.exit(2);
  }

  const css = cssFiles.map((p) => readFileSync(p, "utf8")).join("\n");
  console.log(
    `CSS auditado: ${cssFiles.map((p) => p.split("/").pop()).join(", ")} (${css.length} bytes)`,
  );

  // Set of selectors present in CSS, unescaped, and their bases.
  const rawSet = new Set();
  const selectorRe = /\.((?:[a-zA-Z0-9_-]|\\.)+)/g;
  let m;
  while ((m = selectorRe.exec(css)) !== null) {
    rawSet.add(m[1]);
  }
  const present = new Set([...rawSet].map(unescape));
  const presentBase = new Set([...present].map((c) => c.split(":").pop()));

  // ── 2. Scan src/*.{ts,tsx} for color utility classes ────────────────
  // Variant prefixes: hover:, group-hover:, data-[...]:, lg:, etc.
  // Captures: (optional-variants)(utility)(-family-rest)(optional/opacity)
  const classRe =
    /(?:(?::[a-z-]+)*(?:bg|text|border|ring|shadow|outline|divide|fill|stroke|decoration|from|via|to|placeholder|accent|caret)-[a-z0-9-]+(?:\/[0-9]+)?)/g;

  let rawMatches;
  try {
    rawMatches = execSync(
      `grep -roPh '${classRe.source}' src --include='*.tsx' --include='*.ts'`,
      { cwd: web, encoding: "utf8", maxBuffer: 20 * 1024 * 1024 },
    )
      .split("\n")
      .filter(Boolean);
  } catch {
    rawMatches = [];
  }

  // Normalise ":bg-x" (variant glued to class) → "bg-x"
  const cnt = new Map();
  for (const raw of rawMatches) {
    const c = raw.replace(/^:+/, "");
    cnt.set(c, (cnt.get(c) || 0) + 1);
  }

  // ── 3. Filter to design-token families, detect dead ─────────────────
  const used = new Map();
  const dead = new Map();
  for (const [c, n] of cnt) {
    const base = c.split(":").pop();
    if (!isDesignToken(base)) continue;
    used.set(c, (used.get(c) || 0) + n);
    if (!present.has(c) && !presentBase.has(base)) {
      dead.set(c, (dead.get(c) || 0) + n);
    }
  }

  // Count affected source files
  let nFiles = "0";
  if (dead.size > 0) {
    try {
      const pattern = [...dead.keys()].map(escapeRegex).join("|");
      nFiles = execSync(
        `grep -rlP '${pattern}' src --include='*.tsx' --include='*.ts' | wc -l`,
        { cwd: web, encoding: "utf8" },
      ).trim();
    } catch {
      nFiles = "0";
    }
  }

  const totalUsed = [...used.values()].reduce((a, b) => a + b, 0);
  const totalDead = [...dead.values()].reduce((a, b) => a + b, 0);
  const pct = totalUsed > 0 ? (100 * totalDead) / totalUsed : 0;

  console.log();
  console.log(`clases de token distinct en src : ${used.size}`);
  console.log(`ocurrencias totales             : ${totalUsed}`);
  console.log(`CLASES MUERTAS (distinct)       : ${dead.size}`);
  console.log(`ocurrencias muertas             : ${totalDead}`);
  console.log(`archivos afectados              : ${nFiles}`);
  console.log(`→ ${pct.toFixed(1)}% del markup de color no emite CSS`);

  if (dead.size > 0) {
    console.log("\n--- detalle (por ocurrencias) ---");
    [...dead.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .forEach(([d, n]) => {
        console.log(`  ${d.padEnd(38)} ${n}`);
      });
  }

  console.log(
    "\nGate §1.1 del spec: dead debe ser 0 tras aplicar <alpha-value>.",
  );
  process.exit(dead.size > 0 ? 1 : 0);
}

/** Check if a class base starts with a design-token utility-family prefix. */
function isDesignToken(base) {
  // e.g. "bg-accent-primary/30" → check first two segments after split
  // We need "bg-accent" to match.  The base may be "bg-accent-primary/30".
  // Split on "-" and check "utility-family" up to the second hyphen.
  const parts = base.split("/");
  const noOpacity = parts[0]; // "bg-accent-primary"
  const segs = noOpacity.split("-");
  if (segs.length < 2) return false;
  const prefix = `${segs[0]}-${segs[1]}`;
  return FAM_PREFIX.has(prefix);
}

/** Escape a string for use inside a PCRE/grep regex. */
function escapeRegex(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

main();
