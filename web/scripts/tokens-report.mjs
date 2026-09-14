#!/usr/bin/env node
/**
 * F1-§7.1 gate — dead token detector.
 *
 * Every `--color-*` custom property declared in src/styles/index.css must be
 * consumed somewhere, otherwise it is dead palette weight (the F1b problem).
 *
 * A token counts as consumed if either:
 *   1. `var(--color-…)` appears in any source file (index.css, tsx, config), or
 *   2. a Tailwind utility derived from its place in tailwind.config.js
 *      (`<prefix>-<keyPath>`, e.g. background.elevated -> bg-background-elevated)
 *      appears in the source.
 *
 * Exit 1 on any dead token.
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const ROOT = new URL("..", import.meta.url).pathname;
const CSS = join(ROOT, "src/styles/index.css");
const TW = join(ROOT, "tailwind.config.js");

const cssSrc = readFileSync(CSS, "utf8");
const declared = new Map(); // --color-name -> line number
cssSrc.split("\n").forEach((line, i) => {
  const m = line.match(/^\s*(--color-[a-z0-9-]+)\s*:/);
  if (m && !declared.has(m[1])) declared.set(m[1], i + 1);
});

function walk(dir, out = []) {
  for (const e of readdirSync(dir)) {
    if (e === "node_modules" || e.startsWith(".")) continue;
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(tsx?|jsx?|html|md)$/.test(e)) out.push(p);
  }
  return out;
}

const sources = walk(join(ROOT, "src"));
const corpus =
  sources.map((f) => readFileSync(f, "utf8")).join("\n") +
  "\n" +
  cssSrc +
  "\n" +
  readFileSync(TW, "utf8");

// token -> Set of utility key-paths that reference it, from the config tree.
const config = (await import(pathToFileURL(TW).href)).default;
const tokenPaths = new Map();
function visit(node, path) {
  if (typeof node === "string") {
    const m = node.match(/--color-[a-z0-9-]+/);
    if (m && path.length) {
      if (!tokenPaths.has(m[0])) tokenPaths.set(m[0], new Set());
      tokenPaths.get(m[0]).add(path.join("-"));
    }
    return;
  }
  if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node)) visit(v, k === "DEFAULT" ? path : [...path, k]);
  }
}
visit(config?.theme?.extend?.colors ?? {}, []);

const PREFIXES = [
  "bg", "text", "border", "ring", "from", "via", "to", "fill", "stroke",
  "shadow", "outline", "divide", "placeholder", "decoration", "caret", "accent",
];

const dead = [];
for (const [name, line] of declared) {
  if (corpus.includes(`var(${name})`)) continue;
  const paths = tokenPaths.get(name);
  const used =
    paths &&
    [...paths].some((p) =>
      PREFIXES.some((prefix) =>
        new RegExp(`\\b${prefix}-${p.replace(/-/g, "-")}(?![a-z0-9-])`).test(corpus),
      ),
    );
  if (!used) dead.push({ name, line, paths: paths ? [...paths].join(", ") : "(not in config)" });
}

if (dead.length) {
  console.error(`tokens-report: ${dead.length} dead --color-* token(s):`);
  for (const d of dead) console.error(`  ${d.name} (index.css:${d.line}) paths: ${d.paths}`);
  process.exit(1);
}
console.log(`tokens-report: ${declared.size} declared tokens, all consumed ✅`);
