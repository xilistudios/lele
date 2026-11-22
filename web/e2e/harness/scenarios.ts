import type { ChatMessage } from '../../src/lib/types'

export type Scenario = {
  name: string
  base: ChatMessage[]
  streaming: ChatMessage[]
}

const SESSION_KEY = 'harness-session'
const NOW = '2026-01-01T00:00:00.000Z'

function msg(id: string, role: ChatMessage['role'], content: string): ChatMessage {
  return { id, role, content, streaming: false, createdAt: NOW, sessionKey: SESSION_KEY }
}

function saturatedBase(from: number, to: number): ChatMessage[] {
  const msgs: ChatMessage[] = []
  for (let n = from; n <= to; n++) {
    msgs.push(msg(`b_q${n}`, 'user', `question number ${n}`))
    msgs.push(msg(`b_a${n}`, 'assistant', `answer number ${n}`))
  }
  return msgs
}

const optQ = (n: number, anchorId: string): ChatMessage => ({
  ...msg(`s_q${n}`, 'user', `question number ${n}`),
  optimistic: true,
  optimisticBaseCount: 25,
  optimisticAnchorId: anchorId,
})

const doneA = (n: number): ChatMessage => msg(`s_a${n}`, 'assistant', `answer number ${n}`)

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

const reasoningIteration: Scenario = {
  name: 'reasoning-iteration',
  base: [
    ...saturatedBase(1, 24),
    msg('b_q25', 'user', 'question number 25'),
    { ...msg('b_i25', 'assistant', ''), reasoningContent: 'thinking about the tool call' },
    msg('b_a25', 'assistant', 'answer number 25'),
  ],
  streaming: [
    { ...msg('s_stale', 'assistant', ''), reasoningContent: 'thinking about the tool call' },
    { ...msg('s_i25', 'assistant', ''), reasoningContent: 'thinking about the tool call' },
    doneA(25),
    optQ(26, 'b_q25'),
  ],
}

export const scenarios: Scenario[] = [saturatedWindow, reasoningIteration]
