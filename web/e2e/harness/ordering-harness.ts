/**
 * Message-ordering harness bundle entry.
 *
 * Renders the output of the REAL `mergeMessages` (src/hooks/messageMerge.ts —
 * never a reimplementation) into a real DOM using the app's exact bubble
 * DOM contract:
 *
 *   - MessageList.tsx  → wrapper `data-testid="message-list"`
 *   - MessageBubble.tsx → bubble roots carry `data-testid="message"`,
 *     `data-role`, and `data-message-id={message.stableId ?? message.id}`
 *   - render key (React/Virtuoso) = `m.stableId ?? m.id` — reproduced verbatim.
 *
 * Built to `./dist/harness.js` by e2e/global-setup.ts before every run.
 */
import { mergeMessages } from '../../src/hooks/messageMerge'
import { type Scenario, scenarios } from './scenarios'

declare global {
  interface Window {
    __leleOrdering?: { scenarios: string[] }
  }
}

/** Merge one scenario and emit its section + bubble list in merged order. */
function renderScenario(scenario: Scenario): HTMLElement {
  const merged = mergeMessages(scenario.base, scenario.streaming)

  const section = document.createElement('section')
  section.setAttribute('data-testid', 'scenario')
  section.setAttribute('data-scenario', scenario.name)

  const list = document.createElement('div')
  list.setAttribute('data-testid', 'message-list')

  for (const message of merged) {
    const bubble = document.createElement('div')
    bubble.setAttribute('data-testid', 'message')
    bubble.setAttribute('data-role', message.role)
    // Same expression as MessageBubble/MessageList — the real React key contract.
    bubble.setAttribute('data-message-id', message.stableId ?? message.id)

    const text = document.createElement('span')
    text.setAttribute('data-testid', 'message-text')
    // Message content is DATA — textContent only, never innerHTML.
    text.textContent = message.content
    bubble.appendChild(text)
    list.appendChild(bubble)
  }

  section.appendChild(list)
  return section
}

const root = document.getElementById('root')
if (root) {
  for (const scenario of scenarios) {
    root.appendChild(renderScenario(scenario))
  }
}

// Boot marker so the spec can assert the harness rendered (not a blank page).
window.__leleOrdering = { scenarios: scenarios.map((scenario) => scenario.name) }
