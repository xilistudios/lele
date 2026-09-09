/**
 * Model-name display helpers (spec §3.8).
 *
 * Single source of truth for the "short" form of a model identifier used in
 * the agents views (card meta line, page header, inheritance badges, the
 * SearchableSelect trigger button).
 *
 * The logic previously lived as a private `formatDisplayValue()` inside
 * `components/molecules/SearchableSelect.tsx`. It was moved here VERBATIM
 * (same rules, same provider list) and re-exported from that component, so
 * both consumers share one implementation and the copy cannot drift.
 */

/** Provider prefixes that appear before a `.` in model ids (e.g. `anthropic.claude-3`). */
const PROVIDER_PREFIXES = ['openai', 'anthropic', 'google', 'cohere', 'mistral', 'meta']

/**
 * Reduce a model identifier to its display name.
 *
 * - `openrouter/anthropic/claude-sonnet-4` -> `claude-sonnet-4`
 * - `anthropic.claude-3-5-sonnet`          -> `claude-3-5-sonnet`
 * - `gpt-4o`                               -> `gpt-4o` (unchanged)
 * - `''` / undefined                       -> `''`
 *
 * Never truncates in the middle with an ellipsis: callers that need to fit a
 * fixed width apply CSS `truncate` and keep the full value in `title`.
 */
export function shortModelName(model: string): string {
  if (!model) return model
  const slashParts = model.split('/')
  let name = slashParts[slashParts.length - 1]

  // Clean up common provider prefixes separated by dots (e.g. anthropic.claude...)
  const dotParts = name.split('.')
  if (dotParts.length > 1 && PROVIDER_PREFIXES.includes(dotParts[0].toLowerCase())) {
    name = dotParts.slice(1).join('.')
  }
  return name
}
