import { mergeMessages } from '../../src/hooks/messageMerge'
import { type Scenario, scenarios } from './scenarios'

declare global {
  interface Window {
    __leleOrdering?: { scenarios: string[] }
  }
}

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
    bubble.setAttribute('data-message-id', message.stableId ?? message.id)
    bubble.setAttribute('data-has-thinking', message.reasoningContent ? 'true' : 'false')

    const text = document.createElement('span')
    text.setAttribute('data-testid', 'message-text')
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

window.__leleOrdering = { scenarios: scenarios.map((scenario) => scenario.name) }
