/**
 * Identity of an assistant message for content-based matching — the ONE rule
 * shared by the three places that must agree on it:
 *
 *   - `event-handlers/streaming.ts` (`isConfirmedInCache`): decides whether the
 *     live copy already has a persisted twin, so it can be stripped.
 *   - `messageMerge.ts`: decides whether a live copy and a base copy are the
 *     same logical message (pairing / dedup).
 *   - `lib/chatMessageBuilder.ts`: decides the content the durable stableId
 *     registry is keyed on, so the persisted copy re-attaches the live React
 *     key instead of remounting.
 *
 * An assistant iteration with no text carries its payload in
 * `reasoning_content`: a thinking step, or the tool-call step of any model that
 * does not emit text alongside the call. The backend persists each iteration as
 * its own assistant message (pkg/agent/llm_runner.go `iterationMsgID`), and
 * `shouldAddAssistant` keeps such a message even when `content === ''`.
 *
 * WHY it matters: keying those messages on `content` alone made them
 * unmatchable (`''` never has a non-empty prefix). Live copies were never
 * stripped, accumulated one per tool/thinking iteration, and once the
 * 50-message sliding history window (pkg/channels/rest_chat.go,
 * `resultStartIdx := endIdx - limit`) slid past their twin, `mergeMessages`
 * could no longer pair them and appended them at the END of the list — after
 * newer answers. A page reload clears streaming state, which is why refreshing
 * showed the correct order again.
 *
 * NOTE (known limitation, pre-existing for `content` and inherited here): the
 * match is a 200-char prefix test, so when two iterations of the same turn carry
 * byte-identical reasoning text, a live copy can match an older persisted twin
 * and be stripped before its own copy is in the HTTP cache. The message
 * re-appears on the refetch that `history.updated` triggers (and on the 4s
 * poll), in its correct slot — strictly better than the permanent misordering
 * this identity rule fixes.
 */
export function assistantIdentityText(
  content: string | undefined,
  reasoningContent?: string,
): string {
  // `content` is always sent for history messages (pkg/channels/types.go —
  // `json:"content"`, no `omitempty`) and `applyMessageComplete` normalizes an
  // absent content to '', so this is belt-and-braces for the other payloads
  // that reach these call sites (WS catchup, group snapshots) rather than a
  // load-bearing assumption.
  const text = content ?? ''
  return text.trim() === '' ? (reasoningContent ?? '') : text
}
