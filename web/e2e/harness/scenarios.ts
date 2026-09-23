import type { ChatMessage } from '../../src/lib/types'

export type Scenario = {
  name: string
  base: ChatMessage[]
  streaming: ChatMessage[]
}

const SESSION_KEY = 'harness-session'
const NOW = '2026-01-01T00:00:00.000Z'

/** Minimal ChatMessage carrying the full required shape (see src/lib/types.ts). */
function msg(id: string, role: ChatMessage['role'], content: string): ChatMessage {
  return { id, role, content, streaming: false, createdAt: NOW, sessionKey: SESSION_KEY }
}

/** Alternating user/assistant window: `question/answer number n` for n in [from, to]. */
function saturatedBase(from: number, to: number): ChatMessage[] {
  const msgs: ChatMessage[] = []
  for (let n = from; n <= to; n++) {
    msgs.push(msg(`b_q${n}`, 'user', `question number ${n}`))
    msgs.push(msg(`b_a${n}`, 'assistant', `answer number ${n}`))
  }
  return msgs
}

/** Optimistic (pending) user send, snapshotted against the saturated window (count = 25). */
const optQ = (n: number, anchorId: string): ChatMessage => ({
  ...msg(`s_q${n}`, 'user', `question number ${n}`),
  optimistic: true,
  optimisticBaseCount: 25,
  optimisticAnchorId: anchorId,
})

/** Completed assistant answer arriving over the WebSocket. */
const doneA = (n: number): ChatMessage => msg(`s_a${n}`, 'assistant', `answer number ${n}`)

/**
 * Gate scenario R2 (src/hooks/messageOrderingWindow.test.ts).
 *
 * The 50-message sliding window (rest_chat.go) has saturated at 25
 * non-optimistic users, so the legacy count rule (`25 > 25`) can never
 * confirm the three optimistic sends — without the anchor rule they become
 * immortal and mergeMessages strands them at the END of the list, after
 * newer answers ("messages lose their order until I refresh the page").
 */
const saturatedWindow: Scenario = {
  name: 'saturated-window',
  base: saturatedBase(4, 28),
  streaming: [
    optQ(26, 'b_q25'),
    doneA(26),
    optQ(27, 'b_q26'),
    doneA(27),
    optQ(28, 'b_q27'),
    doneA(28),
  ],
}

export const scenarios: Scenario[] = [saturatedWindow]
