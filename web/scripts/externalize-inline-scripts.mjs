// Post-build step for WEB-M13: Farm emits two inline <script> bootstrap
// blocks in dist/index.html (module system + resource manifest). Inline
// scripts force the CSP to keep 'unsafe-inline' in script-src, which is the
// XSS kill switch (see web/src/lib/markdown.ts history, WEB-M12). This script
// externalizes every inline block into ./dist as a hashed file and rewrites
// the HTML to reference it, so pkg/security can serve script-src 'self'.
//
// Idempotent: a second run finds no inline blocks and exits clean.
import { readFile, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const dist = join(dirname(fileURLToPath(import.meta.url)), "..", "dist");
const htmlPath = join(dist, "index.html");

const html = await readFile(htmlPath, "utf8");
const inline = [...html.matchAll(/<script(?![^>]*\bsrc=)([^>]*)>([\s\S]*?)<\/script>/g)];

if (inline.length === 0) {
  console.log("[externalize-inline-scripts] no inline scripts — ok");
  process.exit(0);
}

let out = html;
for (const [block, attrs, body] of inline) {
  if (!body.trim()) continue; // empty placeholder, leave as-is
  const hash = createHash("sha256").update(body).digest("hex").slice(0, 8);
  const file = `inline.${hash}.js`;
  await writeFile(join(dist, file), body, "utf8");
  const typeAttr = /\btype=/.test(attrs) ? attrs : "";
  out = out.replace(block, `<script${typeAttr} src="/${file}"></script>`);
  console.log(`[externalize-inline-scripts] ${file}`);
}

await writeFile(htmlPath, out, "utf8");
console.log(`[externalize-inline-scripts] rewrote index.html (${inline.length} blocks)`);
