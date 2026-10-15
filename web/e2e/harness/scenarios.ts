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

/**
 * Gate scenario R8/R9 and the end-to-end pin in chatPipelineOrdering.test.ts.
 *
 * A thinking step (or the tool-call iteration of a model that emits no text) is
 * persisted as its own assistant message with `content === ''` and its payload
 * in `reasoning_content` (pkg/agent/llm_runner.go `iterationMsgID`), so an
 * assistant bubble CAN legitimately be text-less. The confirmation match used a
 * content-prefix test and an empty prefix can never match, so such a live copy
 * was never stripped from streaming state; once the sliding window slid past its
 * twin, `mergeMessages` appended it at the END — a block of old "Thinking…"
 * widgets below the newest answer, until a page reload cleared streaming state.
 */
const reasoningIteration: Scenario = {
  name: 'reasoning-iteration',
  base: [
    ...saturatedBase(1, 24),
    msg('b_q25', 'user', 'question number 25'),
    { ...msg('b_i25', 'assistant', ''), reasoningContent: 'thinking about the tool call' },
    msg('b_a25', 'assistant', 'answer number 25'),
  ],
  streaming: [
    // A stale live copy whose own twin slid out of the window, plus the live
    // copies of the current turn (identical reasoning text, one base copy).
    { ...msg('s_stale', 'assistant', ''), reasoningContent: 'thinking about the tool call' },
    { ...msg('s_i25', 'assistant', ''), reasoningContent: 'thinking about the tool call' },
    doneA(25),
    optQ(26, 'b_q25'),
  ],
}

export const scenarios: Scenario[] = [saturatedWindow, reasoningIteration]
