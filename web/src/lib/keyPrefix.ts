/**
 * API-key prefix detection for the "Add provider" flow (spec §2.4).
 *
 * This is a *preselection hint*, nothing more: pasting a key may highlight
 * the matching provider tile in the wizard, but it never submits anything
 * and never overrides a tile the user clicked manually. A wrong guess here
 * costs the user one extra click, so the table only ships prefixes that
 * unambiguously identify one provider.
 *
 * A bare `sk-` is deliberately absent from the table: DeepSeek, Moonshot
 * and dozens of OpenAI-compatible clones all share that shape, so generic
 * `sk-...` keys map to `null` by construction (no row can match them).
 *
 * Matching is case-SENSITIVE (`AIza` and `gsk_` are only distinctive in
 * their real casing; a case-insensitive match would invent detections) and
 * first match wins, so the table is ordered from the most specific prefix
 * to the least (`sk-or-v1-` must precede `sk-or-`).
 */

/** Ordered table: first match wins. Longest/most-specific prefixes first. */
export const KEY_PREFIXES: ReadonlyArray<{ prefix: string; type: string }> = [
  { prefix: 'sk-ant-', type: 'anthropic' },
  { prefix: 'sk-or-v1-', type: 'openrouter' },
  { prefix: 'sk-or-', type: 'openrouter' },
  { prefix: 'sk-proj-', type: 'openai' },
  { prefix: 'sk-svcacct-', type: 'openai' },
  { prefix: 'gsk_', type: 'groq' },
  { prefix: 'AIza', type: 'gemini' },
  { prefix: 'xai-', type: 'xai' },
  { prefix: 'nvapi-', type: 'nvidia' },
  { prefix: 'pplx-', type: 'perplexity' },
]

/**
 * Return the provider type hinted by `key`, or `null` when no unambiguous
 * prefix matches.
 *
 * - Both ends are trimmed first (users paste keys with a trailing newline).
 * - Pasting a whole `export ANTHROPIC_API_KEY=sk-ant-...` line is NOT
 *   supported: after trimming it does not start with a known prefix, so it
 *   returns `null` — the safe direction.
 * - Callers holding a `SecretValue` must pass `value ?? ''`; `null`/`undefined`
 *   is not handled here.
 */
export function detectProviderFromKey(key: string): string | null {
  const trimmed = key.trim()
  for (const { prefix, type } of KEY_PREFIXES) {
    if (trimmed.startsWith(prefix)) return type
  }
  return null
}
